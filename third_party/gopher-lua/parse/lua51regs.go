package parse

// go-moonscript patch: a model of the register allocation of Lua 5.1.5's
// code generator (lcode.c, with lparser.c's calls of it), to give lua5.1's
// "function or expression too complex" error: luaK_checkstack raises it
// while parsing, near the token the lexer is at when a function's frame
// would reach MAXSTACK (250) registers. The compiler (compile.go) allocates
// registers much as luac does, so LState.Load parses a chunk again with
// this model (Lua51Overflow) only when the compiler needed many registers
// (200) or too many, or the parser found a syntax error (lua5.1 reports an
// overflow before it): parsing with it takes a sixth longer. It emits no
// code: it follows the expression descriptors (expDesc, with lcode.c's
// kinds: vKStr is VK, a constant of any type, vVararg is VVARARG), the
// first free register and the constants, which decide whether an operand
// is a constant (RK) or takes a register. The names in the comments are
// those of the C functions.

import (
	"math"
	"strconv"
	"strings"
)

const (
	maxStack51   = 250 // MAXSTACK
	maxIndexRK51 = 255 // MAXINDEXRK
	bitRK51      = 256 // BITRK: an RK operand that is a constant
	// LFIELDS_PER_FLUSH: a constructor stores its list items every 50
	fieldsPerFlush51 = 50
	errComplex       = "function or expression too complex"
)

// tags of the instruction of a vRelocable expression of the model (aux):
// lcode.c looks for these opcodes
const (
	opOther51 = iota
	opNot51
	opConcat51
)

// hasJumps51 is the model's jump list: lcode.c only asks whether there is
// one, so a list is jitNoJump or 0.
const hasJumps51 = 0

// lua51Op is an operator of lcode.c's luaK_prefix, luaK_infix and
// luaK_posfix.
type lua51Op int

const (
	op51Add lua51Op = iota
	op51Sub
	op51Mul
	op51Div
	op51Mod
	op51Pow
	op51Concat
	op51Comp // ==, ~=, <, <=, >, >=
	op51And
	op51Or
	op51Unm
	op51Len
	op51Not
)

var binOps51 = map[int]lua51Op{
	'+': op51Add, '-': op51Sub, '*': op51Mul, '/': op51Div, '%': op51Mod, '^': op51Pow,
	tkConcat: op51Concat, tkAnd: op51And, tkOr: op51Or,
	tkNe: op51Comp, tkEq: op51Comp, '<': op51Comp, tkLe: op51Comp, '>': op51Comp, tkGe: op51Comp,
}

// Lua51Overflow parses a chunk of Lua 5.1 that the compiler found to need
// too many registers with the model of lua5.1's register allocation, and
// returns lua5.1's error: "function or expression too complex" near the
// token lua5.1 is at, on its line. It returns nil if the model finds no
// such point.
func Lua51Overflow(src []byte, name string) (perr *Error) {
	p := &parser{r51: true, level: lua51BaseLevel}
	defer func() {
		if e := recover(); e != nil {
			pe, ok := e.(*Error)
			if !ok {
				panic(e)
			}
			if pe.Message == errComplex {
				perr = pe
			}
		}
	}()
	p.ls = newLexer(src, name, Lua51, false)
	p.openFunc(0, true)
	p.ls.nextToken()
	p.chunk()
	p.check(tkEOS)
	p.closeFunc()
	return nil
}

// lua51Number is the value of a Lua 5.1 numeral (luaO_str2d).
func lua51Number(s string) float64 {
	n, err := strconv.ParseFloat(s, 64)
	if err != nil && (strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X")) {
		u, _ := strconv.ParseUint(s[2:], 16, 64)
		n = float64(u)
	}
	return n
}

/* registers */

// rCheckStack is luaK_checkstack.
func (fs *funcState) rCheckStack(p *parser, n uint32) {
	if fs.freereg+n >= maxStack51 {
		p.ls.syntaxError(errComplex)
	}
}

