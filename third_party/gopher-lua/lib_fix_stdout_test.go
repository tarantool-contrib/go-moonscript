package lua

// go-moonscript patch: tests of the buffered standard output (iolib.go),
// run in a child process whose standard output and error go to one pipe:
// what is written to the standard output appears when it is flushed, as
// with C stdio when the output is not a terminal (the expected outputs are
// lua5.1's for the same code, `lua5.1 -e '<code>' 2>&1 < input`, but for
// reading the standard input: the copy flushes the standard output first
// whatever they are, glibc only when it is line buffered, a terminal).

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestStdoutChild(t *testing.T) {
	code := os.Getenv("GOPHER_LUA_STDOUT")
	if code == "" {
		t.Skip("run by TestStdoutBuffer")
	}
	L := NewState()
	if err := L.DoString(code); err != nil {
		msg, _, _ := strings.Cut(err.Error(), "\n")
		_, _ = os.Stderr.WriteString("error: " + msg + "\n")
	}
	L.Close() // flushes the standard output
	// exit before the testing package writes "PASS" (and, with -cover, the
	// coverage line) to the output under test
	os.Exit(0)
}

func TestStdoutBuffer(t *testing.T) {
	for _, c := range []struct{ code, input, output string }{
		// print and io.write share the buffer
		{`print("a") io.write("b\n") io.stdout:write("c\n") print("d", 1)`, "", "a\nb\nc\nd\t1\n"},
		// flushed at the exit only; standard error unbuffered
		{`io.write("x") print("y") io.stderr:write("E") os.exit(0)`, "", "Exy\n"},
		{`io.write("x") io.stderr:write("E")`, "", "Ex"},
		{`io.write("x") io.stderr:write("E") error("boom")`, "", "Eerror: <string>:1: boom\nx"},
		// io.flush, file:flush
		{`io.write("a") io.flush() io.stderr:write("E") io.write("b")`, "", "aEb"},
		{`io.write("a") io.stdout:flush() io.stderr:write("E") io.write("b")`, "", "aEb"},
		// before the standard input is read (lua5.1: "got line;prompt !\n")
		{`io.write("prompt ") local l = io.read() io.stderr:write("got ", l, ";") print("!")`, "line\n", "prompt got line;!\n"},
		{`io.write("p ") local n = io.stdin:read("*n") io.stderr:write(n, ";")`, "42\n", "p 42;"},
		{`io.write("p ") for l in io.lines() do io.stderr:write(l, ";") end`, "1\n2\n", "p 1;2;"},
		// setvbuf (the size is ignored)
		{`io.stdout:setvbuf("no") io.write("a") io.stderr:write("E") io.write("b")`, "", "aEb"},
		{`io.stdout:setvbuf("line") io.write("a\nb") io.stderr:write("E") io.write("c\n")`, "", "a\nEbc\n"},
		{`io.stdout:setvbuf("full", 4) io.write("abcdef") io.stderr:write("E") io.write("g")`, "", "Eabcdefg"},
		// print calls the global tostring, which must return a string
		{`local ok, e = pcall(print, setmetatable({}, {__tostring = function() end})) io.stderr:write(tostring(ok), " ", e:match("'tostring'.*"))`, "", "false 'tostring' must return a string to 'print'"},
		{`tostring = function(v) return "<" .. type(v) .. ">" end print(1, nil, "x")`, "", "<number>\t<nil>\t<string>\n"},
		// other files are flushed when the state is closed
		{`local f = io.tmpfile() f:write("t") io.stderr:write("E")`, "", "E"},
	} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestStdoutChild$")
		cmd.Env = append(os.Environ(), "GOPHER_LUA_STDOUT="+c.code)
		cmd.Stdin = strings.NewReader(c.input)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Errorf("%s: %v\n%s", c.code, err, output)
			continue
		}
		if got := string(output); got != c.output {
			t.Errorf("%s: output %q, want %q", c.code, got, c.output)
		}
	}
}
