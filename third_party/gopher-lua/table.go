package lua

import (
	"errors"
	"iter"
	"math/bits"
	"unsafe"
)

// go-moonscript patch: a table has the shape of a Lua 5.1 table (ltable.c):
// an array part for the keys 1..len(array), which may hold nils, and a hash
// part (table_hash.go) for the other keys, whose size is 0 or a power of 2.
// The sizes change as in Lua 5.1: a key that is not in the table and does
// not fit in the hash part rehashes the table, which counts the integer
// keys and makes the array part the largest power of 2 that is more than
// half used (computesizes), and the hash part just big enough for the other
// keys; table constructors, SETLIST and lua_createtable (CreateTable) size
// the parts as in Lua 5.1 too, and the hash part places its keys as Lua
// 5.1 does. So # (Len, luaH_getn) gives Lua 5.1's results for tables with
// holes, and a big integer key does not allocate an array up to it (t[1e9]
// = 1 allocated a 16 GB array; yuin/gopher-lua #251, #304, #501, fixed
// differently in the dong-yue, kevincoding525, kiven-li, seasondi,
// gavin-wuz and Luzi0390 forks). Differences with Lua 5.1: its garbage
// collector makes the dead keys that are collectable objects unusable for
// next and for setting them again, and the hashes of tables, functions,
// userdata and threads are other addresses.

const defaultArrayCap = 0 // go-moonscript patch: was 32 (as lua_newtable)
const defaultHashCap = 0  // go-moonscript patch: was 32

// maxArrayBits bounds the array part (Lua 5.1's MAXBITS): the keys above
// 2^maxArrayBits (and above MaxArrayIndex) are always in the hash part.
const maxArrayBits = 30

// tableBigSlots is the size of the parts from which a table checks the
// memory limits before allocating them (tableReserve, gc.go).
const tableBigSlots = 1 << 16

func newLTable(acap int, hcap int) *LTable {
	if acap+hcap >= tableBigSlots {
		tableReserve(acap, hcap) // go-moonscript patch
	}
	// go-moonscript patch: a small array part is allocated together with
	// the table (the table first, so that a weak reference to it is to the
	// start of the object)
	var tb *LTable
	switch acap {
	case 0:
		tb = &LTable{}
	case 1:
		c := &struct {
			tb  LTable
			arr [1]LValue
		}{arr: [1]LValue{LNil}}
		tb = &c.tb
		tb.array = c.arr[:]
	case 2:
		c := &struct {
			tb  LTable
			arr [2]LValue
		}{arr: [2]LValue{LNil, LNil}}
		tb = &c.tb
		tb.array = c.arr[:]
	case 3:
		c := &struct {
			tb  LTable
			arr [3]LValue
		}{arr: [3]LValue{LNil, LNil, LNil}}
		tb = &c.tb
		tb.array = c.arr[:]
	case 4:
		c := &struct {
			tb  LTable
			arr [4]LValue
		}{arr: [4]LValue{LNil, LNil, LNil, LNil}}
		tb = &c.tb
		tb.array = c.arr[:]
	default:
		tb = &LTable{}
		if acap > 0 {
			tb.array = make([]LValue, acap)
			fillNil(tb.array)
		}
	}
	tb.Metatable = LNil
	if hcap > 0 {
		tb.hash.init(hcap)
	}
	return tb
}

// arrayIndex returns k-1 if k is an integer in 1..len(array), -1 otherwise.
func (tb *LTable) arrayIndex(k LNumber) int {
	if i := int(k); LNumber(i) == k && uint(i-1) < uint(len(tb.array)) {
		return i - 1
	}
	return -1
}

// Len returns length of this LTable without using __len.
// go-moonscript patch: it is Lua 5.1's luaH_getn, a border of the table
// (t[n] ~= nil and t[n+1] == nil, or 0 if t[1] == nil): a binary search in
// the array part if it ends with a nil, otherwise an unbound search in the
// hash part. With holes, it is the border Lua 5.1 finds (it was the last
// non-nil element of the array part).
func (tb *LTable) Len() int {
	j := len(tb.array)
	if j > 0 && tb.array[j-1] == LNil {
		i := 0
		for j-i > 1 {
			m := int(uint(i+j) / 2)
			if tb.array[m-1] == LNil {
				j = m
			} else {
				i = m
			}
		}
		return i
	}
	if len(tb.hash.nodes) == 0 {
		return j
	}
	return tb.unboundSearch(j)
}

