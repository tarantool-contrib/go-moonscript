package lua

import (
	"fmt"
	"math"
	"reflect"

	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/ast"
)

/* internal constants & structs  {{{ */

// maxRegisters is Lua 5.1's MAXSTACK: a function uses fewer registers.
// go-moonscript patch: it was 200, LUAI_MAXVARS (the parser checks that
// limit, the number of local variables per function), which made functions
// with many locals fail that Lua 5.1 compiles.
const maxRegisters = 250

type expContextType int

const (
	ecGlobal expContextType = iota
	ecUpvalue
	ecLocal
	ecTable
	ecVararg
	ecMethod
	ecNone
)

const regNotDefined = opMaxArgsA + 1
const labelNoJump = 0

type expcontext struct {
	ctype expContextType
	reg   int
	// varargopt >= 0: wants varargopt+1 results, i.e  a = func()
	// varargopt = -1: ignore results             i.e  func()
	// varargopt = -2: receive all results        i.e  a = {func()}
	varargopt int
}

type assigncontext struct {
	ec       *expcontext
	keyrk    int
	valuerk  int
	keyks    bool
	needmove bool
	keypc    int // go-moonscript patch: the LOADK of a constant key, -1 if none
}

type lblabels struct {
	t int
	f int
	e int
	b bool
}

type constLValueExpr struct {
	ast.ExprBase

	Value LValue
}

// }}}

/* utilities {{{ */
var _ecnone0 = &expcontext{ecNone, regNotDefined, 0}
var _ecnonem1 = &expcontext{ecNone, regNotDefined, -1}
var _ecnonem2 = &expcontext{ecNone, regNotDefined, -2}
var ecfuncdef = &expcontext{ecMethod, regNotDefined, 0}

func ecupdate(ec *expcontext, ctype expContextType, reg, varargopt int) {
	if ec == _ecnone0 || ec == _ecnonem1 || ec == _ecnonem2 {
		panic("can not update ec cache")
	}
	ec.ctype = ctype
	ec.reg = reg
	ec.varargopt = varargopt
}

func ecnone(varargopt int) *expcontext {
	switch varargopt {
	case 0:
		return _ecnone0
	case -1:
		return _ecnonem1
	case -2:
		return _ecnonem2
	}
	return &expcontext{ecNone, regNotDefined, varargopt}
}

func shouldmove(ec *expcontext, reg int) bool {
	return ec.ctype == ecLocal && ec.reg != regNotDefined && ec.reg != reg
}

func sline(pos ast.PositionHolder) int {
	return pos.Line()
}

func eline(pos ast.PositionHolder) int {
	line := pos.LastLine()
	if line == 0 {
		return pos.Line()
	}
	return line
}

func savereg(ec *expcontext, reg int) int {
	if ec.ctype != ecLocal || ec.reg == regNotDefined {
		return reg
	}
	return ec.reg
}

func raiseCompileError(context *funcContext, line int, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	panic(&CompileError{context: context, Line: line, Message: msg})
}

func isVarArgReturnExpr(expr ast.Expr) bool {
	switch ex := expr.(type) {
	case *ast.FuncCallExpr:
		return !ex.AdjustRet
	case *ast.Comma3Expr:
		return !ex.AdjustRet
	}
	return false
}

func lnumberValue(expr ast.Expr) (LNumber, bool) {
	if ex, ok := expr.(*ast.NumberExpr); ok {
		lv, err := parseNumber(ex.Value)
		if err != nil {
			lv = LNumber(math.NaN())
		}
		return lv, true
	} else if ex, ok := expr.(*constLValueExpr); ok {
		return ex.Value.(LNumber), true
	}
	return 0, false
}

// cdataNumeralFn is the function a LuaJIT FFI numeral (1LL, 1ULL, 1i,
// ast.CdataNumberExpr) compiles to a call of, with the numeral: such a
// numeral is cdata, which the VM doesn't have, so evaluating it is an error
// (the function is a constant, which string.dump keeps). go-moonscript
// patch.
var cdataNumeralFn = newLFunctionG(cdataNumeral, nil, 0)

func cdataNumeral(L *LState) int {
	s := L.CheckString(1)
	kind := "64-bit integer"
	if s[len(s)-1]|0x20 == 'i' {
		kind = "complex number"
	}
	L.RaiseError("%s literal '%s' needs the FFI, which is not supported", kind, s)
	return 0
}

/* utilities }}} */

type gotoLabelDesc struct { // {{{
	Id                 int
	Name               string
	Pc                 int
	Line               int
	NumActiveLocalVars int
}

func newLabelDesc(id int, name string, pc, line, n int) *gotoLabelDesc {
	return &gotoLabelDesc{
		Id:                 id,
		Name:               name,
		Pc:                 pc,
		Line:               line,
		NumActiveLocalVars: n,
	}
}

func (l *gotoLabelDesc) SetNumActiveLocalVars(n int) {
	l.NumActiveLocalVars = n
} // }}}

type CompileError struct { // {{{
	context *funcContext
	Line    int
	Message string
	source  string // go-moonscript patch: the chunk name, if no context
}

// Error formats the error as Lua does, with the chunk name verbatim:
// "<chunk>:<line>: <message>". go-moonscript patch: it was "compile error
// near line(<line>) <chunk>: <message>".
func (e *CompileError) Error() string {
	source := e.source
	if e.context != nil {
		source = e.context.Proto.SourceName
	}
	return fmt.Sprintf("%s:%d: %s", source, e.Line, e.Message)
} // }}}

type codeStore struct { // {{{
	codes []uint32
	lines []int
	pc    int
}

func (cd *codeStore) Add(inst uint32, line int) {
	if l := len(cd.codes); l <= 0 || cd.pc == l {
		cd.codes = append(cd.codes, inst)
		cd.lines = append(cd.lines, line)
	} else {
		cd.codes[cd.pc] = inst
		cd.lines[cd.pc] = line
	}
	cd.pc++
}

func (cd *codeStore) AddABC(op int, a int, b int, c int, line int) {
	cd.Add(opCreateABC(op, a, b, c), line)
}

func (cd *codeStore) AddABx(op int, a int, bx int, line int) {
	cd.Add(opCreateABx(op, a, bx), line)
}

func (cd *codeStore) AddASbx(op int, a int, sbx int, line int) {
	cd.Add(opCreateASbx(op, a, sbx), line)
}

func (cd *codeStore) PropagateKMV(top int, save *int, reg *int, inc int) {
	lastinst := cd.Last()
	if opGetArgA(lastinst) >= top {
		switch opGetOpCode(lastinst) {
		case OP_LOADK:
			cindex := opGetArgBx(lastinst)
			if cindex <= opMaxIndexRk {
				cd.Pop()
				*save = opRkAsk(cindex)
				return
			}
		case OP_MOVE:
			cd.Pop()
			*save = opGetArgB(lastinst)
			return
		}
	}
	*save = *reg
	*reg = *reg + inc
}

// PropagateK is PropagateKMV without the MOVE case: the value is never a
// local's register, which a store of a multiple assignment may overwrite
// before it is read. go-moonscript patch (yuin/gopher-lua#560).
func (cd *codeStore) PropagateK(top int, save *int, reg *int, inc int) {
	lastinst := cd.Last()
	if opGetArgA(lastinst) >= top && opGetOpCode(lastinst) == OP_LOADK {
		cindex := opGetArgBx(lastinst)
		if cindex <= opMaxIndexRk {
			cd.Pop()
			*save = opRkAsk(cindex)
			return
		}
	}
	*save = *reg
	*reg = *reg + inc
}

// keepInRegister is a propagator that keeps the value in its own register.
// go-moonscript patch (yuin/gopher-lua#560).
func keepInRegister(top int, save *int, reg *int, inc int) {
	*save = *reg
	*reg = *reg + inc
}

func (cd *codeStore) PropagateMV(top int, save *int, reg *int, inc int) {
	lastinst := cd.Last()
	if opGetArgA(lastinst) >= top {
		switch opGetOpCode(lastinst) {
		case OP_MOVE:
			cd.Pop()
			*save = opGetArgB(lastinst)
			return
		}
	}
	*save = *reg
	*reg = *reg + inc
}

func (cd *codeStore) AddLoadNil(a, b, line int) {
	// this method used to merge multiple consecutive LOADNIL instructions
	// of consecutive registers into a single LOADNIL instruction, but it
	// caused issues when the merged instructions were JMP targets, and so
	// generated invalid code; so the merging functionality has been removed.
	// It is safe to merge the LOADNIL instructions under certain conditions,
	// but additional logic / complexity would be needed here.
	cd.AddABC(OP_LOADNIL, a, b, 0, line)
}

// SetLine sets the line of the instruction at pc. go-moonscript patch.
func (cd *codeStore) SetLine(pc int, line int) {
	cd.lines[pc] = line
}

func (cd *codeStore) SetOpCode(pc int, v int) {
	opSetOpCode(&cd.codes[pc], v)
}

func (cd *codeStore) SetA(pc int, v int) {
	opSetArgA(&cd.codes[pc], v)
}

func (cd *codeStore) SetB(pc int, v int) {
	opSetArgB(&cd.codes[pc], v)
}

func (cd *codeStore) SetC(pc int, v int) {
	opSetArgC(&cd.codes[pc], v)
}

func (cd *codeStore) SetBx(pc int, v int) {
	opSetArgBx(&cd.codes[pc], v)
}

func (cd *codeStore) SetSbx(pc int, v int) {
	opSetArgSbx(&cd.codes[pc], v)
}

func (cd *codeStore) At(pc int) uint32 {
	return cd.codes[pc]
}

func (cd *codeStore) List() []uint32 {
	return cd.codes[:cd.pc]
}

func (cd *codeStore) PosList() []int {
	return cd.lines[:cd.pc]
}

func (cd *codeStore) LastPC() int {
	return cd.pc - 1
}

func (cd *codeStore) Last() uint32 {
	if cd.pc == 0 {
		return opInvalidInstruction
	}
	return cd.codes[cd.pc-1]
}

func (cd *codeStore) Pop() {
	cd.pc--
} /* }}} Code */

/* {{{ VarNamePool */

type varNamePoolValue struct {
	Index int
	Name  string
}

type varNamePool struct {
	names  []string
	offset int
	dbg    []int // go-moonscript patch: index of each local in Proto.DbgLocals
}

func newVarNamePool(offset int) *varNamePool {
	return &varNamePool{names: make([]string, 0, 16), offset: offset}
}

func (vp *varNamePool) Names() []string {
	return vp.names
}

func (vp *varNamePool) List() []varNamePoolValue {
	result := make([]varNamePoolValue, len(vp.names), len(vp.names))
	for i, name := range vp.names {
		result[i].Index = i + vp.offset
		result[i].Name = name
	}
	return result
}

func (vp *varNamePool) LastIndex() int {
	return vp.offset + len(vp.names)
}

func (vp *varNamePool) Find(name string) int {
	for i := len(vp.names) - 1; i >= 0; i-- {
		if vp.names[i] == name {
			return i + vp.offset
		}
	}
	return -1
}

func (vp *varNamePool) RegisterUnique(name string) int {
	index := vp.Find(name)
	if index < 0 {
		return vp.Register(name)
	}
	return index
}

func (vp *varNamePool) Register(name string) int {
	vp.names = append(vp.names, name)
	return len(vp.names) - 1 + vp.offset
}

/* }}} VarNamePool */

/* FuncContext {{{ */

type codeBlock struct {
	LocalVars      *varNamePool
	BreakLabel     int
	Parent         *codeBlock
	RefUpvalue     bool
	LineStart      int
	LastLine       int
	labels         map[string]*gotoLabelDesc
	firstGotoIndex int
	scopeEnded     bool // go-moonscript patch: EndScope was called
}

