package lua

import (
	"fmt"
	"strings"

	"github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua/pm"
)

const emptyLString LString = LString("")

func OpenString(L *LState) int {
	var mod *LTable
	//_, ok := L.G.builtinMts[int(LTString)]
	//if !ok {
	mod = L.RegisterModule(StringLibName, strFuncs).(*LTable)
	gmatch := L.NewFunction(strGmatch)
	mod.RawSetString("gmatch", gmatch)
	if L.Dialect() != DialectLuaJIT { // go-moonscript patch: LUA_COMPAT_GFIND, not in LuaJIT
		mod.RawSetString("gfind", gmatch)
	}
	// go-moonscript patch: the metatable of strings is a table of its own
	// whose __index is the string table, as in Lua 5.1 (from
	// Anaminus/gopher-lua 8bd71831a8)
	mt := L.CreateTable(0, 1)
	mt.RawSetString("__index", mod)
	L.G.builtinMts[int(LTString)] = mt
	//}
	openLuaJIT(L, StringLibName, mod) // go-moonscript patch (luajitlib.go)
	L.Push(mod)
	return 1
}

var strFuncs = map[string]LGFunction{
	"byte":    strByte,
	"char":    strChar,
	"dump":    strDump,
	"find":    strFind,
	"format":  strFormat,
	"gsub":    strGsub,
	"len":     strLen,
	"lower":   strLower,
	"match":   strMatch,
	"rep":     strRep,
	"reverse": strReverse,
	"sub":     strSub,
	"upper":   strUpper,
}

func strByte(L *LState) int {
	str := L.CheckString(1)
	start := L.OptInt(2, 1) - 1
	end := L.OptInt(3, -1)
	l := len(str)
	if start < 0 {
		start = l + start + 1
	}
	if end < 0 {
		end = l + end + 1
	}

	if L.GetTop() == 2 {
		if start < 0 || start >= l {
			return 0
		}
		L.Push(LNumber(str[start]))
		return 1
	}

	start = intMax(start, 0)
	end = intMin(end, l)
	if end < 0 || end <= start || start >= l {
		return 0
	}

	for i := start; i < end; i++ {
		L.Push(LNumber(str[i]))
	}
	return end - start
}

func strChar(L *LState) int {
	top := L.GetTop()
	bytes := make([]byte, L.GetTop())
	for i := 1; i <= top; i++ {
		// go-moonscript patch: the codes are checked as in Lua 5.1
		c := cInt(float64(L.CheckNumber(i)))
		if c < 0 || c > 255 {
			L.ArgError(i, "invalid value")
		}
		bytes[i-1] = uint8(c)
	}
	L.Push(LString(string(bytes)))
	return 1
}

// go-moonscript patch: string.dump writes the copy's own binary chunks
// (dump.go).
func strDump(L *LState) int {
	fn := L.CheckFunction(1)
	b, err := DumpFunction(fn)
	if L.Dialect() == DialectLuaJIT { // LuaJIT's signature, string.dump(f, strip)
		b, err = dumpFunction(fn, dumpHeaderLJ, LVAsBool(L.Get(2)))
	}
	if err != nil {
		L.RaiseError("%s", err.Error())
	}
	L.Push(LString(b))
	return 1
}

func strFind(L *LState) int {
	str := L.CheckString(1)
	pattern := L.CheckString(2)
	// go-moonscript patch: init is clamped to the subject as in Lua 5.1's
	// str_find_aux (past the end: slice bounds panic in a plain find, no
	// match otherwise; an empty pattern was found at 1 whatever init was)
	init := strFindInit(str, L.OptInt(3, 1))
	// go-moonscript patch: like Lua 5.1, a find with a pattern without its
	// SPECIALS is a plain find (so ')' alone is no error there)
	plain := LVAsBool(L.Get(4)) || !strings.ContainsAny(pattern, "^$*+?.([%-")

	if plain {
		pos := strings.Index(str[init:], pattern)
		if pos < 0 {
			L.Push(LNil)
			return 1
		}
		L.Push(LNumber(init+pos) + 1)
		L.Push(LNumber(init + pos + len(pattern)))
		return 2
	}

	mds, err := pm.FindContext(L.ctx, pattern, unsafeFastStringToReadOnlyBytes(str), init, 1) // go-moonscript patch: stops when L.ctx is done
	if err != nil {
		L.RaiseError("%s", err.Error())
	}
	if len(mds) == 0 {
		L.Push(LNil)
		return 1
	}
	md := mds[0]
	L.Push(LNumber(md.Capture(0) + 1))
	L.Push(LNumber(md.Capture(1)))
	return pushCaptures(L, md, str) + 2
}