// rReserve is luaK_reserveregs.
func (fs *funcState) rReserve(p *parser, n uint32) {
	fs.rCheckStack(p, n)
	fs.freereg += n
}

// rFreeReg is freereg.
func (fs *funcState) rFreeReg(reg uint32) {
	if reg < bitRK51 && reg >= fs.nactvar() {
		fs.freereg--
	}
}

// rFreeExp is freeexp.
func (fs *funcState) rFreeExp(e *expDesc) {
	if e.k == vNonReloc {
		fs.rFreeReg(e.info)
	}
}

/* constants: kstr and kn as in the LuaJIT model, nkn counts them all */

func (fs *funcState) rStringK(s string) uint32 {
	if k, ok := fs.kstr[s]; ok {
		return k
	}
	if fs.kstr == nil {
		fs.kstr = map[string]uint32{}
	}
	k := fs.nkn
	fs.kstr[s] = k
	fs.nkn++
	return k
}

func (fs *funcState) rNumberK(n float64) uint32 {
	if k, ok := fs.kn[n]; ok {
		return k
	}
	if fs.kn == nil {
		fs.kn = map[float64]uint32{}
	}
	k := fs.nkn
	fs.kn[n] = k
	fs.nkn++
	return k
}

// rPrimK is nilK or boolK: nil, false, true are constants 0, 1, 2 of k51.
func (fs *funcState) rPrimK(k expKind) uint32 {
	if fs.k51[k] == 0 {
		fs.nkn++
		fs.k51[k] = fs.nkn
	}
	return fs.k51[k] - 1
}

/* expressions */

func hasMultRet51(k expKind) bool { return k == vCall || k == vVararg }

func isNumeral51(e *expDesc) bool { return e.k == vKNum && !e.hasJump() }

// rSetReturns is luaK_setreturns: a "..." takes a register.
func (fs *funcState) rSetReturns(p *parser, e *expDesc) {
	if e.k == vVararg {
		fs.rReserve(p, 1)
	}
}

// rSetOneRet is luaK_setoneret.
func (fs *funcState) rSetOneRet(e *expDesc) {
	switch e.k {
	case vCall:
		e.k, e.info = vNonReloc, e.aux
	case vVararg:
		e.k, e.aux = vRelocable, opOther51
	}
}

// rDischargeVars is luaK_dischargevars.
func (fs *funcState) rDischargeVars(e *expDesc) {
	switch e.k {
	case vLocal:
		e.k = vNonReloc
	case vUpval, vGlobal:
		e.k, e.aux = vRelocable, opOther51
	case vIndexed:
		fs.rFreeReg(e.aux)
		fs.rFreeReg(e.info)
		e.k, e.aux = vRelocable, opOther51
	case vVararg, vCall:
		fs.rSetOneRet(e)
	}
}

// rDischarge2Reg is discharge2reg.
func (fs *funcState) rDischarge2Reg(e *expDesc, reg uint32) {
	fs.rDischargeVars(e)
	switch e.k {
	case vKNum:
		fs.rNumberK(e.nval) // LOADK
	case vKNil, vKFalse, vKTrue, vKStr, vRelocable, vNonReloc:
	default: // vVoid, vJmp
		return
	}
	e.info, e.k = reg, vNonReloc
}

// rDischarge2AnyReg is discharge2anyreg.
func (fs *funcState) rDischarge2AnyReg(p *parser, e *expDesc) {
	if e.k != vNonReloc {
		fs.rReserve(p, 1)
		fs.rDischarge2Reg(e, fs.freereg-1)
	}
}

// rExp2Reg is exp2reg.
func (fs *funcState) rExp2Reg(e *expDesc, reg uint32) {
	fs.rDischarge2Reg(e, reg)
	e.t, e.f = jitNoJump, jitNoJump
	e.info, e.k = reg, vNonReloc
}

