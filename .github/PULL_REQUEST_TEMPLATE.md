<!--
Thanks for the pull request! Describe what it changes and why.
Link the issue it closes, if any: "Closes #123".
-->

- [ ] Tests added or updated; a bug fix has a test that fails without the fix
- [ ] `go test ./...` and golangci-lint pass (the root config and, for `luascope/` and `luamerge/`, their own; see CONTRIBUTING.md)
- [ ] For `luascope/` and `luamerge/`: `codespell ./luascope ./luamerge` passes
- [ ] Generated files regenerated (`go generate ./...`), if their sources changed
- [ ] `lua/CHANGES.md` has an entry for a change to `lua/`
- [ ] Output still identical to the reference (upstream MoonScript 0.8.0, lua5.1, LuaJIT), or the difference is documented
- [ ] The changelog of the tree (`CHANGELOG.md`, `luascope/CHANGELOG.md`, `luamerge/CHANGELOG.md`) updated under `Unreleased`
- [ ] Documentation updated (README, doc comments, `Example…` functions) if the public API changed
