package lua

// go-moonscript patch: Lua 5.1's garbage collection as programs see it, over
// Go's collector, which frees the memory: weak tables (__mode), __gc for
// userdata (newproxy), collectgarbage's options and a memory limit
// (Options.MemoryLimit, "not enough memory").
//
// A collection cycle (collectgarbage("collect"), the end of a cycle of
// "step"s, or an automatic cycle) runs on the state's goroutine, at a safe
// point:
//
//   - The registers above the live ones and the call frames above the top
//     frame of the state's threads are cleared: Lua 5.1 does not mark them,
//     and Go would.
//   - If there are weak tables or userdata to finalize (those made by
//     newproxy with a metatable, and those given a metatable with a __gc
//     field), the Lua objects reachable from the roots (the registry, the globals, the
//     threads' stacks, the type metatables, the userdata waiting for their
//     __gc) are marked as Lua 5.1's collector marks them, weak references
//     excluded; the userdata not marked are separated, and those whose
//     metatable has a __gc field (read now, as Lua 5.1 does) are queued for
//     it, newest first. Go references to Lua objects (a Go closure's
//     variables, a Go value in a userdata) are not roots here, as C
//     references are not in Lua 5.1: a userdata that only Go code keeps
//     must be anchored (in the registry, say) to keep its __gc from running.
//   - The weak tables (those that got a metatable with a __mode field; its
//     value is read at each cycle) keep their references to the objects the
//     marking reached, and give up the others (tables, functions, userdata
//     and threads) for one runtime.GC(): Go's collector decides whether Go
//     references keep them, and the entries whose weak key or value it
//     collected are cleared, as in Lua 5.1 (where a key and a value
//     that refer to each other are not collected in a weak-keyed table:
//     there are no ephemerons). The weak values that are userdata being
//     finalized are cleared too; the weak keys stay until the next cycle.
//   - Then the __gc metamethods run, on this goroutine, never concurrently
//     with the VM. An error in one propagates from where the cycle ran (an
//     allocation, collectgarbage), as in Lua 5.1; L.Close runs the queued
//     ones and every remaining userdata's __gc, ignoring errors (lua_close).
//
// Automatic cycles run at allocation checkpoints (NEWTABLE, CONCAT,
// CLOSURE) after Go's collector has completed a cycle, only if there are
// weak tables or userdata to finalize, and not after collectgarbage("stop").
//
// "count" is the growth of Go's heap since the state was created (its
// lowest point since then, plus 20 Kbytes, about what a new Lua 5.1 state
// takes): Go has one heap per process, so it counts the allocations of the
// other goroutines too.

import (
	"cmp"
	"math"
	"runtime"
	"runtime/metrics"
	"slices"
	"sync"
	"sync/atomic"
	"unsafe"
	"weak"
)

const (
	// gcCheckInterval is the number of allocation checkpoints between two
	// looks at the heap.
	gcCheckInterval = 512
	// gcBaseBytes is added to the heap growth for "count".
	gcBaseBytes = 20 << 10
	// gcBigAlloc is the size from which an allocation is checked against
	// the memory limit by itself (the others at the checkpoints).
	gcBigAlloc = 64 << 10
	// gcStepSize is Lua 5.1's GCSTEPSIZE.
	gcStepSize = 1024
)

// errNotEnoughMemory is Lua 5.1's memory error message (LUA_ERRMEM).
const errNotEnoughMemory = "not enough memory"

// gcState is the collector's state of a Global.
type gcState struct {
	active  bool // the checkpoints have something to do
	tick    int  // checkpoints until the next look at the heap
	stopped bool // collectgarbage("stop")
	inCycle bool
	inFin   int  // __gc metamethods running
	closing bool // L.Close is running the finalizers

	samples []metrics.Sample
	floor   int64  // the lowest heap seen
	cycles  uint64 // Go's collection cycles at the end of the last cycle
	// estimate is the count after the last cycle; stepWork and stepTotal
	// are the work done by "step" in the current cycle and the cycle's
	// work (0: no cycle in progress)
	estimate, stepWork, stepTotal int64

	memLimit int64 // Options.MemoryLimit

	weak     map[weak.Pointer[LTable]]struct{} // tables that may be weak
	proxyMts map[weak.Pointer[LTable]]struct{} // metatables made by newproxy(true)
	fin      []*LUserData                      // to finalize when unreachable, oldest first
	finSet   map[*LUserData]struct{}
	pending  []*LUserData // to finalize now, in this order
	threads  []gcThread
	nthreads int // len(threads) after the last pruning

	entries []weakEntry // scratch of the weak table sweep
	marker  gcMarker    // scratch of the marking
}

