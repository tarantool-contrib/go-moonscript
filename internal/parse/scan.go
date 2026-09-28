package parse

import (
	"github.com/tarantool/go-luamerge/internal/lex"
)

// Lengths of the forms of _G.require, in tokens.
const (
	// fieldRefLen is `_G . require`.
	fieldRefLen = 3
	// indexRefLen is `_G [ "require" ]`.
	indexRefLen = 4
)

// scanner finds the uses of require in the tokens of a file without a syntax
// tree; see scanRequires.
type scanner struct {
	src []byte
	// tokens are the tokens of src without its comments.
	tokens   []lex.Token
	requires []RequireInfo
	// used marks the tokens that stand for require in a call of pcall or
	// xpcall that is recorded already.
	used []bool
}

// scanRequires finds the uses of require in tokens, the tokens of src without
// its comments: the way to find them in a file the parser cannot read. It
// recognizes the calls the walker does, `require(...)`, `_G.require(...)`,
// `_ENV.require(...)`, `pcall(require, ...)` and `xpcall(require, handler,
// ...)`, static when require gets a string literal first. Every other use of
// the name require is indirect, but for a name being declared, defined or
// assigned to, and `require = require`. It knows no scopes: a local named
// require is the global require to it.
func scanRequires(src []byte, tokens []lex.Token) []RequireInfo {
	found := &scanner{src: src, tokens: tokens, requires: []RequireInfo{}, used: make([]bool, len(tokens))}

	for i := range tokens {
		if !found.used[i] {
			found.scan(i)
		}
	}

	return found.requires
}

// scan records the use of require that starts at the token at i, if any.
func (s *scanner) scan(i int) {
	if s.protectedCall(i) {
		return
	}

	ref, end := s.requireRef(i)

	switch {
	case ref == "", s.isTarget(i, end):
	case s.startsArgs(end):
		name, static := s.callName(end)
		s.record(RequireInfo{Name: name, Line: s.tokens[i].Line, Static: static, Protected: false, Indirect: ""})
	case !s.isCopy(i):
		s.record(RequireInfo{
			Name: "", Line: s.tokens[i].Line, Static: false, Protected: false, Indirect: ref + " used as a value",
		})
	}
}

// requireRef returns how the tokens from i name the global require,
// "require", "_G.require" or "_ENV.require", and the index after them; or ""
// and i when they do not name it. A name after "." or ":" is a field.
func (s *scanner) requireRef(i int) (string, int) {
	if s.isSymbol(i-1, ".") || s.isSymbol(i-1, ":") {
		return "", i
	}

	switch table := s.name(i); table {
	case requireName:
		return requireName, i + 1
	case "_G", "_ENV":
		ref := table + "." + requireName

		// Written _G.require.
		if s.isSymbol(i+1, ".") && s.name(i+fieldRefLen-1) == requireName {
			return ref, i + fieldRefLen
		}

		// Written _G["require"].
		closing := i + indexRefLen - 1
		if value, ok := s.stringAt(closing - 1); ok && value == requireName && s.isSymbol(i+1, "[") &&
			s.isSymbol(closing, "]") {
			return ref, i + indexRefLen
		}
	}

	return "", i
}

// protectedCall records `pcall(require, ...)` or `xpcall(require, handler,
// ...)` at i, a call of require in protected mode, and reports whether there
// is one.
func (s *scanner) protectedCall(i int) bool {
	caller, open := s.name(i), i+1
	if caller != "pcall" && caller != "xpcall" || s.isSymbol(i-1, ".") || s.isSymbol(i-1, ":") ||
		!s.isSymbol(open, "(") {
		return false
	}

	first := open + 1

	ref, end := s.requireRef(first)
	if ref == "" {
		return false
	}

	args := end
	if caller == "xpcall" && s.isSymbol(args, ",") {
		// xpcall(f, handler, ...) calls f(...).
		args = s.argEnd(args + 1)
	}

	var (
		name   string
		static bool
	)

	switch {
	case s.isSymbol(args, ","):
		name, static = s.literalArg(args + 1)
	case s.isSymbol(args, ")"):
		// require gets no argument.
	default:
		// Not an argument of its own, as in pcall(require .. x).
		return false
	}

	s.record(RequireInfo{Name: name, Line: s.tokens[i].Line, Static: static, Protected: true, Indirect: ""})

	for k := first; k < end; k++ {
		s.used[k] = true
	}

	return true
}

// startsArgs reports whether the arguments of a call start at i: "(", a string
// literal or a table constructor.
func (s *scanner) startsArgs(i int) bool {
	_, isString := s.stringAt(i)

	return isString || s.isSymbol(i, "(") || s.isSymbol(i, "{")
}

// callName returns the value of the string literal a call whose arguments
// start at i gets first, and whether there is one.
func (s *scanner) callName(i int) (string, bool) {
	if value, ok := s.stringAt(i); ok {
		return value, true
	}

	if s.isSymbol(i, "(") {
		return s.literalArg(i + 1)
	}

	return "", false
}