// rExp2NextReg is luaK_exp2nextreg.
func (fs *funcState) rExp2NextReg(p *parser, e *expDesc) {
	fs.rDischargeVars(e)
	fs.rFreeExp(e)
	fs.rReserve(p, 1)
	fs.rExp2Reg(e, fs.freereg-1)
}

// rExp2AnyReg is luaK_exp2anyreg.
func (fs *funcState) rExp2AnyReg(p *parser, e *expDesc) uint32 {
	fs.rDischargeVars(e)
	if e.k == vNonReloc {
		if !e.hasJump() {
			return e.info
		}
		if e.info >= fs.nactvar() {
			fs.rExp2Reg(e, e.info)
			return e.info
		}
	}
	fs.rExp2NextReg(p, e)
	return e.info
}

// rExp2Val is luaK_exp2val.
func (fs *funcState) rExp2Val(p *parser, e *expDesc) {
	if e.hasJump() {
		fs.rExp2AnyReg(p, e)
	} else {
		fs.rDischargeVars(e)
	}
}

// rExp2RK is luaK_exp2RK: a constant among the first 256 is an operand.
func (fs *funcState) rExp2RK(p *parser, e *expDesc) uint32 {
	fs.rExp2Val(p, e)
	switch e.k {
	case vKNum, vKTrue, vKFalse, vKNil:
		if fs.nkn <= maxIndexRK51 {
			if e.k == vKNum {
				e.info = fs.rNumberK(e.nval)
			} else {
				e.info = fs.rPrimK(e.k)
			}
			e.k = vKStr
			return e.info | bitRK51
		}
	case vKStr:
		if e.info <= maxIndexRK51 {
			return e.info | bitRK51
		}
	}
	return fs.rExp2AnyReg(p, e)
}

// rStoreVar is luaK_storevar.
func (fs *funcState) rStoreVar(p *parser, v, e *expDesc) {
	switch v.k {
	case vLocal:
		fs.rFreeExp(e)
		fs.rExp2Reg(e, v.info)
		return
	case vUpval, vGlobal:
		fs.rExp2AnyReg(p, e)
	default: // vIndexed
		fs.rExp2RK(p, e)
	}
	fs.rFreeExp(e)
}

// rSelf is luaK_self, after checkname's constant of the method's name.
func (fs *funcState) rSelf(p *parser, e *expDesc, key string) {
	var k expDesc
	k.init(vKStr, fs.rStringK(key))
	fs.rExp2AnyReg(p, e)
	fs.rFreeExp(e)
	fn := fs.freereg
	fs.rReserve(p, 2)
	fs.rExp2RK(p, &k)
	fs.rFreeExp(&k)
	e.info, e.k = fn, vNonReloc
}

// rIndexed is luaK_indexed.
func (fs *funcState) rIndexed(p *parser, t, k *expDesc) {
	t.aux = fs.rExp2RK(p, k)
	t.k = vIndexed
}

// rJumpOnCond is jumponcond: a NOT is replaced by a test of its operand.
func (fs *funcState) rJumpOnCond(p *parser, e *expDesc) {
	if e.k == vRelocable && e.aux == opNot51 {
		return
	}
	fs.rDischarge2AnyReg(p, e)
	fs.rFreeExp(e)
}

// rGoIfTrue is luaK_goiftrue.
func (fs *funcState) rGoIfTrue(p *parser, e *expDesc) {
	fs.rDischargeVars(e)
	switch e.k {
	case vKStr, vKNum, vKTrue: // no jump
	case vKFalse, vJmp:
		e.f = hasJumps51
	default:
		fs.rJumpOnCond(p, e)
		e.f = hasJumps51
	}
	e.t = jitNoJump
}

// rGoIfFalse is luaK_goiffalse.
func (fs *funcState) rGoIfFalse(p *parser, e *expDesc) {
	fs.rDischargeVars(e)
	switch e.k {
	case vKNil, vKFalse: // no jump
	case vKTrue, vJmp:
		e.t = hasJumps51
	default:
		fs.rJumpOnCond(p, e)
		e.t = hasJumps51
	}
	e.f = jitNoJump
}

