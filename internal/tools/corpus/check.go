package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/tarantool-contrib/go-moonscript/moonscript/ast"
	"github.com/tarantool-contrib/go-moonscript/moonscript/compiler"
	"github.com/tarantool-contrib/go-moonscript/moonscript/parse"
)

// Parse stage outcomes.
const (
	PTreeMatch    = "tree-match"           // ref and Go trees are identical
	PTreeMismatch = "tree-mismatch"        // both parsed, trees differ
	PGoRejects    = "go-rejects-valid"     // ref parsed, Go returned a parse error
	PErrMatch     = "parse-error-match"    // both failed with the same message
	PErrMismatch  = "parse-error-mismatch" // both failed, messages differ
	PGoAccepts    = "go-accepts-invalid"   // ref failed, Go parsed
	PPanic        = "parse-panic"          // Go parser panicked
	PNoRef        = "no-ref"               // no usable reference result
)

// Compile stage outcomes.
const (
	CLuaMatch         = "lua-match"              // identical Lua output
	CLuaMismatch      = "lua-mismatch"           // both compiled, output differs
	CGoError          = "go-compile-error"       // ref compiled, Go raised a user/compile error
	CGoInternal       = "go-internal-error"      // Go compiler raised panic(string) (unported path, error "...")
	CPanic            = "go-panic"               // Go runtime panic (nil deref, index, type assertion)
	CErrMatch         = "compile-error-match"    // both raised the same compile error
	CErrMismatch      = "compile-error-mismatch" // both raised compile errors, text differs
	CGoAccepts        = "go-compiles-invalid"    // ref raised a compile error, Go compiled
	CRefInternalMatch = "ref-internal-match"     // ref crashed internally, Go raised the same message
	CRefInternalDiff  = "ref-internal-mismatch"  // ref crashed internally, Go did something else
	CSkip             = "skip"                   // not compiled (parse stage failed on a side)
)

// Fatal outcomes (set by the supervisor, the worker process died).
const (
	FCrash   = "crash"
	FTimeout = "timeout"
)

// Result is the outcome for one corpus file.
type Result struct {
	ID      string `json:"id"`
	Ref     string `json:"ref"`
	Parse   string `json:"parse"`
	Compile string `json:"compile"`
	Fatal   string `json:"fatal,omitempty"`
	// Bucket keys group mismatches with the same probable cause; Excerpt
	// is a short first-difference description.
	ParseBucket    string `json:"parse_bucket,omitempty"`
	ParseExcerpt   string `json:"parse_excerpt,omitempty"`
	CompileBucket  string `json:"compile_bucket,omitempty"`
	CompileExcerpt string `json:"compile_excerpt,omitempty"`
	Ms             int64  `json:"ms"`
	ParseMs        int64  `json:"parse_ms"`

	// full outputs, only kept for -diff
	want, got struct{ tree, lua, perr, cerr string }
}

// Ref is the reference result of one file. Hashed: Tree, Lua and Err are
// digests (a fetched corpus, see expected.go).
type Ref struct {
	Status          string
	Tree, Lua, Err  string
	HasTree, HasLua bool
	Hashed          bool
}

func loadRef(dir, id string) Ref {
	if m := mustExpected(dir); m != nil && !hasRefOutputs(dir, "ref", id) {
		r, err := parseRefEntry(m[id].ref)
		if err != nil {
			return Ref{Status: "missing", Hashed: true}
		}
		return r
	}
	refDir := filepath.Join(dir, "ref")
	r := Ref{Status: readStatus(refDir, id)}
	if b, err := os.ReadFile(filepath.Join(refDir, id+".tree")); err == nil {
		r.Tree, r.HasTree = string(b), true
	}
	if b, err := os.ReadFile(filepath.Join(refDir, id+".lua")); err == nil {
		r.Lua, r.HasLua = string(b), true
	}
	if b, err := os.ReadFile(filepath.Join(refDir, id+".err")); err == nil {
		r.Err = string(b)
	}
	return r
}

