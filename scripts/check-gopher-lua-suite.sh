#!/usr/bin/env bash
# Runs the test suites of pristine gopher-lua v1.1.2 (its Go tests,
# _glua-tests and _lua5.1-tests, which are not vendored) against
# third_party/gopher-lua, to catch regressions of the patched copy.
#
# The copy deliberately behaves like PUC Lua 5.1 where gopher-lua didn't,
# and some pristine assertions check the old behaviour. Those are corrected
# to Lua 5.1's results below (Lua scripts) or skipped (Go tests); every
# entry says why. Anything else that fails is a regression.
#
# Usage: scripts/check-gopher-lua-suite.sh
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
module=github.com/yuin/gopher-lua@v1.1.2
ourpath=github.com/tarantool-contrib/go-moonscript/third_party/gopher-lua

# Go tests of pristine behaviour that isn't Lua 5.1's
skip_tests=(
	TestOptString   # luaL_optstring accepts numbers
	TestTableLen    # '#' with holes: Lua 5.1 gives 0 for {[9]=..,[7]=..}
	TestTableAppend # '#' after t[1]=t[2]=nil: Lua 5.1's border search
	TestTableInsert # table.insert(t, 5, v) on {T,T,T}: t[5] goes to the hash part
	TestSkipOpenLibs # Lua 5.1's message: "attempt to call global 'print' (a nil value)"
	TestTableLenType # '#' of a table ignores __len in Lua 5.1 (and LuaJIT)
)

pristine=$(cd "$root" && go mod download -json "$module" | sed -n 's/^\s*"Dir": "\(.*\)",$/\1/p')
[ -d "$pristine" ] || { echo "can't download $module" >&2; exit 1; }

cache=${XDG_CACHE_HOME:-$HOME/.cache}
mkdir -p "$cache"
work=$(mktemp -d "$cache/go-moonscript-glsuite.XXXXXX")
trap 'rm -rf "$work"' EXIT

cp -a "$root/third_party/gopher-lua/." "$work/"
rm -f "$work/moonscript.patch"
chmod -R u+w "$work"
grep -rl "$ourpath" "$work" --include='*.go' --include='*.y' | xargs sed -i "s#$ourpath#github.com/yuin/gopher-lua#g"
cp "$pristine"/*_test.go "$pristine/go.mod" "$pristine/go.sum" "$work/"
cp -r "$pristine/_glua-tests" "$pristine/_lua5.1-tests" "$work/"
chmod -R u+w "$work"

# the table engine has no `dict` field: read the hash part through the API
sed -i -e 's/_, found := tbl.dict\[LString("key")\]/found := tbl.RawGetH(LString("key")) != LNil/' \
	-e 's/_, foundb := tbl.dict\[LTrue\]/foundb := tbl.RawGetH(LTrue) != LNil/' "$work/table_test.go"
# Lua 5.1: "%c" in a gsub replacement gives "c"
sed -i '7s/"%1 %1 %c") == "hello hello %c world world %c"/"%1 %1 %c") == "hello hello c world world c"/' \
	"$work/_glua-tests/strings.lua"
# Lua 5.1's coroutine error messages
sed -i -e 's/can not resume a running thread/cannot resume running coroutine/' \
	-e 's/can not resume a dead thread/cannot resume dead coroutine/' "$work/_glua-tests/coroutine.lua"
# Lua 5.1's message for a function with 302 parameters (LUAI_MAXVARS)
sed -i '129s/"register overflow"/"has more than 200 local variables"/' "$work/_glua-tests/vm.lua"
# Lua 5.1: a ';' must follow a statement ("do;" is "unexpected symbol near ';'")
sed -i '89s/do; table.insert/do table.insert/' "$work/_lua5.1-tests/pm.lua"
# Lua 5.1 has no goto (a name there); the scripts run with DialectLua51
sed -i '/^\t"goto.lua",$/d' "$work/script_test.go"

# Lua 5.1's runtime error messages, and '#' of a table ignores __len
sed -i -e 's/"attempt to call a non-function object"/"attempt to call field '"'"'notfound'"'"' (a nil value)"/' \
	-e 's/"bad argument #1 to pcall"/"bad argument #1 to '"'"'pcall'"'"'"/' "$work/_glua-tests/issues.lua"
sed -i -e 's/"__unm undefined"/"attempt to perform arithmetic on upvalue '"'"'v'"'"'"/' \
	-e '13s/assert(#tbl == 10)/assert(#tbl == 3)/' \
	-e "s/\"for statement init must be a number\"/\"'for' initial value must be a number\"/" \
	-e "s/\"for statement limit must be a number\"/\"'for' limit must be a number\"/" \
	-e "s/\"for statement step must be a number\"/\"'for' step must be a number\"/" \
	-e 's/"cannot perform add operation between table and function"/"attempt to perform arithmetic on a table value"/' \
	-e 's/"cannot perform concat operation between table and function"/"attempt to concatenate a table value"/' \
	"$work/_glua-tests/vm.lua"

# Lua 5.1: '#' of a table ignores __len
sed -i '13s/^assert(#tbl == 10)$/assert(#tbl == 3)/' "$work/_glua-tests/vm.lua"
# string.dump is there: string.dump() is an argument error
sed -i '5s/"GopherLua does not support the string.dump"/"function expected"/' "$work/_glua-tests/strings.lua"
# Lua 5.1: debug.getinfo raises an error for an invalid option
sed -i 's/^assert(debug.getinfo(1, "a") == nil)$/assert(not pcall(debug.getinfo, 1, "a"))/' "$work/_glua-tests/db.lua"
# Lua 5.1: the errno of reading a directory is EISDIR, not always 1
sed -i '/-- issue #455/,/^test()/s/assert(ecode == 1)/assert(ecode == 21)/' "$work/_glua-tests/issues.lua"
# Lua 5.1: os.execute returns system()'s wait status, 256 for exit status 1
sed -i 's/assert(os.execute("date -a") == 1)/assert(os.execute("date -a") == 256)/' "$work/_glua-tests/os.lua"

# tonumber(s, base) after a '-': PUC Lua 5.1 on 64-bit gives strtoul's
# negation modulo 2^64 (the suite assumes a negated value, as LuaJIT 2.1)
sed -i -e "47s/== -36)/== 2^64 - 36)/" -e "48s/== -(10+(16\*(15+(16\*(15+(16\*15))))))/== 2^64 - 0xfffa/" \
	-e "52s/== -2^40)/== 2^64 - 2^40)/" \
	"$work/_lua5.1-tests/math.lua"

skip=$(IFS='|'; echo "^(${skip_tests[*]})\$")
cd "$work"
GOFLAGS=-mod=mod go test -count=1 -skip "$skip" ./...
