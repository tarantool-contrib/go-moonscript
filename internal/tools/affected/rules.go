package main

import (
	"path"
	"strings"
)

// The rules of the repository: what is a whole-repository change, what is
// documentation, which tests read which data outside their package.

// wholeRepo: a change to one of these files, or to a file under one of
// these directories ("dir/"), runs everything: the module's dependencies,
// the CI itself, the linters' configs and this tool.
var wholeRepo = []string{
	"go.mod",
	"go.sum",
	"go.work",
	"go.work.sum",
	".github/", // but its documentation (IsDoc)
	".golangci.yml",
	"luascope/.golangci.yml",
	"luamerge/.golangci.yml",
	".codespellrc",
	"scripts/ci/",
	"scripts/check-gopher-lua-suite.sh",
	"scripts/check-corpus.sh",
	"internal/tools/affected/",
}

// WholeRepo: a change to f runs everything.
func WholeRepo(f string) bool {
	if strings.HasPrefix(f, ".github/") && IsDoc(f) {
		return false
	}
	for _, w := range wholeRepo {
		if f == w || strings.HasSuffix(w, "/") && strings.HasPrefix(f, w) {
			return true
		}
	}
	return false
}

// IsDoc: f is documentation, which no build reads: Markdown, the licenses,
// GitHub's issue templates. A test that reads one lists it in DataDeps,
// and a package that embeds one has it in its files.
func IsDoc(f string) bool {
	switch base := path.Base(f); {
	case strings.HasSuffix(base, ".md"),
		base == "LICENSE", base == "NOTICE",
		strings.HasPrefix(f, ".github/ISSUE_TEMPLATE/"):
		return true
	}
	return false
}

// A DataDep is data that the tests of the package in Reader read outside
// its directory, through paths go list does not know of. Paths are
// files, or directories ending in "/", relative to the root. A change
// under one of them runs Reader's tests (not its dependents': the data
// is the tests' input, not the package's).
//
// scripts/ci/check-data-deps.sh checks the table: it runs every test
// binary under strace and fails on a file a test opens outside its
// package that the table does not list.
type DataDep struct {
	Reader string
	Paths  []string
}

// The archives of the Lua 5.1 test suite, LuaJIT's tests and real-world
// Lua code (internal/luasuites/testdata/archives) are read by every
// parser-level corpus test.
const luaArchives = "internal/luasuites/testdata/archives/"

// DataDeps: luamerge's corpus tests also read gopher-lua v1.1.2's test
// suites from the module cache (go mod download): that is go.sum's
// business, a whole-repository change.
var DataDeps = []DataDep{
	{"lua", []string{"moonscript/runtime/lua/"}},
	{"lua/flow", []string{"lua/testdata/", luaArchives}},
	{"lua/format", []string{"lua/testdata/", luaArchives, "moonscript/testdata/"}},
	{"lua/parse", []string{"lua/testdata/", luaArchives}},
	{"lua/scope", []string{"lua/testdata/", luaArchives}},
	{"luascope", []string{luaArchives}},
	{"luamerge/internal/integration", []string{"luamerge/testdata/"}},
	{"luamerge/internal/lex", []string{"luamerge/testdata/"}},
	{"luamerge/internal/parse", []string{"luamerge/testdata/"}},
	{"luamerge/internal/transform", []string{"luamerge/testdata/"}},
	{"moonscript/parse", []string{"moonscript/testdata/parse/"}},
	{"luascope/globals", []string{luaArchives, "luascope/testdata/golden/"}},
	{"moonscript/runtime", GenerateFiles},
	{"internal/tools/luabench", []string{"moonscript/testdata/bench/"}},
	{"cmd/moon", []string{"moonscript/testdata/"}},
	{"cmd/moonc", []string{"moonscript/testdata/lint/"}},
	{"internal/tools/corpus", []string{"moonscript/testdata/corpus/"}},
	{"internal/tools/genparser", []string{"moonscript/parse/parser_gen.go"}},
}

// DataReaders are the packages (directories) whose tests read f as data
// outside their directory.
func DataReaders(f string) []string {
	var out []string
	for _, d := range DataDeps {
		if d.covers(f) {
			out = append(out, d.Reader)
		}
	}
	return out
}

// covers: f is one of d's paths, or under one of its directories but
// documentation, which a test that reads it lists by name.
func (d DataDep) covers(f string) bool {
	for _, p := range d.Paths {
		if f == p || f+"/" == p || strings.HasSuffix(p, "/") && strings.HasPrefix(f, p) && !IsDoc(f) {
			return true
		}
	}
	return false
}

// Tree is the tree of the package in dir: lua (the fork and the Lua test
// suites run on it), moonscript (the port and its commands), luascope,
// luamerge or tools (internal/tools).
func Tree(dir string) string {
	first, _, _ := strings.Cut(dir, "/")
	switch {
	case first == "lua" || dir == "internal/luasuites":
		return "lua"
	case first == "moonscript" || first == "cmd" || dir == "internal/grammar":
		return "moonscript"
	case first == "luascope" || first == "luamerge":
		return first
	}
	return "tools"
}

// GenerateInputs: the packages of the //go:generate directives and the
// generators they run. go generate's output depends on them and on what
// they import, so a change that selects one runs the generate check.
var GenerateInputs = []string{
	"moonscript/parse",
	"moonscript/runtime",
	"internal/tools/genparser",
	"internal/tools/genruntime",
}

// GenerateFiles: the files of other packages go generate reads or writes
// (internal/tools/genruntime compiles cmd/moon/moon.moon to moon.lua).
var GenerateFiles = []string{"cmd/moon/moon.moon", "cmd/moon/moon.lua"}

// BuildDeps: the code of the package Reader, and so the tests that import
// it, runs go build on the packages Builds (a dependency go list does not
// show): luatest.CLIPath builds luamerge's command.
var BuildDeps = []struct {
	Reader string
	Builds []string
}{
	{"luamerge/internal/luatest", []string{"luamerge/cmd/luamerge"}},
}

// SelfDir is this tool's package. Its tests read the package graph of
// the whole tree and its history: a change that selects a package selects
// them too.
const SelfDir = "internal/tools/affected"

// CorpusChecker is the package of the corpus job's checker.
const CorpusChecker = "internal/tools/corpus"

// GoreleaserInputs: besides luamerge's packages, the files goreleaser
// check reads.
var GoreleaserInputs = []string{
	"luamerge/.goreleaser.yml",
	"luamerge/Dockerfile",
	"luamerge/.dockerignore",
}

// CodespellDirs: the trees codespell checks (.codespellrc).
var CodespellDirs = []string{"luascope/", "luamerge/"}

// ChangesFile is the fork's list of changes, which every change of a
// non-test file of lua/ updates.
const ChangesFile = "lua/CHANGES.md"
