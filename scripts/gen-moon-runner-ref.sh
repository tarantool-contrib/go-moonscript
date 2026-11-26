#!/usr/bin/env bash
# Regenerates the reference `moon` outputs used by cmd/moon's tests (needs
# Docker and the moonref:0.8.0 image, whose `moon` is upstream bin/moon on
# PUC Lua 5.1.5; for the LuaJIT references, luajitref:2.1, which is
# moonref:0.8.0 plus Debian's luajit 2.1, built here if missing):
#
#   testdata/moon_runner/ref/<name>.{stdout,stderr,exit}
#       for every case in testdata/moon_runner/cases.tsv, run in
#       testdata/moon_runner (TestMoonRunner)
#   testdata/moon_smoke/ref/<name>.{stdout,stderr,exit}
#       for every script listed in testdata/moon_smoke/scripts.txt (upstream
#       docs examples and spec inputs), run in testdata; <name> is the path
#       with "/" replaced by "_" (TestMoonSmoke; a script still running after
#       10s is killed and recorded with exit status 124, the test skips it)
#   testdata/moon_runner/ref_luajit/, testdata/moon_smoke/ref_luajit/
#       the same with upstream bin/moon run by luajit instead of lua5.1
#       (TestMoonRunnerLuaJIT, TestMoonSmokeLuaJIT: the port with
#       MOON_LUA=luajit). The launcher is the image's LuaRocks wrapper,
#       /usr/local/bin/moon, with /usr/bin/lua5.1 replaced by
#       /usr/bin/luajit; LuaJIT loads the Lua 5.1 lpeg and lfs C modules.
#
# The reference runs with the port's moonscript.cmd.coverage
# (runtime/lua/moonscript/cmd/coverage.lua) mounted over the installed one:
# upstream's hooks a tail call and never reports anything on Lua 5.1, the
# port fixes that on purpose, and the moon -c cases check the fixed module
# on the reference interpreter.
#
#   scripts/gen-moon-runner-ref.sh [lua51|luajit]   (default: both)
#
# The tests themselves run without Docker.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
image=${MOONREF:-moonref:0.8.0}
jitimage=${LUAJITREF:-luajitref:2.1}
which=${1:-all}

case $which in
lua51 | luajit | all) ;;
*)
	echo "usage: $0 [lua51|luajit]" >&2
	exit 2
	;;
esac

# run_cases runs in the container, $1 the name of the reference directories;
# the moon on PATH is the reference.
run_cases='
set -u
ref=$1
cd /data/moon_runner
rm -rf "$ref" && mkdir "$ref"
grep -v "^#" cases.tsv | while IFS=$'"'"'\t'"'"' read -r -a fields; do
	name=${fields[0]}
	args=("${fields[@]:1}")
	moon "${args[@]}" > "$ref/$name.stdout" 2> "$ref/$name.stderr" < /dev/null
	echo $? > "$ref/$name.exit"
done
cd /data
rm -rf "moon_smoke/$ref" && mkdir "moon_smoke/$ref"
grep -v "^#" moon_smoke/scripts.txt | while read -r f; do
	name=${f//\//_}
	name=${name%.moon}
	timeout 10 moon "$f" > "moon_smoke/$ref/$name.stdout" 2> "moon_smoke/$ref/$name.stderr" < /dev/null
	echo $? > "moon_smoke/$ref/$name.exit"
done
'

# the LuaJIT launcher, in the container's /tmp
jit_setup='
mkdir -p /tmp/jitbin
sed "s|/usr/bin/lua5.1|/usr/bin/luajit|" /usr/local/bin/moon > /tmp/jitbin/moon
chmod +x /tmp/jitbin/moon
export PATH=/tmp/jitbin:$PATH
'

# reference image ref-dir setup-script
reference() {
	timeout 1800 docker run --rm --user "$(id -u):$(id -g)" -v "$root/testdata":/data \
		-v "$root/runtime/lua/moonscript/cmd/coverage.lua":/usr/local/share/lua/5.1/moonscript/cmd/coverage.lua:ro \
		"$1" bash -c "$3$run_cases" bash "$2" 2>&1 | head -c 100000
	echo "moon_runner/$2: $(ls "$root/testdata/moon_runner/$2"/*.exit | wc -l) cases"
	echo "moon_smoke/$2: $(ls "$root/testdata/moon_smoke/$2"/*.exit | wc -l) scripts," \
		"$(grep -l '^0$' "$root/testdata/moon_smoke/$2"/*.exit | wc -l) exit 0"
}

if [ "$which" != luajit ]; then
	reference "$image" ref ""
fi
if [ "$which" != lua51 ]; then
	if ! docker image inspect "$jitimage" >/dev/null 2>&1; then
		printf 'FROM %s\nUSER root\nRUN apt-get update && apt-get install -y --no-install-recommends luajit && rm -rf /var/lib/apt/lists/*\n' "$image" |
			timeout 1800 docker build -q -t "$jitimage" - >/dev/null
	fi
	reference "$jitimage" ref_luajit "$jit_setup"
fi
