// Package lint is a port of the MoonScript v0.8.0 linter
// (moonscript/cmd/lint.moon), used by `moonc -l`.
//
// The reference LinterBlock subclasses the compiler's Block: it compiles the
// whole file, wrapping the "ref" value compiler (global accesses, uses) and
// the "assign" statement compiler (constant assignments, unused names) and
// the statement transformer (imports overwriting a binding), and checks a
// block's unused names when the block is rendered. This port installs the
// same overrides through the per-block hooks of compiler.Block, so the
// compile runs exactly as `moonc` would and the reports come out in the same
// order.
package lint

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/tarantool-contrib/go-moonscript/ast"
	"github.com/tarantool-contrib/go-moonscript/compiler"
	"github.com/tarantool-contrib/go-moonscript/parse"
)

// Stages are the named checks that can be enabled or disabled (LINT_STAGES).
var Stages = []string{"global_access", "unused", "constant_assign", "import_overwrite"}

// Formats are the named output formats (LINT_FORMATS).
var Formats = []string{"default", "compact"}

// DefaultWhitelist are the globals allowed to be referenced.
var DefaultWhitelist = map[string]bool{}

func init() {
	for _, name := range []string{
		"_G", "_VERSION", "assert", "bit32", "collectgarbage", "coroutine",
		"debug", "dofile", "error", "getfenv", "getmetatable", "io", "ipairs",
		"load", "loadfile", "loadstring", "math", "module", "next", "os",
		"package", "pairs", "pcall", "print", "rawequal", "rawget", "rawlen",
		"rawset", "require", "select", "setfenv", "setmetatable", "string",
		"table", "tonumber", "tostring", "type", "unpack", "xpcall",
		"nil", "true", "false",
	} {
		DefaultWhitelist[name] = true
	}
}

// Whitelist reports whether a global may be referenced; nil is the default
// whitelist.
type Whitelist func(name string) bool

func defaultWhitelisted(name string) bool { return DefaultWhitelist[name] }

// Options are lint_code's opts.
type Options struct {
	// Stages limits reporting to these stages; nil reports every stage
	// (an empty non-nil slice reports nothing).
	Stages []string
	// Format is "default" ("" too) or "compact".
	Format string
}

// Crash is an error the reference doesn't catch: lint_code lets compile
// errors propagate, so moonc dies with the Lua interpreter's message.
type Crash struct {
	// Table: the error object is a table ({"user-error", msg, pos} from
	// the compiler), which lua5.1 can't print.
	Table bool
	Msg   string
	Pos   int
}

func (c *Crash) Error() string {
	if c.Table {
		return "(error object is not a string)"
	}
	return c.Msg
}

// InterpreterMessage is what lua5.1 writes to stderr when moonc dies with
// this error.
func (c *Crash) InterpreterMessage() string {
	if c.Table {
		return "/usr/bin/lua5.1: (error object is not a string)\n"
	}
	return "/usr/bin/lua5.1: " + c.Msg + "\nstack traceback:\n\t[C]: ?\n"
}

type lintError struct {
	msg    string
	pos    int
	hasPos bool
	stage  string
}

type linter struct {
	whitelist Whitelist
	stages    map[string]bool // nil: every stage
	errors    []lintError

	valueCompilers     map[string]compiler.ValueCompiler
	statementCompilers map[string]compiler.StatementCompiler
	// lint_unused_names of each block, created on the first assignment
	unused map[*compiler.Block]*luaTable
}

// report records an error if the stage is enabled (lint_report).
func (l *linter) report(stage, msg string, pos int, hasPos bool) {
	if l.stages != nil && !l.stages[stage] {
		return
	}
	l.errors = append(l.errors, lintError{msg, pos, hasPos, stage})
}

func luaTruthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	}
	return true
}

func isConst(binding any) bool {
	_, ok := binding.(*compiler.ConstBinding)
	return ok
}

// install turns a block into a linter block: the root, and every child
// through BlockHook (LinterBlock's overridden block method).
func (l *linter) install(b *compiler.Block) {
	b.ValueCompilers = l.valueCompilers
	b.StatementCompilers = l.statementCompilers
	b.RenderHook = l.checkUnused
	b.BlockHook = l.install
	l.wrapTransform(b)
}

