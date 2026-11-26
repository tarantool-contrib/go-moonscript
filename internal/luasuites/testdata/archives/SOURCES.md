# Test suites

The archives hold third-party Lua test suites and real-world Lua code, run
or compiled by internal/luasuites. Each archive keeps the license of its
source. They are test data and are not part of the program.

| Archive | Source | Revision | License |
|:---|:---|:---|:---|
| `lua5.1-tests.tar.gz` | <https://www.lua.org/tests/lua5.1-tests.tar.gz>, unchanged | Lua 5.1 (sha256 `49e4ca6561f82ea605908c5041ab5fad66ed9930fa0686675bd51b02767f18ad`) | MIT (Lua license, see below) |
| `luajit-tests.tar.gz` | `COPYRIGHT`, `test/LuaJIT-tests`, `test/lua-Harness-tests` and the Lua files of `test/tarantool-tests` of <https://github.com/tarantool/luajit> | `8af934d93cf26fb1e9b08b28a8b51833d55fc8e8` | MIT, `COPYRIGHT` in the archive; lua-Harness files: MIT/X11 (Copyright (C) 2009-2021 Francois Perrad, stated in each file) |
| `tarantool.tar.gz` | `src/lua` and `src/box/lua` of <https://github.com/tarantool/tarantool> | `36f5233ac875e4d5b1a9310697a84ccdb77ef5b5` | BSD-2-Clause, `LICENSE` in the archive |
| `vshard.tar.gz` | all `*.lua` files of <https://github.com/tarantool/vshard> | `4732c58cdaafbb8fd1932028f6471a6c44248d9a` | BSD-2-Clause, `LICENSE` in the archive |
| `crud.tar.gz` | all `*.lua` files of <https://github.com/tarantool/crud> | `e7b4b8b57e0d73785f8891832a3de1c5953ac489` | BSD-2-Clause, `LICENSE` in the archive |

`lua5.1-tests.tar.gz`, `tarantool.tar.gz`, `vshard.tar.gz` and
`crud.tar.gz` are byte-identical copies of the archives of the same names in
go-luascope (<https://github.com/bigbes/go-luascope>,
`parser/testdata/corpus`). `lua5.1-tests.tar.gz` is also byte-identical to
lua.org's download (checked on 2026-11-26).

`luajit-tests.tar.gz` is a superset of go-luascope's archive of that name
(which has only `COPYRIGHT` and the `*.lua` and `*.t` files): it adds what
running the suites needs, the `index` files and `README.md` of
`test/LuaJIT-tests`, `test/LuaJIT-tests/common/ffi_util.inc`, and the regex
data files `rx_captures`, `rx_charclass` and `rx_metachars` of
`test/lua-Harness-tests`. The `src` directory of LuaJIT-tests (the C/C++
test libraries) and the CMake files are left out. Its file list, in a
checkout of the revision above:

```sh
{ git ls-files COPYRIGHT test/LuaJIT-tests test/lua-Harness-tests test/tarantool-tests |
      grep -E '\.(lua|t)$|^COPYRIGHT$'
  git ls-files test/LuaJIT-tests test/lua-Harness-tests |
      grep -E '/index$|/README\.md$|\.inc$|/rx_[a-z]+$'; } | sort -u
```

The archives are reproducible (with umask 002 for the file modes):

```sh
tar --sort=name --owner=0 --group=0 --numeric-owner \
    --mtime='2026-01-01 00:00Z' -cf - <files> | gzip -9n > <archive>
```

## Licenses

All of them permit redistribution, here in a BSD/MIT-licensed repository,
provided the notices are kept:

- Lua 5.1 tests: lua.org states that "all files are distributed under"
  the Lua license (<https://www.lua.org/tests/>,
  <https://www.lua.org/license.html>), the MIT license, which the archive
  does not repeat:

  > Copyright (C) 1994-2012 Lua.org, PUC-Rio.
  >
  > Permission is hereby granted, free of charge, to any person obtaining a
  > copy of this software and associated documentation files (the
  > "Software"), to deal in the Software without restriction, including
  > without limitation the rights to use, copy, modify, merge, publish,
  > distribute, sublicense, and/or sell copies of the Software, and to
  > permit persons to whom the Software is furnished to do so, subject to
  > the following conditions:
  >
  > The above copyright notice and this permission notice shall be included
  > in all copies or substantial portions of the Software.
  >
  > THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS
  > OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF
  > MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT.
  > IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY
  > CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT,
  > TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE
  > SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

- tarantool/luajit (LuaJIT-tests, lua-Harness-tests, tarantool-tests): the
  repository's MIT license, `COPYRIGHT` in the archive (LuaJIT, Copyright
  (C) 2005-2017 Mike Pall, and the Lua 5.1/5.2 code it includes). The
  lua-Harness files also state their own MIT/X11 license in their headers.
- tarantool, vshard, crud: BSD-2-Clause, whose conditions (keep the
  copyright notice, the conditions and the disclaimer with source and
  binary redistributions) the `LICENSE` file in each archive meets.

`testdata/lua/ljrun.lua` is derived from LuaJIT-tests' `test.lua` (MIT, as
above); the other scripts in `testdata/lua` are go-moonscript's.
