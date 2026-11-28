package lua

// go-moonscript patch: string.format is Lua 5.1's str_format (lstrlib.c):
// the conversions of C's sprintf with a long (d, i), an unsigned long (o,
// u, x, X), a double (e, E, f, g, G), an int as a byte (c), a string or
// number (s; the whole string when it is 100 bytes or longer and no
// precision is given, else up to its first NUL) and Lua's %q, with
// "-+ #0" flags and up to two digits of width and of precision. Based on
// yuin/gopher-lua PR #557 (madhavschahar), which did %s, %c and %q.

import (
	"math"
	"strconv"
	"strings"
)

type strFormatSpec struct {
	minus, plus, space, sharp, zero bool
	width, prec                     int // -1: none
}

func strFormat(L *LState) int {
	if L.Dialect() == DialectLuaJIT { // go-moonscript patch (luajitlib.go)
		return strFormatJIT(L)
	}
	str := L.CheckString(1)
	top := L.GetTop()
	arg := 1
	out := make([]byte, 0, len(str)+16)
	for i := 0; i < len(str); {
		c := str[i]
		i++
		if c != '%' {
			out = append(out, c)
			continue
		}
		if i < len(str) && str[i] == '%' {
			out = append(out, '%')
			i++
			continue
		}
		arg++
		if arg > top {
			L.ArgError(arg, "no value")
		}
		spec := strFormatSpec{width: -1, prec: -1}
		start := i
		for i < len(str) && strings.IndexByte("-+ #0", str[i]) >= 0 {
			switch str[i] {
			case '-':
				spec.minus = true
			case '+':
				spec.plus = true
			case ' ':
				spec.space = true
			case '#':
				spec.sharp = true
			case '0':
				spec.zero = true
			}
			i++
		}
		if i-start >= len("-+ #0")+1 {
			L.RaiseError("invalid format (repeated flags)")
		}
		i, spec.width = strFormatDigits(str, i)
		if i < len(str) && str[i] == '.' {
			i, spec.prec = strFormatDigits(str, i+1)
			if spec.prec < 0 {
				spec.prec = 0
			}
		}
		if i < len(str) && strFormatIsDigit(str[i]) {
			L.RaiseError("invalid format (width or precision too long)")
		}
		conv := byte(0)
		if i < len(str) {
			conv = str[i]
			i++
		}
		var item string
		switch conv {
		case 'c':
			b := byte(0)
			if n := cvttsd2si(float64(L.CheckNumber(arg))); n >= math.MinInt32 && n <= math.MaxInt32 {
				b = byte(n) // (int) and then unsigned char, as C does
			}
			spec.zero = false
			item = spec.pad("", string([]byte{b}))
		case 'd', 'i':
			n := cvttsd2si(float64(L.CheckNumber(arg)))
			u := uint64(n)
			sign := ""
			if n < 0 {
				u, sign = -u, "-"
			} else if spec.plus {
				sign = "+"
			} else if spec.space {
				sign = " "
			}
			item = spec.integer(sign, "", strconv.FormatUint(u, 10), false)
		case 'o', 'u', 'x', 'X':
			u := cvttsd2usi(float64(L.CheckNumber(arg)))
			prefix := ""
			var digits string
			switch conv {
			case 'o':
				digits = strconv.FormatUint(u, 8)
			case 'u':
				digits = strconv.FormatUint(u, 10)
			case 'x', 'X':
				digits = strconv.FormatUint(u, 16)
				if spec.sharp && u != 0 {
					prefix = "0x"
				}
				if conv == 'X' {
					digits, prefix = strings.ToUpper(digits), strings.ToUpper(prefix)
				}
			}
			item = spec.integer("", prefix, digits, conv == 'o' && spec.sharp)
		case 'e', 'E', 'f', 'g', 'G':
			item = spec.float(conv, float64(L.CheckNumber(arg)))
		case 'q':
			out = strFormatQuoted(out, L.CheckString(arg))
			continue
		case 's':
			s := L.CheckString(arg)
			if spec.prec < 0 && len(s) >= 100 {
				out = append(out, s...) // too long for sprintf's buffer: whole
				continue
			}
			if k := strings.IndexByte(s, 0); k >= 0 {
				s = s[:k]
			}
			if spec.prec >= 0 && len(s) > spec.prec {
				s = s[:spec.prec]
			}
			spec.zero = false
			item = spec.pad("", s)
		default:
			// C's %c of a NUL adds nothing to the message
			L.RaiseError("invalid option '%%%s' to 'format'", strings.TrimRight(string([]byte{conv}), "\x00"))
		}
		if k := strings.IndexByte(item, 0); k >= 0 {
			item = item[:k] // the item is added with strlen
		}
		out = append(out, item...)
	}
	L.gcReserve(int64(len(out))) // go-moonscript patch: the memory limit (gc.go)
	L.Push(LString(out))
	return 1
}

