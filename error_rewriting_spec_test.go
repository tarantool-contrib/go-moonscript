package moonscript

// Port of upstream v0.8.0 spec/error_rewriting_spec.moon. The line map
// (posmap) and repeated-error cases are compile-only; the "error rewriting"
// cases run the inputs in testdata/spec/error_inputs on gopher-lua with the
// runtime package (moonscript.base.loadfile, moonscript.line_tables,
// moonscript.errors.reverse_line_number).

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"strconv"
	"testing"

	lua "github.com/tarantool-contrib/go-moonscript/lua"

	moonrt "github.com/tarantool-contrib/go-moonscript/runtime"
)

func TestErrorRewritingSpecLineMap(t *testing.T) {
	cases := []struct {
		name, code string
		want       Posmap
		bug        string
	}{
		{"should create line table", `
			print "hello world"
			if something
			  print "cats"
		`, Posmap{1: 1, 2: 23, 3: 36, 4: 21}, ""},
		{"should create line table for multiline string", `
			print "one"
			x = [==[
			  one
			  two
			  thre
			  yes
			  no
			]==]
			print "two"
		`, Posmap{1: 1, 2: 13, 7: 13, 8: 57}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.bug != "" {
				knownBug(t, c.bug)
			}
			src := unindent(c.code)
			code, posmap, err := ToLua(src, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if !maps.Equal(posmap, c.want) {
				t.Errorf("posmap = %v, want %v\n%s", posmap, c.want, DebugPosmap(posmap, src, code))
			}
		})
	}
}

func TestErrorRewritingSpecBadCodeTwice(t *testing.T) {
	_, _, err := ToLua("{b=5}", Options{})
	if err == nil {
		t.Fatal("expected an error")
	}
	_, _, err2 := ToLua("{b=5}", Options{})
	if err2 == nil || err.Error() != err2.Error() {
		t.Errorf("second error %v differs from first %v", err2, err)
	}
}

// TestErrorRewritingSpecRewrite: each input raises a runtime error; the
// line in the error message must be rewritten to the MoonScript line.
func TestErrorRewritingSpecRewrite(t *testing.T) {
	for name, line := range map[string]int{"first": 24, "second": 16, "third": 11} {
		t.Run(name, func(t *testing.T) {
			fname := "testdata/spec/error_inputs/" + name + ".moon"
			got, err := rewrittenLineNo(fname)
			if err != nil {
				t.Fatal(err)
			}
			if got != line {
				t.Errorf("rewritten line = %d, want %d", got, line)
			}
		})
	}
}

// rewrittenLineNo is the spec's get_rewritten_line_no.
func rewrittenLineNo(fname string) (int, error) {
	L := moonrt.NewState()
	defer L.Close()
	chunk, err := moonrt.LoadFile(L, fname, Options{})
	if err != nil {
		return 0, err
	}
	L.Push(chunk)
	perr := L.PCall(0, 0, nil)
	if perr == nil {
		return 0, fmt.Errorf("`%s` is supposed to have runtime error!", fname)
	}
	var ae *lua.ApiError
	if !errors.As(perr, &ae) {
		return 0, fmt.Errorf("not a Lua error: %w", perr)
	}
	msg := lua.LVAsString(ae.Object)
	m := regexp.MustCompile(`^.*?:(\d+):`).FindStringSubmatch(msg)
	if m == nil {
		return 0, fmt.Errorf("no line in %q", msg)
	}
	source, _ := strconv.Atoi(m[1])
	lineTable, err := moonrt.LineTable(L, "@"+fname)
	if err != nil {
		return 0, fmt.Errorf("missing line table: %w", err)
	}
	if lineTable == nil {
		return 0, errors.New("missing line table")
	}
	line, ok, err := moonrt.ReverseLineNumber(L, fname, lineTable, source)
	if err != nil {
		return 0, fmt.Errorf("reverse_line_number: %w", err)
	}
	if !ok {
		return 0, errors.New("reverse_line_number: no line")
	}
	return line, nil
}
