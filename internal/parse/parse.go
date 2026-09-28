package parse

// RequireInfo is one use of require found in a Lua source file. Most are calls:
// static ones name their module, dynamic ones (Static false) compute it. When
// Indirect is set it is a use of require that is not a call.
type RequireInfo struct {
	Name   string // The literal module name, e.g. "foo.bar"; "" unless Static.
	Line   int    // Source line number.
	Static bool   // True if the first argument require gets is a string literal.
	// Indirect, when not empty, marks require aliased, stored or passed on as
	// a value, so the calls made through it cannot be seen. It says how, e.g.
	// `require aliased as local "r"`.
	Indirect string
}

// Parser finds the uses of require in Lua source code.
type Parser interface {
	Parse(source []byte, filename string) ([]RequireInfo, error)
}

// New returns a Parser backed by the gopher-lua parser.
func New() Parser {
	return &gopherParser{}
}
