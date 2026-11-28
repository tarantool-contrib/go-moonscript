#!/usr/bin/env bash
# Regenerates the expected outputs in moonscript/testdata/lint/ (used by
# cmd/moonc/lint_test.go, `go test -run Lint ./cmd/moonc`) with the reference
# moonc in Docker.
#
# moonscript/testdata/lint/COMMANDS.tsv lists the moonc invocations, one per line:
#   NAME <TAB> DIR <TAB> ARGS
# ARGS are split on spaces; an argument "<FILE" feeds FILE to stdin. Each one
# is run in moonscript/testdata/lint/DIR and recorded as moonscript/testdata/lint/NAME.out: the
# line "exit N", then moonc's stderr verbatim (moonc -l writes nothing to
# stdout; anything it does write is kept as NAME.stdout).
#
# Environment:
#   MOONREF    reference image (default moonref:0.8.0)
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
dir="$root/moonscript/testdata/lint"
image=${MOONREF:-moonref:0.8.0}

rm -f "$dir"/*.out "$dir"/*.stdout
docker run --rm -i --network none --user "$(id -u):$(id -g)" -v "$dir":/lint "$image" sh -s <<'SH'
set -u
tab=$(printf '\t')
while IFS="$tab" read -r name sub args; do
  [ -n "$name" ] || continue
  (
    cd "/lint/$sub"
    input=/dev/null
    set --
    for a in $args; do
      case $a in
        "<"*) input=${a#<} ;;
        *) set -- "$@" "$a" ;;
      esac
    done
    moonc "$@" < "$input" > /tmp/stdout 2> /tmp/stderr
    code=$?
    { echo "exit $code"; cat /tmp/stderr; } > "/lint/$name.out"
    if [ -s /tmp/stdout ]; then cp /tmp/stdout "/lint/$name.stdout"; fi
  )
done < /lint/COMMANDS.tsv
SH
echo "wrote $(ls "$dir"/*.out | wc -l) outputs to $dir"
