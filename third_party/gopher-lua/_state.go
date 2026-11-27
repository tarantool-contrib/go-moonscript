package lua

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/parse"
)

const MultRet = -1
const RegistryIndex = -10000
const EnvironIndex = -10001
const GlobalsIndex = -10002

/* ApiError {{{ */

type ApiError struct {
	Type       ApiErrorType
	Object     LValue
	StackTrace string
	// Underlying error. This attribute is set only if the Type is ApiErrorFile or ApiErrorSyntax
	Cause error
}

func newApiError(code ApiErrorType, object LValue) *ApiError {
	return &ApiError{code, object, "", nil}
}

func newApiErrorS(code ApiErrorType, message string) *ApiError {
	return newApiError(code, LString(message))
}

func newApiErrorE(code ApiErrorType, err error) *ApiError {
	return &ApiError{code, LString(err.Error()), "", err}
}

func (e *ApiError) Error() string {
	if len(e.StackTrace) > 0 {
		return fmt.Sprintf("%s\n%s", e.Object.String(), e.StackTrace)
	}
	return e.Object.String()
}

type ApiErrorType int

const (
	ApiErrorSyntax ApiErrorType = iota
	ApiErrorFile
	ApiErrorRun
	ApiErrorError
	ApiErrorPanic
)

/* }}} */

/* ResumeState {{{ */

type ResumeState int

const (
	ResumeOK ResumeState = iota
	ResumeYield
	ResumeError
)

/* }}} */

/* P {{{ */

type P struct {
	Fn      LValue
	NRet    int
	Protect bool
	Handler *LFunction
}

/* }}} */

/* Options {{{ */

// Options is a configuration that is used to create a new LState.
type Options struct {
	// Call stack size. This defaults to `lua.CallStackSize`.
	CallStackSize int
	// Data stack size. This defaults to `lua.RegistrySize`.
	RegistrySize int
	// Allow the registry to grow from the registry size specified up to a value of RegistryMaxSize. A value of 0
	// indicates no growth is permitted. The registry will not shrink again after any growth.
	RegistryMaxSize int
	// If growth is enabled, step up by an additional `RegistryGrowStep` each time to avoid having to resize too often.
	// This defaults to `lua.RegistryGrowStep`
	RegistryGrowStep int
	// Controls whether or not libraries are opened by default
	SkipOpenLibs bool
	// Tells whether a Go stacktrace should be included in a Lua stacktrace when panics occur.
	IncludeGoStackTrace bool
	// If `MinimizeStackMemory` is set, the call stack will be automatically grown or shrank up to a limit of
	// `CallStackSize` in order to minimize memory usage. This does incur a slight performance penalty.
	MinimizeStackMemory bool
	// Dialect is the Lua dialect the state implements: the language its
	// compiler accepts and the libraries OpenLibs opens. The zero value is
	// DialectLua51. Coroutines and NewThread states share their creator's.
	// (go-moonscript patch)
	Dialect Dialect
	// MemoryLimit bounds, in bytes, how much the state may grow Go's heap
	// (as collectgarbage("count") reports it): past it, building a string
	// or growing a table raises the Lua error "not enough memory", which
	// pcall catches, instead of exhausting the process's memory. 0: no
	// limit. See also SetMemoryLimit. (go-moonscript patch)
	MemoryLimit int64
}

// Dialect selects the Lua dialect of a state (Options.Dialect).
// (go-moonscript patch)
type Dialect int

const (
	// DialectLua51 is PUC Lua 5.1.5: what its compiler accepts, its
	// libraries and their behaviour.
	DialectLua51 Dialect = iota
	// DialectLuaJIT is LuaJIT 2.1 (built without LUAJIT_ENABLE_LUA52COMPAT):
	// Lua 5.1 plus LuaJIT's extensions to the language and the libraries,
	// such as goto, "\x"/"\u{}"/"\z" escapes and the bit library. Where
	// PUC 5.1 and LuaJIT differ in a way valid 5.1 programs can see, it
	// behaves as LuaJIT.
	DialectLuaJIT
)

// String returns "lua51" or "luajit".
func (d Dialect) String() string {
	switch d {
	case DialectLua51:
		return "lua51"
	case DialectLuaJIT:
		return "luajit"
	}
	return fmt.Sprintf("Dialect(%d)", int(d))
}

// Dialect returns the dialect of the state (Options.Dialect).
// (go-moonscript patch)
func (ls *LState) Dialect() Dialect { return ls.Options.Dialect }

/* }}} */

/* Debug {{{ */

type Debug struct {
	frame *callFrame
	Name  string
	// NameWhat is Lua 5.1's namewhat: how the function was named in the
	// call ("global", "local", "method", "field", "upvalue"), "" if Name
	// is none (go-moonscript patch)
	NameWhat string
	What     string
	// Source is the chunk's name as error messages show it (lua_Debug's
	// short_src; "=[C]" for a Go function)
	Source string
	// ChunkName is the Lua chunk name of the chunk (lua_Debug's source:
	// "@file", "=name", the source text; "=[C]" for a Go function)
	// (go-moonscript patch, chunkname.go)
	ChunkName       string
	CurrentLine     int
	NUpvalues       int
	LineDefined     int
	LastLineDefined int
}

/* }}} */

/* callFrame {{{ */

type callFrame struct {
	Idx        int
	Fn         *LFunction
	Parent     *callFrame
	Pc         int
	Base       int
	LocalBase  int
	ReturnBase int
	NArgs      int
	NRet       int
	TailCall   int
}

type callFrameStack interface {
	Push(v callFrame)
	Pop() *callFrame
	Last() *callFrame

	SetSp(sp int)
	Sp() int
	At(sp int) *callFrame

	IsFull() bool
	IsEmpty() bool

	FreeAll()

	// ClearUnused clears the frames above the top one, which would keep
	// their functions from being collected (go-moonscript patch, gc.go).
	ClearUnused()
}

type fixedCallFrameStack struct {
	array []callFrame
	sp    int
}

func newFixedCallFrameStack(size int) callFrameStack {
	return &fixedCallFrameStack{
		array: make([]callFrame, size),
		sp:    0,
	}
}

func (cs *fixedCallFrameStack) IsEmpty() bool { return cs.sp == 0 }

func (cs *fixedCallFrameStack) IsFull() bool { return cs.sp == len(cs.array) }

func (cs *fixedCallFrameStack) Clear() {
	cs.sp = 0
}

func (cs *fixedCallFrameStack) Push(v callFrame) {
	cs.array[cs.sp] = v
	cs.array[cs.sp].Idx = cs.sp
	cs.sp++
}

func (cs *fixedCallFrameStack) Sp() int {
	return cs.sp
}

func (cs *fixedCallFrameStack) SetSp(sp int) {
	cs.sp = sp
}

func (cs *fixedCallFrameStack) Last() *callFrame {
	if cs.sp == 0 {
		return nil
	}
	return &cs.array[cs.sp-1]
}

func (cs *fixedCallFrameStack) At(sp int) *callFrame {
	return &cs.array[sp]
}

func (cs *fixedCallFrameStack) Pop() *callFrame {
	cs.sp--
	return &cs.array[cs.sp]
}

func (cs *fixedCallFrameStack) FreeAll() {
	// nothing to do for fixed callframestack
}

func (cs *fixedCallFrameStack) ClearUnused() {
	clear(cs.array[cs.sp:])
}

// FramesPerSegment should be a power of 2 constant for performance reasons. It will allow the go compiler to change
// the divs and mods into bitshifts. Max is 256 due to current use of uint8 to count how many frames in a segment are
// used.
const FramesPerSegment = 8

type callFrameStackSegment struct {
	array [FramesPerSegment]callFrame
}
type segIdx uint16
type autoGrowingCallFrameStack struct {
	segments []*callFrameStackSegment
	segIdx   segIdx
	// segSp is the number of frames in the current segment which are used. Full 'sp' value is segIdx * FramesPerSegment + segSp.
	// It points to the next stack slot to use, so 0 means to use the 0th element in the segment, and a value of
	// FramesPerSegment indicates that the segment is full and cannot accommodate another frame.
	segSp uint8
}

var segmentPool sync.Pool

func newCallFrameStackSegment() *callFrameStackSegment {
	seg := segmentPool.Get()
	if seg == nil {
		return &callFrameStackSegment{}
	}
	return seg.(*callFrameStackSegment)
}

func freeCallFrameStackSegment(seg *callFrameStackSegment) {
	*seg = callFrameStackSegment{} // go-moonscript patch: no stale functions in the pool (gc.go)
	segmentPool.Put(seg)
}

// newCallFrameStack allocates a new stack for a lua state, which will auto grow up to a max size of at least maxSize.
// it will actually grow up to the next segment size multiple after maxSize, where the segment size is dictated by
// FramesPerSegment.
func newAutoGrowingCallFrameStack(maxSize int) callFrameStack {
	cs := &autoGrowingCallFrameStack{
		segments: make([]*callFrameStackSegment, (maxSize+(FramesPerSegment-1))/FramesPerSegment),
		segIdx:   0,
	}
	cs.segments[0] = newCallFrameStackSegment()
	return cs
}

func (cs *autoGrowingCallFrameStack) IsEmpty() bool {
	return cs.segIdx == 0 && cs.segSp == 0
}

// IsFull returns true if the stack cannot receive any more stack pushes without overflowing
func (cs *autoGrowingCallFrameStack) IsFull() bool {
	return int(cs.segIdx) == len(cs.segments) && cs.segSp >= FramesPerSegment
}

func (cs *autoGrowingCallFrameStack) Clear() {
	cs.freeSpare(cs.segIdx + 1) // go-moonscript patch
	for i := segIdx(1); i <= cs.segIdx; i++ {
		freeCallFrameStackSegment(cs.segments[i])
		cs.segments[i] = nil
	}
	cs.segIdx = 0
	cs.segSp = 0
}

func (cs *autoGrowingCallFrameStack) FreeAll() {
	cs.freeSpare(cs.segIdx + 1) // go-moonscript patch
	for i := segIdx(0); i <= cs.segIdx; i++ {
		freeCallFrameStackSegment(cs.segments[i])
		cs.segments[i] = nil
	}
}

