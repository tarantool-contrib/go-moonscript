#!/usr/bin/env bash
# Checks the table of the tests' data dependencies of CI's change detection
# (DataDeps in internal/tools/affected/rules.go): runs the test binary of
# every package under strace -f and fails on a file of the repository a
# test opens or probes outside its package's directory that the table does
# not list (see `go run ./internal/tools/affected verify-data -h`).
#
# It runs in Docker, in an image with strace (6 or newer: -y names the
# working directory of AT_FDCWD), lua5.1 and luajit, so that luamerge's
# tests that run bundles on the interpreters run too. The repository and
# its git directory are mounted at their paths, read-write (the tests
# write nothing there; the go command's VCS stamping runs git), and the
# module cache (luamerge's corpus tests download gopher-lua v1.1.2's
# suites; GOSUMDB=off, as the host's go verified the cache); the build
# cache persists in
# ${XDG_CACHE_HOME:-~/.cache}/go-moonscript/ci-strace, and the work
# directory (-keep keeps it) is in its tmp/.
#
# Usage: scripts/ci/check-data-deps.sh [verify-data flags: -v, -pkg RE, -j N]
#   DOCKER=0  run on the host instead (strace 6+; without lua5.1 and luajit
#             luamerge's interpreter tests skip, and their reads go unseen)
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
go_image=golang:1.27.4-trixie
image=go-moonscript-ci-strace:1

cd "$root"
if [ "${DOCKER:-1}" = 0 ]; then
	exec go run ./internal/tools/affected verify-data -C "$root" "$@"
fi

if ! docker image inspect "$image" >/dev/null 2>&1; then
	docker build -q -t "$image" - <<EOF >/dev/null
FROM $go_image
RUN apt-get update && apt-get install -y --no-install-recommends strace lua5.1 luajit \
	&& rm -rf /var/lib/apt/lists/*
EOF
fi

modcache=$(go env GOMODCACHE)
gitdir=$(cd "$(git rev-parse --git-common-dir)" && pwd)
cache=${XDG_CACHE_HOME:-$HOME/.cache}/go-moonscript/ci-strace
mkdir -p "$cache/go-build" "$cache/home" "$cache/tmp"

docker run --rm --init \
	--user "$(id -u):$(id -g)" \
	--cap-add SYS_PTRACE --security-opt seccomp=unconfined \
	-v "$root:$root" -v "$gitdir:$gitdir" \
	-v "$modcache:/go/pkg/mod" -v "$cache:/cache" \
	-e HOME=/cache/home -e GOCACHE=/cache/go-build -e TMPDIR=/cache/tmp -e GOMODCACHE=/go/pkg/mod \
	-e GOFLAGS=-mod=readonly -e GOTOOLCHAIN=local -e GOSUMDB=off \
	-e LUATEST_INTERPRETERS=lua5.1,luajit \
	-w "$root" "$image" \
	go run ./internal/tools/affected verify-data -C "$root" "$@"
