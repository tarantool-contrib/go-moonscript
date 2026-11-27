package parse

// go-moonscript patch: a model of LuaJIT 2.1's code generator, the part of
// lj_parse.c that emits bytecode, run by the parser under DialectLuaJIT
// only. LuaJIT rejects code it cannot encode in its bytecode: a jump over
// more than 32767 instructions ("control structure too long"), a frame of
// more than 250 slots ("function or expression too complex"), more than
// 65536 constants, a return it cannot fix up after the first closure
// ("function too long for return fixup"). Which code that is depends on
// every instruction, register and constant LuaJIT emits, not on what
// compile.go emits, so the parser emits LuaJIT's bytecode alongside the
// syntax tree, with the same registers, constants and jumps, and drops it
// when a function ends. The names in the comments are those of the C
// functions.
//
// Adapted from go-luascope's parser/codegen.go and parser/bytecode.go
// (https://github.com/bigbes/go-luascope, BSD-2-Clause, Copyright (c) 2026,
// Eugene Blikh; see NOTICE), a port of lj_parse.c and lj_bc.h (LuaJIT,
// Copyright (C) 2005-2017 Mike Pall, MIT): instructions without their
// lines, only the number of the constants kept, the folding of "^" with
// math.Pow (go-luascope rounds it like glibc's pow, which only decides
// whether a folded power shares a constant with an equal one).

import "math"

// Limits of the code generator of LuaJIT (lj_def.h).
const (
	jitMaxSlots  = 250                 // LJ_MAX_SLOTS
	jitMaxBCIns  = 1 << 26             // LJ_MAX_BCINS
	jitMaxConst  = bcMaxD + 1          // BCMAX_D+1 constants of each kind
	jitMaxVstack = 65536 - maxUpvalues // LJ_MAX_VSTACK
	fr2          = 1                   // LJ_FR2: a call frame takes two slots (GC64)
	jitNoJump    = ^uint32(0)          // NO_JMP
	jitNoReg     = bcMaxA              // NO_REG
	jitBiasJ     = 0x8000              // BCBIAS_J
	bcMaxA       = 0xff                // BCMAX_A
	bcMaxB       = 0xff                // BCMAX_B
	bcMaxC       = 0xff                // BCMAX_C
	bcMaxD       = 0xffff              // BCMAX_D
	jitErrJump   = "control structure too long"
	jitErrSlots  = "function or expression too complex"
	jitErrFixup  = "function too long for return fixup"
	jitErrVstack = "chunk has more than 65476 local variables"
)

// Prototype flags (PROTO_*).
const (
	protoChild       = 0x01
	protoVararg      = 0x02
	protoHasReturn   = 0x20
	protoFixupReturn = 0x40
)

// bcOp is a LuaJIT opcode, in the order of BCDEF (lj_bc.h): opcodes are
// computed from each other, like a comparison from its inverse.
type bcOp uint32

const (
	bcISLT bcOp = iota
	bcISGE
	bcISLE
	bcISGT
	bcISEQV
	bcISNEV
	bcISEQS
	bcISNES
	bcISEQN
	bcISNEN
	bcISEQP
	bcISNEP
	bcISTC
	bcISFC
	bcIST
	bcISF
	bcISTYPE
	bcISNUM
	bcMOV
	bcNOT
	bcUNM
	bcLEN
	bcADDVN
	bcSUBVN
	bcMULVN
	bcDIVVN
	bcMODVN
	bcADDNV
	bcSUBNV
	bcMULNV
	bcDIVNV
	bcMODNV
	bcADDVV
	bcSUBVV
	bcMULVV
	bcDIVVV
	bcMODVV
	bcPOW
	bcCAT
	bcKSTR
	bcKCDATA
	bcKSHORT
	bcKNUM
	bcKPRI
	bcKNIL
	bcUGET
	bcUSETV
	bcUSETS
	bcUSETN
	bcUSETP
	bcUCLO
	bcFNEW
	bcTNEW
	bcTDUP
	bcGGET
	bcGSET
	bcTGETV
	bcTGETS
	bcTGETB
	bcTGETR
	bcTSETV
	bcTSETS
	bcTSETB
	bcTSETM
	bcTSETR
	bcCALLM
	bcCALL
	bcCALLMT
	bcCALLT
	bcITERC
	bcITERN
	bcVARG
	bcISNEXT
	bcRETM
	bcRET
	bcRET0
	bcRET1
	bcFORI
	bcJFORI
	bcFORL
	bcIFORL
	bcJFORL
	bcITERL
	bcIITERL
	bcJITERL
	bcLOOP
	bcILOOP
	bcJLOOP
	bcJMP
	bcFUNCF
	bcIFUNCF
	bcJFUNCF
	bcFUNCV
	bcIFUNCV
	bcJFUNCV
	bcFUNCC
	bcFUNCCW
)

