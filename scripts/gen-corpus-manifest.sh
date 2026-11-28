#!/usr/bin/env bash
# Regenerates moonscript/testdata/corpus/manifest.tsv and moonscript/testdata/corpus/expected.tsv,
# the committed manifest and expected results of the corpus check that CI
# runs (scripts/check-corpus.sh), from a full local corpus (see
# internal/tools/corpus/main.go: fetch.sh, then corpus collect):
#
#   1. the reference outputs still missing, with upstream moonc 0.8.0 in
#      Docker (corpus ref and corpus ref -lint; the image comes from
#      scripts/moonref/build.sh);
#   2. corpus export: the repositories covering every distinct file, with
#      their pinned commits, the files' sha256 and git blob ids, and the
#      digests of the reference outputs; it also checks that the mutants
#      regenerate identically.
#
# Usage: scripts/gen-corpus-manifest.sh [--no-ref]
#   --no-ref  skip step 1 (the reference outputs are complete)
#
# Environment:
#   MOON_CORPUS  the full corpus (default ~/.cache/go-moonscript/corpus)
#   MOONREF      reference image (default moonref:0.8.0)
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
corpus=${MOON_CORPUS:-${XDG_CACHE_HOME:-$HOME/.cache}/go-moonscript/corpus}
image=${MOONREF:-moonref:0.8.0}
ref=1
for a in "$@"; do
	case $a in
	--no-ref) ref=0 ;;
	*) echo "usage: $0 [--no-ref]" >&2; exit 2 ;;
	esac
done

cache=${XDG_CACHE_HOME:-$HOME/.cache}
mkdir -p "$cache"
work=$(mktemp -d "$cache/go-moonscript-corpus.XXXXXX")
trap 'rm -rf "$work"' EXIT
cd "$root"
go build -o "$work/corpus-bin" ./internal/tools/corpus

if [ $ref = 1 ]; then
	"$work/corpus-bin" ref -corpus "$corpus" -image "$image"
	"$work/corpus-bin" ref -lint -corpus "$corpus" -image "$image"
fi
"$work/corpus-bin" export -corpus "$corpus" -pinned moonscript/testdata/corpus