// Push pushes the passed callFrame onto the stack. it panics if the stack is full, caller should call IsFull() before
// invoking this to avoid this.
func (cs *autoGrowingCallFrameStack) Push(v callFrame) {
	curSeg := cs.segments[cs.segIdx]
	if cs.segSp >= FramesPerSegment {
		// segment full, push new segment if allowed
		if cs.segIdx < segIdx(len(cs.segments)-1) {
			cs.segIdx++
			curSeg = cs.segments[cs.segIdx] // go-moonscript patch: the spare one
			if curSeg == nil {
				curSeg = newCallFrameStackSegment()
				cs.segments[cs.segIdx] = curSeg
			}
			cs.segSp = 0
		} else {
			panic("lua callstack overflow")
		}
	}
	curSeg.array[cs.segSp] = v
	curSeg.array[cs.segSp].Idx = int(cs.segSp) + FramesPerSegment*int(cs.segIdx)
	cs.segSp++
}

// Sp retrieves the current stack depth, which is the number of frames currently pushed on the stack.
func (cs *autoGrowingCallFrameStack) Sp() int {
	return int(cs.segSp) + int(cs.segIdx)*FramesPerSegment
}

// SetSp can be used to rapidly unwind the stack, freeing all stack frames on the way. It should not be used to
// allocate new stack space, use Push() for that.
func (cs *autoGrowingCallFrameStack) SetSp(sp int) {
	desiredSegIdx := segIdx(sp / FramesPerSegment)
	desiredFramesInLastSeg := uint8(sp % FramesPerSegment)
	cs.freeSpare(cs.segIdx + 1) // go-moonscript patch
	for {
		if cs.segIdx <= desiredSegIdx {
			break
		}
		freeCallFrameStackSegment(cs.segments[cs.segIdx])
		cs.segments[cs.segIdx] = nil
		cs.segIdx--
	}
	cs.segSp = desiredFramesInLastSeg
}

func (cs *autoGrowingCallFrameStack) Last() *callFrame {
	curSeg := cs.segments[cs.segIdx]
	segSp := cs.segSp
	if segSp == 0 {
		if cs.segIdx == 0 {
			return nil
		}
		curSeg = cs.segments[cs.segIdx-1]
		segSp = FramesPerSegment
	}
	return &curSeg.array[segSp-1]
}

func (cs *autoGrowingCallFrameStack) At(sp int) *callFrame {
	segIdx := segIdx(sp / FramesPerSegment)
	frameIdx := uint8(sp % FramesPerSegment)
	return &cs.segments[segIdx].array[frameIdx]
}

// Pop pops off the most recent stack frame and returns it
func (cs *autoGrowingCallFrameStack) ClearUnused() {
	if int(cs.segIdx) < len(cs.segments) && cs.segments[cs.segIdx] != nil {
		clear(cs.segments[cs.segIdx].array[cs.segSp:])
	}
	cs.freeSpare(cs.segIdx + 1) // go-moonscript patch
}

func (cs *autoGrowingCallFrameStack) Pop() *callFrame {
	curSeg := cs.segments[cs.segIdx]
	if cs.segSp == 0 {
		if cs.segIdx == 0 {
			// stack empty
			return nil
		}
		// go-moonscript patch: the segment left stays as a spare, so that
		// calls going back and forth over a segment boundary don't free and
		// allocate it each time; the one above it goes
		cs.freeSpare(cs.segIdx + 1)
		cs.segIdx--
		cs.segSp = FramesPerSegment
		curSeg = cs.segments[cs.segIdx]
	}
	cs.segSp--
	return &curSeg.array[cs.segSp]
}

// freeSpare frees the spare segment i, if any (go-moonscript patch).
func (cs *autoGrowingCallFrameStack) freeSpare(i segIdx) {
	if int(i) < len(cs.segments) && cs.segments[i] != nil {
		freeCallFrameStackSegment(cs.segments[i])
		cs.segments[i] = nil
	}
}

// pushFast pushes *cf if the current segment has room for it, returning
// the new top frame, otherwise nil (go-moonscript patch: the call path
// without the interface calls of Push and Last).
func (cs *autoGrowingCallFrameStack) pushFast(cf *callFrame) *callFrame {
	if cs.segSp >= FramesPerSegment {
		return nil
	}
	f := &cs.segments[cs.segIdx].array[cs.segSp]
	*f = *cf
	f.Idx = int(cs.segSp) + FramesPerSegment*int(cs.segIdx)
	cs.segSp++
	return f
}

// pushSlot pushes a frame if the current segment has room for it,
// returning it with only its Idx set, otherwise nil (go-moonscript patch:
// mainLoop's call path sets the fields, without copying a frame).
func (cs *autoGrowingCallFrameStack) pushSlot() *callFrame {
	if cs.segSp >= FramesPerSegment {
		return nil
	}
	f := &cs.segments[cs.segIdx].array[cs.segSp]
	f.Idx = int(cs.segSp) + FramesPerSegment*int(cs.segIdx)
	cs.segSp++
	return f
}

// popFast pops the top frame if the frame below it is in the same segment,
// returning both, otherwise nil, nil (go-moonscript patch: the return path
// without the interface calls of Pop and Last).
func (cs *autoGrowingCallFrameStack) popFast() (popped, top *callFrame) {
	if cs.segSp < 2 {
		return nil, nil
	}
	seg := cs.segments[cs.segIdx]
	cs.segSp--
	return &seg.array[cs.segSp], &seg.array[cs.segSp-1]
}

/* }}} */

/* registry {{{ */

type registryHandler interface {
	registryOverflow()
}
type registry struct {
	array   []LValue
	top     int
	growBy  int
	maxSize int
	alloc   *allocator
	handler registryHandler
}

func newRegistry(handler registryHandler, initialSize int, growBy int, maxSize int, alloc *allocator) *registry {
	return &registry{make([]LValue, initialSize), 0, growBy, maxSize, alloc, handler}
}

func (rg *registry) checkSize(requiredSize int) { // +inline-start
	if requiredSize > cap(rg.array) {
		rg.resize(requiredSize)
	}
} // +inline-end

func (rg *registry) resize(requiredSize int) { // +inline-start
	newSize := requiredSize + rg.growBy // give some padding
	if newSize > rg.maxSize {
		newSize = rg.maxSize
	}
	if newSize < requiredSize {
		rg.handler.registryOverflow()
		return
	}
	rg.forceResize(newSize)
} // +inline-end

func (rg *registry) forceResize(newSize int) {
	newSlice := make([]LValue, newSize)
	copy(newSlice, rg.array[:rg.top]) // should we copy the area beyond top? there shouldn't be any valid values there so it shouldn't be necessary.
	rg.array = newSlice
}

func (rg *registry) SetTop(topi int) { // +inline-start
	// +inline-call rg.checkSize topi
	oldtopi := rg.top
	rg.top = topi
	for i := oldtopi; i < rg.top; i++ {
		rg.array[i] = LNil
	}
	// values beyond top don't need to be valid LValues, so setting them to nil is fine
	// setting them to nil rather than LNil lets us invoke the golang memclr opto
	if rg.top < oldtopi {
		nilRange := rg.array[rg.top:oldtopi]
		for i := range nilRange {
			nilRange[i] = nil
		}
	}
	//for i := rg.top; i < oldtop; i++ {
	//	rg.array[i] = LNil
	//}
} // +inline-end

func (rg *registry) Top() int {
	return rg.top
}

func (rg *registry) Push(v LValue) {
	newSize := rg.top + 1
	// +inline-call rg.checkSize newSize
	rg.array[rg.top] = v
	rg.top++
}

func (rg *registry) Pop() LValue {
	v := rg.array[rg.top-1]
	rg.array[rg.top-1] = LNil
	rg.top--
	return v
}

func (rg *registry) Get(reg int) LValue {
	return rg.array[reg]
}

// CopyRange will move a section of values from index `start` to index `regv`
// It will move `n` values.
// `limit` specifies the maximum end range that can be copied from. If it's set to -1, then it defaults to stopping at
// the top of the registry (values beyond the top are not initialized, so if specifying an alternative `limit` you should
// pass a value <= rg.top.
// If start+n is beyond the limit, then nil values will be copied to the destination slots.
// After the copy, the registry is truncated to be at the end of the copied range, ie the original of the copied values
// are nilled out. (So top will be regv+n)
// CopyRange should ideally be renamed to MoveRange.
func (rg *registry) CopyRange(regv, start, limit, n int) { // +inline-start
	newSize := regv + n
	// +inline-call rg.checkSize newSize
	if limit == -1 || limit > rg.top {
		limit = rg.top
	}
	for i := 0; i < n; i++ {
		srcIdx := start + i
		if srcIdx >= limit || srcIdx < 0 {
			rg.array[regv+i] = LNil
		} else {
			rg.array[regv+i] = rg.array[srcIdx]
		}
	}

	// values beyond top don't need to be valid LValues, so setting them to nil is fine
	// setting them to nil rather than LNil lets us invoke the golang memclr opto
	oldtop := rg.top
	rg.top = regv + n
	if rg.top < oldtop {
		nilRange := rg.array[rg.top:oldtop]
		for i := range nilRange {
			nilRange[i] = nil
		}
	}
} // +inline-end

// FillNil fills the registry with nil values from regm to regm+n and then sets the registry top to regm+n
func (rg *registry) FillNil(regm, n int) { // +inline-start
	newSize := regm + n
	// +inline-call rg.checkSize newSize
	for i := 0; i < n; i++ {
		rg.array[regm+i] = LNil
	}
	// values beyond top don't need to be valid LValues, so setting them to nil is fine
	// setting them to nil rather than LNil lets us invoke the golang memclr opto
	oldtop := rg.top
	rg.top = regm + n
	if rg.top < oldtop {
		nilRange := rg.array[rg.top:oldtop]
		for i := range nilRange {
			nilRange[i] = nil
		}
	}
} // +inline-end

