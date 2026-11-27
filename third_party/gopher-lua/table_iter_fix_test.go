package lua

// go-moonscript patch: tests of LTable.All and LTable.Array (table.go).

import (
	"fmt"
	"math/rand"
	"testing"
)

type tiPair struct{ k, v LValue }

// tiNext lists the pairs of tb with a loop of Next calls, calling body (if
// not nil) after each pair.
func tiNext(tb *LTable, body func(k LValue)) []tiPair {
	var out []tiPair
	for k, v := tb.Next(LNil); k != LNil; k, v = tb.Next(k) {
		out = append(out, tiPair{k, v})
		if body != nil {
			body(k)
		}
	}
	return out
}

// tiAll lists the pairs of tb with All, calling body (if not nil) after
// each pair.
func tiAll(tb *LTable, body func(k LValue)) []tiPair {
	var out []tiPair
	for k, v := range tb.All() {
		out = append(out, tiPair{k, v})
		if body != nil {
			body(k)
		}
	}
	return out
}

func tiSame(t *testing.T, name string, want, got []tiPair) {
	t.Helper()
	if len(want) != len(got) {
		t.Errorf("%s: %d pairs with Next, %d with All", name, len(want), len(got))
	}
	for i := 0; i < len(want) && i < len(got); i++ {
		if want[i] != got[i] {
			t.Errorf("%s: pair %d: %v=%v with Next, %v=%v with All", name, i+1, want[i].k, want[i].v, got[i].k, got[i].v)
			return
		}
	}
}

// tiTables returns tables of varied shapes, each built twice the same way
// (the same keys in the same order, so the same layout: pointer keys are
// the same objects in both).
func tiTables(t *testing.T) []struct {
	name string
	a, b *LTable
} {
	t.Helper()
	ptrs := make([]LValue, 64)
	for i := range ptrs {
		switch i % 3 {
		case 0:
			ptrs[i] = newLTable(0, 0)
		case 1:
			ptrs[i] = &LFunction{}
		default:
			ptrs[i] = &LUserData{}
		}
	}
	type op struct {
		k, v LValue
	}
	var cases []struct {
		name string
		ops  []op
	}
	add := func(name string, ops []op) {
		cases = append(cases, struct {
			name string
			ops  []op
		}{name, ops})
	}
	seq := func(from, to int, val func(i int) LValue) []op {
		var ops []op
		for i := from; i <= to; i++ {
			ops = append(ops, op{LNumber(i), val(i)})
		}
		return ops
	}
	num := func(i int) LValue { return LNumber(i * 10) }
	str := func(i int) LValue { return LString(fmt.Sprint("k", i)) }
	add("empty", nil)
	add("array", seq(1, 100, num))
	var hash []op
	for i := 0; i < 100; i++ {
		hash = append(hash, op{str(i), LNumber(i)})
	}
	add("hash", hash)
	mixed := append(seq(1, 50, num), hash[:50]...)
	mixed = append(mixed, op{LTrue, LString("t")}, op{LFalse, LString("f")},
		op{LNumber(0.5), LNumber(1)}, op{LNumber(-3), LNumber(2)}, op{LNumber(1e9), LNumber(3)})
	add("mixed", mixed)
	holes := seq(1, 40, num)
	for i := 2; i <= 40; i += 3 {
		holes = append(holes, op{LNumber(i), LNil})
	}
	holes = append(holes, op{LNumber(100), LNumber(1)}, op{LNumber(1000), LNumber(2)})
	add("holes", holes)
	deleted := append(append([]op{}, mixed...), hash[10:60]...)
	for i := 0; i < len(mixed); i += 2 {
		deleted = append(deleted, op{mixed[i].k, LNil})
	}
	add("after deletions", deleted)
	// many keys added and removed: several rehashes, the last ones to a
	// smaller hash part
	var rehashed []op
	for i := 0; i < 300; i++ {
		rehashed = append(rehashed, op{str(i), LNumber(i)}, op{LNumber(i*7 + 1), LNumber(i)})
	}
	for i := 0; i < 290; i++ {
		rehashed = append(rehashed, op{str(i), LNil})
	}
	for i := 300; i < 340; i++ {
		rehashed = append(rehashed, op{str(i), LNumber(i)})
	}
	add("after rehash", rehashed)
	var withPtrs []op
	for i, p := range ptrs {
		withPtrs = append(withPtrs, op{p, LNumber(i)}, op{str(i), LNumber(i)})
	}
	add("pointer keys (insertion order)", withPtrs)
	ptrDeleted := append([]op{}, withPtrs...)
	for i := 0; i < len(ptrs); i += 3 {
		ptrDeleted = append(ptrDeleted, op{ptrs[i], LNil})
	}
	ptrDeleted = append(ptrDeleted, op{ptrs[0], LTrue}) // revived dead key
	ptrDeleted = append(ptrDeleted, seq(1, 20, num)...)
	add("pointer keys after deletions", ptrDeleted)
	r := rand.New(rand.NewSource(1))
	for n := 0; n < 20; n++ {
		var ops []op
		for i := 0; i < 400; i++ {
			var k LValue
			switch r.Intn(4) {
			case 0:
				k = LNumber(r.Intn(64) + 1)
			case 1:
				k = str(r.Intn(100))
			case 2:
				k = ptrs[r.Intn(len(ptrs))]
				if n%2 == 0 {
					k = LNumber(r.Intn(1000) - 100)
				}
			default:
				k = LNumber(float64(r.Intn(100)) / 4)
			}
			var v LValue = LNumber(i)
			if r.Intn(3) == 0 {
				v = LNil
			}
			ops = append(ops, op{k, v})
		}
		add(fmt.Sprint("random ", n), ops)
	}
	out := make([]struct {
		name string
		a, b *LTable
	}, 0, len(cases))
	for _, c := range cases {
		a, b := newLTable(0, 0), newLTable(0, 0)
		for _, o := range c.ops {
			a.RawSet(o.k, o.v)
			b.RawSet(o.k, o.v)
		}
		out = append(out, struct {
			name string
			a, b *LTable
		}{c.name, a, b})
	}
	// constructors size the parts
	L := NewState()
	defer L.Close()
	for i, src := range []string{
		`return {1, 2, 3, nil, 5, x = 1, y = 2, [10] = 10}`,
		`local t = {} for i = 1, 100 do t[i] = i end for i = 1, 100, 2 do t[i] = nil end return t`,
	} {
		var tabs [2]*LTable
		for j := range tabs {
			if err := L.DoString(src); err != nil {
				t.Fatal(err)
			}
			tabs[j] = L.Get(-1).(*LTable)
			L.Pop(1)
		}
		out = append(out, struct {
			name string
			a, b *LTable
		}{fmt.Sprint("constructor ", i), tabs[0], tabs[1]})
	}
	return out
}