func strGsub(L *LState) int {
	str := L.CheckString(1)
	pat := L.CheckString(2)
	// go-moonscript patch: a number replacement is a string, as in Lua 5.1
	// (from Luzi0390/gopher-lua b443b1476a)
	repl := L.Get(3)
	switch lv := repl.(type) {
	case LNumber:
		repl = LString(L.numberString(lv)) // go-moonscript patch: in the dialect
	case LString, *LTable, *LFunction:
	default:
		L.ArgError(3, "string/function/table expected")
	}
	limit := L.OptInt(4, -1)
	if limit <= 0 && L.Get(4) != LNil {
		// go-moonscript patch: as in Lua 5.1, n < 1 replaces nothing (pm.Find
		// replaced every match when the first position matched)
		L.SetTop(1)
		L.Push(LNumber(0))
		return 2
	}

	mds, err := pm.FindContext(L.ctx, pat, unsafeFastStringToReadOnlyBytes(str), 0, limit) // go-moonscript patch: stops when L.ctx is done
	if err != nil {
		L.RaiseError("%s", err.Error())
	}
	if len(mds) == 0 {
		L.SetTop(1)
		L.Push(LNumber(0))
		return 2
	}
	switch lv := repl.(type) {
	case LString:
		L.Push(LString(strGsubStr(L, str, string(lv), mds)))
	case *LTable:
		L.Push(LString(strGsubTable(L, str, lv, mds)))
	case *LFunction:
		L.Push(LString(strGsubFunc(L, str, lv, mds)))
	}
	L.Push(LNumber(len(mds)))
	return 2
}

type replaceInfo struct {
	Indicies []int
	String   string
}

// captureValue is lstrlib's push_onecapture for the capture at slot idx of
// m (2, 4, ...): its position, or its text; an unfinished capture (a '('
// the pattern ends in) raises "unfinished capture". go-moonscript patch.
func captureValue(L *LState, m *pm.MatchData, str string, idx int) LValue {
	if m.IsPosCapture(idx) {
		return LNumber(m.Capture(idx))
	}
	if m.IsUnfinished(idx) {
		L.RaiseError("unfinished capture")
	}
	return LString(str[m.Capture(idx):m.Capture(idx+1)])
}

// pushCaptures is lstrlib's push_captures without the whole match: it
// pushes every capture of m and returns how many. go-moonscript patch.
func pushCaptures(L *LState, m *pm.MatchData, str string) int {
	for i := 2; i < m.CaptureLength(); i += 2 {
		L.Push(captureValue(L, m, str, i))
	}
	return m.CaptureLength()/2 - 1
}

func checkCaptureIndex(L *LState, m *pm.MatchData, idx int) {
	if idx <= 2 {
		return
	}
	if idx >= m.CaptureLength() {
		L.RaiseError("invalid capture index")
	}
}

func capturedString(L *LState, m *pm.MatchData, str string, idx int) string {
	checkCaptureIndex(L, m, idx)
	if idx >= m.CaptureLength() && idx == 2 {
		idx = 0
	}
	if m.IsPosCapture(idx) {
		return fmt.Sprint(m.Capture(idx))
	}
	return string(captureValue(L, m, str, idx).(LString)) // go-moonscript patch: unfinished
}

// strGsubDoReplace replaces the matched ranges in info, which are in order
// and don't overlap. go-moonscript patch: in one pass; it copied the whole
// string once per match, quadratic in the matches (yuin/gopher-lua#559).
func strGsubDoReplace(L *LState, str string, info []replaceInfo) string {
	if L.G.gc.memLimit > 0 { // go-moonscript patch: the memory limit (gc.go)
		size := int64(len(str))
		for _, replace := range info {
			size += int64(len(replace.String) - (replace.Indicies[1] - replace.Indicies[0]))
		}
		L.gcReserve(size)
	}
	var b strings.Builder
	last := 0
	for _, replace := range info {
		b.WriteString(str[last:replace.Indicies[0]])
		b.WriteString(replace.String)
		last = replace.Indicies[1]
	}
	b.WriteString(str[last:])
	return b.String()
}

