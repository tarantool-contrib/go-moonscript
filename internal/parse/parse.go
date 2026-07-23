package parse

// RequireInfo describes a single require(...) call site found in a Lua source file.
type RequireInfo struct {
	Name   string // The literal string argument, e.g. "foo.bar".
	Line   int    // Source line number.
	Static bool   // True if argument is a string literal.
}

// Parser extracts require(...) call information from Lua source code.
type Parser interface {
	Parse(source []byte, filename string) ([]RequireInfo, error)
}

// New returns a Parser backed by the gopher-lua parser.
func New() Parser {
	return &gopherParser{}
}
