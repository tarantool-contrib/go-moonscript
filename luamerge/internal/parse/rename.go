package parse

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/lex"
)

// ErrNotALiteral is the cause of the error PrefixNames returns for a call
// whose Literal is not a string literal of the source that names the call's
// module: a call Parse did not find in the source.
var ErrNotALiteral = errors.New("the call's literal is not a string literal of the source that names its module")

// PrefixNames returns source with prefix put in front of the module name of
// each call in calls, static calls of require that Parse found in source. The
// prefix goes into the string literal that names the module, where its value
// starts, so the call requires prefix and the name, and nothing else of
// source changes: not the form of the literal, not a byte outside it, not a
// line. A short string takes the prefix with an escape for its own quote, a
// backslash and a control character. A long bracket string takes it as it is,
// unless the prefix has a line break, which the string would read as "\n",
// or the string's closing bracket: then the literal becomes a short string of
// the prefixed name, after as many line breaks as the literal had, so every
// token after it stays on its line.
func PrefixNames(source []byte, calls []RequireInfo, prefix string) ([]byte, error) {
	sorted := slices.SortedFunc(slices.Values(calls), func(a, b RequireInfo) int {
		return cmp.Compare(a.Literal.Start, b.Literal.Start)
	})

	renamed := make([]byte, 0, len(source)+len(calls)*len(prefix))
	done := 0

	for _, call := range sorted {
		lit := call.Literal
		if lit.Start < done || lit.End > len(source) || lit.Start >= lit.End {
			return nil, fmt.Errorf("prefix the module of %+v: %w", call, ErrNotALiteral)
		}

		text := source[lit.Start:lit.End]

		kind, ok := literalKind(text)
		if !ok || string(stringValue(text, kind)) != call.Name {
			return nil, fmt.Errorf("prefix the module of %+v, %q: %w", call, text, ErrNotALiteral)
		}

		renamed = append(renamed, source[done:lit.Start]...)
		renamed = append(renamed, prefixLiteral(text, kind, prefix)...)
		done = lit.End
	}

	return append(renamed, source[done:]...), nil
}

// literalKind returns the kind of text when it is a string literal, the text
// of one String or LongString token, and whether it is one.
func literalKind(text []byte) (lex.Kind, bool) {
	tokens, err := lex.New(text).Tokens()
	if err != nil || len(tokens) != 1 || tokens[0].Start != 0 || tokens[0].End != len(text) {
		return lex.Invalid, false
	}

	kind := tokens[0].Kind

	return kind, kind == lex.String || kind == lex.LongString
}

// prefixLiteral returns lit, a string literal of kind, with prefix put in
// front of its value; see PrefixNames.
func prefixLiteral(lit []byte, kind lex.Kind, prefix string) []byte {
	if kind == lex.String {
		quote := lit[0]

		return slices.Concat(lit[:1], shortStringText([]byte(prefix), quote), lit[1:])
	}

	// The value starts after the opening bracket and a line break right
	// after it, which Lua skips.
	level := bytes.IndexByte(lit[1:], '[')
	start := level + len("[[")
	start += lex.LineBreak(lit, start)

	closing := "]" + strings.Repeat("=", level) + "]"
	body := string(lit[start : len(lit)-len(closing)])

	if !strings.ContainsAny(prefix, "\r\n") && !strings.Contains(prefix+body+"]", closing) {
		return slices.Concat(lit[:start], []byte(prefix), lit[start:])
	}

	value := append([]byte(prefix), stringValue(lit, kind)...)
	lineBreaks := 0

	for i := 0; i < len(lit); i++ {
		if n := lex.LineBreak(lit, i); n > 0 {
			lineBreaks++
			i += n - 1
		}
	}

	short := slices.Concat([]byte{'"'}, shortStringText(value, '"'), []byte{'"'})

	return append(bytes.Repeat([]byte("\n"), lineBreaks), short...)
}

// shortStringText returns value written for a short string quoted with
// quote: an escape for the quote, a backslash and a control character, a
// three-digit decimal escape so that no digit after it is taken into it, and
// every other byte as it is.
func shortStringText(value []byte, quote byte) []byte {
	text := make([]byte, 0, len(value))

	for _, char := range value {
		switch {
		case char == quote || char == '\\':
			text = append(text, '\\', char)
		case char < ' ' || char == '\x7f':
			text = fmt.Appendf(text, `\%03d`, char)
		default:
			text = append(text, char)
		}
	}

	return text
}