// rCodeNot is codenot.
func (fs *funcState) rCodeNot(p *parser, e *expDesc) {
	fs.rDischargeVars(e)
	switch e.k {
	case vKNil, vKFalse:
		e.k = vKTrue
	case vKStr, vKNum, vKTrue:
		e.k = vKFalse
	case vRelocable, vNonReloc:
		fs.rDischarge2AnyReg(p, e)
		fs.rFreeExp(e)
		e.k, e.aux = vRelocable, opNot51
	}
	e.t, e.f = e.f, e.t
}

// rConstFolding is constfolding.
func rConstFolding(op lua51Op, e1, e2 *expDesc) bool {
	if !isNumeral51(e1) || !isNumeral51(e2) {
		return false
	}
	v1, v2 := e1.nval, e2.nval
	var r float64
	switch op {
	case op51Add:
		r = v1 + v2
	case op51Sub:
		r = v1 - v2
	case op51Mul:
		r = v1 * v2
	case op51Div:
		if v2 == 0 {
			return false
		}
		r = v1 / v2
	case op51Mod:
		if v2 == 0 {
			return false
		}
		r = v1 - float64(math.Floor(v1/v2)*v2)
	case op51Pow:
		r = math.Pow(v1, v2)
	case op51Unm:
		r = -v1
	default: // op51Len
		return false
	}
	if math.IsNaN(r) {
		return false
	}
	e1.nval = r
	return true
}

// rCodeArith is codearith.
func (fs *funcState) rCodeArith(p *parser, op lua51Op, e1, e2 *expDesc) {
	if rConstFolding(op, e1, e2) {
		return
	}
	if op != op51Unm && op != op51Len {
		fs.rExp2RK(p, e2)
	}
	fs.rExp2RK(p, e1)
	fs.rFreeExp(e2) // the order of the frees does not change the count
	fs.rFreeExp(e1)
	e1.k, e1.aux = vRelocable, opOther51
	if op == op51Concat {
		e1.aux = opConcat51
	}
}

// rPrefix is luaK_prefix.
func (fs *funcState) rPrefix(p *parser, op int, e *expDesc) {
	var e2 expDesc
	e2.init(vKNum, 0)
	switch op {
	case '-':
		if !isNumeral51(e) {
			fs.rExp2AnyReg(p, e)
		}
		fs.rCodeArith(p, op51Unm, e, &e2)
	case tkNot:
		fs.rCodeNot(p, e)
	default: // '#'
		fs.rExp2AnyReg(p, e)
		fs.rCodeArith(p, op51Len, e, &e2)
	}
}

// rInfix is luaK_infix.
func (fs *funcState) rInfix(p *parser, op lua51Op, v *expDesc) {
	switch op {
	case op51And:
		fs.rGoIfTrue(p, v)
	case op51Or:
		fs.rGoIfFalse(p, v)
	case op51Concat:
		fs.rExp2NextReg(p, v)
	case op51Comp:
		fs.rExp2RK(p, v)
	default:
		if !isNumeral51(v) {
			fs.rExp2RK(p, v)
		}
	}
}

// rPosfix is luaK_posfix.
func (fs *funcState) rPosfix(p *parser, op lua51Op, e1, e2 *expDesc) {
	switch op {
	case op51And:
		fs.rDischargeVars(e2)
		if e1.f != jitNoJump {
			e2.f = hasJumps51
		}
		*e1 = *e2
	case op51Or:
		fs.rDischargeVars(e2)
		if e1.t != jitNoJump {
			e2.t = hasJumps51
		}
		*e1 = *e2
	case op51Concat:
		fs.rExp2Val(p, e2)
		if e2.k == vRelocable && e2.aux == opConcat51 {
			fs.rFreeExp(e1)
			e1.k, e1.aux = vRelocable, opConcat51
		} else {
			fs.rExp2NextReg(p, e2)
			fs.rCodeArith(p, op51Concat, e1, e2)
		}
	case op51Comp: // codecomp
		fs.rExp2RK(p, e1)
		fs.rExp2RK(p, e2)
		fs.rFreeExp(e2)
		fs.rFreeExp(e1)
		e1.k = vJmp
	default:
		fs.rCodeArith(p, op, e1, e2)
	}
}

