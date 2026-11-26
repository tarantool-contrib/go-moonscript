package lint

import (
	"errors"
	"strings"
	"testing"
)

func TestCodeErrors(t *testing.T) {
	if _, ok, err := Code("x = = 1", "f.moon", nil, Options{}); ok || err == nil ||
		!strings.HasPrefix(err.Error(), "failed to parse at line 1") {
		t.Errorf("parse error: ok %v err %v", ok, err)
	}
	var crash *Crash
	if _, _, err := Code("x = 1\n.foo!\n", "", nil, Options{}); !errors.As(err, &crash) || !crash.Table ||
		crash.Msg != "Short-dot syntax must be called within a with block" {
		t.Errorf("compile error: %v", err)
	}
	if _, _, err := Code("print x", "", nil, Options{Format: "json"}); err == nil ||
		err.Error() != "unknown lint format: json" {
		t.Errorf("unknown format: %v", err)
	}
	if got, ok, err := Code("print x, y", "", func(name string) bool { return name == "x" || name == "print" },
		Options{Format: "compact"}); err != nil || !ok || got != "string input:1:9: accessing global `y` [global_access]" {
		t.Errorf("custom whitelist: %q %v %v", got, ok, err)
	}
}

// TestLuaTableOrder checks pairs order against Lua 5.1.5 (the reference
// prints names assigned together in this order).
func TestLuaTableOrder(t *testing.T) {
	cases := map[string]string{
		"a b c": "a c b",
		"alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu":                                                         "theta epsilon kappa alpha eta zeta lambda mu beta iota delta gamma",
		"one two three four five six seven eight nine ten eleven twelve thirteen fourteen fifteen sixteen seventeen":                 "fifteen ten twelve nine two one sixteen seven eight six seventeen eleven fourteen thirteen four five three",
		"x1 x2 x3 x4 x5 x6 x7 x8 x9 x10 x11 x12 x13 x14 x15 x16 x17 x18 x19 x20 x21 x22 x23 x24 x25 x26 x27 x28 x29 x30 x31 x32 x33": "x24 x5 x28 x20 x3 x1 x17 x13 x9 x33 x16 x12 x19 x32 x15 x31 x11 x6 x4 x2 x30 x10 x27 x8 x18 x26 x22 x25 x29 x7 x21 x14 x23",
	}
	for in, want := range cases {
		var tbl luaTable
		for i, k := range strings.Fields(in) {
			tbl.set(k, unusedVal{pos: i, isPos: true})
		}
		var got []string
		tbl.each(func(k string, _ unusedVal) { got = append(got, k) })
		if strings.Join(got, " ") != want {
			t.Errorf("pairs order of %q: %q, want %q", in, strings.Join(got, " "), want)
		}
	}
}