func (rg *registry) Insert(value LValue, reg int) {
	top := rg.Top()
	if reg >= top {
		// +inline-call rg.Set reg value
		return
	}
	top--
	for ; top >= reg; top-- {
		// FIXME consider using copy() here if Insert() is called enough
		// +inline-call rg.Set top+1 rg.Get(top)
	}
	// +inline-call rg.Set reg value
}

func (rg *registry) Set(regi int, vali LValue) { // +inline-start
	newSize := regi + 1
	// +inline-call rg.checkSize newSize
	rg.array[regi] = vali
	if regi >= rg.top {
		rg.top = regi + 1
	}
} // +inline-end

func (rg *registry) SetNumber(regi int, vali LNumber) { // +inline-start
	newSize := regi + 1
	// +inline-call rg.checkSize newSize
	rg.array[regi] = rg.alloc.LNumber2I(vali)
	if regi >= rg.top {
		rg.top = regi + 1
	}
} // +inline-end

func (rg *registry) IsFull() bool {
	return rg.top >= cap(rg.array)
}

/* }}} */

/* Global {{{ */

func newGlobal() *Global {
	return &Global{
		MainThread: nil,
		Registry:   newLTable(0, 32),
		Global:     newLTable(0, 64),
		builtinMts: make(map[int]LValue),
		tempFiles:  make([]*os.File, 0, 10),
		gcPause:    200, // go-moonscript patch: Lua 5.1's defaults
		gcStepMul:  200,
	}
}

/* }}} */

/* package local methods {{{ */

func panicWithTraceback(L *LState) {
	err := newApiError(ApiErrorRun, L.Get(-1))
	err.StackTrace = L.stackTrace(0)
	panic(err)
}

func panicWithoutTraceback(L *LState) {
	err := newApiError(ApiErrorRun, L.Get(-1))
	panic(err)
}

func newLState(options Options) *LState {
	al := newAllocator(512) // go-moonscript patch: 4 KB pages, not 256 bytes
	ls := &LState{
		G:       newGlobal(),
		Parent:  nil,
		Panic:   panicWithTraceback,
		Dead:    false,
		Options: options,

		stop:         0,
		alloc:        al,
		currentFrame: nil,
		wrapped:      false,
		uvcache:      nil,
		hasErrorFunc: false,
		mainLoop:     mainLoop,
		ctx:          nil,
	}
	if options.MinimizeStackMemory {
		ls.stack = newAutoGrowingCallFrameStack(options.CallStackSize)
	} else {
		ls.stack = newFixedCallFrameStack(options.CallStackSize)
	}
	ls.reg = newRegistry(ls, options.RegistrySize, options.RegistryGrowStep, options.RegistryMaxSize, al)
	ls.Env = ls.G.Global
	return ls
}

func (ls *LState) printReg() {
	println("-------------------------")
	println("thread:", ls)
	println("top:", ls.reg.Top())
	if ls.currentFrame != nil {
		println("function base:", ls.currentFrame.Base)
		println("return base:", ls.currentFrame.ReturnBase)
	} else {
		println("(vm not started)")
	}
	println("local base:", ls.currentLocalBase())
	for i := 0; i < ls.reg.Top(); i++ {
		println(i, ls.reg.Get(i).String())
	}
	println("-------------------------")
}

func (ls *LState) printCallStack() {
	println("-------------------------")
	for i := 0; i < ls.stack.Sp(); i++ {
		print(i)
		print(" ")
		frame := ls.stack.At(i)
		if frame == nil {
			break
		}
		if frame.Fn.IsG {
			println("IsG:", true, "Frame:", frame, "Fn:", frame.Fn)
		} else {
			println("IsG:", false, "Frame:", frame, "Fn:", frame.Fn, "pc:", frame.Pc)
		}
	}
	println("-------------------------")
}

func (ls *LState) closeAllUpvalues() { // +inline-start
	for cf := ls.currentFrame; cf != nil; cf = cf.Parent {
		if !cf.Fn.IsG {
			ls.closeUpvalues(cf.LocalBase)
		}
	}
} // +inline-end

func (ls *LState) raiseError(level int, format string, args ...interface{}) {
	// go-moonscript patch: open upvalues are closed by PCall (and threadRun)
	// for the frames the error discards only; closing every frame's upvalues
	// here detached closures from the locals of frames that survive the
	// error.
	message := format
	if len(args) > 0 {
		message = fmt.Sprintf(format, args...)
	}
	// go-moonscript patch: the position as Lua 5.1 adds it: the running
	// function's when it is a Lua function (luaG_runerror), else its
	// caller's (luaL_error); none when that isn't a Lua function, and no
	// space before a message without position
	if level > 0 {
		var pos string
		if cf := ls.currentFrame; cf == nil || cf.Fn.IsG {
			pos = ls.luaWhere(1)
		} else {
			pos = ls.where(0, false)
		}
		if pos != "" {
			message = pos + " " + message
		}
	}
	if ls.reg.IsFull() {
		// if the registry is full then it won't be possible to push a value, in this case, force a larger size
		ls.reg.forceResize(ls.reg.Top() + 1)
	}
	ls.reg.Push(LString(message))
	ls.Panic(ls)
}

func (ls *LState) findLocal(frame *callFrame, no int) string {
	fn := frame.Fn
	if !fn.IsG {
		if name, ok := fn.LocalName(no, frame.Pc-1); ok {
			return name
		}
	}
	var top int
	if ls.currentFrame == frame {
		top = ls.reg.Top()
	} else if frame.Idx+1 < ls.stack.Sp() {
		top = ls.stack.At(frame.Idx + 1).Base
	} else {
		return ""
	}
	if top-frame.LocalBase >= no {
		return "(*temporary)"
	}
	return ""
}

// luaWhere is luaL_where without the trailing space (go-moonscript
// patch): "chunkname:currentline:" for the function at level when it is
// a Lua function with a current line, else "".
func (ls *LState) luaWhere(level int) string {
	if ls.isTailCallLevel(level) {
		return ""
	}
	dbg, ok := ls.GetStack(level)
	if !ok {
		return ""
	}
	cf := dbg.frame
	if cf.Fn.IsG || cf.Pc <= 0 {
		return ""
	}
	return fmt.Sprintf("%v:%v:", cf.Fn.Proto.SourceName, cf.Fn.Proto.DbgSourcePositions[cf.Pc-1])
}

func (ls *LState) where(level int, skipg bool) string {
	dbg, ok := ls.GetStack(level)
	if !ok {
		return ""
	}
	cf := dbg.frame
	proto := cf.Fn.Proto
	sourcename := "[G]"
	if proto != nil {
		sourcename = proto.SourceName
	} else if skipg {
		return ls.where(level+1, skipg)
	}
	line := ""
	// go-moonscript patch: no line before the frame's first instruction (a
	// tail call's frame when the registry overflows as it is set up: the
	// line of instruction -1 was a Go panic, yuin/gopher-lua#282)
	if proto != nil && cf.Pc > 0 {
		line = fmt.Sprintf("%v:", proto.DbgSourcePositions[cf.Pc-1])
	}
	return fmt.Sprintf("%v:%v", sourcename, line)
}

func (ls *LState) stackTrace(level int) string {
	buf := []string{}
	header := "stack traceback:"
	if ls.currentFrame != nil {
		i := 0
		for dbg, ok := ls.GetStack(i); ok; dbg, ok = ls.GetStack(i) {
			cf := dbg.frame
			buf = append(buf, fmt.Sprintf("\t%v in %v", ls.Where(i), ls.formattedFrameFuncName(cf)))
			if !cf.Fn.IsG && cf.TailCall > 0 {
				for tc := cf.TailCall; tc > 0; tc-- {
					buf = append(buf, "\t(tailcall): ?")
					i++
				}
			}
			i++
		}
	}
	buf = append(buf, fmt.Sprintf("\t%v: %v", "[G]", "?"))
	buf = buf[intMax(0, intMin(level, len(buf))):len(buf)]
	if len(buf) > 20 {
		newbuf := make([]string, 0, 20)
		newbuf = append(newbuf, buf[0:7]...)
		newbuf = append(newbuf, "\t...")
		newbuf = append(newbuf, buf[len(buf)-7:len(buf)]...)
		buf = newbuf
	}
	return fmt.Sprintf("%s\n%s", header, strings.Join(buf, "\n"))
}

func (ls *LState) formattedFrameFuncName(fr *callFrame) string {
	name, ischunk := ls.frameFuncName(fr)
	if ischunk {
		return name
	}
	if name[0] != '(' && name[0] != '<' {
		return fmt.Sprintf("function '%s'", name)
	}
	return fmt.Sprintf("function %s", name)
}

func (ls *LState) frameFuncName(fr *callFrame) (string, bool) {
	frame := fr.Parent
	if frame == nil {
		if ls.Parent == nil {
			return "main chunk", true
		} else {
			return "corountine", true
		}
	}
	if !frame.Fn.IsG {
		pc := frame.Pc - 1
		for _, call := range frame.Fn.Proto.DbgCalls {
			if call.Pc == pc {
				name := call.Name
				if (name == "?" || fr.TailCall > 0) && !fr.Fn.IsG {
					name = fmt.Sprintf("<%v:%v>", fr.Fn.Proto.SourceName, fr.Fn.Proto.LineDefined)
				}
				return name, false
			}
		}
	}
	if !fr.Fn.IsG {
		return fmt.Sprintf("<%v:%v>", fr.Fn.Proto.SourceName, fr.Fn.Proto.LineDefined), false
	}
	return "(anonymous)", false
}

func (ls *LState) isStarted() bool {
	return ls.currentFrame != nil || ls.yieldNRet == yieldedOut // go-moonscript patch
}

func (ls *LState) kill() {
	ls.Dead = true
	if ls.ctxCancelFn != nil {
		ls.ctxCancelFn()
	}
}

func (ls *LState) indexToReg(idx int) int {
	base := ls.currentLocalBase()
	if idx > 0 {
		return base + idx - 1
	} else if idx == 0 {
		return -1
	} else {
		tidx := ls.reg.Top() + idx
		if tidx < base {
			return -1
		}
		return tidx
	}
}

