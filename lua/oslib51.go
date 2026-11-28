package lua

// go-moonscript patch: os.execute, os.remove, os.rename, os.setlocale and
// os.time as in Lua 5.1's loslib.c (LuaJIT 2.1's lib_os.c is the same for
// them) over glibc: system()'s wait status, "<name>: <strerror>" and errno
// for failures, setlocale over the locales a Go program has (C, POSIX and
// glibc's C.UTF-8, which differ from C only in what Lua never asks: the
// copy's character classes and number formats are C's), and mktime.

import (
	"errors"
	"math/rand/v2"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// cError is the errno of err (EIO if it has none) and C's strerror text of
// it (glibc's, which Go's texts are, uncapitalized).
func cError(err error) (string, syscall.Errno) {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		msg := err.Error()
		if i := strings.LastIndex(msg, ": "); i >= 0 {
			msg = msg[i+2:]
		}
		return msg, syscall.EIO
	}
	msg := errno.Error()
	if msg != "" {
		msg = strings.ToUpper(msg[:1]) + msg[1:]
	}
	return msg, errno
}

// pushFileResult is luaL_fileresult / os_pushresult: true, or nil,
// "<name>: <strerror>" (just "<strerror>" for name "") and errno.
func pushFileResult(L *LState, err error, name string) int {
	if err == nil {
		L.Push(LTrue)
		return 1
	}
	msg, errno := cError(err)
	if name != "" {
		msg = name + ": " + msg
	}
	L.Push(LNil)
	L.Push(LString(msg))
	L.Push(LNumber(errno))
	return 3
}