// wrapTransform is lint_wrap_transform: checks imports before they're
// lowered.
func (l *linter) wrapTransform(block *compiler.Block) {
	checked := map[*ast.Node]bool{}
	block.StatementTransform = func(n any, extra ...any) any {
		importNode := n
		for ast.NType(importNode) == "transform" {
			importNode = importNode.(*ast.Node).At(2)
		}
		if in, ok := importNode.(*ast.Node); ok && in.Kind() == "import" && !checked[in] {
			checked[in] = true
			names, _ := in.At(2).(*ast.Node)
			if names != nil {
				for _, name := range names.Items {
					if name == nil {
						break
					}
					if ast.NType(name) == "colon" {
						name = name.(*ast.Node).At(2)
					}
					s, _ := name.(string)
					binding := block.BindingValue(s)
					// repeated imports are reported by the constant
					// assignment check after the import is lowered
					if !isConst(binding) && luaTruthy(binding) {
						l.report("import_overwrite",
							"import overwrites existing binding `"+s+"`", in.Pos, in.Pos != 0)
					}
				}
			}
		}
		return block.BaseTransformStatement(n, extra...)
	}
}

func (l *linter) compileRef(block *compiler.Block, v any, extra ...any) any {
	val := v.(*ast.Node)
	name, ok := val.At(2).(string)
	if !ok {
		panic("attempt to index local 'name' (a " + luaTypeName(val.At(2)) + " value)")
	}
	if !(block.HasName(name, false) || l.whitelist(name) || strings.Contains(name, ".")) {
		l.report("global_access", "accessing global `"+name+"`", val.Pos, val.Pos != 0)
	}
	l.markUsed(block, name)
	return compiler.DefaultValueCompiler("ref")(block, v, extra...)
}

func luaTypeName(v any) string {
	switch v.(type) {
	case nil:
		return "nil"
	case bool:
		return "boolean"
	case int, float64:
		return "number"
	case string:
		return "string"
	}
	return "table"
}

func (l *linter) compileAssign(block *compiler.Block, n *ast.Node, extra ...any) any {
	names := n.At(2).(*ast.Node)

	// extract the names to be declared
	for _, name := range names.Items {
		if name == nil {
			break
		}
		// don't include autogenerated names
		if ast.IsTable(name) && ast.NType(name) == "temp_name" {
			continue
		}

		var constName string
		hasConst := false
		switch t := name.(type) {
		case string:
			constName, hasConst = t, true
		case *ast.Node:
			if t.Kind() == "ref" {
				constName, hasConst = t.At(2).(string)
			}
		}

		if hasConst {
			if isConst(block.BindingValue(constName)) {
				pos := 0
				if nn, ok := name.(*ast.Node); ok {
					pos = nn.Pos
				}
				if pos == 0 {
					pos = n.Pos
				}
				if pos == 0 {
					pos = block.LastPos()
				}
				l.report("constant_assign", "assigning to constant `"+constName+"`", pos, pos != 0)
			}
		}

		realName, ok, isLocal := block.ExtractAssignName(name)
		// already defined in some other scope
		if !(isLocal || ok && !block.HasName(realName, true)) {
			continue
		}
		if realName == "_" {
			continue
		}

		t := l.unused[block]
		if t == nil {
			t = &luaTable{}
			l.unused[block] = t
		}
		t.set(realName, unusedVal{pos: n.Pos, isPos: true})
	}

	return compiler.DefaultStatementCompiler("assign")(block, n, extra...)
}

// markUsed is lint_mark_used.
func (l *linter) markUsed(block *compiler.Block, name string) {
	for b := block; b != nil; b = b.Parent {
		if t := l.unused[b]; t != nil {
			if v, ok := t.get(name); ok && v.isPos {
				t.set(name, unusedVal{})
				return
			}
		}
	}
}

// checkUnused is lint_check_unused.
func (l *linter) checkUnused(block *compiler.Block) {
	t := l.unused[block]
	if t == nil || t.empty() {
		return
	}

	namesByPosition := map[int][]string{}
	var positions []int
	t.each(func(name string, v unusedVal) {
		if !v.isPos {
			return
		}
		if _, seen := namesByPosition[v.pos]; !seen {
			positions = append(positions, v.pos)
		}
		namesByPosition[v.pos] = append(namesByPosition[v.pos], name)
	})
	sort.Ints(positions)

	for _, pos := range positions {
		names := namesByPosition[pos]
		quoted := make([]string, len(names))
		for i, n := range names {
			quoted[i] = "`" + n + "`"
		}
		l.report("unused", "assigned but unused "+strings.Join(quoted, ", "), pos, true)
	}
}

// posToLineCol is util.pos_to_line_col.
func posToLineCol(str string, pos int) (int, int) {
	line, lineStart := 1, 1
	for {
		nl := -1
		if lineStart-1 <= len(str) {
			if i := strings.IndexByte(str[lineStart-1:], '\n'); i >= 0 {
				nl = lineStart + i
			}
		}
		if nl < 0 || nl >= pos {
			break
		}
		line++
		lineStart = nl + 1
	}
	return line, pos - lineStart + 1
}

