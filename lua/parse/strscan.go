package parse

// go-moonscript patch: LuaJIT 2.1's number scanner, lj_strscan.c's
// lj_strscan_scan, for the numerals of DialectLuaJIT and for its tonumber.
//
// Adapted from go-luascope's parser/number.go
// (https://github.com/bigbes/go-luascope, BSD-2-Clause,
// Copyright (c) 2026, Eugene Blikh; see NOTICE), a port of lj_strscan.c
// (LuaJIT, Copyright (C) 2005-2017 Mike Pall, MIT): it gained the string
// conversion's white space, sign, "inf" and "nan", and passes every digit
// to strconv. Checked against Debian's luajit 2.1.0-beta3.

import (
	"math"
	"strconv"
	"strings"
)

// numFormat is lj_strscan.c's StrScanFmt, in the same order.
type numFormat int

const (
	fmtError numFormat = iota
	fmtNum             // a double
	fmtImag            // cdata: an imaginary number (1i)
	fmtInt             // an integer without a suffix
	fmtU32             // 1U: C syntax only, an error here
	fmtI64             // cdata: 1LL
	fmtU64             // cdata: 1ULL, 1LLU
)

// scanOpt is lj_strscan.c's STRSCAN_OPT_* (STRSCAN_OPT_TONUM always: the VM
// has no integer numbers; never STRSCAN_OPT_C).
type scanOpt int

const (
	optLL   scanOpt = 1 << iota // the LL, ULL and LLU suffixes
	optImag                     // the i suffix
)

// maxExp is STRSCAN_MAXEXP: an exponent, or the count of fractional digits,
// from it on is an error.
const maxExp = 1 << 20

// ljNaN is the NaN LuaJIT's scanner gives (setnanV).
var ljNaN = math.Float64frombits(0xfff8000000000000)

// ScanNumberLuaJIT converts a string to a number as LuaJIT 2.1 does
// (lj_strscan_num, which its tonumber uses): white space around, a sign,
// "inf", "infinity" and "nan" in any case, or a decimal, "0x" hexadecimal
// or "0b" binary number with an optional fraction and exponent ("e", or "p"
// for hexadecimal), correctly rounded. A NUL byte anywhere makes it no
// number.
func ScanNumberLuaJIT(s string) (float64, bool) {
	v, f := strscan(s, 0)
	return v, f == fmtNum
}

// scanNumeral checks a numeral as lex_number does with the FFI's options: it
// returns the value of a number, or reports that the numeral is cdata (a
// 64-bit integer or an imaginary number), or that it is malformed.
func scanNumeral(s string) (v float64, cdata, ok bool) {
	v, f := strscan(s, optLL|optImag)
	switch f {
	case fmtNum:
		return v, false, true
	case fmtImag, fmtI64, fmtU64:
		return 0, true, true
	}
	return 0, false, false
}

