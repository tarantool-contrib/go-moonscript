#!/usr/bin/env bash
# The commands of the CI jobs (.github/workflows/ci.yml), for the workflow
# and for a local run of the same checks.
#
# Usage: scripts/ci/run.sh JOB [ARG...]
#   vet [PKG...]          go vet (but the fork's VM, upstream's code with 4
#                         known findings), gofmt -l, go mod tidy -diff
#   test [PKG...]         go test (default ./...)
#   gopher-lua            pristine gopher-lua's suites against lua/
#   coverage              go test -race with coverage of the module but the
#                         fork's upstream packages and internal/tools
#   testrace TREE [N]     go test -race -count=N (default 100) ./TREE/...
#   generate              go generate ./..., and the tree is unchanged
#   govulncheck           the module against the Go vulnerability database
#   lint TREE             golangci-lint with the config of TREE: root,
#                         luascope or luamerge (CI: golangci-lint-action)
#   codespell             codespell ./luascope ./luamerge (.codespellrc); in
#                         Docker if codespell is not installed
#   fuzz PKG TARGET [T]   go test -fuzz TARGET -fuzztime T (default 90s)
#   all                   everything above, once, timed (a local run of the
#                         full checks; lint needs golangci-lint on PATH)
#
# The versions of the tools, as in ci.yml's env (keep them in sync):
#   GOLANGCI_LINT_VERSION, GOVULNCHECK_VERSION, CODESPELL_VERSION
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"

: "${GOLANGCI_LINT_VERSION:=v2.15.0}"
: "${GOVULNCHECK_VERSION:=v1.8.0}"
: "${CODESPELL_VERSION:=2.4.1}"

# summary TEXT: appended to the job summary in CI
summary() {
	[ -z "${GITHUB_STEP_SUMMARY:-}" ] || printf '%s\n' "$@" >>"$GITHUB_STEP_SUMMARY"
}

job_vet() {
	[ $# -gt 0 ] || set -- ./...
	local pkgs
	# the package lua is upstream gopher-lua's VM: 4 known findings
	pkgs=$(go list "$@" | grep -v '/lua$' || true)
	# shellcheck disable=SC2086 # a list of import paths
	[ -z "$pkgs" ] || go vet $pkgs
	local unformatted
	unformatted=$(gofmt -l .)
	if [ -n "$unformatted" ]; then
		echo "::error::not gofmt-ed: $unformatted"
		return 1
	fi
	go mod tidy -diff
}

job_test() {
	[ $# -gt 0 ] || set -- ./...
	go test "$@"
}

job_gopher_lua() {
	scripts/check-gopher-lua-suite.sh
}

job_coverage() {
	local pkgs
	# not the fork's upstream code (the VM, ast, parse, pm), nor the tools
	pkgs=$(go list ./... | grep -v -e '/lua$' -e '/lua/ast$' -e '/lua/parse$' -e '/lua/pm$' -e /internal/tools/ | paste -sd, -)
	go test -race -coverprofile=coverage.out -coverpkg="$pkgs" ./...
	local total
	total=$(go tool cover -func=coverage.out | tail -1)
	echo "$total"
	summary '### Coverage' '' '```' "$total" '```'
}

job_testrace() {
	local tree=${1:?testrace TREE [COUNT]} count=${2:-100}
	# -count shakes out tests that depend on the order of execution
	go test -race -count="$count" -timeout=40m "./$tree/..."
}

# the state of the work tree: the changes of the tracked files and the
# untracked files (a local run may start from a modified tree)
tree_state() {
	git status --porcelain=v1 --untracked-files=all
	git diff HEAD | sha256sum
}

job_generate() {
	local before after
	before=$(tree_state)
	go generate ./...
	after=$(tree_state)
	if [ "$before" != "$after" ]; then
		echo "::error::generated files are out of date: run go generate ./... and commit the result"
		git status --short
		git diff | head -200
		return 1
	fi
}

job_govulncheck() {
	go run "golang.org/x/vuln/cmd/govulncheck@$GOVULNCHECK_VERSION" ./...
}

job_lint() {
	local tree=${1:?lint TREE}
	local dir=$tree
	[ "$tree" != root ] || dir=.
	local have
	have=$(golangci-lint version --short 2>/dev/null || true)
	[ "v${have#v}" = "$GOLANGCI_LINT_VERSION" ] ||
		echo "warning: golangci-lint ${have:-is not installed}, CI runs $GOLANGCI_LINT_VERSION" >&2
	(cd "$dir" && golangci-lint run)
}

job_codespell() {
	if command -v codespell >/dev/null; then
		codespell ./luascope ./luamerge
	else
		docker run --rm -v "$root:/src:ro" -w /src python:3-slim \
			sh -c "pip install -q --disable-pip-version-check --root-user-action=ignore codespell==$CODESPELL_VERSION && codespell ./luascope ./luamerge"
	fi
}

job_fuzz() {
	local pkg=${1:?fuzz PKG TARGET [TIME]} target=${2:?fuzz PKG TARGET [TIME]} time=${3:-90s}
	go test -run='^$' -fuzz="^$target\$" -fuzztime="$time" "$pkg"
}

job_all() {
	local jobs=(
		"vet" "test" "gopher-lua" "coverage" "testrace luascope 30" "testrace luamerge 100"
		"generate" "govulncheck" "lint root" "lint luascope" "lint luamerge" "codespell"
	)
	local failed=() times=() j start status
	for j in "${jobs[@]}"; do
		echo "=== $j"
		start=$SECONDS
		status=0
		# shellcheck disable=SC2086 # the job and its arguments
		"$0" $j || status=$?
		times+=("$(printf '%-18s %5ds  %s' "$j" $((SECONDS - start)) "$([ $status = 0 ] && echo ok || echo FAIL)")")
		[ $status = 0 ] || failed+=("$j")
	done
	echo
	printf '%s\n' "${times[@]}"
	if [ ${#failed[@]} -gt 0 ]; then
		echo "failed: ${failed[*]}"
		return 1
	fi
}

job=${1:?usage: scripts/ci/run.sh JOB [ARG...] (-h: the jobs)}
shift
case $job in
-h | --help | help)
	sed -n '2,/^set -e/{/^set -e/d;s/^# \{0,1\}//;p}' "$0"
	exit 0
	;;
esac
declare -F "job_${job//-/_}" >/dev/null || { echo "$0: unknown job $job" >&2; exit 2; }
"job_${job//-/_}" "$@"