// gcThread is a thread of the state: root is set for the threads made by
// the Go API (NewThread), which Go code keeps, so their stacks are roots.
type gcThread struct {
	ref  weak.Pointer[LState]
	root bool
}

// gcInit records the heap at the state's creation.
func (g *gcState) init() {
	g.samples = []metrics.Sample{
		{Name: "/memory/classes/heap/objects:bytes"},
		{Name: "/gc/cycles/total:gc-cycles"},
	}
	g.floor = math.MaxInt64
	_, g.cycles = g.heap()
	g.estimate = g.count()
}

// heap returns the bytes in Go's heap (live objects and those not swept
// yet) and the number of Go's collection cycles.
func (g *gcState) heap() (int64, uint64) {
	if g.samples == nil {
		g.init()
	}
	metrics.Read(g.samples)
	h := int64(g.samples[0].Value.Uint64())
	if h < g.floor {
		g.floor = h
	}
	return h, g.samples[1].Value.Uint64()
}

// count is collectgarbage("count") in bytes.
func (g *gcState) count() int64 {
	h, _ := g.heap()
	return h - g.floor + gcBaseBytes
}

// gcCountBytes returns the bytes in use as collectgarbage("count") and
// gcinfo() report them (Kbytes, lua_getgccount).
func (ls *LState) gcCountBytes() int64 {
	return ls.G.gc.count()
}

func (g *gcState) updateActive() {
	g.active = len(g.weak) > 0 || len(g.fin) > 0 || len(g.pending) > 0 || g.memLimit > 0
	if g.tick <= 0 || g.tick > gcCheckInterval {
		g.tick = gcCheckInterval
	}
}

/* registration {{{ */

// gcSetMetatable registers obj, which gets the metatable mt, as a table
// that may be weak or a userdata to finalize.
func (ls *LState) gcSetMetatable(obj LValue, mt LValue) {
	m, ok := mt.(*LTable)
	if !ok {
		return
	}
	switch o := obj.(type) {
	case *LTable:
		if m.RawGetString("__mode") != LNil {
			g := &ls.G.gc
			if g.weak == nil {
				g.weak = make(map[weak.Pointer[LTable]]struct{})
			}
			g.weak[weak.Make(o)] = struct{}{}
			g.updateActive()
		}
	case *LUserData:
		if m.RawGetString("__gc") != LNil {
			ls.gcRegisterUserData(o)
		}
	}
}

// gcRegisterUserData makes ud a userdata to finalize when it becomes
// unreachable (if its metatable has __gc then).
func (ls *LState) gcRegisterUserData(ud *LUserData) {
	g := &ls.G.gc
	if g.closing {
		return // as lua_close, which separates the userdata before their __gc run
	}
	if g.finSet == nil {
		g.finSet = make(map[*LUserData]struct{})
	}
	if _, ok := g.finSet[ud]; ok {
		return
	}
	g.finSet[ud] = struct{}{}
	g.fin = append(g.fin, ud)
	g.updateActive()
}

// gcNewThread registers a new thread of the state.
func (ls *LState) gcNewThread(th *LState, root bool) {
	g := &ls.G.gc
	if len(g.threads) >= 2*g.nthreads+64 {
		live := g.threads[:0]
		for _, t := range g.threads {
			if t.ref.Value() != nil {
				live = append(live, t)
			}
		}
		clear(g.threads[len(live):])
		g.threads = live
		g.nthreads = len(live)
	}
	g.threads = append(g.threads, gcThread{weak.Make(th), root})
}

/* }}} */

/* checkpoints {{{ */