func TestTableAllOrder(t *testing.T) {
	for _, c := range tiTables(t) {
		want := tiNext(c.a, nil)
		tiSame(t, c.name, want, tiAll(c.a, nil))
		// ForEach visits the same pairs in the same order too
		var fe []tiPair
		c.a.ForEach(func(k, v LValue) { fe = append(fe, tiPair{k, v}) })
		tiSame(t, c.name+" (ForEach)", want, fe)
	}
}

func TestTableAllBreak(t *testing.T) {
	for _, c := range tiTables(t) {
		want := tiNext(c.a, nil)
		for stop := 0; stop <= len(want); stop++ {
			var got []tiPair
			for k, v := range c.a.All() {
				if len(got) == stop {
					break
				}
				got = append(got, tiPair{k, v})
			}
			tiSame(t, fmt.Sprint(c.name, " break after ", stop), want[:stop], got)
		}
	}
}

// Assigning to existing fields during the traversal, nil included, gives
// the same traversal as next does under the same assignments.
func TestTableAllAssignExisting(t *testing.T) {
	policies := []struct {
		name string
		body func(tb *LTable, keys []LValue, i int, k LValue)
	}{
		{"clear the current key", func(tb *LTable, _ []LValue, _ int, k LValue) {
			tb.RawSet(k, LNil)
		}},
		{"change the current value", func(tb *LTable, _ []LValue, i int, k LValue) {
			tb.RawSet(k, LNumber(-i))
		}},
		{"clear the keys ahead", func(tb *LTable, keys []LValue, i int, _ LValue) {
			for j := i + 1; j < len(keys); j += 2 {
				tb.RawSet(keys[j], LNil)
			}
		}},
		{"clear the keys behind and change the ones ahead", func(tb *LTable, keys []LValue, i int, _ LValue) {
			if i > 0 {
				tb.RawSet(keys[i-1], LNil)
			}
			if i+1 < len(keys) {
				tb.RawSet(keys[i+1], LString("changed"))
			}
		}},
		{"clear everything", func(tb *LTable, keys []LValue, _ int, _ LValue) {
			for _, k := range keys {
				tb.RawSet(k, LNil)
			}
		}},
	}
	for _, p := range policies {
		for _, c := range tiTables(t) {
			keys := make([]LValue, 0)
			for _, pr := range tiNext(c.a, nil) {
				keys = append(keys, pr.k)
			}
			n := 0
			want := tiNext(c.a, func(k LValue) { p.body(c.a, keys, n, k); n++ })
			n = 0
			got := tiAll(c.b, func(k LValue) { p.body(c.b, keys, n, k); n++ })
			tiSame(t, c.name+": "+p.name, want, got)
		}
	}
}