// literalArg returns the value of the argument that starts at i when it is a
// string literal, in parentheses or not, and whether it is one: the literal
// must be followed by the end of the argument, "," or ")".
func (s *scanner) literalArg(i int) (string, bool) {
	depth := 0
	for s.isSymbol(i, "(") {
		depth++
		i++
	}

	value, ok := s.stringAt(i)
	if !ok {
		return "", false
	}

	for i++; depth > 0; depth-- {
		if !s.isSymbol(i, ")") {
			return "", false
		}

		i++
	}

	if !s.isSymbol(i, ",") && !s.isSymbol(i, ")") {
		return "", false
	}

	return value, true
}

// argEnd returns the index of the "," or ")" that ends the argument that
// starts at i, or the number of tokens when none does. Brackets and blocks
// nest: the argument can be a function.
func (s *scanner) argEnd(i int) int {
	depth := 0

	for ; i < len(s.tokens); i++ {
		if depth == 0 && (s.isSymbol(i, ",") || s.isSymbol(i, ")")) {
			return i
		}

		tok := s.tokens[i]
		depth += nesting(tok.Kind, string(s.src[tok.Start:tok.End]))
	}

	return i
}

// isTarget reports whether the name of require from i up to end is no use of
// require but a name being declared or defined, or assigned to.
func (s *scanner) isTarget(i, end int) bool {
	switch {
	case s.isSymbol(end, "="):
		// require = f, local require = f, { require = f }.
		return true
	case s.name(i-1) == keywordFunction, s.name(i-1) == "goto":
		return true
	case s.isSymbol(i-1, "::") && s.isSymbol(end, "::"):
		// A label.
		return true
	default:
		return s.inNameList(i)
	}
}

// inNameList reports whether the name at i is among the names of a local,
// global or for declaration, `local a <const>, require`, or among the
// parameters of a function.
func (s *scanner) inNameList(i int) bool {
	j := s.attribStart(i - 1)

	for s.isSymbol(j, ",") {
		j = s.attribStart(j - 1)
		if !s.isKind(j, lex.Name) {
			return false
		}

		j = s.attribStart(j - 1)
	}

	switch {
	case s.name(j) == keywordLocal, s.name(j) == "global", s.name(j) == "for":
		return true
	case s.isSymbol(j, "("):
		return s.isFunctionHeader(j - 1)
	default:
		return false
	}
}

// attribStart returns the index before the attribute "<name>" that ends at
// i, or i when none does.
func (s *scanner) attribStart(i int) int {
	if s.isSymbol(i, ">") && s.isKind(i-1, lex.Name) && s.isSymbol(i-attribLen+1, "<") {
		return i - attribLen
	}

	return i
}

// isFunctionHeader reports whether the token at i ends the head of a function
// before its parameters: "function", or the name of a function statement,
// `function a.b:c`.
func (s *scanner) isFunctionHeader(i int) bool {
	for s.isKind(i, lex.Name) && s.name(i) != keywordFunction {
		if !s.isSymbol(i-1, ".") && !s.isSymbol(i-1, ":") {
			i--

			break
		}

		i -= 2
	}

	return s.name(i) == keywordFunction
}

// isCopy reports whether the name require at i is the value of an assignment
// to require, `require = require`, `local require = require` or
// `_G.require = require`, which stores it where the calls are found again.
// Stored in a table constructor, `{ require = require }`, it is a use.
func (s *scanner) isCopy(i int) bool {
	if !s.isSymbol(i-1, "=") {
		return false
	}

	for _, start := range []int{i - 2, i - 1 - fieldRefLen, i - 1 - indexRefLen} {
		if ref, end := s.requireRef(start); ref != "" && end == i-1 {
			return !s.isSymbol(start-1, "{") && !s.isSymbol(start-1, ",") && !s.isSymbol(start-1, ";")
		}
	}

	return false
}

// record records a use of require.
func (s *scanner) record(info RequireInfo) {
	s.requires = append(s.requires, info)
}

// stringAt returns the value of the string literal at i, and whether there is
// one.
func (s *scanner) stringAt(i int) (string, bool) {
	if !s.isKind(i, lex.String) && !s.isKind(i, lex.LongString) {
		return "", false
	}

	tok := s.tokens[i]

	return string(stringValue(s.src[tok.Start:tok.End], tok.Kind)), true
}

// isKind reports whether there is a token at i and it is of kind.
func (s *scanner) isKind(i int, kind lex.Kind) bool {
	return i >= 0 && i < len(s.tokens) && s.tokens[i].Kind == kind
}

// name returns the text of the token at i when it is a Name, "" otherwise.
func (s *scanner) name(i int) string {
	if !s.isKind(i, lex.Name) {
		return ""
	}

	return string(s.src[s.tokens[i].Start:s.tokens[i].End])
}

// isSymbol reports whether the token at i is the symbol symbol.
func (s *scanner) isSymbol(i int, symbol string) bool {
	return s.isKind(i, lex.Symbol) && string(s.src[s.tokens[i].Start:s.tokens[i].End]) == symbol
}
