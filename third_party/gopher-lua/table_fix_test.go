package lua

// go-moonscript patch: tests of the LTable rewrite (table.go,
// table_hash.go; see moonscript.patch). The Lua side, against Lua 5.1, is
// in testdata/compat/table_*.lua (table_fuzz.lua is a differential fuzz
// test).

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"runtime"
	"strings"
	"testing"
)

func tfEqual(t *testing.T, want, got interface{}) {
	t.Helper()
	if want != got {
		t.Errorf("%v expected, but got %v", want, got)
	}
}

// Integer keys far past the array part go to the hash part instead of
// padding the array with nils (yuin/gopher-lua#251, #304, #501).
func TestTableFixBigIntKey(t *testing.T) {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	tbl := newLTable(0, 0)
	tbl.RawSetInt(1000000000, LTrue)
	tbl.RawSet(LNumber(60000000), LTrue)
	tbl.RawSetInt(10, LTrue)
	runtime.ReadMemStats(&after)
	if n := after.TotalAlloc - before.TotalAlloc; n > 1<<20 {
		t.Errorf("3 sparse keys allocated %d bytes", n)
	}
	tfEqual(t, 0, len(tbl.array))
	tfEqual(t, LTrue, tbl.RawGetInt(1000000000))
	tfEqual(t, LTrue, tbl.RawGet(LNumber(60000000)))
	tfEqual(t, LTrue, tbl.RawGetInt(10))
	tfEqual(t, 0, tbl.Len())
	tfEqual(t, 60000000, tbl.MaxN())

	L := NewState()
	defer L.Close()
	runtime.ReadMemStats(&before)
	if err := L.DoString(`t = {} t[1e9] = 1 t[3e7] = 2 n = #t`); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	if n := after.TotalAlloc - before.TotalAlloc; n > 1<<20 {
		t.Errorf("t[1e9] = 1 allocated %d bytes", n)
	}
	tb := L.GetGlobal("t").(*LTable)
	tfEqual(t, 0, len(tb.array))
	tfEqual(t, LNumber(0), L.GetGlobal("n"))
}

// RawGetInt finds the keys out of the array part (it did not find t[0]).
func TestTableFixRawGetIntHash(t *testing.T) {
	tbl := newLTable(0, 0)
	keys := []int{0, -1, 5, MaxArrayIndex, MaxArrayIndex + 1}
	for _, k := range keys {
		tbl.RawSetInt(k, LNumber(k))
	}
	for _, k := range keys {
		tfEqual(t, LNumber(k), tbl.RawGetInt(k))
		tfEqual(t, LNumber(k), tbl.RawGet(LNumber(k)))
		tfEqual(t, LNumber(k), tbl.RawGetH(LNumber(k)))
	}
	tfEqual(t, LNil, tbl.RawGetInt(4))
}

// Len is Lua 5.1's # (luaH_getn) whether the table is built by Go or by Lua.
func TestTableFixLen(t *testing.T) {
	L := NewState()
	defer L.Close()
	for _, c := range []struct {
		lua string
		set func(*LTable)
	}{
		{`t[10] = nil t[9] = 10 t[8] = nil t[7] = 10`, func(tb *LTable) {
			tb.RawSetInt(10, LNil)
			tb.RawSetInt(9, LNumber(10))
			tb.RawSetInt(8, LNil)
			tb.RawSetInt(7, LNumber(10))
		}},
		{`t[1] = 1 t[2] = 2 t[4] = 4`, func(tb *LTable) {
			tb.RawSetInt(1, LNumber(1))
			tb.RawSetInt(2, LNumber(2))
			tb.RawSetInt(4, LNumber(4))
		}},
		{`for i = 1, 3 do t[i] = i end t[1] = nil t[2] = nil table.insert(t, 4)`, func(tb *LTable) {
			for i := 1; i <= 3; i++ {
				tb.RawSetInt(i, LNumber(i))
			}
			tb.RawSetInt(1, LNil)
			tb.RawSetInt(2, LNil)
			tb.Append(LNumber(4))
		}},
		{`for i = 1, 3 do t[i] = true end table.insert(t, 5, false)`, func(tb *LTable) {
			for i := 0; i < 3; i++ {
				tb.Append(LTrue)
			}
			tb.Insert(5, LFalse)
		}},
		{`for i = 1, 20 do t[i] = i end t.x = 1 t[21] = 21 t[23] = 23 t[20] = nil`, func(tb *LTable) {
			for i := 1; i <= 20; i++ {
				tb.RawSetInt(i, LNumber(i))
			}
			tb.RawSetString("x", LNumber(1))
			tb.RawSetInt(21, LNumber(21))
			tb.RawSet(LNumber(23), LNumber(23))
			tb.RawSetH(LNumber(20), LNil)
		}},
	} {
		if err := L.DoString(`t = {} ` + c.lua + ` n = #t`); err != nil {
			t.Fatal(err)
		}
		tb := newLTable(0, 0)
		c.set(tb)
		if n := L.GetGlobal("n"); LNumber(tb.Len()) != n {
			t.Errorf("%s: Len() is %d, #t %v", c.lua, tb.Len(), n)
		}
	}
}

