package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/pprof"
	"strconv"
	"strings"
	"time"
)

var reDigits = regexp.MustCompile(`\d+`)

// normMsg normalizes an error message for bucketing.
func normMsg(s string) string {
	return truncate(reDigits.ReplaceAllString(s, "N"), 150)
}

// ---- tree diffs ----

// treeDiff compares two dump.tree outputs (one top-level statement per line).
// src is used to show the source line near the difference.
func treeDiff(want, got, src string) (bucket, excerpt string) {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	i := 0
	for i < len(wl) && i < len(gl) && wl[i] == gl[i] {
		i++
	}
	if i == len(wl) || i == len(gl) {
		return fmt.Sprintf("statement count: want %d, got %d", len(wl), len(gl)),
			fmt.Sprintf("    first %d statements equal; want %d, got %d", i, len(wl), len(gl))
	}
	w, g := wl[i], gl[i]
	k := 0
	for k < len(w) && k < len(g) && w[k] == g[k] {
		k++
	}
	typ := enclosingType(w, k)
	wt, gt := tokAt(w, k), tokAt(g, k)
	nw, ng := reDigits.ReplaceAllString(wt, "N"), reDigits.ReplaceAllString(gt, "N")
	if nw == ng {
		bucket = fmt.Sprintf("in %s: position differs", typ)
	} else {
		bucket = fmt.Sprintf("in %s: want %s | got %s", typ, nw, ng)
	}
	lo := max(k-70, 0)
	excerpt = fmt.Sprintf("    statement %d, column %d; %s\n    want: %s\n    got:  %s",
		i+1, k+1, sourceAt(src, lastPos(w, k)), clip(w, lo, k+70), clip(g, lo, k+70))
	return bucket, excerpt
}

func clip(s string, lo, hi int) string {
	pre, post := "", ""
	if lo > 0 {
		pre = "..."
	}
	if hi < len(s) {
		post = "..."
	} else {
		hi = len(s)
	}
	if lo > len(s) {
		lo = len(s)
	}
	return pre + s[lo:hi] + post
}

var rePosTag = regexp.MustCompile(`\[(\d+)\]`)

// lastPos is the last "[pos]" annotation before offset k (0 if none).
func lastPos(s string, k int) int {
	m := rePosTag.FindAllStringSubmatch(s[:min(k, len(s))], -1)
	if len(m) == 0 {
		return 0
	}
	n, _ := strconv.Atoi(m[len(m)-1][1])
	return n
}

// sourceAt describes the source line holding 1-based byte offset pos.
func sourceAt(src string, pos int) string {
	if pos <= 0 || pos > len(src) {
		return "source position unknown"
	}
	line := strings.Count(src[:pos-1], "\n") + 1
	lo := strings.LastIndexByte(src[:pos-1], '\n') + 1
	hi := strings.IndexByte(src[pos-1:], '\n')
	if hi < 0 {
		hi = len(src)
	} else {
		hi += pos - 1
	}
	return fmt.Sprintf("near source line %d: %s", line, truncate(strings.TrimSpace(src[lo:hi]), 100))
}

