# Changes of the gopher-lua fork

`lua/` is a fork of [gopher-lua](https://github.com/yuin/gopher-lua)
v1.1.2 (MIT, see `LICENSE`). This file lists what the fork changes and
adds, where each change comes from (other forks and pull requests are
credited by number and author) and how it is tested. Add an entry with
every change to `lua/`.

The full diff against the original is not kept in the repository:
`scripts/diff-upstream.sh` prints it (the original from the Go module
cache, with its import paths rewritten to the fork's first, so that the
diff does not show that change). Until 2026-11-28 it was kept as
`lua/moonscript.patch`, whose header this file was.

  - Upvalue closing on errors: open upvalues are closed by PCall (and a
    dying coroutine) for the frames an error discards only, not for every
    frame (state.go, vm.go).
  - Debug hooks: debug.sethook / debug.gethook with Lua 5.1 semantics, and
    LState.SetHook / GetHook (hook.go; the VM calls in _vm.go, _state.go,
    coroutinelib.go, debuglib.go, value.go). debug.getinfo reports a level
    lost to a tail call as Lua 5.1's "(tail call)" and, like Lua 5.1,
    fills in only the fields its second argument asks for.
  - go vet: calls of RaiseError/raiseError with a message that isn't a
    constant format pass it as "%s" (Go 1.24's printf check).
  - Line numbers: instructions get the lines luac gives them, so that
    line hooks fire on the same lines as in Lua 5.1 (compile.go; the
    parser records where call arguments and table constructors start and
    end: ast/expr.go, parse/lexer.go, parse/parser.go.y).

  - Fixes ported from other gopher-lua forks and pull requests (Lua 5.1
    compatibility tests: compat_test.go runs testdata/compat/*.lua and
    compares their output with PUC Lua 5.1.5's in testdata/compat/ref,
    recorded by scripts/gen-compat-ref.sh; Go tests in *_fix_test.go and lib_fix_*_test.go):
    - compile.go: the jump-to-jump optimization stops at backward jumps
      (yuin/gopher-lua#561 by ViktorBystrov72, issue #540).
    - compile.go: TESTSET for the last operand of `and`, so a false call
      result reaches the target (yuin/gopher-lua#553 by skyon0522-ai, #490).
    - compile.go: no K propagation of the table of an assignment target,
      ("s").k = v (yuin/gopher-lua#538 by snowyukitty, issue #310).
    - compile.go: multiple assignment evaluates every expression before
      assigning (yuin/gopher-lua#560 by phinze, issue #440).
    - compile.go: a generic for sets all 3 of its hidden registers
      (yuin/gopher-lua#528 by faelys, issue #514), and its explist is
      compiled before its locals (issue #564, own fix).
    - compile.go, function.go: debug.getlocal scopes as in luac (issues #413,
      #252; LilithGames/gopher-lua 92ca436742, its copy zyedidia/gopher-lua
      ee3da8af8b, and taybart/midnight d4a2911c41 by Taylor Bartlett,
      extended).
    - compile.go: "too many upvalues" compile error instead of a runtime
      panic, and limit errors report their line (yuin/gopher-lua#556 by
      kimjooyoon, issues #554, #555; the limit stays 255).
    - compile.go: extended SETLIST for constructors with more than 25550
      positional items (yuin/gopher-lua#537 by efiguti; its API additions
      not taken), and constructor flushes around multiple results and
      key=value fields (own fix).
    - utils.go, baselib.go: strings convert to numbers like luaO_str2d
      (strtod: decimal "010", hex floats, inf/nan, C whitespace) in
      arithmetic and tonumber(s) (halogencapital/gopher-lua d3de08565a by
      Yahia Sweid and yuin/gopher-lua#478, own implementation).
    - parse/lexer.go: numerals are scanned like read_numeral, "malformed
      number" for 1e, 1e+ (own fix).
    - parse/parser.go.y: at most one trailing separator in a table
      constructor, {1,,2} is a syntax error (issue #517, own fix).
    - compile.go, ast/stmt.go, parse/parser.go.y: `local f = function`
      does not see the local f, only `local function f` does (own fix).

    - value.go, alloc.go, mathlib.go: numbers convert to strings with
      "%.14g" like Lua 5.1 (inf, nan, -0), math.huge is HUGE_VAL
      (yuin/gopher-lua#557 by madhavschahar, #469 by MrParano1d, issue
      #468; GuardAngelY/gopher-lua c6f70aa8e5).
    - strformat.go: string.format is Lua 5.1's str_format (based on
      yuin/gopher-lua#557 by madhavschahar).
    - baselib.go: tonumber(s, base) as strtoul, "base out of range"
      (go-moonscript; completes #473 by hugebubu, #492 by longquan0104,
      #180 by meetmauro, #535 by ljluestc; issues #472, #491).
    - baselib.go: xpcall accepts callable values and, like LuaJIT 2.1,
      extra arguments (yuin/gopher-lua#552 by XinyuGuan, issue #551; #467
      by waschik, issues #454, #464; Anaminus/gopher-lua 24121d5f61).
    - baselib.go, _state.go, value.go: collectgarbage honours its option
      and returns Lua 5.1's results (yuin/gopher-lua#549 by eldologuzzo,
      issue #546).
    - stringlib.go: string.match returns nil when there is no match
      (yuin/gopher-lua#544 by eldologuzzo, issue #543; epikur-io 58a035d98d).
    - stringlib.go: string.gmatch returns a closure over its state
      (yuin/gopher-lua#466 by waschik, issue #462; Urie96 467210daee).
    - oslib.go, utils.go: os.date as Lua 5.1 on glibc strftime in the C
      locale, os.date(nil), *t with yday/isdst (yuin/gopher-lua#534 by
      Andriamanitra, #465 by bvisness, #420 by cezarguimaraes, #244 by
      cnblvr; epikur-io 4a9f4a0297, 6920514e97, 90501ab984; Subtixx
      4c1967af86).
    - mathlib.go: math.random checks its interval and argument count
      (Anaminus/gopher-lua 16a51f2de7).
    - stringlib.go: strings have their own metatable {__index = string}
      (Anaminus/gopher-lua 8bd71831a8).
    - iolib.go: io.open accepts the modes fopen accepts (yuin/gopher-lua#458
      by 0x501D, issue #457); io.popen "w" closes the child's stdin so
      close() returns, and the child inherits the streams it does not pipe
      (yuin/gopher-lua#404 by scr-oath).
    - oslib.go: os.exit with a non-number no longer crashes after closing
      the state; os.exit(true/false) as LuaJIT 2.1 (idea from
      bjorand/gopher-lua 4fce9ef384).
    - loadlib.go: package.config without a trailing newline, as PUC 5.1
      (tarantool/gopher-lua d832a156ee).
    - baselib.go: load(string, chunkname, mode, env) as LuaJIT 2.1
      (awirix/lua bd6991bfbf).
    - tablelib.go: table.sort sorts 1..#t with ltablib's quicksort, same
      order of equal elements and "invalid order function" errors
      (AlohaWu/gopher-lua 05a013494b).
    - _vm.go, mathlib.go: a % b is a - floor(a/b)*b and math.mod is fmod,
      as in Lua 5.1 (difference found by pschlump/gopher-lua 3ef7c4baf3's
      fuzzer).

  - Fixes ported in the second round, with their tests in
    testdata/compat and *_fix_test.go:
    Fixes of coroutines and the VM:
    - Coroutines: a yield across pcall, a metamethod, a generic-for iterator
      or a Go function calling Lua raises Lua 5.1's "attempt to yield across
      metamethod/C-call boundary" (was a nil-pointer crash on the next
      resume): yuin/gopher-lua#548 (eldologuzzo), issue #545
    - Coroutines: the values a coroutine is resumed with are the yield's
      results (nil-filled, extra ones dropped; was a nil-pointer crash),
      tail-called and Go-body yields, Go function bodies return their
      results: https://github.com/mdogan/gopher-lua/commit/76d62e4131
      (Mehmet Dogan; rest go-moonscript)
    - Coroutines: resuming an active coroutine fails with "cannot resume
      normal coroutine" (was a fatal Go stack overflow), status "normal" for
      every active coroutine: https://github.com/mdogan/gopher-
      lua/commit/afcb8e0069 (Mehmet Dogan)
    - Coroutines: a coroutine.wrap coroutine that raises an error is dead:
      https://github.com/mdogan/gopher-lua/commit/7e999239a2 (Mehmet Dogan)
    - Coroutines: Lua 5.1's coroutine.resume/wrap error messages, wrap adds
      the caller's position: go-moonscript
    - Coroutines run under the context of their resumer; coroutine.create
      makes no child context (leak of a child cancelCtx per unfinished
      coroutine): https://github.com/mdogan/gopher-lua/commit/f7b3485bd8
      (Mehmet Dogan); leak fix go-moonscript, cf. yuin/gopher-lua#512
    - coroutine.create/wrap accept Lua functions only, as Lua 5.1: go-
      moonscript
    - Go stack traces for Go panics in coroutines with
      Options.IncludeGoStackTrace: yuin/gopher-lua#539 (eliotee), issue #529
    - Compiler: recursion limit ("chunk has too many syntax levels" instead
      of a fatal Go stack overflow) and constant folding memoised (was
      quadratic): after https://github.com/mdogan/gopher-
      lua/commit/3f80512213 (Mehmet Dogan)
    - where(): no line for a frame that has run no instruction (a registry
      overflow while a tail call sets up its frame was a Go panic, index out
      of range [-1]): issue yuin/gopher-lua#282, go-moonscript
    Fixes of the string library and pattern matching:
    - pm/pm.go: the matcher loops over repeats (* + -) like Lua 5.1's
      max_expand/min_expand instead of recursing once per byte (".*" on 2 MB
      was "pattern/input too complex"), at most 32 captures ("too many
      captures"), back references to unfinished captures raise "invalid
      capture index" and to position captures never match (Go slice panics
      before): https://github.com/mdogan/gopher-
      lua/commit/3f805122133afac1e3bc59d7d3abca7e54880e3f (Mehmet Dogan)
    - pm/pm.go, stringlib.go: pattern matching stops when the LState context
      is done (pm.FindContext): https://github.com/mdogan/gopher-
      lua/commit/64abd4bac7fcfe957c42a6c533956a1851b3e02c (Mehmet Dogan);
      yuin/gopher-lua PR #566 (draarivpatel-ui); issues #559, #521
    - stringlib.go: string.gsub builds its result in one pass (was quadratic
      in the matches): issue yuin/gopher-lua#559; cf.
      https://github.com/mdogan/gopher-
      lua/commit/c360ab642640f1905f30b82c20002c272d22e9f1 (Mehmet Dogan; its
      memory accounting not taken)
    - pm/pm.go: compiled-pattern cache and match-data reuse: yuin/gopher-lua
      PR #558 (pixel2175); literal-pattern fast path: https://github.com/tay
      bart/midnight/commit/9adf4c41965352971f92d646ad9fc635705a0cdb (Taylor
      Bartlett)
    - stringlib.go: string.gsub takes a number as the replacement, lstrlib's
      add_s ("%c" gives "c", a trailing "%" a NUL), "invalid replacement
      value" for table/function results that are not strings or numbers:
      https://github.com/Luzi0390/gopher-
      lua/commit/b443b1476a3269c40ed83e92ca8afd01439c5c9c (Luzi0390; its nil
      replacement not taken)
    - auxlib.go, baselib.go: luaL_check*/opt* coerce numeric strings
      (CheckInt, CheckInt64, OptInt, OptInt64, OptNumber) and OptString
      accepts numbers; select takes string indices: own fix (go-moonscript)
    - stringlib.go: string.gsub with n < 1 replaces nothing (replaced every
      match when the first position matched): own fix (go-moonscript)
    - stringlib.go: string.find/match clamp init to the subject like
      str_find_aux (plain find past the end panicked, find/match past the
      end found nothing, find with "" ignored init), find with a pattern
      without 5.1's SPECIALS is plain, and a leading '^' in gmatch matches
      '^': own fix (go-moonscript, found by the differential test)
    - stringlib.go: string.gmatch matches lazily, one match per iterator
      call like gmatch_aux (it found every match up front): own fix (go-
      moonscript)
    - pm/pm.go: sets parsed like 5.1's classEnd/matchbracketclass ("[%a-z]",
      "[a-c-e]" matched nothing), %f[set] frontiers (5.1.5), malformed items
      (trailing '%', '%b' without delimiters, missing ']', '%f' without '[',
      ')' and '%0') raise their 5.1 error when the matcher reaches them: own
      fix (go-moonscript, found by the differential test)
    - testdata/compat/lib_pattern_fuzz.lua, scripts/gen-pattern-fuzz.sh,
      scripts/pattern-fuzz-gen.lua: differential test of find/plain
      find/match/gsub/gmatch on 3000 generated cases (half literal patterns,
      half matcher patterns) against lua5.1: own (go-moonscript)
    Fixes of the table engine and the table library:
    - table.go, value.go, _vm.go, _state.go, compile.go, utils.go: tables
      have
    - table_hash.go: the hash part is one table of nodes laid out as Lua
    - table_hash.go: keys removed from a table no longer stay in it forever
    - table.go, baselib.go: LTable.RawSet ignores nil and NaN keys, rawset
    - table.go, baselib.go: next/pairs raise "invalid key to 'next'" for a
    - tablelib.go: table.insert, table.remove and table.maxn as Lua 5.1's
    - tablelib.go: table.concat as Lua 5.1's tconcat, built in Go instead of
    - function.go, compile.go, _vm.go, _state.go: string constants carry
      their
    - auxlib.go, iolib.go: library functions are added to their tables in
  - The dialect option, Options.Dialect (DialectLua51, the default, or
    DialectLuaJIT) and LState.Dialect: go-moonscript. Third round of
    fixes, by area, with their tests in testdata/compat, testdata/dialect
    and *_fix_test.go:
    Fixes of error messages:
    - Runtime errors: Lua 5.1's messages (ldebug.c), with the variable from
      getobjname/symbexec over the bytecode ("attempt to index field 'b' (a
      nil value)", "attempt to call method 'm' (a nil value)", "attempt to
      compare two table values", "attempt to get length of", "attempt to
      perform arithmetic on", "attempt to concatenate", "loop in gettable");
      were "attempt to index a non-table object(nil) with key 'c'", "cannot
      perform add operation between ...", "__len undefined", "__unm
      undefined", "attempt to call a non-function object" (go-moonscript).
    - Numeric for: Lua 5.1's checks of initial value, limit and step before
      the loop, with string coercion and the "'for' initial value must be a
      number" messages (go-moonscript).
    - Length operator: no __len for tables, as in Lua 5.1 and LuaJIT 2.1
      without 5.2 compat (go-moonscript).
    - debug.getinfo/GetInfo 'n': name and namewhat from the calling
      instruction (Lua 5.1's getfuncname; Debug.NameWhat added); LuaJIT
      dialect names metamethods ("metamethod", "__index") (go-moonscript).
    - Argument errors: luaL_argerror/luaL_typerror messages (quoted name,
      '?', "no value", "calling 'x' on bad self"); io.input/io.output "FILE*
      expected" (was "string or file expedted") and nil returning the
      current file (go-moonscript).
    - Error positions: RaiseError adds luaL_where(1) (none when the caller
      is a Go function such as pcall) and no leading space without position;
      error(): exact level (level 2 was one too shallow), any value
      including none, numbers get the position; assert checks its argument;
      table.concat/table.insert errors get luaL_error's position (go-
      moonscript).
    - Compiler: the generic for's iterator call has luac's line (the token
      after "in") (go-moonscript).
    Fixes of the parser and the compiler:
    - parse/parser.go: recursive-descent parser ported from Lua 5.1's
      lparser.c (DialectLua51) and LuaJIT 2.1's lj_parse.c (DialectLuaJIT),
      building the same AST with the same lines; replaces the goyacc grammar
      (parse/parser.go.y and parse/Makefile removed, so the header's goyacc
      regeneration note no longer applies); Lua 5.1 syntax error messages
      ("<chunk>:<line>: <message> near '<token>'", e.g. "'end' expected (to
      close 'function' at line 3)"); limits LUAI_MAXCCALLS 200, LUAI_MAXVARS
      200, LUAI_MAXUPVALUES 60 with lua5.1's messages: go-moonscript
    - parse/lexer.go: lexer ported from llex.c / lj_lex.c: lua5.1's numeral,
      string and long-string errors and their tokens; escapes per dialect
      (5.1: an unknown escape keeps the character; LuaJIT: \x, \z, \u{});
      hex floats and high-byte identifiers under DialectLuaJIT only;
      LL/ULL/i suffixes rejected under both dialects (they need FFI cdata):
      go-moonscript
    - dialect-dependent syntax: goto and labels only under DialectLuaJIT
      (under DialectLua51 goto is a name; a lone ';' is rejected under
      both, as by lua5.1 and LuaJIT 2.1), selected in
      LState.Load from L.Dialect() via parse.ParseDialect
      (_state.go/state.go); a parse error with no token is reported as
      "<chunk>:<line>: <message>": go-moonscript
    - parse/parser.go, compile.go, ast/misc.go: no implicit "arg" in
      vararg functions under DialectLuaJIT (ast.ParList.NoArg; no local,
      no table), as in LuaJIT 2.1; DialectLua51 keeps LUA_COMPAT_VARARG's:
      go-moonscript
    - parse/parser.go, parse/jitcode.go: under DialectLuaJIT a goto or break
      has the line of its jump instruction, as LuaJIT reports it: right
      after a block whose locals are upvalues, the line of that block's
      UCLO, into which bcemit_jmp merges the jump ("undefined label", "no
      loop to break", "jumps into the scope of local"); the instructions'
      lines come from the model of LuaJIT's code generator: go-moonscript
    - parse/parser.go, parse/lexer.go, _state.go/state.go: Lua 5.1's limit
      of 32767 local variable records per function ("too many local
      variables", raised without a position: parse.NoLine, which
      LState.Load keeps as it is): go-moonscript
    - parse/lexer.go: under DialectLua51 check_next takes a NUL byte for
      any character of its set (strchr finds the terminator) and a numeral
      is converted up to a NUL ("x = 1\0garbage", "x = a.\0b" load as in
      lua5.1): go-moonscript
    - parse/lexer.go, luajitlib.go, auxlib.go: under DialectLuaJIT bytecode
      after a "#" line or a UTF-8 BOM is refused with "cannot load
      malformed bytecode" (lj_lex_setup), before the mode check of load
      and loadfile (JITLoadModeError); loadfile and LState.LoadFile leave
      the "#" line to the parser: go-moonscript
    - testdata/compat/parse_vararg_arg, parse_goto_line, parse_locvars_limit,
      parse_nul_byte, parse_shebang_bytecode (lua5.1 refs for .lua, luajit
      refs for .jit.lua): go-moonscript
    - DialectLuaJIT checks the limits of LuaJIT's variable stack and code
      generator (parse/jitcode.go, adapted from bigbes/go-luascope
      parser/codegen.go and parser/bytecode.go, BSD-2-Clause, a port of
      lj_parse.c's code generator): the parser emits LuaJIT's bytecode and
      its lines with the tree, through the expression descriptors its
      expression functions pass around (p.jit), and drops it when a function
      ends: "chunk has more than 65476 local variables" (LJ_MAX_VSTACK),
      "control structure too long", "function or expression too complex near
      <token>" (LJ_MAX_SLOTS 250, a call frame takes two slots), more than
      65536 constants, "function too long for return fixup", at LuaJIT's
      token and line; the parser reuses a function's state per depth and its
      memory across chunks (sync.Pool); parse/jitcode_test.go has
      go-luascope's snippets at these limits: go-moonscript
    - DialectLua51: a register overflow is lua5.1's "function or expression
      too complex" near the token luaK_checkstack is at, before a syntax
      error after it: parse/lua51regs.go models lcode.c's register
      allocation (p.r51), run by LState.Load (parse.Lua51Overflow) when the
      compiler needed 200 registers or more, or too many, or the parser
      found a syntax error; compile.go's register limit is what its
      instructions address (256, was MAXSTACK 250), the dialects' limits are
      the parser's models': go-moonscript
    - compile.go: a CLOSURE's register counts in the function's frame (it
      was too small for closures passed as call arguments), and the targets
      of a multiple assignment indexing a local a later target assigns share
      one copy of it, as luac's check_conflict: go-moonscript
    - Tests: limits_fix_test.go (register overflows of 71 families at the
      limits against lua5.1's and luajit's results), compile_regs_fix_test.go;
      compat parse_limits.lua prints the overflow errors (lua5.1 ref),
      parse_limits_on_luajit.jit.lua checks LuaJIT's limits (luajit ref):
      go-moonscript
    - NOTICE credits Lua 5.1.5 (llex.c, lparser.c, lcode.c) and LuaJIT 2.1
      (lj_lex.c, lj_parse.c, lj_bc.h) for the ports in parse/: go-moonscript
    - compile.go: funcContext.ConstIndex looks constants up in a map instead
      of a linear scan (compiling verybig.lua: 2m31s -> 0.69s): go-
      moonscript
    - compile.go: an elseif chain is compiled in a loop rather than as
      nested else blocks (compiling 20k elseifs: 24.7s -> 0.16s): go-
      moonscript
    - compile.go: register limit is Lua 5.1's MAXSTACK 250 (it was 200,
      which counted temporaries) with luaK_checkstack's "function or
      expression too complex" (since superseded: the limit is now 256, what
      instructions address, and the dialects' limits are the parser's models'
      of lcode.c and lj_parse.c, see below); register use as luac (SETTABLE takes a
      local's register or a constant key, nil/true/false as RK constants,
      check_conflict for multiple assignment, LOADNIL elision, scope closed
      before the final RETURN), so what lua5.1 compiles compiles (vshard
      storage/init.lua); builds on yuin/gopher-lua#556 (kimjooyoon) and #560
      (phinze), already in the patch; reviewed against the survey notes
      (glnotes commits.tsv/prs.tsv): no fork commit or PR provides these
      changes: go-moonscript
    - compile.go: CompileError reads "<chunk>:<line>: <message>" like Lua's
      (was "compile error near line(<line>) <chunk>: <message>"); goto/label
      errors use LuaJIT's messages: go-moonscript
    - compile_fix_test.go: the yuin/gopher-lua#556 (kimjooyoon) limit tests
      adjusted to Lua 5.1's limits and messages: go-moonscript
    - testdata/compat/parse_errors.lua, parse_lexer.lua, parse_limits.lua (+
      lua5.1 refs): syntax, lexer and limit errors checked against lua5.1:
      go-moonscript
    Fixes of the standard library:
    - Lua 5.1 functions gopher-lua lacked (lib51.go): gcinfo,
      table.foreach/foreachi/getn, table.setn ("'setn' is obsolete"; absent
      under DialectLuaJIT), math.mod as math.fmod (absent under
      DialectLuaJIT), string.gfind only under DialectLua51
      (LUA_COMPAT_GFIND): go-moonscript
    - package.loadlib returns nil, msg, "open" (no such file) or "absent" as
      Lua 5.1 does, instead of raising (lib51.go): go-moonscript
    - os.execute() with no argument, os.execute's wait status,
      os.remove/os.rename failures (nil, "name: strerror", errno),
      os.setlocale's results for "C"/"" and the categories, os.time's errors
      for missing fields (oslib51.go): go-moonscript
    - debug.getinfo as Lua 5.1's db_getinfo: optional thread argument, what
      ("C"/"main"/"Lua"), source "=[C]" for Go functions, short_src,
      linedefined/lastlinedefined, nups, func, activelines, "invalid option"
      and "level out of range" errors (source and short_src: see Lua chunk
      names below; runtime/compat.go's getinfo wrapper since removed): go-
      moonscript
    - debug.traceback (db_errorfb: optional thread, message passthrough),
      debug.setfenv (table environment, "'setfenv' cannot change environment
      of given object"), debug.setmetatable returns true, debug.getregistry,
      debug.debug: go-moonscript
    - getfenv/setfenv levels and errors as lbaselib.c getfunc ("level must
      be non-negative", "invalid level", tail calls); next with an invalid
      key; setmetatable's argument checks; newproxy's argument error: go-
      moonscript
    - tostring calls __tostring as luaL_callmeta does; print calls the
      global tostring ("'tostring' must return a string to 'print'"): go-
      moonscript
    - string.char range checks ("invalid value"): go-moonscript
    - '#' ignores __len on tables under both dialects (OP_LEN in _vm.go):
      go-moonscript
    - string.dump writes the copy's own binary chunks ("\27Lua" signature
      with a version byte that is not PUC's 0x51; "\27LJ" with the copy's
      own version under DialectLuaJIT, string.dump(f, true) strips),
      loadstring/load read them back; closures' upvalues detached as in 5.1;
      "unable to dump given function" for Go functions (dump.go): go-
      moonscript
    - io library rewritten as Lua 5.1's liolib.c over C stdio: buffered
      stdout (full buffering when not a tty, line buffering on a tty),
      stderr unbuffered, print through the same buffer (runtime/compat.go's
      print too), flush on io.flush/file:flush, before reading stdin, at
      os.exit, at L.Close() and on every cmd/moon exit path; closing a
      standard file, io.input(nil)/io.output(nil), tostring(file) "file
      (0x...)"/"file (closed)", popen'd file close result, closed-file
      error, read("*n"), io.lines with no file, file:seek/setvbuf results;
      popen close keeps the pipe-closing fix (from yuin/gopher-lua PR #404,
      scr-oath): go-moonscript
    - math.random/math.randomseed reproduce PUC Lua 5.1 on glibc
      (rand()/srand(), TYPE_3 random_r, seed 1 when never seeded) under
      DialectLua51, and LuaJIT 2.1's Tausworthe PRNG (lj_prng / lib_math.c)
      under DialectLuaJIT (mathrandom.go): go-moonscript
    - tests: compat scripts lib_base_51, lib_debug_getinfo, lib_io_51,
      lib_io_read_number, lib_math_random_seq, lib_os_51, lib_string_dump
      with lua5.1 refs; dialect scripts lib_io_read, lib_math_random_seq,
      lib_string_dump (.luajit.lua) with luajit refs; Go tests
      lib_fix_dump_test.go, lib_fix_os_test.go, lib_fix_stdout_test.go,
      lib_fix_dialect_test.go; scripts/gen-compat-ref.sh also records the
      dialect refs; cmd/moon smoke skips for math.random removed and the
      luasuites "unstable" math.random item dropped; check-gopher-lua-
      suite.sh accepts the changed pristine assertions: go-moonscript
    Fixes of garbage collection and memory:
    - gc.go (new; hooks in _state.go
      SetMetatable/NewState/Close/newThread/NewThread, the callFrameStack
      ClearUnused method and pooled segments cleared, value.go Global.gc,
      _vm.go allocation checkpoints at NEWTABLE/CONCAT/CLOSURE): Lua 5.1's
      garbage collection as programs see it over Go's collector: weak tables
      (__mode "k"/"v"/"kv", read at each cycle; the objects the Lua roots
      reach stay, Go's collector decides for the others with their weak
      references taken out for one runtime.GC(), weak.Pointer), __gc for
      userdata (newproxy proxies and userdata given a metatable with __gc;
      unreachable from the Lua roots, marked as Lua 5.1 does, so cycles
      through the userdata are finalized; run on the state's goroutine at
      safe points, newest first, errors propagate as in GCTM, all remaining
      ones at Close as lua_close), stale registers and call frames cleared
      at a cycle, automatic cycles after Go collects: go-moonscript
    - baselib.go -> gc.go: collectgarbage "step" works through a cycle of
      the size of the heap (stepmul) and returns true only when it finishes
      it, "stop"/"restart" stop the automatic cycles, "count" is the heap
      growth since the state was created, "isrunning" for DialectLuaJIT;
      newproxy(bool|proxy) with Lua 5.1's argument check; gcinfo
      (lua_getgccount) on the same count: go-moonscript
    - gc.go, _state.go Options.MemoryLimit,
      LState.SetMemoryLimit/MemoryLimit; checks in _vm.go stringConcat,
      stringlib.go string.rep and gsub, strformat.go, tablelib.go
      table.concat, table.go newLTable/resize/resizeArray: an optional
      memory limit, past which a big allocation raises the Lua error "not
      enough memory" (catchable by pcall) instead of a fatal out-of-memory
      error of the Go runtime: go-moonscript
    - runtime/compat.go: the chunk-name table of debug.getinfo no longer
      grows with every loadstring whose chunk id gives the source back (it
      kept every loaded string alive; the table was later removed, protos
      keep their chunk name): go-moonscript
    - cmd/moon/main.go: the runner sets a memory limit (GOMEMLIMIT if set,
      else 4 GiB); internal/luasuites: the interpreter sets 1 GiB (a third
      of its 3 GiB address space), closure.lua is no longer a slow unit: go-
      moonscript
    Fixes of the LuaJIT dialect:
    - LuaJIT 2.1 dialect libraries (luajitlib.go, openLuaJIT hooks in the
      Open functions, linit.go): the bit library, an interpreter-only jit
      module (jit.on/off/flush/status/attach,
      jit.version/version_num/os/arch), table.new/table.clear/table.move, no
      table.setn, math.log(x, base), LuaJIT's math.max/min, no
      math.mod/string.gfind, string.rep(s, n, sep), coroutine.isyieldable,
      package.searchpath, debug.upvalueid/upvaluejoin, debug.getinfo "u"
      nparams/isvararg, debug.getlocal(f, n), collectgarbage("isrunning"),
      assert raising any message value, os.date E/O modifiers; all gated on
      L.Dialect() == DialectLuaJIT (go-moonscript).
    - load/loadstring/loadfile: Lua 5.1's load(func [, chunkname]) under
      DialectLua51; LuaJIT's load(chunk [, chunkname [, mode [, env]]]) with
      string chunks, mode and env moved to the LuaJIT dialect (jitLoad;
      chunk names now in the core, see below): awirix/lua bd6991bfbf
      (string chunk, mode, env), the rest go-moonscript.
    - xpcall: extra arguments passed to the function under DialectLuaJIT
      only, ignored as in Lua 5.1 otherwise: yuin/gopher-lua#467 (waschik),
      Anaminus/gopher-lua 24121d5f61 (the extra arguments); the dialect
      split go-moonscript.
    - os.exit(true/false) as EXIT_SUCCESS/EXIT_FAILURE under DialectLuaJIT
      only: bjorand/gopher-lua 4fce9ef384 (idea); the gating go-moonscript.
    - tonumber(s, base) after a '-': LuaJIT negates the value, Lua 5.1 keeps
      strtoul's result modulo 2^64; LuaJIT scans the whole string (go-
      moonscript).
    - string.format and number-to-string conversion of LuaJIT 2.1
      (luajit_strformat.go, LState.numberString/lvAsString in tostring,
      concatenation, string/table/io functions, runtime's compat layer):
      lj_strfmt rounding of ties away from zero, "nan", %F, %a/%A, 64-bit
      %d/%x, %c, %s/%q taking any value (go-moonscript).
    - runtime: StateOptions(dialect) and ParseDialect for creating a state
      in the LuaJIT dialect (go-moonscript).
    - Tests: compat_test.go runs *.jit.lua scripts with DialectLuaJIT, new
      compat scripts with lua5.1/luajit references; luasuites runs LuaJIT-
      tests and the lua-Harness LuaJIT view on the VM in its LuaJIT dialect
      (LUASUITES_DIALECT) instead of a jit stub; LuaJIT-tests'
      lang/stackov.lua skipped (registry overflow and traceback depth, ~120
      s) (go-moonscript).
    - check-gopher-lua-suite.sh: _lua5.1-tests/math.lua lines 47, 48, 52
      corrected to PUC Lua 5.1's tonumber("-...", base) on 64-bit (2^64 - n,
      checked with lua5.1) (go-moonscript).
    - Close runs the __gc metamethods on an emptied stack, as lua_close
      does: go-moonscript
    - DialectLuaJIT scans numbers as lj_strscan (parse/strscan.go, adapted
      from bigbes/go-luascope parser/number.go, BSD-2-Clause): 0b numerals,
      LuaJIT's limits and rounding, tonumber's STRSCAN_OPT_TONUM; the FFI
      literals 1LL/1ULL/1i parse and raise "needs the FFI, which is not
      supported" when evaluated (ast.CdataNumberExpr) (go-moonscript).
    - gcinfo()/collectgarbage("count") read the heap exactly (ReadMemStats
      flushes the per-P span caches that /memory/classes/heap/objects lags
      behind) and leave out the collector's own scratch, kept in a mark set
      of known size: a cycle that frees Lua garbage reports less, as
      Lua 5.1 programs (gc.lua) expect (go-moonscript).

  - sandbox/: a new package (first version) that builds an environment
    for untrusted code from an allow-list of globals ("print", "os.time",
    "string.*", "*") and modules (require of those only); masks never
    match functions that leave the environment or reach the host
    (sandbox.Risky; allowed by full name only), library tables are
    copied, load/loadstring are wrapped to load text into the
    environment, sandbox.Load refuses binary chunks. No limits or file
    system rules yet: see the design issue: go-moonscript

  - Plain-state behaviour as Lua 5.1/LuaJIT 2.1 (go-moonscript):
    - pm/pm.go, stringlib.go: an unclosed '(' runs to the end of the
      pattern and matches with the capture left open, as lstrlib's
      do_match (CAP_UNFINISHED): "unfinished capture" only where its value
      is pushed (find, match, gmatch, gsub with a function, a table or
      %1), a '$' at the end is the anchor inside it (was "unfinished
      capture at EOS" for any such pattern); compat
      lib_pattern_unfinished(.jit).lua, 300 more pattern fuzz cases.
    - chunkname.go (new), _state.go/state.go, function.go, dump.go,
      debuglib.go, baselib.go, luajitlib.go: Lua chunk names
      (yuin/gopher-lua#547; PR #550's approach not taken):
      LState.LoadChunk(reader, chunkname) is lua_load ("@file", "=name",
      the source text), ChunkID is luaO_chunkid / lj_debug_shortname (Lua
      5.1's syntax errors with MAXSRC 80); FunctionProto.Source and
      Debug.ChunkName hold the chunk name, debug.getinfo's source and
      short_src are Lua's; LState.Load keeps display names (chunk name
      "=" + name); loadstring, load and LuaJIT's load name chunks as Lua
      does; string.dump keeps the chunk name (revision 2).
    - chunkname.go, auxlib.go, baselib.go, luajitlib.go: loadfile, dofile,
      LState.LoadFile/DoFile are luaL_loadfile: "cannot open/read <name>:
      <strerror>" (an ApiErrorFile, Cause the Go error; Strerror), nil for
      stdin, a "#" line skipped keeping the lines (a binary chunk after it
      under DialectLua51 from the first ESC byte), dofile raises the
      message as it is.
    - loadlib.go, baselib.go: require is ll_require with Lua 5.1's four
      loaders (preload, path, cpath, C root; a C library found is
      "dynamic libraries not enabled"), its messages, package.loaders read
      from the package table; package.loaded is the registry's _LOADED.
    - runtime/compat.go, runtime/luajit.go: the runner's copies of these
      (ChunkID, the chunk-name table, syntaxError, readChunkFile, the
      load/loadstring/loadfile/dofile and require wrappers, the getinfo
      wrapper) removed; the runner's output is unchanged.
    - Tests: compat lib_chunkname, lib_loadfile, lib_require (.lua with
      lua5.1 refs, .jit.lua with luajit refs, files in
      testdata/compat/files), chunkname_fix_test.go; compat scripts run
      as "@<name>.lua" in testdata/compat.

  - Table API for the libraries built on the fork (go-moonscript):
    - table.go: LTable.All, an iter.Seq2 over the pairs in next's order
      (array part, then the hash part in node or insertion order), with
      next's semantics under modification: assigning to existing fields,
      nil included, skips or repeats no pair; adding keys is undefined but
      never panics nor runs forever (the parts are walked as they were, at
      most one step per node in insertion order). LTable.Array is the raw
      ipairs. ForEach unchanged. Tests and benchmarks:
      table_iter_fix_test.go.
    - table.go, table_hash.go, _vm.go/vm.go, _state.go/state.go, auxlib.go,
      baselib.go, tablelib.go, luajitlib.go: frozen tables: LTable.Freeze
      (for good, shallow), Frozen, FreezeDeep (keys, values, metatables),
      ErrFrozenTable. Lua code gets "attempt to modify a read-only table"
      (catchable by pcall, at the assignment's or the library caller's
      position) from assignments (SETTABLE, SETTABLEKS, SETGLOBAL, the
      mainLoop fast path, __newindex chains that reach a frozen table; a
      frozen table's own __newindex is not called), SETLIST, rawset,
      table.insert/remove/sort, LuaJIT's table.clear/table.move,
      setmetatable and debug.setmetatable; LState's SetField, SetTable,
      SetGlobal, RawSet, RawSetInt and SetMetatable raise the Lua error,
      LTable's RawSet/RawSetInt/RawSetString/RawSetH/Append/Insert/Remove
      panic with ErrFrozenTable. The mark is hashPart.lastfree = -1 (a
      frozen table gets no new key; the field moves next to the array
      part); the VM tests it only on assignments. Weak entries of a frozen
      table are still cleared. Tests: table_frozen_fix_test.go (both
      dialects).
    - sandbox/: Env freezes the library copies it gives;
      sandbox.FreezeLibraries freezes the state's library tables and the
      string and file metatables (the globals and package stay writable).
    - _state.go/state.go, value.go: LState.FreezeTypeMetatables makes the
      metatables the types other than tables and userdata share per state
      final: SetMetatable/debug.setmetatable on a string, number, boolean,
      nil, function, thread or channel raises lua5.1's "cannot change a
      protected metatable" (off by default, as in Lua);
      sandbox.FreezeLibraries calls it. Tests: typemt_fix_test.go.

  - Loading a syntax tree (go-moonscript):
    - compileast.go (new): lua.CompileAST(chunk, chunkname, dialect) and
      LState.LoadAST(chunk, chunkname) compile and load a tree of package
      ast that other code built or transformed, as LoadChunk does source
      (Lua chunk names, LoadChunk's ids; errors an ApiErrorSyntax whose
      Cause is the *CompileError); a parsed chunk's tree gives LoadChunk's
      function (the same dump). After yuin/gopher-lua#275 (xushiwei,
      LoadAst), which compiled the tree unchecked; #402 (SeigeC,
      CompileWithParNames) not taken: a "local a, b = ..." first does it
      (ExampleCompileAST).
    - compileast.go: a tree is checked before it is compiled (astCheck):
      nil and unknown nodes, operators, numerals, empty lists, call
      statements and calls, assignment targets and function names, "..."
      outside a vararg function, break outside a loop, 200 locals and 60
      upvalues per function (lua5.1's messages), 32767 local records
      (DialectLua51), the compiler's nesting limit (cycles), goto, labels
      and cdata numerals only under DialectLuaJIT, negative lines: a
      *CompileError at the node's line. Only source loading has the
      parsers' messages near a token, their 200 syntax levels and the
      models of lua5.1's registers and LuaJIT's code generator.
    - compileast.go: a node without a line takes the line of the node
      before it in source order (1 at the start), without a last line the
      last line of its subtree, on a copy of the tree (lineFiller).
    - compile.go: compiling leaves the tree as it is (a numeric for's
      default step and the folded operand of a unary minus were stored in
      it); compileDialect: no implicit "arg" under DialectLuaJIT whatever
      ParList.NoArg (LState.Load uses it); a goto into a local's scope
      names the first local it jumps into, as LuaJIT (an index panic in a
      block without locals); the cdata numeral function takes "".
    - compile.go, found by FuzzCompileAST, from source too: an assignment
      to one local with extra values evaluates them all first (i = 5,
      f(i)); "..." for a local's register is loaded into a free one and
      moved (VARARG cleared the registers above it: a nil-pointer panic);
      backward jumps and FORPREP/FORLOOP over more than sBx instructions
      are lua5.1's "control structure too long" at the jump's line (they
      jumped out of the code; "too long to jump." at line 0 before); more
      than 262143 closures in a function "constant table overflow".
    - Tests: compileast_fix_test.go (round trip of the test scripts in
      both dialects, compat scripts through LoadAST, errors, limits,
      lines, the tree unchanged), compileast_fuzz_fix_test.go
      (FuzzCompileAST, TestCompileASTMutations), compileast_example_test.go,
      compile_fix_test.go TestCompileLongJumps, compat
      compile_assign_extra.lua (lua5.1 ref); internal/luasuites/load_test.go
      (round trip of the archives, BenchmarkLoadArchives): go-moonscript

  - Positions in the syntax tree (go-moonscript):
    - ast/ast.go, ast/pos.go, ast/expr.go, ast/stmt.go, ast/misc.go,
      ast/doc.go: every element of a tree (ast.Element: statements,
      expressions, table fields, parameter lists, function names, the
      chunk) has a Span, the byte offsets [Start, End) of its text from
      its first token to its last; names held as strings and blocks have
      their spans next to them (NameSpans, NameSpan, LabelSpan,
      MethodSpan, VarargSpan, StmtsSpan, ThenSpan, ElseSpan); ast.Lines
      gives an offset's line and byte column (go-luascope's), from the
      starts of the lines found on first use, counted as the lexer counts
      them. A node keeps two 32-bit offsets (SpanBase), not a Pos. Line,
      LastLine and the other fields are unchanged; the compiler does not
      read spans: every source compiles to the same dump.
    - ast/expr.go: parentheses are Expr.Parens (count, span of the
      outermost pair) and OuterSpan, not nodes; FuncCallExpr.AdjustRet
      and Comma3Expr.AdjustRet still tell the compiler.
    - ast/chunk.go, parse/parser.go, parse/lexer.go: parse.ParseChunk(src,
      name, parse.Options) returns an ast.Chunk: the statements
      ParseBytes returns, the block's span, Lines, the comments with
      Options.Comments (ast.Comment: span, text as go-luascope's, long or
      not), the header the lexer skipped (LuaJIT's BOM and "#" line; under
      Lua51 the "#" line with Options.Shebang, as luaL_loadfile). Parse,
      ParseDialect and ParseBytes unchanged.
    - ast/walk.go: ast.Walk (go/ast's Visitor) and ast.Inspect
      (go-luascope's: no call with nil).
    - parse/lexer.go: tokens carry their offsets; white space is skipped
      a run at a time. parse/parser.go: the lists of the tree (statements,
      expressions, table fields) are built on pooled stacks and copied at
      their size; a function expression keeps its body's node; Dump
      leaves out spans and unexported fields.
    - compileast.go: the line filler copies a field's span.
    - Tests: parse/span_test.go, parse/span_check_test.go (the spans of
      every element of the compat scripts and of the luasuites archives
      against the lexer's tokens, each element reparsed alone, comments,
      lines; FuzzSpans); internal/luasuites/load_test.go
      BenchmarkParseArchives: go-moonscript

  - Tokens of the source (go-moonscript):
    - ast/tokens.go, ast/chunk.go, ast/doc.go: ast.Tokens, the tokens of
      a source as written: each token of the grammar and each piece of
      trivia as a token of its own (TokenHeader, TokenWhitespace within a
      line, TokenNewline per line break, TokenComment), then an empty
      TokenEOF; their texts in order are the source. ast.Lexeme is a
      TokenKind and a span in two 32-bit offsets (12 bytes; ast.Token,
      gopher-lua's former token type, is unchanged). Nodes hold no token
      indices: Tokens.Range and Chunk.TokenRange find an element's first
      and last token by binary search over the offsets, Tokens.At the
      token at an offset; ast.LongBracketLevel. Chunk.Tokens.
    - parse/lexer.go, parse/parser.go, parse/tokens.go: Options.Tokens
      keeps them in Chunk.Tokens (the lexer appends each token and piece
      of trivia it reads; a flag test when off, the lexer's size class
      kept); parse.Tokenize(src, name, Options) reads them without
      parsing, up to the first lexical error (an *Error with lua5.1's or
      LuaJIT's message). The trees and every dump are unchanged.
    - Tests: parse/tokens_test.go, parse/tokens_check_test.go (round trip,
      the tokens against the lexer's, the comments and the header,
      Tokenize, the token range of every element and span next to it, of
      the test and compat scripts and the luasuites archives in both
      dialects; line breaks, "\z", long brackets, headers, lexical
      errors; FuzzTokens), parse/example_tokens_test.go;
      internal/luasuites/load_test.go BenchmarkParseArchives tokens and
      tokenize: go-moonscript

  - Name resolution over the syntax tree (go-moonscript):
    - scope/doc.go, scope/scope.go, scope/resolve.go: package scope
      resolves the names of a tree of package ast, parsed or built by other
      code (it imports neither the parser nor the compiler). scope.Resolve
      (chunk, dialect) and ResolveStmts return an Info: every declared
      variable (Var: kind - local, local function, parameter, for variable,
      the implicit self of a method, the implicit arg of a Lua51 vararg
      function, label -, declaring node, name span, scope, reads and writes,
      captured or not, the variable it shadows); every occurrence of a name
      (Ref: the IdentExpr or GotoStmt node, the Var or global it refers to,
      read, write or jump), found by node (RefOf) or offset (RefAt, VarAt,
      ScopeAt); the scope tree with spans and each function's upvalues in the
      compiler's order; the globals read and written; the shadowing; the
      problems of the labels of a built tree. The rules are Lua's (local x =
      x, local function, repeat-until, for scopes, self, arg) and, under
      LuaJIT, lj_parse.c's for goto and labels.
    - Tests: scope/resolve_test.go (each rule in both dialects, built trees,
      label errors), scope/oracle_test.go (the compiler as oracle: per
      function the upvalue names and order, GETGLOBAL, SETGLOBAL, GETUPVAL,
      SETUPVAL operands and debug locals, over testdata and the luasuites
      archives in both dialects), scope/fuzz_test.go (invariants,
      FuzzResolve), scope/example_test.go (unused locals, global writes,
      shadowing), scope/bench_test.go (BenchmarkResolveArchives): go-moonscript

  - Printing and formatting (go-moonscript):
    - format/doc.go, format/comments.go: package format; NewCommentMap
      attaches every comment of a chunk parsed with its tokens to an
      element (CommentMap, as go/ast's): Trailing to the outermost element
      ending before it on its line (before the "," or ";" after it),
      Leading to the outermost element starting after it, else Inner to the
      innermost element holding it, with the number of its children before
      it and the kind of the next token (empty blocks, tables and lists, the
      end of a block, before a keyword); a Comment keeps its text as
      written, SameLine, BlankBefore and NewlineAfter.
    - format/format.go, format/printer.go, format/layout.go,
      format/literal.go: Source(src, Options) formats a source, Chunk(w,
      chunk, CommentMap, Options) prints a chunk keeping, for the elements
      with a span in its source, the author's literals, key and call forms,
      parentheses, "else if", empty lines, one-line blocks and broken
      tables; Node(w, node, Options) prints a built or transformed tree in
      the canonical style. What is printed parses back to a tree that
      compiles to the same code: parentheses for precedence, prefix
      expressions and AdjustRet at the end of a list, a.b only for names of
      the dialect, strings and numerals that read back exactly (1e999, a
      unary minus, 0 / 0), no line break before call arguments, a ";"
      before a statement starting with "(", spaces between tokens that
      would merge. Layout: a Wadler/Oppen document laid out within a width,
      Prettier's variant, with calls hugging a last (or first) function or
      table argument. Options: Dialect, Width, IndentWidth, UseTabs, Quotes,
      TrailingComma, KeepSemicolons.
    - Tests: format/check_test.go (over testdata, go-moonscript's testdata
      and the luasuites archives in both dialects: the formatted source and
      the printed tree compile to the same code as the source, but for
      lines; formatting is idempotent; comments and string and numeral
      tokens are kept), format/printer_test.go (precedence, prefix
      expressions, AdjustRet, keys, strings, numerals, the call/statement
      ambiguity, errors of built trees), format/format_test.go (layout,
      literal forms, quotes, blank lines, semicolons, tables, comments,
      every attachment rule, comments of built and transformed trees),
      format/golden_test.go (testdata/golden), format/fuzz_test.go
      (FuzzFormat, FuzzPrint), format/example_test.go,
      format/bench_test.go: go-moonscript

  - Control flow and data flow over the syntax tree (go-moonscript):
    - flow/doc.go, flow/graph.go, flow/build.go, flow/dataflow.go,
      flow/analysis.go, flow/query.go: package flow builds, from a tree of
      package ast and its resolution by package scope, the control-flow
      graph of every function (the main chunk and each FunctionExpr): basic
      blocks of nodes with spans (the statements; the conditions of if,
      elseif, while and repeat; a for's iteration; the end of an iteration
      of a loop's body), an Entry and an Exit, and the edges of if/elseif/
      else, while, repeat-until (its condition in the body's scope), numeric
      and generic for, break, return and, under LuaJIT, goto (to the label
      package scope binds it to). A literal condition is a constant, as for
      luacheck ("while true do"); "and"/"or" are not split, calls are opaque
      (Options.NoReturn names functions that don't return, none by
      default). flow.Analyze(info, opts) returns a Program: the graphs and
      the queries Graph.Unreachable (luacheck's W511, and W512 for a loop
      whose body runs at most once), Graph.Returns (returns with values,
      bare, the end of the body reachable: inconsistent returns),
      Program.Empty (W541, W542, empty loop and function bodies),
      Program.UnusedRecursive; and the data flow over the locals: every
      value assigned (Def: used, mutated, overwritten before use) and every
      use (Use: read or mutation, the values it may see, uninitialized), by
      reaching definitions with luacheck's rules for upvalues (a closure may
      run at any time after it is created, its uses and assignments taken
      in bulk at its creation) and for circular assignments.
    - Tests: flow/flow_test.go (graphs, unreachable code, returns, empty
      blocks, the data flow and its rules for closures, in both dialects;
      trees without spans, nil nodes, label problems, concurrent use),
      flow/invariants_test.go and flow/fuzz_test.go (invariants of the
      graphs and the data flow, the reaching definitions against a walk of
      the nodes; TestCorpus over testdata, the luasuites archives and random
      programs; FuzzAnalyze), flow/luacheck_test.go (luacheck 1.2 as the
      oracle of W511, W512, W541, W542, W311, W312, W313, W321 and W341 over
      testdata, the archives and random programs, per file, line, column
      and code; every difference explained by one of four approximations of
      luacheck), flow/example_test.go (a lint pass, a graph),
      flow/bench_test.go (BenchmarkGraphsArchives, BenchmarkAnalyzeArchives,
      BenchmarkQueriesArchives): go-moonscript

  - LuaJIT's Lua 5.2 compatibility syntax (go-moonscript):
    - parse/parser.go: parse.Options.Lua52Compat reads, under
      DialectLuaJIT, what LuaJIT built with LUAJIT_ENABLE_LUA52COMPAT
      reads, as its lj_parse.c does: ";" is a statement of its own (and
      goes with a label before it), "break" need not end its block, a
      call's "(" may start a line (no "ambiguous syntax"), goto is a
      keyword everywhere. Under Lua51 it changes nothing; Lua 5.2's own
      additions are still refused. Checked against such a LuaJIT build on
      the parity sources (the 1543 it reads differently from plain
      LuaJIT). For luamerge, which reads files written for later Lua
      versions too. Tests: parse/lua52compat_test.go: go-moonscript

  Performance, numbers (go-moonscript):
    - LNumber2I builds the interface value in a local, without a scratch
      LValue (a write barrier per boxed number) or reflect.SliceHeader;
      boxed numbers come from 512-float (4 KB) pages instead of 32.
    - FORLOOP boxes the index once, shared by the internal index and the
      loop variable.
    - ADD/SUB/MUL/DIV/MOD/POW have a function each, with the number case
      inline; other operands still go through opArith.
    - math functions and tonumber box results through the allocator;
      tonumber(number) returns its argument.
    - compat test vm_arith_numbers.lua (-0, nan, inf, preload limits, %, ^,
      coercion, metamethods, numeric for, error messages).

  Performance, tables and calls (go-moonscript):
    - GETTABLE/SETTABLE read and write the array or hash part inline; the
      field helpers return at once for a table without a metatable, and
      the string-key opcodes (GETGLOBAL, GETTABLEKS, SELF, SETTABLEKS) look
      the first table up inline and continue a miss from the metatable.
    - A closure with up to 4 upvalues is one allocation.
    - Call/return push and pop call frames without the callFrameStack
      interface, keeping one spare segment.
    - XMoveTo (coroutine resume/yield) copies the values in one go.
    - Open upvalues are listed from the top of the stack down, as Lua's
      openupval, so RETURN and TAILCALL stop at the first one below.
    - SETLIST copies the values into the array part it has sized.
    - callR calls a Go function (iterators, metamethods) without entering
      mainLoop.
    - A table with an array part of 1-4 slots is one allocation with it;
      when the array part is replaced, that one is cleared, or the values
      left in it would keep weak table entries (compat
      gc_weak_array_growth.lua).
    - The grown array part is copied with makeslicecopy (barrier on the
      source only) and filled with LNil by word copies (fillNil).

  Performance, dispatch (go-moonscript):
    - mainLoop handles the frequent opcodes in a switch (MOVE, LOADK,
      GETUPVAL, array GETTABLE/SETTABLE, GETGLOBAL/GETTABLEKS/SELF on
      tables, number ADD/SUB/MUL/DIV and EQ/LT/LE, TEST/TESTSET with the
      following JMP, JMP, FORLOOP), the others through jumpTable; the
      frame, code, constants, base and pc are kept in locals.
    - CALL and RETURN between Lua functions run in mainLoop, the frame
      built in place (pushSlot); with a call/return hook, at a stack
      segment's end or when a coroutine ends they take the general path.

vm.go and state.go are generated from _vm.go and _state.go: after editing
those, run `python3 ./_tools/go-inline *.go && gofmt -w vm.go state.go` in
lua/. parse/parser.go is no longer generated: the goyacc
grammar (parse/parser.go.y) is replaced by a hand-written port of Lua 5.1's
lparser.c and LuaJIT 2.1's lj_parse.c.
