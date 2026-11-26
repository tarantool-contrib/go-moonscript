#!/usr/bin/env bash
# Regenerates testdata/docs/ (used by docs_test.go, `go test -run Docs .`)
# from the upstream documentation and the reference compiler.
#
# Every ```moon, ```moononly and ```moonret block of docs/*.md becomes
# testdata/docs/<doc>_<NNN>.moon; the reference moonscript.base.to_lua output
# is stored next to it as <doc>_<NNN>.lua (exact code) or <doc>_<NNN>.err
# (exact error message). MANIFEST.tsv records where each block comes from.
#
# Environment:
#   MOON_SRC   upstream moonscript v0.8.0 checkout
#              (default ~/.cache/go-moonscript/moonscript-0.8.0,
#              fetched by scripts/moonref/build.sh)
#   MOONREF    reference image (default moonref:0.8.0)
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
out="$root/testdata/docs"
moon_src=${MOON_SRC:-${XDG_CACHE_HOME:-$HOME/.cache}/go-moonscript/moonscript-0.8.0}
image=${MOONREF:-moonref:0.8.0}

[ -d "$moon_src/docs" ] || { echo "MOON_SRC=$moon_src has no docs/" >&2; exit 1; }

rm -rf "$out"
mkdir -p "$out"
printf '# name\tsource (upstream v0.8.0 doc:line of the first code line)\tfence\n' > "$out/MANIFEST.tsv"
for md in "$moon_src"/docs/*.md; do
	awk -v out="$out" -v doc="$(basename "${md%.md}")" '
		!inblk && /^```moon[a-z]*[ \t]*$/ {
			n++; name = sprintf("%s_%03d", doc, n); f = out "/" name ".moon"
			fence = $0; sub(/^```/, "", fence); sub(/[ \t]*$/, "", fence)
			printf "%s\tdocs/%s.md:%d\t%s\n", name, doc, NR + 1, fence >> (out "/MANIFEST.tsv")
			inblk = 1; printf "" > f; next
		}
		inblk && /^```/ { inblk = 0; close(f); next }
		inblk { print >> f }
	' "$md"
done

docker run --rm -i --user "$(id -u):$(id -g)" -v "$out":/docs -w /docs "$image" \
	sh -c 'cat > /tmp/refcompile.lua && lua5.1 /tmp/refcompile.lua *.moon' <<'EOF'
-- writes <name>.lua (exact to_lua output) or <name>.err for each argument
local to_lua = require("moonscript.base").to_lua
for _, fname in ipairs(arg) do
  local f = assert(io.open(fname))
  local src = f:read("*a")
  f:close()
  local base = fname:gsub("%.moon$", "")
  local ok, code, err = pcall(to_lua, src)
  local res, ext
  if ok and code then res, ext = code, ".lua"
  elseif ok then res, ext = err, ".err"
  else res, ext = "internal error: " .. tostring(code), ".err" end
  local o = assert(io.open(base .. ext, "w"))
  o:write(res)
  o:close()
end
EOF

echo "generated $(find "$out" -name '*.moon' | wc -l) blocks" \
	"($(find "$out" -name '*.lua' | wc -l) lua, $(find "$out" -name '*.err' | wc -l) errors) in $out"