func (ls *LState) currentLocalBase() int {
	base := 0
	if ls.currentFrame != nil {
		base = ls.currentFrame.LocalBase
	}
	return base
}

func (ls *LState) currentEnv() *LTable {
	return ls.Env
	/*
		if ls.currentFrame == nil {
			return ls.Env
		}
		return ls.currentFrame.Fn.Env
	*/
}

func (ls *LState) rkValue(idx int) LValue {
	/*
		if OpIsK(idx) {
			return ls.currentFrame.Fn.Proto.Constants[opIndexK(idx)]
		}
		return ls.reg.Get(ls.currentFrame.LocalBase + idx)
	*/
	if (idx & opBitRk) != 0 {
		return ls.currentFrame.Fn.Proto.Constants[idx & ^opBitRk]
	}
	return ls.reg.array[ls.currentFrame.LocalBase+idx]
}

func (ls *LState) rkString(idx int) string {
	if (idx & opBitRk) != 0 {
		return ls.currentFrame.Fn.Proto.stringConstants[idx & ^opBitRk]
	}
	return string(ls.reg.array[ls.currentFrame.LocalBase+idx].(LString))
}

// rkStringK is rkString, also returning the key as an LValue and its
// hashString (go-moonscript patch).
func (ls *LState) rkStringK(idx int) (LValue, string, uint32) {
	if (idx & opBitRk) != 0 {
		proto := ls.currentFrame.Fn.Proto
		k := idx & ^opBitRk
		return proto.Constants[k], proto.stringConstants[k], proto.strConstHash(k)
	}
	lv := ls.reg.array[ls.currentFrame.LocalBase+idx]
	s := string(lv.(LString))
	return lv, s, hashString(s)
}

// go-moonscript patch: the open upvalues (uvcache) are listed from the
// highest stack slot down, as Lua's openupval, so that closing the ones
// of a returning function stops at the first one below it (it went over
// the whole list, all the open upvalues of the thread, on each return)
// and a closure finds the upvalues of the top frame first.

func (ls *LState) closeUpvalues(idx int) { // +inline-start
	for uv := ls.uvcache; uv != nil && uv.index >= idx; uv = ls.uvcache {
		ls.uvcache = uv.next
		uv.next = nil
		uv.Close()
	}
} // +inline-end

func (ls *LState) findUpvalue(idx int) *Upvalue {
	var prev *Upvalue
	uv := ls.uvcache
	for ; uv != nil && uv.index > idx; uv = uv.next {
		prev = uv
	}
	if uv != nil && uv.index == idx {
		return uv
	}
	nuv := &Upvalue{reg: ls.reg, index: idx, next: uv}
	if prev != nil {
		prev.next = nuv
	} else {
		ls.uvcache = nuv
	}
	return nuv
}

func (ls *LState) metatable(lvalue LValue, rawget bool) LValue {
	var metatable LValue = LNil
	switch obj := lvalue.(type) {
	case *LTable:
		metatable = obj.Metatable
	case *LUserData:
		metatable = obj.Metatable
	default:
		if table, ok := ls.G.builtinMts[int(obj.Type())]; ok {
			metatable = table
		}
	}

	if !rawget && metatable != LNil {
		oldmt := metatable
		if tb, ok := metatable.(*LTable); ok {
			metatable = tb.RawGetString("__metatable")
			if metatable == LNil {
				metatable = oldmt
			}
		}
	}

	return metatable
}

func (ls *LState) metaOp1(lvalue LValue, event string) LValue {
	if mt := ls.metatable(lvalue, true); mt != LNil {
		if tb, ok := mt.(*LTable); ok {
			return tb.RawGetString(event)
		}
	}
	return LNil
}

// hashIndex and hashNewIndex are the hashString of "__index" and
// "__newindex" (go-moonscript patch).
var (
	hashIndex    = hashString("__index")
	hashNewIndex = hashString("__newindex")
)

// metaOp1H is metaOp1 for an event whose hashString is h (go-moonscript
// patch).
func (ls *LState) metaOp1H(lvalue LValue, event string, h uint32) LValue {
	var mt LValue
	switch obj := lvalue.(type) {
	case *LTable:
		mt = obj.Metatable
	case *LUserData:
		mt = obj.Metatable
	default:
		mt = ls.metatable(lvalue, true)
	}
	if tb, ok := mt.(*LTable); ok {
		return tb.hash.getStringH(event, h)
	}
	return LNil
}

func (ls *LState) metaOp2(value1, value2 LValue, event string) LValue {
	if mt := ls.metatable(value1, true); mt != LNil {
		if tb, ok := mt.(*LTable); ok {
			if ret := tb.RawGetString(event); ret != LNil {
				return ret
			}
		}
	}
	if mt := ls.metatable(value2, true); mt != LNil {
		if tb, ok := mt.(*LTable); ok {
			return tb.RawGetString(event)
		}
	}
	return LNil
}

func (ls *LState) metaCall(lvalue LValue) (*LFunction, bool) {
	if fn, ok := lvalue.(*LFunction); ok {
		return fn, false
	}
	if fn, ok := ls.metaOp1(lvalue, "__call").(*LFunction); ok {
		return fn, true
	}
	return nil, false
}

func (ls *LState) initCallFrame(cf *callFrame) { // +inline-start
	if cf.Fn.IsG {
		ls.reg.SetTop(cf.LocalBase + cf.NArgs)
	} else {
		proto := cf.Fn.Proto
		nargs := cf.NArgs
		np := int(proto.NumParameters)
		if nargs < np {
			// default any missing arguments to nil
			newSize := cf.LocalBase + np
			// +inline-call ls.reg.checkSize newSize
			for i := nargs; i < np; i++ {
				ls.reg.array[cf.LocalBase+i] = LNil
			}
			nargs = np
			ls.reg.top = newSize
		}

		if (proto.IsVarArg & VarArgIsVarArg) == 0 {
			if nargs < int(proto.NumUsedRegisters) {
				nargs = int(proto.NumUsedRegisters)
			}
			newSize := cf.LocalBase + nargs
			// +inline-call ls.reg.checkSize newSize
			for i := np; i < nargs; i++ {
				ls.reg.array[cf.LocalBase+i] = LNil
			}
			ls.reg.top = cf.LocalBase + int(proto.NumUsedRegisters)
		} else {
			/* swap vararg positions:
					   closure
					   namedparam1 <- lbase
					   namedparam2
					   vararg1
					   vararg2

			           TO

					   closure
					   nil
					   nil
					   vararg1
					   vararg2
					   namedparam1 <- lbase
					   namedparam2
			*/
			nvarargs := nargs - np
			if nvarargs < 0 {
				nvarargs = 0
			}

			ls.reg.SetTop(cf.LocalBase + nargs + np)
			for i := 0; i < np; i++ {
				//ls.reg.Set(cf.LocalBase+nargs+i, ls.reg.Get(cf.LocalBase+i))
				ls.reg.array[cf.LocalBase+nargs+i] = ls.reg.array[cf.LocalBase+i]
				//ls.reg.Set(cf.LocalBase+i, LNil)
				ls.reg.array[cf.LocalBase+i] = LNil
			}

			if CompatVarArg {
				ls.reg.SetTop(cf.LocalBase + nargs + np + 1)
				if (proto.IsVarArg & VarArgNeedsArg) != 0 {
					argtb := newLTable(nvarargs, 1) // go-moonscript patch: as luaH_new(L, nvar, 1)
					for i := 0; i < nvarargs; i++ {
						argtb.RawSetInt(i+1, ls.reg.Get(cf.LocalBase+np+i))
					}
					argtb.RawSetString("n", LNumber(nvarargs))
					//ls.reg.Set(cf.LocalBase+nargs+np, argtb)
					ls.reg.array[cf.LocalBase+nargs+np] = argtb
				} else {
					ls.reg.array[cf.LocalBase+nargs+np] = LNil
				}
			}
			cf.LocalBase += nargs
			maxreg := cf.LocalBase + int(proto.NumUsedRegisters)
			ls.reg.SetTop(maxreg)
		}
	}
} // +inline-end

func (ls *LState) pushCallFrame(cf callFrame, fn LValue, meta bool) { // +inline-start
	if meta {
		cf.NArgs++
		ls.reg.Insert(fn, cf.LocalBase)
	}
	if cf.Fn == nil {
		ls.callError(fn, cf.Base) // go-moonscript patch: Lua 5.1's message
	}
	var newcf *callFrame
	if s, ok := ls.stack.(*autoGrowingCallFrameStack); ok {
		newcf = s.pushFast(&cf) // go-moonscript patch
	}
	if newcf == nil {
		if ls.stack.IsFull() {
			ls.RaiseError("stack overflow")
		}
		ls.stack.Push(cf)
		newcf = ls.stack.Last()
	}
	// +inline-call ls.initCallFrame newcf
	ls.currentFrame = newcf
	if ls.hookMask&MaskCall != 0 && !newcf.Fn.IsG { // go-moonscript patch
		ls.hookCall()
	}
} // +inline-end

func (ls *LState) callR(nargs, nret, rbase int) {
	base := ls.reg.Top() - nargs - 1
	if rbase < 0 {
		rbase = base
	}
	lv := ls.reg.Get(base)
	fn, meta := ls.metaCall(lv)
	ls.pushCallFrame(callFrame{
		Fn:         fn,
		Pc:         0,
		Base:       base,
		LocalBase:  base + 1,
		ReturnBase: rbase,
		NArgs:      nargs,
		NRet:       nret,
		Parent:     ls.currentFrame,
		TailCall:   0,
	}, lv, meta)
	ls.nCcalls++ // go-moonscript patch: a coroutine can't yield across this call
	if ls.G.MainThread == nil {
		ls.G.MainThread = ls
		ls.G.CurrentThread = ls
		ls.mainLoop(ls, nil)
	} else if ls.currentFrame.Fn.IsG {
		// go-moonscript patch: what mainLoop does for a Go function, without
		// entering it (iterators of generic for loops, metamethods)
		callGFunction(ls, false)
	} else {
		ls.mainLoop(ls, ls.currentFrame)
	}
	ls.nCcalls--
	if nret != MultRet {
		ls.reg.SetTop(rbase + nret)
	}
}