// strscan is lj_strscan_scan with STRSCAN_OPT_TONUM and the options opt. It
// returns the format and, for fmtNum and fmtImag, the value (the imaginary
// part).
func strscan(s string, opt scanOpt) (float64, numFormat) {
	// at is the byte at i, 0 past the end, as in LuaJIT's NUL-terminated
	// strings
	at := func(i int) int {
		if i < len(s) {
			return int(s[i])
		}
		return 0
	}
	lower := func(i int) int { return at(i) | 0x20 }
	p, neg := 0, false

	// white space, the sign, "inf", "infinity", "nan"
	if !isDigit(at(p)) {
		for isSpace(at(p)) {
			p++
		}
		if c := at(p); c == '+' || c == '-' {
			neg = c == '-'
			p++
		}
		if at(p) >= 'A' {
			v := ljNaN
			if lower(p) == 'i' && lower(p+1) == 'n' && lower(p+2) == 'f' {
				v = math.Inf(1)
				if neg {
					v = math.Inf(-1)
				}
				p += 3
				if lower(p) == 'i' && lower(p+1) == 'n' && lower(p+2) == 'i' &&
					lower(p+3) == 't' && lower(p+4) == 'y' {
					p += 5
				}
			} else if lower(p) == 'n' && lower(p+1) == 'a' && lower(p+2) == 'n' {
				p += 3
			}
			for isSpace(at(p)) {
				p++
			}
			if p < len(s) {
				return 0, fmtError
			}
			return v, fmtNum
		}
	}

	// the base and leading zeros
	f, base, digit := fmtInt, 10, isDigit
	dp, hasdig := -1, false
	if at(p) <= '0' {
		if at(p) == '0' {
			switch lower(p + 1) {
			case 'x':
				base, digit = 16, isXDigit
				p += 2
			case 'b':
				base = 2 // the digits are decimal ones, checked below
				p += 2
			}
		}
		for ; ; p++ {
			if at(p) == '0' {
				hasdig = true
			} else if at(p) == '.' {
				if dp >= 0 {
					return 0, fmtError
				}
				dp = p
			} else {
				break
			}
		}
	}

	// the significant digits and the decimal point
	sp, dig := p, 0
	for ; ; p++ {
		if digit(at(p)) {
			dig++
		} else if at(p) == '.' {
			if dp >= 0 {
				return 0, fmtError
			}
			dp = p
		} else {
			break
		}
	}
	if !hasdig && dig == 0 {
		return 0, fmtError
	}
	ex := 0 // the exponent of the significant digits as an integer
	if dp >= 0 {
		if base == 2 {
			return 0, fmtError
		}
		f = fmtNum
		if dig > 0 {
			ex = dp - (p - 1)
			for q := p - 1; ex < 0 && s[q] == '0'; q-- { // trailing zeros
				ex++
				dig--
			}
			if ex <= -maxExp {
				return 0, fmtError
			}
			if base == 16 {
				ex *= 4
			}
		}
	}

	// the exponent
	if base >= 10 && (base == 16 && lower(p) == 'p' || base == 10 && lower(p) == 'e') {
		f = fmtNum
		p++
		negx := false
		if c := at(p); c == '+' || c == '-' {
			negx = c == '-'
			p++
		}
		if !isDigit(at(p)) {
			return 0, fmtError
		}
		xx := at(p) & 15
		for p++; isDigit(at(p)); p++ {
			xx = xx*10 + at(p)&15
			if xx >= maxExp {
				return 0, fmtError
			}
		}
		if negx {
			xx = -xx
		}
		ex += xx
	}

	// the suffix: I (imaginary), LL (int64), ULL or LLU (uint64); U (uint32)
	// and L (long) are C syntax only. Then white space.
	if at(p) != 0 {
		if lower(p) == 'i' {
			if opt&optImag == 0 {
				return 0, fmtError
			}
			p++
			f = fmtImag
		} else if f == fmtInt {
			if lower(p) == 'u' {
				p++
				f = fmtU32
			}
			if lower(p) == 'l' {
				p++
				if lower(p) != 'l' {
					return 0, fmtError
				}
				p++
				f += fmtI64 - fmtInt
			}
			if lower(p) == 'u' && (f == fmtInt || f == fmtI64) {
				p++
				f += fmtU32 - fmtInt
			}
			if f == fmtU32 || f >= fmtI64 && opt&optLL == 0 {
				return 0, fmtError
			}
		}
		for isSpace(at(p)) {
			p++
		}
		if at(p) != 0 {
			return 0, fmtError
		}
	}
	if p < len(s) { // a NUL byte
		return 0, fmtError
	}

	// the base-specific conversion (strscan_hex, strscan_bin, strscan_dec)
	if f == fmtInt {
		f = fmtNum
	}
	digits := func() string { // the significant digits, without the point
		d := strings.Replace(s[sp:], ".", "", 1)
		return d[:dig]
	}
	switch base {
	case 2:
		if dig > 64 {
			return 0, fmtError
		}
		var x uint64
		for _, c := range []byte(s[sp : sp+dig]) {
			if c&^1 != '0' {
				return 0, fmtError
			}
			x = x<<1 | uint64(c&1)
		}
		if f == fmtI64 || f == fmtU64 {
			return 0, f
		}
		return signed(float64(x), neg), f
	case 16:
		if f == fmtI64 || f == fmtU64 {
			if dig > 16 {
				return 0, fmtError
			}
			return 0, f
		}
		if dig == 0 {
			return signed(0, neg), f
		}
		// "0x.<digits>p<exponent>": the exponent stays small for a value in
		// range (strconv stops reading exponents at 10000)
		return signed(parseFloat("0x."+digits()+"p"+strconv.Itoa(ex+4*dig)), neg), f
	default:
		if f == fmtI64 || f == fmtU64 {
			if !fitsUint64(digits()) {
				return 0, fmtError
			}
			return 0, f
		}
		if dig == 0 {
			return signed(0, neg), f
		}
		return signed(parseFloat("0."+digits()+"e"+strconv.Itoa(ex+dig)), neg), f
	}
}

// parseFloat is strconv.ParseFloat of a number strscan made, ±Inf or 0 out
// of range. It rounds correctly as LuaJIT does: LuaJIT takes the first 800
// significant decimal digits and whether any other is not 0, enough to.
func parseFloat(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if ne, ok := err.(*strconv.NumError); ok && ne.Err != strconv.ErrRange {
		panic("parse: number " + strconv.Quote(s) + ": " + err.Error())
	}
	return v
}

func signed(v float64, neg bool) float64 {
	if neg {
		return -v
	}
	return v
}

// fitsUint64 reports whether the decimal integer d is below 2^64, as
// strscan_dec requires of an LL, ULL or LLU numeral (at most 20 digits,
// without overflow).
func fitsUint64(d string) bool {
	if d == "" { // only zeros
		return true
	}
	if len(d) > 20 {
		return false
	}
	_, err := strconv.ParseUint(d, 10, 64)
	return err == nil
}
