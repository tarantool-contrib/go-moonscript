package lua

// go-moonscript patch: debug hooks, Lua 5.1's lua_sethook/lua_gethook and
// the hook calls of ldo.c (luaD_callhook) and lvm.c (traceexec), for
// debug.sethook and debug.gethook.
//
// Hooks are per thread, as in Lua 5.1: debug.sethook([thread,] hook, mask
// [, count]) sets the hook of the given (default: the running) coroutine. A
// new coroutine inherits the creating thread's mask and count but not its
// hook function, which in Lua 5.1 lives in a registry table keyed by thread:
// it runs no hook until one is set on it, and debug.gethook in it reports
// nil, the inherited mask and count.
//
// The events and their order are Lua 5.1's:
//
//   - "call" when a function is entered: a Lua function before its first
//     instruction, a Go function before it runs (also coroutine bodies when
//     first resumed, metamethods, functions called from Go);
//   - "return" when a function returns normally (not when an error unwinds
//     it), followed by one "tail return" per tail call the frame went
//     through; a Go function that yields gets its "return" when the
//     coroutine is resumed;
//   - "line" with the line number before an instruction that starts a new
//     line, or that is the first of a function or the target of a backward
//     jump (traceexec's rule);
//   - "count" every count instructions.
//
// The hook runs with hooks disabled (instructions still count towards the
// next count event), as a function called from the hooked function, so
// debug.getinfo(2) inside it describes the function that triggered the
// event, with currentline the line being executed.
//
// Where events fall depends on the compiler's instructions and their lines.
// The compiler is patched to give instructions the lines luac gives them
// (compile.go: a call is on the line of its arguments, a closure on the
// line of its "end", stores after their right-hand side, ...), so line
// events match PUC Lua's for the code MoonScript generates and ordinary
// Lua; luac's lazy loading of operands (a constant or variable is loaded
// when the next token is read) is only approximated. Instruction counts
// differ from luac's, so "count" events don't fall on the same
// instructions, and instructions a Lua hook runs count towards the next
// count event as in Lua 5.1, but their number differs too.
//
// A tail call of a Go function runs the Go function in place of the
// caller's frame (gopher-lua removes it): the events of the caller's
// RETURN instruction that Lua 5.1 runs afterwards (count, line, return and
// tail return) are run after the Go function's return event.

// Hook event masks, as LUA_MASK* in Lua 5.1.
const (
	MaskCall   = 1 << iota // "c": call events
	MaskReturn             // "r": return and tail return events
	MaskLine               // "l": line events
	MaskCount              // count events (count > 0)
)

// hookMaskTrace are the events traceExec handles, checked before every
// instruction.
const hookMaskTrace = MaskLine | MaskCount

type hookState struct {
	fn        LValue // as given to sethook (Lua 5.1 keeps it with an empty mask)
	mask      int    // as set, reported by gethook
	baseCount int
	count     int
	running   bool // a hook is running: hooks are disabled

	// the last instruction traceExec saw (Lua 5.1 compares with the frame's
	// savedpc); a frame other than lastFrame resumes after the instruction
	// before its pc (a call)
	lastFrame *callFrame
	lastPc    int

	// coroutine resume events: starting is set until a new coroutine runs
	// ("call" for its body), yieldFn is the Go function that yielded
	// ("return" when resumed; set with or without hooks, as a hook may be
	// set while the coroutine is suspended)
	starting bool
	yieldFn  *LFunction
}

// SetHook sets the debug hook of ls, like debug.sethook(fn, mask, count)
// on ls: mask is a string of "c", "r" and "l"; count > 0 adds count events.
// A nil fn or an empty mask with no count turns the hook off.
func (ls *LState) SetHook(fn *LFunction, mask string, count int) {
	m := 0
	for _, c := range mask {
		switch c {
		case 'c':
			m |= MaskCall
		case 'r':
			m |= MaskReturn
		case 'l':
			m |= MaskLine
		}
	}
	if count > 0 {
		m |= MaskCount
	}
	var v LValue = LNil
	if fn != nil {
		v = fn
	}
	ls.setHook(v, m, count)
}

func (ls *LState) setHook(fn LValue, mask, count int) {
	if fn == LNil || mask == 0 {
		mask = 0
	}
	h := &ls.hook
	h.fn = fn
	h.mask = mask
	h.baseCount = count
	h.count = count
	h.lastFrame = nil
	ls.updateHookMask()
}

// GetHook returns the hook of ls as debug.gethook does: the function (LNil
// if none), the mask string and the count.
func (ls *LState) GetHook() (LValue, string, int) {
	h := &ls.hook
	fn := h.fn
	if fn == nil {
		fn = LNil
	}
	mask := ""
	if h.mask&MaskCall != 0 {
		mask += "c"
	}
	if h.mask&MaskReturn != 0 {
		mask += "r"
	}
	if h.mask&MaskLine != 0 {
		mask += "l"
	}
	return fn, mask, h.baseCount
}

// updateHookMask sets the mask the VM checks: the events of the hook, if it
// is a function.
func (ls *LState) updateHookMask() {
	if _, ok := ls.hook.fn.(*LFunction); ok {
		ls.hookMask = ls.hook.mask
	} else {
		ls.hookMask = 0
	}
}

// inheritHook gives a new thread the mask and count of its creator, but no
// hook function.
func (th *LState) inheritHook(ls *LState) {
	th.hook.fn = LNil
	th.hook.mask = ls.hook.mask
	th.hook.baseCount = ls.hook.baseCount
	th.hook.count = ls.hook.baseCount
	th.updateHookMask()
}

