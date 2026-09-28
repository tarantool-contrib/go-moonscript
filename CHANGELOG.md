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
- The entry's code is the bundle's main chunk: a bundle no longer ends with
  `return require("<entry>")`, and the entry is registered in
  `package.preload` only when a module requires it by name, as a separate
  instance, the way a `require` of the entry's file loads one natively.

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
- A module name resolves like Lua's `package.searchpath`: relative to the
  root, then to each search directory, and never relative to the file that
  requires it. A project with `util.lua` and `sub/util.lua` used to get
  `sub/util.lua` embedded as `util` for every module, with no warning even
  with `--strict`; now `util` is `util.lua`, as it is when the program runs
  from the root. The `--strip-prefix`, `--package-name`, root-name and
  search-directory-name prefix stripping applies only when that lookup finds
  nothing, and one module name registered for two different files is an
  error.
- A module name written as a path follows Lua's rules too: every `.` in it
  becomes a `/`, so `require("./foo")`, `require("../foo")` and
  `require(".foo")` all embed `foo.lua` and `require("a/b.c")` embeds
  `a/b/c.lua`, as they load natively. Such names used to be taken as they
  were: `../foo` pulled in a file from outside the project, `.foo` embedded
  `.foo.lua`, and `a/b.c` was not found.
- The entry of a bundle runs like a script: its `...` holds the command-line
  arguments, all of its return values are kept, the "am I the main script?"
  idiom (`pcall(debug.getlocal, 4, 1)`) works, and the entry is not cached in
  `package.loaded`, so a second bundle whose entry has the same name, or a
  second `dofile` of the same bundle, runs its entry again.
- A library bundled into `mylib.lua` from an entry named `mylib` can be loaded
  with `require("mylib")` on Lua 5.1, LuaJIT and Tarantool.
- With `--fallback`, a `main.lua` in the working directory no longer replaces
  the bundle's entry.
- A bundle built with `--suffix` failed to load with a syntax error: the
  suffix was emitted after the final `return`. The suffix now runs after the
  entry, and the bundle still returns every value the entry returns.

[Unreleased]: https://github.com/tarantool/go-luamerge/compare/HEAD
