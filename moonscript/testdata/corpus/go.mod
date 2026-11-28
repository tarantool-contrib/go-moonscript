// The corpus manifest and expected digests (scripts/check-corpus.sh) are
// data for CI, not for users of the module: this go.mod makes the directory
// its own module, so it is left out of the module zip.
module github.com/tarantool-contrib/go-moonscript/testdata/corpus

go 1.26
