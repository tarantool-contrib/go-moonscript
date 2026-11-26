#!/usr/bin/env bash
# Builds the reference Docker image (default tag moonref:0.8.0) that the
# scripts/gen-*.sh generators and internal/tools/{corpus,parsefuzz} run, and
# unpacks the same upstream v0.8.0 sources into MOON_SRC for the generators
# that copy upstream inputs (scripts/gen-parse-golden.sh --import,
# scripts/gen-docs-golden.sh).
#
# Environment:
#   MOONREF   image tag (default moonref:0.8.0)
#   MOON_SRC  where to unpack the sources
#             (default ~/.cache/go-moonscript/moonscript-0.8.0)
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
image=${MOONREF:-moonref:0.8.0}
moon_src=${MOON_SRC:-${XDG_CACHE_HOME:-$HOME/.cache}/go-moonscript/moonscript-0.8.0}
url=https://github.com/leafo/moonscript/archive/refs/tags/v0.8.0.tar.gz
sha256=0458ba86359c9089f823f81e4198ea3f7d57c2f2773fb8a1364940ebdd802ac5

docker build -t "$image" "$here"

if [ ! -f "$moon_src/moonscript-dev-1.rockspec" ]; then
	tmp=$(mktemp -d "${moon_src%/*}/.moonscript.XXXXXX" 2>/dev/null ||
		{ mkdir -p "${moon_src%/*}" && mktemp -d "${moon_src%/*}/.moonscript.XXXXXX"; })
	trap 'rm -rf "$tmp"' EXIT
	curl -fsSL "$url" -o "$tmp/src.tar.gz"
	echo "$sha256  $tmp/src.tar.gz" | sha256sum -c --quiet
	mkdir -p "$moon_src"
	tar -xzf "$tmp/src.tar.gz" -C "$moon_src" --strip-components=1
fi
echo "image: $image"
echo "sources: $moon_src"
