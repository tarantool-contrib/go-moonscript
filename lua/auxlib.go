package lua

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

/* checkType {{{ */

func (ls *LState) CheckAny(n int) LValue {
	if n > ls.GetTop() {
		ls.ArgError(n, "value expected")
	}
	return ls.Get(n)
}

// go-moonscript patch: as luaL_checkinteger, luaL_optinteger,
// luaL_optnumber (and luaL_checknumber already did), the integer and number
// arguments may be strings that convert to numbers, and, as luaL_optstring
// (and CheckString already did), a string argument may be a number.
func argNumber(v LValue) (LNumber, bool) {
	switch lv := v.(type) {
	case LNumber:
		return lv, true
	case LString:
		if num, err := parseNumber(string(lv)); err == nil {
			return num, true
		}
	}
	return 0, false
}

func (ls *LState) CheckInt(n int) int {
	v := ls.Get(n)
	if intv, ok := argNumber(v); ok {
		return int(intv)
	}
	ls.TypeError(n, LTNumber)
	return 0
}

func (ls *LState) CheckInt64(n int) int64 {
	v := ls.Get(n)
	if intv, ok := argNumber(v); ok {
		return int64(intv)
	}
	ls.TypeError(n, LTNumber)
	return 0
}

func (ls *LState) CheckNumber(n int) LNumber {
	v := ls.Get(n)
	if lv, ok := v.(LNumber); ok {
		return lv
	}
	if lv, ok := v.(LString); ok {
		if num, err := parseNumber(string(lv)); err == nil {
			return num
		}
	}
	ls.TypeError(n, LTNumber)
	return 0
}

func (ls *LState) CheckString(n int) string {
	v := ls.Get(n)
	if lv, ok := v.(LString); ok {
		return string(lv)
	} else if LVCanConvToString(v) {
		return ls.ToString(n)
	}
	ls.TypeError(n, LTString)
	return ""
}

func (ls *LState) CheckBool(n int) bool {
	v := ls.Get(n)
	if lv, ok := v.(LBool); ok {
		return bool(lv)
	}
	ls.TypeError(n, LTBool)
	return false
}

func (ls *LState) CheckTable(n int) *LTable {
	v := ls.Get(n)
	if lv, ok := v.(*LTable); ok {
		return lv
	}
	ls.TypeError(n, LTTable)
	return nil
}

// checkWritableTable is CheckTable for a table the function changes: a
// frozen table raises "attempt to modify a read-only table" (go-moonscript
// patch: LTable.Freeze).
func (ls *LState) checkWritableTable(n int) *LTable {
	tb := ls.CheckTable(n)
	if tb.Frozen() {
		ls.frozenTableError()
	}
	return tb
}

func (ls *LState) CheckFunction(n int) *LFunction {
	v := ls.Get(n)
	if lv, ok := v.(*LFunction); ok {
		return lv
	}
	ls.TypeError(n, LTFunction)
	return nil
}

func (ls *LState) CheckUserData(n int) *LUserData {
	v := ls.Get(n)
	if lv, ok := v.(*LUserData); ok {
		return lv
	}
	ls.TypeError(n, LTUserData)
	return nil
}

func (ls *LState) CheckThread(n int) *LState {
	v := ls.Get(n)
	if lv, ok := v.(*LState); ok {
		return lv
	}
	ls.TypeError(n, LTThread)
	return nil
}

func (ls *LState) CheckType(n int, typ LValueType) {
	v := ls.Get(n)
	if v.Type() != typ {
		ls.TypeError(n, typ)
	}
}

func (ls *LState) CheckTypes(n int, typs ...LValueType) {
	vt := ls.Get(n).Type()
	for _, typ := range typs {
		if vt == typ {
			return
		}
	}
	buf := []string{}
	for _, typ := range typs {
		buf = append(buf, typ.String())
	}
	ls.ArgError(n, strings.Join(buf, " or ")+" expected, got "+ls.typeName(n)) // go-moonscript patch: "no value"
}

func (ls *LState) CheckOption(n int, options []string) int {
	str := ls.CheckString(n)
	for i, v := range options {
		if v == str {
			return i
		}
	}
	ls.ArgError(n, fmt.Sprintf("invalid option: %s (must be one of %s)", str, strings.Join(options, ",")))
	return 0
}

/* }}} */

/* optType {{{ */

func (ls *LState) OptInt(n int, d int) int {
	v := ls.Get(n)
	if v == LNil {
		return d
	}
	if intv, ok := argNumber(v); ok {
		return int(intv)
	}
	ls.TypeError(n, LTNumber)
	return 0
}

