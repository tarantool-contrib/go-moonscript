package lua

import (
	"context"
	"fmt"
	"math"
	"os"
	"strconv"
)

type LValueType int

const (
	LTNil LValueType = iota
	LTBool
	LTNumber
	LTString
	LTFunction
	LTUserData
	LTThread
	LTTable
	LTChannel
)

var lValueNames = [9]string{"nil", "boolean", "number", "string", "function", "userdata", "thread", "table", "channel"}

func (vt LValueType) String() string {
	return lValueNames[int(vt)]
}

type LValue interface {
	String() string
	Type() LValueType
}

// LVIsFalse returns true if a given LValue is a nil or false otherwise false.
func LVIsFalse(v LValue) bool { return v == LNil || v == LFalse }

// LVIsFalse returns false if a given LValue is a nil or false otherwise true.
func LVAsBool(v LValue) bool { return v != LNil && v != LFalse }

// LVAsString returns string representation of a given LValue
// if the LValue is a string or number, otherwise an empty string.
func LVAsString(v LValue) string {
	switch sn := v.(type) {
	case LString, LNumber:
		return sn.String()
	default:
		return ""
	}
}

// LVCanConvToString returns true if a given LValue is a string or number
// otherwise false.
func LVCanConvToString(v LValue) bool {
	switch v.(type) {
	case LString, LNumber:
		return true
	default:
		return false
	}
}

// LVAsNumber tries to convert a given LValue to a number.
func LVAsNumber(v LValue) LNumber {
	switch lv := v.(type) {
	case LNumber:
		return lv
	case LString:
		if num, err := parseNumber(string(lv)); err == nil {
			return num
		}
	}
	return LNumber(0)
}

type LNilType struct{}

func (nl *LNilType) String() string   { return "nil" }
func (nl *LNilType) Type() LValueType { return LTNil }

var LNil = LValue(&LNilType{})

type LBool bool

func (bl LBool) String() string {
	if bool(bl) {
		return "true"
	}
	return "false"
}
func (bl LBool) Type() LValueType { return LTBool }

var LTrue = LBool(true)
var LFalse = LBool(false)

type LString string

func (st LString) String() string   { return string(st) }
func (st LString) Type() LValueType { return LTString }

// fmt.Formatter interface
func (st LString) Format(f fmt.State, c rune) {
	switch c {
	case 'd', 'i':
		if nm, err := parseNumber(string(st)); err != nil {
			defaultFormat(nm, f, 'd')
		} else {
			defaultFormat(string(st), f, 's')
		}
	default:
		defaultFormat(string(st), f, c)
	}
}

// go-moonscript patch: numbers convert to strings as in Lua 5.1, whose
// lua_number2str is sprintf("%.14g") (LUAI_NUMFMT), with C's spellings of
// infinities and NaNs ("inf", "-inf", "nan", "-nan") and the sign of -0.
func (nm LNumber) String() string {
	f := float64(nm)
	if f > -1e14 && f < 1e14 && f == float64(int64(f)) && (f != 0 || !math.Signbit(f)) {
		return strconv.FormatInt(int64(f), 10) // what "%.14g" gives, faster
	}
	return number2str(f)
}

func number2str(f float64) string {
	switch {
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	case math.IsNaN(f):
		if math.Signbit(f) {
			return "-nan"
		}
		return "nan"
	}
	return strconv.FormatFloat(f, 'g', 14, 64)
}

func (nm LNumber) Type() LValueType { return LTNumber }

// fmt.Formatter interface
func (nm LNumber) Format(f fmt.State, c rune) {
	switch c {
	case 'q', 's':
		defaultFormat(nm.String(), f, c)
	case 'b', 'c', 'd', 'o', 'x', 'X', 'U':
		defaultFormat(int64(nm), f, c)
	case 'e', 'E', 'f', 'F', 'g', 'G':
		defaultFormat(float64(nm), f, c)
	case 'i':
		defaultFormat(int64(nm), f, 'd')
	default:
		if isInteger(nm) {
			defaultFormat(int64(nm), f, c)
		} else {
			defaultFormat(float64(nm), f, c)
		}
	}
}

type LTable struct {
	Metatable LValue

	array []LValue
	hash  hashPart // go-moonscript patch: the other keys (table_hash.go)
}

func (tb *LTable) String() string   { return fmt.Sprintf("table: %p", tb) }
func (tb *LTable) Type() LValueType { return LTTable }

type LFunction struct {
	IsG       bool
	Env       *LTable
	Proto     *FunctionProto
	GFunction LGFunction
	Upvalues  []*Upvalue
}
type LGFunction func(*LState) int

func (fn *LFunction) String() string   { return fmt.Sprintf("function: %p", fn) }
func (fn *LFunction) Type() LValueType { return LTFunction }

type Global struct {
	MainThread    *LState
	CurrentThread *LState
	Registry      *LTable
	Global        *LTable

	builtinMts map[int]LValue
	tempFiles  []*os.File
	gccount    int32

	// go-moonscript patch: the standard streams and open files of the io
	// library (iolib.go)
	io *ioState

	// go-moonscript patch: collectgarbage("setpause"/"setstepmul") values
	// (kept and returned; the Go collector is not tuned per state)
	gcPause, gcStepMul int

	gc gcState // go-moonscript patch: weak tables, __gc, memory limit (gc.go)
}

type LState struct {
	G       *Global
	Parent  *LState
	Env     *LTable
	Panic   func(*LState)
	Dead    bool
	Options Options

	stop         int32
	reg          *registry
	stack        callFrameStack
	alloc        *allocator
	currentFrame *callFrame
	hookMask     int // go-moonscript patch: the debug hook events to run (hook.go)
	wrapped      bool
	uvcache      *Upvalue
	hasErrorFunc bool
	nCcalls      int // go-moonscript patch: Go-to-Lua calls (callR) in progress
	yieldNRet    int // go-moonscript patch: results the yield wants (switchToParentThread)
	mainLoop     func(*LState, *callFrame)
	ctx          context.Context
	ctxCancelFn  context.CancelFunc
	hook         hookState // go-moonscript patch: debug hook (hook.go)
}

func (ls *LState) String() string   { return fmt.Sprintf("thread: %p", ls) }
func (ls *LState) Type() LValueType { return LTThread }

type LUserData struct {
	Value     interface{}
	Env       *LTable
	Metatable LValue
}

func (ud *LUserData) String() string   { return fmt.Sprintf("userdata: %p", ud) }
func (ud *LUserData) Type() LValueType { return LTUserData }

type LChannel chan LValue

func (ch LChannel) String() string   { return fmt.Sprintf("channel: %p", ch) }
func (ch LChannel) Type() LValueType { return LTChannel }