/* statements */

// rAdjustAssign is adjust_assign.
func (p *parser) rAdjustAssign(nvars, nexps int, e *expDesc) {
	fs := p.fs
	extra := nvars - nexps
	if hasMultRet51(e.k) {
		extra++ // the call itself
		if extra < 0 {
			extra = 0
		}
		fs.rSetReturns(p, e)
		if extra > 1 {
			fs.rReserve(p, uint32(extra-1))
		}
	} else {
		if e.k != vVoid {
			fs.rExp2NextReg(p, e)
		}
		if extra > 0 {
			fs.rReserve(p, uint32(extra))
		}
	}
}

// rCheckConflict is check_conflict: the tables and keys of the indexed
// targets before the local v are copied if the assignment to v
// overwrites them.
func (p *parser) rCheckConflict(base int, v *expDesc) {
	fs := p.fs
	extra := fs.freereg
	conflict := false
	for i := len(p.lhs) - 2; i >= base; i-- {
		lh := &p.lhs[i]
		if lh.k != vIndexed {
			continue
		}
		if lh.info == v.info {
			conflict = true
			lh.info = extra
		}
		if lh.aux == v.info {
			conflict = true
			lh.aux = extra
		}
	}
	if conflict {
		fs.rReserve(p, 1)
	}
}

// rAssignStore stores the values to the targets p.lhs[base:], the last
// value in e (assignment).
func (p *parser) rAssignStore(base, nexps int, e *expDesc) {
	fs := p.fs
	last := len(p.lhs) - 1
	nvars := last - base + 1
	stored := false
	if nexps != nvars {
		p.rAdjustAssign(nvars, nexps, e)
		if nexps > nvars {
			fs.freereg -= uint32(nexps - nvars)
		}
	} else {
		fs.rSetOneRet(e)
		fs.rStoreVar(p, &p.lhs[last], e)
		stored = true
	}
	for i := last; i >= base; i-- {
		if i == last && stored {
			continue
		}
		e.init(vNonReloc, fs.freereg-1)
		fs.rStoreVar(p, &p.lhs[i], e)
	}
}

// rReturn is retstat's register use, the last value in e.
func (p *parser) rReturn(e *expDesc, nret int) {
	fs := p.fs
	switch {
	case hasMultRet51(e.k):
		fs.rSetReturns(p, e)
	case nret == 1:
		fs.rExp2AnyReg(p, e)
	default:
		fs.rExp2NextReg(p, e)
	}
}

/* table constructors */

// rCloseListField is closelistfield.
func (p *parser) rCloseListField(c *tableCons, t *expDesc) {
	fs := p.fs
	if c.v51.k == vVoid {
		return
	}
	fs.rExp2NextReg(p, &c.v51)
	c.v51.k = vVoid
	if c.tostore == fieldsPerFlush51 {
		fs.freereg = t.info + 1 // luaK_setlist
		c.tostore = 0
	}
}

// rLastListField is lastlistfield.
func (p *parser) rLastListField(c *tableCons, t *expDesc) {
	fs := p.fs
	if c.tostore == 0 {
		return
	}
	if hasMultRet51(c.v51.k) {
		fs.rSetReturns(p, &c.v51)
	} else if c.v51.k != vVoid {
		fs.rExp2NextReg(p, &c.v51)
	}
	fs.freereg = t.info + 1 // luaK_setlist
}
