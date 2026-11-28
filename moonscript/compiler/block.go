package compiler

// Port of Block, RootBlock and the tree entry point from
// moonscript/compile.moon.

import (
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/tarantool-contrib/go-moonscript/moonscript/ast"
	"github.com/tarantool-contrib/go-moonscript/moonscript/parse"
)

// ConstBinding is the descriptor stored by declare_constants.
type ConstBinding struct {
	Pos int
}

// Options are the compile options (moonscript.compile.tree options).
type Options struct {
	// ImplicitlyReturnRoot defaults to true; set NoImplicitReturnRoot to
	// disable (implicitly_return_root = false).
	NoImplicitReturnRoot bool
	// Correlate pins output lines to source lines (moonc --correlate).
	Correlate bool
	// Source is the original code, required by Correlate.
	Source string
}

// Block is a compile scope producing a block of Lua code.
type Block struct {
	Parent *Block
	Header any // string or *Line
	Footer any // string
	Next   *Block
	Pos    int
	Indent int

	ExportAll    bool
	ExportProper bool

	lines         *Lines
	names         map[string]any
	state         map[string]any
	nameWhitelist map[string]bool
	discard       *NameProxy

	CurrentStms *ast.Node
	CurrentStmI int

	// Per-block overrides, the fields moonscript/cmd/lint.moon's LinterBlock
	// replaces on Block instances (nil: default behavior):
	//
	//   ValueCompilers, StatementCompilers: @value_compilers /
	//     @statement_compilers, consulted before the default compilers.
	//   StatementTransform: @transform.statement; BaseTransformStatement is
	//     the default.
	//   RenderHook: runs at the start of render.
	//   BlockHook: runs on every child created by Block (the overridden
	//     @block method).
	ValueCompilers     map[string]ValueCompiler
	StatementCompilers map[string]StatementCompiler
	StatementTransform func(n any, extra ...any) any
	RenderHook         func(b *Block)
	BlockHook          func(child *Block)

	root *Block
	// root only
	options Options
	lastPos int
	posmap  map[int]int
	stmT    *transformer
	valT    *transformer
}

// NewBlock is Block(parent, header, footer); nil header/footer take the
// class defaults "do" / "end".
func NewBlock(parent *Block, header, footer any) *Block {
	if header == nil {
		header = "do"
	}
	if footer == nil {
		footer = "end"
	}
	b := &Block{
		Parent: parent,
		Header: header,
		Footer: footer,
		lines:  NewLines(),
		names:  map[string]any{},
		state:  map[string]any{},
	}
	if parent != nil {
		b.root = parent.root
		b.Indent = parent.Indent + 1
	} else {
		b.root = b
	}
	return b
}

// NewRootBlock is RootBlock(options).
func NewRootBlock(opts Options) *Block {
	b := NewBlock(nil, nil, nil)
	b.options = opts
	b.stmT = newTransformer(statementTransformers)
	b.valT = newTransformer(valueTransformers)
	return b
}

// TransformStatement is @transform.statement node, ...
func (b *Block) TransformStatement(n any, extra ...any) any {
	if b.StatementTransform != nil {
		return b.StatementTransform(n, extra...)
	}
	return b.BaseTransformStatement(n, extra...)
}

// BaseTransformStatement is the default @transform.statement.
func (b *Block) BaseTransformStatement(n any, extra ...any) any {
	return b.root.stmT.transform(b, n, extra...)
}

// Root is @root.
func (b *Block) Root() *Block { return b.root }

// LastPos is @root.last_pos: the most recent statement position, 0 if none.
func (b *Block) LastPos() int { return b.root.lastPos }

// DefaultValueCompiler returns the default value compiler for a node type.
func DefaultValueCompiler(kind string) ValueCompiler { return valueCompilers[kind] }

// DefaultStatementCompiler returns the default statement compiler for a node
// type.
func DefaultStatementCompiler(kind string) StatementCompiler { return statementCompilers[kind] }

func (b *Block) valueCompiler(kind string) ValueCompiler {
	if fn, ok := b.ValueCompilers[kind]; ok {
		return fn
	}
	return valueCompilers[kind]
}

