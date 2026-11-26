package lua

// go-moonscript patch: string.format and number-to-string conversion of the
// LuaJIT dialect, as LuaJIT 2.1's lj_strfmt.c and lj_strfmt_num.c:
//
//   - the floating-point conversions round exact ties away from zero
//     (string.format("%.0f", 2.5) is "3", tostring(123456789012345) is
//     "1.2345678901235e+14"; C and Lua 5.1 round them to even), a NaN is
//     "nan" whatever its sign, %F is accepted, %a/%A format hexadecimal
//     floats;
//   - %d/%i convert to a 64-bit integer, %o/%u/%x/%X to an unsigned one, %c
//     to an int32, with lj_num2int64/lj_num2u64/lj_num2int's results out of
//     range;
//   - %s and %q take any value, converted as tostring does (a __tostring
//     metamethod is called), embedded NULs included; %q escapes control
//     characters as \ddd (no leading zeros unless a digit follows) and has
//     no width; %p gives the address of an object ("NULL" for nil,
//     booleans and numbers);
//   - an invalid conversion is "invalid option '%<spec>' to 'format'" with
//     the spec as far as it was read (a width or precision of 3 digits is
//     invalid), a missing argument "bad argument #n to 'format' (value
//     expected)".

import (
	"math"
	"math/big"
	"math/bits"
	"strconv"
	"strings"
	"unsafe"
)

// numberString converts a number to a string in the dialect of the state.
func (ls *LState) numberString(n LNumber) string {
	if ls.Options.Dialect == DialectLuaJIT {
		return JITNumberString(float64(n))
	}
	return n.String()
}

// lvAsString is LVAsString in the dialect of the state.
func (ls *LState) lvAsString(v LValue) string {
	if n, ok := v.(LNumber); ok && ls.Options.Dialect == DialectLuaJIT {
		return JITNumberString(float64(n))
	}
	return LVAsString(v)
}

// JITNumberString is the conversion of a number to a string of LuaJIT 2.1:
// "%.14g" with exact ties rounded away from zero, "nan" for every NaN.
func JITNumberString(f float64) string {
	if f > -1e14 && f < 1e14 && f == float64(int64(f)) {
		return LNumber(f).String() // at most 14 digits: no rounding
	}
	if math.IsNaN(f) {
		return "nan"
	}
	if math.IsInf(f, 0) || !jitIsTie(f, 13-decimalExponent(f)) {
		return number2str(f)
	}
	return jitFormatFloat(strFormatSpec{width: -1, prec: 14}, 'g', f)
}

// decimalExponent is the exponent of f in decimal scientific notation.
func decimalExponent(f float64) int {
	s := strconv.FormatFloat(f, 'e', 16, 64)
	x, _ := strconv.Atoi(s[strings.LastIndexByte(s, 'e')+1:])
	return x
}

// jitIsTie reports whether f is exactly halfway between two numbers of
// frac fractional digits (frac < 0: -frac integer digits dropped), where
// LuaJIT rounds away from zero and Go, like C, to even.
func jitIsTie(f float64, frac int) bool {
	if f == 0 || math.IsInf(f, 0) || math.IsNaN(f) {
		return false
	}
	b := math.Float64bits(f)
	exp := int(b >> 52 & 0x7ff)
	mant := b & (1<<52 - 1)
	if exp == 0 {
		exp = 1
	} else {
		mant |= 1 << 52
	}
	tz := bits.TrailingZeros64(mant)
	e := exp - 1075 + tz // |f| = m * 2^e with m odd
	if e < 0 {
		// |f| has exactly -e fractional digits, the last one a 5: a tie
		// when the rounding drops that digit only
		return frac == -e-1
	}
	// an integer: a tie when it ends in 5 followed by j-1 zeros, j the
	// digits dropped, which needs 2^(j-1) to be its power of two
	j := -frac
	if j < 1 || j != e+1 {
		return false
	}
	m := new(big.Int).Lsh(new(big.Int).SetUint64(mant>>tz), uint(e))
	unit := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(j)), nil)
	half := new(big.Int).Mul(big.NewInt(5), new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(j-1)), nil))
	return new(big.Int).Mod(m, unit).Cmp(half) == 0
}