// check runs the Go parser and compiler on src and compares with ref.
func check(id, src string, ref Ref, parseOnly bool) (res Result) {
	start := time.Now()
	res = Result{ID: id, Ref: ref.Status, Parse: PNoRef, Compile: CSkip}
	defer func() { res.Ms = time.Since(start).Milliseconds() }()

	refParsed := ref.HasTree
	switch ref.Status {
	case "ok", "compile-error", "compile-internal", "parse-error":
	default: // missing, pending, ref-timeout, parse-internal, ...
		return res
	}

	// parse stage
	var tree *ast.Node
	var perr error
	var panicMsg string
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicMsg = panicText(r, string(debug.Stack()))
			}
		}()
		tree, perr = parse.String(src)
	}()
	res.ParseMs = time.Since(start).Milliseconds()
	gotTree := ""
	if panicMsg == "" && perr == nil {
		func() {
			defer func() {
				if r := recover(); r != nil {
					panicMsg = "ast.Tree: " + panicText(r, string(debug.Stack()))
				}
			}()
			gotTree = ast.Tree(tree)
		}()
	}
	res.want.tree, res.want.perr = ref.Tree, ""
	if !refParsed {
		res.want.perr = ref.Err
	}
	res.got.tree = gotTree
	if perr != nil {
		res.got.perr = perr.Error()
	}

	switch {
	case panicMsg != "":
		res.Parse = PPanic
		res.ParseBucket = panicBucket(panicMsg)
		res.ParseExcerpt = firstLines(panicMsg, 12)
		return res
	case refParsed && perr == nil:
		switch {
		case ref.same(ref.Tree, gotTree):
			res.Parse = PTreeMatch
		case ref.Hashed:
			res.Parse = PTreeMismatch
			res.ParseBucket, res.ParseExcerpt = "tree differs from the expected digest", digestOnly
		default:
			res.Parse = PTreeMismatch
			res.ParseBucket, res.ParseExcerpt = treeDiff(ref.Tree, gotTree, src)
		}
	case refParsed:
		res.Parse = PGoRejects
		res.ParseBucket = "go error: " + normMsg(firstLine(perr.Error()))
		res.ParseExcerpt = indent(perr.Error())
		return res
	case perr == nil:
		res.Parse = PGoAccepts
		res.ParseBucket = "ref error: " + normMsg(firstLine(ref.errText()))
		res.ParseExcerpt = "reference error:\n" + indent(ref.errText())
		return res
	default:
		switch {
		case ref.same(ref.Err, perr.Error()):
			res.Parse = PErrMatch
		case ref.Hashed:
			res.Parse = PErrMismatch
			res.ParseBucket = "go error: " + normMsg(firstLine(perr.Error()))
			res.ParseExcerpt = indent(perr.Error()) + "\n" + digestOnly
		default:
			res.Parse = PErrMismatch
			res.ParseBucket, res.ParseExcerpt = errDiff(ref.Err, perr.Error())
		}
		return res
	}
	if parseOnly {
		return res
	}

	// compile stage
	var code string
	var cerr *compiler.CompileError
	panicMsg = ""
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicMsg = panicText(r, string(debug.Stack()))
			}
		}()
		code, _, cerr = compiler.Tree(tree, compiler.Options{})
	}()
	if panicMsg == "" && cerr != nil && cerr.Kind == "unknown" {
		// compiler.Tree turns panics into Kind "unknown" (value + stack)
		panicMsg = cerr.Msg
	}
	gotErr := ""
	if cerr != nil {
		gotErr = compiler.FormatError(cerr.Msg, cerr.Pos, src)
	}
	res.want.lua, res.want.cerr = ref.Lua, ""
	if ref.Status != "ok" {
		res.want.cerr = ref.Err
	}
	res.got.lua, res.got.cerr = code, gotErr
	if panicMsg != "" && res.got.cerr == "" {
		res.got.cerr = panicMsg
	}

	switch {
	case ref.Status == "compile-internal":
		w := ref.Err // its digest
		if !ref.Hashed {
			w = normInternal(ref.Err)
		}
		g := ""
		if panicMsg != "" {
			g = normInternal(panicMsg)
		} else if cerr != nil {
			g = normInternal(gotErr)
		}
		if g != "" && ref.same(w, g) {
			res.Compile = CRefInternalMatch
		} else {
			res.Compile = CRefInternalDiff
			res.CompileBucket = "ref internal: " + truncate(w, 100)
			if ref.Hashed {
				w = "(digest " + w + ")"
			}
			res.CompileExcerpt = "reference: " + w + "\ngo:        " + orStr(g, "(compiled)")
		}
	case panicMsg != "":
		if isRuntimePanic(panicMsg) {
			res.Compile = CPanic
		} else {
			res.Compile = CGoInternal
		}
		res.CompileBucket = panicBucket(panicMsg)
		res.CompileExcerpt = firstLines(panicMsg, 12)
	case ref.Status == "ok" && cerr == nil:
		switch {
		case ref.same(ref.Lua, code):
			res.Compile = CLuaMatch
		case ref.Hashed:
			res.Compile = CLuaMismatch
			res.CompileBucket, res.CompileExcerpt = "lua differs from the expected digest", digestOnly
		default:
			res.Compile = CLuaMismatch
			res.CompileBucket, res.CompileExcerpt = luaDiff(ref.Lua, code)
		}
	case ref.Status == "ok":
		res.Compile = CGoError
		res.CompileBucket = "go error: " + normMsg(firstLine(gotErr))
		res.CompileExcerpt = indent(gotErr)
	case cerr == nil:
		res.Compile = CGoAccepts
		res.CompileBucket = "ref error: " + normMsg(firstLine(ref.errText()))
		res.CompileExcerpt = "reference error:\n" + indent(ref.errText())
	default:
		switch {
		case ref.same(ref.Err, gotErr):
			res.Compile = CErrMatch
		case ref.Hashed:
			res.Compile = CErrMismatch
			res.CompileBucket = "go error: " + normMsg(firstLine(gotErr))
			res.CompileExcerpt = indent(gotErr) + "\n" + digestOnly
		default:
			res.Compile = CErrMismatch
			res.CompileBucket, res.CompileExcerpt = errDiff(ref.Err, gotErr)
		}
	}
	return res
}