// bcIns is an instruction: the opcode in bits 0-7, A in bits 8-15, and
// either C in bits 16-23 and B in bits 24-31 or D in bits 16-31.
type bcIns uint32

func (i bcIns) op() bcOp       { return bcOp(i & 0xff) }
func (i bcIns) a() uint32      { return uint32(i>>8) & 0xff }
func (i bcIns) c() uint32      { return uint32(i>>16) & 0xff }
func (i bcIns) d() uint32      { return uint32(i >> 16) }
func (i bcIns) j() int64       { return int64(i.d()) - jitBiasJ }
func (i *bcIns) setOp(x bcOp)  { *i = *i&^0xff | bcIns(x&0xff) }
func (i *bcIns) setA(x uint32) { *i = *i&^0xff00 | bcIns(x&0xff)<<8 }
func (i *bcIns) setB(x uint32) { *i = *i&^0xff000000 | bcIns(x&0xff)<<24 }
func (i *bcIns) setD(x uint32) { *i = *i&^0xffff0000 | bcIns(x&0xffff)<<16 }
func (i *bcIns) setJ(x int64)  { i.setD(uint32(x + jitBiasJ)) }

// insABC, insAD and insAJ compose instructions like BCINS_ABC, BCINS_AD
// and BCINS_AJ, with the same overflow of operands out of range.
func insABC(o bcOp, a, b, c uint32) bcIns { return bcIns(uint32(o) | a<<8 | b<<24 | c<<16) }
func insAD(o bcOp, a, d uint32) bcIns     { return bcIns(uint32(o) | a<<8 | d<<16) }
func insAJ(o bcOp, a uint32, j int64) bcIns {
	return insAD(o, a, uint32(j+jitBiasJ))
}

// isRetOrTail is bc_isret_or_tail.
func isRetOrTail(op bcOp) bool {
	switch op {
	case bcCALLMT, bcCALLT, bcRETM, bcRET, bcRET0, bcRET1:
		return true
	}
	return false
}

// expKind is the kind of an expression descriptor (ExpKind); the constants
// come first.
type expKind uint8

const (
	vKNil expKind = iota
	vKFalse
	vKTrue
	vKStr // sval is the string
	vKNum // nval is the number
	// vKCdata is an FFI numeral (1LL, 1ULL, 1i): not a constant expression
	vKCdata
	vLocal     // info is the register
	vUpval     // info is the upvalue index
	vGlobal    // sval is the name
	vIndexed   // info is the table register, aux the key
	vJmp       // info is the instruction
	vRelocable // info is the instruction
	vNonReloc  // info is the register
	vCall      // info is the instruction, aux the base
	vVoid
	vVararg // the model of Lua 5.1's registers only: VVARARG

	vKLast = vKNum
)

// expDesc is an expression descriptor (ExpDesc). Under DialectLua51 the
// parser passes them around unused.
type expDesc struct {
	k    expKind
	info uint32
	aux  uint32
	nval float64
	sval string
	t, f uint32 // the true and false jump lists
}

func (e *expDesc) init(k expKind, info uint32) {
	e.k, e.info, e.t, e.f = k, info, jitNoJump, jitNoJump
}

func (e *expDesc) hasJump() bool      { return e.t != e.f }
func (e *expDesc) isK() bool          { return e.k <= vKLast }
func (e *expDesc) isKNoJump() bool    { return e.isK() && !e.hasJump() }
func (e *expDesc) isNumK() bool       { return e.k == vKNum }
func (e *expDesc) isNumKNoJump() bool { return e.isNumK() && !e.hasJump() }

// jitFunc is the code generator's part of a FuncState.
type jitFunc struct {
	// bc holds the instructions up to pc: a part of parser.bcstack from
	// base on, as in LuaJIT a function's bytecode follows the bytecode of
	// the function enclosing it. lines are their lines (parser.lnstack).
	bc         []bcIns
	lines      []int32
	base       uint32
	pc         uint32
	lasttarget uint32 // the last jump target
	jpc        uint32 // the pending jumps to the next instruction
	freereg    uint32
	framesize  uint32
	flags      uint32
	numparams  uint32
	kn         map[float64]uint32
	kstr       map[string]uint32
	nkn, nkgc  uint32
	vbase      int // parser.vtop when the function starts
	// the register model of Lua 5.1 (lua51regs.go) has freereg, kstr, kn
	// and its constants in nkn, and the constants nil, false, true
	k51 [3]uint32
}

// maxPooledBytecode is the size of the largest bytecode stack kept for the
// next chunks, 512 KB.
const maxPooledBytecode = 1 << 17

func num2int(n float64) int32 { // lj_num2int on x86
	if n >= -(1<<31) && n < 1<<31 {
		return int32(n)
	}
	return math.MinInt32
}

// nactvar is the number of active locals.
func (fs *funcState) nactvar() uint32 { return uint32(len(fs.actvar)) }

func checki16(k int32) bool { return k == int32(int16(k)) }
func checku8(k int32) bool  { return k == int32(uint8(k)) }