// osExecute is system(): with no command, whether a shell is there
// (nonzero); else the wait status of /bin/sh -c command (the exit status
// times 256, or the signal), 127*256 if the shell can't run.
func osExecute(L *LState) int {
	if L.Get(1) == LNil {
		if _, err := os.Stat("/bin/sh"); err != nil {
			L.Push(LNumber(0))
		} else {
			L.Push(LNumber(1))
		}
		return 1
	}
	c, args := popenArgs(L.CheckString(1))
	cmd := exec.Command(c, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Run()
	if cmd.ProcessState == nil {
		L.Push(LNumber(127 << 8))
		return 1
	}
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok {
		L.Push(LNumber(ws))
	} else if err != nil {
		L.Push(LNumber(1))
	} else {
		L.Push(LNumber(0))
	}
	return 1
}

func osRemove(L *LState) int {
	name := L.CheckString(1)
	return pushFileResult(L, cRemove(name), name)
}

// cRemove is glibc's remove: unlink, rmdir if it is a directory.
func cRemove(name string) error {
	err := syscall.Unlink(name)
	if err == syscall.EISDIR {
		err = syscall.Rmdir(name)
	}
	return err
}

func osRename(L *LState) int {
	from := L.CheckString(1)
	to := L.CheckString(2)
	return pushFileResult(L, syscall.Rename(from, to), from)
}

// the categories of glibc's locale, in the order of its composite names;
// os.setlocale takes the first six (and "all").
var localeCategories = []string{"LC_CTYPE", "LC_NUMERIC", "LC_TIME", "LC_COLLATE", "LC_MONETARY",
	"LC_MESSAGES", "LC_PAPER", "LC_NAME", "LC_ADDRESS", "LC_TELEPHONE", "LC_MEASUREMENT", "LC_IDENTIFICATION"}

var setlocaleOptions = []string{"all", "collate", "ctype", "monetary", "numeric", "time"}

// setlocaleCategory is the index in localeCategories of each of
// setlocaleOptions but "all".
var setlocaleCategory = []int{-1, 3, 0, 4, 1, 2}

// localeState is the locale of a state: the name of each category.
type localeState struct {
	names [12]string
}

func openOsLocale(L *LState, mod *LTable) {
	st := &localeState{}
	for i := range st.names {
		st.names[i] = "C"
	}
	ud := L.NewUserData()
	ud.Value = st
	mod.RawSetString("setlocale", L.NewClosure(osSetLocale, ud))
}

// localeName is the name a locale has once set, ok false for a locale
// that is not there.
func localeName(name string) (string, bool) {
	switch name {
	case "C", "POSIX":
		return "C", true
	case "C.UTF-8", "C.utf8":
		return name, true
	}
	return "", false
}

// envLocale is the locale "" means for category cat: $LC_ALL, $<cat> or
// $LANG, the first set.
func envLocale(cat string) string {
	for _, v := range []string{"LC_ALL", cat, "LANG"} {
		if s := os.Getenv(v); s != "" {
			return s
		}
	}
	return "C"
}

// osSetLocale is setlocale(category, locale): the name of the locale set
// (or of the current one for a nil locale), nil if it can't be set.
func osSetLocale(L *LState) int {
	ud, _ := L.Get(UpvalueIndex(1)).(*LUserData)
	st, _ := ud.Value.(*localeState)
	var locale *string
	if L.Get(1) != LNil {
		s := L.CheckString(1)
		locale = &s
	}
	op := 0 // luaL_checkoption with the default "all"
	if L.Get(2) != LNil {
		op = L.CheckOption(2, setlocaleOptions)
	}
	cats := []int{setlocaleCategory[op]}
	if op == 0 {
		cats = []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	}
	if locale != nil {
		names := st.names
		for _, c := range cats {
			name := *locale
			if name == "" {
				name = envLocale(localeCategories[c])
			}
			n, ok := localeName(name)
			if !ok {
				L.Push(LNil)
				return 1
			}
			names[c] = n
		}
		st.names = names
	}
	if op != 0 {
		L.Push(LString(st.names[cats[0]]))
		return 1
	}
	same := true
	for _, n := range st.names {
		same = same && n == st.names[0]
	}
	if same {
		L.Push(LString(st.names[0]))
		return 1
	}
	parts := make([]string, len(st.names))
	for i, n := range st.names {
		parts[i] = localeCategories[i] + "=" + n
	}
	L.Push(LString(strings.Join(parts, ";")))
	return 1
}

// timeField is loslib.c getfield: t[key] as an int if it is a number (or a
// string convertible to one), else d, an error if d < 0.
func timeField(L *LState, tbl *LTable, key string, d int) int {
	v := L.GetField(tbl, key)
	var n LNumber
	switch lv := v.(type) {
	case LNumber:
		n = lv
	case LString:
		num, err := parseNumber(string(lv))
		if err != nil {
			if d < 0 {
				L.RaiseError("field '%s' missing in date table", key)
			}
			return d
		}
		n = num
	default:
		if d < 0 {
			L.RaiseError("field '%s' missing in date table", key)
		}
		return d
	}
	return int(cInt(float64(n)))
}

// osTime is os.time: now, or mktime of the fields of a table (sec, min and
// hour default to 0, 0 and 12; isdst nil leaves the choice to mktime); nil
// for a time mktime can't give (or -1, which is its error result).
func osTime(L *LState) int {
	if L.Get(1) == LNil {
		L.Push(LNumber(time.Now().Unix()))
		return 1
	}
	tbl := L.CheckTable(1)
	sec := timeField(L, tbl, "sec", 0)
	min := timeField(L, tbl, "min", 0)
	hour := timeField(L, tbl, "hour", 12)
	day := timeField(L, tbl, "day", -1)
	month := timeField(L, tbl, "month", -1)
	year := timeField(L, tbl, "year", -1)
	isdst := -1
	if v := L.GetField(tbl, "isdst"); v != LNil {
		isdst = 0
		if LVAsBool(v) {
			isdst = 1
		}
	}
	t := time.Date(year, time.Month(month), day, hour, min, sec, 0, time.Local)
	unix := mktimeDST(t, isdst)
	if unix == -1 || int64(year)-1900 > 1<<31-1 || int64(year)-1900 < -(1<<31) {
		L.Push(LNil)
		return 1
	}
	L.Push(LNumber(unix))
	return 1
}

// mktimeDST is glibc mktime's handling of tm_isdst: when the time has the
// other DST flag, the UTC offset of a neighbouring time (within about 268
// days) with the flag asked for, else an hour of difference.
func mktimeDST(t time.Time, isdst int) int64 {
	unix := t.Unix()
	if isdst < 0 || t.IsDST() == (isdst == 1) {
		return unix
	}
	_, off := t.Zone()
	const stride, bound = 601200, 536454000/2 + 601200
	for delta := int64(stride); delta < bound; delta += stride {
		for _, dir := range []int64{-1, 1} {
			o := time.Unix(unix+dir*delta, 0).In(t.Location())
			if o.IsDST() == (isdst == 1) {
				_, ooff := o.Zone()
				return unix + int64(off-ooff)
			}
		}
	}
	if isdst == 1 {
		return unix - 3600
	}
	return unix + 3600
}

// osTmpname is lua_tmpnam with mkstemp: a new empty file
// /tmp/lua_XXXXXX, left there.
func osTmpname(L *LState) int {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	for try := 0; try < 100; try++ {
		var b [6]byte
		for i := range b {
			b[i] = chars[rand.IntN(len(chars))]
		}
		name := "/tmp/lua_" + string(b[:])
		f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			_ = f.Close()
			L.Push(LString(name))
			return 1
		}
		if !errors.Is(err, os.ErrExist) {
			break
		}
	}
	L.RaiseError("unable to generate a unique filename")
	return 0
}
