# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Initial public release of `luamerge`, a Go library and CLI that merges a
  multi-file Lua project into a single self-contained `.lua` file while
  preserving exact `require()` semantics (run-once caching, circular
  dependencies, module identity).
- Static `require()` analysis via the gopher-lua parser: modules are discovered
  by parsing source, without running the program.
- Public Go API (`Bundle`, `Options`, `Result`, `LoadOptions`,
  `DefaultOptions`, classifiable errors and warnings) with the implementation
  kept under `internal/`.
- CLI (`cmd/luamerge`) with YAML config, `LUAMERGE_*` environment overrides and
  flags: entry/output/root/path/search, `--skip`/`--include`, `--strict`,
  `--debug`, `--fallback`, `--shebang`, transform flags
  (`--remove-comments`, `--remove-empty-lines`, `--minify`, `--strip-shebang`),
  `--prefix`/`--suffix`, and package-name remapping
  (`--package-prefix`, `--strip-prefix`, `--package-name`).
- Configuration is assembled and merged (defaults → YAML file → `LUAMERGE_*`
  environment) by the Tarantool ecosystem's `go-config` library and validated
  against a JSON Schema, so an unknown key or a wrong-typed value in
  `luamerge.yaml` is reported as an error rather than silently ignored.
- `Result.Files` lists the absolute path of every source file embedded in the
  bundle, the entry first, so a caller can refuse an output path that is one of
  the inputs or record the bundle's dependencies.
- `WarnIndirectRequire` (`indirect-require`): a warning for `require` used
  other than by a call — aliased (`local r = require`), stored in a table or
  passed to a function — because the modules loaded through it cannot be found
  and are not embedded. Such uses used to be dropped without a word.

### Changed

- Go 1.26.5 is the minimum supported version, as required by the updated
  dependencies. The update also makes the module build with Go 1.27, where the
  previous `go-json-experiment/json` failed to compile.

### Fixed

- The CLI never overwrites its inputs: an `--output` that is the same file as
  the entry, a bundled module or the config file is refused. It used to be
  truncated before it was read, and the build then bundled the empty file and
  exited with success.
- A failed build keeps the previous output: the bundle is written only once
  the build has succeeded, so a failed build no longer leaves an empty file
  that `make` takes for up to date, creates no directories and prints nothing
  with `--output -`.
- The CLI replaces the output file atomically: the bundle is written to a
  temporary file in the same directory and renamed over the old one only once
  complete, so a failed or interrupted write (a full disk, a file-size limit)
  no longer leaves a truncated bundle. The file keeps its permission bits, and
  a read-only file is still refused.
- A `require` whose result is the receiver of a method call is found and its
  module embedded: `require("x"):new()`, `(require "x"):upper()`,
  `require("x").field:m()` and every require inside a method chain such as
  `app:use(require "a"):listen()`. These modules were left out of the bundle
  without a warning, even with `--strict`.
- `_G.require("x")`, `_ENV.require("x")` and `xpcall(require, handler, "x")`
  embed their module, and `pcall(require, name)` with a computed name warns
  as a dynamic require, like `require(name)`. All of them used to be ignored
  silently. `require("x", …)` is the require of `"x"`, as in Lua, rather
  than a dynamic require.
- Release archives now include a `darwin_arm64` build for Apple Silicon
  Macs. GoReleaser's build matrix excluded that target under a comment
  claiming it needs CGO, although the build already sets `CGO_ENABLED=0`.

[Unreleased]: https://github.com/tarantool/go-luamerge/compare/HEAD
