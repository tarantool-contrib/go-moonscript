#!/usr/bin/env bash
# Regenerates the reference outputs of gopher-lua's Lua compatibility tests
# (needs Docker):
#
#   lua/testdata/compat/ref/<name>.out
#       standard output of `lua5.1 <name>.lua` (PUC Lua 5.1.5, from the
#       moonref:0.8.0 image) for every script in testdata/compat, and of
#       `luajit <name>.jit.lua` (LuaJIT 2.1, from an image built on top of
#       moonref:0.8.0 with Debian's luajit package) for the scripts named
#       <name>.jit.lua; run in that directory (TestCompat)
#
#   lua/testdata/dialect/ref/<name>.out
#       the same for the scripts of the dialect tests (TestDialectScripts):
#       `luajit <name>.luajit.lua`, `lua5.1 <name>.lua5.1.lua`
#
# The scripts must run without error. The tests themselves run without
# Docker.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
image=${MOONREF:-moonref:0.8.0}
jitimage=${LUAJITREF:-luajitref:2.1}
dir=$root/lua/testdata/compat

if ! docker image inspect "$jitimage" >/dev/null 2>&1; then
	printf 'FROM %s\nUSER root\nRUN apt-get update && apt-get install -y --no-install-recommends luajit && rm -rf /var/lib/apt/lists/*\n' "$image" |
		docker build -q -t "$jitimage" - >/dev/null
fi

rm -rf "$dir/ref" && mkdir "$dir/ref"
run() { # <image> <interpreter> <pattern>
	docker run --rm --user "$(id -u):$(id -g)" -v "$dir":/data "$1" bash -c '
set -eu
cd /data
for f in '"$3"'; do
	[ -e "$f" ] || continue
	'"$2"' "$f" > "ref/${f%.lua}.out"
done
'
}
run "$image" lua5.1 '$(ls *.lua | grep -v "\.jit\.lua$")'
run "$jitimage" luajit '*.jit.lua'
echo "compat: $(ls "$dir"/ref/*.out | wc -l) scripts"

ddir=$root/lua/testdata/dialect
rm -rf "$ddir/ref" && mkdir "$ddir/ref"
for v in "$image lua5.1 lua5.1" "$jitimage luajit luajit"; do
	set -- $v
	docker run --rm --user "$(id -u):$(id -g)" -v "$ddir":/data "$1" bash -c '
set -eu
cd /data
for f in *.'"$3"'.lua; do
	[ -e "$f" ] || continue
	'"$2"' "$f" > "ref/${f%.lua}.out"
done
'
done
echo "dialect: $(ls "$ddir"/ref/*.out | wc -l) scripts"
