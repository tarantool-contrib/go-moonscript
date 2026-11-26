#!/usr/bin/env bash
# Discovers MoonScript repositories on GitHub and clones them for the corpus
# (see main.go). Repositories are untrusted data: they are cloned shallow,
# blobless and sparse (only *.moon files are checked out), without
# submodules, hooks, LFS smudging or symlinks, and nothing in them is run.
#
# Usage:
#   internal/tools/corpus/fetch.sh            discover (if needed) + clone
#   internal/tools/corpus/fetch.sh --moonlibs    only the moonlibs-lua segment:
#       *.lua files of the github.com/moonlibs org (Tarantool Lua, no
#       MoonScript) into $CORPUS/repos-moonlibs/, fed to both compilers as
#       parse-error parity cases; the vendored moonlibs/tarantool and
#       moonlibs/luarocks are skipped
#   internal/tools/corpus/fetch.sh --rediscover  redo the GitHub searches
#   internal/tools/corpus/fetch.sh --codesearch  also run `gh api search/code`
#                                                queries (slow: rate limited)
#
# Environment: MOON_CORPUS (default ~/.cache/go-moonscript/corpus, as for
# main.go), JOBS (default 12).
# Output: $CORPUS/repos/OWNER__NAME (with .git/DONE holding the commit),
#         $CORPUS/meta/repos.txt (the clone list), $CORPUS/meta/clone.log.
# Extra repositories can be listed one per line in $CORPUS/meta/extra.txt.
set -euo pipefail

CORPUS=${MOON_CORPUS:-${XDG_CACHE_HOME:-$HOME/.cache}/go-moonscript/corpus}
JOBS=${JOBS:-12}
export CORPUS
meta=$CORPUS/meta
mkdir -p "$meta" "$CORPUS/repos"

# clone_one REPO [REPOS_SUBDIR [SPARSE_PATTERN]]
clone_one() {
	local repo=$1 sub=${2:-repos} pattern=${3:-'*.moon'}
	local dir="$CORPUS/$sub/${repo/\//__}"
	[ -e "$dir/.git/DONE" ] && return 0
	rm -rf -- "$dir"
	local err
	err=$(mktemp "$meta/clone-err.XXXXXX")
	export GIT_TERMINAL_PROMPT=0 GIT_LFS_SKIP_SMUDGE=1 GIT_CONFIG_NOSYSTEM=1
	local g=(git -c core.hooksPath=/dev/null -c core.symlinks=false -c protocol.file.allow=never)
	if timeout 300 "${g[@]}" clone -q --depth 1 --filter=blob:none --no-checkout \
		--no-recurse-submodules "git@github.com:$repo.git" "$dir" 2>"$err"; then
		if (cd "$dir" && "${g[@]}" sparse-checkout set --no-cone "$pattern" &&
			timeout 300 "${g[@]}" checkout -q) 2>"$err"; then
			git -C "$dir" rev-parse HEAD >"$dir/.git/DONE"
			echo "OK $repo"
		else
			echo "CHECKOUT-FAIL $repo $(head -1 "$err")"
		fi
	else
		echo "CLONE-FAIL $repo $(head -1 "$err")"
		rm -rf -- "$dir"
	fi
	rm -f -- "$err"
}

if [ "${1:-}" = "--clone-one" ]; then # internal: used through xargs below
	clone_one "$2" "${3:-}" "${4:-}"
	exit 0
fi

rediscover=0 codesearch=0 moonlibs=0
for a in "$@"; do
	case $a in
	--rediscover) rediscover=1 ;;
	--codesearch) codesearch=1 ;;
	--moonlibs) moonlibs=1 ;;
	*) echo "unknown argument $a" >&2; exit 2 ;;
	esac
done


if [ $moonlibs = 1 ]; then
	mkdir -p "$CORPUS/repos-moonlibs"
	gh api 'orgs/moonlibs/repos?per_page=100' --paginate --jq '.[].full_name' |
		grep -vx -e moonlibs/tarantool -e moonlibs/luarocks | sort >"$meta/repos_moonlibs.txt"
	echo "cloning $(wc -l <"$meta/repos_moonlibs.txt") moonlibs repositories" >&2
	xargs -P "$JOBS" -I{} "$0" --clone-one {} repos-moonlibs '*.lua' <"$meta/repos_moonlibs.txt" |
		tee "$meta/clone_moonlibs.log" | grep -v '^OK' || true
	exit 0
