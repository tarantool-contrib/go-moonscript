package lua

// go-moonscript patch: tests of the compiler fixes ported from yuin/gopher-lua
// pull requests (see moonscript.patch).

import (
	"fmt"
	"strings"
	"testing"
)

// From yuin/gopher-lua#556 (kimjooyoon), with Lua 5.1's limits: the parser
// allows 200 local variables per function (LUAI_MAXVARS), the compiler
// fewer than 250 registers (MAXSTACK). The errors are lua5.1's (luac
// reports a register overflow near a token, one token later).
func TestCompileRegisterLimitLocations(t *testing.T) {
	locals := func(count int, initializer string) string {
		var source strings.Builder
		for i := 0; i < count; i++ {
			fmt.Fprintf(&source, "local v%d%s\n", i, initializer)
		}
		return source.String()
	}
	call := func(arguments int) string {
		return "consume(" + strings.TrimSuffix(strings.Repeat("1,", arguments), ",") + ")\n"
	}
	const mainPrefix = "-- main chunk\n"
	const nestedPrefix = "-- nested function\n\n\n\nlocal function oversized()\n"
	const mainLocals = "main function has more than 200 local variables"
	const nestedLocals = "function at line 5 has more than 200 local variables"
	const registerError = "function or expression too complex"
	locals195 := mainPrefix + locals(195, " = 1")
	locals200 := mainPrefix + locals(200, " = 1")
	multilineCall := "consume(\n" + strings.Repeat("1,\n", 59) + "1\n)\n"

	tests := []struct {
		name        string
		source      string
		wantLine    int
		wantMessage string
	}{
		{"initialized_locals_in_main", mainPrefix + locals(210, " = 1"), 202, mainLocals},
		{"initialized_locals_in_nested_function", nestedPrefix + locals(210, " = 1") + "end\n", 206, nestedLocals},
		// the error is on the line of the token after the name
		{"uninitialized_locals_in_main", mainPrefix + locals(210, ""), 203, mainLocals},
		{"uninitialized_locals_in_nested_function", nestedPrefix + locals(210, "") + "end\n", 207, nestedLocals},
		{"multiline_initializer", locals200 + "local overflow =\n1\n", 202, mainLocals},
		{"local_function_declaration", locals200 + "local function overflow()\nend\n", 202, mainLocals},
		// the function in register 195, 54 arguments: 250 registers
		{"call_arguments_on_one_line", locals195 + call(54), 197, registerError},
		// the 54th argument uses register 249, the first beyond the limit
		{"multiline_call_uses_first_overflowing_argument", locals195 + multilineCall, 251, registerError},
		{"later_larger_call_keeps_first_overflow_location", locals195 + call(20) + "\n" + call(60), 199, registerError},
		{"accepted_200_locals", locals200 + "return v199\n", 0, ""},
		{"accepted_call_below_register_limit", locals195 + call(53), 0, ""},
		{"accepted_locals_reused_after_scope_exit", "do\n" + locals(200, " = 1") + "end\n" +
			"do\n" + locals(200, " = 1") + "end\nreturn 1\n", 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			L := NewState()
			defer L.Close()
			_, err := L.Load(strings.NewReader(tt.source), "x")
			if tt.wantMessage == "" {
				if err != nil {
					t.Fatalf("Load rejected an accepted function: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("Load accepted an overflowing function")
			}
			apiErr, ok := err.(*ApiError)
			if !ok || apiErr.Type != ApiErrorSyntax {
				t.Fatalf("Load error = %v (%T), want ApiErrorSyntax", err, err)
			}
			compileErr, ok := apiErr.Cause.(*CompileError)
			if !ok {
				t.Fatalf("Load error = %v (%T), want a *CompileError", apiErr.Cause, apiErr.Cause)
			}
			if compileErr.Message != tt.wantMessage || compileErr.Line != tt.wantLine {
				t.Errorf("Load error = %d: %q, want %d: %q", compileErr.Line, compileErr.Message, tt.wantLine, tt.wantMessage)
			}
		})
	}
}

// From yuin/gopher-lua#556 (kimjooyoon), with Lua 5.1's limit of 60 upvalues
// per function (LUAI_MAXUPVALUES), which is also LuaJIT's: gopher-lua's was
// 255, its patch made more a compile error. The errors are lua5.1's and
// luajit's.
func TestCompileUpvalueLimit(t *testing.T) {
	tests := []struct {
		upvalues int
		wantErr  string
	}{
		{59, ""},
		{60, ""},
		{61, "<string>:66: function at line 64 has more than 60 upvalues"},
		{300, "<string>:304: function at line 152 has more than 60 upvalues"},
	}
	for _, tt := range tests {
		for _, dialect := range []Dialect{DialectLua51, DialectLuaJIT} {
			t.Run(fmt.Sprintf("%d/%v", tt.upvalues, dialect), func(t *testing.T) {
				// Split locals between two enclosing functions so the leaf
				// closure reaches the upvalue limit without reaching the
				// local-variable limit.
				var source strings.Builder
				source.WriteString("local function outer()\n")
				for i := 0; i < tt.upvalues/2; i++ {
					fmt.Fprintf(&source, "local v%d = 1\n", i)
				}
				source.WriteString("return function()\n")
				for i := tt.upvalues / 2; i < tt.upvalues; i++ {
					fmt.Fprintf(&source, "local v%d = 1\n", i)
				}
				source.WriteString("return function()\nreturn ")
				for i := 0; i < tt.upvalues; i++ {
					if i > 0 {
						source.WriteString(" + ")
					}
					fmt.Fprintf(&source, "v%d", i)
				}
				source.WriteString("\nend\nend\nend\nreturn outer()()()\n")

				L := NewState(Options{Dialect: dialect})
				defer L.Close()
				fn, err := L.LoadString(source.String())
				if tt.wantErr != "" {
					if err == nil {
						t.Fatal("LoadString accepted too many upvalues")
					}
					apiErr, ok := err.(*ApiError)
					if !ok || apiErr.Type != ApiErrorSyntax {
						t.Fatalf("LoadString error = %v (%T), want ApiErrorSyntax", err, err)
					}
					if err.Error() != tt.wantErr {
						t.Fatalf("LoadString error = %q, want %q", err, tt.wantErr)
					}
					return
				}
				if err != nil {
					t.Fatalf("LoadString failed: %v", err)
				}
				if err := L.CallByParam(P{Fn: fn, NRet: 1, Protect: true}); err != nil {
					t.Fatalf("closure execution failed: %v", err)
				}
				if got := L.Get(-1); got != LNumber(tt.upvalues) {
					t.Fatalf("closure returned %v, want %d", got, tt.upvalues)
				}
			})
		}
	}
}
