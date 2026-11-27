package lua

// go-moonscript patch: the registers the compiler counts for a function,
// as luac counts them.

import (
	"strings"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/parse"
)

func compileString(t *testing.T, src string) *FunctionProto {
	t.Helper()
	chunk, err := parse.Parse(strings.NewReader(src), "x")
	if err != nil {
		t.Fatal(err)
	}
	proto, err := Compile(chunk, "x")
	if err != nil {
		t.Fatal(err)
	}
	return proto
}

// TestCompileClosureRegisters: a closure is in the register its CLOSURE
// sets, which counts in the function's registers like any other: f's
// arguments are closures in registers 11-20, which no other instruction
// sets (the frame had 11 registers, the arguments were written past it).
func TestCompileClosureRegisters(t *testing.T) {
	src := "local a, b, c, d, e, f, g, h, i, j\nf(" +
		strings.TrimSuffix(strings.Repeat("function() end, ", 10), ", ") + ")"
	if n := compileString(t, src).NumUsedRegisters; n < 21 {
		t.Errorf("NumUsedRegisters = %d, want at least 21", n)
	}
}

// TestCompileAssignConflictRegisters: the targets of a multiple assignment
// indexing a local that a later target assigns share one copy of it, as
// luac's check_conflict makes: lua5.1 compiles this with 190 locals.
func TestCompileAssignConflictRegisters(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 190; i++ {
		b.WriteString("local v" + string(rune('a'+i%26)) + strings.Repeat("x", i/26) + " = 1\n")
	}
	b.WriteString("local t, k = {}, 1\n")
	b.WriteString(strings.Repeat("t[k], ", 50) + "t, k = " + strings.TrimSuffix(strings.Repeat("1, ", 52), ", "))
	proto := compileString(t, b.String())
	if n := proto.NumUsedRegisters; n > 192+52+2 {
		t.Errorf("NumUsedRegisters = %d, want at most %d", n, 192+52+2)
	}
	L := NewState()
	defer L.Close()
	if err := L.DoString("local t, k = {}, 1 t[k], t[k], t, k = 'a', 'b', 'c', 'd' assert(type(t) == 'string' and k == 'd')"); err != nil {
		t.Error(err)
	}
}