// gcCheckpoint is an allocation checkpoint of the VM (luaC_checkGC), called
// when g.active.
func (ls *LState) gcCheckpoint() {
	g := &ls.G.gc
	g.tick--
	if g.tick > 0 {
		return
	}
	g.tick = gcCheckInterval
	if g.inCycle || g.inFin > 0 {
		return
	}
	h, cycles := g.heap()
	if g.memLimit > 0 && h-g.floor > g.memLimit {
		ls.gcCheckLimit(0)
	}
	if !g.stopped && (len(g.weak) > 0 || len(g.fin) > 0) && cycles != g.cycles &&
		(ls.G.gcPause <= 200 || h-g.floor+gcBaseBytes >= g.estimate/100*int64(ls.G.gcPause)) {
		ls.gcCycle(false)
	}
	if len(g.pending) > 0 {
		ls.gcRunFinalizers()
	}
}

// gcReserve checks that an allocation of n bytes fits in the memory limit,
// if it is a big one (the others are checked at the checkpoints).
func (ls *LState) gcReserve(n int64) {
	if ls.G.gc.memLimit > 0 && n >= gcBigAlloc {
		ls.gcCheckLimit(n)
	}
}

// gcCheckLimit checks that n more bytes fit in the memory limit; if not, it
// collects, and raises "not enough memory" if they still do not.
func (ls *LState) gcCheckLimit(n int64) {
	g := &ls.G.gc
	h, _ := g.heap()
	if h-g.floor <= g.memLimit-n {
		return
	}
	runtime.GC()
	h, _ = g.heap()
	if h-g.floor <= g.memLimit-n {
		return
	}
	ls.memoryError()
}

// gcReserveProduct is gcReserve for size * count bytes (string.rep).
func (ls *LState) gcReserveProduct(size, count int) {
	if ls.G.gc.memLimit <= 0 || size <= 0 || count <= 0 {
		return
	}
	if size > math.MaxInt64/count {
		ls.memoryError()
	}
	ls.gcReserve(int64(size) * int64(count))
}

// memoryError raises Lua 5.1's memory error: the message, without a
// position.
func (ls *LState) memoryError() {
	ls.Error(LString(errNotEnoughMemory), 0)
}

// tableLimits are the largest heaps that the memory limits of the states
// allow: tables do not know their state, so a table that grows a lot
// (resize) checks the process's heap against the largest of them.
var tableLimits struct {
	sync.Mutex
	bounds map[weak.Pointer[Global]]int64
	max    atomic.Int64 // 0: no state has a limit
}

func setTableLimit(g *Global, bound int64) {
	tableLimits.Lock()
	defer tableLimits.Unlock()
	if tableLimits.bounds == nil {
		tableLimits.bounds = make(map[weak.Pointer[Global]]int64)
	}
	switch {
	case g == nil: // only the pruning
	case bound > 0:
		tableLimits.bounds[weak.Make(g)] = bound
	default:
		delete(tableLimits.bounds, weak.Make(g))
	}
	var m int64
	for k, b := range tableLimits.bounds {
		if k.Value() == nil {
			delete(tableLimits.bounds, k)
		} else if b > m {
			m = b
		}
	}
	tableLimits.max.Store(m)
}

// SetMemoryLimit sets the memory limit of the state (Options.MemoryLimit):
// past it, building a string or growing a table raises "not enough
// memory"; 0 or less: no limit. The heap is the process's: a table, which
// does not know its state, is limited by the largest limit of the states.
// (go-moonscript patch)
func (ls *LState) SetMemoryLimit(bytes int64) {
	g := &ls.G.gc
	if bytes < 0 {
		bytes = 0
	}
	g.memLimit = bytes
	var bound int64
	if bytes > 0 {
		g.heap() // the floor
		bound = g.floor + bytes
	}
	setTableLimit(ls.G, bound)
	g.updateActive()
}

// MemoryLimit returns the memory limit of the state, 0 if none.
// (go-moonscript patch)
func (ls *LState) MemoryLimit() int64 {
	return ls.G.gc.memLimit
}