// unboundSearch is Lua 5.1's unbound_search: j is 0 or a present key.
func (tb *LTable) unboundSearch(j int) int {
	const maxInt = 1<<31 - 1 // C's INT_MAX
	i := j
	j++
	for tb.RawGetInt(j) != LNil {
		i = j
		j *= 2
		if j > maxInt {
			// a table built to defeat the search: linear search
			i = 1
			for tb.RawGetInt(i) != LNil {
				i++
			}
			return i - 1
		}
	}
	for j-i > 1 {
		m := (i + j) / 2
		if tb.RawGetInt(m) == LNil {
			j = m
		} else {
			i = m
		}
	}
	return i
}

// Append appends a given LValue to this LTable.
// go-moonscript patch: at Len()+1, as table.insert(t, v) does (it was after
// the last non-nil element of the array part).
func (tb *LTable) Append(value LValue) {
	tb.checkWritable()
	if value == LNil {
		return
	}
	tb.RawSetInt(tb.Len()+1, value)
}

// Insert inserts a given LValue at position `i` in this table.
// go-moonscript patch: the elements i..Len() move up, as table.insert(t, i,
// v) does; for i < 1 or past Len()+1, it only sets t[i].
func (tb *LTable) Insert(i int, value LValue) {
	tb.checkWritable()
	if i < 1 {
		tb.RawSetInt(i, value)
		return
	}
	if e := tb.Len() + 1; i < e {
		tb.moveUp(i, e)
	}
	tb.RawSetInt(i, value)
}

// moveUp sets t[k] = t[k-1] for k = e down to i+1 (1 <= i < e), as
// table.insert does.
func (tb *LTable) moveUp(i, e int) {
	tb.RawSetInt(e, tb.RawGetInt(e-1)) // may be a new key
	if e-1 <= len(tb.array) {
		copy(tb.array[i:e-1], tb.array[i-1:e-2])
		return
	}
	for k := e - 1; k > i; k-- {
		tb.RawSetInt(k, tb.RawGetInt(k-1))
	}
}

// MaxN returns a maximum number key that nil value does not exist before it.
// go-moonscript patch: the largest positive integer key below MaxArrayIndex
// (the keys out of the array part were not counted); table.maxn is Lua's.
func (tb *LTable) MaxN() int {
	n := 0
	for i := len(tb.array) - 1; i >= 0; i-- {
		if tb.array[i] != LNil {
			n = i + 1
			break
		}
	}
	tb.hash.forEach(func(k, _ LValue) {
		if kn, ok := k.(LNumber); ok && isArrayKey(kn) && int(kn) > n {
			n = int(kn)
		}
	})
	return n
}

// Remove removes from this table the element at a given position.
// go-moonscript patch: as table.remove(t, pos), the elements pos+1..Len()
// move down; a position below 1 removes the element at Len(), one past
// Len() removes nothing.
func (tb *LTable) Remove(pos int) LValue {
	tb.checkWritable()
	e := tb.Len()
	if pos < 1 {
		pos = e
	}
	if pos < 1 || pos > e {
		return LNil
	}
	oldval := tb.RawGetInt(pos)
	if e <= len(tb.array) {
		copy(tb.array[pos-1:e-1], tb.array[pos:e])
	} else {
		for ; pos < e; pos++ {
			tb.RawSetInt(pos, tb.RawGetInt(pos+1))
		}
	}
	tb.RawSetInt(e, LNil)
	return oldval
}

// RawSet sets a given LValue to a given index without the __newindex metamethod.
// It is recommended to use `RawSetString` or `RawSetInt` for performance
// if you already know the given LValue is a string or number.
// go-moonscript patch: a nil or NaN key is ignored (yuin/gopher-lua#319:
// such a key went into the table, where it could not be found again; the
// Lua functions raise "table index is nil" or "table index is NaN").
func (tb *LTable) RawSet(key LValue, value LValue) {
	tb.checkWritable()
	switch k := key.(type) {
	case LNumber:
		tb.setNumber(k, value)
	case LString:
		tb.setString(k, string(k), hashString(string(k)), value)
	case *LNilType, nil:
	default:
		h, isptr := keyHash(key)
		if i := tb.hash.find(key, h); i >= 0 {
			tb.hash.nodes[i].val = value
			return
		}
		tb.newKey(key, h, isptr, value)
	}
}