/* functions */

// jitOpenFunc starts the code of a function (fs_init); the parser's own
// state is set up by openFunc.
func (p *parser) jitOpenFunc(fs *funcState) {
	fs.jpc = jitNoJump
	fs.framesize = 1 // the minimum frame size
	fs.vbase = p.vtop
	if prev := fs.prev; prev != nil {
		fs.base = prev.base + prev.pc
	}
	if int(fs.base) >= len(p.bcstack) {
		p.growBytecode()
	}
	fs.bc = p.bcstack[fs.base:fs.base:len(p.bcstack)]
	fs.lines = p.lnstack[fs.base:fs.base:len(p.lnstack)]
}

// jitCloseFunc ends a function (fs_finish): its final return, its scope
// and its limits. The current token is the function's 'end' or '<eof>'.
func (p *parser) jitCloseFunc() {
	fs := p.fs
	p.fixupReturn()
	if fs.nkn > jitMaxConst || fs.nkgc > jitMaxConst {
		p.errorLimit(fs, jitMaxConst, "constants")
	}
	p.vtop = fs.vbase
}

// fixupReturn adds the final return of a function and ends its scope
// (fs_fixup_ret). A function that returns before its first closure is
// created gets each of those returns replaced by a UCLO, which closes the
// upvalues, jumping to a copy of the return.
func (p *parser) fixupReturn() {
	fs := p.fs
	lastpc := fs.pc
	if lastpc <= fs.lasttarget || !isRetOrTail(fs.bc[lastpc-1].op()) {
		if fs.bl.upval {
			fs.emit(p, insAJ(bcUCLO, 0, 0))
		}
		fs.emit(p, insAD(bcRET0, 0, 1)) // a final return
	}
	fs.bl.noClose = true // closed above
	p.leaveBlock()
	if fs.flags&protoFixupReturn == 0 {
		return
	}
	for pc := uint32(1); pc < lastpc; pc++ {
		switch ins := fs.bc[pc]; ins.op() {
		case bcCALLMT, bcCALLT, bcRETM, bcRET, bcRET0, bcRET1:
			offset := fs.emit(p, ins) // a copy of the return
			fs.lines[offset] = fs.lines[pc]
			offset = offset - (pc + 1) + jitBiasJ
			if offset > bcMaxD {
				p.ls.syntaxError(jitErrFixup)
			}
			fs.bc[pc] = insAD(bcUCLO, 0, offset) // UCLO with the jump
		case bcFNEW:
			return // done
		}
	}
}

// growBytecode doubles the bytecode stack and moves the functions being
// parsed to the new one.
func (p *parser) growBytecode() {
	stack := make([]bcIns, max(2*len(p.bcstack), 1024))
	copy(stack, p.bcstack)
	p.bcstack = stack
	lines := make([]int32, len(stack))
	copy(lines, p.lnstack)
	p.lnstack = lines
	for fs := p.fs; fs != nil; fs = fs.prev {
		fs.bc = stack[fs.base : fs.base+uint32(len(fs.bc)) : len(stack)]
		fs.lines = lines[fs.base : fs.base+uint32(len(fs.lines)) : len(lines)]
	}
}

// vstackPush adds an entry to LuaJIT's variable stack (ls->vstack): every
// local, goto and label declared takes one until its function ends, so
// the stack limits those declared in a function and the functions
// enclosing it (var_new, gola_new).
func (p *parser) vstackPush() {
	p.vstackCheck()
	p.vtop++
}

func (p *parser) vstackCheck() {
	if p.vtop >= jitMaxVstack {
		p.ls.lexError(jitErrVstack, 0)
	}
}

/* constants */

// constNum adds a number constant (const_num); numbers equal as table
// keys share one, 0 and -0 do.
func (fs *funcState) constNum(n float64) uint32 {
	if k, ok := fs.kn[n]; ok {
		return k
	}
	if fs.kn == nil {
		fs.kn = map[float64]uint32{}
	}
	k := fs.nkn
	fs.kn[n] = k
	fs.nkn++
	return k
}

// constGC adds a garbage-collected constant (const_gc): a string, a
// prototype, a template table or cdata.
func (fs *funcState) constGC() uint32 {
	k := fs.nkgc
	fs.nkgc++
	return k
}

// constStr adds a string constant (const_str); equal strings share one.
func (fs *funcState) constStr(s string) uint32 {
	if k, ok := fs.kstr[s]; ok {
		return k
	}
	if fs.kstr == nil {
		fs.kstr = map[string]uint32{}
	}
	k := fs.constGC()
	fs.kstr[s] = k
	return k
}

/* jump lists */

// jmpNext is jmp_next.
func (fs *funcState) jmpNext(pc uint32) uint32 {
	delta := fs.bc[pc].j()
	if uint32(delta) == jitNoJump {
		return jitNoJump
	}
	return uint32(int64(pc) + 1 + delta)
}