// Adding keys during the traversal is undefined, but All neither panics
// nor runs forever, and yields only pairs with a value.
func TestTableAllAddKeys(t *testing.T) {
	policies := []struct {
		name string
		body func(tb *LTable, i int, k LValue)
	}{
		{"a number key per pair", func(tb *LTable, i int, _ LValue) {
			tb.RawSetInt(100000+i, LTrue)
		}},
		{"a string key per pair", func(tb *LTable, i int, _ LValue) {
			tb.RawSetString(fmt.Sprint("new", i), LTrue)
		}},
		{"a pointer key per pair", func(tb *LTable, _ int, _ LValue) {
			tb.RawSet(newLTable(0, 0), LTrue)
		}},
		{"clear the current key, add a pointer key", func(tb *LTable, _ int, k LValue) {
			tb.RawSet(k, LNil)
			tb.RawSet(newLTable(0, 0), LTrue)
		}},
		{"clear the current key, add a string key", func(tb *LTable, i int, k LValue) {
			tb.RawSet(k, LNil)
			tb.RawSetString(fmt.Sprint("new", i), LTrue)
		}},
		{"add and remove a key", func(tb *LTable, i int, _ LValue) {
			k := LString(fmt.Sprint("tmp", i))
			tb.RawSet(k, LTrue)
			tb.RawSet(k, LNil)
		}},
		{"grow the array part", func(tb *LTable, i int, _ LValue) {
			tb.RawSetInt(tb.Len()+1, LNumber(i))
		}},
		{"1000 keys at the first pair", func(tb *LTable, i int, _ LValue) {
			if i == 0 {
				for j := 0; j < 1000; j++ {
					tb.RawSetString(fmt.Sprint("bulk", j), LTrue)
					tb.RawSet(newLTable(0, 0), LTrue)
				}
			}
		}},
		{"clear all, then add keys", func(tb *LTable, i int, _ LValue) {
			if i == 0 {
				var keys []LValue
				for _, p := range tiNext(tb, nil) {
					keys = append(keys, p.k)
				}
				for _, k := range keys {
					tb.RawSet(k, LNil)
				}
			}
			tb.RawSetString(fmt.Sprint("new", i), LTrue)
			tb.RawSetInt(i+1, LTrue)
		}},
	}
	const limit = 1000000
	for _, p := range policies {
		for _, c := range tiTables(t) {
			size := len(tiNext(c.a, nil))
			n := 0
			for k, v := range c.a.All() {
				if v == LNil || v == nil || k == nil || k == LNil {
					t.Fatalf("%s: %s: pair %v=%v", c.name, p.name, k, v)
				}
				p.body(c.a, n, k)
				n++
				if n > limit {
					t.Fatalf("%s: %s: more than %d pairs from a table of %d", c.name, p.name, limit, size)
				}
			}
		}
	}
}