func (tb *LTable) setNumber(k LNumber, value LValue) {
	if i := tb.arrayIndex(k); i >= 0 {
		tb.array[i] = value
		return
	}
	if k != k { // NaN
		return
	}
	h := hashNumber(k)
	if i := tb.hash.findNumber(k, h); i >= 0 {
		tb.hash.nodes[i].val = value
		return
	}
	tb.newKey(k, h, false, value)
}

// setString sets the string key key, lkey as an LValue (nil: not boxed
// yet), whose hash is h.
func (tb *LTable) setString(lkey LValue, key string, h uint32, value LValue) {
	if i := tb.hash.findString(key, h); i >= 0 {
		tb.hash.nodes[i].val = value
		return
	}
	if lkey == nil {
		lkey = LString(key)
	}
	tb.newKey(lkey, h, false, value)
}

// setStringAt is setString for a key whose node (live or dead) is j, or
// that is not in the table if j < 0 (hash.findString's result).
func (tb *LTable) setStringAt(j int32, lkey LValue, key string, h uint32, value LValue) {
	if j >= 0 {
		tb.hash.nodes[j].val = value
		return
	}
	if lkey == nil {
		lkey = LString(key)
	}
	tb.newKey(lkey, h, false, value)
}

// newKey adds a key that is not in the table (Lua's newkey), whose hash
// is h; isptr: the hash is an address. As in Lua, a nil value adds a dead
// key.
func (tb *LTable) newKey(key LValue, h uint32, isptr bool, value LValue) {
	if tb.hash.add(key, value, h, isptr) {
		return
	}
	tb.rehash(key)
	if n, ok := key.(LNumber); ok {
		if i := tb.arrayIndex(n); i >= 0 {
			tb.array[i] = value
			return
		}
	}
	tb.hash.mustAdd(key, value, h, isptr)
}

// countInt counts key in nums if it may go in the array part (Lua's
// countint).
func countInt(key LValue, nums *[maxArrayBits + 1]int) int {
	if n, ok := key.(LNumber); ok {
		if k := int(n); LNumber(k) == n && k >= 1 && k <= 1<<maxArrayBits && k <= MaxArrayIndex {
			nums[bits.Len(uint(k-1))]++
			return 1
		}
	}
	return 0
}

// rehash resizes the table for the new key ek as Lua 5.1's rehash does.
func (tb *LTable) rehash(ek LValue) {
	var nums [maxArrayBits + 1]int // nums[i]: the keys in (2^(i-1), 2^i]
	nasize := 0
	for i, v := range tb.array {
		if v != LNil {
			nums[bits.Len(uint(i))]++
			nasize++
		}
	}
	totaluse := nasize
	tb.hash.forEach(func(k, _ LValue) {
		nasize += countInt(k, &nums)
		totaluse++
	})
	nasize += countInt(ek, &nums)
	totaluse++
	// computesizes: the largest power of 2 n such that more than half of
	// 1..n are keys
	a, na, n := 0, 0, 0
	for i, twotoi := 0, 1; twotoi/2 < nasize; i, twotoi = i+1, twotoi*2 {
		if nums[i] > 0 {
			a += nums[i]
			if a > twotoi/2 {
				n = twotoi
				na = a
			}
		}
		if a == nasize {
			break
		}
	}
	tb.resize(n, totaluse-na)
}

// resize gives the table an array part of nasize elements and a hash part
// for nhsize keys, as Lua 5.1's resize does (in the same order, so that the
// keys get the same nodes). The dead keys go.
func (tb *LTable) resize(nasize, nhsize int) {
	if n := nasize + nhsize; n >= tableBigSlots && n > len(tb.array)+len(tb.hash.nodes) {
		tableReserve(nasize, nhsize) // go-moonscript patch: before any change
	}
	oldarray := tb.array
	oldhash := tb.hash
	if nasize != len(oldarray) {
		tb.array = resizedArray(oldarray, nasize)
		defer releaseArray(oldarray) // when resize no longer reads it
	}
	tb.hash.init(nhsize)
	for i := nasize; i < len(oldarray); i++ {
		if v := oldarray[i]; v != LNil {
			k := LNumber(i + 1)
			tb.hash.mustAdd(k, v, hashNumber(k), false)
		}
	}
	for i := len(oldhash.nodes) - 1; i >= 0; i-- {
		node := &oldhash.nodes[i]
		if node.key == nil || node.val == LNil {
			continue
		}
		if n, ok := node.key.(LNumber); ok {
			if j := tb.arrayIndex(n); j >= 0 {
				tb.array[j] = node.val
				continue
			}
		}
		tb.hash.mustAdd(node.key, node.val, node.hash, isPtrKey(node.key))
	}
	if tb.hash.ptrkeys > 0 {
		// the insertion order is the old one, not the order of the
		// nodes, which depends on addresses
		h := &tb.hash
		h.first, h.last = 0, 0
		for i := nasize; i < len(oldarray); i++ {
			if oldarray[i] != LNil {
				h.link(h.findKey(LNumber(i + 1)))
			}
		}
		for i := oldhash.first; i > 0; {
			node := &oldhash.nodes[i-1]
			i = node.ins
			if node.val == LNil {
				continue
			}
			if j := h.findKey(node.key); j >= 0 {
				h.link(j)
			}
		}
	}
}

