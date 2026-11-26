package lua

// go-moonscript patch: Lua 5.1's runtime error messages (ldebug.c): the
// description of the value an operation failed on ("local 'x'", "global
// 'y'", "field 'z'", "upvalue 'u'", "method 'm'") found by symbolic
// execution of the function's bytecode (getobjname, symbexec), the name of
// a called function (getfuncname, for debug.getinfo's name/namewhat and
// for argument errors), and the messages of luaG_typeerror,
// luaG_concaterror, luaG_aritherror and luaG_ordererror. Everything here
// runs on error paths only.

import "math"

// symbexecNeutral is what symbexec answers when no instruction before
// lastpc changed the register (Lua 5.1 answers the final RETURN, which
// getobjname gives no name for).
const symbexecNeutral = ^uint32(0)

// symbexec is Lua 5.1's symbexec without the bytecode checks: the last
// instruction before lastpc that changed register reg, following forward
// jumps that don't skip lastpc. The opcodes gopher-lua adds are mapped to
// Lua 5.1's: MOVEN is a MOVE (the moves it stands for follow it as MOVE
// instructions), GETTABLEKS a GETTABLE, SETTABLEKS a SETTABLE and NOP a
// JMP to the next instruction.
func symbexec(p *FunctionProto, lastpc, reg int) uint32 {
	last := -1
	code := p.Code
	if lastpc > len(code) {
		lastpc = len(code)
	}
	for pc := 0; pc < lastpc; pc++ {
		inst := code[pc]
		op := opGetOpCode(inst)
		a := opGetArgA(inst)
		switch op {
		case OP_MOVE, OP_MOVEN, OP_LOADK, OP_LOADBOOL, OP_GETUPVAL,
			OP_GETGLOBAL, OP_GETTABLE, OP_GETTABLEKS, OP_NEWTABLE,
			OP_ADD, OP_SUB, OP_MUL, OP_DIV, OP_MOD, OP_POW, OP_UNM, OP_NOT,
			OP_LEN, OP_CONCAT, OP_TEST, OP_TESTSET, OP_VARARG:
			// Lua 5.1's opmodes with the A flag (TEST has it too)
			if a == reg {
				last = pc
			}
		case OP_LOADNIL:
			if a <= reg && reg <= opGetArgB(inst) {
				last = pc
			}
		case OP_SELF:
			if a == reg || a+1 == reg {
				last = pc
			}
		case OP_TFORLOOP:
			if reg >= a+2 {
				last = pc
			}
		case OP_FORLOOP, OP_FORPREP, OP_JMP:
			if op != OP_JMP && a == reg {
				last = pc
			}
			b := opGetArgSbx(inst)
			if dest := pc + 1 + b; pc < dest && dest <= lastpc {
				pc += b
			}
		case OP_CALL, OP_TAILCALL:
			if reg >= a {
				last = pc
			}
		case OP_SETLIST:
			if opGetArgC(inst) == 0 {
				pc++
			}
		case OP_CLOSURE:
			if a == reg {
				last = pc
			}
			if bx := opGetArgBx(inst); bx < len(p.FunctionPrototypes) {
				pc += int(p.FunctionPrototypes[bx].NumUpvalues)
			}
		}
	}
	if last < 0 {
		return symbexecNeutral
	}
	return code[last]
}

// kname is Lua 5.1's kname: the constant string RK index c names, or "?".
func kname(p *FunctionProto, c int) string {
	if opIsK(c) {
		if s, ok := p.Constants[opIndexK(c)].(LString); ok {
			return string(s)
		}
	}
	return "?"
}

// getobjname is Lua 5.1's getobjname: the kind ("local", "global",
// "field", "upvalue", "method"; "" for none) and the name of what register
// reg holds when fn is at instruction pc.
func getobjname(fn *LFunction, pc, reg int) (string, string) {
	for depth := 0; depth < 256; depth++ { // MOVE chains go down: b < a
		if name, ok := fn.LocalName(reg+1, pc); ok {
			return "local", name
		}
		p := fn.Proto
		inst := symbexec(p, pc, reg)
		if inst == symbexecNeutral {
			return "", ""
		}
		switch opGetOpCode(inst) {
		case OP_GETGLOBAL:
			if s, ok := p.Constants[opGetArgBx(inst)].(LString); ok {
				return "global", string(s)
			}
			return "", ""
		case OP_MOVE, OP_MOVEN:
			a, b := opGetArgA(inst), opGetArgB(inst)
			if b < a {
				reg = b
				continue
			}
			return "", ""
		case OP_GETTABLE, OP_GETTABLEKS:
			return "field", kname(p, opGetArgC(inst))
		case OP_GETUPVAL:
			if b := opGetArgB(inst); b < len(p.DbgUpvalues) {
				return "upvalue", p.DbgUpvalues[b]
			}
			return "upvalue", "?"
		case OP_SELF:
			return "method", kname(p, opGetArgC(inst))
		}
		return "", ""
	}
	return "", ""
}

