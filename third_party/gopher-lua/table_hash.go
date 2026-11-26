package lua

// go-moonscript patch: the hash part of a table (the keys that are not in
// its array part) is one hash table of hnode slots with cached hashes,
// instead of gopher-lua's four structures (a map for the string keys, one
// for the other keys, and the keys in insertion order with a map from key
// to position, all written on each new key). Idea from pschlump/gopher-lua
// 0fe4625111 ("P2: table engine — unified open-addressed hash part"),
// rewritten as the node vector of a Lua 5.1 table (ltable.c): the same
// hash functions for strings, numbers and booleans, the same main
// positions, the same collision chains (Brent's variation, with a free
// pointer that only goes down), so that its size changes when Lua 5.1's
// does and # of tables with holes is Lua 5.1's (see table.go).
//
// A key whose value is set to nil stays in its node (a dead key) until the
// table is rehashed or a new key whose main position it is takes the node,
// so next works on a key whose value was just cleared, and the keys removed
// do not accumulate (yuin/gopher-lua#214).
//
// Iteration order (next, pairs, ForEach): Lua 5.1's, the node order, if no
// key is a table, function, userdata or thread; otherwise, since their
// hashes are addresses, the order in which the keys were added (a chain of
// nodes kept up to date in both cases), so that the order is the same on
// every run for the same operations.

import (
	"hash/maphash"
	"math"
	"unsafe"
)

// hnode is a node of the hash part.
type hnode struct {
	key  LValue // nil: a free node
	val  LValue // LNil (or nil in a free node): no value, a dead key
	hash uint32 // keyHash(key)
	next int32  // 1 + the next node in the collision chain, 0 at the end
	ins  int32  // 1 + the next node in insertion order, 0 at the end
	prev int32  // 1 + the previous node in insertion order, 0 at the start
}

// hashPart is the hash part of an LTable. Its zero value is an empty hash
// part of size 0 (Lua's dummynode).
type hashPart struct {
	nodes    []hnode // size nodes
	lastfree int32   // the free nodes are below it
	ptrkeys  int32   // keys hashed by address (live or dead)
	first    int32   // 1 + the first node in insertion order, 0 if none
	last     int32   // 1 + the last one, 0 if none
}

// hashString is Lua 5.1's luaS_hash.
func hashString(s string) uint32 {
	l := len(s)
	h := uint32(l)
	step := (l >> 5) + 1
	for l1 := l; l1 >= step; l1 -= step {
		h ^= (h << 5) + (h >> 2) + uint32(s[l1-1])
	}
	return h
}

// hashNumber is the hash of a number key: Lua 5.1's hashnum adds the two
// halves of the double (0 and -0: 0, node 0).
func hashNumber(n LNumber) uint32 {
	if n == 0 {
		return 0
	}
	b := math.Float64bits(float64(n))
	return uint32(b) + uint32(b>>32)
}

var hashSeed = maphash.MakeSeed()

// keyHash is the hash of a key and whether it is an address.
func keyHash(key LValue) (uint32, bool) {
	switch k := key.(type) {
	case LString:
		return hashString(string(k)), false
	case LNumber:
		return hashNumber(k), false
	case LBool:
		if k {
			return 1, false
		}
		return 0, false
	case *LTable:
		return uint32(uintptr(unsafe.Pointer(k))), true
	case *LFunction:
		return uint32(uintptr(unsafe.Pointer(k))), true
	case *LUserData:
		return uint32(uintptr(unsafe.Pointer(k))), true
	case *LState:
		return uint32(uintptr(unsafe.Pointer(k))), true
	}
	// other LValue implementations (a Go map key panics on the
	// uncomparable ones too)
	return uint32(maphash.Comparable(hashSeed, key)), true
}

// isPtrKey reports whether the hash of key is an address (keyHash).
func isPtrKey(key LValue) bool {
	switch key.(type) {
	case LString, LNumber, LBool:
		return false
	}
	return true
}

