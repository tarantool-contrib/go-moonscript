package moonscript

// Port of upstream v0.8.0 spec/lint_spec.moon, run against the lint package
// (moonscript.cmd.lint). testdata/lint/ holds more lint cases with
// reference moonc -l output (cmd/moonc/lint_test.go).

import (
	"strings"
	"testing"

	"github.com/tarantool-contrib/go-moonscript/moonscript/lint"
)

// lintCode is moonscript.cmd.lint.lint_code(code, name, nil, opts): it
// returns the report and false when there's nothing to report (Lua nil).
// Errors (parse errors, crashes) aren't expected by any case and panic.
var lintCode = func(code, name string, opts lintOpts) (string, bool) {
	report, ok, err := lint.Code(code, name, nil, lint.Options{Stages: opts.stages, Format: opts.format})
	if err != nil {
		panic(err)
	}
	return report, ok
}

type lintOpts struct {
	stages []string // nil: every stage
	format string   // "" (default) or "compact"
}

var lintSpecCases = []struct {
	name, code, fname string
	opts              lintOpts
	want              string // "" means lint_code returns nil
	wantContains      []string
	wantNotContains   []string
}{
	{name: "reports unused assignments in nested blocks", code: `
		if true
		  unused = 1
	`, want: `
		string input

		line 2: assigned but unused ` + "`unused`" + `
		====================================
		>   unused = 1
	`},
	{name: "reports assigning to constant import", code: `
		import insert from table
		insert = 5
	`, want: `
		string input

		line 2: assigning to constant ` + "`insert`" + `
		======================================
		> insert = 5
	`},
	{name: "reports update op on constant import", code: `
		import count from require "thing"
		count += 1
	`, want: `
		string input

		line 2: assigning to constant ` + "`count`" + `
		=====================================
		> count += 1
	`},
	{name: "allows shadowing a constant import", code: `
		import insert from table
		insert {}, 1
		f = (insert) -> insert
		g = ->
		  local insert
		  insert = 5
		  insert
		f g!
	`},
	{name: "allows loop variables shadowing a constant import", code: `
		import x from table
		f = -> x
		for x = 1, 2
		  x = 3
		  f!
		for x in ipairs {}
		  x = 4
		  f!
		for x in *{1, 2}
		  x = 5
		  f!
	`},
	{name: "flags import overwriting an enclosing binding", code: `
		insert = "hello"
		f = ->
		  import insert from table
		  insert
		f!
	`, want: `
		string input

		line 3: import overwrites existing binding ` + "`insert`" + `
		===================================================
		>   import insert from table
	`},
	{name: "flags import overwriting a binding in the same scope", code: `
		insert = "hello"
		import insert from table
		insert {}, 1
	`, want: `
		string input

		line 2: import overwrites existing binding ` + "`insert`" + `
		===================================================
		> import insert from table
	`},
	{name: "flags a final import overwriting an enclosing binding", code: `
		insert = "hello"
		f = ->
		  print insert
		  import insert from table
		f!
	`, want: `
		string input

		line 4: import overwrites existing binding ` + "`insert`" + `
		===================================================
		>   import insert from table
	`},
	{name: "flags import inside nested function overwriting a declared local", code: `
		f = ->
		  local check_app_version
		  reload = ->
		    import check_app_version from require "helpers.api"
		    print "reloaded"
		  reload!
		  check_app_version!
		f!
	`, want: `
		string input

		line 4: import overwrites existing binding ` + "`check_app_version`" + `
		==============================================================
		>     import check_app_version from require "helpers.api"
	`},
	{name: "flags a repeated import", code: `
		import insert from table
		insert {}, 1
		do
		  import insert from require "custom"
		  insert 2
	`, want: `
		string input

		line 4: assigning to constant ` + "`insert`" + `
		=======================================
		>   import insert from require "custom"
	`},
	{name: "limits reporting to the given stages (constant_assign)", code: lintStagesCode,
		opts: lintOpts{stages: []string{"constant_assign"}}, want: `
		string input

		line 2: assigning to constant ` + "`insert`" + `
		======================================
		> insert = 5
	`},
	{name: "limits reporting to the given stages (global_access, unused)", code: lintStagesCode,
		opts:            lintOpts{stages: []string{"global_access", "unused"}},
		wantContains:    []string{"accessing global", "assigned but unused"},
		wantNotContains: []string{"assigning to constant"}},
	{name: "limits reporting to the given stages (import_overwrite)", code: lintStagesCode,
		opts: lintOpts{stages: []string{"import_overwrite"}}},
	{name: "limits reporting to the given stages (none)", code: lintStagesCode,
		opts: lintOpts{stages: []string{}}},
	{name: "formats output with the compact format", code: `
		import insert from table
		insert = 5
		do
		  unused_var = 1
		f = -> missing_global 10
		f!
	`, fname: "test.moon", opts: lintOpts{format: "compact"}, want: `
		test.moon:2:1: assigning to constant ` + "`insert`" + ` [constant_assign]
		test.moon:4:3: assigned but unused ` + "`unused_var`" + ` [unused]
		test.moon:5:7: accessing global ` + "`missing_global`" + ` [global_access]
	`},
	{name: "combines compact format with stage filter", code: `
		import insert from table
		insert = 5
		missing_global 10
	`, fname: "test.moon", opts: lintOpts{format: "compact", stages: []string{"global_access"}},
		want: "\ntest.moon:3:1: accessing global `missing_global` [global_access]"},
	{name: "compact format returns nothing for clean code", code: "\nx = 5\nprint x",
		fname: "test.moon", opts: lintOpts{format: "compact"}},
}

const lintStagesCode = `
	import insert from table
	insert = 5
	do
	  unused_var = 1
	missing_global 10
`

func TestLintSpec(t *testing.T) {
	for _, c := range lintSpecCases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := lintCode(unindent(c.code), c.fname, c.opts)
			switch {
			case c.wantContains != nil:
				for _, s := range c.wantContains {
					if !ok || !strings.Contains(got, s) {
						t.Errorf("report %q doesn't contain %q", got, s)
					}
				}
				for _, s := range c.wantNotContains {
					if strings.Contains(got, s) {
						t.Errorf("report %q contains %q", got, s)
					}
				}
			case c.want == "":
				if ok {
					t.Errorf("expected no report, got\n%s", got)
				}
			default:
				if want := unindent(c.want); !ok || got != want {
					t.Errorf("report mismatch\n--- want ---\n%s\n--- got ---\n%s", want, got)
				}
			}
		})
	}
}