func (ls *LState) OptInt64(n int, d int64) int64 {
	v := ls.Get(n)
	if v == LNil {
		return d
	}
	if intv, ok := argNumber(v); ok {
		return int64(intv)
	}
	ls.TypeError(n, LTNumber)
	return 0
}

func (ls *LState) OptNumber(n int, d LNumber) LNumber {
	v := ls.Get(n)
	if v == LNil {
		return d
	}
	if lv, ok := argNumber(v); ok {
		return lv
	}
	ls.TypeError(n, LTNumber)
	return 0
}

func (ls *LState) OptString(n int, d string) string {
	v := ls.Get(n)
	if v == LNil {
		return d
	}
	if lv, ok := v.(LString); ok {
		return string(lv)
	} else if lv, ok := v.(LNumber); ok {
		return lv.String()
	}
	ls.TypeError(n, LTString)
	return ""
}

func (ls *LState) OptBool(n int, d bool) bool {
	v := ls.Get(n)
	if v == LNil {
		return d
	}
	if lv, ok := v.(LBool); ok {
		return bool(lv)
	}
	ls.TypeError(n, LTBool)
	return false
}

func (ls *LState) OptTable(n int, d *LTable) *LTable {
	v := ls.Get(n)
	if v == LNil {
		return d
	}
	if lv, ok := v.(*LTable); ok {
		return lv
	}
	ls.TypeError(n, LTTable)
	return nil
}

func (ls *LState) OptFunction(n int, d *LFunction) *LFunction {
	v := ls.Get(n)
	if v == LNil {
		return d
	}
	if lv, ok := v.(*LFunction); ok {
		return lv
	}
	ls.TypeError(n, LTFunction)
	return nil
}

func (ls *LState) OptUserData(n int, d *LUserData) *LUserData {
	v := ls.Get(n)
	if v == LNil {
		return d
	}
	if lv, ok := v.(*LUserData); ok {
		return lv
	}
	ls.TypeError(n, LTUserData)
	return nil
}

/* }}} */

/* error operations {{{ */

// ArgError is luaL_argerror (go-moonscript patch: Lua 5.1's message: the
// function's name quoted, '?' when it has none, as debug.getinfo gives it;
// for a method, the self argument isn't counted).
func (ls *LState) ArgError(n int, message string) {
	if ls.currentFrame == nil {
		ls.RaiseError("bad argument #%d (%s)", n, message)
	}
	namewhat, name := ls.argErrorName(ls.currentFrame)
	if namewhat == "method" {
		n--
		if n == 0 {
			ls.RaiseError("calling '%s' on bad self (%s)", name, message)
		}
	}
	ls.RaiseError("bad argument #%d to '%s' (%s)", n, name, message)
}

// TypeError is luaL_typerror (go-moonscript patch: "no value" for a
// missing argument, as in Lua 5.1).
func (ls *LState) TypeError(n int, typ LValueType) {
	ls.ArgError(n, typ.String()+" expected, got "+ls.typeName(n))
}

// typeName is luaL_typename: the type name of argument n, "no value" if
// there is none (go-moonscript patch).
func (ls *LState) typeName(n int) string {
	if n > 0 && n > ls.GetTop() {
		return "no value"
	}
	return ls.Get(n).Type().String()
}

/* }}} */

/* debug operations {{{ */

func (ls *LState) Where(level int) string {
	return ls.where(level, false)
}

/* }}} */

/* table operations {{{ */

func (ls *LState) FindTable(obj *LTable, n string, size int) LValue {
	names := strings.Split(n, ".")
	curobj := obj
	for _, name := range names {
		if curobj.Type() != LTTable {
			return LNil
		}
		nextobj := ls.RawGet(curobj, LString(name))
		if nextobj == LNil {
			tb := ls.CreateTable(0, size)
			ls.RawSet(curobj, LString(name), tb)
			curobj = tb
		} else if nextobj.Type() != LTTable {
			return LNil
		} else {
			curobj = nextobj.(*LTable)
		}
	}
	return curobj
}

/* }}} */

/* register operations {{{ */

func (ls *LState) RegisterModule(name string, funcs map[string]LGFunction) LValue {
	tb := ls.FindTable(ls.Get(RegistryIndex).(*LTable), "_LOADED", 1)
	mod := ls.GetField(tb, name)
	if mod.Type() != LTTable {
		newmod := ls.FindTable(ls.Get(GlobalsIndex).(*LTable), name, len(funcs))
		if newmodtb, ok := newmod.(*LTable); !ok {
			ls.RaiseError("name conflict for module(%v)", name)
		} else {
			for _, fname := range funcNames(funcs) {
				newmodtb.RawSetString(fname, ls.NewFunction(funcs[fname]))
			}
			ls.SetField(tb, name, newmodtb)
			return newmodtb
		}
	}
	return mod
}

