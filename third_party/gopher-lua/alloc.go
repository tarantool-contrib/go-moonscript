package lua

import (
	"math"
	"unsafe"
)

// iface is an internal representation of the go-interface.
type iface struct {
	itab unsafe.Pointer
	word unsafe.Pointer
}

const preloadLimit LNumber = 128

var preloads [int(preloadLimit)]LValue

// numberItab is the itab of LNumber as an LValue (go-moonscript patch).
var numberItab unsafe.Pointer

func init() {
	for i := 0; i < int(preloadLimit); i++ {
		preloads[i] = LNumber(i)
	}
	var v LValue = LNumber(0)
	numberItab = (*iface)(unsafe.Pointer(&v)).itab
}

// allocator is a fast bulk memory allocator for the LValue.
type allocator struct {
	size  int
	fptrs []float64 // the current page (go-moonscript patch: its first idx floats are in use)
	idx   int
}

func newAllocator(size int) *allocator {
	return &allocator{size: size}
}

// LNumber2I takes a number value and returns an interface LValue representing the same number.
// Converting an LNumber to a LValue naively, by doing:
// `var val LValue = myLNumber`
// will result in an individual heap alloc of 8 bytes for the float value. LNumber2I amortizes the cost and memory
// overhead of these allocs by allocating blocks of floats instead.
// The downside of this is that all of the floats on a given block have to become eligible for gc before the block
// as a whole can be gc-ed.
func (al *allocator) LNumber2I(v LNumber) LValue {
	f := float64(v)
	// first check for shared preloaded numbers
	// (go-moonscript patch: not for -0, which would lose its sign; the
	// range is checked first, which is cheaper than the conversions)
	if f >= 0 && f < float64(preloadLimit) {
		if i := int(f); float64(i) == f && (i != 0 || math.Float64bits(f) == 0) {
			return preloads[i]
		}
	}

	// check if we need a new alloc page
	idx := al.idx
	if idx >= len(al.fptrs) {
		al.fptrs = make([]float64, al.size)
		idx = 0
	}
	// alloc a new float, and store our value into it
	fptr := &al.fptrs[idx]
	*fptr = f
	al.idx = idx + 1

	return numberIface(fptr)
}

// numberIface is the LValue of the LNumber at fptr (go-moonscript patch).
// The interface is built in a local rather than in a scratch LValue on the
// heap, which saves a write barrier.
func numberIface(fptr *float64) LValue {
	return *(*LValue)(unsafe.Pointer(&iface{numberItab, unsafe.Pointer(fptr)}))
}