// A queue (t[last+1] = v ... t[first] = nil) does not grow forever, nor do
// keys added and removed (yuin/gopher-lua#214).
func TestTableFixQueue(t *testing.T) {
	tbl := newLTable(0, 0)
	first, last := 1, 0
	for i := 0; i < 1000000; i++ {
		last++
		tbl.RawSetInt(last, LNumber(last))
		if last-first >= 10 {
			tfEqual(t, LNumber(first), tbl.RawGetInt(first))
			tbl.RawSetInt(first, LNil)
			first++
		}
	}
	if len(tbl.array) > 1000 || len(tbl.hash.nodes) > 1000 {
		t.Fatalf("queue of 10 elements: array part of %d, hash part of %d", len(tbl.array), len(tbl.hash.nodes))
	}
	for i := first; i <= last; i++ {
		tfEqual(t, LNumber(i), tbl.RawGetInt(i))
	}
	tfEqual(t, LNil, tbl.RawGetInt(first-1))
}

func TestTableFixKeysLeak(t *testing.T) {
	tbl := newLTable(0, 0)
	for i := 0; i < 100000; i++ {
		k := "k" + LNumber(i).String()
		tbl.RawSetString(k, LTrue)
		tbl.RawSetString(k, LNil)
		tbl.RawSetH(LNumber(-i), LTrue)
		tbl.RawSetH(LNumber(-i), LNil)
		p := &LTable{}
		tbl.RawSetH(p, LTrue)
		tbl.RawSetH(p, LNil)
		tbl.RawSet(LNumber(i)+0.5, LNil)
	}
	if len(tbl.hash.nodes) > 1000 {
		t.Fatalf("%d nodes for 100000 keys", len(tbl.hash.nodes))
	}
}