func (ls *LState) SetFuncs(tb *LTable, funcs map[string]LGFunction, upvalues ...LValue) *LTable {
	for _, fname := range funcNames(funcs) {
		tb.RawSetString(fname, ls.NewClosure(funcs[fname], upvalues...))
	}
	return tb
}

// funcNames returns the names of funcs sorted (go-moonscript patch: the
// functions are added to their table in this order, not Go's random map
// order, so that the iteration order of the table is the same on every
// run).
func funcNames(funcs map[string]LGFunction) []string {
	return slices.Sorted(maps.Keys(funcs))
}

/* }}} */

/* metatable operations {{{ */

func (ls *LState) NewTypeMetatable(typ string) *LTable {
	regtable := ls.Get(RegistryIndex)
	mt := ls.GetField(regtable, typ)
	if tb, ok := mt.(*LTable); ok {
		return tb
	}
	mtnew := ls.NewTable()
	ls.SetField(regtable, typ, mtnew)
	return mtnew
}

func (ls *LState) GetMetaField(obj LValue, event string) LValue {
	return ls.metaOp1(obj, event)
}

func (ls *LState) GetTypeMetatable(typ string) LValue {
	return ls.GetField(ls.Get(RegistryIndex), typ)
}

func (ls *LState) CallMeta(obj LValue, event string) LValue {
	op := ls.metaOp1(obj, event)
	if op.Type() == LTFunction {
		ls.reg.Push(op)
		ls.reg.Push(obj)
		ls.Call(1, 1)
		return ls.reg.Pop()
	}
	return LNil
}

/* }}} */

/* load and function call operations {{{ */

// LoadFile loads the chunk in file path, or in the standard input (the io
// library's) if path is "". go-moonscript patch: it is luaL_loadfile, the
// chunk named "@" + path ("=stdin"), so its errors show the path and
// debug.getinfo its source as in Lua; a first line starting with '#' is
// skipped, keeping the line numbers (and a binary chunk after it loads
// under DialectLua51); a file that cannot be read is the ApiErrorFile
// "cannot open <path>: <strerror>" ("cannot read ..."), its Cause the Go
// error (chunkname.go).
func (ls *LState) LoadFile(path string) (*LFunction, error) {
	return ls.loadFile(path, path == "")
}

func (ls *LState) LoadString(source string) (*LFunction, error) {
	return ls.Load(strings.NewReader(source), "<string>")
}

func (ls *LState) DoFile(path string) error {
	if fn, err := ls.LoadFile(path); err != nil {
		return err
	} else {
		ls.Push(fn)
		return ls.PCall(0, MultRet, nil)
	}
}

func (ls *LState) DoString(source string) error {
	if fn, err := ls.LoadString(source); err != nil {
		return err
	} else {
		ls.Push(fn)
		return ls.PCall(0, MultRet, nil)
	}
}

/* }}} */

/* GopherLua original APIs {{{ */

// ToStringMeta returns string representation of given LValue.
// This method calls the `__tostring` meta method if defined.
func (ls *LState) ToStringMeta(lv LValue) LValue {
	if fn, ok := ls.metaOp1(lv, "__tostring").(*LFunction); ok {
		ls.Push(fn)
		ls.Push(lv)
		ls.Call(1, 1)
		return ls.reg.Pop()
	} else if n, ok := lv.(LNumber); ok {
		return LString(ls.numberString(n)) // go-moonscript patch: the dialect's
	} else {
		return LString(lv.String())
	}
}

// Set a module loader to the package.preload table.
func (ls *LState) PreloadModule(name string, loader LGFunction) {
	preload := ls.GetField(ls.GetField(ls.Get(EnvironIndex), "package"), "preload")
	if _, ok := preload.(*LTable); !ok {
		ls.RaiseError("package.preload must be a table")
	}
	ls.SetField(preload, name, ls.NewFunction(loader))
}

// Checks whether the given index is an LChannel and returns this channel.
func (ls *LState) CheckChannel(n int) chan LValue {
	v := ls.Get(n)
	if ch, ok := v.(LChannel); ok {
		return (chan LValue)(ch)
	}
	ls.TypeError(n, LTChannel)
	return nil
}

// If the given index is a LChannel, returns this channel. If this argument is absent or is nil, returns ch. Otherwise, raises an error.
func (ls *LState) OptChannel(n int, ch chan LValue) chan LValue {
	v := ls.Get(n)
	if v == LNil {
		return ch
	}
	if ch, ok := v.(LChannel); ok {
		return (chan LValue)(ch)
	}
	ls.TypeError(n, LTChannel)
	return nil
}

/* }}} */

//
