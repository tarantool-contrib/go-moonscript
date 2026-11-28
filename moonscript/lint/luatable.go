package lint

// luaTable reproduces the iteration order of a Lua 5.1 table holding only
// string keys whose values are never set to nil (ltable.c, lstring.c), so
// `pairs` over the linter's unused names visits them in the same order as
// the reference: names reported together (`a, b, c = ...`) come out in Lua's
// hash order, e.g. "`a`, `c`, `b`".
type luaTable struct {
	nodes    []luaNode // len is a power of 2; nil is Lua's dummynode
	lastFree int       // getfreepos scans down from here
}

type luaNode struct {
	key  string
	used bool // key is non-nil
	val  unusedVal
	next int // index of the next node in the collision chain, -1 for none
}

// unusedVal is a lint_unused_names value: a position (node[-1] or 0), or
// false once the name has been used.
type unusedVal struct {
	pos   int
	isPos bool
}

// luaStrHash is luaS_newlstr's hash.
func luaStrHash(s string) uint32 {
	l := len(s)
	h := uint32(l)
	step := (l >> 5) + 1
	for l1 := l; l1 >= step; l1 -= step {
		h ^= (h << 5) + (h >> 2) + uint32(s[l1-1])
	}
	return h
}

func (t *luaTable) mainPosition(key string) int {
	return int(luaStrHash(key) & uint32(len(t.nodes)-1))
}

func (t *luaTable) find(key string) int {
	if len(t.nodes) == 0 {
		return -1
	}
	for i := t.mainPosition(key); i >= 0; i = t.nodes[i].next {
		if t.nodes[i].used && t.nodes[i].key == key {
			return i
		}
	}
	return -1
}

func (t *luaTable) get(key string) (unusedVal, bool) {
	if i := t.find(key); i >= 0 {
		return t.nodes[i].val, true
	}
	return unusedVal{}, false
}

func (t *luaTable) empty() bool {
	for _, n := range t.nodes {
		if n.used {
			return false
		}
	}
	return true
}

// set is t[key] = val (luaH_set + newkey).
func (t *luaTable) set(key string, val unusedVal) {
	if i := t.find(key); i >= 0 {
		t.nodes[i].val = val
		return
	}
	t.newKey(key, val)
}

func (t *luaTable) getFreePos() int {
	for t.lastFree > 0 {
		t.lastFree--
		if !t.nodes[t.lastFree].used {
			return t.lastFree
		}
	}
	return -1
}

func (t *luaTable) newKey(key string, val unusedVal) {
	if len(t.nodes) == 0 {
		// the dummynode is never free
		t.rehash()
		t.set(key, val)
		return
	}
	mp := t.mainPosition(key)
	if t.nodes[mp].used {
		n := t.getFreePos()
		if n < 0 {
			t.rehash()
			t.set(key, val)
			return
		}
		othern := t.mainPosition(t.nodes[mp].key)
		if othern != mp {
			// colliding node is out of its main position: move it into the
			// free position
			for t.nodes[othern].next != mp {
				othern = t.nodes[othern].next
			}
			t.nodes[othern].next = n
			t.nodes[n] = t.nodes[mp]
			t.nodes[mp] = luaNode{next: -1}
		} else {
			// new node goes into the free position
			t.nodes[n].next = t.nodes[mp].next
			t.nodes[mp].next = n
			mp = n
		}
	}
	t.nodes[mp].key = key
	t.nodes[mp].used = true
	t.nodes[mp].val = val
}

// rehash grows the node part to fit every key plus the one being added
// (string keys only: the array part stays empty).
func (t *luaTable) rehash() {
	total := 1
	for _, n := range t.nodes {
		if n.used {
			total++
		}
	}
	size := 1
	for size < total {
		size <<= 1
	}
	old := t.nodes
	t.nodes = make([]luaNode, size)
	for i := range t.nodes {
		t.nodes[i].next = -1
	}
	t.lastFree = size
	for i := len(old) - 1; i >= 0; i-- {
		if old[i].used {
			t.set(old[i].key, old[i].val)
		}
	}
}

// each is pairs order.
func (t *luaTable) each(fn func(key string, val unusedVal)) {
	for _, n := range t.nodes {
		if n.used {
			fn(n.key, n.val)
		}
	}
}
