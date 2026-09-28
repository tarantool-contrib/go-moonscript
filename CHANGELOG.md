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

[Unreleased]: https://github.com/tarantool/go-luamerge/compare/HEAD