func (b *Block) statementCompiler(kind string) StatementCompiler {
	if fn, ok := b.StatementCompilers[kind]; ok {
		return fn
	}
	return statementCompilers[kind]
}

// TransformValue is @transform.value node, ...
func (b *Block) TransformValue(n any, extra ...any) any {
	return b.root.valT.transform(b, n, extra...)
}

// TransformValueOnce is Value\transform_once @, node
func (b *Block) TransformValueOnce(n any, extra ...any) any {
	return b.root.valT.transformOnce(b, n, extra...)
}

// Set stores compiler state visible to this block and its children. Setting
// nil removes the local value so lookups fall through to the parent again.
func (b *Block) Set(name string, value any) {
	if value == nil {
		delete(b.state, name)
		return
	}
	b.state[name] = value
}

func (b *Block) Get(name string) any {
	for s := b; s != nil; s = s.Parent {
		if v, ok := s.state[name]; ok {
			return v
		}
	}
	return nil
}

func (b *Block) GetCurrent(name string) any { return b.state[name] }

// ExtractAssignName returns the real name of an assign target (ok=false when
// it has none) and whether it's always declared local.
func (b *Block) ExtractAssignName(n any) (name string, ok bool, isLocal bool) {
	switch t := n.(type) {
	case *LocalName:
		return t.GetName(b), true, true
	case *NameProxy:
		return t.GetName(b), true, false
	case *ast.Node:
		if t.Kind() == "ref" {
			s, ok := t.At(2).(string)
			return s, ok, false
		}
		return "", false, false
	case string:
		// TODO: some legacy transformers might use string for ref
		return t, true, false
	}
	return "", false, false
}

// Declare returns the names that need a local declaration.
func (b *Block) Declare(names *ast.Node) []string {
	var undeclared []string
	for _, n := range names.Items {
		if n == nil {
			break
		}
		real, ok, isLocal := b.ExtractAssignName(n)
		if !(isLocal || ok && !b.HasName(real, true)) {
			continue
		}
		// this also puts exported names so they can be assigned a new value
		// in deeper scope
		b.PutName(real)
		if b.NameExported(real) {
			continue
		}
		undeclared = append(undeclared, real)
	}
	return undeclared
}

func (b *Block) WhitelistNames(names *ast.Node) {
	b.nameWhitelist = map[string]bool{}
	for _, n := range names.Items {
		if s, ok := n.(string); ok {
			b.nameWhitelist[s] = true
		}
	}
}

func (b *Block) NameExported(name string) bool {
	if b.ExportAll {
		return true
	}
	return b.ExportProper && matchUpper(name)
}

// PutName records a binding; value defaults to true.
func (b *Block) PutName(name any, value ...any) {
	var v any = true
	if len(value) > 0 {
		v = value[0]
	}
	var key string
	switch t := name.(type) {
	case *NameProxy:
		key = t.GetName(b)
	case string:
		key = t
	default:
		panic(fmt.Sprintf("put_name: unexpected name %v", name))
	}
	b.names[key] = v
}

// PutFreshNames records names the current construct always binds fresh (loop
// variables, local statements), so they shadow any binding in an enclosing
// scope
func (b *Block) PutFreshNames(names *ast.Node) {
	for _, n := range names.Items {
		if n == nil {
			break
		}
		if real, ok, _ := b.ExtractAssignName(n); ok {
			b.PutName(real)
		}
	}
}

// BindingValue is the value stored for a binding by put_name, following the
// same scope visibility as HasName.
func (b *Block) BindingValue(name string) any {
	v, ok := b.names[name]
	if !ok && b.Parent != nil {
		if b.nameWhitelist == nil || b.nameWhitelist[name] {
			return b.Parent.BindingValue(name)
		}
	}
	return v
}

// HasName checks if a name is defined in the current or any enclosing scope.
// skipExports: ignore names that have been exported using `export`.
func (b *Block) HasName(name string, skipExports bool) bool {
	if !skipExports && b.NameExported(name) {
		return true
	}
	v, ok := b.names[name]
	if !ok && b.Parent != nil {
		if b.nameWhitelist == nil || b.nameWhitelist[name] {
			return b.Parent.HasName(name, true)
		}
		return false
	}
	return truthy(v)
}