func newCodeBlock(localvars *varNamePool, blabel int, parent *codeBlock, pos ast.PositionHolder, firstGotoIndex int) *codeBlock {
	bl := &codeBlock{localvars, blabel, parent, false, 0, 0, map[string]*gotoLabelDesc{}, firstGotoIndex, false}
	if pos != nil {
		bl.LineStart = pos.Line()
		bl.LastLine = pos.LastLine()
	}
	return bl
}

func (b *codeBlock) AddLabel(label *gotoLabelDesc) *gotoLabelDesc {
	if old, ok := b.labels[label.Name]; ok {
		return old
	}
	b.labels[label.Name] = label
	return nil
}

func (b *codeBlock) GetLabel(label string) *gotoLabelDesc {
	if v, ok := b.labels[label]; ok {
		return v
	}
	return nil
}

func (b *codeBlock) LocalVarsCount() int {
	count := 0
	for block := b; block != nil; block = block.Parent {
		count += len(block.LocalVars.Names())
	}
	return count
}

type funcContext struct {
	Proto           *FunctionProto
	Code            *codeStore
	Parent          *funcContext
	Upvalues        *varNamePool
	Block           *codeBlock
	Blocks          []*codeBlock
	regTop          int
	labelId         int
	labelPc         map[int]int
	gotosCount      int
	unresolvedGotos map[int]*gotoLabelDesc
	entryRegs       int            // go-moonscript patch: parameter registers (loadNil)
	elidedNilReg    int            // go-moonscript patch: highest register of an elided LOADNIL, -1 if none
	lastTarget      int            // go-moonscript patch: highest label pc (loadNil)
	stmtLine        int            // go-moonscript patch: line of the statement being compiled (limit errors)
	state           *compileState  // go-moonscript patch: shared by a Compile call's funcContexts
	constIndex      map[LValue]int // go-moonscript patch: the index of each constant
}

// go-moonscript patch: maxCompileDepth is how deep the compiler recurses
// into the syntax tree. Lua 5.1's parser stops at 200 levels of nesting
// (LUAI_MAXCCALLS, "chunk has too many syntax levels"); gopher-lua's parser
// has no limit, and a left-associative chain (a+a+...+a, a.b.c...) is a
// tree as deep as it is long, which Lua 5.1 accepts at any length. Each
// level costs up to a few kilobytes of Go stack, so a deep enough tree
// crashed the process with a fatal Go stack overflow. The limit is far
// above the nesting any Lua 5.1 program can have. Ported from
// mdogan/gopher-lua 3f80512213 (whose limit is 10000).
const maxCompileDepth = 100000

type compileState struct {
	depth int // statements and expressions being compiled
	// what constFold returned for an operator expression, so that a chain is
	// folded once, not once per level (that was quadratic)
	folded map[ast.Expr]ast.Expr
}

// enterLevel enters one more level of the syntax tree, failing past
// maxCompileDepth; pair it with leaveLevel.
func (fc *funcContext) enterLevel(line int) {
	fc.state.depth++
	if fc.state.depth > maxCompileDepth {
		raiseCompileError(fc, line, "chunk has too many syntax levels")
	}
}

func (fc *funcContext) leaveLevel() {
	fc.state.depth--
}

func newFuncContext(sourcename string, parent *funcContext) *funcContext {
	fc := &funcContext{
		Proto:           newFunctionProto(sourcename),
		Code:            &codeStore{make([]uint32, 0, 1024), make([]int, 0, 1024), 0},
		Parent:          parent,
		Upvalues:        newVarNamePool(0),
		Block:           newCodeBlock(newVarNamePool(0), labelNoJump, nil, nil, 0),
		regTop:          0,
		labelId:         1,
		labelPc:         map[int]int{},
		gotosCount:      0,
		unresolvedGotos: map[int]*gotoLabelDesc{},
		elidedNilReg:    -1,
		lastTarget:      -2,
		constIndex:      map[LValue]int{},
	}
	if parent != nil {
		fc.state = parent.state
	} else {
		fc.state = &compileState{folded: map[ast.Expr]ast.Expr{}}
	}
	fc.Blocks = []*codeBlock{fc.Block}
	return fc
}

func (fc *funcContext) CheckUnresolvedGoto() {
	for i := fc.Block.firstGotoIndex; i < fc.gotosCount; i++ {
		gotoLabel, ok := fc.unresolvedGotos[i]
		if !ok {
			continue
		}
		// go-moonscript patch: LuaJIT's message (the parser checks gotos)
		raiseCompileError(fc, gotoLabel.Line, "undefined label '%s'", gotoLabel.Name)
	}
}

func (fc *funcContext) AddUnresolvedGoto(label *gotoLabelDesc) {
	fc.unresolvedGotos[fc.gotosCount] = label
	fc.gotosCount++
}

func (fc *funcContext) AddNamedLabel(label *gotoLabelDesc) {
	if old := fc.Block.AddLabel(label); old != nil {
		raiseCompileError(fc, label.Line, "duplicate label '%s'", label.Name) // go-moonscript patch: LuaJIT's
	}
	fc.SetLabelPc(label.Id, label.Pc)
}

func (fc *funcContext) GetNamedLabel(name string) *gotoLabelDesc {
	return fc.Block.GetLabel(name)
}

func (fc *funcContext) ResolveGoto(from, to *gotoLabelDesc, index int) {
	if from.NumActiveLocalVars < to.NumActiveLocalVars {
		varName := fc.Block.LocalVars.Names()[len(fc.Block.LocalVars.Names())-1]
		// go-moonscript patch: LuaJIT's message
		raiseCompileError(fc, from.Line, "<goto %s> jumps into the scope of local '%s'", to.Name, varName)
	}
	fc.Code.SetSbx(from.Pc, to.Id)
	delete(fc.unresolvedGotos, index)
}

func (fc *funcContext) FindLabel(block *codeBlock, gotoLabel *gotoLabelDesc, i int) bool {
	target := block.GetLabel(gotoLabel.Name)
	if target != nil {
		if gotoLabel.NumActiveLocalVars > target.NumActiveLocalVars && block.RefUpvalue {
			fc.Code.SetA(gotoLabel.Pc-1, target.NumActiveLocalVars)
		}
		fc.ResolveGoto(gotoLabel, target, i)
		return true
	}
	return false
}

func (fc *funcContext) ResolveCurrentBlockGotosWithParentBlock() {
	blockActiveLocalVars := fc.Block.Parent.LocalVarsCount()
	for i := fc.Block.firstGotoIndex; i < fc.gotosCount; i++ {
		gotoLabel, ok := fc.unresolvedGotos[i]
		if !ok {
			continue
		}
		if gotoLabel.NumActiveLocalVars > blockActiveLocalVars {
			if fc.Block.RefUpvalue {
				fc.Code.SetA(gotoLabel.Pc-1, blockActiveLocalVars)
			}
			gotoLabel.SetNumActiveLocalVars(blockActiveLocalVars)
		}
		fc.FindLabel(fc.Block.Parent, gotoLabel, i)
	}
}

func (fc *funcContext) ResolveForwardGoto(target *gotoLabelDesc) {
	for i := fc.Block.firstGotoIndex; i <= fc.gotosCount; i++ {
		gotoLabel, ok := fc.unresolvedGotos[i]
		if !ok {
			continue
		}
		if gotoLabel.Name == target.Name {
			fc.ResolveGoto(gotoLabel, target, i)
		}
	}
}

func (fc *funcContext) NewLabel() int {
	ret := fc.labelId
	fc.labelId++
	return ret
}

func (fc *funcContext) SetLabelPc(label int, pc int) {
	fc.labelPc[label] = pc
	if pc > fc.lastTarget { // go-moonscript patch (loadNil)
		fc.lastTarget = pc
	}
}

// blockEndLine is the line luac gives the instructions it emits at the end
// of a block (upvalue CLOSE, loop jumps): the line of the block's last
// token, approximated by the last line of its last statement; fallback for
// an empty block. go-moonscript patch: gopher-lua used the line of the
// block's "end", which made debug hooks report line events for "end" lines
// that Lua 5.1 doesn't.
func blockEndLine(stmts []ast.Stmt, fallback int) int {
	if len(stmts) == 0 {
		return fallback
	}
	return stmtLastLine(stmts[len(stmts)-1])
}

// isLazyExpr reports whether luac would load expr into a register only
// when the next token is seen (luaK_exp2nextreg on a constant, variable or
// indexed expression), so its last instruction has the line of the token
// after it.
func isLazyExpr(expr ast.Expr) bool {
	switch expr.(type) {
	case *ast.NilExpr, *ast.TrueExpr, *ast.FalseExpr, *ast.NumberExpr, *ast.StringExpr,
		*constLValueExpr, *ast.IdentExpr, *ast.AttrGetExpr:
		return true
	}
	return false
}

// compileLazyExpr compiles expr like compileExpr; a lazy expression's last
// instruction gets line (the line luac discharges it on). go-moonscript
// patch.
func compileLazyExpr(context *funcContext, reg int, expr ast.Expr, ec *expcontext, line int) int {
	pc := context.Code.LastPC()
	n := compileExpr(context, reg, expr, ec)
	if last := context.Code.LastPC(); line > 0 && last > pc && isLazyExpr(expr) {
		context.Code.SetLine(last, line)
	}
	return n
}

// stmtLastLine approximates the line of the last token of a statement: the
// end of a block statement, else the highest line in its expressions.
func stmtLastLine(stmt ast.Stmt) int {
	line := eline(stmt)
	max := func(exprs ...ast.Expr) {
		for _, e := range exprs {
			if l := exprLastLine(e); l > line {
				line = l
			}
		}
	}
	switch st := stmt.(type) {
	case *ast.AssignStmt:
		max(st.Lhs...)
		max(st.Rhs...)
	case *ast.LocalAssignStmt:
		max(st.Exprs...)
	case *ast.FuncCallStmt:
		max(st.Expr)
	case *ast.ReturnStmt:
		max(st.Exprs...)
	case *ast.FuncDefStmt:
		max(st.Func)
	}
	return line
}

// exprLastLine is the highest line in an expression (the line of its last
// token as far as the syntax tree records it).
func exprLastLine(expr ast.Expr) int {
	if expr == nil {
		return 0
	}
	line := eline(expr)
	max := func(exprs ...ast.Expr) {
		for _, e := range exprs {
			if l := exprLastLine(e); l > line {
				line = l
			}
		}
	}
	switch ex := expr.(type) {
	case *ast.AttrGetExpr:
		max(ex.Object, ex.Key)
	case *ast.TableExpr:
		for _, f := range ex.Fields {
			max(f.Key, f.Value)
		}
	case *ast.FuncCallExpr:
		max(ex.Func, ex.Receiver)
		max(ex.Args...)
		if ex.ArgsEndLine > line {
			line = ex.ArgsEndLine
		}
	case *ast.LogicalOpExpr:
		max(ex.Lhs, ex.Rhs)
	case *ast.RelationalOpExpr:
		max(ex.Lhs, ex.Rhs)
	case *ast.StringConcatOpExpr:
		max(ex.Lhs, ex.Rhs)
	case *ast.ArithmeticOpExpr:
		max(ex.Lhs, ex.Rhs)
	case *ast.UnaryMinusOpExpr:
		max(ex.Expr)
	case *ast.UnaryNotOpExpr:
		max(ex.Expr)
	case *ast.UnaryLenOpExpr:
		max(ex.Expr)
	}
	return line
}