// prevIns is the test before a conditional jump, or the jump itself at 0.
func prevIns(pc uint32) uint32 {
	if pc >= 1 {
		return pc - 1
	}
	return pc
}

// jmpNoValue reports whether a jump of the list produces no value
// (jmp_novalue).
func (fs *funcState) jmpNoValue(list uint32) bool {
	for ; list != jitNoJump; list = fs.jmpNext(list) {
		p := fs.bc[prevIns(list)]
		if p.op() != bcISTC && p.op() != bcISFC && p.a() != jitNoReg {
			return true
		}
	}
	return false
}

// jmpPatchTestReg is jmp_patchtestreg.
func (fs *funcState) jmpPatchTestReg(pc, reg uint32) bool {
	i := prevIns(pc)
	ip := &fs.bc[i]
	switch op := ip.op(); {
	case op == bcISTC || op == bcISFC:
		if reg != jitNoReg && reg != ip.d() {
			ip.setA(reg)
		} else { // nothing to store, or in the right register already
			ip.setOp(op + (bcIST - bcISTC))
			ip.setA(0)
		}
	case ip.a() == jitNoReg:
		if reg == jitNoReg {
			*ip = insAJ(bcJMP, fs.bc[pc].a(), 0)
		} else {
			ip.setA(reg)
			if next := &fs.bc[i+1]; reg >= next.a() {
				next.setA(reg + 1)
			}
		}
	default:
		return false // other instructions cannot be patched
	}
	return true
}

// jmpDropVal is jmp_dropval.
func (fs *funcState) jmpDropVal(list uint32) {
	for ; list != jitNoJump; list = fs.jmpNext(list) {
		fs.jmpPatchTestReg(list, jitNoReg)
	}
}

// jmpPatchIns is jmp_patchins: a jump's offset has 16 bits.
func (fs *funcState) jmpPatchIns(p *parser, pc, dest uint32) {
	offset := dest - (pc + 1) + jitBiasJ
	if offset > bcMaxD {
		p.ls.syntaxError(jitErrJump)
	}
	fs.bc[pc].setD(offset)
}

// jmpAppend is jmp_append.
func (fs *funcState) jmpAppend(p *parser, l1 *uint32, l2 uint32) {
	switch {
	case l2 == jitNoJump:
	case *l1 == jitNoJump:
		*l1 = l2
	default:
		list := *l1
		for next := fs.jmpNext(list); next != jitNoJump; next = fs.jmpNext(list) {
			list = next
		}
		fs.jmpPatchIns(p, list, l2)
	}
}

// jmpPatchVal is jmp_patchval.
func (fs *funcState) jmpPatchVal(p *parser, list, vtarget, reg, dtarget uint32) {
	for list != jitNoJump {
		next := fs.jmpNext(list)
		if fs.jmpPatchTestReg(list, reg) {
			fs.jmpPatchIns(p, list, vtarget) // to the target with a value
		} else {
			fs.jmpPatchIns(p, list, dtarget) // to the default target
		}
		list = next
	}
}

// jmpToHere is jmp_tohere.
func (fs *funcState) jmpToHere(p *parser, list uint32) {
	fs.lasttarget = fs.pc
	fs.jmpAppend(p, &fs.jpc, list)
}

// jmpPatch is jmp_patch.
func (fs *funcState) jmpPatch(p *parser, list, target uint32) {
	if target == fs.pc {
		fs.jmpToHere(p, list)
	} else {
		fs.jmpPatchVal(p, list, target, jitNoReg, target)
	}
}

/* registers */

// regBump is bcreg_bump.
func (fs *funcState) regBump(p *parser, n uint32) {
	if sz := fs.freereg + n; sz > fs.framesize {
		if sz >= jitMaxSlots {
			p.ls.syntaxError(jitErrSlots)
		}
		fs.framesize = sz
	}
}

// regReserve is bcreg_reserve.
func (fs *funcState) regReserve(p *parser, n uint32) {
	fs.regBump(p, n)
	fs.freereg += n
}

// regFree is bcreg_free.
func (fs *funcState) regFree(reg uint32) {
	if reg >= fs.nactvar() {
		fs.freereg--
	}
}

// exprFree is expr_free.
func (fs *funcState) exprFree(e *expDesc) {
	if e.k == vNonReloc {
		fs.regFree(e.info)
	}
}

/* emitter */

// emit is bcemit_INS: it patches the pending jumps to the instruction.
func (fs *funcState) emit(p *parser, ins bcIns) uint32 {
	pc := fs.pc
	fs.jmpPatchVal(p, fs.jpc, pc, jitNoReg, pc)
	fs.jpc = jitNoJump
	// a function and the functions enclosing it share one stack of
	// LJ_MAX_BCINS instructions
	if fs.base+pc >= jitMaxBCIns {
		p.errorLimit(fs, jitMaxBCIns, "bytecode instructions")
	}
	if int(pc) >= len(fs.bc) {
		if int(pc) >= cap(fs.bc) {
			p.growBytecode()
		}
		fs.bc = fs.bc[:pc+1]
		fs.lines = fs.lines[:pc+1]
	}
	fs.bc[pc] = ins
	fs.lines[pc] = int32(p.ls.lastline) // the line of the previous token
	fs.pc = pc + 1
	return pc
}

