#!/usr/bin/env bash
# Builds and runs the Lua benchmark harness (internal/tools/luabench):
# our VM versus PUC Lua 5.1.5 and LuaJIT 2.1 (-joff and JIT) on LuaJIT's
# benchmark suite and our MoonScript benchmarks. Needs Docker (images
# moonref:0.8.0 and luajitref:2.1; the latter is built from the former if
# missing) and git with SSH access to GitHub (the suite is fetched into
# $LUABENCH_CACHE, default ~/.cache/go-moonscript/luajit-bench).
#
#   scripts/luabench.sh [luabench flags]   e.g. -n 9 -bench 'fasta,moon/*'
#                                          -compare old.json -pprof dir
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
image=${MOONREF:-moonref:0.8.0}
jitimage=${LUAJITREF:-luajitref:2.1}
cache=${LUABENCH_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/go-moonscript/luajit-bench}

if ! docker image inspect "$jitimage" >/dev/null 2>&1; then
	printf 'FROM %s\nUSER root\nRUN apt-get update && apt-get install -y --no-install-recommends luajit && rm -rf /var/lib/apt/lists/*\n' "$image" |
		docker build -q -t "$jitimage" - >/dev/null
fi

mkdir -p "$cache"
(cd "$root" && go build -o "$cache/luabench" ./internal/tools/luabench)
cd "$root"
LUABENCH_CACHE=$cache MOONREF=$image LUAJITREF=$jitimage exec "$cache/luabench" "$@"
