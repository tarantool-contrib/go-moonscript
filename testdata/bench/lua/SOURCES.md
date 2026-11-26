# Benchmark programs: sources and licenses

The Lua benchmarks of `internal/tools/luabench` are LuaJIT's benchmark
suite, <https://github.com/LuaJIT/LuaJIT-test-cleanup> directory `bench/` at
commit `014708bceb70550a3ab8d539cff14d9085ca9cb8`. That repository has no
license of its own and its programs come from various sources, so only the
files whose license clearly allows redistribution are kept here, unmodified;
the harness fetches the others at the pinned commit into its cache directory
(`$LUABENCH_CACHE`, default `~/.cache/go-moonscript/luajit-bench`).

## Vendored here

| file | origin | license |
|---|---|---|
| `coroutine-ring.lua` | The Computer Language Benchmarks Game (header: contributed by Sam Roberts, reviewed by Bruno Massa) | BSD 3-Clause (the Benchmarks Game's revised BSD license, below) |
| `life.lua` | Lua 5.1 distribution, `test/life.lua` (original by Dave Bollinger, posted to lua-l); byte-identical to the file in lua-5.1.5.tar.gz | MIT (the Lua license, below) |

## Fetched at run time (not vendored)

| file(s) | origin | why not vendored |
|---|---|---|
| `binary-trees`, `chameneos`, `fannkuch`, `fasta`, `k-nucleotide`, `mandelbrot`, `mandelbrot-bit`, `meteor`, `nbody`, `nsieve`, `nsieve-bit`, `nsieve-bit-fp`, `partialsums`, `pidigits-nogmp`, `recursive-ack`, `recursive-fib`, `revcomp`, `spectral-norm`, `sum-file` (`.lua`) | Benchmarks Game problems; the files carry no header or attribution (likely Mike Pall's own versions, several of which he contributed to the Benchmarks Game) | origin and license not stated in the file |
| `array3d.lua`, `euler14-bit.lua`, `md5.lua` | no header (LuaJIT's own tests, presumably by Mike Pall) | no license stated |
| `ray.lua` | no header; a port of Jon Harrop's ray tracer benchmark | no license stated |
| `scimark_lib.lua`, `scimark-fft.lua`, `scimark-lu.lua`, `scimark-sor.lua`, `scimark-sparse.lua`, `scimark-2010-12-20.lua` | Lua SciMark, "a literal translation of SciMark 2.0a" (SciMark itself is public domain); `scimark-2010-12-20.lua` says "Copyright (C) 2006-2010 Mike Pall" | a copyright notice without a license grant |
| `SUMCOL_1.txt` (input of `sum-file`) | Benchmarks Game test data | not stated in the repository |

`PARAM_x86.txt` and `TEST_md5sum.txt` are not copied either: the harness
has its own parameters, and the expected checksums of `TEST_md5sum.txt`
are listed in its benchmark table (`internal/tools/luabench/suite.go`).

## Licenses

### coroutine-ring.lua: The Computer Language Benchmarks Game (BSD 3-Clause)

The Benchmarks Game's "Revised BSD license", as published at
<https://benchmarksgame-team.pages.debian.net/benchmarksgame/license.html>
(retrieved 2026-11-26):

    Copyright (c) 2004-2008 Brent Fulgham, 2005-2025 Isaac Gouy
    All rights reserved.

    Redistribution and use in source and binary forms, with or without
    modification, are permitted provided that the following conditions are
    met:

      * Redistributions of source code must retain the above copyright
        notice, this list of conditions and the following disclaimer.
      * Redistributions in binary form must reproduce the above copyright
        notice, this list of conditions and the following disclaimer in the
        documentation and/or other materials provided with the distribution.
      * Neither the name "The Computer Language Benchmarks Game" nor the name
        "The Benchmarks Game" nor the name "The Computer Language Shootout
        Benchmarks" nor the names of its contributors may be used to endorse
        or promote products derived from this software without specific
        prior written permission.

    THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS
    IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED
    TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A
    PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT OWNER
    OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL,
    EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT LIMITED TO,
    PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE, DATA, OR
    PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY THEORY OF
    LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT (INCLUDING
    NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE OF THIS
    SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

### life.lua: Lua 5.1 (MIT)

    Copyright (C) 1994-2012 Lua.org, PUC-Rio.

    Permission is hereby granted, free of charge, to any person obtaining a
    copy of this software and associated documentation files (the
    "Software"), to deal in the Software without restriction, including
    without limitation the rights to use, copy, modify, merge, publish,
    distribute, sublicense, and/or sell copies of the Software, and to
    permit persons to whom the Software is furnished to do so, subject to
    the following conditions:

    The above copyright notice and this permission notice shall be included
    in all copies or substantial portions of the Software.

    THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS
    OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF
    MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT.
    IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY
    CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT,
    TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE
    SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