func strGsubStr(L *LState, str string, repl string, matches []*pm.MatchData) string {
	infoList := make([]replaceInfo, 0, len(matches))
	var size int64 // go-moonscript patch: the replacements so far (gc.go)
	for _, match := range matches {
		start, end := match.Capture(0), match.Capture(1)
		// go-moonscript patch: lstrlib's add_s: "%<digit>" is a capture,
		// '%' and any other character is that character (a '%' at the end:
		// the NUL that ends the C string)
		buf := make([]byte, 0, len(repl))
		for i := 0; i < len(repl); i++ {
			c := repl[i]
			if c != '%' {
				buf = append(buf, c)
				continue
			}
			i++
			if i == len(repl) {
				buf = append(buf, 0)
			} else if c = repl[i]; c >= '0' && c <= '9' {
				buf = append(buf, capturedString(L, match, str, 2*(int(c)-48))...)
			} else {
				buf = append(buf, c)
			}
		}
		infoList = append(infoList, replaceInfo{[]int{start, end}, string(buf)})
		if size += int64(len(buf)); size >= gcBigAlloc {
			L.gcReserve(size)
			size = 0
		}
	}

	return strGsubDoReplace(L, str, infoList)
}

func strGsubTable(L *LState, str string, repl *LTable, matches []*pm.MatchData) string {
	infoList := make([]replaceInfo, 0, len(matches))
	for _, match := range matches {
		idx := 0
		if match.CaptureLength() > 2 { // has captures
			idx = 2
		}
		value := L.GetTable(repl, captureValue(L, match, str, idx)) // go-moonscript patch: unfinished
		if !LVIsFalse(value) {
			infoList = append(infoList, replaceInfo{[]int{match.Capture(0), match.Capture(1)}, strGsubValue(L, value)})
		}
	}
	return strGsubDoReplace(L, str, infoList)
}

func strGsubFunc(L *LState, str string, repl *LFunction, matches []*pm.MatchData) string {
	infoList := make([]replaceInfo, 0, len(matches))
	for _, match := range matches {
		start, end := match.Capture(0), match.Capture(1)
		L.Push(repl)
		nargs := 0
		if match.CaptureLength() > 2 { // has captures
			nargs = pushCaptures(L, match, str)
		} else {
			L.Push(LString(capturedString(L, match, str, 0)))
			nargs++
		}
		L.Call(nargs, 1)
		ret := L.reg.Pop()
		if !LVIsFalse(ret) {
			infoList = append(infoList, replaceInfo{[]int{start, end}, strGsubValue(L, ret)})
		}
	}
	return strGsubDoReplace(L, str, infoList)
}

// strGsubValue is a replacement value from a table or a function, which
// must be a string or a number (go-moonscript patch, as in Lua 5.1).
func strGsubValue(L *LState, value LValue) string {
	if !LVCanConvToString(value) {
		L.RaiseError("invalid replacement value (a %s)", value.Type().String())
	}
	return L.lvAsString(value) // go-moonscript patch: in the dialect
}

// go-moonscript patch: string.gmatch matches lazily, one match per call
// of its iterator, as Lua 5.1's gmatch_aux does (it found every match when
// called: an error of the pattern was raised by gmatch, and a loop that
// breaks early still matched the whole subject).
type strMatchData struct {
	str     string
	pattern string
	pos     int // where the next match is tried from; past the end when done
}