// tableReserve checks the growth of a table by n slots of the array part
// and m nodes against tableLimit; it is called by resize for large sizes.
func tableReserve(arraySlots, nodes int) {
	limit := tableLimits.max.Load()
	if limit <= 0 {
		return
	}
	n := int64(arraySlots)*int64(unsafe.Sizeof(LValue(nil))) + int64(nodes)*int64(unsafe.Sizeof(hnode{}))
	var ms [1]metrics.Sample
	ms[0].Name = "/memory/classes/heap/objects:bytes"
	metrics.Read(ms[:])
	if int64(ms[0].Value.Uint64())+n <= limit {
		return
	}
	runtime.GC()
	setTableLimit(nil, 0) // without the states collected meanwhile
	if limit = tableLimits.max.Load(); limit <= 0 {
		return
	}
	metrics.Read(ms[:])
	if int64(ms[0].Value.Uint64())+n <= limit {
		return
	}
	panic(newApiError(ApiErrorRun, LString(errNotEnoughMemory)))
}

/* }}} */

/* collection cycle {{{ */

// gcCycle runs a collection cycle; explicit: collectgarbage("collect") (or
// a cycle of steps), which always runs Go's collector.
func (ls *LState) gcCycle(explicit bool) {
	g := &ls.G.gc
	if g.inCycle {
		return
	}
	g.inCycle = true
	defer func() { g.inCycle = false }()
	ls.gcClearStale()
	var dying []*LUserData
	var m *gcMarker
	if len(g.fin) > 0 || len(g.weak) > 0 {
		m = ls.gcMark()
	}
	if len(g.fin) > 0 {
		dying = ls.gcSeparate(m)
		g.pending = append(g.pending, dying...)
	}
	if len(g.weak) > 0 {
		g.sweepWeak(m, dying, explicit)
	} else if explicit {
		runtime.GC()
	}
	if m != nil {
		clear(m.stack[:cap(m.stack)]) // keeps no object alive
		m.stack = m.stack[:0]
		if len(m.seen) > 1<<20 {
			m.seen = nil // not kept between the cycles of a big heap
		}
	}
	if len(g.proxyMts) > 0 {
		for k := range g.proxyMts {
			if k.Value() == nil {
				delete(g.proxyMts, k)
			}
		}
	}
	_, g.cycles = g.heap()
	g.estimate = g.count()
	g.stepWork, g.stepTotal = 0, 0
	g.updateActive()
}

// gcThreads calls fn for each live thread of the state (once each): the
// running one and those that resumed it, the main thread, the registered
// ones (root: made by NewThread, or running).
func (ls *LState) gcThreads(fn func(th *LState, root bool)) {
	g := &ls.G.gc
	seen := make(map[*LState]struct{}, 4)
	visit := func(th *LState, root bool) {
		if th == nil {
			return
		}
		if _, ok := seen[th]; ok {
			return
		}
		seen[th] = struct{}{}
		fn(th, root)
	}
	for th := ls; th != nil; th = th.Parent {
		visit(th, true)
	}
	for th := ls.G.CurrentThread; th != nil; th = th.Parent {
		visit(th, true)
	}
	visit(ls.G.MainThread, true)
	for _, t := range g.threads {
		if th := t.ref.Value(); th != nil {
			visit(th, t.root)
		}
	}
}

// liveTop returns the end of the live registers of th: its top, as for
// Lua 5.1's markstack (during a Go function, the registers of the Lua
// functions below it that are above its arguments are dead), or the end of
// the frame of the running function if it is a Lua function (an
// allocation checkpoint), which may be higher.
func (th *LState) liveTop() int {
	top := th.reg.top
	if th.stack != nil && th.stack.Sp() > 0 {
		if fr := th.stack.Last(); fr.Fn != nil && !fr.Fn.IsG {
			top = max(top, fr.LocalBase+int(fr.Fn.Proto.NumUsedRegisters))
		}
	}
	return min(top, len(th.reg.array))
}

// gcClearStale clears the registers and call frames of the threads above
// the live ones (Lua 5.1's markstack clears them too).
func (ls *LState) gcClearStale() {
	ls.gcThreads(func(th *LState, _ bool) {
		clear(th.reg.array[th.liveTop():])
		if th.stack != nil {
			th.stack.ClearUnused()
		}
	})
}

/* }}} */

/* finalizers: marking {{{ */