// resizedArray returns a new array part of n elements with the elements
// of a, LNil past them. (go-moonscript patch: a make followed by a copy
// from local to local is one makeslicecopy, whose write barrier shades
// only the source; copy into the result of make shades the destination
// too. The new elements are set with fillNil.)
func resizedArray(a []LValue, n int) []LValue {
	array := make([]LValue, n)
	copy(array, a)
	if len(a) < n {
		fillNil(array[len(a):])
	}
	return array
}

// releaseArray clears an array part the table no longer uses if it may be
// the one allocated with the table (newLTable): that one lives as long as
// the table, and the values left in it would keep the objects they refer
// to from Go's collector (a weak table's entry would not be cleared).
// (go-moonscript patch)
func releaseArray(old []LValue) {
	if cap(old) <= 4 {
		clear(old)
	}
}

// fillNil sets the elements of s, which are nil (zeroed memory), to LNil.
// The two words of LNil are pointers out of the Go heap (an itab and
// runtime.zerobase), and so are those of nil: the collector has nothing
// to shade, and they are copied as plain words, without a write barrier.
func fillNil(s []LValue) {
	if len(s) < 8 {
		for i := range s {
			s[i] = LNil
		}
		return
	}
	w := unsafe.Slice((*uintptr)(unsafe.Pointer(&s[0])), 2*len(s))
	nw := (*[2]uintptr)(unsafe.Pointer(&LNil))
	w[0], w[1] = nw[0], nw[1]
	for i := 2; i < len(w); i *= 2 {
		copy(w[i:], w[:i])
	}
}

// resizeArray makes the array part n elements long if it is shorter
// (OP_SETLIST, luaH_resizearray).
func (tb *LTable) resizeArray(n int) {
	if n <= len(tb.array) {
		return
	}
	if len(tb.hash.nodes) == 0 {
		if n >= tableBigSlots {
			tableReserve(n, 0) // go-moonscript patch
		}
		old := tb.array
		tb.array = resizedArray(old, n)
		releaseArray(old)
		return
	}
	tb.resize(n, len(tb.hash.nodes))
}

// RawSetInt sets a given LValue at a position `key` without the __newindex metamethod.
func (tb *LTable) RawSetInt(key int, value LValue) {
	tb.checkWritable()
	if uint(key-1) < uint(len(tb.array)) {
		tb.array[key-1] = value
		return
	}
	tb.setNumber(LNumber(key), value)
}

// RawSetString sets a given LValue to a given string index without the __newindex metamethod.
func (tb *LTable) RawSetString(key string, value LValue) {
	tb.checkWritable()
	tb.setString(nil, key, hashString(key), value)
}

// RawSetH sets a given LValue to a given index without the __newindex metamethod.
// go-moonscript patch: it is RawSet (a number key may be in the array part).
func (tb *LTable) RawSetH(key LValue, value LValue) {
	tb.RawSet(key, value)
}

// RawGet returns an LValue associated with a given key without __index metamethod.
func (tb *LTable) RawGet(key LValue) LValue {
	switch k := key.(type) {
	case LNumber:
		if i := tb.arrayIndex(k); i >= 0 {
			return tb.array[i]
		}
		return tb.hash.getNumber(k)
	case LString:
		return tb.hash.getString(string(k))
	}
	return tb.hash.get(key)
}

// RawGetInt returns an LValue at position `key` without __index metamethod.
// go-moonscript patch: the keys out of the array part are in the hash part
// (RawGetInt(0) did not find t[0]; fixed in the kiven-li/golua and
// dong-yue/gopher-lua forks too).
func (tb *LTable) RawGetInt(key int) LValue {
	if uint(key-1) < uint(len(tb.array)) {
		return tb.array[key-1]
	}
	if len(tb.hash.nodes) == 0 {
		return LNil
	}
	return tb.hash.getNumber(LNumber(key))
}