// discharge is expr_discharge.
func (fs *funcState) discharge(p *parser, e *expDesc) {
	var ins bcIns
	switch e.k {
	case vUpval:
		ins = insAD(bcUGET, 0, e.info)
	case vGlobal:
		ins = insAD(bcGGET, 0, fs.constStr(e.sval))
	case vIndexed:
		switch rc := e.aux; {
		case int32(rc) < 0:
			ins = insABC(bcTGETS, 0, e.info, ^rc)
		case rc > bcMaxC:
			ins = insABC(bcTGETB, 0, e.info, rc-(bcMaxC+1))
		default:
			fs.regFree(rc)
			ins = insABC(bcTGETV, 0, e.info, rc)
		}
		fs.regFree(e.info)
	case vCall:
		e.info = e.aux
		e.k = vNonReloc
		return
	case vLocal:
		e.k = vNonReloc
		return
	default:
		return
	}
	e.info = fs.emit(p, ins)
	e.k = vRelocable
}

// emitNil is bcemit_nil.
func (fs *funcState) emitNil(p *parser, from, n uint32) {
	if fs.mergeNil(from, n) {
		return
	}
	if n == 1 {
		fs.emit(p, insAD(bcKPRI, from, uint32(vKNil)))
	} else {
		fs.emit(p, insAD(bcKNIL, from, from+n-1))
	}
}

// mergeNil merges the setting of registers to nil with the previous
// instruction if no jump targets the current position (bcemit_nil).
func (fs *funcState) mergeNil(from, n uint32) bool {
	if fs.pc <= fs.lasttarget {
		return false
	}
	ip := &fs.bc[fs.pc-1]
	pfrom := ip.a()
	switch ip.op() {
	case bcKPRI:
		if ip.d() != uint32(vKNil) {
			return false
		}
		switch from {
		case pfrom:
			if n == 1 {
				return true
			}
		case pfrom + 1:
			from = pfrom
			n++
		default:
			return false
		}
		*ip = insAD(bcKNIL, from, from+n-1) // replaces the KPRI
		return true
	case bcKNIL:
		pto := ip.d()
		if from < pfrom || from > pto+1 { // the ranges do not connect
			return false
		}
		if from+n-1 > pto {
			ip.setD(from + n - 1) // extends the KNIL
		}
		return true
	}
	return false
}

// toRegNoBranch is expr_toreg_nobranch.
func (fs *funcState) toRegNoBranch(p *parser, e *expDesc, reg uint32) {
	var ins bcIns
	fs.discharge(p, e)
	switch {
	case e.k == vKStr:
		ins = insAD(bcKSTR, reg, fs.constStr(e.sval))
	case e.k == vKNum:
		if k := num2int(e.nval); checki16(k) && e.nval == float64(k) {
			ins = insAD(bcKSHORT, reg, uint32(uint16(k)))
		} else {
			ins = insAD(bcKNUM, reg, fs.constNum(e.nval))
		}
	case e.k == vKCdata:
		ins = insAD(bcKCDATA, reg, fs.constGC())
	case e.k == vRelocable:
		fs.bc[e.info].setA(reg)
		e.info, e.k = reg, vNonReloc
		return
	case e.k == vNonReloc:
		if reg == e.info {
			return
		}
		ins = insAD(bcMOV, reg, e.info)
	case e.k == vKNil:
		fs.emitNil(p, reg, 1)
		e.info, e.k = reg, vNonReloc
		return
	case e.k <= vKTrue:
		ins = insAD(bcKPRI, reg, uint32(e.k))
	default: // vVoid or vJmp
		return
	}
	fs.emit(p, ins)
	e.info, e.k = reg, vNonReloc
}

// toReg is expr_toreg.
func (fs *funcState) toReg(p *parser, e *expDesc, reg uint32) {
	fs.toRegNoBranch(p, e, reg)
	if e.k == vJmp {
		fs.jmpAppend(p, &e.t, e.info) // to the true list
	}
	if e.hasJump() { // an expression with branches
		jfalse, jtrue := jitNoJump, jitNoJump
		if fs.jmpNoValue(e.t) || fs.jmpNoValue(e.f) {
			jval := jitNoJump
			if e.k != vJmp {
				jval = fs.jmp(p)
			}
			jfalse = fs.emit(p, insAD(bcKPRI, reg, uint32(vKFalse)))
			fs.emit(p, insAJ(bcJMP, fs.freereg, 1))
			jtrue = fs.emit(p, insAD(bcKPRI, reg, uint32(vKTrue)))
			fs.jmpToHere(p, jval)
		}
		jend := fs.pc
		fs.lasttarget = jend
		fs.jmpPatchVal(p, e.f, jend, reg, jfalse)
		fs.jmpPatchVal(p, e.t, jend, reg, jtrue)
	}
	e.f, e.t = jitNoJump, jitNoJump
	e.info, e.k = reg, vNonReloc
}