// init makes h an empty hash part with room for n keys, rounded up to a
// power of 2 (Lua's setnodevector).
func (h *hashPart) init(n int) {
	*h = hashPart{}
	if n <= 0 {
		return
	}
	size := 1
	for size < n {
		size *= 2
	}
	h.nodes = make([]hnode, size)
	h.lastfree = int32(size)
}

// mainPosition is the node where a key with hash hash goes first.
func (h *hashPart) mainPosition(key LValue, hash uint32) int32 {
	switch key.(type) {
	case LString, LBool:
		return int32(hash & uint32(len(h.nodes)-1)) // hashpow2
	}
	return int32(hash % (uint32(len(h.nodes)-1) | 1)) // hashmod
}

// find returns the node of key (live or dead), or -1. key is not a string
// nor a number.
func (h *hashPart) find(key LValue, hash uint32) int32 {
	if len(h.nodes) == 0 {
		return -1
	}
	i := h.mainPosition(key, hash)
	for {
		n := &h.nodes[i]
		if n.hash == hash && n.key == key {
			return i
		}
		if n.next == 0 {
			return -1
		}
		i = n.next - 1
	}
}

// findNumber returns the node of the number key key (live or dead), or -1.
func (h *hashPart) findNumber(key LNumber, hash uint32) int32 {
	if len(h.nodes) == 0 {
		return -1
	}
	i := int32(hash % (uint32(len(h.nodes)-1) | 1))
	for {
		n := &h.nodes[i]
		if n.hash == hash {
			if k, ok := n.key.(LNumber); ok && k == key {
				return i
			}
		}
		if n.next == 0 {
			return -1
		}
		i = n.next - 1
	}
}

// findString returns the node of the string key key (live or dead), or -1.
func (h *hashPart) findString(key string, hash uint32) int32 {
	if len(h.nodes) == 0 {
		return -1
	}
	i := int32(hash & uint32(len(h.nodes)-1))
	for {
		n := &h.nodes[i]
		if n.hash == hash {
			if s, ok := n.key.(LString); ok && string(s) == key {
				return i
			}
		}
		if n.next == 0 {
			return -1
		}
		i = n.next - 1
	}
}

// findKey returns the node of key (live or dead), or -1.
func (h *hashPart) findKey(key LValue) int32 {
	switch k := key.(type) {
	case LString:
		return h.findString(string(k), hashString(string(k)))
	case LNumber:
		return h.findNumber(k, hashNumber(k))
	}
	hash, _ := keyHash(key)
	return h.find(key, hash)
}

// getStringH returns the value of the string key key, whose hash is hash,
// LNil if there is none.
func (h *hashPart) getStringH(key string, hash uint32) LValue {
	if i := h.findString(key, hash); i >= 0 {
		return h.nodes[i].val
	}
	return LNil
}

// getString returns the value of the string key key, LNil if there is none.
func (h *hashPart) getString(key string) LValue {
	if len(h.nodes) == 0 {
		return LNil
	}
	return h.getStringH(key, hashString(key))
}

// getNumber returns the value of the number key key, LNil if there is none.
func (h *hashPart) getNumber(key LNumber) LValue {
	if len(h.nodes) == 0 {
		return LNil
	}
	if i := h.findNumber(key, hashNumber(key)); i >= 0 {
		return h.nodes[i].val
	}
	return LNil
}

// get returns the value of key, LNil if there is none.
func (h *hashPart) get(key LValue) LValue {
	if len(h.nodes) == 0 {
		return LNil
	}
	if i := h.findKey(key); i >= 0 {
		return h.nodes[i].val
	}
	return LNil
}

// getFreePos returns a free node, or -1 (Lua's getfreepos).
func (h *hashPart) getFreePos() int32 {
	for h.lastfree > 0 {
		h.lastfree--
		if h.nodes[h.lastfree].key == nil {
			return h.lastfree
		}
	}
	return -1
}

