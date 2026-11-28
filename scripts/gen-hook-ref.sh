#!/usr/bin/env bash
# Regenerates the reference outputs of the debug hook tests (needs Docker
# and the moonref:0.8.0 image, which has PUC Lua 5.1.5 as lua5.1):
#
#   moonscript/runtime/testdata/hooks/ref/<name>.out
#       standard output of `lua5.1 <name>.lua` for every script in
#       moonscript/runtime/testdata/hooks, run in that directory (TestHooks)
#
# The scripts must run without error. The tests themselves run without
# Docker.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
image=${MOONREF:-moonref:0.8.0}

run='
set -eu
cd /data
rm -rf ref && mkdir ref
for f in *.lua; do
	lua5.1 "$f" > "ref/${f%.lua}.out"
done
'

docker run --rm --user "$(id -u):$(id -g)" -v "$root/moonscript/runtime/testdata/hooks":/data "$image" bash -c "$run"
echo "hooks: $(ls "$root"/moonscript/runtime/testdata/hooks/ref/*.out | wc -l) scripts"