// endBlockPos is a position for EnterBlock whose last line is
// blockEndLine(stmts, fallback).
func endBlockPos(pos ast.PositionHolder, stmts []ast.Stmt, fallback int) ast.PositionHolder {
	ph := &ast.Node{}
	ph.SetLine(sline(pos))
	ph.SetLastLine(blockEndLine(stmts, fallback))
	return ph
}

// loadNil sets registers a..b to nil. go-moonscript patch: like luac's
// luaK_nil, it emits nothing at the start of a function (when no jump
// targets it) for registers above the parameters, which the VM has set to
// nil already, so that debug hooks see the same line events as in Lua 5.1
// (a "local x" there has no instruction). patchCode keeps such registers
// below the frame's last register, the one a tail call doesn't copy.
func (fc *funcContext) loadNil(a, b, line int) {
	last := fc.Code.LastPC()
	if !fc.isJumpTarget(last) {
		if last < 0 {
			if a >= fc.entryRegs {
				if b > fc.elidedNilReg {
					fc.elidedNilReg = b
				}
				return
			}
		} else if prev := fc.Code.Last(); opGetOpCode(prev) == OP_LOADNIL {
			// extend the previous LOADNIL if the ranges connect
			if pa, pb := opGetArgA(prev), opGetArgB(prev); pa <= a && a <= pb+1 {
				if b > pb {
					fc.Code.SetB(last, b)
				}
				return
			}
		}
	}
	fc.Code.AddLoadNil(a, b, line)
}

// isJumpTarget reports whether a label targets the instruction after pc,
// the last one (labels are set at the last instruction, so the highest is
// the latest).
func (fc *funcContext) isJumpTarget(pc int) bool {
	return fc.lastTarget >= pc
}

func (fc *funcContext) GetLabelPc(label int) int {
	return fc.labelPc[label]
}

// ConstIndex is the index of a constant, added if new. go-moonscript patch:
// looked up in a map, not by a linear search of the constants, which made
// compiling a function with many constants quadratic (Lua 5.1's
// verybig.lua took minutes). NaN is never found, as it was.
func (fc *funcContext) ConstIndex(value LValue) int {
	if i, ok := fc.constIndex[value]; ok {
		return i
	}
	fc.Proto.Constants = append(fc.Proto.Constants, value)
	v := len(fc.Proto.Constants) - 1
	fc.constIndex[value] = v
	if v > opMaxArgBx {
		raiseCompileError(fc, fc.Proto.LineDefined, "too many constants")
	}
	return v
}
func (fc *funcContext) BlockLocalVarsCount() int {
	count := 0
	for block := fc.Block; block != nil; block = block.Parent {
		count += len(block.LocalVars.Names())
	}
	return count
}

func (fc *funcContext) RegisterLocalVar(name string) int {
	ret := fc.Block.LocalVars.Register(name)
	fc.Block.LocalVars.dbg = append(fc.Block.LocalVars.dbg, len(fc.Proto.DbgLocals)) // go-moonscript patch
	fc.Proto.DbgLocals = append(fc.Proto.DbgLocals, &DbgLocalInfo{Name: name, StartPc: fc.Code.LastPC() + 1})
	fc.SetRegTop(fc.RegTop() + 1)
	return ret
}

func (fc *funcContext) FindLocalVarAndBlock(name string) (int, *codeBlock) {
	for block := fc.Block; block != nil; block = block.Parent {
		if index := block.LocalVars.Find(name); index > -1 {
			return index, block
		}
	}
	return -1, nil
}

func (fc *funcContext) FindLocalVar(name string) int {
	idx, _ := fc.FindLocalVarAndBlock(name)
	return idx
}

func (fc *funcContext) LocalVars() []varNamePoolValue {
	result := make([]varNamePoolValue, 0, 32)
	for _, block := range fc.Blocks {
		result = append(result, block.LocalVars.List()...)
	}
	return result
}

func (fc *funcContext) EnterBlock(blabel int, pos ast.PositionHolder) {
	fc.Block = newCodeBlock(newVarNamePool(fc.RegTop()), blabel, fc.Block, pos, fc.gotosCount)
	fc.Blocks = append(fc.Blocks, fc.Block)
}

func (fc *funcContext) CloseUpvalues() int {
	n := -1
	if fc.Block.RefUpvalue {
		n = fc.Block.Parent.LocalVars.LastIndex()
		fc.Code.AddABC(OP_CLOSE, n, 0, 0, fc.Block.LastLine)
	}
	return n
}

func (fc *funcContext) LeaveBlock() int {
	fc.EndScope() // go-moonscript patch: before the CLOSE, as in luac
	closed := fc.CloseUpvalues()

	if fc.Block.Parent != nil {
		fc.ResolveCurrentBlockGotosWithParentBlock()
	}
	fc.Block = fc.Block.Parent
	fc.SetRegTop(fc.Block.LocalVars.LastIndex())
	return closed
}

// EndScope ends the scope of the block's locals after the last instruction
// so far (once per block): like in luac, a local is live from its StartPc
// to its EndPc, exclusive. go-moonscript patch (from LilithGames/gopher-lua
// 92ca436742, taybart/midnight d4a2911c41): it took the locals' registers
// for their indices in DbgLocals, which they aren't once a register is
// reused, and ended the scope one instruction early.
func (fc *funcContext) EndScope() {
	if fc.Block.scopeEnded {
		return
	}
	fc.Block.scopeEnded = true
	for _, i := range fc.Block.LocalVars.dbg {
		fc.Proto.DbgLocals[i].EndPc = fc.Code.LastPC() + 1
	}
}

// endLoopScope ends the scope of a for loop's hidden locals (n locals from
// DbgLocals[first]) after the loop instruction, as in luac: they are live
// in FORLOOP/TFORLOOP, the loop variables are not. go-moonscript patch.
func (fc *funcContext) endLoopScope(first, n int) {
	for i := first; i < first+n; i++ {
		fc.Proto.DbgLocals[i].EndPc = fc.Code.LastPC() + 1
	}
}

func (fc *funcContext) SetRegTop(top int) {
	if top > maxRegisters {
		// go-moonscript patch (yuin/gopher-lua#556): on the line of the
		// declaration, not the function's first line
		raiseCompileError(fc, fc.stmtLine, "too many local variables")
	}
	fc.regTop = top
}

func (fc *funcContext) RegTop() int {
	return fc.regTop
}

/* FuncContext }}} */

func compileChunk(context *funcContext, chunk []ast.Stmt, untilFollows bool) { // {{{
	for i, stmt := range chunk {
		lastStmt := true
		for j := i + 1; j < len(chunk); j++ {
			_, ok := chunk[j].(*ast.LabelStmt)
			if !ok {
				lastStmt = false
				break
			}
		}
		compileStmt(context, stmt, lastStmt && !untilFollows)
	}
} // }}}

func compileBlock(context *funcContext, chunk []ast.Stmt) { // {{{
	if len(chunk) == 0 {
		return
	}
	ph := &ast.Node{}
	ph.SetLine(sline(chunk[0]))
	ph.SetLastLine(eline(chunk[len(chunk)-1]))
	context.EnterBlock(labelNoJump, ph)
	for i, stmt := range chunk {
		lastStmt := true
		for j := i + 1; j < len(chunk); j++ {
			_, ok := chunk[j].(*ast.LabelStmt)
			if !ok {
				lastStmt = false
				break
			}
		}
		compileStmt(context, stmt, lastStmt)
	}
	context.LeaveBlock()
} // }}}

func compileStmt(context *funcContext, stmt ast.Stmt, isLastStmt bool) { // {{{
	context.stmtLine = sline(stmt)  // go-moonscript patch
	context.enterLevel(sline(stmt)) // go-moonscript patch
	defer context.leaveLevel()
	switch st := stmt.(type) {
	case *ast.AssignStmt:
		compileAssignStmt(context, st)
	case *ast.LocalAssignStmt:
		compileLocalAssignStmt(context, st)
	case *ast.FuncCallStmt:
		compileFuncCallExpr(context, context.RegTop(), st.Expr.(*ast.FuncCallExpr), ecnone(-1))
	case *ast.DoBlockStmt:
		context.EnterBlock(labelNoJump, endBlockPos(st, st.Stmts, sline(st))) // go-moonscript patch
		compileChunk(context, st.Stmts, false)
		context.LeaveBlock()
	case *ast.WhileStmt:
		compileWhileStmt(context, st)
	case *ast.RepeatStmt:
		compileRepeatStmt(context, st)
	case *ast.FuncDefStmt:
		compileFuncDefStmt(context, st)
	case *ast.ReturnStmt:
		compileReturnStmt(context, st)
	case *ast.IfStmt:
		compileIfStmt(context, st)
	case *ast.BreakStmt:
		compileBreakStmt(context, st)
	case *ast.NumberForStmt:
		compileNumberForStmt(context, st)
	case *ast.GenericForStmt:
		compileGenericForStmt(context, st)
	case *ast.LabelStmt:
		compileLabelStmt(context, st, isLastStmt)
	case *ast.GotoStmt:
		compileGotoStmt(context, st)
	}
} // }}}

func compileAssignStmtLeft(context *funcContext, stmt *ast.AssignStmt) (int, []*assigncontext) { // {{{
	reg := context.RegTop()
	acs := make([]*assigncontext, 0, len(stmt.Lhs))
	for i, lhs := range stmt.Lhs {
		switch st := lhs.(type) {
		case *ast.IdentExpr:
			identtype := getIdentRefType(context, context, st)
			ec := &expcontext{identtype, regNotDefined, 0}
			switch identtype {
			case ecGlobal:
				context.ConstIndex(LString(st.Value))
			case ecUpvalue:
				context.Upvalues.RegisterUnique(st.Value)
			case ecLocal:
				ec.reg = context.FindLocalVar(st.Value)
			}
			acs = append(acs, &assigncontext{ec, 0, 0, false, false, -1})
		case *ast.AttrGetExpr:
			ac := &assigncontext{&expcontext{ecTable, regNotDefined, 0}, 0, 0, false, false, -1}
			// go-moonscript patch: the table and the key are operands of
			// SETTABLE as in luac, a local's register or a constant (the key)
			// rather than a temporary, unless a later target of a multiple
			// assignment is that local (luac's check_conflict; yuin/gopher-
			// lua#560: t, t.x = u, 1): it is assigned first. The table is
			// operand A, a register: a constant object, as in ("s").k = v,
			// stays loaded (yuin/gopher-lua#538, #310).
			if assignConflict(context, stmt.Lhs[i+1:], st.Object) {
				compileExprWithPropagation(context, st.Object, &reg, &ac.ec.reg, keepInRegister)
			} else {
				compileExprWithMVPropagation(context, st.Object, &reg, &ac.ec.reg)
			}
			pc := context.Code.LastPC()
			if assignConflict(context, stmt.Lhs[i+1:], st.Key) {
				compileExprWithPropagation(context, st.Key, &reg, &ac.keyrk, keepInRegister)
			} else {
				compileExprWithKMVPropagation(context, st.Key, &reg, &ac.keyrk)
			}
			switch st.Key.(type) {
			case *ast.StringExpr, *ast.NumberExpr:
				// a constant key not in RK range is loaded; luac has no
				// instruction for it: the load gets the store's line
				if last := context.Code.LastPC(); last > pc && !opIsK(ac.keyrk) {
					ac.keypc = last
				}
			}
			if _, ok := st.Key.(*ast.StringExpr); ok {
				ac.keyks = true
			}
			acs = append(acs, ac)

		default:
			panic("invalid left expression.")
		}
	}
	return reg, acs
} // }}}