func (ls *LState) getField(obj LValue, key LValue) LValue {
	curobj := obj
	for i := 0; i < MaxTableGetLoop; i++ {
		tb, istable := curobj.(*LTable)
		if istable {
			ret := tb.RawGet(key)
			if ret != LNil || tb.Metatable == LNil { // go-moonscript patch
				return ret
			}
		}
		metaindex := ls.metaOp1H(curobj, "__index", hashIndex) // go-moonscript patch: hashed once
		if metaindex == LNil {
			if !istable {
				ls.indexError(curobj, i == 0) // go-moonscript patch: Lua 5.1's message
			}
			return LNil
		}
		if metaindex.Type() == LTFunction {
			ls.reg.Push(metaindex)
			ls.reg.Push(curobj)
			ls.reg.Push(key)
			ls.Call(2, 1)
			return ls.reg.Pop()
		} else {
			curobj = metaindex
		}
	}
	ls.RaiseError("loop in gettable") // go-moonscript patch: Lua 5.1's message
	return nil
}

func (ls *LState) getFieldString(obj LValue, key string) LValue {
	curobj := obj
	for i := 0; i < MaxTableGetLoop; i++ {
		tb, istable := curobj.(*LTable)
		if istable {
			ret := tb.RawGetString(key)
			if ret != LNil || tb.Metatable == LNil { // go-moonscript patch
				return ret
			}
		}
		metaindex := ls.metaOp1H(curobj, "__index", hashIndex) // go-moonscript patch: hashed once
		if metaindex == LNil {
			if !istable {
				ls.indexError(curobj, i == 0) // go-moonscript patch: Lua 5.1's message
			}
			return LNil
		}
		if metaindex.Type() == LTFunction {
			ls.reg.Push(metaindex)
			ls.reg.Push(curobj)
			ls.reg.Push(LString(key))
			ls.Call(2, 1)
			return ls.reg.Pop()
		} else {
			curobj = metaindex
		}
	}
	ls.RaiseError("loop in gettable") // go-moonscript patch: Lua 5.1's message
	return nil
}

// getFieldStringK is getFieldString for the key skey, lkey as an LValue,
// whose hashString is h (go-moonscript patch: for the VM, which knows the
// hash of constant keys, and doesn't need to box the key again).
func (ls *LState) getFieldStringK(obj LValue, lkey LValue, skey string, h uint32) LValue {
	return ls.getFieldStringKMiss(obj, lkey, skey, h, false)
}

// getFieldStringKMiss is getFieldStringK; missed: obj is a table that has
// been looked up and lacks the key (go-moonscript patch).
func (ls *LState) getFieldStringKMiss(obj LValue, lkey LValue, skey string, h uint32, missed bool) LValue {
	curobj := obj
	for i := 0; i < MaxTableGetLoop; i++ {
		tb, istable := curobj.(*LTable)
		if istable && missed {
			missed = false
		} else if istable {
			ret := tb.hash.getStringH(skey, h)
			if ret != LNil || tb.Metatable == LNil { // go-moonscript patch
				return ret
			}
		}
		metaindex := ls.metaOp1H(curobj, "__index", hashIndex) // go-moonscript patch: hashed once
		if metaindex == LNil {
			if !istable {
				ls.indexError(curobj, i == 0) // go-moonscript patch: Lua 5.1's message
			}
			return LNil
		}
		if metaindex.Type() == LTFunction {
			ls.reg.Push(metaindex)
			ls.reg.Push(curobj)
			ls.reg.Push(lkey)
			ls.Call(2, 1)
			return ls.reg.Pop()
		} else {
			curobj = metaindex
		}
	}
	ls.RaiseError("loop in gettable") // go-moonscript patch: Lua 5.1's message
	return nil
}

// setFieldStringK is setFieldString for the key skey, lkey as an LValue,
// whose hashString is h (go-moonscript patch).
func (ls *LState) setFieldStringK(obj LValue, lkey LValue, skey string, h uint32, value LValue) {
	curobj := obj
	for i := 0; i < MaxTableGetLoop; i++ {
		tb, istable := curobj.(*LTable)
		j := int32(-1)
		if istable {
			if tb.Frozen() { // go-moonscript patch: before __newindex
				ls.frozenTableError()
			}
			// go-moonscript patch: one lookup for the key
			j = tb.hash.findString(skey, h)
			if j >= 0 && tb.hash.nodes[j].val != LNil || tb.Metatable == LNil {
				tb.setStringAt(j, lkey, skey, h, value)
				return
			}
		}
		metaindex := ls.metaOp1H(curobj, "__newindex", hashNewIndex) // go-moonscript patch: hashed once
		if metaindex == LNil {
			if !istable {
				ls.indexError(curobj, i == 0) // go-moonscript patch: Lua 5.1's message
			}
			tb.setStringAt(j, lkey, skey, h, value)
			return
		}
		if metaindex.Type() == LTFunction {
			ls.reg.Push(metaindex)
			ls.reg.Push(curobj)
			ls.reg.Push(lkey)
			ls.reg.Push(value)
			ls.Call(3, 0)
			return
		} else {
			curobj = metaindex
		}
	}
	ls.RaiseError("loop in settable") // go-moonscript patch: Lua 5.1's message
}

func (ls *LState) setField(obj LValue, key LValue, value LValue) {
	curobj := obj
	for i := 0; i < MaxTableGetLoop; i++ {
		tb, istable := curobj.(*LTable)
		if istable {
			if tb.Frozen() { // go-moonscript patch: before __newindex
				ls.frozenTableError()
			}
			if tb.Metatable == LNil || tb.RawGet(key) != LNil { // go-moonscript patch
				ls.RawSet(tb, key, value)
				return
			}
		}
		metaindex := ls.metaOp1H(curobj, "__newindex", hashNewIndex) // go-moonscript patch: hashed once
		if metaindex == LNil {
			if !istable {
				ls.indexError(curobj, i == 0) // go-moonscript patch: Lua 5.1's message
			}
			ls.RawSet(tb, key, value)
			return
		}
		if metaindex.Type() == LTFunction {
			ls.reg.Push(metaindex)
			ls.reg.Push(curobj)
			ls.reg.Push(key)
			ls.reg.Push(value)
			ls.Call(3, 0)
			return
		} else {
			curobj = metaindex
		}
	}
	ls.RaiseError("loop in settable") // go-moonscript patch: Lua 5.1's message
}

func (ls *LState) setFieldString(obj LValue, key string, value LValue) {
	curobj := obj
	for i := 0; i < MaxTableGetLoop; i++ {
		tb, istable := curobj.(*LTable)
		if istable {
			if tb.Frozen() { // go-moonscript patch: before __newindex
				ls.frozenTableError()
			}
			if tb.Metatable == LNil || tb.RawGetString(key) != LNil { // go-moonscript patch
				tb.RawSetString(key, value)
				return
			}
		}
		metaindex := ls.metaOp1H(curobj, "__newindex", hashNewIndex) // go-moonscript patch: hashed once
		if metaindex == LNil {
			if !istable {
				ls.indexError(curobj, i == 0) // go-moonscript patch: Lua 5.1's message
			}
			tb.RawSetString(key, value)
			return
		}
		if metaindex.Type() == LTFunction {
			ls.reg.Push(metaindex)
			ls.reg.Push(curobj)
			ls.reg.Push(LString(key))
			ls.reg.Push(value)
			ls.Call(3, 0)
			return
		} else {
			curobj = metaindex
		}
	}
	ls.RaiseError("loop in settable") // go-moonscript patch: Lua 5.1's message
}

/* }}} */

/* api methods {{{ */

func NewState(opts ...Options) *LState {
	var ls *LState
	if len(opts) == 0 {
		ls = newLState(Options{
			CallStackSize: CallStackSize,
			RegistrySize:  RegistrySize,
		})
		ls.G.gc.init() // go-moonscript patch
		ls.OpenLibs()
	} else {
		if opts[0].CallStackSize < 1 {
			opts[0].CallStackSize = CallStackSize
		}
		if opts[0].RegistrySize < 128 {
			opts[0].RegistrySize = RegistrySize
		}
		if opts[0].RegistryMaxSize < opts[0].RegistrySize {
			opts[0].RegistryMaxSize = 0 // disable growth if max size is smaller than initial size
		} else {
			// if growth enabled, grow step is set
			if opts[0].RegistryGrowStep < 1 {
				opts[0].RegistryGrowStep = RegistryGrowStep
			}
		}
		ls = newLState(opts[0])
		ls.G.gc.init() // go-moonscript patch
		if opts[0].MemoryLimit > 0 {
			ls.SetMemoryLimit(opts[0].MemoryLimit)
		}
		if !opts[0].SkipOpenLibs {
			ls.OpenLibs()
		}
	}
	return ls
}

func (ls *LState) IsClosed() bool {
	return ls.stack == nil
}

func (ls *LState) Close() {
	// go-moonscript patch: the __gc metamethods run first, as in lua_close
	if ls.stack != nil && ls.Parent == nil && (ls.G.MainThread == nil || ls.G.MainThread == ls) {
		ls.gcClose()
	}
	atomic.AddInt32(&ls.stop, 1)
	ls.closeIO() // go-moonscript patch: flush and close the io library's files (iolib.go)
	for _, file := range ls.G.tempFiles {
		// ignore errors in these operations
		file.Close()
		os.Remove(file.Name())
	}
	ls.stack.FreeAll()
	ls.stack = nil
}

/* registry operations {{{ */

func (ls *LState) GetTop() int {
	return ls.reg.Top() - ls.currentLocalBase()
}

func (ls *LState) SetTop(idx int) {
	base := ls.currentLocalBase()
	newtop := ls.indexToReg(idx) + 1
	if newtop < base {
		ls.reg.SetTop(base)
	} else {
		ls.reg.SetTop(newtop)
	}
}