// getfuncname is Lua 5.1's getfuncname: the namewhat ("" for none) and
// the name of the function running in frame fr, from the instruction of
// its caller that called it. Under DialectLuaJIT, a metamethod called by
// an instruction is named as LuaJIT names it: namewhat "metamethod", the
// name of the event ("__index").
func (ls *LState) getfuncname(fr *callFrame) (string, string) {
	if fr == nil || (!fr.Fn.IsG && fr.TailCall > 0) {
		return "", ""
	}
	caller := fr.Parent
	if caller == nil || caller.Fn.IsG || caller.Pc <= 0 || caller.Pc > len(caller.Fn.Proto.Code) {
		return "", ""
	}
	pc := caller.Pc - 1
	inst := caller.Fn.Proto.Code[pc]
	op := opGetOpCode(inst)
	switch op {
	case OP_CALL, OP_TAILCALL, OP_TFORLOOP:
		return getobjname(caller.Fn, pc, opGetArgA(inst))
	}
	if ls.Dialect() == DialectLuaJIT {
		if event := opEvents[op]; event != "" {
			return "metamethod", event
		}
	}
	return "", ""
}

// opEvents are the metamethods the instructions call.
var opEvents = [...]string{
	OP_GETGLOBAL: "__index", OP_GETTABLE: "__index", OP_GETTABLEKS: "__index",
	OP_SELF: "__index", OP_SETGLOBAL: "__newindex", OP_SETTABLE: "__newindex",
	OP_SETTABLEKS: "__newindex", OP_ADD: "__add", OP_SUB: "__sub",
	OP_MUL: "__mul", OP_DIV: "__div", OP_MOD: "__mod", OP_POW: "__pow",
	OP_UNM: "__unm", OP_LEN: "__len", OP_CONCAT: "__concat", OP_EQ: "__eq",
	OP_LT: "__lt", OP_LE: "__le", OP_NOP: "",
}

// sameValue reports whether a and b are the same Lua value (a NaN is the
// same as itself).
func sameValue(a, b LValue) bool {
	if a == b {
		return true
	}
	x, ok1 := a.(LNumber)
	y, ok2 := b.(LNumber)
	return ok1 && ok2 && math.IsNaN(float64(x)) && math.IsNaN(float64(y))
}

// currentLuaInst is the frame, the pc and the instruction of the running
// function when it is a Lua function.
func (ls *LState) currentLuaInst() (*callFrame, int, uint32, bool) {
	cf := ls.currentFrame
	if cf == nil || cf.Fn.IsG || cf.Pc <= 0 || cf.Pc > len(cf.Fn.Proto.Code) {
		return nil, 0, 0, false
	}
	return cf, cf.Pc - 1, cf.Fn.Proto.Code[cf.Pc-1], true
}

// varinfo is the description Lua 5.1's luaG_typeerror gives value o, in
// register reg of the running Lua function (-1: not in a register): the
// kind and the name getobjname finds, or "". The register must hold o,
// unless o stands for a value Lua 5.1 keeps in the register and gopher-lua
// doesn't (any is set: the partial results of a concatenation).
func (ls *LState) varinfo(o LValue, reg int, any bool) (string, string) {
	if reg < 0 || reg >= opBitRk {
		return "", ""
	}
	cf, pc, _, ok := ls.currentLuaInst()
	if !ok || cf.LocalBase+reg >= len(ls.reg.array) || (!any && !sameValue(ls.reg.Get(cf.LocalBase+reg), o)) {
		return "", ""
	}
	return getobjname(cf.Fn, pc, reg)
}

// typeError is Lua 5.1's luaG_typeerror for value o in register reg of the
// running Lua function (-1: not in a register).
func (ls *LState) typeError(o LValue, op string, reg int) {
	ls.typeErrorAt(o, op, reg, false)
}