// assignConflict reports whether expr is a local that one of the targets
// assigns. go-moonscript patch.
func assignConflict(context *funcContext, targets []ast.Expr, expr ast.Expr) bool {
	id, ok := expr.(*ast.IdentExpr)
	if !ok || getIdentRefType(context, context, id) != ecLocal {
		return false
	}
	reg := context.FindLocalVar(id.Value)
	for _, t := range targets {
		if tid, ok := t.(*ast.IdentExpr); ok && getIdentRefType(context, context, tid) == ecLocal &&
			context.FindLocalVar(tid.Value) == reg {
			return true
		}
	}
	return false
}

func compileAssignStmtRight(context *funcContext, stmt *ast.AssignStmt, reg int, acs []*assigncontext) (int, []*assigncontext) { // {{{
	lennames := len(stmt.Lhs)
	lenexprs := len(stmt.Rhs)
	namesassigned := 0

	for namesassigned < lennames {
		ac := acs[namesassigned]
		ec := ac.ec
		// go-moonscript patch: the last value of a multiple assignment with
		// as many values as targets is stored first, before anything else
		// is (and after the other values are evaluated), as in luac
		last := namesassigned == lennames-1 && lenexprs == lennames
		if lennames > 1 && ec.ctype == ecLocal && !last {
			// go-moonscript patch (yuin/gopher-lua#560): a, b = b, a needs the
			// value in a temporary, as later expressions may read the local
			ec = ecnone(0)
		}
		var expr ast.Expr = nil
		if namesassigned >= lenexprs {
			expr = &ast.NilExpr{}
			expr.SetLine(sline(stmt.Lhs[namesassigned]))
			expr.SetLastLine(eline(stmt.Lhs[namesassigned]))
		} else if isVarArgReturnExpr(stmt.Rhs[namesassigned]) && (lenexprs-namesassigned-1) <= 0 {
			varargopt := lennames - namesassigned - 1
			regstart := reg
			reginc := compileExpr(context, reg, stmt.Rhs[namesassigned], ecnone(varargopt))
			reg += reginc
			for i := namesassigned; i < namesassigned+int(reginc); i++ {
				acs[i].needmove = true
				if acs[i].ec.ctype == ecTable {
					acs[i].valuerk = regstart + (i - namesassigned)
				}
			}
			namesassigned = lennames
			continue
		}

		if expr == nil {
			expr = stmt.Rhs[namesassigned]
		}
		if ec.ctype == ecTable {
			// go-moonscript patch: a nil, true or false value is a constant
			// operand of SETTABLE, as in luac
			if k, ok := literalConst(expr); ok {
				if i := context.ConstIndex(k); i <= opMaxIndexRk {
					ac.valuerk = opRkAsk(i)
					namesassigned += 1
					continue
				}
			}
		}
		idx := reg
		reginc := compileExpr(context, reg, expr, ec)
		if ec.ctype == ecTable {
			if _, ok := expr.(*ast.LogicalOpExpr); !ok {
				if lennames > 1 && !last { // go-moonscript patch (yuin/gopher-lua#560)
					context.Code.PropagateK(context.RegTop(), &ac.valuerk, &reg, reginc)
				} else {
					context.Code.PropagateKMV(context.RegTop(), &ac.valuerk, &reg, reginc)
				}
			} else {
				ac.valuerk = idx
				reg += reginc
			}
		} else {
			ac.needmove = reginc != 0
			reg += reginc
		}
		namesassigned += 1
	}

	rightreg := reg - 1

	// extra right exprs
	for i := namesassigned; i < lenexprs; i++ {
		varargopt := -1
		if i != lenexprs-1 {
			varargopt = 0
		}
		reg += compileExpr(context, reg, stmt.Rhs[i], ecnone(varargopt))
	}
	return rightreg, acs
} // }}}

func compileAssignStmt(context *funcContext, stmt *ast.AssignStmt) { // {{{
	compileAssignStmtAt(context, stmt, false)
}

func compileAssignStmtAt(context *funcContext, stmt *ast.AssignStmt, funcdef bool) {
	code := context.Code
	lennames := len(stmt.Lhs)
	reg, acs := compileAssignStmtLeft(context, stmt)
	rhsbase := reg
	reg, acs = compileAssignStmtRight(context, stmt, reg, acs)

	// go-moonscript patch: luac stores after the right-hand side, on the
	// line of its last token (except for function statements, where it is
	// on the "function" line)
	storeline := func(ex ast.Expr) int { return sline(ex) }
	if !funcdef {
		last := 0
		for _, e := range stmt.Rhs {
			if l := exprLastLine(e); l > last {
				last = l
			}
		}
		storeline = func(ex ast.Expr) int {
			if l := sline(ex); l > last {
				return l
			}
			return last
		}
	}

	for i := lennames - 1; i >= 0; i-- {
		ex := stmt.Lhs[i]
		switch acs[i].ec.ctype {
		case ecLocal:
			if acs[i].needmove {
				code.AddABC(OP_MOVE, context.FindLocalVar(ex.(*ast.IdentExpr).Value), reg, 0, storeline(ex))
				reg -= 1
			}
		case ecGlobal:
			code.AddABx(OP_SETGLOBAL, reg, context.ConstIndex(LString(ex.(*ast.IdentExpr).Value)), storeline(ex))
			reg -= 1
		case ecUpvalue:
			code.AddABC(OP_SETUPVAL, reg, context.Upvalues.RegisterUnique(ex.(*ast.IdentExpr).Value), 0, storeline(ex))
			reg -= 1
		case ecTable:
			opcode := OP_SETTABLE
			if acs[i].keyks {
				opcode = OP_SETTABLEKS
			}
			code.AddABC(opcode, acs[i].ec.reg, acs[i].keyrk, acs[i].valuerk, storeline(ex))
			if acs[i].keypc >= 0 {
				code.SetLine(acs[i].keypc, storeline(ex))
			}
			// go-moonscript patch (yuin/gopher-lua#560): a value in a local's
			// register took no temporary
			if !opIsK(acs[i].valuerk) && acs[i].valuerk >= rhsbase {
				reg -= 1
			}
		}
	}
} // }}}

// compileRegAssignment assigns exprs to the nvars registers from reg.
// go-moonscript patch (yuin/gopher-lua#528): it took the names too and
// assigned len(names) registers, which isn't nvars for a generic for (3
// registers): "for k in f do" left the state and control registers as they
// were, not nil.
func compileRegAssignment(context *funcContext, exprs []ast.Expr, reg int, nvars int, line int) { // {{{
	lennames := nvars
	lenexprs := len(exprs)
	namesassigned := 0
	ec := &expcontext{}

	for namesassigned < lennames && namesassigned < lenexprs {
		if isVarArgReturnExpr(exprs[namesassigned]) && (lenexprs-namesassigned-1) <= 0 {

			varargopt := nvars - namesassigned
			ecupdate(ec, ecVararg, reg, varargopt-1)
			compileExpr(context, reg, exprs[namesassigned], ec)
			reg += varargopt
			namesassigned = lennames
		} else {
			ecupdate(ec, ecLocal, reg, 0)
			compileExpr(context, reg, exprs[namesassigned], ec)
			reg += 1
			namesassigned += 1
		}
	}

	// extra left names
	if lennames > namesassigned {
		restleft := lennames - namesassigned - 1
		context.loadNil(reg, reg+restleft, line) // go-moonscript patch
		reg += restleft
	}

	// extra right exprs
	for i := namesassigned; i < lenexprs; i++ {
		varargopt := -1
		if i != lenexprs-1 {
			varargopt = 0
		}
		ecupdate(ec, ecNone, reg, varargopt)
		reg += compileExpr(context, reg, exprs[i], ec)
	}
} // }}}

func compileLocalAssignStmt(context *funcContext, stmt *ast.LocalAssignStmt) { // {{{
	reg := context.RegTop()
	// go-moonscript patch: only "local function f" is in the scope of f
	// before its body, not "local f = function"
	if stmt.IsFunction && len(stmt.Names) == 1 && len(stmt.Exprs) == 1 {
		if _, ok := stmt.Exprs[0].(*ast.FunctionExpr); ok {
			context.RegisterLocalVar(stmt.Names[0])
			compileRegAssignment(context, stmt.Exprs, reg, len(stmt.Names), sline(stmt))
			return
		}
	}

	compileRegAssignment(context, stmt.Exprs, reg, len(stmt.Names), sline(stmt))
	for _, name := range stmt.Names {
		context.RegisterLocalVar(name)
	}
} // }}}

func compileReturnStmt(context *funcContext, stmt *ast.ReturnStmt) { // {{{
	lenexprs := len(stmt.Exprs)
	code := context.Code
	reg := context.RegTop()
	a := reg
	lastisvaarg := false

	if lenexprs == 1 {
		switch ex := stmt.Exprs[0].(type) {
		case *ast.IdentExpr:
			if idx := context.FindLocalVar(ex.Value); idx > -1 {
				code.AddABC(OP_RETURN, idx, 2, 0, stmtLastLine(stmt)) // go-moonscript patch: after the values, as luac
				return
			}
		case *ast.FuncCallExpr:
			if ex.AdjustRet { // return (func())
				reg += compileExpr(context, reg, ex, ecnone(0))
			} else {
				reg += compileExpr(context, reg, ex, ecnone(-2))
				code.SetOpCode(code.LastPC(), OP_TAILCALL)
			}
			code.AddABC(OP_RETURN, a, 0, 0, stmtLastLine(stmt)) // go-moonscript patch: after the values, as luac
			return
		}
	}

	for i, expr := range stmt.Exprs {
		if i == lenexprs-1 && isVarArgReturnExpr(expr) {
			compileExpr(context, reg, expr, ecnone(-2))
			lastisvaarg = true
		} else {
			reg += compileExpr(context, reg, expr, ecnone(0))
		}
	}
	count := reg - a + 1
	if lastisvaarg {
		count = 0
	}
	context.Code.AddABC(OP_RETURN, a, count, 0, stmtLastLine(stmt)) // go-moonscript patch: after the values, as luac
} // }}}

func compileIfStmt(context *funcContext, stmt *ast.IfStmt) { // {{{
	endlabel := context.NewLabel()
	hasElse := len(stmt.Else) > 0
	// go-moonscript patch: an elseif (an else block of an if statement
	// only) is compiled in the loop, not in a block of the else block: a
	// long elseif chain was a deep chain of blocks, which made compiling it
	// quadratic
	for {
		thenlabel := context.NewLabel()
		elselabel := context.NewLabel()
		compileBranchCondition(context, context.RegTop(), stmt.Condition, thenlabel, elselabel, false)
		context.SetLabelPc(thenlabel, context.Code.LastPC())
		compileBlock(context, stmt.Then)
		if len(stmt.Else) > 0 {
			// go-moonscript patch: on the then block's last line, as luac
			context.Code.AddASbx(OP_JMP, 0, endlabel, blockEndLine(stmt.Then, eline(stmt.Condition)))
		}
		context.SetLabelPc(elselabel, context.Code.LastPC())
		if len(stmt.Else) == 0 {
			break
		}
		if elseif, ok := stmt.Else[0].(*ast.IfStmt); ok && len(stmt.Else) == 1 {
			stmt = elseif
			context.stmtLine = sline(stmt)
			continue
		}
		compileBlock(context, stmt.Else)
		break
	}
	if hasElse {
		context.SetLabelPc(endlabel, context.Code.LastPC())
	}
} // }}}