// RawGetH returns an LValue associated with a given key without __index metamethod.
// go-moonscript patch: it is RawGet (a number key may be in the array part).
func (tb *LTable) RawGetH(key LValue) LValue {
	return tb.RawGet(key)
}

// RawGetString returns an LValue associated with a given key without __index metamethod.
func (tb *LTable) RawGetString(key string) LValue {
	return tb.hash.getString(key)
}

// Frozen (read-only) tables. (go-moonscript patch)
//
// A frozen table cannot be changed: its fields and its metatable stay as
// they are. Freezing is for tables shared with code that must not change
// them, such as the library tables of a sandbox (see the sandbox package).
//
// Every way Lua code has to change a table raises the Lua error
// "attempt to modify a read-only table" (catchable by pcall, with the
// position of the assignment, or of the caller of the library function):
// an assignment t[k] = v, also through __newindex chains; rawset;
// table.insert, table.remove, table.sort, LuaJIT's table.clear and
// table.move into it (even when they would change nothing);
// setmetatable and debug.setmetatable. The assignment raises the error
// whether the key exists or not, and does not call a __newindex
// metamethod of the frozen table: the table is read-only, not a proxy
// (a table whose __newindex is a frozen table raises it when the chain
// gets there). The LState functions follow Lua: SetField, SetTable,
// SetGlobal, RawSet, RawSetInt and SetMetatable raise the same Lua error.
//
// The LTable methods that change the table (RawSet, RawSetInt,
// RawSetString, RawSetH, Append, Insert, Remove) panic with
// ErrFrozenTable instead: changing a frozen table from Go is a
// programming error. (A panic in a Go function called by Lua becomes an
// error of PCall, as every Go panic does.) Go code that assigns the
// Metatable field directly is not checked.
//
// A frozen table is read and iterated as any other (next, pairs, ipairs,
// All, Array), may be the metatable or the __index of other tables, and
// is collected as any other. Freezing is shallow: the tables it holds are
// not frozen (see FreezeDeep). The entries of a frozen weak table are
// still cleared when their objects are collected, as weakness is not a
// change to the table made by a program.

// ErrFrozenTable is the panic value of the LTable methods that change a
// frozen table; its text is the message of the Lua error.
var ErrFrozenTable = errors.New(frozenTableMessage)

const frozenTableMessage = "attempt to modify a read-only table"

// Freeze makes the table read-only, for good. Its parts keep their size:
// a frozen table never gets a new key, so it no longer needs the free
// node pointer of its hash part, which marks it.
func (tb *LTable) Freeze() {
	tb.hash.lastfree = -1
}

// Frozen reports whether the table is read-only (Freeze).
func (tb *LTable) Frozen() bool {
	return tb.hash.lastfree < 0
}

// FreezeDeep freezes the table and, recursively, the tables that are its
// keys, its values and its metatable, and theirs: all the tables a program
// can reach from it without calling a function (the environments and
// upvalues of functions are not followed). Use it for a tree of data, not
// for a table that holds tables of the state, such as package (whose
// package.loaded holds the globals).
func (tb *LTable) FreezeDeep() {
	seen := map[*LTable]bool{tb: true}
	todo := []*LTable{tb}
	add := func(v LValue) {
		if t, ok := v.(*LTable); ok && !seen[t] {
			seen[t] = true
			todo = append(todo, t)
		}
	}
	for len(todo) > 0 {
		t := todo[len(todo)-1]
		todo = todo[:len(todo)-1]
		t.Freeze()
		add(t.Metatable)
		for k, v := range t.All() {
			add(k)
			add(v)
		}
	}
}

// checkWritable panics with ErrFrozenTable if the table is frozen.
func (tb *LTable) checkWritable() {
	if tb.Frozen() {
		panicFrozen()
	}
}

//go:noinline
func panicFrozen() {
	panic(ErrFrozenTable)
}

// ForEach iterates over this table of elements, yielding each in turn to a given function.
// go-moonscript patch: the array part, then the hash part in the order its
// keys were added (it was Go's random map order).
func (tb *LTable) ForEach(cb func(LValue, LValue)) {
	for i, v := range tb.array {
		if v != LNil {
			cb(LNumber(i+1), v)
		}
	}
	tb.hash.forEach(cb)
}