// toNextReg is expr_tonextreg.
func (fs *funcState) toNextReg(p *parser, e *expDesc) {
	fs.discharge(p, e)
	fs.exprFree(e)
	fs.regReserve(p, 1)
	fs.toReg(p, e, fs.freereg-1)
}

// toAnyReg is expr_toanyreg.
func (fs *funcState) toAnyReg(p *parser, e *expDesc) uint32 {
	fs.discharge(p, e)
	if e.k == vNonReloc {
		if !e.hasJump() {
			return e.info // in a register already
		}
		if e.info >= fs.nactvar() {
			fs.toReg(p, e, e.info) // to its temporary register
			return e.info
		}
	}
	fs.toNextReg(p, e)
	return e.info
}

// toVal is expr_toval.
func (fs *funcState) toVal(p *parser, e *expDesc) {
	if e.hasJump() {
		fs.toAnyReg(p, e)
	} else {
		fs.discharge(p, e)
	}
}

// store is bcemit_store.
func (fs *funcState) store(p *parser, v, e *expDesc) {
	var ins bcIns
	switch v.k {
	case vLocal:
		fs.exprFree(e)
		fs.toReg(p, e, v.info)
		return
	case vUpval:
		fs.toVal(p, e)
		switch {
		case e.k <= vKTrue:
			ins = insAD(bcUSETP, v.info, uint32(e.k))
		case e.k == vKStr:
			ins = insAD(bcUSETS, v.info, fs.constStr(e.sval))
		case e.k == vKNum:
			ins = insAD(bcUSETN, v.info, fs.constNum(e.nval))
		default:
			ins = insAD(bcUSETV, v.info, fs.toAnyReg(p, e))
		}
	case vGlobal:
		ra := fs.toAnyReg(p, e)
		ins = insAD(bcGSET, ra, fs.constStr(v.sval))
	default: // vIndexed
		ra := fs.toAnyReg(p, e)
		switch rc := v.aux; {
		case int32(rc) < 0:
			ins = insABC(bcTSETS, ra, v.info, ^rc)
		case rc > bcMaxC:
			ins = insABC(bcTSETB, ra, v.info, rc-(bcMaxC+1))
		default:
			ins = insABC(bcTSETV, ra, v.info, rc)
		}
	}
	fs.emit(p, ins)
	fs.exprFree(e)
}

// method is bcemit_method: the method and the object as the 1st argument.
func (fs *funcState) method(p *parser, e *expDesc, key string) {
	obj := fs.toAnyReg(p, e)
	fs.exprFree(e)
	fn := fs.freereg
	fs.emit(p, insAD(bcMOV, fn+1+fr2, obj))
	if idx := fs.constStr(key); idx <= bcMaxC {
		fs.regReserve(p, 2+fr2)
		fs.emit(p, insABC(bcTGETS, fn, obj, idx))
	} else {
		fs.regReserve(p, 3+fr2)
		fs.emit(p, insAD(bcKSTR, fn+2+fr2, idx))
		fs.emit(p, insABC(bcTGETV, fn, obj, fn+2+fr2))
		fs.freereg--
	}
	e.info, e.k = fn, vNonReloc
}

// index is expr_index: the key is a constant string or byte, or a
// register.
func (fs *funcState) index(p *parser, t, e *expDesc) {
	t.k = vIndexed
	switch e.k {
	case vKNum:
		if k := num2int(e.nval); checku8(k) && e.nval == float64(k) {
			t.aux = bcMaxC + 1 + uint32(k) // 256..511: a constant byte
			return
		}
	case vKStr:
		if idx := fs.constStr(e.sval); idx <= bcMaxC {
			t.aux = ^idx // a constant string
			return
		}
	}
	t.aux = fs.toAnyReg(p, e) // 0..255: a register
}

/* branches */

// jmp is bcemit_jmp: a jump right after a UCLO is the UCLO.
func (fs *funcState) jmp(p *parser) uint32 {
	jpc := fs.jpc
	j := fs.pc - 1
	fs.jpc = jitNoJump
	if ip := &fs.bc[j]; int32(j) >= int32(fs.lasttarget) && ip.op() == bcUCLO {
		ip.setJ(-1) // NO_JMP
		fs.lasttarget = j + 1
	} else {
		j = fs.emit(p, insAJ(bcJMP, fs.freereg, -1))
	}
	fs.jmpAppend(p, &j, jpc)
	return j
}

// invertCond is invertcond.
func (fs *funcState) invertCond(e *expDesc) {
	ip := &fs.bc[e.info-1]
	ip.setOp(ip.op() ^ 1)
}