func (b *Block) IsLocal(n any) bool {
	switch t := n.(type) {
	case string:
		return b.HasName(t, false)
	case *NameProxy, *LocalName:
		return true
	case *ast.Node:
		if t.Kind() == "ref" || (t.Kind() == "chain" && t.Len() == 2) {
			return b.IsLocal(t.At(2))
		}
	}
	return false
}

func (b *Block) FreeName(prefix string, dontPut bool) string {
	if prefix == "" {
		prefix = "moon"
	}
	var name string
	for i := 0; ; i++ {
		name = fmt.Sprintf("_%s_%d", prefix, i)
		if !b.HasName(name, true) {
			break
		}
	}
	if !dontPut {
		b.PutName(name)
	}
	return name
}

func (b *Block) InitFreeVar(prefix string, value any) string {
	name := b.FreeName(prefix, true)
	b.Stm(node("assign", node(name), node(value)))
	return name
}

// DiscardName: expressions that need to be coerced into statements are
// assigned to this name
func (b *Block) DiscardName() *NameProxy {
	if b.discard == nil {
		b.discard = NewNameProxy("scrap")
	}
	return b.discard
}

// Add adds something to the line buffer.
func (b *Block) Add(item any, pos ...int) any {
	b.lines.Add(item)
	if len(pos) > 0 && pos[0] != 0 {
		b.lines.MarkPos(pos[0])
	}
	return item
}

func (b *Block) Render(buffer *Lines) *Lines {
	if b.RenderHook != nil {
		b.RenderHook(b)
	}
	buffer.Add(b.Header)
	buffer.MarkPos(b.Pos)

	if b.Next != nil {
		buffer.Add(b.lines)
		b.Next.Render(buffer)
	} else {
		// join an empty block into a single line
		last, isStr := any(nil), false
		if n := len(buffer.Items); n > 0 {
			last = buffer.Items[n-1]
			_, isStr = last.(string)
		}
		if b.lines.Len() == 0 && isStr {
			footer := NewLines().Add(b.Footer)
			f := ""
			if len(footer.Items) > 0 {
				f, _ = footer.Items[0].(string)
			}
			buffer.Items[len(buffer.Items)-1] = last.(string) + " " + f
		} else {
			buffer.Add(b.lines)
			buffer.Add(b.Footer)
			buffer.MarkPos(b.Pos)
		}
	}
	return buffer
}

func (b *Block) Block(header, footer any) *Block {
	child := NewBlock(b, header, footer)
	if b.BlockHook != nil {
		b.BlockHook(child)
	}
	return child
}

func (b *Block) Line(items ...any) *Line { return NewLine(items...) }

func (b *Block) IsStm(n any) bool {
	t := ntype(n)
	return t == "run" || b.statementCompiler(t) != nil
}

func (b *Block) IsValue(n any) bool { return blockIsValue(n) }

// Name compiles a name for assign.
func (b *Block) Name(n any, extra ...any) any {
	if s, ok := n.(string); ok {
		return s
	}
	return b.Value(n, extra...)
}

// Value compiles a value; returns string, *Line or *Block.
func (b *Block) Value(n any, extra ...any) any {
	n = b.TransformValue(n)
	action := "raw_value"
	if isTable(n) {
		action = ntype(n)
	}
	fn := b.valueCompiler(action)
	if fn == nil {
		panic(&CompileError{
			Kind: "compile-error",
			Msg:  "Failed to find value compiler for: " + ast.FlatValue(n),
			Pos:  posOf(n),
		})
	}
	out := fn(b, n, extra...)

	// store the pos, creating a line if necessary
	if isTable(n) && posOf(n) != 0 {
		switch t := out.(type) {
		case string:
			l := NewLine(t)
			l.Pos = posOf(n)
			out = l
		case *Line:
			t.Pos = posOf(n)
		case *Block:
			t.Pos = posOf(n)
		}
	}
	return out
}

func (b *Block) Values(values *ast.Node, delim ...string) *Line {
	d := ", "
	if len(delim) > 0 {
		d = delim[0]
	}
	l := NewLine()
	l.AppendList(b.valueList(values.Items), d)
	return l
}