// getLine is util.get_line: the lines of str:gmatch "([^\n]*)\n?".
func getLine(str string, lineNum int) (string, bool) {
	lines := strings.Split(str, "\n")
	if lines[len(lines)-1] != "" {
		lines = append(lines, "")
	}
	if lineNum < 1 || lineNum > len(lines) {
		return "", false
	}
	return lines[lineNum-1], true
}

// formatCompact is format_lint_compact: one `file:line:col: message
// [stage]` line per error.
func formatCompact(errs []lintError, code, header string) string {
	out := make([]string, len(errs))
	for i, e := range errs {
		location := header
		if e.hasPos {
			line, col := posToLineCol(code, e.pos)
			location = header + ":" + strconv.Itoa(line) + ":" + strconv.Itoa(col)
		}
		out[i] = location + ": " + e.msg + " [" + e.stage + "]"
	}
	return strings.Join(out, "\n")
}

// formatDefault is format_lint.
func formatDefault(errs []lintError, code, header string) string {
	out := []string{header}
	for _, e := range errs {
		if !e.hasPos {
			out = append(out, e.msg)
			continue
		}
		line, _ := posToLineCol(code, e.pos)
		msg := "line " + strconv.Itoa(line) + ": " + e.msg
		text, ok := getLine(code, line)
		if !ok {
			panic(&Crash{Msg: "attempt to concatenate a nil value"})
		}
		lineText := "> " + text
		sepLen := max(len(msg), len(lineText))
		out = append(out, msg+"\n"+strings.Repeat("=", sepLen)+"\n"+lineText)
	}
	return strings.Join(out, "\n\n")
}

// Code is lint_code(code, name, whitelist_globals, opts). It returns the
// report and true, or false when there's nothing to report. err is the parse
// error message (lint_code's nil, err), or a *Crash for errors the reference
// doesn't catch. name defaults to "string input".
func Code(code, name string, whitelist Whitelist, opts Options) (report string, ok bool, err error) {
	if name == "" {
		name = "string input"
	}
	var format func([]lintError, string, string) string
	switch opts.Format {
	case "", "default":
		format = formatDefault
	case "compact":
		format = formatCompact
	default:
		return "", false, &Crash{Msg: "unknown lint format: " + opts.Format}
	}

	tree, perr := parse.String(code)
	if perr != nil {
		return "", false, perr
	}

	if whitelist == nil {
		whitelist = defaultWhitelisted
	}
	l := &linter{
		whitelist: whitelist,
		unused:    map[*compiler.Block]*luaTable{},
	}
	if opts.Stages != nil {
		l.stages = map[string]bool{}
		for _, s := range opts.Stages {
			l.stages[s] = true
		}
	}
	l.valueCompilers = map[string]compiler.ValueCompiler{"ref": l.compileRef}
	l.statementCompilers = map[string]compiler.StatementCompiler{"assign": l.compileAssign}

	if crash := l.run(tree); crash != nil {
		return "", false, crash
	}
	if len(l.errors) == 0 {
		return "", false, nil
	}

	defer func() {
		if r := recover(); r != nil {
			c, isCrash := r.(*Crash)
			if !isCrash {
				panic(r)
			}
			report, ok, err = "", false, c
		}
	}()
	return format(l.errors, code, name), true, nil
}

// run compiles the tree with a linter root block, turning panics into the
// errors the reference would die with.
func (l *linter) run(tree *ast.Node) (crash *Crash) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		switch e := r.(type) {
		case *compiler.CompileError:
			crash = &Crash{Table: true, Msg: e.Msg, Pos: e.Pos}
		case *Crash:
			crash = e
		case error:
			crash = &Crash{Msg: e.Error()}
		default:
			crash = &Crash{Msg: fmt.Sprint(r)}
		}
	}()
	scope := compiler.NewRootBlock(compiler.Options{})
	l.install(scope)
	scope.Stms(tree)
	l.checkUnused(scope)
	return nil
}

// File is lint_file(fname, opts): the file's whitelist comes from the
// lint_config module (see WhitelistForFile).
func File(fname string, opts Options) (string, bool, error) {
	text, err := os.ReadFile(fname)
	if err != nil {
		return "", false, luaIOError(fname, err)
	}
	whitelist, err := WhitelistForFile(fname)
	if err != nil {
		return "", false, err
	}
	return Code(string(text), fname, whitelist, opts)
}

// luaIOError formats an io.open failure like Lua: "<fname>: <strerror>".
func luaIOError(fname string, err error) error {
	msg := err.Error()
	var errno syscall.Errno
	if errors.As(err, &errno) {
		msg = errno.Error()
	}
	if msg != "" {
		msg = strings.ToUpper(msg[:1]) + msg[1:]
	}
	return errors.New(fname + ": " + msg)
}
