package lua

import "math"

// go-moonscript patch: math.random and math.randomseed reproduce the
// reference interpreters' sequences. Under DialectLua51 they are PUC Lua
// 5.1's (lmathlib.c over C rand()/srand(), here glibc's: the TYPE_3
// additive feedback generator of random_r, RAND_MAX 2^31-1, seed 1 until
// randomseed is called); under DialectLuaJIT they are LuaJIT 2.1's
// (lib_math.c: a Tausworthe generator with period 2^223, seeded as by
// math.randomseed(0) at start). The generator state belongs to the math
// library of the state (an upvalue of both functions; coroutines share
// it), where C's rand() is global to the process.

type glibcRand struct {
	r    [31]int32
	f, b int // indexes of fptr and rptr in r
}

// seed is glibc's __srandom_r for TYPE_3.
func (g *glibcRand) seed(seed uint32) {
	if seed == 0 {
		seed = 1
	}
	g.r[0] = int32(seed)
	word := int32(seed)
	for i := 1; i < len(g.r); i++ {
		hi := int64(word) / 127773
		lo := int64(word) % 127773
		word = int32(16807*lo - 2836*hi)
		if word < 0 {
			word += 2147483647
		}
		g.r[i] = word
	}
	g.f, g.b = 3, 0
	for i := 0; i < 10*len(g.r); i++ {
		g.next()
	}
}

// next is glibc's __random_r for TYPE_3: a number in [0, 2^31-1].
func (g *glibcRand) next() int32 {
	val := uint32(g.r[g.f]) + uint32(g.r[g.b])
	g.r[g.f] = int32(val)
	g.f++
	g.b++
	if g.f >= len(g.r) {
		g.f = 0
	} else if g.b >= len(g.r) {
		g.b = 0
	}
	return int32(val >> 1)
}

type luajitRand struct {
	gen [4]uint64
}

func (rs *luajitRand) gen1(i int, k, q, s uint, r *uint64) {
	z := rs.gen[i]
	z = (((z << q) ^ z) >> (k - s)) ^ ((z & (^uint64(0) << (64 - k))) << s)
	*r ^= z
	rs.gen[i] = z
}

// step is lj_math_random_step: a double in [1, 2) as its bit pattern.
func (rs *luajitRand) step() uint64 {
	var r uint64
	rs.gen1(0, 63, 31, 18, &r)
	rs.gen1(1, 58, 19, 28, &r)
	rs.gen1(2, 55, 24, 7, &r)
	rs.gen1(3, 47, 21, 8, &r)
	return r&0x000fffffffffffff | 0x3ff0000000000000
}

// seed is random_init of lib_math.c.
func (rs *luajitRand) seed(d float64) {
	r := uint32(0x11090601) // 64-k[i] as four 8 bit constants
	for i := 0; i < 4; i++ {
		m := uint64(1) << (r & 255)
		r >>= 8
		d = d*3.14159265358979323846 + 2.7182818284590452354
		u := math.Float64bits(d)
		if u < m {
			u += m
		}
		rs.gen[i] = u
	}
	for i := 0; i < 10; i++ {
		rs.step()
	}
}

type mathRandState struct {
	jit   bool
	glibc glibcRand
	lj    luajitRand
}

func openMathRandom(L *LState, mod *LTable) {
	st := &mathRandState{jit: L.Dialect() == DialectLuaJIT}
	if st.jit {
		st.lj.seed(0)
	} else {
		st.glibc.seed(1)
	}
	ud := L.NewUserData()
	ud.Value = st
	mod.RawSetString("random", L.NewClosure(mathRandom, ud))
	mod.RawSetString("randomseed", L.NewClosure(mathRandomseed, ud))
}

func randState(L *LState) *mathRandState {
	if ud, ok := L.Get(UpvalueIndex(1)).(*LUserData); ok {
		if st, ok := ud.Value.(*mathRandState); ok {
			return st
		}
	}
	L.RaiseError("math.random: no generator state")
	return nil
}

// cInt is luaL_checkint's conversion of a lua_Number to an int on x86-64:
// (int)(ptrdiff_t)n, the low 32 bits of cvttsd2si.
func cInt(f float64) int32 {
	return int32(cvttsd2si(f))
}

func mathRandom(L *LState) int {
	st := randState(L)
	if st.jit {
		d := math.Float64frombits(st.lj.step()) - 1.0
		if n := L.GetTop(); n > 0 {
			r1 := float64(L.CheckNumber(1))
			if n == 1 {
				d = math.Floor(d*r1) + 1.0
			} else {
				r2 := float64(L.CheckNumber(2))
				d = math.Floor(d*(r2-r1+1.0)) + r1
			}
		}
		L.Push(LNumber(d))
		return 1
	}
	// lmathlib.c math_random: the % avoids 1.0 when rand() == RAND_MAX
	r := float64(st.glibc.next()%math.MaxInt32) / float64(math.MaxInt32)
	switch L.GetTop() {
	case 0:
		L.Push(LNumber(r))
	case 1:
		u := cInt(float64(L.CheckNumber(1)))
		if u < 1 {
			L.ArgError(1, "interval is empty")
		}
		L.Push(LNumber(math.Floor(r*float64(u)) + 1))
	case 2:
		l := cInt(float64(L.CheckNumber(1)))
		u := cInt(float64(L.CheckNumber(2)))
		if l > u {
			L.ArgError(2, "interval is empty")
		}
		L.Push(LNumber(math.Floor(r*float64(u-l+1)) + float64(l))) // int arithmetic, as in C
	default:
		L.RaiseError("wrong number of arguments")
	}
	return 1
}

func mathRandomseed(L *LState) int {
	st := randState(L)
	if st.jit {
		st.lj.seed(float64(L.CheckNumber(1)))
	} else {
		st.glibc.seed(uint32(cInt(float64(L.CheckNumber(1)))))
	}
	return 0
}
