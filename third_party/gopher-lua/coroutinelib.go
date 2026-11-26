package lua

func OpenCoroutine(L *LState) int {
	// TODO: Tie module name to contents of linit.go?
	mod := L.RegisterModule(CoroutineLibName, coFuncs)
	openLuaJIT(L, CoroutineLibName, mod) // go-moonscript patch (luajitlib.go)
	L.Push(mod)
	return 1
}

var coFuncs = map[string]LGFunction{
	"create":  coCreate,
	"yield":   coYield,
	"resume":  coResume,
	"running": coRunning,
	"status":  coStatus,
	"wrap":    coWrap,
}

func coCreate(L *LState) int {
	fn, ok := L.Get(1).(*LFunction)
	if !ok || fn.IsG {
		// go-moonscript patch: a Lua function only, as in Lua 5.1 (LuaJIT
		// accepts a Go function too; L.Resume runs one as a coroutine body)
		L.ArgError(1, "Lua function expected")
	}
	newthread := L.newThread() // go-moonscript patch: no context, see coResume
	base := 0
	newthread.stack.Push(callFrame{
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
	L.Push(newthread)
	return 1
}

func coYield(L *LState) int {
	return -1
}

func coResume(L *LState) int {
	th := L.CheckThread(1)
	// go-moonscript patch: Lua 5.1's messages (auxresume), and for a wrapped
	// coroutine the position of the caller (auxwrap); were "can not resume a
	// running thread" and "can not resume a dead thread".
	status := ""
	switch {
	case L.G.CurrentThread == th:
		status = "running"
	case th.Dead:
		status = "dead"
	case th.Parent != nil || th == L.G.MainThread:
		// a thread with a parent is active, it resumed the running thread
		// (directly or through other coroutines), and so is the main thread:
		// resuming one re-entered its frames until the Go stack overflowed
		// (a fatal error). From mdogan/gopher-lua afcb8e0069.
		status = "normal"
	}
	if status != "" {
		msg := "cannot resume " + status + " coroutine"
		if th.wrapped {
			L.raiseError(0, "%s", coWrapWhere(L)+msg)
			return 0
		}
		L.Push(LFalse)
		L.Push(LString(msg))
		return 2
	}
	th.Parent = L
	L.G.CurrentThread = th
	// go-moonscript patch: the coroutine runs under the resumer's context,
	// not the one it got when it was created (now none, see newThread): it
	// can outlive the call that created it, and would keep that call's
	// context values and miss the resumer's deadline (and one created
	// without a context never stopped). From mdogan/gopher-lua f7b3485bd8.
	prevCtx, prevMainLoop := th.ctx, th.mainLoop
	th.ctx, th.mainLoop = L.ctx, L.mainLoop
	defer func() { th.ctx, th.mainLoop = prevCtx, prevMainLoop }()
	if !th.isStarted() {
		cf := th.stack.Last()
		th.currentFrame = cf
		th.SetTop(0)
		nargs := L.GetTop() - 1
		L.XMoveTo(th, nargs)
		cf.NArgs = nargs
		th.initCallFrame(cf)
		th.Panic = panicWithoutTraceback
		th.hook.starting = true // go-moonscript patch: "call" hook event
	} else {
		base := th.reg.Top()
		nargs := L.GetTop() - 1
		L.XMoveTo(th, nargs)
		th.resumeValues(base) // go-moonscript patch
	}
	top := L.GetTop()
	threadRun(th)
	return L.GetTop() - top
}

func coRunning(L *LState) int {
	if L.G.MainThread == L {
		L.Push(LNil)
		return 1
	}
	L.Push(L.G.CurrentThread)
	return 1
}

func coStatus(L *LState) int {
	L.Push(LString(L.Status(L.CheckThread(1))))
	return 1
}

// coWrapWhere is the position Lua 5.1's coroutine.wrap function adds to a
// string error (luaL_where(L, 1)): the line of the Lua function that called
// it, "" if a Go function did. go-moonscript patch.
func coWrapWhere(L *LState) string {
	if dbg, ok := L.GetStack(1); ok && !dbg.frame.Fn.IsG {
		return L.where(1, false) + " "
	}
	return ""
}

func wrapaux(L *LState) int {
	L.Insert(L.ToThread(UpvalueIndex(1)), 1)
	return coResume(L)
}

func coWrap(L *LState) int {
	coCreate(L)
	L.CheckThread(L.GetTop()).wrapped = true
	v := L.Get(L.GetTop())
	L.Pop(1)
	L.Push(L.NewClosure(wrapaux, v))
	return 1
}

//
