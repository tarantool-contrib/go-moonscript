package main

// Expected results as digests: what a corpus fetched from the committed
// manifest (see pinned.go) is checked against, in place of the reference
// outputs under ref/ and ref-lint/.
//
// A digest is the first 64 bits of the sha256 of the compared text, in hex
// (a mismatching output goes unnoticed with probability 2^-64). Each case
// has a reference entry, its outcome code and digests:
//
//	o:TREE:LUA        compiled (ok)
//	c:TREE:ERR        compile error (compile.format_error text)
//	i:TREE:MSG        reference internal error (MSG: normInternal of it)
//	p:ERR             parse error
//	STATUS            no usable reference (ref-timeout, parse-internal, ...)
//
// and a lint entry (moonscript segment only, else "-"): c (clean), l:REPORT
// (lint; REPORT: compact + NUL + default), p:ERR (parse error), t:FIELDS
// (crash-table: message and position, without the kind), s:MSG
// (crash-string: without the reference file:line: prefix).
//
// A fetched corpus holds <corpus>/expected.tsv (written by `corpus
// prepare`): "id TAB ref TAB lint" for every file under files/; `corpus run`
// and `corpus -lint` then compare digests instead of texts, and their
// reports name the differing files without a diff.

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// digest is the digest of s the expected results hold.
func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

var refCodes = map[string]string{"o": "ok", "c": "compile-error", "i": "compile-internal", "p": "parse-error"}

// refEntry encodes a reference result as an expected entry.
func refEntry(r Ref) string {
	switch r.Status {
	case "ok":
		return "o:" + digest(r.Tree) + ":" + digest(r.Lua)
	case "compile-error":
		return "c:" + digest(r.Tree) + ":" + digest(r.Err)
	case "compile-internal":
		return "i:" + digest(r.Tree) + ":" + digest(normInternal(r.Err))
	case "parse-error":
		return "p:" + digest(r.Err)
	case "":
		return "missing"
	}
	return r.Status
}

// parseRefEntry decodes an expected entry into a Ref holding digests.
func parseRefEntry(e string) (Ref, error) {
	f := strings.Split(e, ":")
	st, ok := refCodes[f[0]]
	if !ok {
		if len(f) != 1 || e == "" {
			return Ref{}, fmt.Errorf("bad reference entry %q", e)
		}
		return Ref{Status: e, Hashed: true}, nil
	}
	want := 3
	if f[0] == "p" {
		want = 2
	}
	if len(f) != want {
		return Ref{}, fmt.Errorf("bad reference entry %q", e)
	}
	r := Ref{Status: st, Hashed: true}
	if f[0] == "p" {
		r.Err = f[1]
		return r, nil
	}
	r.Tree, r.HasTree = f[1], true
	if f[0] == "o" {
		r.Lua, r.HasLua = f[2], true
	} else {
		r.Err = f[2]
	}
	return r, nil
}

var lintCodes = map[string]string{"c": "clean", "l": "lint", "p": "parse-error", "t": "crash-table", "s": "crash-string"}

// reLuaLocation is the chunk:line: prefix of a Lua error message.
var reLuaLocation = regexp.MustCompile(`^\S*:\d+: `)

// lintKey is the part of a lint result that has to match: ref is the
// reference side (its crash-string messages carry a Lua location).
func lintKey(o lintOut, ref bool) string {
	switch o.Status {
	case "lint":
		return o.Compact + "\x00" + o.Default
	case "crash-table":
		// the kind (user-error / compile-error) isn't kept by the port
		_, rest, _ := strings.Cut(o.Err, "\n")
		return rest
	case "crash-string":
		if ref {
			return reLuaLocation.ReplaceAllString(o.Err, "")
		}
		return o.Err
	}
	return o.Err
}

// lintEntry encodes a reference lint result as an expected entry.
func lintEntry(o lintOut) string {
	for code, st := range lintCodes {
		if st == o.Status {
			if code == "c" {
				return code
			}
			return code + ":" + digest(lintKey(o, true))
		}
	}
	if o.Status == "" {
		return "missing"
	}
	return o.Status
}

func parseLintEntry(e string) (lintOut, error) {
	code, d, hasDigest := strings.Cut(e, ":")
	st, ok := lintCodes[code]
	switch {
	case !ok && !hasDigest && e != "":
		return lintOut{Status: e, Hashed: true}, nil
	case !ok, hasDigest == (code == "c"):
		return lintOut{}, fmt.Errorf("bad lint entry %q", e)
	}
	return lintOut{Status: st, Digest: d, Hashed: true}, nil
}

// expectedFile is the expected results of a fetched corpus.
const expectedFile = "expected.tsv"

type expectedEntry struct{ ref, lint string }

var expected struct {
	once sync.Once
	m    map[string]expectedEntry // nil: not a fetched corpus
	err  error
}

// loadExpected returns the expected results of a fetched corpus (nil for a
// corpus with reference outputs).
func loadExpected(dir string) (map[string]expectedEntry, error) {
	expected.once.Do(func() {
		f, err := os.Open(filepath.Join(dir, expectedFile))
		if os.IsNotExist(err) {
			return
		} else if err != nil {
			expected.err = err
			return
		}
		defer func() { _ = f.Close() }()
		m := map[string]expectedEntry{}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			c := strings.Split(sc.Text(), "\t")
			if len(c) != 3 {
				expected.err = fmt.Errorf("%s: bad line %q", expectedFile, truncate(sc.Text(), 80))
				return
			}
			m[c[0]] = expectedEntry{c[1], c[2]}
		}
		expected.m, expected.err = m, sc.Err()
	})
	return expected.m, expected.err
}

// mustExpected is loadExpected for the places that cannot return errors;
// the file was already read without error by then (run, runLint).
func mustExpected(dir string) map[string]expectedEntry {
	m, err := loadExpected(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "corpus:", err)
		os.Exit(1)
	}
	return m
}

// refStatus is the reference status of a file: from ref/ or expected.tsv.
func refStatus(dir, id string) string {
	if m := mustExpected(dir); m != nil && !hasRefOutputs(dir, "ref", id) {
		r, err := parseRefEntry(m[id].ref)
		if err != nil {
			return "missing"
		}
		return r.Status
	}
	return readStatus(filepath.Join(dir, "ref"), id)
}

// hasRefOutputs reports whether the reference outputs of a file are in
// <dir>/<sub> (ref or ref-lint): in a fetched corpus, they take precedence
// over the digests (made by `corpus ref -filter ID` for a diff).
func hasRefOutputs(dir, sub, id string) bool {
	st := readStatus(filepath.Join(dir, sub), id)
	return st != "" && st != "pending"
}

// same reports whether got matches want, a text or (Hashed) its digest.
func (r Ref) same(want, got string) bool {
	if r.Hashed {
		return digest(got) == want
	}
	return got == want
}

// digestOnly is the report excerpt of a difference from an expected digest.
const digestOnly = "    (the expected results are digests: for a diff, make the reference outputs\n" +
	"    with `corpus ref [-lint] -filter ID` (Docker, see CONTRIBUTING.md), then -diff ID)"
