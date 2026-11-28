#!/usr/bin/env bash
# Regenerates the diff part of lua/moonscript.patch: the
# changes of the vendored copy against pristine gopher-lua v1.1.2, over the
# files vendored here. The header (everything before the first "--- a/"
# line: what changed, where it comes from, how to regenerate the generated
# files) is kept as it is; edit it by hand.
#
# The pristine module comes from the Go module cache (`go mod download`).
# Its import paths are rewritten to the vendored package's first, so the
# diff doesn't show that change. The result is checked: applying the diff to
# the pristine copy must reproduce lua exactly.
#
# Usage: scripts/gen-gopher-lua-patch.sh [--check]
#   --check  fail if moonscript.patch is not up to date (CI)
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
vendored=$root/lua
patch_file=$vendored/moonscript.patch
module=github.com/yuin/gopher-lua@v1.1.2
ourpath=github.com/tarantool-contrib/go-moonscript/lua

pristine=$(cd "$root" && go mod download -json "$module" | sed -n 's/^\s*"Dir": "\(.*\)",$/\1/p')
[ -d "$pristine" ] || { echo "can't download $module" >&2; exit 1; }

work=$(mktemp -d "${XDG_CACHE_HOME:-$HOME/.cache}/go-moonscript-patch.XXXXXX" 2>/dev/null ||
	{ mkdir -p "${XDG_CACHE_HOME:-$HOME/.cache}" && mktemp -d "${XDG_CACHE_HOME:-$HOME/.cache}/go-moonscript-patch.XXXXXX"; })
trap 'rm -rf "$work"' EXIT

cp -r "$pristine" "$work/a"
chmod -R u+w "$work/a"
grep -rl "\"github.com/yuin/gopher-lua" "$work/a" | xargs sed -i "s#\"github.com/yuin/gopher-lua#\"$ourpath#g"

sed -n '/^--- a\//q;p' "$patch_file" > "$work/header"
{
	cat "$work/header"
	(cd "$vendored" && find . -type f ! -path ./moonscript.patch | sed 's#^\./##' | LC_ALL=C sort |
		while read -r f; do
			diff -uNa --label "a/$f" --label "b/$f" "$work/a/$f" "$f" || [ $? -eq 1 ]
		done)
} > "$work/new.patch"

# applying the diff to the pristine copy reproduces the vendored tree
cp -r "$work/a" "$work/check"
(cd "$work/check" && sed -n '/^--- a\//,$p' "$work/new.patch" | patch -p1 -s)
(cd "$vendored" && find . -type f ! -path ./moonscript.patch) | while read -r f; do
	cmp -s "$work/check/$f" "$vendored/$f" || { echo "patch does not reproduce $f" >&2; exit 1; }
done

if [ "${1:-}" = "--check" ]; then
	cmp -s "$work/new.patch" "$patch_file" || {
		echo "lua/moonscript.patch is out of date: run scripts/gen-gopher-lua-patch.sh" >&2
		exit 1
	}
	echo "moonscript.patch is up to date"
else
	cp "$work/new.patch" "$patch_file"
	echo "wrote $patch_file"
fi