// jitDigits is strconv.FormatFloat(f, c, prec, 64) for f >= 0 and c 'e' or
// 'f', with exact ties rounded away from zero (LuaJIT) instead of to even.
func jitDigits(f float64, c byte, prec int) string {
	frac := prec
	if c == 'e' {
		frac -= decimalExponent(f)
	}
	if !jitIsTie(f, frac) {
		return strconv.FormatFloat(f, c, prec, 64)
	}
	// one more digit is exact (a 5); drop it and round up
	s := strconv.FormatFloat(f, c, prec+1, 64)
	mant, x := s, 0
	if c == 'e' {
		k := strings.IndexByte(s, 'e')
		mant = s[:k]
		x, _ = strconv.Atoi(s[k+1:])
	}
	d := []byte(mant[:len(mant)-1])
	i := len(d) - 1
	for ; i >= 0; i-- {
		if d[i] == '.' {
			continue
		}
		if d[i] < '9' {
			d[i]++
			break
		}
		d[i] = '0'
	}
	if i < 0 { // carried out of the first digit
		d = append([]byte{'1'}, d...)
		if c == 'e' { // 10.00 -> 1.000, one more in the exponent
			digits := strings.ReplaceAll(string(d), ".", "")[:prec+1]
			d = []byte(digits[:1])
			if prec > 0 {
				d = append(append(d, '.'), digits[1:]...)
			}
			x++
		}
	}
	out := strings.TrimSuffix(string(d), ".")
	if c == 'e' {
		sign := "+"
		if x < 0 {
			sign, x = "-", -x
		}
		e := strconv.Itoa(x)
		if len(e) < 2 {
			e = "0" + e
		}
		out += "e" + sign + e
	}
	return out
}