func compileBranchCondition(context *funcContext, reg int, expr ast.Expr, thenlabel, elselabel int, hasnextcond bool) { // {{{
	context.enterLevel(sline(expr)) // go-moonscript patch
	defer context.leaveLevel()
	// TODO folding constants?
	code := context.Code
	flip := 0
	jumplabel := elselabel
	if hasnextcond {
		flip = 1
		jumplabel = thenlabel
	}

	switch ex := expr.(type) {
	case *ast.FalseExpr, *ast.NilExpr:
		if !hasnextcond {
			code.AddASbx(OP_JMP, 0, elselabel, sline(expr))
			return
		}
	case *ast.TrueExpr, *ast.NumberExpr, *ast.StringExpr:
		if !hasnextcond {
			return
		}
	case *ast.UnaryNotOpExpr:
		// go-moonscript patch: fold "not <constant>" as luac does
		if !hasnextcond {
			switch ex.Expr.(type) {
			case *ast.FalseExpr, *ast.NilExpr:
				return
			case *ast.TrueExpr, *ast.NumberExpr, *ast.StringExpr:
				code.AddASbx(OP_JMP, 0, elselabel, sline(expr))
				return
			}
		}
		compileBranchCondition(context, reg, ex.Expr, elselabel, thenlabel, !hasnextcond)
		return
	case *ast.LogicalOpExpr:
		switch ex.Operator {
		case "and":
			nextcondlabel := context.NewLabel()
			compileBranchCondition(context, reg, ex.Lhs, nextcondlabel, elselabel, false)
			context.SetLabelPc(nextcondlabel, context.Code.LastPC())
			compileBranchCondition(context, reg, ex.Rhs, thenlabel, elselabel, hasnextcond)
		case "or":
			nextcondlabel := context.NewLabel()
			compileBranchCondition(context, reg, ex.Lhs, thenlabel, nextcondlabel, true)
			context.SetLabelPc(nextcondlabel, context.Code.LastPC())
			compileBranchCondition(context, reg, ex.Rhs, thenlabel, elselabel, hasnextcond)
		}
		return
	case *ast.RelationalOpExpr:
		compileRelationalOpExprAux(context, reg, ex, flip, jumplabel)
		return
	}

	a := reg
	compileExprWithMVPropagation(context, expr, &reg, &a)
	code.AddABC(OP_TEST, a, 0, 0^flip, exprLastLine(expr))
	code.AddASbx(OP_JMP, 0, jumplabel, exprLastLine(expr))
} // }}}

func compileWhileStmt(context *funcContext, stmt *ast.WhileStmt) { // {{{
	thenlabel := context.NewLabel()
	elselabel := context.NewLabel()
	condlabel := context.NewLabel()

	context.SetLabelPc(condlabel, context.Code.LastPC())
	compileBranchCondition(context, context.RegTop(), stmt.Condition, thenlabel, elselabel, false)
	context.SetLabelPc(thenlabel, context.Code.LastPC())
	endline := blockEndLine(stmt.Stmts, eline(stmt.Condition)) // go-moonscript patch
	context.EnterBlock(elselabel, endBlockPos(stmt, stmt.Stmts, endline))
	compileChunk(context, stmt.Stmts, false)
	context.EndScope() // go-moonscript patch: before the CLOSE and the jump, as in luac
	context.CloseUpvalues()
	context.Code.AddASbx(OP_JMP, 0, condlabel, endline)
	context.LeaveBlock()
	context.SetLabelPc(elselabel, context.Code.LastPC())
} // }}}

func compileRepeatStmt(context *funcContext, stmt *ast.RepeatStmt) { // {{{
	initlabel := context.NewLabel()
	thenlabel := context.NewLabel()
	elselabel := context.NewLabel()

	context.SetLabelPc(initlabel, context.Code.LastPC())
	context.SetLabelPc(elselabel, context.Code.LastPC())
	context.EnterBlock(thenlabel, stmt)
	compileChunk(context, stmt.Stmts, true)
	compileBranchCondition(context, context.RegTop(), stmt.Condition, thenlabel, elselabel, false)

	context.SetLabelPc(thenlabel, context.Code.LastPC())
	n := context.LeaveBlock()

	if n > -1 {
		endline := eline(stmt.Condition) // go-moonscript patch: the line of the condition, as luac
		label := context.NewLabel()
		context.Code.AddASbx(OP_JMP, 0, label, endline)
		context.SetLabelPc(elselabel, context.Code.LastPC())
		context.Code.AddABC(OP_CLOSE, n, 0, 0, endline)
		context.Code.AddASbx(OP_JMP, 0, initlabel, endline)
		context.SetLabelPc(label, context.Code.LastPC())
	}

} // }}}

func compileBreakStmt(context *funcContext, stmt *ast.BreakStmt) { // {{{
	for block := context.Block; block != nil; block = block.Parent {
		if label := block.BreakLabel; label != labelNoJump {
			if block.RefUpvalue {
				context.Code.AddABC(OP_CLOSE, block.Parent.LocalVars.LastIndex(), 0, 0, sline(stmt))
			}
			context.Code.AddASbx(OP_JMP, 0, label, sline(stmt))
			return
		}
	}
	raiseCompileError(context, sline(stmt), "no loop to break")
} // }}}

func compileFuncDefStmt(context *funcContext, stmt *ast.FuncDefStmt) { // {{{
	if stmt.Name.Func == nil {
		reg := context.RegTop()
		var treg, kreg int
		compileExprWithKMVPropagation(context, stmt.Name.Receiver, &reg, &treg)
		kreg = loadRk(context, &reg, stmt.Func, LString(stmt.Name.Method))
		compileExpr(context, reg, stmt.Func, ecfuncdef)
		context.Code.AddABC(OP_SETTABLE, treg, kreg, reg, sline(stmt.Name.Receiver))
	} else {
		astmt := &ast.AssignStmt{Lhs: []ast.Expr{stmt.Name.Func}, Rhs: []ast.Expr{stmt.Func}}
		astmt.SetLine(sline(stmt.Func))
		astmt.SetLastLine(eline(stmt.Func))
		compileAssignStmtAt(context, astmt, true)
	}
} // }}}

func compileNumberForStmt(context *funcContext, stmt *ast.NumberForStmt) { // {{{
	code := context.Code
	endlabel := context.NewLabel()
	ec := &expcontext{}

	context.EnterBlock(endlabel, endBlockPos(stmt, stmt.Stmts, sline(stmt))) // go-moonscript patch
	// go-moonscript patch: the expressions are compiled before the loop's
	// registers become locals, as in luac (debug.getlocal)
	rindex := context.RegTop()
	ecupdate(ec, ecLocal, rindex, 0)
	compileExpr(context, rindex, stmt.Init, ec)

	rlimit := rindex + 1
	ecupdate(ec, ecLocal, rlimit, 0)
	compileExpr(context, rlimit, stmt.Limit, ec)

	rstep := rindex + 2
	if stmt.Step == nil {
		stmt.Step = &ast.NumberExpr{Value: "1"}
		stmt.Step.SetLine(sline(stmt.Init))
	}
	ecupdate(ec, ecLocal, rstep, 0)
	compileExpr(context, rstep, stmt.Step, ec)

	firstdbg := len(context.Proto.DbgLocals)
	context.RegisterLocalVar("(for index)")
	context.RegisterLocalVar("(for limit)")
	context.RegisterLocalVar("(for step)")

	// go-moonscript patch: luac emits FORPREP after "do", see above
	prepline := exprLastLine(stmt.Limit)
	if l := exprLastLine(stmt.Step); l > prepline {
		prepline = l
	}
	code.AddASbx(OP_FORPREP, rindex, 0, prepline)

	context.RegisterLocalVar(stmt.Name)

	bodypc := code.LastPC()
	compileChunk(context, stmt.Stmts, false)

	context.LeaveBlock()

	flpc := code.LastPC()
	code.AddASbx(OP_FORLOOP, rindex, bodypc-(flpc+1), sline(stmt))
	context.endLoopScope(firstdbg, 3) // go-moonscript patch

	context.SetLabelPc(endlabel, code.LastPC())
	code.SetSbx(bodypc, flpc-bodypc)

} // }}}

func compileGenericForStmt(context *funcContext, stmt *ast.GenericForStmt) { // {{{
	code := context.Code
	endlabel := context.NewLabel()
	bodylabel := context.NewLabel()
	fllabel := context.NewLabel()
	nnames := len(stmt.Names)

	context.EnterBlock(endlabel, endBlockPos(stmt, stmt.Stmts, sline(stmt))) // go-moonscript patch
	// go-moonscript patch (yuin/gopher-lua#564): the expressions are
	// compiled before the loop's registers become locals, as in luac;
	// compileExpr takes registers below RegTop for locals (a call there
	// keeps no result register, so "for x in f()[1]:m() do" lost f()'s
	// result)
	compileRegAssignment(context, stmt.Exprs, context.RegTop(), 3, sline(stmt))
	firstdbg := len(context.Proto.DbgLocals) // go-moonscript patch
	rgen := context.RegisterLocalVar("(for generator)")
	context.RegisterLocalVar("(for state)")
	context.RegisterLocalVar("(for control)")

	// go-moonscript patch: luac emits the jump after "do"; the line of the
	// last iterator expression stands in for it
	code.AddASbx(OP_JMP, 0, fllabel, exprLastLine(stmt.Exprs[len(stmt.Exprs)-1]))

	for _, name := range stmt.Names {
		context.RegisterLocalVar(name)
	}

	context.SetLabelPc(bodylabel, code.LastPC())
	compileChunk(context, stmt.Stmts, false)

	context.LeaveBlock()

	context.SetLabelPc(fllabel, code.LastPC())
	// go-moonscript patch: luac gives the call of the iterator the line of
	// the token after "in" (forlist's line)
	code.AddABC(OP_TFORLOOP, rgen, 0, nnames, sline(stmt.Exprs[0]))
	// go-moonscript patch: luac emits this jump after the body's last line
	code.AddASbx(OP_JMP, 0, bodylabel, blockEndLine(stmt.Stmts, sline(stmt)))
	context.endLoopScope(firstdbg, 3) // go-moonscript patch

	context.SetLabelPc(endlabel, code.LastPC())
} // }}}

func compileLabelStmt(context *funcContext, stmt *ast.LabelStmt, isLastStmt bool) { // {{{
	labelId := context.NewLabel()
	label := newLabelDesc(labelId, stmt.Name, context.Code.LastPC(), sline(stmt), context.BlockLocalVarsCount())
	context.AddNamedLabel(label)
	if isLastStmt {
		label.SetNumActiveLocalVars(context.Block.Parent.LocalVarsCount())
	}
	context.ResolveForwardGoto(label)
} // }}}

func compileGotoStmt(context *funcContext, stmt *ast.GotoStmt) { // {{{
	context.Code.AddABC(OP_CLOSE, 0, 0, 0, sline(stmt))
	context.Code.AddASbx(OP_JMP, 0, labelNoJump, sline(stmt))
	label := newLabelDesc(-1, stmt.Label, context.Code.LastPC(), sline(stmt), context.BlockLocalVarsCount())
	context.AddUnresolvedGoto(label)
	context.FindLabel(context.Block, label, context.gotosCount-1)
} // }}}