// errText is the reference error for reports.
func (r Ref) errText() string {
	if r.Hashed {
		return "(expected digest " + r.Err + ")"
	}
	return r.Err
}

func panicText(r any, stack string) string {
	return fmt.Sprintf("%v\n%s", r, stack)
}

var reRuntimePanic = regexp.MustCompile(`^(runtime error|interface conversion|assignment to entry in nil map|reflect|close of|all goroutines)`)

func isRuntimePanic(msg string) bool {
	return reRuntimePanic.MatchString(strings.TrimPrefix(firstLine(msg), "ast.Tree: "))
}

// panicBucket is the panic value plus the frame that raised it.
func panicBucket(msg string) string {
	return normMsg(truncate(firstLine(msg), 120)) + " @ " + faultFrame(msg)
}

// faultFrame finds the first go-moonscript function below the panic call in
// a stack trace text (debug.Stack format).
func faultFrame(stack string) string {
	lines := strings.Split(stack, "\n")
	afterPanic := false
	first := ""
	for _, l := range lines {
		if strings.HasPrefix(l, "\t") || l == "" {
			continue
		}
		if strings.HasPrefix(l, "panic(") {
			afterPanic = true
			continue
		}
		if strings.HasPrefix(l, "github.com/tarantool-contrib/go-moonscript/") {
			fn := l
			if i := strings.LastIndex(fn, "("); i > 0 {
				fn = fn[:i]
			}
			fn = strings.TrimPrefix(fn, "github.com/tarantool-contrib/go-moonscript/")
			fn = strings.TrimPrefix(fn, "moonscript/")
			if afterPanic {
				return fn
			}
			if first == "" && !strings.Contains(fn, "internal/tools") {
				first = fn
			}
		}
	}
	if first == "" {
		return "?"
	}
	return first
}

var reInternalPrefix = regexp.MustCompile(`^(failed to compile: )?(\[string "[^"]*"\]|[^\s:]+\.(lua|moon)):\d+: `)

// normInternal reduces an internal error (reference Lua error or Go panic)
// to its message: first line, without "failed to compile: " and Lua
// file:line prefixes.
func normInternal(msg string) string {
	l := firstLine(msg)
	l = reInternalPrefix.ReplaceAllString(l, "")
	l = strings.TrimPrefix(l, "failed to compile: ")
	return strings.TrimSpace(l)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func firstLines(s string, n int) string {
	ls := strings.SplitN(s, "\n", n+1)
	if len(ls) > n {
		ls = append(ls[:n], "...")
	}
	return indent(strings.Join(ls, "\n"))
}

func indent(s string) string {
	s = strings.TrimRight(s, "\n")
	return "    " + strings.ReplaceAll(s, "\n", "\n    ")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func orStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