// branch is bcemit_branch.
func (fs *funcState) branch(p *parser, e *expDesc, cond bool) uint32 {
	if e.k == vRelocable {
		if ip := &fs.bc[e.info]; ip.op() == bcNOT {
			op := bcIST
			if cond {
				op = bcISF
			}
			*ip = insAD(op, 0, ip.d())
			return fs.jmp(p)
		}
	}
	if e.k != vNonReloc {
		fs.regReserve(p, 1)
		fs.toRegNoBranch(p, e, fs.freereg-1)
	}
	op := bcISFC
	if cond {
		op = bcISTC
	}
	fs.emit(p, insAD(op, jitNoReg, e.info))
	pc := fs.jmp(p)
	fs.exprFree(e)
	return pc
}

// branchT is bcemit_branch_t: a jump if false.
func (fs *funcState) branchT(p *parser, e *expDesc) {
	var pc uint32
	fs.discharge(p, e)
	switch e.k {
	case vKStr, vKNum, vKTrue:
		pc = jitNoJump // never jumps
	case vJmp:
		fs.invertCond(e)
		pc = e.info
	case vKFalse, vKNil:
		fs.toRegNoBranch(p, e, jitNoReg)
		pc = fs.jmp(p)
	default:
		pc = fs.branch(p, e, false)
	}
	fs.jmpAppend(p, &e.f, pc)
	fs.jmpToHere(p, e.t)
	e.t = jitNoJump
}

// branchF is bcemit_branch_f: a jump if true.
func (fs *funcState) branchF(p *parser, e *expDesc) {
	var pc uint32
	fs.discharge(p, e)
	switch e.k {
	case vKNil, vKFalse:
		pc = jitNoJump // never jumps
	case vJmp:
		pc = e.info
	case vKStr, vKNum, vKTrue:
		fs.toRegNoBranch(p, e, jitNoReg)
		pc = fs.jmp(p)
	default:
		pc = fs.branch(p, e, true)
	}
	fs.jmpAppend(p, &e.t, pc)
	fs.jmpToHere(p, e.f)
	e.f = jitNoJump
}

/* operators */

// jitBinOp is a binary operator (BinOpr), in the order the opcodes of the
// arithmetic and comparison operators are computed from.
type jitBinOp int

const (
	binAdd jitBinOp = iota
	binSub
	binMul
	binDiv
	binMod
	binPow
	binConcat
	binNe
	binEq
	binLt
	binGe
	binLe
	binGt
	binAnd
	binOr
)

// foldArith is foldarith.
func foldArith(opr jitBinOp, e1, e2 *expDesc) bool {
	if !e1.isNumKNoJump() || !e2.isNumKNoJump() {
		return false
	}
	x, y := e1.nval, e2.nval
	var n float64
	switch opr {
	case binAdd:
		n = x + y
	case binSub:
		n = x - y
	case binMul:
		n = x * y
	case binDiv:
		n = x / y
	case binMod:
		n = x - float64(math.Floor(x/y)*y) // the conversion prevents a fused multiply-add
	default:
		n = math.Pow(x, y)
	}
	if math.IsNaN(n) || n == 0 && math.Signbit(n) {
		return false // no NaN or -0 constants
	}
	e1.nval = n
	return true
}

// arith is bcemit_arith.
func (fs *funcState) arith(p *parser, opr jitBinOp, e1, e2 *expDesc) {
	var rb, rc uint32
	var op bcOp
	if foldArith(opr, e1, e2) {
		return
	}
	if opr == binPow {
		op = bcPOW
		rc = fs.toAnyReg(p, e2)
		rb = fs.toAnyReg(p, e1)
	} else {
		op = bcOp(opr-binAdd) + bcADDVV
		// the 2nd operand first: an indexed one may free registers
		fs.toVal(p, e2)
		if e2.isNumK() {
			rc = fs.constNum(e2.nval)
		}
		if e2.isNumK() && rc <= bcMaxC {
			op -= bcADDVV - bcADDVN
		} else {
			rc = fs.toAnyReg(p, e2)
		}
		// the 1st operand is discharged by binopLeft, but may need KNUM
		fs.toVal(p, e1)
		var t uint32
		if e1.isNumK() && !e2.isNumK() {
			t = fs.constNum(e1.nval)
		}
		if e1.isNumK() && !e2.isNumK() && t <= bcMaxB {
			rb, rc = rc, t
			op -= bcADDVV - bcADDNV
		} else {
			rb = fs.toAnyReg(p, e1)
		}
	}
	fs.freeOperands(e1, e2)
	e1.info = fs.emit(p, insABC(op, 0, rb, rc))
	e1.k = vRelocable
}