func (ls *LState) Replace(idx int, value LValue) {
	base := ls.currentLocalBase()
	if idx > 0 {
		reg := base + idx - 1
		if reg < ls.reg.Top() {
			ls.reg.Set(reg, value)
		}
	} else if idx == 0 {
	} else if idx > RegistryIndex {
		if tidx := ls.reg.Top() + idx; tidx >= base {
			ls.reg.Set(tidx, value)
		}
	} else {
		switch idx {
		case RegistryIndex:
			if tb, ok := value.(*LTable); ok {
				ls.G.Registry = tb
			} else {
				ls.RaiseError("registry must be a table(%v)", value.Type().String())
			}
		case EnvironIndex:
			if ls.currentFrame == nil {
				ls.RaiseError("no calling environment")
			}
			if tb, ok := value.(*LTable); ok {
				ls.currentFrame.Fn.Env = tb
			} else {
				ls.RaiseError("environment must be a table(%v)", value.Type().String())
			}
		case GlobalsIndex:
			if tb, ok := value.(*LTable); ok {
				ls.G.Global = tb
			} else {
				ls.RaiseError("_G must be a table(%v)", value.Type().String())
			}
		default:
			fn := ls.currentFrame.Fn
			index := GlobalsIndex - idx - 1
			if index < len(fn.Upvalues) {
				fn.Upvalues[index].SetValue(value)
			}
		}
	}
}

func (ls *LState) Get(idx int) LValue {
	base := ls.currentLocalBase()
	if idx > 0 {
		reg := base + idx - 1
		if reg < ls.reg.Top() {
			return ls.reg.Get(reg)
		}
		return LNil
	} else if idx == 0 {
		return LNil
	} else if idx > RegistryIndex {
		tidx := ls.reg.Top() + idx
		if tidx < base {
			return LNil
		}
		return ls.reg.Get(tidx)
	} else {
		switch idx {
		case RegistryIndex:
			return ls.G.Registry
		case EnvironIndex:
			if ls.currentFrame == nil {
				return ls.Env
			}
			return ls.currentFrame.Fn.Env
		case GlobalsIndex:
			return ls.G.Global
		default:
			fn := ls.currentFrame.Fn
			index := GlobalsIndex - idx - 1
			if index < len(fn.Upvalues) {
				return fn.Upvalues[index].Value()
			}
			return LNil
		}
	}
	return LNil
}

func (ls *LState) Push(value LValue) {
	ls.reg.Push(value)
}

func (ls *LState) Pop(n int) {
	for i := 0; i < n; i++ {
		if ls.GetTop() == 0 {
			ls.RaiseError("register underflow")
		}
		ls.reg.Pop()
	}
}

func (ls *LState) Insert(value LValue, index int) {
	reg := ls.indexToReg(index)
	top := ls.reg.Top()
	if reg >= top {
		ls.reg.Set(reg, value)
		return
	}
	if reg <= ls.currentLocalBase() {
		reg = ls.currentLocalBase()
	}
	top--
	for ; top >= reg; top-- {
		ls.reg.Set(top+1, ls.reg.Get(top))
	}
	ls.reg.Set(reg, value)
}

func (ls *LState) Remove(index int) {
	reg := ls.indexToReg(index)
	top := ls.reg.Top()
	switch {
	case reg >= top:
		return
	case reg < ls.currentLocalBase():
		return
	case reg == top-1:
		ls.Pop(1)
		return
	}
	for i := reg; i < top-1; i++ {
		ls.reg.Set(i, ls.reg.Get(i+1))
	}
	ls.reg.SetTop(top - 1)
}

/* }}} */

/* object allocation {{{ */

func (ls *LState) NewTable() *LTable {
	return newLTable(defaultArrayCap, defaultHashCap)
}

func (ls *LState) CreateTable(acap, hcap int) *LTable {
	return newLTable(acap, hcap)
}

// NewThread returns a new LState that shares with the original state all global objects.
// If the original state has context.Context, the new state has a new child context of the original state and this function returns its cancel function.
func (ls *LState) NewThread() (*LState, context.CancelFunc) {
	thread := ls.newThread()
	ls.G.gc.threads[len(ls.G.gc.threads)-1].root = true // go-moonscript patch: kept by Go code (gc.go)
	var f context.CancelFunc = nil
	if ls.ctx != nil {
		thread.mainLoop = mainLoopWithContext
		thread.ctx, f = context.WithCancel(ls.ctx)
		thread.ctxCancelFn = f
	}
	return thread, f
}

// newThread is NewThread without a context: coroutine.create's threads run
// under the context of whoever resumes them (coResume), and a child context
// of the creator's would stay registered with it as long as that context
// lives, for every coroutine that is never finished. go-moonscript patch.
func (ls *LState) newThread() *LState {
	thread := newLState(ls.Options)
	thread.G = ls.G
	thread.Env = ls.Env
	thread.inheritHook(ls)        // go-moonscript patch
	ls.gcNewThread(thread, false) // go-moonscript patch (gc.go)
	return thread
}

func (ls *LState) NewFunctionFromProto(proto *FunctionProto) *LFunction {
	return newLFunctionL(proto, ls.Env, int(proto.NumUpvalues))
}

func (ls *LState) NewUserData() *LUserData {
	return &LUserData{
		Env:       ls.currentEnv(),
		Metatable: LNil,
	}
}

func (ls *LState) NewFunction(fn LGFunction) *LFunction {
	return newLFunctionG(fn, ls.currentEnv(), 0)
}

func (ls *LState) NewClosure(fn LGFunction, upvalues ...LValue) *LFunction {
	cl := newLFunctionG(fn, ls.currentEnv(), len(upvalues))
	for i, lv := range upvalues {
		cl.Upvalues[i] = &Upvalue{}
		cl.Upvalues[i].Close()
		cl.Upvalues[i].SetValue(lv)
	}
	return cl
}

/* }}} */

/* toType {{{ */

func (ls *LState) ToBool(n int) bool {
	return LVAsBool(ls.Get(n))
}

func (ls *LState) ToInt(n int) int {
	if lv, ok := ls.Get(n).(LNumber); ok {
		return int(lv)
	}
	if lv, ok := ls.Get(n).(LString); ok {
		if num, err := parseNumber(string(lv)); err == nil {
			return int(num)
		}
	}
	return 0
}

func (ls *LState) ToInt64(n int) int64 {
	if lv, ok := ls.Get(n).(LNumber); ok {
		return int64(lv)
	}
	if lv, ok := ls.Get(n).(LString); ok {
		if num, err := parseNumber(string(lv)); err == nil {
			return int64(num)
		}
	}
	return 0
}

func (ls *LState) ToNumber(n int) LNumber {
	return LVAsNumber(ls.Get(n))
}

func (ls *LState) ToString(n int) string {
	return ls.lvAsString(ls.Get(n)) // go-moonscript patch: in the dialect
}

func (ls *LState) ToTable(n int) *LTable {
	if lv, ok := ls.Get(n).(*LTable); ok {
		return lv
	}
	return nil
}

func (ls *LState) ToFunction(n int) *LFunction {
	if lv, ok := ls.Get(n).(*LFunction); ok {
		return lv
	}
	return nil
}

func (ls *LState) ToUserData(n int) *LUserData {
	if lv, ok := ls.Get(n).(*LUserData); ok {
		return lv
	}
	return nil
}

func (ls *LState) ToThread(n int) *LState {
	if lv, ok := ls.Get(n).(*LState); ok {
		return lv
	}
	return nil
}

/* }}} */

/* error & debug operations {{{ */

func (ls *LState) registryOverflow() {
	ls.RaiseError("registry overflow")
}

// This function is equivalent to luaL_error( http://www.lua.org/manual/5.1/manual.html#luaL_error ).
func (ls *LState) RaiseError(format string, args ...interface{}) {
	ls.raiseError(1, format, args...)
}

// frozenTableError raises the error of a change to a frozen table, at the
// running Lua function's line or the caller's of the running Go function
// (go-moonscript patch: LTable.Freeze).
//
//go:noinline
func (ls *LState) frozenTableError() {
	ls.raiseError(1, frozenTableMessage)
}

// This function is equivalent to lua_error( http://www.lua.org/manual/5.1/manual.html#lua_error ).
// A string value is prefixed with the position of the function at level,
// as Lua 5.1's error function does (luaL_where: 1 is the caller of the
// running Go function; go-moonscript patch: that level exactly, the
// position only when it is a Lua function, no position for level 0).
func (ls *LState) Error(lv LValue, level int) {
	if str, ok := lv.(LString); ok {
		message := string(str)
		if level > 0 {
			if pos := ls.luaWhere(level); pos != "" {
				message = pos + " " + message
			}
		}
		ls.raiseError(0, "%s", message)
	} else {
		ls.Push(lv)
		ls.Panic(ls)
	}
}

func (ls *LState) GetInfo(what string, dbg *Debug, fn LValue) (LValue, error) {
	if !strings.HasPrefix(what, ">") {
		fn = dbg.frame.Fn
	} else {
		what = what[1:]
	}
	f, ok := fn.(*LFunction)
	if !ok {
		return LNil, newApiErrorS(ApiErrorRun, "can not get debug info(an object in not a function)")
	}

	retfn := false
	for _, c := range what {
		switch c {
		case 'f':
			retfn = true
		case 'S':
			// go-moonscript patch: Lua 5.1's funcinfo: "C" for a Go function
			// (source "=[C]", no lines), "main" for a main chunk (defined at
			// line 0; a coroutine's body is not one, a function reached by a
			// tail call is no "tail"), "Lua" for the others
			if f.IsG {
				dbg.What = "C"
				dbg.Source = "=[C]"
				dbg.ChunkName = "=[C]"
				dbg.LineDefined = -1
				dbg.LastLineDefined = -1
			} else {
				dbg.What = "Lua"
				dbg.Source = f.Proto.SourceName
				dbg.ChunkName = f.Proto.chunkName()
				dbg.LineDefined = f.Proto.LineDefined
				dbg.LastLineDefined = f.Proto.LastLineDefined
				if f.Proto.LineDefined == 0 {
					dbg.What = "main"
					dbg.LastLineDefined = 0
				}
			}
		case 'l':
			if !f.IsG && dbg.frame != nil {
				if dbg.frame.Pc > 0 {
					dbg.CurrentLine = f.Proto.DbgSourcePositions[dbg.frame.Pc-1]
				}
			} else {
				dbg.CurrentLine = -1
			}
		case 'u':
			dbg.NUpvalues = len(f.Upvalues)
		case 'n':
			// go-moonscript patch: Lua 5.1's getfuncname, from the
			// instruction of the caller that called the function
			if dbg.frame != nil {
				dbg.NameWhat, dbg.Name = ls.getfuncname(dbg.frame)
			}
		default:
			return LNil, newApiErrorS(ApiErrorRun, "invalid what: "+string(c))
		}
	}

	if retfn {
		return f, nil
	}
	return LNil, nil

}

