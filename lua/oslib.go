package lua

import (
	"math"
	"os"
	"strings"
	"time"
)

var startedAt time.Time

func init() {
	startedAt = time.Now()
}

func OpenOs(L *LState) int {
	osmod := L.RegisterModule(OsLibName, osFuncs)
	openOsLocale(L, osmod.(*LTable)) // go-moonscript patch (oslib51.go)
	openLuaJIT(L, OsLibName, osmod)  // go-moonscript patch (luajitlib.go)
	L.Push(osmod)
	return 1
}

var osFuncs = map[string]LGFunction{
	"clock":     osClock,
	"difftime":  osDiffTime,
	"execute":   osExecute,
	"exit":      osExit,
	"date":      osDate,
	"getenv":    osGetEnv,
	"remove":    osRemove,
	"rename":    osRename,
	"setenv":    osSetEnv,
	"setlocale": osSetLocale,
	"time":      osTime,
	"tmpname":   osTmpname,
}

func osClock(L *LState) int {
	L.Push(LNumber(float64(time.Now().Sub(startedAt)) / float64(time.Second)))
	return 1
}

func osDiffTime(L *LState) int {
	L.Push(LNumber(L.CheckInt64(1) - L.CheckInt64(2)))
	return 1
}

// go-moonscript patch: os.exit reads its argument before it closes the
// state (an invalid one made a Go panic in the closed state), and, as in
// LuaJIT 2.1, true is EXIT_SUCCESS and false EXIT_FAILURE.
func osExit(L *LState) int {
	code := 0
	if b, ok := L.Get(1).(LBool); ok && L.Dialect() == DialectLuaJIT { // go-moonscript patch: LuaJIT 2.1 only
		if !b {
			code = 1
		}
	} else {
		code = L.OptInt(1, 0)
	}
	L.Close()
	os.Exit(code)
	return 1
}

// go-moonscript patch: os.date is Lua 5.1's os_date: the format and the
// time are optional (nil: "%c", now), "*t" (exactly) gives a table with
// yday and isdst, and a time out of the range of C's struct tm gives nil.
func osDate(L *LState) int {
	cfmt := "%c"
	if L.Get(1) != LNil {
		cfmt = L.CheckString(1) // a number is converted
	}
	t := time.Now()
	if L.Get(2) != LNil {
		sec := float64(L.CheckNumber(2))
		if math.IsNaN(sec) || sec < -(1<<63) || sec >= 1<<63 {
			L.Push(LNil)
			return 1
		}
		t = time.Unix(int64(sec), 0) // (time_t), truncated
	}
	if strings.HasPrefix(cfmt, "!") {
		cfmt = cfmt[1:]
		t = t.UTC()
	}
	if year := int64(t.Year()) - 1900; year < math.MinInt32 || year > math.MaxInt32 {
		L.Push(LNil) // tm_year is an int
		return 1
	}
	if cfmt == "*t" {
		ret := L.CreateTable(0, 9)
		ret.RawSetString("year", LNumber(t.Year()))
		ret.RawSetString("month", LNumber(t.Month()))
		ret.RawSetString("day", LNumber(t.Day()))
		ret.RawSetString("hour", LNumber(t.Hour()))
		ret.RawSetString("min", LNumber(t.Minute()))
		ret.RawSetString("sec", LNumber(t.Second()))
		ret.RawSetString("wday", LNumber(t.Weekday()+1))
		ret.RawSetString("yday", LNumber(t.YearDay()))
		ret.RawSetString("isdst", LBool(t.IsDST()))
		L.Push(ret)
		return 1
	}
	if L.Dialect() == DialectLuaJIT { // go-moonscript patch (luajitlib.go)
		cfmt = JITDateFormat(cfmt)
	}
	L.Push(LString(strftime(t, cfmt)))
	return 1
}

func osGetEnv(L *LState) int {
	v := os.Getenv(L.CheckString(1))
	if len(v) == 0 {
		L.Push(LNil)
	} else {
		L.Push(LString(v))
	}
	return 1
}

func osSetEnv(L *LState) int {
	err := os.Setenv(L.CheckString(1), L.CheckString(2))
	if err != nil {
		L.Push(LNil)
		L.Push(LString(err.Error()))
		return 2
	} else {
		L.Push(LTrue)
		return 1
	}
}

//