// callHook runs the hook for event (line < 0: none) with the current frame
// as the hooked function, preserving the registers.
func (ls *LState) callHook(event string, line int) {
	h := &ls.hook
	fn, ok := h.fn.(*LFunction)
	if !ok || h.running {
		return
	}
	h.running = true
	defer func() { h.running = false }()
	top := ls.reg.Top()
	ls.reg.Push(fn)
	ls.reg.Push(LString(event))
	if line >= 0 {
		ls.reg.Push(LNumber(line))
	} else {
		ls.reg.Push(LNil)
	}
	ls.Call(2, 0)
	ls.reg.SetTop(top)
}

// hookCall is the call event for the current frame, a Lua function about to
// run its first instruction (debug.getinfo reports that instruction's
// line).
func (ls *LState) hookCall() {
	cf := ls.currentFrame
	pc := cf.Pc
	cf.Pc = pc + 1
	ls.callHook("call", -1)
	cf.Pc = pc
}

// hookReturn is the return event for the current frame, followed by its tail
// return events (luaD_poscall's callrethooks).
func (ls *LState) hookReturn() {
	cf := ls.currentFrame
	ls.callHook("return", -1)
	if cf.Fn.IsG {
		return
	}
	for n := cf.TailCall; n > 0 && ls.hookMask&MaskReturn != 0; n-- {
		ls.currentFrame = cf // the hook's return resets it to the top frame
		ls.callHook("tail return", -1)
	}
}

// hookGoReturn runs the events after the Go function of the current frame
// has returned: its return event and, if it was tail-called by a Lua
// function, what Lua 5.1 runs for the caller's RETURN instruction after it
// (which gopher-lua skips by removing the caller's frame): count and line
// events, return and tail return events.
func (ls *LState) hookGoReturn(tailcall bool) {
	cf := ls.currentFrame
	if ls.hookMask&MaskReturn != 0 {
		ls.callHook("return", -1)
	}
	caller := cf.Parent
	if !tailcall || caller == nil || caller.Fn.IsG {
		ls.currentFrame = cf
		return
	}
	defer func() { ls.currentFrame = cf }()
	if ls.hookMask&hookMaskTrace != 0 && caller.Pc < len(caller.Fn.Proto.Code) {
		ls.currentFrame = caller
		traceExec(ls, caller)
	}
	if ls.hookMask&MaskReturn != 0 {
		ls.currentFrame = caller
		ls.hookReturn()
	}
}

// hookResume runs the events of a coroutine being resumed: the call event
// of its body or the return event of the function that yielded.
func (ls *LState) hookResume() {
	h := &ls.hook
	starting, yieldFn := h.starting, h.yieldFn
	h.starting, h.yieldFn = false, nil
	cf := ls.currentFrame
	if cf == nil {
		return
	}
	if starting && !cf.Fn.IsG && ls.hookMask&MaskCall != 0 {
		ls.hookCall()
	}
	if yieldFn != nil && ls.hookMask&MaskReturn != 0 {
		top := ls.reg.Top()
		ls.stack.Push(callFrame{
			Fn:         yieldFn,
			Base:       top,
			LocalBase:  top,
			ReturnBase: top,
			NRet:       MultRet,
			Parent:     cf,
		})
		ls.currentFrame = ls.stack.Last()
		defer func() {
			ls.stack.Pop()
			ls.currentFrame = cf
		}()
		ls.callHook("return", -1)
	}
}

// traceExec runs the count and line events for the instruction at cf.Pc,
// about to be executed (lvm.c's traceexec).
func traceExec(L *LState, cf *callFrame) {
	h := &L.hook
	pc := cf.Pc
	mask := L.hookMask
	if mask&MaskCount != 0 {
		h.count--
		if h.count == 0 {
			h.count = h.baseCount
			cf.Pc = pc + 1
			L.callHook("count", -1)
			cf.Pc = pc
		}
	}
	if mask&MaskLine != 0 {
		lines := cf.Fn.Proto.DbgSourcePositions
		oldpc := pc - 1
		if h.lastFrame == cf {
			oldpc = h.lastPc
		}
		if pc == 0 || pc <= oldpc || lines[pc] != lines[oldpc] {
			cf.Pc = pc + 1
			L.callHook("line", lines[pc])
			cf.Pc = pc
		}
		h.lastFrame = cf
		h.lastPc = pc
	}
}

func debugSetHook(L *LState) int {
	th, arg := L, 0
	if t, ok := L.Get(1).(*LState); ok {
		th, arg = t, 1
	}
	if fn := L.Get(arg + 1); fn == LNil {
		th.setHook(LNil, 0, 0)
		return 0
	}
	mask := L.CheckString(arg + 2)
	fn := L.CheckFunction(arg + 1)
	count := L.OptInt(arg+3, 0)
	th.SetHook(fn, mask, count)
	return 0
}

func debugGetHook(L *LState) int {
	th := L
	if t, ok := L.Get(1).(*LState); ok {
		th = t
	}
	fn, mask, count := th.GetHook()
	L.Push(fn)
	L.Push(LString(mask))
	L.Push(LNumber(count))
	return 3
}

// isTailCallLevel reports whether a stack level is one of the levels lost to
// a tail call, which GetStack answers with the bottom frame (Lua 5.1's
// lua_getstack walks the levels the same way and returns a "(tail call)"
// level there).
func (ls *LState) isTailCallLevel(level int) bool {
	frame := ls.currentFrame
	for ; level > 0 && frame != nil; frame = frame.Parent {
		level--
		if !frame.Fn.IsG {
			level -= frame.TailCall
		}
	}
	return level < 0
}
