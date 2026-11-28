package format

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/tarantool-contrib/go-moonscript/lua/parse"
)

var keywords = map[string]bool{
	"and": true, "break": true, "do": true, "else": true, "elseif": true, "end": true,
	"false": true, "for": true, "function": true, "if": true, "in": true, "local": true,
	"nil": true, "not": true, "or": true, "repeat": true, "return": true, "then": true,
	"true": true, "until": true, "while": true,
}

// isName reports whether s is a name of the dialect: letters, digits and
// "_", not starting with a digit, no keyword ("goto" is one under LuaJIT,
// whose names may also hold bytes 128-255).
func isName(s string, jit bool) bool {
	if s == "" || isDigit(s[0]) {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || jit && c >= 0x80) {
			return false
		}
	}
	if keywords[s] {
		return false
	}
	return !(jit && s == "goto")
}

// quoteString returns a short string literal holding the bytes of s:
// double-quoted, or single-quoted if s holds a double quote and no single
// one (or the reverse if single quotes are preferred). It escapes the
// backslash, the quote, the control characters (as \a \b \f \n \r \t \v
// or decimal \ddd), DEL and the bytes that are not part of valid UTF-8
// (as \ddd); decimal escapes are written with three digits before a digit.
// The escapes are those of Lua 5.1, which LuaJIT reads the same.
func quoteString(s string, single bool) string {
	q := byte('"')
	if single {
		q = '\''
	}
	if strings.IndexByte(s, q) >= 0 {
		other := byte('\'')
		if single {
			other = '"'
		}
		if strings.IndexByte(s, other) < 0 {
			q = other
		}
	}
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte(q)
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == q || c == '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
			i++
			continue
		case c == '\a':
			b.WriteString(`\a`)
		case c == '\b':
			b.WriteString(`\b`)
		case c == '\f':
			b.WriteString(`\f`)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c == '\t':
			b.WriteString(`\t`)
		case c == '\v':
			b.WriteString(`\v`)
		case c < 0x20 || c == 0x7f:
			writeDecimal(&b, c, i+1 < len(s) && isDigit(s[i+1]))
		case c < 0x80:
			b.WriteByte(c)
		default:
			r, n := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && n <= 1 {
				writeDecimal(&b, c, i+1 < len(s) && isDigit(s[i+1]))
				i++
				continue
			}
			b.WriteString(s[i : i+n])
			i += n
			continue
		}
		i++
	}
	b.WriteByte(q)
	return b.String()
}

func writeDecimal(b *strings.Builder, c byte, beforeDigit bool) {
	b.WriteByte('\\')
	d := strconv.Itoa(int(c))
	if beforeDigit {
		for i := len(d); i < 3; i++ {
			b.WriteByte('0')
		}
	}
	b.WriteString(d)
}

// requote returns the short string literal raw with the quote style
// asked for (single or double quotes), if that needs no more escapes: its
// escaped old quotes are unescaped, as they need not be escaped anymore.
// It returns raw if raw is no short string literal, already has the quote,
// or holds the new quote unescaped.
func requote(raw string, single bool) string {
	q := byte('"')
	if single {
		q = '\''
	}
	if len(raw) < 2 || raw[0] != '"' && raw[0] != '\'' || raw[0] == q {
		return raw
	}
	old := raw[0]
	body := raw[1 : len(raw)-1]
	var b strings.Builder
	b.Grow(len(raw))
	b.WriteByte(q)
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch {
		case c == q:
			return raw
		case c == '\\' && i+1 < len(body):
			if body[i+1] == old {
				b.WriteByte(old)
			} else {
				b.WriteByte(c)
				b.WriteByte(body[i+1])
			}
			i++
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte(q)
	return b.String()
}

// numeral is a canonical numeral for the value a NumberExpr's Value
// stands for (as the compiler converts it): Value itself if it is a
// numeral of the dialect, else the shortest decimal numeral of the value,
// 1e999 for the infinity. neg is set for a negative value (the numeral is
// of its magnitude, to write after a unary minus) and nan for NaN (which
// has no numeral: 0/0, which the compiler folds to NaN).
func numeral(value string, jit bool) (s string, neg, nan bool) {
	if isNumeral(value, jit) {
		return value, false, false
	}
	v, ok := numberValue(value)
	if !ok || math.IsNaN(v) {
		return "", false, true
	}
	if math.Signbit(v) {
		neg, v = true, -v
	}
	if math.IsInf(v, 0) {
		return "1e999", neg, false
	}
	return strconv.FormatFloat(v, 'g', -1, 64), neg, false
}

// isNumeral reports whether s is one numeral token of the dialect that is
// no LuaJIT cdata numeral.
func isNumeral(s string, jit bool) bool {
	if s == "" || !isDigit(s[0]) && !(s[0] == '.' && len(s) > 1 && isDigit(s[1])) {
		return false
	}
	d := parse.Lua51
	if jit {
		d = parse.LuaJIT
	}
	toks, err := parse.Tokenize([]byte(s), "numeral", parse.Options{Dialect: d})
	if err != nil || len(toks) != 2 || toks[0].Span().End != len(s) {
		return false
	}
	if jit {
		_, ok := parse.ScanNumberLuaJIT(s)
		return ok
	}
	return true
}

// numberValue converts a numeral as the compiler does (C's strtod, with
// spaces around it): decimal or hexadecimal, inf, nan.
func numberValue(s string) (float64, bool) {
	s = strings.Trim(s, " \t\n\v\f\r")
	if i := strings.IndexByte(s, 0); i >= 0 {
		s = s[:i]
	}
	neg := false
	if s != "" && (s[0] == '-' || s[0] == '+') {
		neg = s[0] == '-'
		s = s[1:]
	}
	if s == "" {
		return 0, false
	}
	var v float64
	switch strings.ToLower(s) {
	case "inf", "infinity":
		v = math.Inf(1)
	case "nan":
		v = math.NaN()
	default:
		t := s
		if len(t) > 1 && t[0] == '0' && t[1]|0x20 == 'x' && !strings.ContainsAny(t, "pP") {
			t += "p0"
		}
		if strings.ContainsAny(t, "_") {
			return 0, false
		}
		var err error
		v, err = strconv.ParseFloat(t, 64)
		if err != nil {
			if ne, ok := err.(*strconv.NumError); !ok || ne.Err != strconv.ErrRange {
				return 0, false
			}
		}
	}
	if neg {
		v = -v
	}
	return v, true
}
