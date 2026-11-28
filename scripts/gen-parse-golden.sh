#!/usr/bin/env bash
# Regenerates the parser golden files under moonscript/testdata/parse/ with the
# reference implementation (Docker image moonref:0.8.0).
#
# Layout: moonscript/testdata/parse/<group>/<name>.moon is an input; next to it the
# reference output is stored as <name>.tree (what `moonc -T` prints) or
# <name>.err (the exact parse.string error message). moonscript/parse/golden_test.go
# compares the Go parser against these files.
#
# Differential fuzzing against the same reference lives in
# internal/tools/parsefuzz (go run ./internal/tools/parsefuzz -h).
#
# Usage:
#   scripts/gen-parse-golden.sh            regenerate expected outputs only
#   scripts/gen-parse-golden.sh --import   first (re)copy the external corpus:
#       moonscript/testdata/spec/inputs        -> moonscript/testdata/parse/spec/
#       moonscript/testdata/spec/error_inputs  -> moonscript/testdata/parse/spec_errors/
#       upstream *.moon, bin/*      -> moonscript/testdata/parse/upstream/
#       ```moon blocks in docs/*.md -> moonscript/testdata/parse/docs/
#     (snippets/ and fuzz/ are maintained by hand / by the fuzzer)
#
# Environment:
#   MOON_SRC   upstream moonscript v0.8.0 checkout
#              (default ~/.cache/go-moonscript/moonscript-0.8.0,
#              fetched by scripts/moonref/build.sh)
#   MOONREF    reference image (default moonref:0.8.0)
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
golden="$root/moonscript/testdata/parse"
moon_src=${MOON_SRC:-${XDG_CACHE_HOME:-$HOME/.cache}/go-moonscript/moonscript-0.8.0}
image=${MOONREF:-moonref:0.8.0}

import_corpus() {
	[ -d "$moon_src/moonscript" ] || { echo "MOON_SRC=$moon_src is not a moonscript checkout" >&2; exit 1; }

	rm -rf "$golden/spec" "$golden/spec_errors" "$golden/upstream" "$golden/docs"
	mkdir -p "$golden/spec" "$golden/spec_errors" "$golden/upstream" "$golden/docs"

	cp "$root"/moonscript/testdata/spec/inputs/*.moon "$golden/spec/"
	cp "$root"/moonscript/testdata/spec/error_inputs/*.moon "$golden/spec_errors/"

	# every MoonScript source of the upstream repo (spec/inputs is already
	# covered above), plus the bin/ scripts (some are Lua: they make good
	# parse error cases)
	(cd "$moon_src" && find . -name '*.moon' -not -path './spec/inputs/*' \
		-not -path './spec/error_inputs/*' -print; \
		find bin -maxdepth 1 -type f -not -name '*.moon' -print) |
		sed 's|^\./||' | sort | while read -r f; do
			cp "$moon_src/$f" "$golden/upstream/$(echo "${f%.moon}" | tr '/' '_').moon"
		done

	# code blocks of the language reference
	for md in "$moon_src"/docs/*.md; do
		awk -v out="$golden/docs/$(basename "${md%.md}")" '
			/^```moon(only)?[ \t]*$/ { n++; f = sprintf("%s_%03d.moon", out, n); inblk = 1; printf "" > f; next }
			inblk && /^```/ { inblk = 0; close(f); next }
			inblk { print >> f }
		' "$md"
	done
}

if [ "${1:-}" = "--import" ]; then
	import_corpus
	shift
fi

mapfile -t inputs < <(cd "$golden" && find . -name '*.moon' | sed 's|^\./||' | sort)
[ "${#inputs[@]}" -gt 0 ] || { echo "no inputs under $golden" >&2; exit 1; }

# one container for the whole corpus, one lua process per input so a
# pathological case (the grammar backtracks exponentially on deep nesting)
# hits the timeout instead of hanging the run
for f in "${inputs[@]}"; do rm -f -- "$golden/${f%.moon}.tree" "$golden/${f%.moon}.err"; done
docker run --rm -i --user "$(id -u):$(id -g)" \
	-v "$golden":/golden -v "$root/scripts":/scripts:ro -w /golden \
	"$image" sh -c 'xargs -0 -n 1 -P 4 timeout "$1" lua5.1 /scripts/refparse.lua batch' \
	sh "${CASE_TIMEOUT:-60}" < <(printf '%s\0' "${inputs[@]}") || true

missing=0
for f in "${inputs[@]}"; do
	if [ ! -e "$golden/${f%.moon}.tree" ] && [ ! -e "$golden/${f%.moon}.err" ]; then
		echo "no reference output (timeout?): $f" >&2
		missing=$((missing + 1))
	fi
done

trees=$(find "$golden" -name '*.tree' | wc -l)
errs=$(find "$golden" -name '*.err' | wc -l)
echo "generated ${#inputs[@]} cases ($trees trees, $errs errors) in $golden"
[ "$missing" -eq 0 ] || exit 1
