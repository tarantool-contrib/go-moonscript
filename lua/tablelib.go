package lua

import "strings"

func OpenTable(L *LState) int {
	tabmod := L.RegisterModule(TabLibName, tableFuncs).(*LTable)
	if L.Dialect() == DialectLuaJIT {
		tabmod.RawSetString("setn", LNil) // go-moonscript patch: not in LuaJIT
	}
	openLuaJIT(L, TabLibName, tabmod) // go-moonscript patch (luajitlib.go)
	L.Push(tabmod)
	return 1
}

var tableFuncs = map[string]LGFunction{
	"getn":   tableGetN,
	"concat": tableConcat,
	"insert": tableInsert,
	"maxn":   tableMaxN,
	"remove": tableRemove,
	"sort":   tableSort,

	// go-moonscript patch: Lua 5.1's (lib51.go)
	"foreach":  tableForeach,
	"foreachi": tableForeachi,
	"setn":     tableSetN,
}

// go-moonscript patch: table.sort sorts t[1] to t[#t] (not the array part
// of the table, which may hold trailing nils and miss elements of the hash
// part; from AlohaWu/gopher-lua 05a013494b, which used #t), takes a nil
// comparator, and is Lua 5.1's quicksort (ltablib.c auxsort), so that the
// order of equal elements and the "invalid order function for sorting"
// errors are Lua 5.1's.
func tableSort(L *LState) int {
	tbl := L.checkWritableTable(1) // go-moonscript patch: LTable.Freeze
	var fn *LFunction
	if L.Get(2) != LNil {
		fn = L.CheckFunction(2)
	}
	n := tbl.Len()
	a := make([]LValue, n+1) // a[1..n], written back even after an error
	for i := 1; i <= n; i++ {
		a[i] = tbl.RawGetInt(i)
	}
	defer func() {
		for i := 1; i <= n; i++ {
			tbl.RawSetInt(i, a[i])
		}
	}()
	lt := func(x, y LValue) bool {
		if fn == nil {
			return lessThan(L, x, y)
		}
		L.Push(fn)
		L.Push(x)
		L.Push(y)
		L.Call(2, 1)
		return LVAsBool(L.reg.Pop())
	}
	tableAuxSort(L, a, 1, n, lt)
	return 0
}

func tableAuxSort(L *LState, a []LValue, l, u int, lt func(x, y LValue) bool) {
	for l < u { // for tail recursion
		// sort elements a[l], a[(l+u)/2] and a[u]
		if lt(a[u], a[l]) {
			a[l], a[u] = a[u], a[l]
		}
		if u-l == 1 {
			break // only 2 elements
		}
		i := (l + u) / 2
		if lt(a[i], a[l]) {
			a[i], a[l] = a[l], a[i]
		} else if lt(a[u], a[i]) {
			a[i], a[u] = a[u], a[i]
		}
		if u-l == 2 {
			break // only 3 elements
		}
		p := a[i] // pivot
		a[i], a[u-1] = a[u-1], p
		// a[l] <= P == a[u-1] <= a[u], only need to sort from l+1 to u-2
		i, j := l, u-1
		for { // invariant: a[l..i] <= P <= a[j..u]
			for i++; lt(tableSortGet(a, i), p); i++ { // until a[i] >= P
				if i > u {
					L.RaiseError("invalid order function for sorting")
				}
			}
			for j--; lt(p, tableSortGet(a, j)); j-- { // until a[j] <= P
				if j < l {
					L.RaiseError("invalid order function for sorting")
				}
			}
			if j < i {
				break
			}
			a[i], a[j] = a[j], a[i]
		}
		a[u-1], a[i] = a[i], a[u-1] // swap pivot (a[u-1]) with a[i]
		// a[l..i-1] <= a[i] == P <= a[i+1..u]
		// adjust so that smaller half is in [j..i] and larger one in [l..u]
		if i-l < u-i {
			j, i, l = l, i-1, i+1
		} else {
			j, i, u = i+1, u, i-1
		}
		tableAuxSort(L, a, j, i, lt) // call recursively the smaller one
	} // repeat the routine for the larger one
}