// gcMarker marks the Lua objects reachable from the roots, as Lua 5.1's
// collector does, to find the userdata to finalize.
type gcMarker struct {
	seen  map[uintptr]struct{} // the addresses of the marked objects, which live until the end of the cycle
	stack []LValue
}

func (m *gcMarker) mark(v LValue) {
	var p unsafe.Pointer
	switch o := v.(type) {
	case *LTable:
		p = unsafe.Pointer(o)
	case *LFunction:
		p = unsafe.Pointer(o)
	case *LUserData:
		p = unsafe.Pointer(o)
	case *LState:
		p = unsafe.Pointer(o)
	default:
		return
	}
	if p == nil {
		return
	}
	if _, ok := m.seen[uintptr(p)]; ok {
		return
	}
	m.seen[uintptr(p)] = struct{}{}
	m.stack = append(m.stack, v)
}

func (m *gcMarker) marked(v unsafe.Pointer) bool {
	_, ok := m.seen[uintptr(v)]
	return ok
}

// weakMode returns the weakness of a table whose metatable is mt (its
// __mode field, a string with 'k' or 'v').
func weakMode(mt LValue) (wk, wv bool) {
	m, ok := mt.(*LTable)
	if !ok {
		return false, false
	}
	mode, ok := m.RawGetString("__mode").(LString)
	if !ok {
		return false, false
	}
	for i := 0; i < len(mode); i++ {
		switch mode[i] {
		case 'k':
			wk = true
		case 'v':
			wv = true
		}
	}
	return wk, wv
}

func (m *gcMarker) propagate() {
	for len(m.stack) > 0 {
		v := m.stack[len(m.stack)-1]
		m.stack[len(m.stack)-1] = nil
		m.stack = m.stack[:len(m.stack)-1]
		switch o := v.(type) {
		case *LTable:
			m.mark(o.Metatable)
			wk, wv := weakMode(o.Metatable)
			if !wv {
				for _, e := range o.array {
					m.mark(e)
				}
			}
			for i := range o.hash.nodes {
				n := &o.hash.nodes[i]
				if n.key == nil || n.val == LNil {
					continue
				}
				if !wk {
					m.mark(n.key)
				}
				if !wv {
					m.mark(n.val)
				}
			}
		case *LFunction:
			if o.Env != nil {
				m.mark(o.Env)
			}
			for _, uv := range o.Upvalues {
				if uv != nil {
					m.mark(uv.Value())
				}
			}
		case *LUserData:
			m.mark(o.Metatable)
			if o.Env != nil {
				m.mark(o.Env)
			}
			if lv, ok := o.Value.(LValue); ok {
				m.mark(lv)
			}
		case *LState:
			m.markThread(o)
		}
	}
}

func (m *gcMarker) markThread(th *LState) {
	if th.Env != nil {
		m.mark(th.Env)
	}
	if th.hook.fn != nil {
		m.mark(th.hook.fn)
	}
	if th.reg != nil {
		for _, v := range th.reg.array[:th.liveTop()] {
			m.mark(v)
		}
	}
	if th.stack != nil {
		for i, sp := 0, th.stack.Sp(); i < sp; i++ {
			if fr := th.stack.At(i); fr.Fn != nil {
				m.mark(fr.Fn)
			}
		}
	}
}

// gcMark marks the Lua objects reachable from the roots.
func (ls *LState) gcMark() *gcMarker {
	g := &ls.G.gc
	m := &g.marker
	if m.seen == nil {
		m.seen = make(map[uintptr]struct{}, 1024)
	} else {
		clear(m.seen) // (the map keeps its size: no garbage for the next cycles)
	}
	m.mark(ls.G.Registry)
	m.mark(ls.G.Global)
	for _, mt := range ls.G.builtinMts {
		m.mark(mt)
	}
	ls.gcThreads(func(th *LState, root bool) {
		if root {
			m.mark(th)
		}
	})
	for _, ud := range g.pending {
		m.mark(ud)
	}
	m.propagate()
	return m
}

