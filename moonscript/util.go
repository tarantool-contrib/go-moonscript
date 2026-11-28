package moonscript

// Ports of the moonscript.util helpers used by moonc.

import (
	"fmt"
	"sort"
	"strings"
)

// PosToLine is util.pos_to_line: the 1-based line holding the 1-based byte
// offset pos.
func PosToLine(str string, pos int) int {
	line, lineStart := 1, 1
	for {
		nl := -1
		if lineStart-1 <= len(str) {
			if i := strings.IndexByte(str[lineStart-1:], '\n'); i >= 0 {
				nl = lineStart + i
			}
		}
		if !(nl >= 0 && nl < pos) {
			break
		}
		line++
		lineStart = nl + 1
	}
	return line
}

// luaTrim is util.trim: str:match "^%s*(.-)%s*$".
func luaTrim(s string) string {
	return strings.Trim(s, " \t\n\v\f\r")
}

// getLine is util.get_line; ok is false when the reference returns nil.
func getLine(str string, lineNum int) (string, bool) {
	// str:gmatch "([^\n]*)\n?" yields every line plus a trailing empty match
	// when the string doesn't end in a newline
	lines := strings.Split(str, "\n")
	if str != "" && !strings.HasSuffix(str, "\n") {
		lines = append(lines, "")
	}
	if lineNum >= 1 && lineNum <= len(lines) {
		return lines[lineNum-1], true
	}
	return "", false
}

// getClosestLine is util.get_closest_line.
func getClosestLine(str string, lineNum int) (string, int) {
	line, ok := getLine(str, lineNum)
	if (!ok || luaTrim(line) == "") && lineNum > 1 {
		return getClosestLine(str, lineNum-1)
	}
	return line, lineNum
}

// DebugPosmap is util.debug_posmap: one line per posmap entry, sorted by
// Lua line, showing the Lua line and the MoonScript line it maps to.
func DebugPosmap(posmap Posmap, moonCode, luaCode string) string {
	luaLines := make([]int, 0, len(posmap))
	for k := range posmap {
		luaLines = append(luaLines, k)
	}
	sort.Ints(luaLines)

	out := make([]string, 0, len(luaLines))
	for _, luaLine := range luaLines {
		pos := posmap[luaLine]
		moonLine := PosToLine(moonCode, pos)

		luaText, _ := getLine(luaCode, luaLine)
		moonText, _ := getClosestLine(moonCode, moonLine)

		out = append(out, fmt.Sprintf("%d\t %d:[ %s ] >> %d:[ %s ]",
			pos, luaLine, luaTrim(luaText), moonLine, luaTrim(moonText)))
	}
	return strings.Join(out, "\n")
}