func compileExpr(context *funcContext, reg int, expr ast.Expr, ec *expcontext) int { // {{{
	context.enterLevel(sline(expr)) // go-moonscript patch
	defer context.leaveLevel()
	code := context.Code
	sreg := savereg(ec, reg)
	sused := 1
	if sreg < reg {
		sused = 0
	}

	switch ex := expr.(type) {
	case *ast.StringExpr:
		code.AddABx(OP_LOADK, sreg, context.ConstIndex(LString(ex.Value)), sline(ex))
		return sused
	case *ast.NumberExpr:
		num, err := parseNumber(ex.Value)
		if err != nil {
			num = LNumber(math.NaN())
		}
		code.AddABx(OP_LOADK, sreg, context.ConstIndex(num), sline(ex))
		return sused
	case *ast.CdataNumberExpr:
		// go-moonscript patch: a LuaJIT FFI numeral calls cdataNumeral,
		// which raises the error, in free registers (a call may write
		// above its function's register)
		code.AddABx(OP_LOADK, reg, context.ConstIndex(cdataNumeralFn), sline(ex))
		code.AddABx(OP_LOADK, reg+1, context.ConstIndex(LString(ex.Value)), sline(ex))
		code.AddABC(OP_CALL, reg, 2, 2, sline(ex))
		if sreg != reg {
			code.AddABC(OP_MOVE, sreg, reg, 0, sline(ex))
		}
		return sused
	case *constLValueExpr:
		code.AddABx(OP_LOADK, sreg, context.ConstIndex(ex.Value), sline(ex))
		return sused
	case *ast.NilExpr:
		context.loadNil(sreg, sreg, sline(ex)) // go-moonscript patch
		return sused
	case *ast.FalseExpr:
		code.AddABC(OP_LOADBOOL, sreg, 0, 0, sline(ex))
		return sused
	case *ast.TrueExpr:
		code.AddABC(OP_LOADBOOL, sreg, 1, 0, sline(ex))
		return sused
	case *ast.IdentExpr:
		switch getIdentRefType(context, context, ex) {
		case ecGlobal:
			code.AddABx(OP_GETGLOBAL, sreg, context.ConstIndex(LString(ex.Value)), sline(ex))
		case ecUpvalue:
			code.AddABC(OP_GETUPVAL, sreg, context.Upvalues.RegisterUnique(ex.Value), 0, sline(ex))
		case ecLocal:
			b := context.FindLocalVar(ex.Value)
			code.AddABC(OP_MOVE, sreg, b, 0, sline(ex))
		}
		return sused
	case *ast.Comma3Expr:
		if context.Proto.IsVarArg == 0 {
			raiseCompileError(context, sline(ex), "cannot use '...' outside a vararg function")
		}
		context.Proto.IsVarArg &= ^VarArgNeedsArg
		code.AddABC(OP_VARARG, sreg, 2+ec.varargopt, 0, sline(ex))
		if context.RegTop() > (sreg+2+ec.varargopt) || ec.varargopt < -1 {
			return 0
		}
		return (sreg + 1 + ec.varargopt) - reg
	case *ast.AttrGetExpr:
		a := sreg
		b := reg
		compileExprWithMVPropagation(context, ex.Object, &reg, &b)
		c := reg
		compileExprWithKMVPropagation(context, ex.Key, &reg, &c)
		opcode := OP_GETTABLE
		if _, ok := ex.Key.(*ast.StringExpr); ok {
			opcode = OP_GETTABLEKS
		}
		code.AddABC(opcode, a, b, c, sline(ex))
		return sused
	case *ast.TableExpr:
		compileTableExpr(context, reg, ex, ec)
		return 1
	case *ast.ArithmeticOpExpr:
		compileArithmeticOpExpr(context, reg, ex, ec)
		return sused
	case *ast.StringConcatOpExpr:
		compileStringConcatOpExpr(context, reg, ex, ec)
		return sused
	case *ast.UnaryMinusOpExpr, *ast.UnaryNotOpExpr, *ast.UnaryLenOpExpr:
		compileUnaryOpExpr(context, reg, ex, ec)
		return sused
	case *ast.RelationalOpExpr:
		compileRelationalOpExpr(context, reg, ex, ec)
		return sused
	case *ast.LogicalOpExpr:
		compileLogicalOpExpr(context, reg, ex, ec)
		return sused
	case *ast.FuncCallExpr:
		return compileFuncCallExpr(context, reg, ex, ec)
	case *ast.FunctionExpr:
		childcontext := newFuncContext(context.Proto.SourceName, context)
		compileFunctionExpr(childcontext, ex, ec)
		protono := len(context.Proto.FunctionPrototypes)
		context.Proto.FunctionPrototypes = append(context.Proto.FunctionPrototypes, childcontext.Proto)
		// go-moonscript patch: luac creates the closure after the body, on
		// the line of its "end"
		line := eline(ex)
		code.AddABx(OP_CLOSURE, sreg, protono, line)
		for _, upvalue := range childcontext.Upvalues.List() {
			localidx, block := context.FindLocalVarAndBlock(upvalue.Name)
			if localidx > -1 {
				code.AddABC(OP_MOVE, 0, localidx, 0, line)
				block.RefUpvalue = true
			} else {
				upvalueidx := context.Upvalues.Find(upvalue.Name)
				if upvalueidx < 0 {
					upvalueidx = context.Upvalues.RegisterUnique(upvalue.Name)
				}
				code.AddABC(OP_GETUPVAL, 0, upvalueidx, 0, line)
			}
		}
		return sused
	default:
		panic(fmt.Sprintf("expr %v not implemented.", reflect.TypeOf(ex).Elem().Name()))
	}

} // }}}

func compileExprWithPropagation(context *funcContext, expr ast.Expr, reg *int, save *int, propergator func(int, *int, *int, int)) { // {{{
	reginc := compileExpr(context, *reg, expr, ecnone(0))
	if _, ok := expr.(*ast.LogicalOpExpr); ok {
		*save = *reg
		*reg = *reg + reginc
	} else {
		propergator(context.RegTop(), save, reg, reginc)
	}
} // }}}

// literalConst is the value of a nil, true or false expression.
func literalConst(expr ast.Expr) (LValue, bool) {
	switch expr.(type) {
	case *ast.NilExpr:
		return LNil, true
	case *ast.TrueExpr:
		return LTrue, true
	case *ast.FalseExpr:
		return LFalse, true
	}
	return nil, false
}

func compileExprWithKMVPropagation(context *funcContext, expr ast.Expr, reg *int, save *int) { // {{{
	// go-moonscript patch: nil, true and false are constants in an RK
	// operand, as in luac (luaK_exp2RK), not loaded into a register
	if k, ok := literalConst(expr); ok {
		if idx := context.ConstIndex(k); idx <= opMaxIndexRk {
			*save = opRkAsk(idx)
			return
		}
	}
	compileExprWithPropagation(context, expr, reg, save, context.Code.PropagateKMV)
} // }}}

func compileExprWithMVPropagation(context *funcContext, expr ast.Expr, reg *int, save *int) { // {{{
	compileExprWithPropagation(context, expr, reg, save, context.Code.PropagateMV)
} // }}}

// constFold returns exp with its constant arithmetic folded. go-moonscript
// patch: each operator expression is folded once (compileArithmeticOpExpr
// folds every level of a chain), and not deeper than maxCompileDepth (the
// compiler fails there anyway). From mdogan/gopher-lua 3f80512213.
func (fc *funcContext) constFold(exp ast.Expr) ast.Expr { // {{{
	return fc.constFoldAt(exp, 0)
} // }}}

func (fc *funcContext) constFoldAt(exp ast.Expr, depth int) ast.Expr { // {{{
	switch exp.(type) {
	case *ast.ArithmeticOpExpr, *ast.UnaryMinusOpExpr:
	default:
		return exp
	}
	if depth > maxCompileDepth {
		return exp
	}
	if folded, ok := fc.state.folded[exp]; ok {
		return folded
	}
	folded := fc.constFoldOnce(exp, depth)
	fc.state.folded[exp] = folded
	return folded
} // }}}

func (fc *funcContext) constFoldOnce(exp ast.Expr, depth int) ast.Expr { // {{{
	switch expr := exp.(type) {
	case *ast.ArithmeticOpExpr:
		lvalue, lisconst := lnumberValue(fc.constFoldAt(expr.Lhs, depth+1))
		rvalue, risconst := lnumberValue(fc.constFoldAt(expr.Rhs, depth+1))
		if lisconst && risconst {
			switch expr.Operator {
			case "+":
				return &constLValueExpr{Value: lvalue + rvalue}
			case "-":
				return &constLValueExpr{Value: lvalue - rvalue}
			case "*":
				return &constLValueExpr{Value: lvalue * rvalue}
			case "/":
				return &constLValueExpr{Value: lvalue / rvalue}
			case "%":
				return &constLValueExpr{Value: luaModulo(lvalue, rvalue)}
			case "^":
				return &constLValueExpr{Value: LNumber(math.Pow(float64(lvalue), float64(rvalue)))}
			default:
				panic(fmt.Sprintf("unknown binop: %v", expr.Operator))
			}
		} else {
			return expr
		}
	case *ast.UnaryMinusOpExpr:
		expr.Expr = fc.constFoldAt(expr.Expr, depth+1)
		if value, ok := lnumberValue(expr.Expr); ok {
			return &constLValueExpr{Value: LNumber(-value)}
		}
		return expr
	default:

		return exp
	}
} // }}}

func compileFunctionExpr(context *funcContext, funcexpr *ast.FunctionExpr, ec *expcontext) { // {{{
	context.Proto.LineDefined = sline(funcexpr)
	context.Proto.LastLineDefined = eline(funcexpr)
	context.stmtLine = sline(funcexpr) // go-moonscript patch
	if len(funcexpr.ParList.Names) > maxRegisters {
		raiseCompileError(context, context.Proto.LineDefined, "register overflow")
	}
	context.Proto.NumParameters = uint8(len(funcexpr.ParList.Names))
	if ec.ctype == ecMethod {
		context.Proto.NumParameters += 1
		context.RegisterLocalVar("self")
	}
	for _, name := range funcexpr.ParList.Names {
		context.RegisterLocalVar(name)
	}
	if funcexpr.ParList.HasVargs {
		if CompatVarArg {
			context.Proto.IsVarArg = VarArgHasArg | VarArgNeedsArg
			if context.Parent != nil {
				context.RegisterLocalVar("arg")
			} else {
				// go-moonscript patch: the main chunk has no local arg, so
				// the VM needn't fill its register (loadNil relies on it
				// being nil)
				context.Proto.IsVarArg = VarArgHasArg
			}
		}
		context.Proto.IsVarArg |= VarArgIsVarArg
	}
	context.entryRegs = context.RegTop() // go-moonscript patch

	compileChunk(context, funcexpr.Stmts, false)

	retline := eline(funcexpr)
	if context.Parent == nil {
		// go-moonscript patch: the main chunk's final return is on the line
		// of its last token, as in luac
		retline = blockEndLine(funcexpr.Stmts, 1)
	}
	context.EndScope() // go-moonscript patch: before the final RETURN, as in luac
	context.Code.AddABC(OP_RETURN, 0, 1, 0, retline)
	context.CheckUnresolvedGoto()
	context.Proto.Code = context.Code.List()
	context.Proto.DbgSourcePositions = context.Code.PosList()
	context.Proto.DbgUpvalues = context.Upvalues.Names()
	if len(context.Proto.DbgUpvalues) > math.MaxUint8 {
		// go-moonscript patch (yuin/gopher-lua#556): NumUpvalues would
		// overflow (Lua 5.1 allows 60 upvalues, gopher-lua 255)
		raiseCompileError(context, context.Proto.LineDefined, "too many upvalues")
	}
	context.Proto.NumUpvalues = uint8(len(context.Proto.DbgUpvalues))
	for _, clv := range context.Proto.Constants {
		sv := ""
		if slv, ok := clv.(LString); ok {
			sv = string(slv)
		}
		context.Proto.stringConstants = append(context.Proto.stringConstants, sv)
		context.Proto.stringConstHashes = append(context.Proto.stringConstHashes, hashString(sv)) // go-moonscript patch
	}
	patchCode(context)
} // }}}

