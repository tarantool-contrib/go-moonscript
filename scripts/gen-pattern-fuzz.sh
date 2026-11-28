#!/usr/bin/env bash
# Regenerates the pattern differential test (needs Docker):
#
#   lua/testdata/compat/lib_pattern_fuzz.lua
#       random patterns and subjects with a fixed seed, written by
#       scripts/pattern-fuzz-gen.lua run on lua5.1 (moonref:0.8.0)
#   lua/testdata/compat/ref/lib_pattern_fuzz.out
#       its output on lua5.1, which TestCompat compares gopher-lua's with
#
# usage: gen-pattern-fuzz.sh [ncases]   (default 3000; half literal patterns;
#        then ncases/10 cases ending inside an unclosed capture)
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
image=${MOONREF:-moonref:0.8.0}
dir=$root/lua/testdata/compat
n=${1:-3000}

docker run --rm --user "$(id -u):$(id -g)" -v "$root/scripts":/gen:ro -v "$dir":/data "$image" bash -c '
set -eu
cd /data
lua5.1 /gen/pattern-fuzz-gen.lua '"$n"' > lib_pattern_fuzz.lua
lua5.1 lib_pattern_fuzz.lua > ref/lib_pattern_fuzz.out
'
echo "lib_pattern_fuzz: $n cases, $(wc -c < "$dir/ref/lib_pattern_fuzz.out") bytes of reference output"