// freeOperands frees the registers of the operands in LuaJIT's order.
func (fs *funcState) freeOperands(e1, e2 *expDesc) {
	if e1.k == vNonReloc && e1.info >= fs.nactvar() {
		fs.freereg--
	}
	if e2.k == vNonReloc && e2.info >= fs.nactvar() {
		fs.freereg--
	}
}

// comp is bcemit_comp.
func (fs *funcState) comp(p *parser, opr jitBinOp, e1, e2 *expDesc) {
	var ins bcIns
	eret := e1
	fs.toVal(p, e1)
	if opr == binEq || opr == binNe {
		op := bcISEQV
		if opr == binNe {
			op = bcISNEV
		}
		if e1.isK() {
			e1, e2 = e2, eret // the constant as the 2nd operand
		}
		ra := fs.toAnyReg(p, e1) // the 1st operand in a register
		fs.toVal(p, e2)
		switch e2.k {
		case vKNil, vKFalse, vKTrue:
			ins = insAD(op+(bcISEQP-bcISEQV), ra, uint32(e2.k))
		case vKStr:
			ins = insAD(op+(bcISEQS-bcISEQV), ra, fs.constStr(e2.sval))
		case vKNum:
			ins = insAD(op+(bcISEQN-bcISEQV), ra, fs.constNum(e2.nval))
		default:
			ins = insAD(op, ra, fs.toAnyReg(p, e2))
		}
	} else {
		var ra, rd uint32
		op := bcOp(opr-binLt) + bcISLT
		if (op-bcISLT)&1 != 0 { // GT -> LT, GE -> LE: swapped operands
			e1, e2 = e2, eret
			op = ((op - bcISLT) ^ 3) + bcISLT
			fs.toVal(p, e1)
			ra = fs.toAnyReg(p, e1)
			rd = fs.toAnyReg(p, e2)
		} else {
			rd = fs.toAnyReg(p, e2)
			ra = fs.toAnyReg(p, e1)
		}
		ins = insAD(op, ra, rd)
	}
	fs.freeOperands(e1, e2)
	fs.emit(p, ins)
	eret.info = fs.jmp(p)
	eret.k = vJmp
}

// binopLeft is bcemit_binop_left.
func (fs *funcState) binopLeft(p *parser, opr jitBinOp, e *expDesc) {
	switch opr {
	case binAnd:
		fs.branchT(p, e)
	case binOr:
		fs.branchF(p, e)
	case binConcat:
		fs.toNextReg(p, e)
	case binEq, binNe:
		if !e.isKNoJump() {
			fs.toAnyReg(p, e)
		}
	default:
		if !e.isNumKNoJump() {
			fs.toAnyReg(p, e)
		}
	}
}

// binop is bcemit_binop.
func (fs *funcState) binop(p *parser, opr jitBinOp, e1, e2 *expDesc) {
	switch opr {
	case binAdd, binSub, binMul, binDiv, binMod, binPow:
		fs.arith(p, opr, e1, e2)
	case binAnd:
		fs.discharge(p, e2)
		fs.jmpAppend(p, &e2.f, e1.f)
		*e1 = *e2
	case binOr:
		fs.discharge(p, e2)
		fs.jmpAppend(p, &e2.t, e1.t)
		*e1 = *e2
	case binConcat:
		fs.toVal(p, e2)
		if e2.k == vRelocable && fs.bc[e2.info].op() == bcCAT {
			fs.exprFree(e1)
			fs.bc[e2.info].setB(e1.info)
			e1.info = e2.info
		} else {
			fs.toNextReg(p, e2)
			fs.exprFree(e2)
			fs.exprFree(e1)
			e1.info = fs.emit(p, insABC(bcCAT, 0, e1.info, e2.info))
		}
		e1.k = vRelocable
	default:
		fs.comp(p, opr, e1, e2)
	}
}

// unop is bcemit_unop.
func (fs *funcState) unop(p *parser, op bcOp, e *expDesc) {
	if op == bcNOT {
		e.f, e.t = e.t, e.f // swaps the true and false lists
		fs.jmpDropVal(e.f)
		fs.jmpDropVal(e.t)
		fs.discharge(p, e)
		switch {
		case e.k == vKNil || e.k == vKFalse:
			e.k = vKTrue
			return
		case e.isK() || e.k == vKCdata:
			e.k = vKFalse
			return
		case e.k == vJmp:
			fs.invertCond(e)
			return
		case e.k == vRelocable:
			fs.regReserve(p, 1)
			fs.bc[e.info].setA(fs.freereg - 1)
			e.info, e.k = fs.freereg-1, vNonReloc
		}
	} else {
		if op == bcUNM && !e.hasJump() { // folds negated constants
			if e.k == vKCdata {
				return // folded in place: cdata is not interned
			}
			if e.isNumK() && e.nval != 0 { // not to -0
				e.nval = -e.nval
				return
			}
		}
		fs.toAnyReg(p, e)
	}
	fs.exprFree(e)
	e.info = fs.emit(p, insAD(op, 0, e.info))
	e.k = vRelocable
}
