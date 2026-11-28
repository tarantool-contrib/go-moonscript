#!/usr/bin/env bash
# Regenerates the reference outputs under moonscript/testdata/upstream_spec/ref/ used by
# spec_runtime_test.go (needs Docker and the moonref:0.8.0 image):
#
#   ref/<name>.lua            reference to_lua output for each
#                             moonscript/testdata/upstream_spec/{,lib/}*.moon
#   ref/runtime_compiles.json every compile.tree call the specs make while
#                             running on the reference (scripts/refspecrun.lua
#                             runs them with the same busted stand-in; this
#                             script fails unless every test passes there too)
#
# The spec and library sources themselves are verbatim copies of upstream
# v0.8.0 files: spec/<name>_spec.moon, moon/init.moon (lib/moon.moon),
# moonscript/util.moon and moonscript/base.moon (lib/moonscript.*.moon).
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
data="$root/moonscript/testdata/upstream_spec"
image=${MOONREF:-moonref:0.8.0}

mkdir -p "$data/ref"
rm -f "$data"/ref/*.lua "$data/ref/runtime_compiles.json"

compile_lua='
local f = assert(io.open(arg[1])); local src = f:read("*a"); f:close()
local code, err = require("moonscript.base").to_lua(src)
local o = assert(io.open(arg[2], "w")); o:write((assert(code, err))); o:close()
'

docker run --rm --user "$(id -u):$(id -g)" -v "$data":/data -v "$root/scripts":/scripts:ro \
	-w /data -e COMPILE_LUA="$compile_lua" "$image" sh -c '
set -e
printf "%s" "$COMPILE_LUA" > /tmp/compile.lua
for f in *.moon lib/*.moon; do
	lua5.1 /tmp/compile.lua "$f" "ref/$(basename "$f" .moon).lua"
done
lua5.1 /scripts/refspecrun.lua /data ref/runtime_compiles.json *_spec.moon > /tmp/results.txt
cat /tmp/results.txt
' > "$root/.ref-results.txt"

failing=$(awk -F'\t' '$3 != "true"' "$root/.ref-results.txt")
total=$(wc -l < "$root/.ref-results.txt")
rm -f "$root/.ref-results.txt"
if [ -n "$failing" ]; then
	echo "specs failing on the reference:" >&2
	echo "$failing" >&2
	exit 1
fi
echo "$total tests pass on the reference"
