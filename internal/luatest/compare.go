package luatest

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/stretchr/testify/assert"
)

// TestingT is what the assertions need from a test; *testing.T has it. It is
// testify's assert.TestingT, so a recorder can stand in to check that an
// assertion fails.
type TestingT interface {
	Errorf(format string, args ...any)
}

// helperT is the part of a test that marks helpers; a recorder may lack it.
// Each assertion marks itself: Helper marks the function that calls it.
type helperT interface {
	Helper()
}

// AssertSame asserts that the bundle behaved like the original: the same exit
// status, the same stdout and the same error message (see NormalizeStderr).
// It reports every difference, then both runs in full once, and returns
// whether there was no difference.
func AssertSame(test TestingT, orig, bundle Result) bool {
	if h, ok := test.(helperT); ok {
		h.Helper()
	}

	same := sameExitCode(test, orig, bundle, "")
	same = sameStdout(test, orig, bundle, "") && same
	same = sameStderr(test, orig, bundle, "") && same

	if !same {
		test.Errorf("the runs compared on %s:\n%s", orig.Interp.Name, describePair(orig, bundle))
	}

	return same
}

// AssertSameExitCode asserts that both runs exited with the same status.
func AssertSameExitCode(test TestingT, orig, bundle Result) bool {
	if h, ok := test.(helperT); ok {
		h.Helper()
	}

	return sameExitCode(test, orig, bundle, describePair(orig, bundle))
}

// AssertSameStdout asserts that both runs printed exactly the same to stdout.
func AssertSameStdout(test TestingT, orig, bundle Result) bool {
	if h, ok := test.(helperT); ok {
		h.Helper()
	}

	return sameStdout(test, orig, bundle, describePair(orig, bundle))
}

// AssertSameStderr asserts that both runs reported the same error message:
// stderr compared after NormalizeStderr.
func AssertSameStderr(test TestingT, orig, bundle Result) bool {
	if h, ok := test.(helperT); ok {
		h.Helper()
	}

	return sameStderr(test, orig, bundle, describePair(orig, bundle))
}

func sameExitCode(test TestingT, orig, bundle Result, details string) bool {
	if h, ok := test.(helperT); ok {
		h.Helper()
	}

	return assert.Equal(test, orig.ExitCode, bundle.ExitCode,
		"exit status differs on %s (original, bundle)\n%s", orig.Interp.Name, details)
}

func sameStdout(test TestingT, orig, bundle Result, details string) bool {
	if h, ok := test.(helperT); ok {
		h.Helper()
	}

	return assert.Equal(test, orig.Stdout, bundle.Stdout,
		"stdout differs on %s (original, bundle)\n%s", orig.Interp.Name, details)
}

func sameStderr(test TestingT, orig, bundle Result, details string) bool {
	if h, ok := test.(helperT); ok {
		h.Helper()
	}

	return assert.Equal(test, orig.Message(), bundle.Message(),
		"error message differs on %s (original, bundle; stderr after NormalizeStderr)\n%s",
		orig.Interp.Name, details)
}

func describePair(orig, bundle Result) string {
	return fmt.Sprintf("=== original: %s=== bundle: %s", orig, bundle)
}

// Message is r.Stderr normalised for comparison; see NormalizeStderr.
func (r Result) Message() string {
	return NormalizeStderr(r.Interp.Name, r.Stderr)
}

// tracebackRe starts the traceback PUC Lua and LuaJIT print after an uncaught
// error. Its frames are positions in the chunk that raised.
var tracebackRe = regexp.MustCompile(`(?m)^stack traceback:`)

// tarantoolTrailerRe is the JSON object Tarantool appends to an uncaught
// error: ` {"type":"LuajitError","trace":[{"file":"src/lua/utils.c",...}]}`.
var tarantoolTrailerRe = regexp.MustCompile(` \{"type":".*$`)

// positionRe is a `chunk:line: ` position as error() and the runtime put in
// front of a message. The chunk is a file name, possibly shortened with a
// leading "..." (LUA_IDSIZE), or a [string "..."] chunk.
var positionRe = regexp.MustCompile(`(^|\s)(?:\[string "[^\n]*?"\]|[^\s:"'()\[\]]+):\d+: `)

// addressRe is a pointer, as in `table: 0x600003c1c000`; it differs between
// any two runs.
var addressRe = regexp.MustCompile(`0x[0-9a-fA-F]+`)

// tarantoolFatal is the line Tarantool prints after an uncaught error.
const tarantoolFatal = "fatal error, exiting the event loop"

// NormalizeStderr reduces the stderr of a run of the interpreter named interp
// to what must agree between an original program and its bundle. Positions
// legitimately differ (`./flaky.lua:1: init failed` natively, `bundle.lua:10:
// init failed` in the bundle), and so do the formats of the interpreters.
// Removed are:
//
//   - the traceback after "stack traceback:" (PUC Lua, LuaJIT);
//   - the "<interp>: " prefix of an uncaught error (PUC Lua, LuaJIT);
//   - Tarantool's JSON trailer and its "fatal error, exiting the event loop";
//   - the searcher lines of "module 'x' not found:" ("\tno file ...",
//     "\tno field ..."): they enumerate the search path, which depends on the
//     working directory (Tarantool lists absolute paths up to /) and, with
//     --fallback, on the bundle's own searcher;
//   - chunk:line positions (StripPositions);
//   - addresses such as `table: 0x...`, which become `0x?`.
//
// Everything else, program output on stderr included, is kept.
func NormalizeStderr(interp, stderr string) string {
	loc := tracebackRe.FindStringIndex(stderr)
	if loc != nil {
		stderr = stderr[:loc[0]]
	}

	var lines []string

	for line := range strings.SplitSeq(stderr, "\n") {
		if line == tarantoolFatal || strings.HasPrefix(line, "\tno ") {
			continue
		}

		if interp != "" {
			line = strings.TrimPrefix(line, interp+": ")
		}

		line = tarantoolTrailerRe.ReplaceAllString(line, "")
		line = StripPositions(line)
		line = addressRe.ReplaceAllString(line, "0x?")
		lines = append(lines, line)
	}

	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// StripPositions removes every `chunk:line: ` position from text, including
// stacked ones (`./a.lua:3: ./b.lua:1: boom` becomes `boom`). Use it on
// stdout when a program prints caught error messages, e.g.
//
//	assert.Equal(t, luatest.StripPositions(orig.Stdout), luatest.StripPositions(bundle.Stdout))
func StripPositions(text string) string {
	for {
		stripped := positionRe.ReplaceAllString(text, "${1}")
		if stripped == text {
			return text
		}

		text = stripped
	}
}