// unlink removes node i from the insertion order.
func (h *hashPart) unlink(i int32) {
	n := &h.nodes[i]
	if n.prev > 0 {
		h.nodes[n.prev-1].ins = n.ins
	} else {
		h.first = n.ins
	}
	if n.ins > 0 {
		h.nodes[n.ins-1].prev = n.prev
	} else {
		h.last = n.prev
	}
	n.ins, n.prev = 0, 0
}

// link adds node i last in insertion order.
func (h *hashPart) link(i int32) {
	n := &h.nodes[i]
	n.ins, n.prev = 0, h.last
	if h.last > 0 {
		h.nodes[h.last-1].ins = i + 1
	} else {
		h.first = i + 1
	}
	h.last = i + 1
}

// add adds a key that is not in the hash part, as Lua 5.1's newkey does. It
// reports false, adding nothing, if there is no free node (the table must
// be rehashed).
func (h *hashPart) add(key, value LValue, hash uint32, isptr bool) bool {
	if len(h.nodes) == 0 {
		return false
	}
	mp := h.mainPosition(key, hash)
	m := &h.nodes[mp]
	if m.key != nil && m.val != LNil {
		// the main position is taken: use a free node
		f := h.getFreePos()
		if f < 0 {
			return false
		}
		othern := h.mainPosition(m.key, m.hash)
		if othern != mp {
			// the colliding node is out of its main position: move it
			// into the free node, and use its node
			for h.nodes[othern].next-1 != mp {
				othern = h.nodes[othern].next - 1
			}
			h.nodes[othern].next = f + 1
			fn := &h.nodes[f]
			*fn = *m
			if fn.prev > 0 {
				h.nodes[fn.prev-1].ins = f + 1
			} else {
				h.first = f + 1
			}
			if fn.ins > 0 {
				h.nodes[fn.ins-1].prev = f + 1
			} else {
				h.last = f + 1
			}
			m.next, m.ins, m.prev = 0, 0, 0
		} else {
			// the colliding node is in its main position: the new key
			// goes into the free node
			h.nodes[f].next = m.next
			m.next = f + 1
			mp = f
			m = &h.nodes[f]
		}
	} else if m.key != nil {
		// a dead key: its node is reused
		if isPtrKey(m.key) {
			h.ptrkeys--
		}
		h.unlink(mp)
	}
	m.key, m.val, m.hash = key, value, hash
	if isptr {
		h.ptrkeys++
	}
	h.link(mp)
	return true
}

// mustAdd is add for a key that fits (after a rehash).
func (h *hashPart) mustAdd(key, value LValue, hash uint32, isptr bool) {
	if !h.add(key, value, hash, isptr) {
		panic("gopher-lua: no free node in a resized table")
	}
}

// inOrder reports whether the iteration goes in insertion order (rather
// than node order).
func (h *hashPart) inOrder() bool {
	return h.ptrkeys > 0
}

// nextAfter returns the first live key after node i (i < 0: from the
// start) in iteration order.
func (h *hashPart) nextAfter(i int32) (LValue, LValue) {
	if h.inOrder() {
		j := h.first
		if i >= 0 {
			j = h.nodes[i].ins
		}
		for ; j > 0; j = h.nodes[j-1].ins {
			if n := &h.nodes[j-1]; n.val != LNil {
				return n.key, n.val
			}
		}
		return LNil, LNil
	}
	for i++; int(i) < len(h.nodes); i++ {
		if n := &h.nodes[i]; n.key != nil && n.val != LNil {
			return n.key, n.val
		}
	}
	return LNil, LNil
}

// forEach calls cb for each live key in iteration order. (If cb adds keys,
// it goes on over the nodes as they were.)
func (h *hashPart) forEach(cb func(LValue, LValue)) {
	nodes := h.nodes
	if h.inOrder() {
		for i := h.first; i > 0; {
			n := &nodes[i-1]
			i = n.ins
			if n.val != LNil {
				cb(n.key, n.val)
			}
		}
		return
	}
	for i := range nodes {
		if n := &nodes[i]; n.key != nil && n.val != LNil {
			cb(n.key, n.val)
		}
	}
}
