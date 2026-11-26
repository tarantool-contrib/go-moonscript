#!/usr/bin/env bash
# Regenerates the reference `moon` outputs used by cmd/moon's tests (needs
# Docker and the moonref:0.8.0 image, whose `moon` is upstream bin/moon on
# PUC Lua 5.1.5):
#
#   testdata/moon_runner/ref/<name>.{stdout,stderr,exit}
#       for every case in testdata/moon_runner/cases.tsv, run in
#       testdata/moon_runner (TestMoonRunner)
#   testdata/moon_smoke/ref/<name>.{stdout,stderr,exit}
#       for every script listed in testdata/moon_smoke/scripts.txt (upstream
#       docs examples and spec inputs), run in testdata; <name> is the path
#       with "/" replaced by "_" (TestMoonSmoke; a script still running after
#       10s is killed and recorded with exit status 124, the test skips it)
#
# The reference runs with the port's moonscript.cmd.coverage
# (runtime/lua/moonscript/cmd/coverage.lua) mounted over the installed one:
# upstream's hooks a tail call and never reports anything on Lua 5.1, the
# port fixes that on purpose, and the moon -c cases check the fixed module
# on PUC Lua.
#
# The tests themselves run without Docker.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
image=${MOONREF:-moonref:0.8.0}

run_cases='
set -u
cd /data/moon_runner
rm -rf ref && mkdir ref
grep -v "^#" cases.tsv | while IFS=$'"'"'\t'"'"' read -r -a fields; do
	name=${fields[0]}
	args=("${fields[@]:1}")
	moon "${args[@]}" > "ref/$name.stdout" 2> "ref/$name.stderr" < /dev/null
	echo $? > "ref/$name.exit"
done
cd /data
rm -rf moon_smoke/ref && mkdir moon_smoke/ref
grep -v "^#" moon_smoke/scripts.txt | while read -r f; do
	name=${f//\//_}
	name=${name%.moon}
	timeout 10 moon "$f" > "moon_smoke/ref/$name.stdout" 2> "moon_smoke/ref/$name.stderr" < /dev/null
	echo $? > "moon_smoke/ref/$name.exit"
done
'

docker run --rm --user "$(id -u):$(id -g)" -v "$root/testdata":/data \
	-v "$root/runtime/lua/moonscript/cmd/coverage.lua":/usr/local/share/lua/5.1/moonscript/cmd/coverage.lua:ro \
	"$image" bash -c "$run_cases"
echo "moon_runner: $(ls "$root"/testdata/moon_runner/ref/*.exit | wc -l) cases"
echo "moon_smoke: $(ls "$root"/testdata/moon_smoke/ref/*.exit | wc -l) scripts," \
	"$(grep -l '^0$' "$root"/testdata/moon_smoke/ref/*.exit | wc -l) exit 0"