// tableSortGet is a[i], nil out of a[1..n] (lua_rawgeti, where an invalid
// order function makes the quicksort read past the elements).
func tableSortGet(a []LValue, i int) LValue {
	if i < 1 || i >= len(a) {
		return LNil
	}
	return a[i]
}

func tableGetN(L *LState) int {
	L.Push(LNumber(L.CheckTable(1).Len()))
	return 1
}

// go-moonscript patch: table.maxn is Lua 5.1's, the largest positive
// number key, integer or not (it was the end of the array part).
func tableMaxN(L *LState) int {
	tbl := L.CheckTable(1)
	maxn := LNumber(0)
	tbl.ForEach(func(k, _ LValue) {
		if n, ok := k.(LNumber); ok && n > maxn {
			maxn = n
		}
	})
	L.Push(maxn)
	return 1
}

// go-moonscript patch: table.remove is Lua 5.1's tremove: nothing removed
// and no value when the position is not in 1..#t (it removed the last
// element of the array part for a position below 1).
func tableRemove(L *LState) int {
	tbl := L.checkWritableTable(1) // go-moonscript patch: LTable.Freeze
	e := tbl.Len()
	pos := L.OptInt(2, e)
	if pos < 1 || pos > e {
		return 0
	}
	L.Push(tbl.Remove(pos))
	return 1
}

// go-moonscript patch: table.concat is Lua 5.1's tconcat (t[i] to t[j],
// i and j not clamped to 1..#t, a number separator), and builds the result
// in Go, with one allocation, instead of on the Lua stack, which overflowed
// for big tables ("registry overflow"; Anaminus/gopher-lua 2b1e764063,
// taybart/midnight df3458d5db).
func tableConcat(L *LState) int {
	tbl := L.CheckTable(1)
	sep := ""
	if L.Get(2) != LNil {
		sep = L.CheckString(2)
	}
	i := L.OptInt(3, 1)
	j := 0
	if L.Get(4) == LNil {
		j = tbl.Len()
	} else {
		j = L.CheckInt(4)
	}
	if i > j {
		L.Push(emptyLString)
		return 1
	}
	size := len(sep) * (j - i)
	for k := i; k <= j; k++ {
		switch v := tbl.RawGetInt(k).(type) {
		case LString:
			size += len(v)
		case LNumber:
			size += 24
		default:
			L.RaiseError("invalid value (%s) at index %d in table for 'concat'", v.Type().String(), k) // go-moonscript patch: luaL_error's position
		}
	}
	L.gcReserve(int64(size)) // go-moonscript patch: the memory limit (gc.go)
	var sb strings.Builder
	sb.Grow(size)
	for k := i; ; k++ {
		sb.WriteString(L.lvAsString(tbl.RawGetInt(k))) // go-moonscript patch: in the dialect
		if k == j {
			break
		}
		sb.WriteString(sep)
	}
	L.Push(LString(sb.String()))
	return 1
}

// go-moonscript patch: table.insert is Lua 5.1's tinsert: t[pos..#t] move
// up even for pos < 1, a nil value is stored, and an error for other
// numbers of arguments.
func tableInsert(L *LState) int {
	tbl := L.checkWritableTable(1) // go-moonscript patch: LTable.Freeze
	e := tbl.Len() + 1
	var pos int
	switch L.GetTop() {
	case 2:
		pos = e
	case 3:
		pos = L.CheckInt(2)
		if pos > e {
			e = pos
		}
		if pos >= 1 {
			if pos < e {
				tbl.moveUp(pos, e)
			}
		} else {
			for i := e; i > pos; i-- {
				tbl.RawSetInt(i, tbl.RawGetInt(i-1))
			}
		}
	default:
		L.RaiseError("wrong number of arguments to 'insert'") // go-moonscript patch: luaL_error's position
	}
	tbl.RawSetInt(pos, L.Get(L.GetTop()))
	return 0
}

//