// A traversal can clear or change the fields, Next with a key that is not
// in the table ends the traversal, next raises an error.
func TestTableFixNext(t *testing.T) {
	for _, ptr := range []bool{false, true} {
		tbl := newLTable(0, 0)
		for i := 0; i < 1000; i++ {
			tbl.RawSetH(LNumber(i)+0.5, LNumber(i))
			tbl.RawSetInt(i+1, LNumber(i))
		}
		if ptr {
			tbl.RawSet(&LTable{}, LTrue)
		}
		n := 0
		for k, _ := tbl.Next(LNil); k != LNil; k, _ = tbl.Next(k) {
			if n%2 == 0 {
				tbl.RawSet(k, LNil)
			} else {
				tbl.RawSet(k, LFalse)
			}
			n++
		}
		tfEqual(t, 2001-btoi(!ptr), n)
		n = 0
		tbl.ForEach(func(k, v LValue) {
			tfEqual(t, LFalse, v)
			n++
		})
		tfEqual(t, 1000, n)
	}

	tbl := newLTable(0, 0)
	tbl.RawSetString("a", LTrue)
	tbl.RawSetString("b", LTrue)
	k, _ := tbl.Next(LString("zz"))
	tfEqual(t, LNil, k)
	k, _ = tbl.Next(LNumber(7))
	tfEqual(t, LNil, k)

	L := NewState()
	defer L.Close()
	err := L.DoString(`next({a = 1}, "zz")`)
	if err == nil || !strings.Contains(err.Error(), "invalid key to 'next'") {
		t.Errorf("next with an invalid key: %v", err)
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// A nil or NaN key is ignored by the Go API, as there is no Lua error to
// raise (yuin/gopher-lua#319: it went into the table); rawset and t[k] = v
// raise errors (testdata/compat/table_nilkey.lua).
func TestTableFixNilKey(t *testing.T) {
	nan := LNumber(math.NaN())
	for _, key := range []LValue{LNil, nil, nan} {
		tbl := newLTable(0, 0)
		tbl.RawSet(key, LTrue)
		tbl.RawSetH(key, LTrue)
		tbl.RawSet(key, LNil)
		if k, _ := tbl.Next(LNil); k != LNil || len(tbl.hash.nodes) != 0 {
			t.Errorf("RawSet(%v, true) set a key", key)
		}
		tfEqual(t, LNil, tbl.RawGet(key))
	}
	tbl := newLTable(0, 0)
	tbl.RawSetInt(1, LTrue)
	tbl.RawSet(nan, LTrue)
	tfEqual(t, 1, tbl.Len())
}

// The iteration order depends on the operations only, not on addresses.
func TestTableFixOrder(t *testing.T) {
	order := func(keys []LValue) string {
		tbl := newLTable(0, 0)
		for i, k := range keys {
			tbl.RawSet(k, LNumber(i))
			if i%3 == 0 {
				tbl.RawSet(keys[i/2], LNil)
			}
		}
		var b strings.Builder
		for k, v := tbl.Next(LNil); k != LNil; k, v = tbl.Next(k) {
			if _, ok := k.(*LTable); ok {
				k = LString("table")
			}
			fmt.Fprintf(&b, "%v=%v ", k, v)
		}
		return b.String()
	}
	mk := func() []LValue {
		var keys []LValue
		for i := 0; i < 200; i++ {
			switch i % 4 {
			case 0:
				keys = append(keys, &LTable{})
			case 1:
				keys = append(keys, LString(fmt.Sprint("k", i)))
			case 2:
				keys = append(keys, LNumber(i*i))
			default:
				keys = append(keys, LNumber(i)+0.5)
			}
		}
		return keys
	}
	want := order(mk())
	for i := 0; i < 5; i++ {
		if got := order(mk()); got != want {
			t.Fatalf("order differs:\n%s\n%s", got, want)
		}
	}
}

// TestTableFixFuzz runs testdata/compat/table_fuzz.lua for the sequences
// that FUZZ_SEED, FUZZ_N and FUZZ_OPS select and compares its output with
// the file FUZZ_REF, Lua 5.1's (scripts/fuzz-tables.sh).
func TestTableFixFuzz(t *testing.T) {
	ref := os.Getenv("FUZZ_REF")
	if ref == "" {
		t.Skip("FUZZ_REF is not set (scripts/fuzz-tables.sh)")
	}
	want, err := os.ReadFile(ref)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.SplitAfter(runCompatScript(t, "testdata/compat/table_fuzz.lua"), "\n")
	wantLines := strings.SplitAfter(string(want), "\n")
	bad := 0
	for i := range max(len(got), len(wantLines)) {
		g, w := "", ""
		if i < len(got) {
			g = got[i]
		}
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if g != w {
			if bad++; bad <= 3 {
				t.Errorf("line %d differs:\n--- got\n%s--- want\n%s", i+1, g, w)
			}
		}
	}
	if bad > 0 {
		t.Errorf("%d of %d lines differ", bad, len(wantLines))
	}
}

// A randomized test of the Go API against a Go map.
func TestTableFixModel(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	ptrs := make([]LValue, 8)
	for i := range ptrs {
		ptrs[i] = &LTable{}
	}
	key := func() LValue {
		switch r.Intn(10) {
		case 0:
			return LString(fmt.Sprint("s", r.Intn(20)))
		case 1:
			return LNumber(r.Intn(5)) - 2.5
		case 2:
			return ptrs[r.Intn(len(ptrs))]
		case 3:
			return LBool(r.Intn(2) == 0)
		case 4:
			return LNumber(int(1) << r.Intn(30))
		}
		return LNumber(r.Intn(64) + 1)
	}
	for round := 0; round < 300; round++ {
		tbl := newLTable(r.Intn(8), r.Intn(8))
		model := map[LValue]LValue{}
		check := func(op string) {
			t.Helper()
			n := 0
			tbl.ForEach(func(k, v LValue) {
				if model[k] != v {
					t.Fatalf("round %d, %s: ForEach %v=%v, want %v", round, op, k, v, model[k])
				}
				n++
			})
			if n != len(model) {
				t.Fatalf("round %d, %s: ForEach %d keys, want %d", round, op, n, len(model))
			}
			for k, v := range model {
				if got := tbl.RawGet(k); got != v {
					t.Fatalf("round %d, %s: RawGet(%v) = %v, want %v", round, op, k, got, v)
				}
			}
			l := tbl.Len()
			if l < 0 || (l > 0 && tbl.RawGetInt(l) == LNil) || tbl.RawGetInt(l+1) != LNil {
				t.Fatalf("round %d, %s: Len() = %d is not a border", round, op, l)
			}
		}
		for op := 0; op < 200; op++ {
			v := LValue(LNumber(op))
			if r.Intn(3) == 0 {
				v = LNil
			}
			k := key()
			switch r.Intn(10) {
			case 0:
				if s, ok := k.(LString); ok {
					tbl.RawSetString(string(s), v)
				} else {
					tbl.RawSetH(k, v)
				}
			case 1:
				if n, ok := k.(LNumber); ok && n == LNumber(int(n)) {
					tbl.RawSetInt(int(n), v)
				} else {
					tbl.RawSet(k, v)
				}
			case 2:
				// a traversal that changes the current key
				seen := map[LValue]bool{}
				for k, _ := tbl.Next(LNil); k != LNil; k, _ = tbl.Next(k) {
					if seen[k] {
						t.Fatalf("round %d: Next visits %v twice", round, k)
					}
					seen[k] = true
					if r.Intn(2) == 0 {
						tbl.RawSet(k, LNil)
						delete(model, k)
					} else {
						tbl.RawSet(k, LTrue)
						model[k] = LTrue
					}
				}
				check("traversal")
				continue
			case 3:
				if v != LNil {
					pos := tbl.Len() + 1
					tbl.Append(v)
					model[LNumber(pos)] = v
				}
				check("Append")
				continue
			default:
				tbl.RawSet(k, v)
			}
			if v == LNil {
				delete(model, k)
			} else {
				model[k] = v
			}
			if op%10 == 0 {
				check("set")
			}
		}
		check("end")
	}
}
