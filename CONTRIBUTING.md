# Contribution Guide

## First steps

The project requires Go 1.25 or later. Clone the repository and install
dependencies.

```sh
$ git clone https://github.com/tarantool/go-luamerge
$ cd go-luamerge
$ go mod tidy
```

Some integration tests execute generated bundles under a `lua` interpreter.
They are skipped automatically when no `lua` is on `PATH`, so a Lua interpreter
is optional but recommended for full coverage.

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

[godoc-url]: https://pkg.go.dev/github.com/tarantool/go-luamerge
