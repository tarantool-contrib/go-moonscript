#!/usr/bin/env bash
# Regenerates the outcomes of the Lua test suites of internal/luasuites:
#
#   internal/luasuites/testdata/ref/{lua5.1,luajit}/<suite>.txt
#       the reference interpreters' outcomes (needs Docker): lua5.1 (PUC Lua
#       5.1.5) from the moonref:0.8.0 image, luajit (LuaJIT 2.1) from an
#       image built on top of it with Debian's luajit package; for each unit
#       a block "### <unit> exit=<status>" with the lines of its standard
#       output the test looks at
#   internal/luasuites/testdata/expected.tsv
#       the ledger of our VM's outcomes:
#       LUASUITES_UPDATE=1 go test ./internal/luasuites
#
# Usage: scripts/gen-luasuites-ref.sh [--ledger]   (--ledger: the ledger only;
# that is all a change of our VM needs, the references depend only on the
# archives and the scripts in testdata/lua).
#
# Every unit runs as the Go test runs it (suites_test.go, parse_test.go), in
# a fresh copy of its suite's directory; skip.tsv lists what neither runs.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
dir=$root/internal/luasuites/testdata
image=${MOONREF:-moonref:0.8.0}
jitimage=${LUAJITREF:-luajitref:2.1}

if [ "${1:-}" != "--ledger" ]; then
	if ! docker image inspect "$jitimage" >/dev/null 2>&1; then
		printf 'FROM %s\nUSER root\nRUN apt-get update && apt-get install -y --no-install-recommends luajit && rm -rf /var/lib/apt/lists/*\n' "$image" |
			docker build -q -t "$jitimage" - >/dev/null
	fi

	# runs in the container: $1 interpreter, $2 output directory, $3 the
	# harness profile, $4 the skip.tsv suite of the harness
	run_suites='
set -u
lua=$1 out=$2 profile=$3 hskip=$4
w=/tmp/w
mkdir -p "$w" "$out"
for a in lua5.1-tests luajit-tests tarantool vshard crud; do
	mkdir -p "$w/$a" && tar xzf "/suites/archives/$a.tar.gz" -C "$w/$a"
done
mkdir -p "$w/lua5.1-tests/lua5.1-tests/libs/P1"
skipped() { awk -F"\t" -v s="$1" -v u="$2" "\$1 == s && \$2 == u { f = 1 } END { exit !f }" /suites/skip.tsv; }
# block <unit> <filter> <dir> <LUA_PATH> <args...>: runs the interpreter in
# a copy of <dir> and prints the block of <unit>
block() {
	local unit=$1 filter=$2 src=$3 path=$4
	shift 4
	local d
	d=$(mktemp -d)
	cp -a "$src/." "$d/"
	(cd "$d" && ulimit -v 3145728 && LUA_PATH=$path timeout 300 "$lua" "$@" > "$d.out" 2> /dev/null < /dev/null)
	echo "### $unit exit=$?"
	grep -aP "$filter" "$d.out"
	rm -rf "$d" "$d.out"
}
cd "$w/lua5.1-tests/lua5.1-tests"
for f in --setup *.lua; do
	skipped lua5.1-tests "$f" && continue
	block "$f" "^(RESULT|NOTE)\t" . "?;./?.lua" /suites/lua/run51.lua "$f"
done > "$out/lua5.1-tests.txt"
cd "$w/luajit-tests/test/lua-Harness-tests"
for f in *.t; do
	skipped "$hskip" "$f" && continue
	block "$f" "^(not )?ok [0-9]|^1\.\.[0-9]|^Bail out!" . "./?.lua;;" -l "$profile" "$f"
done > "$out/harness.txt"
cd "$w/luajit-tests/test/LuaJIT-tests"
{
	block --list "^(FILE|SKIP)\t" . "./?.lua;;" /suites/lua/ljrun.lua --list
	"$lua" /suites/lua/ljrun.lua --list | awk -F"\t" "\$1 == \"FILE\" { print \$2 }" | while read -r f; do
		skipped luajit-tests "$f" && continue
		block "$f" "^(TEST|LOAD)\t" . "./?.lua;;" /suites/lua/ljrun.lua "$f"
	done
} > "$out/luajit-tests.txt"
for a in lua5.1-tests luajit-tests tarantool vshard crud; do
	cd "$w/$a"
	echo "### $a exit=0"
	find . -type f \( -name "*.lua" -o -name "*.t" \) | sed "s|^\./||" | LC_ALL=C sort | "$lua" /suites/lua/parse.lua .
done > "$out/parse.txt"
'
	rm -rf "$dir/ref" && mkdir -p "$dir/ref"
	docker run --rm --user "$(id -u):$(id -g)" -v "$dir":/suites:ro -v "$dir/ref":/ref "$image" \
		bash -c "$run_suites" run lua5.1 /ref/lua5.1 profile_lua51 harness
	docker run --rm --user "$(id -u):$(id -g)" -v "$dir":/suites:ro -v "$dir/ref":/ref "$jitimage" \
		bash -c "$run_suites" run luajit /ref/luajit profile_luajit21 harness-jit
	for f in "$dir"/ref/*/*.txt; do
		echo "ref: ${f#"$dir"/}: $(grep -c '^### ' "$f") units"
	done
fi

cd "$root"
LUASUITES_UPDATE=1 go test ./internal/luasuites -run 'TestSuites$' -count=1 -v | grep -E 'summary:|wrote|FAIL|^ok' || true