// Weak entries collected during the traversal are not visited.
func TestTableAllWeak(t *testing.T) {
	L := NewState()
	defer L.Close()
	if err := L.DoString(`
		weak = setmetatable({}, {__mode = "v"})
		for i = 1, 10 do weak[i] = {} weak["k" .. i] = {} end
		keep = {weak[1], weak.k1}
	`); err != nil {
		t.Fatal(err)
	}
	tb := L.GetGlobal("weak").(*LTable)
	n := 0
	for k, v := range tb.All() {
		if n == 0 {
			if err := L.DoString(`collectgarbage()`); err != nil {
				t.Fatal(err)
			}
		}
		n++
		if v == LNil {
			t.Fatalf("%v: nil value", k)
		}
	}
	if n > 3 { // the first pair, and the two kept
		t.Errorf("%d pairs after a collection", n)
	}
}

func TestTableArray(t *testing.T) {
	L := NewState()
	defer L.Close()
	for _, c := range []struct {
		src  string
		want string
	}{
		{`return {}`, ""},
		{`return {1, 2, 3}`, "1=1 2=2 3=3 "},
		{`return {1, 2, nil, 4}`, "1=1 2=2 "},
		{`return {nil, 2}`, ""},
		{`local t = {} t[3] = 3 t[2] = 2 t[1] = 1 t[5] = 5 return t`, "1=1 2=2 3=3 "},        // the hash part
		{`local t = {1, 2} t[3] = 3 t[4] = 4 t.x = 1 t[0] = 0 return t`, "1=1 2=2 3=3 4=4 "}, // both parts
		{`return setmetatable({1}, {__index = function(t, i) return i end})`, "1=1 "},        // raw
	} {
		if err := L.DoString(c.src); err != nil {
			t.Fatal(err)
		}
		tb := L.Get(-1).(*LTable)
		L.Pop(1)
		got := ""
		for i, v := range tb.Array() {
			got += fmt.Sprintf("%d=%v ", i, v)
		}
		tfEqual(t, c.want, got)
		// ipairs agrees
		L.SetGlobal("t", tb)
		if err := L.DoString(`local s = "" for i, v in ipairs(t) do s = s .. i .. "=" .. tostring(v) .. " " end return s`); err != nil {
			t.Fatal(err)
		}
		tfEqual(t, c.want, L.Get(-1).String())
		L.Pop(1)
	}

	// break, and changes as ipairs sees them
	tb := newLTable(0, 0)
	for i := 1; i <= 10; i++ {
		tb.RawSetInt(i, LNumber(i))
	}
	n := 0
	for i := range tb.Array() {
		if i == 3 {
			break
		}
		n++
	}
	tfEqual(t, 2, n)
	n = 0
	for i, v := range tb.Array() {
		n++
		if i == 2 {
			tb.RawSetInt(3, LString("x")) // seen
		}
		if i == 3 {
			tfEqual(t, LString("x"), v)
		}
		if i <= 5 {
			tb.RawSetInt(10+i, LTrue) // appended: seen
		}
		if i == 12 {
			tb.RawSetInt(14, LNil) // ends it
		}
	}
	tfEqual(t, 13, n)
}

func benchTable() *LTable {
	tb := newLTable(0, 0)
	for i := 1; i <= 1000; i++ {
		tb.RawSetInt(i, LNumber(i))
		tb.RawSetString(fmt.Sprint("key", i), LNumber(i))
	}
	return tb
}

func BenchmarkTableAll(b *testing.B) {
	tb := benchTable()
	b.ResetTimer()
	for b.Loop() {
		n := 0
		for _, v := range tb.All() {
			n += int(v.(LNumber))
		}
	}
}

func BenchmarkTableForEach(b *testing.B) {
	tb := benchTable()
	b.ResetTimer()
	for b.Loop() {
		n := 0
		tb.ForEach(func(_, v LValue) {
			n += int(v.(LNumber))
		})
	}
}

func BenchmarkTableNext(b *testing.B) {
	tb := benchTable()
	b.ResetTimer()
	for b.Loop() {
		n := 0
		for k, v := tb.Next(LNil); k != LNil; k, v = tb.Next(k) {
			n += int(v.(LNumber))
		}
	}
}

func BenchmarkTableArray(b *testing.B) {
	tb := benchTable()
	b.ResetTimer()
	for b.Loop() {
		n := 0
		for _, v := range tb.Array() {
			n += int(v.(LNumber))
		}
	}
}