// gcSeparate takes the userdata that m did not mark out of g.fin; it
// returns those to finalize (their metatable has __gc), newest first, as
// Lua 5.1's luaC_separateudata.
func (ls *LState) gcSeparate(m *gcMarker) []*LUserData {
	g := &ls.G.gc
	var dying []*LUserData
	live := g.fin[:0]
	for i := len(g.fin) - 1; i >= 0; i-- {
		ud := g.fin[i]
		if m.marked(unsafe.Pointer(ud)) {
			continue
		}
		g.fin[i] = nil
		delete(g.finSet, ud)
		if mt, ok := ud.Metatable.(*LTable); ok && mt.RawGetString("__gc") != LNil {
			dying = append(dying, ud)
		}
	}
	for _, ud := range g.fin {
		if ud != nil {
			live = append(live, ud)
		}
	}
	clear(g.fin[len(live):])
	g.fin = live
	return dying
}

/* }}} */

/* weak tables {{{ */

const (
	weakArray = iota // a value in the array part
	weakVal          // the value of a node
	weakKey          // the key of a node
)

const (
	weakTable = iota
	weakFunction
	weakUserData
	weakThread
)

// weakEntry is a reference of a weak table to a collectable object, taken
// out of the table while Go collects.
type weakEntry struct {
	tb   *LTable
	obj  unsafe.Pointer // the referent, until ref is made
	ref  weak.Pointer[byte]
	idx  int32
	part uint8
	kind uint8
}

// collectedKey is the key of a node whose key was collected: a dead key no
// value equals.
type collectedKeyType struct{ _ byte }

func (*collectedKeyType) String() string   { return "<collected>" }
func (*collectedKeyType) Type() LValueType { return LTNil }

var collectedKey LValue = &collectedKeyType{}

// makeWeakEntry returns an entry for v if it is collectable and m did not
// mark it (the objects that the Lua roots reach are live: Go only decides
// for the others, which only Go references may keep).
func makeWeakEntry(m *gcMarker, tb *LTable, idx int, part uint8, v LValue) (weakEntry, bool) {
	e := weakEntry{tb: tb, idx: int32(idx), part: part}
	switch o := v.(type) {
	case *LTable:
		e.obj, e.kind = unsafe.Pointer(o), weakTable
	case *LFunction:
		e.obj, e.kind = unsafe.Pointer(o), weakFunction
	case *LUserData:
		e.obj, e.kind = unsafe.Pointer(o), weakUserData
	case *LState:
		e.obj, e.kind = unsafe.Pointer(o), weakThread
	default:
		return e, false
	}
	if m.marked(e.obj) {
		return e, false
	}
	return e, true
}

// value returns the referent of e, nil if it was collected.
func (e *weakEntry) value() LValue {
	p := unsafe.Pointer(e.ref.Value())
	if p == nil {
		return nil
	}
	switch e.kind {
	case weakTable:
		return (*LTable)(p)
	case weakFunction:
		return (*LFunction)(p)
	case weakUserData:
		return (*LUserData)(p)
	}
	return (*LState)(p)
}

// takeWeak takes the weak references out of the weak tables into
// g.entries.
//
//go:noinline
func (g *gcState) takeWeak(m *gcMarker) {
	entries := g.entries[:0]
	for ref := range g.weak {
		tb := ref.Value()
		if tb == nil {
			delete(g.weak, ref)
			continue
		}
		wk, wv := weakMode(tb.Metatable)
		if !wk && !wv {
			continue
		}
		if wv {
			for i, v := range tb.array {
				if e, ok := makeWeakEntry(m, tb, i, weakArray, v); ok {
					entries = append(entries, e)
					tb.array[i] = LNil
				}
			}
		}
		for i := range tb.hash.nodes {
			n := &tb.hash.nodes[i]
			if n.key == nil {
				continue
			}
			dead := n.val == LNil // a dead key does not keep its object
			if wv {
				if e, ok := makeWeakEntry(m, tb, i, weakVal, n.val); ok {
					entries = append(entries, e)
					n.val = LNil
				}
			}
			if wk || dead {
				if e, ok := makeWeakEntry(m, tb, i, weakKey, n.key); ok {
					entries = append(entries, e)
					n.key = collectedKey
				}
			}
		}
	}
	// the weak pointers are made from the highest address down: the
	// runtime keeps them in a list per span sorted by address, which each
	// one walks from the start
	slices.SortFunc(entries, func(a, b weakEntry) int {
		return cmp.Compare(uintptr(b.obj), uintptr(a.obj))
	})
	for i := range entries {
		e := &entries[i]
		e.ref = weak.Make((*byte)(e.obj))
		e.obj = nil
	}
	g.entries = entries
}

