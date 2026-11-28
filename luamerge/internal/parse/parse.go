package parse

// Span is where a token lies in a source: source[Start:End].
type Span struct {
	Start int
	End   int
}

// RequireInfo is one use of require found in a Lua source file. Most are calls:
// static ones name their module, dynamic ones (Static false) compute it. When
// Indirect is set it is a use of require that is not a call.
type RequireInfo struct {
	Name   string // The literal module name, e.g. "foo.bar"; "" unless Static.
	Line   int    // Source line number.
	Static bool   // True if the first argument require gets is a string literal.
	// Literal is where the string literal that names the module of a static
	// call lies in the source, such as `"x"` in `require(("x"), y)`: the
	// literal require gets first. It is the zero Span otherwise.
	Literal Span
	// Protected is true for a call of require in protected mode:
	// `pcall(require, "x")` or `xpcall(require, handler, "x")`. Its caller
	// gets false and the error instead of the error being raised, so the
	// program is written to run on without the module.
	Protected bool
	// Indirect, when not empty, marks require aliased, stored or passed on as
	// a value, so the calls made through it cannot be seen. It says how, e.g.
	// `require aliased as local "r"`.
	Indirect string
}

// Result is what Parse finds in a Lua source file.
type Result struct {
	// Requires are the uses of require in the file.
	Requires []RequireInfo
	// Fallback, when not nil, says that the parser could not read the file
	// and why, and that the token scanner found Requires instead.
	Fallback *Fallback
}

// Fallback is why the parser could not read a file whose uses of require the
// token scanner found instead. The token scanner recognizes the calls of
// require the parser does and reports every other use of the name require as
// indirect, but it knows no scopes: a local named require is the global
// require to it.
type Fallback struct {
	// Line is the line the parser failed on, or the last line of the file
	// when the file ended before the parser expected it to.
	Line int
	// Reason is the parser's error, such as `syntax error near "="`.
	Reason string
}

// Parser finds the uses of require in Lua source code.
type Parser interface {
	// Parse finds the uses of require in source, the contents of the file
	// filename. A file that no Lua dialect reads, such as one with an
	// unfinished string, is an error.
	Parse(source []byte, filename string) (Result, error)
}

// New returns a Parser backed by the gopher-lua parser.
func New() Parser {
	return &gopherParser{}
}