func (ls *LState) typeErrorAt(o LValue, op string, reg int, any bool) {
	if kind, name := ls.varinfo(o, reg, any); kind != "" {
		ls.RaiseError("attempt to %s %s '%s' (a %s value)", op, kind, name, o.Type().String())
	}
	ls.RaiseError("attempt to %s a %s value", op, o.Type().String())
}

// operandReg is the register RK index idx of the running instruction
// names, -1 for a constant.
func operandReg(idx int) int {
	if opIsK(idx) {
		return -1
	}
	return idx
}

// arithError is Lua 5.1's luaG_aritherror for lhs op rhs (rhs is lhs for
// the unary minus): the first operand that isn't a number (or a string
// convertible to one) is blamed.
func (ls *LState) arithError(lhs, rhs LValue) {
	blamed, isrhs := lhs, false
	if _, ok := toArithNumber(lhs); ok {
		blamed, isrhs = rhs, true
	}
	reg := -1
	if _, _, inst, ok := ls.currentLuaInst(); ok {
		switch opGetOpCode(inst) {
		case OP_ADD, OP_SUB, OP_MUL, OP_DIV, OP_MOD, OP_POW:
			if isrhs {
				reg = operandReg(opGetArgC(inst))
			} else {
				reg = operandReg(opGetArgB(inst))
			}
		case OP_UNM:
			reg = operandReg(opGetArgB(inst))
		}
	}
	ls.typeError(blamed, "perform arithmetic on", reg)
}

// toArithNumber is luaV_tonumber.
func toArithNumber(v LValue) (LNumber, bool) {
	switch lv := v.(type) {
	case LNumber:
		return lv, true
	case LString:
		if n, err := parseNumber(string(lv)); err == nil {
			return n, true
		}
	}
	return 0, false
}

// concatError is Lua 5.1's luaG_concaterror for lhs .. rhs, the values of
// the absolute registers lreg and rreg: lhs is blamed unless it is a
// string or a number.
func (ls *LState) concatError(lhs, rhs LValue, lreg, rreg int) {
	blamed, reg := lhs, lreg
	switch lhs.(type) {
	case LString, LNumber:
		blamed, reg = rhs, rreg
	}
	rel := -1
	if cf, _, inst, ok := ls.currentLuaInst(); ok && opGetOpCode(inst) == OP_CONCAT {
		rel = reg - cf.LocalBase
	}
	ls.typeErrorAt(blamed, "concatenate", rel, true)
}

// orderError is Lua 5.1's luaG_ordererror. Like Lua 5.1, it compares the
// third letters of the type names ("string" and "thread" are "two string
// values"); LuaJIT compares the types.
func (ls *LState) orderError(lhs, rhs LValue) {
	t1, t2 := lhs.Type().String(), rhs.Type().String()
	same := len(t1) > 2 && len(t2) > 2 && t1[2] == t2[2]
	if ls.Dialect() == DialectLuaJIT {
		same = t1 == t2
	}
	if same {
		ls.RaiseError("attempt to compare two %s values", t1)
	}
	ls.RaiseError("attempt to compare %s with %s", t1, t2)
}

// indexError is Lua 5.1's luaG_typeerror(t, "index") for the gettable and
// settable loops: first tells whether obj is the indexed value itself (in
// the instruction's register), not one an __index or __newindex chain led
// to.
func (ls *LState) indexError(obj LValue, first bool) {
	reg := -1
	if first {
		if _, _, inst, ok := ls.currentLuaInst(); ok {
			switch opGetOpCode(inst) {
			case OP_GETTABLE, OP_GETTABLEKS, OP_SELF:
				reg = opGetArgB(inst)
			case OP_SETTABLE, OP_SETTABLEKS:
				reg = opGetArgA(inst)
			}
		}
	}
	ls.typeError(obj, "index", reg)
}

// callError is Lua 5.1's luaG_typeerror(func, "call") for value fn at the
// absolute register base, the function slot of a call.
func (ls *LState) callError(fn LValue, base int) {
	reg := -1
	if cf, _, inst, ok := ls.currentLuaInst(); ok {
		switch opGetOpCode(inst) {
		case OP_CALL, OP_TAILCALL, OP_TFORLOOP:
			reg = base - cf.LocalBase
		}
	}
	ls.typeError(fn, "call", reg)
}

// argErrorName is the name of the function running in frame fr for argument
// errors (luaL_argerror): its namewhat and name, "?" for no name.
func (ls *LState) argErrorName(fr *callFrame) (string, string) {
	namewhat, name := ls.getfuncname(fr)
	if namewhat == "" {
		name = "?"
	}
	return namewhat, name
}