// go-moonscript patch: string.gmatch returns a closure that needs no
// arguments, as in Lua 5.1, not an iterator function and its state
// (yuin/gopher-lua PR #466, waschik).
func strGmatchIter(L *LState) int {
	md := L.Get(UpvalueIndex(1)).(*LUserData).Value.(*strMatchData)
	str := md.str
	if md.pos > len(str) {
		return 0
	}
	mds, err := pm.FindContext(L.ctx, md.pattern, unsafeFastStringToReadOnlyBytes(str), md.pos, 1)
	if err != nil {
		L.RaiseError("%s", err.Error())
	}
	if len(mds) == 0 {
		md.pos = len(str) + 1
		return 0
	}
	match := mds[0]
	md.pos = match.Capture(1)
	if md.pos == match.Capture(0) { // an empty match: on by at least one
		md.pos++
	}
	if match.CaptureLength() == 2 {
		L.Push(LString(str[match.Capture(0):match.Capture(1)]))
		return 1
	}
	return pushCaptures(L, match, str)
}

// strFindInit is the byte offset where string.find and string.match start,
// from their init argument, as in Lua 5.1's str_find_aux: relative to the
// end if negative, and clamped to 0..len(str). go-moonscript patch.
func strFindInit(str string, init int) int {
	l := len(str)
	if init < 0 {
		init += l + 1
	}
	init--
	if init < 0 {
		return 0
	}
	if init > l {
		return l
	}
	return init
}

func strGmatch(L *LState) int {
	str := L.CheckString(1)
	pattern := L.CheckString(2)
	if strings.HasPrefix(pattern, "^") {
		// go-moonscript patch: in Lua 5.1 a '^' at the start of a gmatch
		// pattern is not an anchor but matches '^' (gopher-lua anchored)
		pattern = "%" + pattern
	}
	ud := L.NewUserData()
	ud.Value = &strMatchData{str, pattern, 0}
	L.Push(L.NewClosure(strGmatchIter, ud))
	return 1
}

func strLen(L *LState) int {
	str := L.CheckString(1)
	L.Push(LNumber(len(str)))
	return 1
}

func strLower(L *LState) int {
	str := L.CheckString(1)
	L.Push(LString(strings.ToLower(str)))
	return 1
}

func strMatch(L *LState) int {
	str := L.CheckString(1)
	pattern := L.CheckString(2)
	offset := strFindInit(str, L.OptInt(3, 1)) // go-moonscript patch: clamped as in Lua 5.1

	mds, err := pm.FindContext(L.ctx, pattern, unsafeFastStringToReadOnlyBytes(str), offset, 1) // go-moonscript patch: stops when L.ctx is done
	if err != nil {
		L.RaiseError("%s", err.Error())
	}
	if len(mds) == 0 {
		L.Push(LNil)
		return 1 // go-moonscript patch: nil, as in Lua 5.1 (yuin/gopher-lua PR #544)
	}
	md := mds[0]
	nsubs := md.CaptureLength() / 2
	switch nsubs {
	case 1:
		L.Push(LString(str[md.Capture(0):md.Capture(1)]))
		return 1
	default:
		return pushCaptures(L, md, str)
	}
}

func strRep(L *LState) int {
	str := L.CheckString(1)
	n := L.CheckInt(2)
	if n < 0 {
		L.Push(emptyLString)
	} else {
		L.gcReserveProduct(len(str), n) // go-moonscript patch: the memory limit (gc.go)
		L.Push(LString(strings.Repeat(str, n)))
	}
	return 1
}

func strReverse(L *LState) int {
	str := L.CheckString(1)
	bts := []byte(str)
	out := make([]byte, len(bts))
	for i, j := 0, len(bts)-1; j >= 0; i, j = i+1, j-1 {
		out[i] = bts[j]
	}
	L.Push(LString(string(out)))
	return 1
}

func strSub(L *LState) int {
	str := L.CheckString(1)
	start := luaIndex2StringIndex(str, L.CheckInt(2), true)
	end := luaIndex2StringIndex(str, L.OptInt(3, -1), false)
	l := len(str)
	if start >= l || end < start {
		L.Push(emptyLString)
	} else {
		L.Push(LString(str[start:end]))
	}
	return 1
}

func strUpper(L *LState) int {
	str := L.CheckString(1)
	L.Push(LString(strings.ToUpper(str)))
	return 1
}

func luaIndex2StringIndex(str string, i int, start bool) int {
	if start && i != 0 {
		i -= 1
	}
	l := len(str)
	if i < 0 {
		i = l + i + 1
	}
	i = intMax(0, i)
	if !start && i > l {
		i = l
	}
	return i
}

//
