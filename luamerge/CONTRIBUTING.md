# Contribution Guide

## First steps

The project requires Go 1.26.5 or later. It is `luamerge/` of the monorepo
of the Go Lua tooling (one Go module; see the root
[CONTRIBUTING.md](../CONTRIBUTING.md) for what the whole repository runs).
Clone the repository and run the commands of this file in `luamerge/`:

```sh
$ git clone git@github.com:tarantool-contrib/go-moonscript.git
$ cd go-moonscript/luamerge
$ go mod tidy
```

The tests that execute generated bundles need a Lua interpreter and are
skipped without one; see [Lua interpreters](#lua-interpreters).

## Branch naming conventions

When creating feature branches, follow these naming patterns:

- `<user.name>/gh-<issue-id>-short-description` (for GitHub issues)
- `<user.name>/gh-no-short-description` (for work not tied to an issue)

For release branches, use:

- `<user.name>/release-v<version>`

Replace `<user.name>` with your Git username, `<issue-id>` with the issue
number, and `<short-description>` with a brief description using lowercase,
hyphen-separated words; do not use any symbols except numbers, letters and
dashes.

Although working on features without an opened issue is possible — avoid that
at all costs.

## Running tests

```bash
# Run unit and integration tests for the main package and each subpackage:
make test

# Run tests with the race detector:
make testrace

# Run tests for a specific package (e.g., internal/parse):
go test ./internal/parse
```

### Lua interpreters

The tests that execute generated bundles (`internal/integration`, the Lua
test of the root package and the harness in `internal/luatest`) run each
bundle on every Lua interpreter found on `PATH`: `lua5.1`, `lua5.2`,
`lua5.3`, `lua5.4`, `lua5.5`, `luajit`, `tarantool` and `lua`, once per
distinct binary. The original program runs from its project directory and
the bundle from an empty directory, so a module missing from the bundle
cannot be loaded from disk.

With no interpreter on `PATH` these tests are skipped. To make a missing
interpreter a failure instead, list the interpreters that must be present
in `LUATEST_INTERPRETERS`; CI sets it to the interpreters it installs:

```bash
LUATEST_INTERPRETERS=lua5.1,lua5.4,luajit make test
```

The variable does not start with `LUAMERGE_`: that prefix belongs to the
configuration, which rejects unknown `LUAMERGE_` variables.

To run the tests on the interpreters CI uses without installing them, use a
container:

```bash
docker run --rm -v "$PWD:/src:ro" golang:1.26-alpine sh -c '
  apk add --no-cache git lua5.1 lua5.2 lua5.3 lua5.4 luajit &&
  cp -r /src /work && cd /work &&
  LUATEST_INTERPRETERS=lua5.1,lua5.2,lua5.3,lua5.4,luajit go test ./...'
```

### Test naming conventions

Follow these patterns when naming test functions:

```go
Test<FunctionName>_<Description>
Test<StructName>_<Method>_<Description>
```

Tests are written in the Arrange-Act-Assert (AAA) style, are table-driven where
it helps, and must not be flaky or depend on execution order.

### Code coverage

To run tests with coverage collection:

```bash
make coverage
```

This generates a coverage profile and prints coverage statistics. Aim for at
least 80% coverage on new code.

## Examples

Executable examples live in `example_test.go` and the `Example…` functions of
each package. They are compiled and run in CI, and are rendered on
[pkg.go.dev][godoc-url]. To run them:

```bash
go test -v -run Example ./...
```

## Linting

To run the linter (golangci-lint) on the codebase:

```bash
# Install the pinned linter version once:
make lint-deps

make lint
```

The linter uses `golangci-lint` v2 with `default: all` and a small set of
point disables, each with a comment explaining why. The allowed import list is
enforced by `depguard` in `.golangci.yml` — a new external dependency must be
added there explicitly.

## Formatting

The project uses `gofmt` and `goimports` for formatting:

```bash
make fmt
```

## Spell checking

To check for common misspellings in code and documentation:

```bash
make codespell
```

## Commit message guidelines

Commit messages have three parts: a subject, a body, and issue references.

```
prefix: commit subject

The commit body explains what changed and why, not how.

Closes #1337
```

- Subject and body are in English.
- The subject is `prefix: subject`, where `prefix` is a short area tag
  (typical: `core`, `cli`, `docs`, `tests`, `ci`), followed by a colon, a
  space, and the subject in lowercase. Keep it within ~50 characters, no
  trailing period, in the imperative mood — it should complete the sentence
  "If applied, this commit will …".
- Do not put issue numbers or `Closes #xxx` in the subject line.
- Separate the body from the subject with a blank line; wrap body lines at 72
  characters.
- Reference issues in the last lines of the body: `Part of #NNN` for
  intermediate commits, `Closes #NNN` in the final PR commit.
- Each commit is atomic and logically complete.

## Code review checklist

- The public API exposes only what users need from outside; everything else
  lives under `internal/`.
- Public functions, types, variables and constants have at least a single-line
  doc comment.
- Code is DRY.
- New features have tests; bugfix commits have a regression test based on a
  reproducer.
- There are no changes in files unrelated to the issue.
- There are no obvious flaky tests.
- A `CHANGELOG.md` entry is present for user-visible changes.
- Public API is covered by executable `Example…` functions where it helps.
- No internal URLs, tokens, host names or corporate paths leak into the code.

## Pull request process

1. Fork the repository and create a feature branch.
2. Ensure your code passes `make lint` and `make test`.
3. Update `CHANGELOG.md` with a brief description of your change.
4. Submit a pull request with a clear description of the change and its
   purpose. The repository requires review approval before merge.

## Questions?

If you have questions, feel free to open an issue or reach out via Telegram
([English](http://telegram.me/tarantool) / [Russian](http://telegram.me/tarantoolru)).

[godoc-url]: https://pkg.go.dev/github.com/tarantool-contrib/go-moonscript/luamerge