func (b *Block) valueList(items []any) []any {
	out := make([]any, 0, len(items))
	for _, v := range items {
		if v == nil {
			break
		}
		out = append(out, b.Value(v))
	}
	return out
}

func (b *Block) Stm(n any, extra ...any) {
	if !truthy(n) {
		return // skip blank statements
	}

	// track the most recent position for errors raised without one
	if p := posOf(n); p != 0 {
		b.root.lastPos = p
	}

	n = b.TransformStatement(n)

	var result any
	if r, ok := n.(*Run); ok {
		// statement compiler "run": code\call self
		r.Call(b)
		return
	}
	if fn := b.statementCompiler(ntype(n)); fn != nil {
		result = fn(b, n.(*ast.Node), extra...)
	} else if valueCanBeStatement(n) {
		result = b.Value(n)
	} else {
		// coerce value into statement
		b.Stm(node("assign", node(b.DiscardName()), node(n)))
	}

	if result != nil {
		if p := posOf(n); p != 0 {
			switch t := result.(type) {
			case *Line:
				t.Pos = p
			case *Block:
				t.Pos = p
			}
		}
		b.Add(result)
	}
}

func (b *Block) Stms(stms *ast.Node) {
	saved, savedI := b.CurrentStms, b.CurrentStmI
	b.CurrentStms = stms
	n := stms.Len()
	for i := 1; i <= n; i++ {
		b.CurrentStmI = i
		b.Stm(stms.At(i))
	}
	b.CurrentStms, b.CurrentStmI = saved, savedI
}

func (b *Block) rootStms(stms *ast.Node) {
	if !b.options.NoImplicitReturnRoot {
		stms = rootStms(b, stms)
	}
	b.Stms(stms)
}

// rootStms is the root_stms statement transformer.
func rootStms(scope *Block, body *ast.Node) *ast.Node {
	return transformLastStm(body, implicitlyReturn(scope))
}

func (b *Block) renderRoot() string {
	var buffer []string
	if b.options.Correlate {
		st := &correlateState{line: 1, posmap: map[int]int{}}
		buffer = b.lines.FlattenCorrelated(lineLocator(b.options.Source), st, nil, nil)
		b.posmap = st.posmap
	} else {
		buffer = b.lines.Flatten(nil, nil)
	}
	if n := len(buffer); n > 0 && buffer[n-1] == "\n" {
		buffer = buffer[:n-1]
	}
	return strings.Join(buffer, "")
}

// CompileError is a compile failure; Pos is a 1-based source offset or 0.
type CompileError struct {
	Kind string // "user-error", "compile-error" or "unknown"
	Msg  string
	Pos  int
}

func (e *CompileError) Error() string { return e.Msg }

// userError is errors.user_error.
func userError(msg string, pos int) {
	panic(&CompileError{Kind: "user-error", Msg: msg, Pos: pos})
}

// Tree compiles a parsed tree. It returns the Lua code and the posmap (Lua
// line number -> source position).
func Tree(tree *ast.Node, opts Options) (code string, posmap map[int]int, err *CompileError) {
	if tree == nil {
		panic("missing tree")
	}
	scope := NewRootBlock(opts)

	func() {
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			switch e := r.(type) {
			case *CompileError:
				err = &CompileError{Kind: e.Kind, Msg: e.Msg, Pos: e.Pos}
			default:
				err = &CompileError{
					Kind: "unknown",
					Msg:  fmt.Sprintf("%v\n%s", r, debug.Stack()),
				}
			}
			if err.Pos == 0 {
				err.Pos = scope.lastPos
			}
		}()
		scope.rootStms(tree)
	}()
	if err != nil {
		return "", nil, err
	}

	code = scope.renderRoot()
	posmap = scope.posmap
	if posmap == nil {
		posmap, _ = scope.lines.FlattenPosmap(0, nil)
	}
	return code, posmap, nil
}

// FormatError is compile.format_error.
func FormatError(msg string, pos int, src string) string {
	msg = "failed to compile: " + msg
	if pos == 0 {
		return msg
	}
	return parse.FormatError(src, pos, msg)
}