// strFormatDigits reads up to two digits at s[i:]: the index after them and
// their value, -1 for none.
func strFormatDigits(s string, i int) (int, int) {
	n := -1
	for k := 0; k < 2 && i < len(s) && strFormatIsDigit(s[i]); k++ {
		if n < 0 {
			n = 0
		}
		n = n*10 + int(s[i]-'0')
		i++
	}
	return i, n
}

// strFormatQuoted is lstrlib's addquoted.
func strFormatQuoted(buf []byte, s string) []byte {
	buf = append(buf, '"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '"', '\\', '\n':
			buf = append(buf, '\\', c)
		case '\r':
			buf = append(buf, '\\', 'r')
		case 0:
			buf = append(buf, '\\', '0', '0', '0')
		default:
			buf = append(buf, c)
		}
	}
	return append(buf, '"')
}

// cvttsd2si is the conversion of a double to a long of C on x86-64: the
// value truncated, and LONG_MIN for a NaN or a value out of range.
func cvttsd2si(f float64) int64 {
	if f >= -(1<<63) && f < 1<<63 {
		return int64(f)
	}
	return math.MinInt64
}

// cvttsd2usi is the conversion of a double to an unsigned long of gcc on
// x86-64 (negative values wrap around, as their conversion to a long does).
func cvttsd2usi(f float64) uint64 {
	if f >= 1<<63 {
		return uint64(cvttsd2si(f-(1<<63))) ^ (1 << 63)
	}
	return uint64(cvttsd2si(f))
}

// pad pads sign+body to the width: with spaces on the left, on the right
// for '-', with zeros after the sign for '0'.
func (spec strFormatSpec) pad(sign, body string) string {
	n := spec.width - len(sign) - len(body)
	if n <= 0 {
		return sign + body
	}
	switch {
	case spec.minus:
		return sign + body + strings.Repeat(" ", n)
	case spec.zero:
		return sign + strings.Repeat("0", n) + body
	default:
		return strings.Repeat(" ", n) + sign + body
	}
}

// integer formats C's integer conversions: the digits are padded with
// zeros to the precision (no digits for a zero with precision 0), and the
// '0' flag is ignored when a precision is given. altOctal is %#o: the first
// digit is made a zero.
func (spec strFormatSpec) integer(sign, prefix, digits string, altOctal bool) string {
	if spec.prec >= 0 {
		spec.zero = false
		if spec.prec == 0 && digits == "0" {
			digits = ""
		} else if len(digits) < spec.prec {
			digits = strings.Repeat("0", spec.prec-len(digits)) + digits
		}
	}
	if altOctal && (digits == "" || digits[0] != '0') {
		digits = "0" + digits
	}
	return spec.pad(sign+prefix, digits)
}

// float formats C's floating-point conversions (precision 6 by default),
// infinities and NaNs as glibc does ("inf", "nan", uppercase for E and G,
// padded with spaces).
func (spec strFormatSpec) float(conv byte, f float64) string {
	sign := ""
	if math.Signbit(f) {
		sign = "-"
	} else if spec.plus {
		sign = "+"
	} else if spec.space {
		sign = " "
	}
	upper := conv == 'E' || conv == 'G'
	if math.IsInf(f, 0) || math.IsNaN(f) {
		body := "inf"
		if math.IsNaN(f) {
			body = "nan"
		}
		if upper {
			body = strings.ToUpper(body)
		}
		spec.zero = false
		return spec.pad(sign, body)
	}
	f = math.Abs(f)
	prec := spec.prec
	if prec < 0 {
		prec = 6
	}
	var body string
	switch conv {
	case 'e', 'E':
		body = strFormatE(f, prec, spec.sharp)
	case 'f':
		body = strconv.FormatFloat(f, 'f', prec, 64)
		if spec.sharp && prec == 0 {
			body += "."
		}
	case 'g', 'G':
		if prec == 0 {
			prec = 1
		}
		e := strconv.FormatFloat(f, 'e', prec-1, 64)
		x, _ := strconv.Atoi(e[strings.IndexByte(e, 'e')+1:])
		if prec > x && x >= -4 {
			body = strconv.FormatFloat(f, 'f', prec-1-x, 64)
			if spec.sharp && !strings.Contains(body, ".") {
				body += "."
			}
		} else {
			body = strFormatE(f, prec-1, spec.sharp)
		}
		if !spec.sharp { // remove trailing zeros from the fraction
			mant, exp := body, ""
			if k := strings.IndexByte(body, 'e'); k >= 0 {
				mant, exp = body[:k], body[k:]
			}
			if strings.Contains(mant, ".") {
				mant = strings.TrimRight(strings.TrimRight(mant, "0"), ".")
			}
			body = mant + exp
		}
	}
	if upper {
		body = strings.ToUpper(body)
	}
	return spec.pad(sign, body)
}

func strFormatE(f float64, prec int, sharp bool) string {
	body := strconv.FormatFloat(f, 'e', prec, 64)
	if sharp && prec == 0 {
		k := strings.IndexByte(body, 'e')
		body = body[:k] + "." + body[k:]
	}
	return body
}

func strFormatIsDigit(c byte) bool { return '0' <= c && c <= '9' }
