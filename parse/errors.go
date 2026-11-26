package parse

import (
	"fmt"
	"strings"
)

// FormatError is moonscript/parse/errors.lua errors.format without color or
// context options: "<label> at line N, column M:\n  <line>\n  <spaces>^".
// pos is a 1-based byte offset.
func FormatError(subject string, pos int, label string) string {
	if label == "" {
		label = "error"
	}
	line, col := calcLine(subject, pos)
	text, colInLine := getLine(subject, pos)
	return fmt.Sprintf("%s at line %d, column %d:\n", label, line, col) +
		"  " + text + "\n" +
		"  " + strings.Repeat(" ", max(colInLine-1, 0)) + "^"
}

func calcLine(subject string, pos int) (int, int) {
	if pos <= 1 {
		return 1, 1
	}
	end := min(pos-1, len(subject))
	line, lastNewline := 1, 0
	for i := 0; i < end; i++ {
		if subject[i] == '\n' {
			line++
			lastNewline = i + 1
		}
	}
	return line, pos - lastNewline
}

func getLine(subject string, pos int) (string, int) {
	// 1-based positions as in the Lua version
	at := func(i int) byte {
		if i < 1 || i > len(subject) {
			return 0
		}
		return subject[i-1]
	}
	start := pos
	for start > 1 && at(start-1) != '\n' {
		start--
	}
	end := pos
	for end <= len(subject) && at(end) != '\n' {
		end++
	}
	return luaSub(subject, start, end-1), pos - start + 1
}

// luaSub is string.sub with 1-based inclusive indices.
func luaSub(s string, i, j int) string {
	if i < 1 {
		i = 1
	}
	if j > len(s) {
		j = len(s)
	}
	if i > j {
		return ""
	}
	return s[i-1 : j]
}