func (ls *LState) GetStack(level int) (*Debug, bool) {
	frame := ls.currentFrame
	for ; level > 0 && frame != nil; frame = frame.Parent {
		level--
		if !frame.Fn.IsG {
			level -= frame.TailCall
		}
	}

	if level == 0 && frame != nil {
		return &Debug{frame: frame}, true
	} else if level < 0 && ls.stack.Sp() > 0 {
		return &Debug{frame: ls.stack.At(0)}, true
	}
	return &Debug{}, false
}

func (ls *LState) GetLocal(dbg *Debug, no int) (string, LValue) {
	frame := dbg.frame
	if name := ls.findLocal(frame, no); len(name) > 0 {
		return name, ls.reg.Get(frame.LocalBase + no - 1)
	}
	return "", LNil
}

func (ls *LState) SetLocal(dbg *Debug, no int, lv LValue) string {
	frame := dbg.frame
	if name := ls.findLocal(frame, no); len(name) > 0 {
		ls.reg.Set(frame.LocalBase+no-1, lv)
		return name
	}
	return ""
}

func (ls *LState) GetUpvalue(fn *LFunction, no int) (string, LValue) {
	if fn.IsG {
		return "", LNil
	}

	no--
	if no >= 0 && no < len(fn.Upvalues) {
		return fn.Proto.DbgUpvalues[no], fn.Upvalues[no].Value()
	}
	return "", LNil
}

func (ls *LState) SetUpvalue(fn *LFunction, no int, lv LValue) string {
	if fn.IsG {
		return ""
	}

	no--
	if no >= 0 && no < len(fn.Upvalues) {
		fn.Upvalues[no].SetValue(lv)
		return fn.Proto.DbgUpvalues[no]
	}
	return ""
}

/* }}} */

/* env operations {{{ */

func (ls *LState) GetFEnv(obj LValue) LValue {
	switch lv := obj.(type) {
	case *LFunction:
		return lv.Env
	case *LUserData:
		return lv.Env
	case *LState:
		return lv.Env
	}
	return LNil
}

func (ls *LState) SetFEnv(obj LValue, env LValue) {
	tb, ok := env.(*LTable)
	if !ok {
		ls.RaiseError("cannot use %v as an environment", env.Type().String())
	}

	switch lv := obj.(type) {
	case *LFunction:
		lv.Env = tb
	case *LUserData:
		lv.Env = tb
	case *LState:
		lv.Env = tb
	}
	/* do nothing */
}

/* }}} */

/* table operations {{{ */

func (ls *LState) RawGet(tb *LTable, key LValue) LValue {
	return tb.RawGet(key)
}

func (ls *LState) RawGetInt(tb *LTable, key int) LValue {
	return tb.RawGetInt(key)
}

func (ls *LState) GetField(obj LValue, skey string) LValue {
	return ls.getFieldString(obj, skey)
}

func (ls *LState) GetTable(obj LValue, key LValue) LValue {
	return ls.getField(obj, key)
}

// RawSet is rawset: go-moonscript patch: a frozen table raises the Lua
// error "attempt to modify a read-only table" (LTable.Freeze).
func (ls *LState) RawSet(tb *LTable, key LValue, value LValue) {
	if tb.Frozen() {
		ls.frozenTableError()
	}
	if n, ok := key.(LNumber); ok && math.IsNaN(float64(n)) {
		ls.RaiseError("table index is NaN")
	} else if key == LNil {
		ls.RaiseError("table index is nil")
	}
	tb.RawSet(key, value)
}

// RawSetInt is lua_rawseti: go-moonscript patch: a frozen table raises the
// Lua error "attempt to modify a read-only table" (LTable.Freeze).
func (ls *LState) RawSetInt(tb *LTable, key int, value LValue) {
	if tb.Frozen() {
		ls.frozenTableError()
	}
	tb.RawSetInt(key, value)
}

// SetField is t[key] = value, with __newindex. go-moonscript patch: a frozen
// table raises the Lua error "attempt to modify a read-only table", before
// any __newindex (LTable.Freeze); so do SetTable and SetGlobal.
func (ls *LState) SetField(obj LValue, key string, value LValue) {
	ls.setFieldString(obj, key, value)
}

func (ls *LState) SetTable(obj LValue, key LValue, value LValue) {
	ls.setField(obj, key, value)
}

func (ls *LState) ForEach(tb *LTable, cb func(LValue, LValue)) {
	tb.ForEach(cb)
}

func (ls *LState) GetGlobal(name string) LValue {
	return ls.GetField(ls.Get(GlobalsIndex), name)
}

func (ls *LState) SetGlobal(name string, value LValue) {
	ls.SetField(ls.Get(GlobalsIndex), name, value)
}

func (ls *LState) Next(tb *LTable, key LValue) (LValue, LValue) {
	return tb.Next(key)
}

/* }}} */

/* unary operations {{{ */

func (ls *LState) ObjLen(v1 LValue) int {
	if v1.Type() == LTString {
		return len(string(v1.(LString)))
	}
	op := ls.metaOp1(v1, "__len")
	if op.Type() == LTFunction {
		ls.Push(op)
		ls.Push(v1)
		ls.Call(1, 1)
		ret := ls.reg.Pop()
		if ret.Type() == LTNumber {
			return int(ret.(LNumber))
		}
	} else if v1.Type() == LTTable {
		return v1.(*LTable).Len()
	}
	return 0
}

/* }}} */

/* binary operations {{{ */

func (ls *LState) Concat(values ...LValue) string {
	top := ls.reg.Top()
	for _, value := range values {
		ls.reg.Push(value)
	}
	ret := stringConcat(ls, len(values), ls.reg.Top()-1)
	ls.reg.SetTop(top)
	return LVAsString(ret)
}

func (ls *LState) LessThan(lhs, rhs LValue) bool {
	return lessThan(ls, lhs, rhs)
}

func (ls *LState) Equal(lhs, rhs LValue) bool {
	return equals(ls, lhs, rhs, false)
}

func (ls *LState) RawEqual(lhs, rhs LValue) bool {
	return equals(ls, lhs, rhs, true)
}

/* }}} */

/* register operations {{{ */

func (ls *LState) Register(name string, fn LGFunction) {
	ls.SetGlobal(name, ls.NewFunction(fn))
}

/* }}} */

/* load and function call operations {{{ */

// Load loads a chunk named name, which error messages and tracebacks show
// as it is ("name:1: ..."). go-moonscript patch: debug.getinfo reports its
// source as "=" + name and its short_src as name; LoadChunk loads a chunk
// with a Lua chunk name ("@file", "=name", the source text), as lua_load
// does (chunkname.go).
func (ls *LState) Load(reader io.Reader, name string) (*LFunction, error) {
	return ls.load(reader, name, name, "="+name)
}

// load loads a chunk whose name in error messages is name (parseName in
// syntax errors) and whose Lua chunk name is chunkname (go-moonscript
// patch).
func (ls *LState) load(reader io.Reader, name, parseName, chunkname string) (*LFunction, error) {
	// go-moonscript patch: binary chunks, from string.dump (dump.go)
	reader, fn, err := ls.loadBinary(reader, chunkname)
	if fn != nil || err != nil {
		return fn, err
	}
	// go-moonscript patch: the state's dialect's syntax
	dialect := parse.Lua51
	if ls.Dialect() == DialectLuaJIT {
		dialect = parse.LuaJIT
	}
	src, err := io.ReadAll(reader)
	if err != nil {
		return nil, newApiErrorE(ApiErrorSyntax, err)
	}
	chunk, err := parse.ParseBytes(src, parseName, dialect)
	if err != nil {
		// go-moonscript patch: a function that needs too many registers
		// before the error, which lua5.1 finds while parsing (lua51Overflow)
		if dialect == parse.Lua51 {
			if pe := parse.Lua51Overflow(src, parseName); pe != nil {
				err = pe
			}
		}
		// go-moonscript patch: an error near no token (a limit) is reported
		// like the compiler's limit errors, as "<chunk>:<line>: <message>"
		if pe, ok := err.(*parse.Error); ok && pe.Token == "" && pe.Pos.Line != parse.NoLine {
			err = &CompileError{Line: pe.Pos.Line, Message: pe.Message, source: parseName}
		}
		return nil, newApiErrorE(ApiErrorSyntax, err)
	}
	proto, err := compileDialect(chunk, parseName, ls.Dialect()) // go-moonscript patch
	// go-moonscript patch: lua5.1 finds that a function needs too many
	// registers while parsing, and reports it near the token it is at; the
	// parser's model of its registers tells, if the compiler needed many
	if dialect == parse.Lua51 && lua51Overflow(proto, err) {
		if pe := parse.Lua51Overflow(src, parseName); pe != nil {
			return nil, newApiErrorE(ApiErrorSyntax, pe)
		}
	}
	if err != nil {
		return nil, newApiErrorE(ApiErrorSyntax, err)
	}
	setSource(proto, name, chunkname) // go-moonscript patch (chunkname.go)
	return newLFunctionL(proto, ls.currentEnv(), 0), nil
}

func (ls *LState) Call(nargs, nret int) {
	ls.callR(nargs, nret, -1)
}