// enclosingType is the type of the innermost typed node open at offset k
// (brace counting ignores quoting, which is good enough for bucketing).
func enclosingType(s string, k int) string {
	var stack []int
	for i := 0; i < k && i < len(s); i++ {
		switch s[i] {
		case '{':
			stack = append(stack, i)
		case '}':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	list := false
	for j := len(stack) - 1; j >= 0; j-- {
		m := reNodeHead.FindStringSubmatch(s[stack[j]+1:])
		switch {
		case m == nil:
			list = true // an untyped list (names, args, body ...)
		case stack[j]+1+len(m[0]) > k:
			// the difference is in this node's own type: report the parent
		case list:
			return m[1] + "/list"
		default:
			return m[1]
		}
	}
	return "top"
}

var reNodeHead = regexp.MustCompile(`^(?:\[\d+\] )?"([A-Za-z_]+)"`)

func isTokChar(c byte) bool {
	return c == '_' || c == '"' || c == '[' || c == ']' || c == '.' || c == '-' ||
		(c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// tokAt returns the token around offset k.
func tokAt(s string, k int) string {
	if k >= len(s) {
		return "<end>"
	}
	lo, hi := k, k
	for lo > 0 && isTokChar(s[lo-1]) {
		lo--
	}
	for hi < len(s) && isTokChar(s[hi]) {
		hi++
	}
	if lo == hi {
		return strconv.Quote(s[k : k+1])
	}
	return truncate(s[lo:hi], 40)
}

// prettyTree puts every node of a tree dump on its own line, for -diff.
func prettyTree(s string) string {
	var b strings.Builder
	depth := 0
	inStr := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			inStr = !inStr
		case inStr:
		case c == '\n':
			depth = 0
			b.WriteByte('\n')
			continue
		case c == '{':
			if i > 0 && s[i-1] != '\n' {
				b.WriteString("\n" + strings.Repeat("  ", depth))
			}
			depth++
		case c == '}':
			depth--
		case c == ' ' && i > 0 && s[i-1] == ',' && i+1 < len(s) && s[i+1] == '{':
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// ---- Lua diffs ----

var reLuaTok = regexp.MustCompile(`"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|\[=*\[|0[xX][0-9a-fA-F]+|\d+(?:\.\d+)?(?:[eE][-+]?\d+)?|[A-Za-z_][A-Za-z0-9_]*|\.\.\.|\.\.|==|~=|<=|>=|\S`)

var luaKeep = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`and break do else elseif end false for function goto if in
		local nil not or repeat return then true until while self setmetatable getmetatable
		rawget rawset ipairs pairs unpack select type error tostring next table insert concat`) {
		luaKeep[w] = true
	}
}

// normLua abstracts a Lua line for bucketing: literals and user names become
// placeholders, keywords and generated names (_base_0 ...) stay.
func normLua(line string) string {
	toks := reLuaTok.FindAllString(line, -1)
	for i, t := range toks {
		c := t[0]
		switch {
		case c == '"' || c == '\'' || (c == '[' && len(t) > 1):
			toks[i] = "S"
		case c >= '0' && c <= '9':
			toks[i] = "N"
		case c == '_':
			toks[i] = reDigits.ReplaceAllString(t, "N")
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
			if !luaKeep[t] {
				toks[i] = "x"
			}
		}
	}
	return truncate(strings.Join(toks, " "), 120)
}

// luaDiff compares two Lua outputs line by line.
func luaDiff(want, got string) (bucket, excerpt string) {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	i := 0
	for i < len(wl) && i < len(gl) && wl[i] == gl[i] {
		i++
	}
	w, g := "<EOF>", "<EOF>"
	if i < len(wl) {
		w = wl[i]
	}
	if i < len(gl) {
		g = gl[i]
	}
	switch {
	case strings.TrimSpace(w) == strings.TrimSpace(g):
		bucket = "indentation differs: " + normLua(w)
	case strings.Join(strings.Fields(w), "") == strings.Join(strings.Fields(g), ""):
		bucket = "spacing differs: " + normLua(w)
	case w != "<EOF>" && g != "<EOF>" && normLua(w) == normLua(g):
		bucket = "names/literals differ: " + normLua(w)
	default:
		nw, ng := w, g
		if w != "<EOF>" {
			nw = normLua(w)
		}
		if g != "<EOF>" {
			ng = normLua(g)
		}
		bucket = "want `" + nw + "` | got `" + ng + "`"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "    line %d:\n", i+1)
	ctx := func(label string, ls []string) {
		for j := max(i-2, 0); j < min(i+3, len(ls)); j++ {
			mark := " "
			if j == i {
				mark = ">"
			}
			fmt.Fprintf(&b, "    %s %s %4d| %s\n", label, mark, j+1, truncate(ls[j], 160))
		}
		if i >= len(ls) {
			fmt.Fprintf(&b, "    %s > <EOF>\n", label)
		}
	}
	ctx("want", wl)
	ctx("got ", gl)
	return bucket, strings.TrimRight(b.String(), "\n")
}

// ---- error diffs ----

func errDiff(want, got string) (bucket, excerpt string) {
	nw, ng := normMsg(firstLine(want)), normMsg(firstLine(got))
	if nw == ng {
		bucket = "same message, position/context differs: " + nw
	} else {
		bucket = "want `" + nw + "` | got `" + ng + "`"
	}
	excerpt = "    want:\n" + indent(firstLines(want, 6)) + "\n    got:\n" + indent(firstLines(got, 6))
	return bucket, excerpt
}

// ---- -diff ----

func showDiff(dir, sel string) error {
	files, err := readManifest(dir)
	if err != nil {
		return err
	}
	var hits []*Occurrence
	for _, f := range files {
		if f.ID == sel {
			hits = []*Occurrence{f}
			break
		}
		if f.matches(sel) {
			hits = append(hits, f)
		}
	}
	if len(hits) != 1 {
		for i, h := range hits {
			if i == 20 {
				fmt.Printf("... and %d more\n", len(hits)-20)
				break
			}
			fmt.Printf("%s  %s\n", h.ID, h.Name())
		}
		return fmt.Errorf("-diff %q matches %d files, need exactly one", sel, len(hits))
	}
	f := hits[0]
	srcPath := filepath.Join(dir, "files", f.ID+".moon")
	src, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}
	ref := loadRef(dir, f.ID)
	if *flagCPUProfile != "" {
		pf, err := os.Create(*flagCPUProfile)
		if err != nil {
			return err
		}
		if err := pprof.StartCPUProfile(pf); err != nil {
			return err
		}
		defer func() {
			pprof.StopCPUProfile()
			if err := pf.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "cpu profile: %v\n", err)
				return
			}
			fmt.Printf("cpu profile: %s (go tool pprof -top %s)\n", *flagCPUProfile, *flagCPUProfile)
		}()
	}
	res, ok := checkTimeout(f.ID, string(src), ref, *flagParseOnly, *flagTimeout)
	if !ok {
		return fmt.Errorf("%s: timed out after %s", f.ID, *flagTimeout)
	}
	fmt.Printf("file:    %s (%d lines)\nid:      %s\nsource:  %s\nref:     %s\nparse:   %s\ncompile: %s\ntime:    %d ms (parse %d ms)\n",
		f.Name(), f.Lines, f.ID, srcPath, ref.Status, res.Parse, res.Compile, res.Ms, res.ParseMs)
	if res.ParseBucket != "" {
		fmt.Printf("parse bucket:   %s\n", res.ParseBucket)
	}
	if res.CompileBucket != "" {
		fmt.Printf("compile bucket: %s\n", res.CompileBucket)
	}
	if ref.Hashed {
		fmt.Printf("\n%s\n", digestOnly)
		return nil
	}

	tmp, err := os.MkdirTemp(filepath.Join(dir), "tmp-diff-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	udiff := func(title, want, got string) error {
		fmt.Printf("\n===== %s (--- reference, +++ go)\n", title)
		wp, gp := filepath.Join(tmp, "reference"), filepath.Join(tmp, "go")
		if err := os.WriteFile(wp, []byte(want+"\n"), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(gp, []byte(got+"\n"), 0o644); err != nil {
			return err
		}
		// diff exits with status 1 when the files differ: only its output matters
		out, _ := exec.Command("diff", "-u", "-L", "reference", "-L", "go", wp, gp).CombinedOutput()
		_, err := os.Stdout.Write(out)
		return err
	}
	switch res.Parse {
	case PTreeMismatch:
		err = udiff("tree (one node per line)", prettyTree(res.want.tree), prettyTree(res.got.tree))
	case PErrMismatch, PGoRejects, PGoAccepts:
		err = udiff("parse result", orStr(res.want.perr, res.want.tree), orStr(res.got.perr, res.got.tree))
	case PPanic:
		fmt.Printf("\n===== parse panic\n%s\n", res.ParseExcerpt)
	}
	if err != nil {
		return err
	}
	switch res.Compile {
	case CLuaMismatch:
		err = udiff("lua", res.want.lua, res.got.lua)
	case CGoError, CGoAccepts, CErrMismatch, CRefInternalDiff:
		err = udiff("compile result", orStr(res.want.cerr, res.want.lua), orStr(res.got.cerr, res.got.lua))
	case CPanic, CGoInternal:
		fmt.Printf("\n===== compile panic\n%s\n", res.got.cerr)
	}
	return err
}

// checkTimeout runs check in-process with a timeout (a timed-out check
// keeps running in its goroutine).
func checkTimeout(id, src string, ref Ref, parseOnly bool, timeout time.Duration) (Result, bool) {
	ch := make(chan Result, 1)
	go func() { ch <- check(id, src, ref, parseOnly) }()
	select {
	case r := <-ch:
		return r, true
	case <-time.After(timeout):
		return Result{ID: id, Ref: ref.Status, Parse: PNoRef, Compile: CSkip, Fatal: FTimeout}, false
	}
}