// jitFormatFloat formats %e, %f, %g (and their uppercase) as LuaJIT: C's
// conversions with ties rounded away from zero.
func jitFormatFloat(spec strFormatSpec, conv byte, f float64) string {
	upper := conv == 'E' || conv == 'F' || conv == 'G'
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return jitNonFinite(spec, upper, f)
	}
	sign := ""
	if math.Signbit(f) {
		sign = "-"
	} else if spec.plus {
		sign = "+"
	} else if spec.space {
		sign = " "
	}
	f = math.Abs(f)
	prec := spec.prec
	if prec < 0 {
		prec = 6
	}
	expForm := func(prec int) string {
		body := jitDigits(f, 'e', prec)
		if spec.sharp && prec == 0 {
			k := strings.IndexByte(body, 'e')
			body = body[:k] + "." + body[k:]
		}
		return body
	}
	var body string
	switch conv | 0x20 {
	case 'e':
		body = expForm(prec)
	case 'f':
		body = jitDigits(f, 'f', prec)
		if spec.sharp && prec == 0 {
			body += "."
		}
	case 'g':
		if prec == 0 {
			prec = 1
		}
		e := jitDigits(f, 'e', prec-1)
		x, _ := strconv.Atoi(e[strings.IndexByte(e, 'e')+1:])
		if prec > x && x >= -4 {
			body = jitDigits(f, 'f', prec-1-x)
			if spec.sharp && !strings.Contains(body, ".") {
				body += "."
			}
		} else {
			body = expForm(prec - 1)
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

// strFormatJIT is LuaJIT 2.1's string.format.
func strFormatJIT(L *LState) int {
	str := L.CheckString(1)
	top := L.GetTop()
	arg := 1
	out := make([]byte, 0, len(str)+16)
	at := func(p int) byte { // the C string's bytes, NUL at the end
		if p < len(str) {
			return str[p]
		}
		return 0
	}
	for i := 0; i < len(str); {
		if str[i] != '%' {
			j := strings.IndexByte(str[i:], '%')
			if j < 0 {
				j = len(str) - i
			}
			out = append(out, str[i:i+j]...)
			i += j
			continue
		}
		if at(i+1) == '%' {
			out = append(out, '%')
			i += 2
			continue
		}
		start, p := i, i+1
		spec := strFormatSpec{width: -1, prec: -1}
	flags:
		for ; at(p)-' ' <= '0'-' '; p++ {
			switch at(p) {
			case '-':
				spec.minus = true
			case '+':
				spec.plus = true
			case '0':
				spec.zero = true
			case ' ':
				spec.space = true
			case '#':
				spec.sharp = true
			default:
				break flags
			}
		}
		if c := at(p); strFormatIsDigit(c) {
			spec.width = int(c - '0')
			p++
			if c := at(p); strFormatIsDigit(c) {
				spec.width = spec.width*10 + int(c-'0')
				p++
			}
		}
		if at(p) == '.' {
			p++
			spec.prec = 0
			if c := at(p); strFormatIsDigit(c) {
				spec.prec = int(c - '0')
				p++
				if c := at(p); strFormatIsDigit(c) {
					spec.prec = spec.prec*10 + int(c-'0')
					p++
				}
			}
		}
		conv := at(p)
		if strings.IndexByte("aAcdeEfFgGiopqsuxX", conv) < 0 || conv == 0 {
			if conv >= 32 {
				p++
			}
			L.RaiseError("invalid option '%s' to 'format'", str[start:min(p, len(str))])
		}
		i = p + 1
		arg++
		if arg > top {
			L.ArgError(arg, "value expected")
		}
		width := max(spec.width, 0)
		switch conv {
		case 'd', 'i':
			out = jitPutInt(out, spec, true, uint64(cvttsd2si(float64(L.CheckNumber(arg)))), 0)
		case 'o', 'u', 'x', 'X':
			out = jitPutInt(out, spec, false, jitNum2U64(float64(L.CheckNumber(arg))), conv)
		case 'c':
			c := byte(jitCheckInt(L, arg))
			if spec.minus {
				out = append(out, c)
			}
			for k := 1; k < width; k++ {
				out = append(out, ' ')
			}
			if !spec.minus {
				out = append(out, c)
			}
		case 'e', 'E', 'f', 'F', 'g', 'G':
			out = append(out, jitFormatFloat(spec, conv, float64(L.CheckNumber(arg)))...)
		case 'a', 'A':
			out = append(out, jitFormatHexFloat(spec, conv == 'A', float64(L.CheckNumber(arg)))...)
		case 's', 'q':
			v := L.Get(arg)
			var s string
			switch lv := v.(type) {
			case LString:
				s = string(lv)
			case LNumber:
				s = JITNumberString(float64(lv))
			default:
				s = L.lvAsString(L.ToStringMeta(v))
			}
			if conv == 'q' {
				out = jitPutQuoted(out, s)
				continue
			}
			if spec.prec >= 0 && len(s) > spec.prec {
				s = s[:spec.prec]
			}
			if spec.minus {
				out = append(out, s...)
			}
			for k := len(s); k < width; k++ {
				out = append(out, ' ')
			}
			if !spec.minus {
				out = append(out, s...)
			}
		case 'p':
			out = jitPutPtr(out, jitObjPtr(L.Get(arg)))
		}
	}
	L.Push(LString(out))
	return 1
}

// jitNum2U64 is lj_num2u64: the conversion to uint64 through int64, with
// the values from 2^63 on taken modulo 2^64 first.
func jitNum2U64(f float64) uint64 {
	if f >= 1<<63 {
		return uint64(cvttsd2si(f - (1 << 64)))
	}
	return uint64(cvttsd2si(f))
}

// jitPutInt is lj_strfmt_putfxint: k as a signed (%d) or unsigned decimal,
// octal ('o') or hex ('x', 'X') integer.
func jitPutInt(out []byte, spec strFormatSpec, signed bool, k uint64, conv byte) []byte {
	prefix := ""
	if signed {
		switch {
		case int64(k) < 0:
			k = -k
			prefix = "-"
		case spec.plus:
			prefix = "+"
		case spec.space:
			prefix = " "
		}
	}
	if spec.prec >= 0 {
		spec.zero = false
	}
	var digits string
	switch {
	case k == 0:
		if spec.prec != 0 || conv == 'o' && spec.sharp {
			digits = "0"
		}
	case conv == 'x' || conv == 'X':
		digits = strconv.FormatUint(k, 16)
		if conv == 'X' {
			digits = strings.ToUpper(digits)
		}
		if spec.sharp {
			prefix = "0" + string(conv)
		}
	case conv == 'o':
		digits = strconv.FormatUint(k, 8)
		if spec.sharp {
			digits = "0" + digits
		}
	default:
		digits = strconv.FormatUint(k, 10)
	}
	prec := max(spec.prec, len(digits))
	width := max(spec.width, 0)
	pprec := prec + len(prefix)
	if !spec.minus && !spec.zero {
		for ; width > pprec; width-- {
			out = append(out, ' ')
		}
	}
	out = append(out, prefix...)
	if !spec.minus && spec.zero {
		for ; width > pprec; width-- {
			out = append(out, '0')
		}
	}
	for n := len(digits); n < prec; n++ {
		out = append(out, '0')
	}
	out = append(out, digits...)
	if spec.minus {
		for ; width > pprec; width-- {
			out = append(out, ' ')
		}
	}
	return out
}

// jitNonFinite formats an infinity or a NaN as lj_strfmt_num: "inf",
// "-inf", "nan" (never signed; the '+' flag is ignored for it), padded
// with spaces.
func jitNonFinite(spec strFormatSpec, upper bool, f float64) string {
	prefix := ""
	body := "inf"
	if math.IsNaN(f) {
		body = "nan"
		if spec.space {
			prefix = " "
		}
	} else {
		switch {
		case f < 0:
			prefix = "-"
		case spec.plus:
			prefix = "+"
		case spec.space:
			prefix = " "
		}
	}
	if upper {
		body = strings.ToUpper(body)
	}
	spec.zero = false
	return spec.pad(prefix, body)
}

// jitFormatHexFloat is lj_strfmt_num's %a/%A: normalized (subnormals too),
// the shortest exact precision by default, rounding half up.
func jitFormatHexFloat(spec strFormatSpec, upper bool, f float64) string {
	if math.IsInf(f, 0) || math.IsNaN(f) {
		return jitNonFinite(spec, upper, f)
	}
	hexdig := "0123456789abcdef"
	px, pp := "x", "p"
	if upper {
		hexdig, px, pp = "0123456789ABCDEF", "X", "P"
	}
	u := math.Float64bits(f)
	prefix := ""
	switch {
	case u>>63 != 0:
		prefix = "-"
	case spec.plus:
		prefix = "+"
	case spec.space:
		prefix = " "
	}
	e := int(u >> 52 & 0x7ff)
	t := u & (1<<52 - 1)
	if e != 0 {
		t |= 1 << 52
		e -= 1023
	} else if t != 0 { // normalize a subnormal
		shift := 53 - bits.Len64(t)
		e = -1022 - shift
		t <<= uint(shift)
	}
	prec := spec.prec
	if prec < 0 { // the smallest precision giving the exact value
		lo, hi := uint32(t), uint32(t>>32)
		if lo != 0 {
			prec = 13 - bits.TrailingZeros32(lo)/4
		} else {
			prec = 5 - bits.TrailingZeros32(hi|0x100000)/4
		}
	} else if prec < 13 {
		t += 1 << uint(51-prec*4)
	}
	esign := "+"
	if e < 0 {
		esign = "-"
		e = -e
	}
	var body strings.Builder
	body.WriteByte(byte('0' + t>>52))
	if prec > 0 || spec.sharp {
		body.WriteByte('.')
		frac := make([]byte, prec)
		v := t
		if prec < 13 {
			v >>= uint(52 - prec*4)
		} else {
			for k := prec - 1; k >= 13; k-- {
				frac[k] = '0'
			}
		}
		for k := min(prec, 13) - 1; k >= 0; k-- {
			frac[k] = hexdig[v&15]
			v >>= 4
		}
		body.Write(frac)
	}
	body.WriteString(pp + esign + strconv.Itoa(e))
	head := prefix + "0" + px
	width := max(spec.width, 0)
	n := width - len(head) - body.Len()
	switch {
	case n <= 0:
		return head + body.String()
	case spec.minus:
		return head + body.String() + strings.Repeat(" ", n)
	case spec.zero:
		return head + strings.Repeat("0", n) + body.String()
	}
	return strings.Repeat(" ", n) + head + body.String()
}

// jitPutQuoted is lj_strfmt_putquoted.
func jitPutQuoted(out []byte, s string) []byte {
	out = append(out, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"' || c == '\\' || c == '\n':
			out = append(out, '\\', c)
		case c < 32 || c == 127:
			out = append(out, '\\')
			if c >= 100 || i+1 < len(s) && strFormatIsDigit(s[i+1]) {
				out = append(out, '0'+c/100, '0'+c/10%10, '0'+c%10)
			} else if c >= 10 {
				out = append(out, '0'+c/10, '0'+c%10)
			} else {
				out = append(out, '0'+c)
			}
		default:
			out = append(out, c)
		}
	}
	return append(out, '"')
}

// jitObjPtr is lj_obj_ptr: the address of a garbage-collected object, 0
// for the other values.
func jitObjPtr(v LValue) uintptr {
	switch lv := v.(type) {
	case LString:
		return uintptr(unsafe.Pointer(unsafe.StringData(string(lv))))
	case *LTable:
		return uintptr(unsafe.Pointer(lv))
	case *LFunction:
		return uintptr(unsafe.Pointer(lv))
	case *LUserData:
		return uintptr(unsafe.Pointer(lv))
	case *LState:
		return uintptr(unsafe.Pointer(lv))
	}
	return 0
}

// jitPutPtr is lj_strfmt_wptr: "NULL", or 0x and the hex digits of the
// address, 8 of them for a 32-bit one, else 8 more for each byte of the
// upper half.
func jitPutPtr(out []byte, x uintptr) []byte {
	if x == 0 {
		return append(out, "NULL"...)
	}
	n := 2 + 2*4
	if hi := uint64(x) >> 32; hi != 0 {
		n += 2 + 2*((bits.Len32(uint32(hi))-1)>>3)
	}
	buf := make([]byte, n)
	buf[0], buf[1] = '0', 'x'
	for i, v := n-1, uint64(x); i >= 2; i, v = i-1, v>>4 {
		buf[i] = "0123456789abcdef"[v&15]
	}
	return append(out, buf...)
}