func (ls *LState) PCall(nargs, nret int, errfunc *LFunction) (err error) {
	err = nil
	sp := ls.stack.Sp()
	base := ls.reg.Top() - nargs - 1
	nccalls := ls.nCcalls // go-moonscript patch: restored when an error unwinds callR
	oldpanic := ls.Panic
	ls.Panic = panicWithoutTraceback
	oldHasErrorFunc := ls.hasErrorFunc
	if errfunc != nil {
		ls.hasErrorFunc = true
	}
	defer func() {
		ls.Panic = oldpanic
		ls.hasErrorFunc = oldHasErrorFunc
		rcv := recover()
		if rcv != nil {
			ls.nCcalls = nccalls
			if _, ok := rcv.(*ApiError); !ok {
				err = newApiErrorS(ApiErrorPanic, fmt.Sprint(rcv))
				if ls.Options.IncludeGoStackTrace {
					buf := make([]byte, 4096)
					runtime.Stack(buf, false)
					err.(*ApiError).StackTrace = strings.Trim(string(buf), "\000") + "\n" + ls.stackTrace(0)
				}
			} else {
				err = rcv.(*ApiError)
			}
			if errfunc != nil {
				ls.Push(errfunc)
				ls.Push(err.(*ApiError).Object)
				ls.Panic = panicWithoutTraceback
				defer func() {
					ls.Panic = oldpanic
					rcv := recover()
					if rcv != nil {
						if _, ok := rcv.(*ApiError); !ok {
							err = newApiErrorS(ApiErrorPanic, fmt.Sprint(rcv))
							if ls.Options.IncludeGoStackTrace {
								buf := make([]byte, 4096)
								runtime.Stack(buf, false)
								err.(*ApiError).StackTrace = strings.Trim(string(buf), "\000") + ls.stackTrace(0)
							}
						} else {
							err = rcv.(*ApiError)
							err.(*ApiError).StackTrace = ls.stackTrace(0)
						}
						ls.closeUpvalues(base) // go-moonscript patch
						ls.nCcalls = nccalls
						ls.stack.SetSp(sp)
						ls.currentFrame = ls.stack.Last()
						ls.reg.SetTop(base)
					}
				}()
				ls.Call(1, 1)
				err = newApiError(ApiErrorError, ls.Get(-1))
			} else if len(err.(*ApiError).StackTrace) == 0 {
				err.(*ApiError).StackTrace = ls.stackTrace(0)
			}
			// go-moonscript patch: the frames above sp are discarded
			ls.closeUpvalues(base)
			ls.stack.SetSp(sp)
			ls.currentFrame = ls.stack.Last()
			ls.reg.SetTop(base)
		}
		ls.stack.SetSp(sp)
		if sp == 0 {
			ls.currentFrame = nil
		}
	}()

	ls.Call(nargs, nret)

	return
}

func (ls *LState) GPCall(fn LGFunction, data LValue) error {
	ls.Push(newLFunctionG(fn, ls.currentEnv(), 0))
	ls.Push(data)
	return ls.PCall(1, MultRet, nil)
}

func (ls *LState) CallByParam(cp P, args ...LValue) error {
	ls.Push(cp.Fn)
	for _, arg := range args {
		ls.Push(arg)
	}

	if cp.Protect {
		return ls.PCall(len(args), cp.NRet, cp.Handler)
	}
	ls.Call(len(args), cp.NRet)
	return nil
}

/* }}} */

/* metatable operations {{{ */

func (ls *LState) GetMetatable(obj LValue) LValue {
	return ls.metatable(obj, false)
}

// SetMetatable sets the metatable of obj. go-moonscript patch: a frozen
// table raises the Lua error "attempt to modify a read-only table"
// (LTable.Freeze).
func (ls *LState) SetMetatable(obj LValue, mt LValue) {
	switch mt.(type) {
	case *LNilType, *LTable:
	default:
		ls.RaiseError("metatable must be a table or nil, but got %v", mt.Type().String())
	}

	switch v := obj.(type) {
	case *LTable:
		if v.Frozen() { // go-moonscript patch (LTable.Freeze)
			ls.frozenTableError()
		}
		v.Metatable = mt
	case *LUserData:
		v.Metatable = mt
	default:
		ls.G.builtinMts[int(obj.Type())] = mt
		return
	}
	ls.gcSetMetatable(obj, mt) // go-moonscript patch: weak tables, __gc (gc.go)
}

/* }}} */

/* coroutine operations {{{ */

func (ls *LState) Status(th *LState) string {
	status := "suspended"
	if th.Dead {
		status = "dead"
	} else if ls.G.CurrentThread == th {
		status = "running"
	} else if th.Parent != nil || th == ls.G.MainThread {
		// go-moonscript patch: active, but it resumed the running thread
		// (directly or not), or the main thread; was only for the direct
		// parent. From mdogan/gopher-lua afcb8e0069.
		status = "normal"
	}
	return status
}

func (ls *LState) Resume(th *LState, fn *LFunction, args ...LValue) (ResumeState, error, []LValue) {
	isstarted := th.isStarted()
	if !isstarted && th.stack.IsEmpty() { // go-moonscript patch: coroutine.create pushed it
		base := 0
		th.stack.Push(callFrame{
			Fn:         fn,
			Pc:         0,
			Base:       base,
			LocalBase:  base + 1,
			ReturnBase: base,
			NArgs:      0,
			NRet:       MultRet,
			Parent:     nil,
			TailCall:   0,
		})
	}

	if ls.G.CurrentThread == th {
		return ResumeError, newApiErrorS(ApiErrorRun, "can not resume a running thread"), nil
	}
	if th.Dead {
		return ResumeError, newApiErrorS(ApiErrorRun, "can not resume a dead thread"), nil
	}
	if th.Parent != nil || th == ls.G.MainThread { // go-moonscript patch: see coResume
		return ResumeError, newApiErrorS(ApiErrorRun, "cannot resume normal coroutine"), nil
	}
	th.Parent = ls
	ls.G.CurrentThread = th
	if th.ctx == nil && ls.ctx != nil {
		// go-moonscript patch: a thread without a context of its own (made
		// by coroutine.create) runs under the resumer's, see coResume
		prevMainLoop := th.mainLoop
		th.ctx, th.mainLoop = ls.ctx, ls.mainLoop
		defer func() { th.ctx, th.mainLoop = nil, prevMainLoop }()
	}
	if !isstarted {
		cf := th.stack.Last()
		th.currentFrame = cf
		th.SetTop(0)
		for _, arg := range args {
			th.Push(arg)
		}
		cf.NArgs = len(args)
		th.initCallFrame(cf)
		th.Panic = panicWithoutTraceback
		th.hook.starting = true // go-moonscript patch: "call" hook event
	} else {
		base := th.reg.Top()
		for _, arg := range args {
			th.Push(arg)
		}
		th.resumeValues(base) // go-moonscript patch
	}
	top := ls.GetTop()
	threadRun(th)
	haserror := LVIsFalse(ls.Get(top + 1))
	ret := make([]LValue, 0, ls.GetTop())
	for idx := top + 2; idx <= ls.GetTop(); idx++ {
		ret = append(ret, ls.Get(idx))
	}
	if len(ret) == 0 {
		ret = append(ret, LNil)
	}
	ls.SetTop(top)

	if haserror {
		return ResumeError, newApiError(ApiErrorRun, ret[0]), nil
	} else if th.Dead { // go-moonscript patch: a yield can leave no frame (yieldedOut)
		return ResumeOK, nil, ret
	}
	return ResumeYield, nil, ret
}

func (ls *LState) Yield(values ...LValue) int {
	ls.SetTop(0)
	for _, lv := range values {
		ls.Push(lv)
	}
	return -1
}

func (ls *LState) XMoveTo(other *LState, n int) {
	if ls == other {
		return
	}
	top := ls.GetTop()
	n = intMin(n, top)
	// go-moonscript patch: one copy (it was a Push of each Get)
	if n > 0 {
		rg := other.reg
		newSize := rg.top + n
		if newSize > cap(rg.array) {
			rg.resize(newSize)
		}
		copy(rg.array[rg.top:newSize], ls.reg.array[ls.reg.top-n:ls.reg.top])
		rg.top = newSize
	}
	ls.SetTop(top - n)
}

/* }}} */

/* GopherLua original APIs {{{ */

// Set maximum memory size. This function can only be called from the main thread.
func (ls *LState) SetMx(mx int) {
	if ls.Parent != nil {
		ls.RaiseError("sub threads are not allowed to set a memory limit")
	}
	go func() {
		limit := uint64(mx * 1024 * 1024) //MB
		var s runtime.MemStats
		for atomic.LoadInt32(&ls.stop) == 0 {
			runtime.ReadMemStats(&s)
			if s.Alloc >= limit {
				fmt.Println("out of memory")
				os.Exit(3)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}()
}

// SetContext set a context ctx to this LState. The provided ctx must be non-nil.
func (ls *LState) SetContext(ctx context.Context) {
	ls.mainLoop = mainLoopWithContext
	ls.ctx = ctx
}

// Context returns the LState's context. To change the context, use WithContext.
func (ls *LState) Context() context.Context {
	return ls.ctx
}

// RemoveContext removes the context associated with this LState and returns this context.
func (ls *LState) RemoveContext() context.Context {
	oldctx := ls.ctx
	ls.mainLoop = mainLoop
	ls.ctx = nil
	return oldctx
}

// Converts the Lua value at the given acceptable index to the chan LValue.
func (ls *LState) ToChannel(n int) chan LValue {
	if lv, ok := ls.Get(n).(LChannel); ok {
		return (chan LValue)(lv)
	}
	return nil
}

// RemoveCallerFrame removes the stack frame above the current stack frame. This is useful in tail calls. It returns
// the new current frame.
func (ls *LState) RemoveCallerFrame() *callFrame {
	cs := ls.stack
	sp := cs.Sp()
	parentFrame := cs.At(sp - 2)
	currentFrame := cs.At(sp - 1)
	parentsParentFrame := parentFrame.Parent
	*parentFrame = *currentFrame
	parentFrame.Parent = parentsParentFrame
	parentFrame.Idx = sp - 2
	cs.Pop()
	return parentFrame
}

/* }}} */

/* }}} */

//