func compileTableExpr(context *funcContext, reg int, ex *ast.TableExpr, ec *expcontext) { // {{{
	code := context.Code
	/*
		tablereg := savereg(ec, reg)
		if tablereg == reg {
			reg += 1
		}
	*/
	tablereg := reg
	reg++
	newline := sline(ex)
	if ex.PrevLine > 0 { // go-moonscript patch: as luac, see ast.TableExpr
		newline = ex.PrevLine
	}
	code.AddABC(OP_NEWTABLE, tablereg, 0, 0, newline)
	tablepc := code.LastPC()
	regbase := reg

	arraycount := 0
	hashcount := 0 // go-moonscript patch: key = value fields (luac's nh)
	lastvararg := false
	extendedSetlist := false // go-moonscript patch (yuin/gopher-lua#537)
	flushed := 0             // go-moonscript patch: positional items stored so far
	for i, field := range ex.Fields {
		islast := i == len(ex.Fields)-1
		if field.Key == nil {
			if islast && isVarArgReturnExpr(field.Value) {
				reg += compileExpr(context, reg, field.Value, ecnone(-2))
				lastvararg = true
			} else {
				if islast { // go-moonscript patch: luac loads it at the "}"
					reg += compileLazyExpr(context, reg, field.Value, ecnone(0), eline(ex))
				} else {
					reg += compileExpr(context, reg, field.Value, ecnone(0))
				}
				arraycount += 1
			}
		} else {
			hashcount++
			regorg := reg
			b := reg
			compileExprWithKMVPropagation(context, field.Key, &reg, &b)
			c := reg
			compileExprWithKMVPropagation(context, field.Value, &reg, &c)
			opcode := OP_SETTABLE
			if _, ok := field.Key.(*ast.StringExpr); ok {
				opcode = OP_SETTABLEKS
			}
			// go-moonscript patch: on the line of the value, as luac does
			code.AddABC(opcode, tablereg, b, c, exprLastLine(field.Value))
			reg = regorg
		}
		// go-moonscript patch: store the positional items not stored yet.
		// gopher-lua stored the last 50 again when a key = value field
		// followed them (undoing a [k] = v of the field), stored the results
		// of a call or ... after 50*n items at the first page, and took a
		// call or ... ending the constructor as a key's value for a
		// multiple-results positional item (B = 0).
		pending := arraycount - flushed
		if (pending > 0 && (pending == FieldsPerFlush || islast)) || lastvararg {
			reg = regbase
			c := flushed/FieldsPerFlush + 1
			b := pending
			if lastvararg {
				b = 0
			}
			flushed = arraycount
			line := field.Value
			if field.Key != nil {
				line = field.Key
			}
			setline := sline(line)
			if islast {
				// go-moonscript patch: luac stores the last items after the
				// closing brace
				setline = eline(ex)
			}
			// go-moonscript patch (yuin/gopher-lua#537): past 511 flushes
			// (25550 items), C is 0 and the next word holds the real C; it
			// was 0 too, which stored the items at the wrong indices
			if c > 511 {
				code.AddABC(OP_SETLIST, tablereg, b, 0, setline)
				code.Add(uint32(c), setline)
				extendedSetlist = true
			} else {
				code.AddABC(OP_SETLIST, tablereg, b, c, setline)
			}
		}
	}
	code.SetB(tablepc, int2Fb(arraycount))
	code.SetC(tablepc, int2Fb(hashcount)) // go-moonscript patch: a final call or ... was counted
	if shouldmove(ec, tablereg) {
		code.AddABC(OP_MOVE, ec.reg, tablereg, 0, eline(ex)) // go-moonscript patch: after the "}", as luac
	} else if extendedSetlist {
		// go-moonscript patch (yuin/gopher-lua#537): the last word mustn't be
		// SETLIST's C word, which looks like a MOVE to the peephole
		// optimizations (PropagateKMV would pop it); a no-op MOVE instead
		code.AddABC(OP_MOVE, tablereg, tablereg, 0, eline(ex))
	}
} // }}}

func compileArithmeticOpExpr(context *funcContext, reg int, expr *ast.ArithmeticOpExpr, ec *expcontext) { // {{{
	exp := context.constFold(expr)
	if ex, ok := exp.(*constLValueExpr); ok {
		exp.SetLine(sline(expr))
		compileExpr(context, reg, ex, ec)
		return
	}
	expr, _ = exp.(*ast.ArithmeticOpExpr)
	a := savereg(ec, reg)
	b := reg
	compileExprWithKMVPropagation(context, expr.Lhs, &reg, &b)
	c := reg
	compileExprWithKMVPropagation(context, expr.Rhs, &reg, &c)

	op := 0
	switch expr.Operator {
	case "+":
		op = OP_ADD
	case "-":
		op = OP_SUB
	case "*":
		op = OP_MUL
	case "/":
		op = OP_DIV
	case "%":
		op = OP_MOD
	case "^":
		op = OP_POW
	}
	context.Code.AddABC(op, a, b, c, sline(expr))
} // }}}

func compileStringConcatOpExpr(context *funcContext, reg int, expr *ast.StringConcatOpExpr, ec *expcontext) { // {{{
	code := context.Code
	crange := 1
	for current := expr.Rhs; current != nil; {
		if ex, ok := current.(*ast.StringConcatOpExpr); ok {
			crange += 1
			current = ex.Rhs
		} else {
			current = nil
		}
	}
	a := savereg(ec, reg)
	basereg := reg
	reg += compileExpr(context, reg, expr.Lhs, ecnone(0))
	reg += compileExpr(context, reg, expr.Rhs, ecnone(0))
	for pc := code.LastPC(); pc != 0 && opGetOpCode(code.At(pc)) == OP_CONCAT; pc-- {
		code.Pop()
	}
	code.AddABC(OP_CONCAT, a, basereg, basereg+crange, sline(expr))
} // }}}

func compileUnaryOpExpr(context *funcContext, reg int, expr ast.Expr, ec *expcontext) { // {{{
	opcode := 0
	code := context.Code
	var operandexpr ast.Expr
	switch ex := expr.(type) {
	case *ast.UnaryMinusOpExpr:
		exp := context.constFold(ex)
		if lvexpr, ok := exp.(*constLValueExpr); ok {
			exp.SetLine(sline(expr))
			compileExpr(context, reg, lvexpr, ec)
			return
		}
		ex, _ = exp.(*ast.UnaryMinusOpExpr)
		operandexpr = ex.Expr
		opcode = OP_UNM
	case *ast.UnaryNotOpExpr:
		switch ex.Expr.(type) {
		case *ast.TrueExpr:
			code.AddABC(OP_LOADBOOL, savereg(ec, reg), 0, 0, sline(expr))
			return
		case *ast.FalseExpr, *ast.NilExpr:
			code.AddABC(OP_LOADBOOL, savereg(ec, reg), 1, 0, sline(expr))
			return
		default:
			opcode = OP_NOT
			operandexpr = ex.Expr
		}
	case *ast.UnaryLenOpExpr:
		opcode = OP_LEN
		operandexpr = ex.Expr
	}

	a := savereg(ec, reg)
	b := reg
	compileExprWithMVPropagation(context, operandexpr, &reg, &b)
	code.AddABC(opcode, a, b, 0, sline(expr))
} // }}}

func compileRelationalOpExprAux(context *funcContext, reg int, expr *ast.RelationalOpExpr, flip int, label int) { // {{{
	// go-moonscript patch: comparisons, tests and jumps of conditions are on
	// the last line of the expression, as luac emits them after it
	code := context.Code
	b := reg
	compileExprWithKMVPropagation(context, expr.Lhs, &reg, &b)
	c := reg
	compileExprWithKMVPropagation(context, expr.Rhs, &reg, &c)
	switch expr.Operator {
	case "<":
		code.AddABC(OP_LT, 0^flip, b, c, exprLastLine(expr))
	case ">":
		code.AddABC(OP_LT, 0^flip, c, b, exprLastLine(expr))
	case "<=":
		code.AddABC(OP_LE, 0^flip, b, c, exprLastLine(expr))
	case ">=":
		code.AddABC(OP_LE, 0^flip, c, b, exprLastLine(expr))
	case "==":
		code.AddABC(OP_EQ, 0^flip, b, c, exprLastLine(expr))
	case "~=":
		code.AddABC(OP_EQ, 1^flip, b, c, exprLastLine(expr))
	}
	code.AddASbx(OP_JMP, 0, label, exprLastLine(expr))
} // }}}

func compileRelationalOpExpr(context *funcContext, reg int, expr *ast.RelationalOpExpr, ec *expcontext) { // {{{
	a := savereg(ec, reg)
	code := context.Code
	jumplabel := context.NewLabel()
	compileRelationalOpExprAux(context, reg, expr, 1, jumplabel)
	code.AddABC(OP_LOADBOOL, a, 0, 1, exprLastLine(expr))
	context.SetLabelPc(jumplabel, code.LastPC())
	code.AddABC(OP_LOADBOOL, a, 1, 0, exprLastLine(expr))
} // }}}

func compileLogicalOpExpr(context *funcContext, reg int, expr *ast.LogicalOpExpr, ec *expcontext) { // {{{
	a := savereg(ec, reg)
	code := context.Code
	endlabel := context.NewLabel()
	lb := &lblabels{context.NewLabel(), context.NewLabel(), endlabel, false}
	nextcondlabel := context.NewLabel()
	if expr.Operator == "and" {
		compileLogicalOpExprAux(context, reg, expr.Lhs, ec, nextcondlabel, endlabel, false, lb)
		context.SetLabelPc(nextcondlabel, code.LastPC())
		compileLogicalOpExprAux(context, reg, expr.Rhs, ec, endlabel, endlabel, false, lb)
	} else {
		compileLogicalOpExprAux(context, reg, expr.Lhs, ec, endlabel, nextcondlabel, true, lb)
		context.SetLabelPc(nextcondlabel, code.LastPC())
		compileLogicalOpExprAux(context, reg, expr.Rhs, ec, endlabel, endlabel, false, lb)
	}

	if lb.b {
		context.SetLabelPc(lb.f, code.LastPC())
		code.AddABC(OP_LOADBOOL, a, 0, 1, sline(expr))
		context.SetLabelPc(lb.t, code.LastPC())
		code.AddABC(OP_LOADBOOL, a, 1, 0, sline(expr))
	}

	lastinst := code.Last()
	if opGetOpCode(lastinst) == OP_JMP && opGetArgSbx(lastinst) == endlabel {
		code.Pop()
	}

	context.SetLabelPc(endlabel, code.LastPC())
} // }}}

