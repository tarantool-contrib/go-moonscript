#!/usr/bin/env bash
# Prints the diff of the gopher-lua fork (lua/) against the original
# gopher-lua v1.1.2, over the files of lua/ or the paths given (relative to
# lua/). What the changes are and where they come from: lua/CHANGES.md.
#
# The original comes from the Go module cache (`go mod download`). Its
# import paths are rewritten to the fork's first, so the diff doesn't show
# that change.
#
# Usage: scripts/diff-upstream.sh [path...]   e.g. scripts/diff-upstream.sh vm.go parse
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
fork=$root/lua
module=github.com/yuin/gopher-lua@v1.1.2
ourpath=github.com/tarantool-contrib/go-moonscript/lua

pristine=$(cd "$root" && go mod download -json "$module" | sed -n 's/^\s*"Dir": "\(.*\)",$/\1/p')
[ -d "$pristine" ] || { echo "can't download $module" >&2; exit 1; }

cache=${XDG_CACHE_HOME:-$HOME/.cache}
mkdir -p "$cache"
work=$(mktemp -d "$cache/go-moonscript-upstream.XXXXXX")
trap 'rm -rf "$work"' EXIT

cp -r "$pristine" "$work/a"
chmod -R u+w "$work/a"
grep -rl "\"github.com/yuin/gopher-lua" "$work/a" | xargs sed -i "s#\"github.com/yuin/gopher-lua#\"$ourpath#g"

cd "$fork"
[ $# -gt 0 ] || set -- .
# the fork's files (the original's files the fork never had, such as its
# tests and CI, are left out)
find "$@" -type f ! -path ./CHANGES.md ! -path CHANGES.md | sed 's#^\./##' | LC_ALL=C sort | while read -r f; do
	diff -uNa --label "a/$f" --label "b/$f" "$work/a/$f" "$f" || [ $? -eq 1 ]
done