fi

if [ $rediscover = 1 ] || [ ! -s "$meta/repos_lang.jsonl" ]; then
	: >"$meta/repos_lang.jsonl"
	# the search API returns at most 1000 results per query; language:MoonScript
	# (forks excluded) is below that
	for p in $(seq 1 10); do
		gh api "search/repositories?q=language:MoonScript&sort=stars&order=desc&per_page=100&page=$p" \
			--jq '.items[] | {full_name, stars: .stargazers_count, size, default_branch, pushed_at} | tostring' \
			>>"$meta/repos_lang.jsonl" || break
		sleep 2
	done
	gh api 'users/leafo/repos?per_page=100' --paginate --jq '.[] | select(.fork==false) | .full_name' >"$meta/leafo_all.txt"
	gh api 'orgs/itchio/repos?per_page=100' --paginate --jq '.[] | select(.fork==false) | .full_name' >"$meta/itchio_all.txt"
	gh api 'orgs/luarocks/repos?per_page=100' --paginate --jq '.[] | .full_name' >"$meta/luarocks_org.txt"
	# forks (about 1000, split by creation date to stay under the cap);
	# identical files are deduplicated by collect
	: >"$meta/repos_forks.txt"
	for range in "created:<2016-01-01" "created:2016-01-01..2019-12-31" "created:>2019-12-31"; do
		for p in $(seq 1 10); do
			out=$(gh api -X GET search/repositories -f q="language:MoonScript fork:only $range" \
				-f per_page=100 -f page="$p" --jq '.items[].full_name') || break
			[ -z "$out" ] && break
			echo "$out" >>"$meta/repos_forks.txt"
			sleep 2
		done
	done
fi

if [ $codesearch = 1 ]; then
	# code search finds repositories with .moon files that are not tagged
	# as MoonScript; each query is capped at 1000 hits, so vary the terms
	: >"$meta/repos_code.txt"
	for q in "extension:moon" "extension:moon class" "extension:moon import" \
		"extension:moon require" "extension:moon export" "extension:moon lapis" \
		"extension:moon love" "extension:moon =>" "extension:moon with" \
		"extension:moon local" "extension:moon return" "extension:moon for" \
		"extension:moon if" "extension:moon print" "extension:moon self" \
		"extension:moon table" "extension:moon string" "extension:moon game" \
		"extension:moon test" "extension:moon describe"; do
		for p in $(seq 1 10); do
			out=$(gh api -X GET search/code -f q="$q" -f per_page=100 -f page="$p" \
				--jq '.items[].repository.full_name' 2>/dev/null) || break
			[ -z "$out" ] && break
			echo "$out" >>"$meta/repos_code.txt"
			sleep 7
		done
	done
fi

# leafo/moonscript itself (and its forks) is covered by testdata/parse/upstream
{
	sed -n 's/.*"full_name":"\([^"]*\)".*/\1/p' "$meta/repos_lang.jsonl"
	cat "$meta"/leafo_all.txt "$meta"/itchio_all.txt "$meta"/luarocks_org.txt \
		"$meta"/repos_code.txt "$meta"/extra.txt 2>/dev/null || true
	grep -v '/moonscript$' "$meta"/repos_forks.txt 2>/dev/null || true
} | grep -E '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$' | grep -vx 'leafo/moonscript' | sort -u >"$meta/repos.txt"

echo "cloning $(wc -l <"$meta/repos.txt") repositories into $CORPUS/repos" >&2
xargs -P "$JOBS" -n 1 "$0" --clone-one <"$meta/repos.txt" | tee "$meta/clone.log" | grep -v '^OK' || true
echo "done: $(grep -c '^OK' "$meta/clone.log") ok, $(grep -vc '^OK' "$meta/clone.log") failed" >&2