func compileLogicalOpExprAux(context *funcContext, reg int, expr ast.Expr, ec *expcontext, thenlabel, elselabel int, hasnextcond bool, lb *lblabels) { // {{{
	context.enterLevel(sline(expr)) // go-moonscript patch
	defer context.leaveLevel()
	// TODO folding constants?
	code := context.Code
	flip := 0
	jumplabel := elselabel
	if hasnextcond {
		flip = 1
		jumplabel = thenlabel
	}

	switch ex := expr.(type) {
	case *ast.FalseExpr:
		if elselabel == lb.e {
			code.AddASbx(OP_JMP, 0, lb.f, sline(expr))
			lb.b = true
		} else {
			code.AddASbx(OP_JMP, 0, elselabel, sline(expr))
		}
		return
	case *ast.NilExpr:
		if elselabel == lb.e {
			compileExpr(context, reg, expr, ec)
			code.AddASbx(OP_JMP, 0, lb.e, sline(expr))
		} else {
			code.AddASbx(OP_JMP, 0, elselabel, sline(expr))
		}
		return
	case *ast.TrueExpr:
		if thenlabel == lb.e {
			code.AddASbx(OP_JMP, 0, lb.t, sline(expr))
			lb.b = true
		} else {
			code.AddASbx(OP_JMP, 0, thenlabel, sline(expr))
		}
		return
	case *ast.NumberExpr, *ast.StringExpr:
		if thenlabel == lb.e {
			compileExpr(context, reg, expr, ec)
			code.AddASbx(OP_JMP, 0, lb.e, sline(expr))
		} else {
			code.AddASbx(OP_JMP, 0, thenlabel, sline(expr))
		}
		return
	case *ast.LogicalOpExpr:
		switch ex.Operator {
		case "and":
			nextcondlabel := context.NewLabel()
			compileLogicalOpExprAux(context, reg, ex.Lhs, ec, nextcondlabel, elselabel, false, lb)
			context.SetLabelPc(nextcondlabel, context.Code.LastPC())
			compileLogicalOpExprAux(context, reg, ex.Rhs, ec, thenlabel, elselabel, hasnextcond, lb)
		case "or":
			nextcondlabel := context.NewLabel()
			compileLogicalOpExprAux(context, reg, ex.Lhs, ec, thenlabel, nextcondlabel, true, lb)
			context.SetLabelPc(nextcondlabel, context.Code.LastPC())
			compileLogicalOpExprAux(context, reg, ex.Rhs, ec, thenlabel, elselabel, hasnextcond, lb)
		}
		return
	case *ast.RelationalOpExpr:
		if thenlabel == elselabel {
			flip ^= 1
			jumplabel = lb.t
			lb.b = true
		} else if thenlabel == lb.e {
			jumplabel = lb.t
			lb.b = true
		} else if elselabel == lb.e {
			jumplabel = lb.f
			lb.b = true
		}
		compileRelationalOpExprAux(context, reg, ex, flip, jumplabel)
		return
	}

	a := reg
	sreg := savereg(ec, a)
	isLastAnd := elselabel == lb.e && thenlabel != elselabel
	isLastOr := thenlabel == lb.e && hasnextcond

	if ident, ok := expr.(*ast.IdentExpr); ok && (isLastAnd || isLastOr) && getIdentRefType(context, context, ident) == ecLocal {
		b := context.FindLocalVar(ident.Value)
		op := OP_TESTSET
		if sreg == b {
			op = OP_TEST
		}
		code.AddABC(op, sreg, b, 0^flip, exprLastLine(expr))
	} else if !hasnextcond && thenlabel == elselabel {
		reg += compileExpr(context, reg, expr, &expcontext{ec.ctype, intMax(a, sreg), ec.varargopt})
		last := context.Code.Last()
		if opGetOpCode(last) == OP_MOVE && opGetArgA(last) == a {
			context.Code.SetA(context.Code.LastPC(), sreg)
		} else {
			context.Code.AddABC(OP_MOVE, sreg, a, 0, exprLastLine(expr))
		}
	} else {
		reg += compileExpr(context, reg, expr, ecnone(0))
		// go-moonscript patch (yuin/gopher-lua#553): the last operand of an
		// "and" whose false value ends the expression must be copied to the
		// target register (TESTSET), not just tested
		if !hasnextcond && !isLastAnd {
			code.AddABC(OP_TEST, a, 0, 0^flip, exprLastLine(expr))
		} else {
			code.AddABC(OP_TESTSET, sreg, a, 0^flip, exprLastLine(expr))
		}
	}
	code.AddASbx(OP_JMP, 0, jumplabel, exprLastLine(expr))
} // }}}

func compileFuncCallExpr(context *funcContext, reg int, expr *ast.FuncCallExpr, ec *expcontext) int { // {{{
	funcreg := reg
	if ec.ctype == ecLocal && ec.reg == (int(context.Proto.NumParameters)-1) {
		funcreg = ec.reg
		reg = ec.reg
	}
	argc := len(expr.Args)
	islastvararg := false
	name := "(anonymous)"

	if expr.Func != nil { // hoge.func()
		reg += compileExpr(context, reg, expr.Func, ecnone(0))
		name = getExprName(context, expr.Func)
	} else { // hoge:method()
		b := reg
		compileExprWithMVPropagation(context, expr.Receiver, &reg, &b)
		c := loadRk(context, &reg, expr, LString(expr.Method))
		context.Code.AddABC(OP_SELF, funcreg, b, c, sline(expr))
		// increments a register for an implicit "self"
		reg = b + 1
		reg2 := funcreg + 2
		if reg2 > reg {
			reg = reg2
		}
		argc += 1
		name = string(expr.Method)
	}

	endline := expr.ArgsEndLine // go-moonscript patch
	if endline == 0 {
		endline = sline(expr)
	}
	for i, ar := range expr.Args {
		islastvararg = (i == len(expr.Args)-1) && isVarArgReturnExpr(ar)
		if islastvararg {
			compileExpr(context, reg, ar, ecnone(-2))
		} else if i == len(expr.Args)-1 {
			reg += compileLazyExpr(context, reg, ar, ecnone(0), endline) // go-moonscript patch
		} else {
			reg += compileExpr(context, reg, ar, ecnone(0))
		}
	}
	b := argc + 1
	if islastvararg {
		b = 0
	}
	// go-moonscript patch: on the line where the arguments start, as luac
	callline := sline(expr)
	if expr.ArgsLine > 0 {
		callline = expr.ArgsLine
	}
	context.Code.AddABC(OP_CALL, funcreg, b, ec.varargopt+2, callline)
	context.Proto.DbgCalls = append(context.Proto.DbgCalls, DbgCall{Pc: context.Code.LastPC(), Name: name})

	if ec.varargopt == 0 && shouldmove(ec, funcreg) {
		// go-moonscript patch: luac moves the result after the call
		context.Code.AddABC(OP_MOVE, ec.reg, funcreg, 0, endline)
		return 1
	}
	if context.RegTop() > (funcreg+2+ec.varargopt) || ec.varargopt < -1 {
		return 0
	}
	return ec.varargopt + 1
} // }}}

func loadRk(context *funcContext, reg *int, expr ast.Expr, cnst LValue) int { // {{{
	cindex := context.ConstIndex(cnst)
	if cindex <= opMaxIndexRk {
		return opRkAsk(cindex)
	} else {
		ret := *reg
		*reg++
		context.Code.AddABx(OP_LOADK, ret, cindex, sline(expr))
		return ret
	}
} // }}}

func getIdentRefType(context *funcContext, current *funcContext, expr *ast.IdentExpr) expContextType { // {{{
	if current == nil {
		return ecGlobal
	} else if current.FindLocalVar(expr.Value) > -1 {
		if current == context {
			return ecLocal
		}
		return ecUpvalue
	}
	return getIdentRefType(context, current.Parent, expr)
} // }}}

func getExprName(context *funcContext, expr ast.Expr) string { // {{{
	switch ex := expr.(type) {
	case *ast.IdentExpr:
		return ex.Value
	case *ast.AttrGetExpr:
		switch kex := ex.Key.(type) {
		case *ast.StringExpr:
			return kex.Value
		}
		return "?"
	}
	return "?"
} // }}}

func patchCode(context *funcContext) { // {{{
	maxreg := 1
	if np := int(context.Proto.NumParameters); np > 1 {
		maxreg = np
	}
	moven := 0
	code := context.Code.List()
	// go-moonscript patch (yuin/gopher-lua#556): a register overflow is
	// reported on the line of the first instruction using too many
	overflowLine := context.Proto.LineDefined
	lines := context.Code.PosList()
	for pc := 0; pc < len(code); pc++ {
		inst := code[pc]
		previousMax := maxreg
		skipword := false
		curop := opGetOpCode(inst)
		switch curop {
		case OP_CLOSURE:
			pc += int(context.Proto.FunctionPrototypes[opGetArgBx(inst)].NumUpvalues)
			moven = 0
			continue
		case OP_SETLIST:
			// go-moonscript patch (yuin/gopher-lua#537): skip the C word
			skipword = opGetArgC(inst) == 0
		case OP_SETGLOBAL, OP_SETUPVAL, OP_EQ, OP_LT, OP_LE, OP_TEST,
			OP_TAILCALL, OP_RETURN, OP_FORPREP, OP_FORLOOP, OP_TFORLOOP,
			OP_CLOSE:
			/* nothing to do */
		case OP_CALL:
			if reg := opGetArgA(inst) + opGetArgC(inst) - 2; reg > maxreg {
				maxreg = reg
			}
		case OP_VARARG:
			if reg := opGetArgA(inst) + opGetArgB(inst) - 1; reg > maxreg {
				maxreg = reg
			}
		case OP_SELF:
			if reg := opGetArgA(inst) + 1; reg > maxreg {
				maxreg = reg
			}
		case OP_LOADNIL:
			if reg := opGetArgB(inst); reg > maxreg {
				maxreg = reg
			}
		case OP_JMP: // jump to jump optimization
			distance := 0
			count := 0 // avoiding infinite loops
			for jmp := inst; opGetOpCode(jmp) == OP_JMP && count < 5; jmp = context.Code.At(pc + distance + 1) {
				d := context.GetLabelPc(opGetArgSbx(jmp)) - pc
				if d > opMaxArgSbx {
					if distance == 0 {
						raiseCompileError(context, context.Proto.LineDefined, "too long to jump.")
					}
					break
				}
				distance = d
				count++
				// go-moonscript patch (yuin/gopher-lua#561): a jump at a lower
				// pc was patched already and holds an offset, not a label
				if distance < 0 {
					break
				}
			}
			if distance == 0 {
				context.Code.SetOpCode(pc, OP_NOP)
			} else {
				context.Code.SetSbx(pc, distance)
			}
		default:
			if reg := opGetArgA(inst); reg > maxreg {
				maxreg = reg
			}
		}
		if previousMax < maxRegisters-1 && maxreg >= maxRegisters-1 {
			overflowLine = lines[pc]
		}

		// bulk move optimization(reducing op dipatch costs)
		if curop == OP_MOVE {
			moven++
		} else {
			if moven > 1 {
				context.Code.SetOpCode(pc-moven, OP_MOVEN)
				context.Code.SetC(pc-moven, intMin(moven-1, opMaxArgsC))
			}
			moven = 0
		}
		if skipword {
			pc++
		}
	}
	if context.elidedNilReg >= maxreg { // go-moonscript patch (loadNil)
		maxreg = context.elidedNilReg + 1
	}
	maxreg++
	if maxreg >= maxRegisters {
		// go-moonscript patch: luaK_checkstack's message
		raiseCompileError(context, overflowLine, "function or expression too complex")
	}
	context.Proto.NumUsedRegisters = uint8(maxreg)
} // }}}

func Compile(chunk []ast.Stmt, name string) (proto *FunctionProto, err error) { // {{{
	defer func() {
		if rcv := recover(); rcv != nil {
			if _, ok := rcv.(*CompileError); ok {
				err = rcv.(error)
			} else {
				panic(rcv)
			}
		}
	}()
	err = nil
	parlist := &ast.ParList{HasVargs: true, Names: []string{}}
	funcexpr := &ast.FunctionExpr{ParList: parlist, Stmts: chunk}
	if len(chunk) > 0 {
		funcexpr.SetLastLine(sline(chunk[0]))
		funcexpr.SetLastLine(eline(chunk[len(chunk)-1]) + 1)
	}
	context := newFuncContext(name, nil)
	compileFunctionExpr(context, funcexpr, ecnone(0))
	proto = context.Proto
	return
} // }}}