// All returns an iterator over the key/value pairs of the table, in the
// order next and pairs give them: for k, v := range tb.All() visits the
// pairs a loop of Next calls from LNil visits, and a break stops it.
// (go-moonscript patch)
//
// While it runs, the loop body may assign to fields of the table that
// exist, nil included, as with next: the other pairs are visited once
// each, in the same order, and a field set to nil before the loop
// reaches it is not visited. As in Lua, adding a field (assigning to a
// key that has no value) during the loop is undefined: the loop may skip
// pairs, visit some twice and may or may not visit the new ones, but it
// does not panic and it ends; when the table has been resized, it goes
// on over the keys the table had, with their current values.
//
// It reads the table raw (no __index, __pairs or metatable at all).
func (tb *LTable) All() iter.Seq2[LValue, LValue] {
	return func(yield func(LValue, LValue) bool) {
		// the array part as it was: a key past it is either in the hash
		// part as it was, or new
		n := len(tb.array)
		for i := 0; i < n; i++ {
			var v LValue
			if a := tb.array; i < len(a) {
				v = a[i]
			} else {
				v = tb.hash.getNumber(LNumber(i + 1))
			}
			if v != LNil && !yield(LNumber(i+1), v) {
				return
			}
		}
		// the hash part: its nodes as they were; when the table has been
		// resized (live is false), the table no longer writes them, so its
		// keys and links are those it had then, and the values are read
		// from the table
		nodes := tb.hash.nodes
		if len(nodes) == 0 {
			return
		}
		live := func() bool { return unsafe.SliceData(tb.hash.nodes) == &nodes[0] }
		if !tb.hash.inOrder() {
			for i := range nodes {
				nd := &nodes[i]
				k, v := nd.key, nd.val
				if k == nil {
					continue
				}
				if !live() {
					v = tb.RawGet(k)
				}
				if v != LNil && !yield(k, v) {
					return
				}
			}
			return
		}
		// insertion order: at most one step per node, which a traversal
		// that adds no key never reaches, so that keys added during the
		// loop (relinked nodes) cannot make it endless
		for j, steps := tb.hash.first, 0; j > 0 && steps < len(nodes); steps++ {
			k, v := nodes[j-1].key, nodes[j-1].val
			if k == nil {
				return // not a node of the chain: only an undefined traversal gets here
			}
			if !live() {
				v = tb.RawGet(k)
			}
			if v != LNil && !yield(k, v) {
				return
			}
			if live() && nodes[j-1].key != k {
				// a new key moved k to another node (or took its dead
				// node): go on from where k is, if it still is
				i := tb.hash.findKey(k)
				if i < 0 {
					return
				}
				j = i + 1
			}
			j = nodes[j-1].ins
		}
	}
}

// Array returns an iterator over the elements 1, 2, ... of the table up to
// the first nil, as ipairs: for i, v := range tb.Array() visits i, t[i]
// read raw. Like ipairs, it reads t[i] when it gets there, so that the
// loop body may change the table. (go-moonscript patch)
func (tb *LTable) Array() iter.Seq2[int, LValue] {
	return func(yield func(int, LValue) bool) {
		for i := 1; ; i++ {
			v := tb.RawGetInt(i)
			if v == LNil || !yield(i, v) {
				return
			}
		}
	}
}

// This function is equivalent to lua_next ( http://www.lua.org/manual/5.1/manual.html#lua_next ).
// go-moonscript patch: a key that is not in the table ends the traversal
// (it went on from the second key of the hash part); next raises an error.
func (tb *LTable) Next(key LValue) (LValue, LValue) {
	k, v, _ := tb.next(key)
	return k, v
}

// next is Next; ok is false if key is not a key of the table (Lua 5.1's
// "invalid key to 'next'"). The array part comes first, then the hash part
// in insertion order.
func (tb *LTable) next(key LValue) (k, v LValue, ok bool) {
	index := 0
	if key != LNil && key != nil {
		index = -1
		if n, isnum := key.(LNumber); isnum {
			index = tb.arrayIndex(n)
		}
		if index < 0 {
			i := tb.hash.findKey(key)
			if i < 0 {
				return LNil, LNil, false
			}
			k, v = tb.hash.nextAfter(i)
			return k, v, true
		}
		index++
	}
	for ; index < len(tb.array); index++ {
		if v := tb.array[index]; v != LNil {
			return LNumber(index + 1), v, true
		}
	}
	k, v = tb.hash.nextAfter(-1)
	return k, v, true
}
