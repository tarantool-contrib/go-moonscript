#!/usr/bin/env bash
# The real-world corpus check without a local corpus, as CI runs it: fetches
# the files listed in testdata/corpus/manifest.tsv from their GitHub
# repositories (only the ones not there yet), regenerates their mutants and
# compares the parser, the compiler and the linter with the digests of the
# reference outputs in testdata/corpus/expected.tsv. It fails on any
# difference; repositories that are gone are reported and skipped (the
# fetch fails only when less than 90% of the files are there).
#
# Usage: scripts/check-corpus.sh [STEP...]
#   steps: fetch prepare run lint (default: all four, in this order)
#
# Environment:
#   MOON_CORPUS  directory of the fetched corpus (default
#                ~/.cache/go-moonscript/corpus-pinned); not a full corpus
#   GIT_URL      repository URL, %s is owner/name (default
#                https://github.com/%s.git; git@github.com:%s.git for SSH)
#   FETCH_JOBS   parallel repository fetches (default 16)
#   JOBS         parallel check workers (default: CPUs - 1)
#   PINNED       directory of manifest.tsv and expected.tsv (default
#                testdata/corpus; e.g. a subset of the manifest)
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
corpus=${MOON_CORPUS:-${XDG_CACHE_HOME:-$HOME/.cache}/go-moonscript/corpus-pinned}
steps=("$@")
[ ${#steps[@]} -gt 0 ] || steps=(fetch prepare run lint)

mkdir -p "$corpus"
cd "$root"
go build -o "$corpus/corpus-bin" ./internal/tools/corpus
common=(-corpus "$corpus" -pinned "${PINNED:-$root/testdata/corpus}")
jobs=()
[ -z "${JOBS:-}" ] || jobs=(-j "$JOBS")

# summary STEP: the output's summary lines, for the GitHub job summary
summary() {
	[ -n "${GITHUB_STEP_SUMMARY:-}" ] || { cat >/dev/null; return; }
	{
		echo "### corpus $1"
		echo '```'
		grep -E '^(fetch|prepare|lint):|^  (skipped|[a-z-]+ +[0-9]+$)|^== segment|^(PARSE|COMPILE|FATAL|FULL)|^corpus:' |
			grep -vE '^fetch: [0-9]+/[0-9]+ repositories' || true
		echo '```'
	} >>"$GITHUB_STEP_SUMMARY"
}

for step in "${steps[@]}"; do
	log="$corpus/$step.log"
	case $step in
	fetch) cmd=(fetch -git-url "${GIT_URL:-https://github.com/%s.git}" -fetch-j "${FETCH_JOBS:-16}") ;;
	prepare) cmd=(prepare) ;;
	run) cmd=(run -strict "${jobs[@]}") ;;
	lint) cmd=(-lint -strict "${jobs[@]}") ;;
	*) echo "$0: unknown step $step (fetch, prepare, run, lint)" >&2; exit 2 ;;
	esac
	status=0
	"$corpus/corpus-bin" "${cmd[@]}" "${common[@]}" 2>&1 | tee "$log" || status=$?
	summary "$step" <"$log"
	[ $status = 0 ] || exit $status
done