// sweepWeak runs Go's collector without the weak references to the objects
// that m did not mark (if there are any, or explicit) and clears those it
// collected; the weak values that are userdata of dying are cleared too.
func (g *gcState) sweepWeak(m *gcMarker, dying []*LUserData, explicit bool) {
	g.takeWeak(m)
	if len(g.entries) > 0 || explicit {
		runtime.GC()
	}
	var fin map[*LUserData]struct{}
	if len(dying) > 0 {
		fin = make(map[*LUserData]struct{}, len(dying))
		for _, ud := range dying {
			fin[ud] = struct{}{}
		}
	}
	// the keys first: a node whose key was collected is dead
	for i := range g.entries {
		e := &g.entries[i]
		if e.part != weakKey {
			continue
		}
		n := &e.tb.hash.nodes[e.idx]
		if v := e.value(); v != nil {
			n.key = v
		} else {
			n.val = LNil
		}
	}
	for i := range g.entries {
		e := &g.entries[i]
		if e.part == weakKey {
			continue
		}
		v := e.value()
		if ud, ok := v.(*LUserData); ok && fin != nil {
			if _, dead := fin[ud]; dead {
				v = nil
			}
		}
		if v == nil {
			continue // already LNil
		}
		if e.part == weakArray {
			e.tb.array[e.idx] = v
		} else if n := &e.tb.hash.nodes[e.idx]; n.key != collectedKey {
			n.val = v
		}
	}
	clear(g.entries)
	g.entries = g.entries[:0]
}

/* }}} */

/* finalizers: running {{{ */

// gcRunFinalizers runs the queued __gc metamethods; an error propagates
// (the userdata whose __gc failed is not finalized again).
func (ls *LState) gcRunFinalizers() {
	g := &ls.G.gc
	for len(g.pending) > 0 {
		ud := g.pending[0]
		g.pending[0] = nil
		g.pending = g.pending[1:]
		if len(g.pending) == 0 {
			g.pending = nil
		}
		ls.gcCallFinalizer(ud)
	}
	g.updateActive()
}

// gcCallFinalizer calls ud's __gc (Lua 5.1's GCTM): with the debug hooks
// off and no automatic cycle meanwhile.
func (ls *LState) gcCallFinalizer(ud *LUserData) {
	mt, ok := ud.Metatable.(*LTable)
	if !ok {
		return
	}
	tm := mt.RawGetString("__gc")
	if tm == LNil {
		return
	}
	g := &ls.G.gc
	running := ls.hook.running
	ls.hook.running = true
	g.inFin++
	defer func() {
		g.inFin--
		ls.hook.running = running
	}()
	ls.Push(tm)
	ls.Push(ud)
	ls.Call(1, 0)
}

// gcClose runs the finalizers at L.Close, as lua_close: the queued ones,
// then those of every userdata to finalize, newest first; errors are
// ignored.
func (ls *LState) gcClose() {
	g := &ls.G.gc
	if g.memLimit > 0 {
		setTableLimit(ls.G, 0)
	}
	if g.closing || (len(g.pending) == 0 && len(g.fin) == 0) {
		return
	}
	g.closing = true
	for i := len(g.fin) - 1; i >= 0; i-- {
		g.pending = append(g.pending, g.fin[i])
	}
	g.fin, g.finSet = nil, nil
	// as lua_close, the finalizers run on an emptied stack: Close may be
	// called on a state whose stack is full or mid-call (e.g. deferred after
	// an error)
	ls.stack.SetSp(0)
	ls.currentFrame = nil
	ls.reg.SetTop(0)
	top := 0
	for len(g.pending) > 0 {
		ud := g.pending[0]
		g.pending[0] = nil
		g.pending = g.pending[1:]
		ls.Push(ls.NewFunction(func(L *LState) int {
			L.gcCallFinalizer(ud)
			return 0
		}))
		_ = ls.PCall(0, 0, nil)
		ls.reg.SetTop(top)
	}
	g.pending = nil
	g.updateActive()
}

