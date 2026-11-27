package parse

// go-moonscript patch: tests of strscan.go. Its behaviour is checked
// against luajit by testdata/compat/parse_numeral_on_luajit.jit.lua and
// lib_tonumber_strscan_on_luajit.jit.lua; these test the corners directly.

import (
	"math"
	"strings"
	"testing"
)

func TestScanNumberLuaJIT(t *testing.T) {
	for _, c := range []struct {
		s  string
		v  float64
		ok bool
	}{
		{"1", 1, true},
		{" \t1\n ", 1, true},
		{"-0", math.Copysign(0, -1), true},
		{"-0x10", -16, true},
		{"0b101", 5, true},
		{"-0b101", -5, true},
		{"0x.8", 0.5, true},
		{"0x1p-1074", math.SmallestNonzeroFloat64, true},
		{"0x1.8p-1075", math.SmallestNonzeroFloat64, true},
		{"1e1048575", math.Inf(1), true},
		{"-INFINITY", math.Inf(-1), true},
		{"0." + strings.Repeat("0", 1048574) + "1", 0, true},
		{"0." + strings.Repeat("0", 1048575) + "1", 0, false},
		{strings.Repeat("1", 1<<20+1) + "e-1048570", 1111111.1111111111111111, true},
		{"0x1" + strings.Repeat("0", 1000) + "p-3990", 1024, true},
		{"0x1p1048576", 0, false},
		{"1e1048576", 0, false},
		{"nan(1)", 0, false},
		{"1\x00", 0, false},
		{"1LL", 0, false},
		{"1i", 0, false},
		{"0b2", 0, false},
		{"0b" + strings.Repeat("1", 65), 0, false},
		{"", 0, false},
	} {
		v, ok := ScanNumberLuaJIT(c.s)
		name := c.s
		if len(name) > 40 {
			name = name[:40] + "..."
		}
		if ok != c.ok || ok && math.Float64bits(v) != math.Float64bits(c.v) {
			t.Errorf("ScanNumberLuaJIT(%q) = %v, %v; want %v, %v", name, v, ok, c.v, c.ok)
		}
	}
	if v, ok := ScanNumberLuaJIT("-nan"); !ok || math.Float64bits(v) != 0xfff8000000000000 {
		t.Errorf(`ScanNumberLuaJIT("-nan") = %x, %v; want LuaJIT's NaN`, math.Float64bits(v), ok)
	}
}

func TestScanNumeral(t *testing.T) {
	for _, c := range []struct {
		s         string
		cdata, ok bool
	}{
		{"1", false, true},
		{"1LL", true, true},
		{"1ULL", true, true},
		{"1LLU", true, true},
		{"0LL", true, true},
		{"1.5i", true, true},
		{"0x1p4i", true, true},
		{"0xffffffffffffffffULL", true, true},
		{"0x10000000000000000ULL", false, false},
		{"18446744073709551615ULL", true, true},
		{"18446744073709551616ULL", false, false},
		{"1.5LL", false, false},
		{"1U", false, false},
		{"1L", false, false},
		{"1 ", false, true}, // not in a numeral, but as lj_strscan
	} {
		_, cdata, ok := scanNumeral(c.s)
		if cdata != c.cdata || ok != c.ok {
			t.Errorf("scanNumeral(%q) = cdata %v, ok %v; want %v, %v", c.s, cdata, ok, c.cdata, c.ok)
		}
	}
}

func FuzzScanNumberLuaJIT(f *testing.F) {
	for _, s := range []string{"1", "0x1.8p3", "0b11", " -inf ", "1e5LL", "0x.p", "12.5i", ".5e-3"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		ScanNumberLuaJIT(s)
		scanNumeral(s)
	})
}