/* }}} */

/* library {{{ */

// baseCollectGarbage is collectgarbage (lbaselib.c, lua_gc): "collect"
// runs a cycle and its finalizers; "count" is the Kbytes in use, a float;
// "step" does a step of a cycle of the size of the heap (Lua 5.1's
// stepmul/100 Kbytes of work per Kbyte of its argument) and returns true
// when the step finished the cycle; "stop" and "restart" stop and restart
// the automatic cycles (Go's collector keeps running); "setpause" and
// "setstepmul" return the old value. LuaJIT 2.1 has "isrunning" too.
func baseCollectGarbage(L *LState) int {
	opt := L.OptString(1, "collect")
	switch opt {
	case "stop", "restart", "collect", "count", "step", "setpause", "setstepmul":
	case "isrunning":
		if L.Dialect() != DialectLuaJIT {
			L.ArgError(1, "invalid option '"+opt+"'")
		}
	default:
		L.ArgError(1, "invalid option '"+opt+"'")
	}
	ex := L.OptInt(2, 0)
	g := &L.G.gc
	switch opt {
	case "collect":
		L.gcCycle(true)
		L.gcRunFinalizers()
		L.Push(LNumber(0))
	case "count":
		L.Push(LNumber(float64(g.count()) / 1024))
	case "step":
		L.Push(LBool(L.gcStep(ex)))
	case "stop":
		g.stopped = true
		L.Push(LNumber(0))
	case "restart":
		g.stopped = false
		L.Push(LNumber(0))
	case "isrunning":
		L.Push(LBool(!g.stopped))
	case "setpause":
		L.Push(LNumber(L.G.gcPause))
		L.G.gcPause = ex
	case "setstepmul":
		L.Push(LNumber(L.G.gcStepMul))
		L.G.gcStepMul = ex
	}
	return 1
}

// gcStep is collectgarbage("step", kb): Lua 5.1 runs luaC_step about kb+1
// times, each doing stepmul*10 units of work; a cycle is as much work as
// the bytes in use when it started.
func (ls *LState) gcStep(kb int) bool {
	g := &ls.G.gc
	if g.stepTotal == 0 {
		g.stepTotal = max(g.count(), 1)
	}
	if kb < 0 {
		kb = 0
	}
	if mul := ls.G.gcStepMul; mul <= 0 {
		g.stepWork = g.stepTotal // no limit
	} else {
		g.stepWork += int64(kb+1) * (gcStepSize / 100) * int64(mul)
	}
	if g.stepWork < g.stepTotal {
		return false
	}
	ls.gcCycle(true)
	ls.gcRunFinalizers()
	return true
}

// baseNewProxy is Lua 5.1's newproxy: a userdata without a metatable
// (newproxy(false), newproxy()), with a new one (newproxy(true)), or with
// the metatable of a proxy made by newproxy(true) (newproxy(proxy)).
func baseNewProxy(L *LState) int {
	L.SetTop(1)
	arg := L.Get(1)
	ud := L.NewUserData()
	if !LVAsBool(arg) {
		L.Push(ud)
		return 1
	}
	g := &L.G.gc
	var mt *LTable
	if b, ok := arg.(LBool); ok && bool(b) {
		mt = L.NewTable()
		if g.proxyMts == nil {
			g.proxyMts = make(map[weak.Pointer[LTable]]struct{})
		}
		g.proxyMts[weak.Make(mt)] = struct{}{}
	} else {
		if p, ok := arg.(*LUserData); ok {
			if m, ok := p.Metatable.(*LTable); ok {
				if _, valid := g.proxyMts[weak.Make(m)]; valid {
					mt = m
				}
			}
		}
		if mt == nil {
			L.ArgError(1, "boolean or proxy expected")
		}
	}
	ud.Metatable = mt
	L.gcRegisterUserData(ud)
	L.Push(ud)
	return 1
}

// baseGcInfo is gcinfo: the Kbytes in use, an integer (lua_getgccount).
func baseGcInfo(L *LState) int {
	L.Push(LNumber(L.gcCountBytes() >> 10))
	return 1
}

/* }}} */
