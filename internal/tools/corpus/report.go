package main

import (
	"cmp"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"
)

// summary counts outcomes; denominators come from the reference status so
// that fatal files count as failures.
type summary struct {
	files                                  int
	ref                                    map[string]int
	parse, compile, fatal                  map[string]int
	refParsed, refParseErr, refOK, refCErr int
	refCInternal, refOther, fullMatchFiles int
}

func summarize(res []Result) *summary {
	s := &summary{ref: map[string]int{}, parse: map[string]int{}, compile: map[string]int{}, fatal: map[string]int{}}
	for _, r := range res {
		s.files++
		s.ref[orStr(r.Ref, "(missing)")]++
		switch r.Ref {
		case "ok":
			s.refParsed++
			s.refOK++
		case "compile-error":
			s.refParsed++
			s.refCErr++
		case "compile-internal":
			s.refParsed++
			s.refCInternal++
		case "parse-error":
			s.refParseErr++
		default:
			s.refOther++
		}
		if r.Fatal != "" {
			s.fatal[r.Fatal]++
			continue
		}
		s.parse[r.Parse]++
		s.compile[r.Compile]++
		if (r.Parse == PTreeMatch || r.Parse == PErrMatch) &&
			(r.Compile == CLuaMatch || r.Compile == CErrMatch || r.Compile == CRefInternalMatch ||
				(r.Compile == CSkip && r.Parse == PErrMatch)) {
			s.fullMatchFiles++
		}
	}
	return s
}

func pct(n, d int) string {
	if d == 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f%%", 100*float64(n)/float64(d))
}

func (s *summary) text(filteredOut int, parseOnly bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "files: %d", s.files)
	if filteredOut > 0 {
		fmt.Fprintf(&b, " (%d filtered out)", filteredOut)
	}
	b.WriteString("\nreference:")
	for _, k := range sortedKeys(s.ref) {
		fmt.Fprintf(&b, " %s %d,", k, s.ref[k])
	}
	b.WriteString("\n\n")
	line := func(title string, denom int, m map[string]int, keys ...string) {
		fmt.Fprintf(&b, "%-32s", fmt.Sprintf("%s (%d):", title, denom))
		for i, k := range keys {
			if i > 0 {
				b.WriteString(" | ")
			}
			fmt.Fprintf(&b, "%s %d", k, m[k])
			if i == 0 {
				fmt.Fprintf(&b, " (%s)", pct(m[k], denom))
			}
		}
		b.WriteString("\n")
	}
	line("PARSE  ref parsed", s.refParsed, s.parse, PTreeMatch, PTreeMismatch, PGoRejects, PPanic)
	line("PARSE  ref parse error", s.refParseErr, s.parse, PErrMatch, PErrMismatch, PGoAccepts)
	if !parseOnly {
		line("COMPILE ref compiled", s.refOK, s.compile, CLuaMatch, CLuaMismatch, CGoError, CGoInternal, CPanic)
		line("COMPILE ref error", s.refCErr, s.compile, CErrMatch, CErrMismatch, CGoAccepts)
		line("COMPILE ref internal", s.refCInternal, s.compile, CRefInternalMatch, CRefInternalDiff)
	}
	fmt.Fprintf(&b, "%-32s%s %d | %s %d\n", "FATAL (worker died):", FCrash, s.fatal[FCrash], FTimeout, s.fatal[FTimeout])
	if !parseOnly {
		fmt.Fprintf(&b, "%-32s%d of %d (%s)\n", "FULL MATCH (all stages):", s.fullMatchFiles, s.files-s.refOther,
			pct(s.fullMatchFiles, s.files-s.refOther))
	}
	return b.String()
}

// bucket groups files with the same category and bucket key.
type bucket struct {
	stage, category, key string
	files                []*Occurrence
	results              []Result
}

var categoryOrder = []string{
	// parser first: it is supposed to be complete
	PTreeMismatch, PGoRejects, PPanic, PErrMismatch, PGoAccepts,
	FCrash, FTimeout,
	CPanic, CGoInternal, CGoError, CLuaMismatch, CErrMismatch, CGoAccepts, CRefInternalDiff,
}

func bucketize(res []Result, byID map[string]*Occurrence) map[string][]*bucket {
	idx := map[string]*bucket{}
	out := map[string][]*bucket{}
	add := func(stage, cat, key string, r Result) {
		k := cat + "\x00" + key
		b := idx[k]
		if b == nil {
			b = &bucket{stage: stage, category: cat, key: key}
			idx[k] = b
			out[cat] = append(out[cat], b)
		}
		b.files = append(b.files, byID[r.ID])
		b.results = append(b.results, r)
	}
	for _, r := range res {
		if r.Fatal != "" {
			add("fatal", r.Fatal, orStr(r.CompileBucket, r.Fatal), r)
			continue
		}
		switch r.Parse {
		case PTreeMismatch, PGoRejects, PPanic, PErrMismatch, PGoAccepts:
			add("parse", r.Parse, r.ParseBucket, r)
		}
		switch r.Compile {
		case CPanic, CGoInternal, CGoError, CLuaMismatch, CErrMismatch, CGoAccepts, CRefInternalDiff:
			key := r.CompileBucket
			add("compile", r.Compile, key, r)
		}
	}
	for _, bs := range out {
		sort.SliceStable(bs, func(i, j int) bool {
			return cmp.Or(cmp.Compare(len(bs[j].files), len(bs[i].files)), strings.Compare(bs[i].key, bs[j].key)) < 0
		})
		for _, b := range bs {
			// smallest files first: the easiest repros
			ord := make([]int, len(b.files))
			for i := range ord {
				ord[i] = i
			}
			sort.SliceStable(ord, func(i, j int) bool { return b.files[ord[i]].Bytes < b.files[ord[j]].Bytes })
			fs, rs := make([]*Occurrence, len(ord)), make([]Result, len(ord))
			for i, o := range ord {
				fs[i], rs[i] = b.files[o], b.results[o]
			}
			b.files, b.results = fs, rs
		}
	}
	return out
}

func catTotal(bs []*bucket) int {
	n := 0
	for _, b := range bs {
		n += len(b.files)
	}
	return n
}

func topBuckets(buckets map[string][]*bucket, top int) string {
	var b strings.Builder
	for _, cat := range categoryOrder {
		bs := buckets[cat]
		if len(bs) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n%s: %d files in %d buckets\n", cat, catTotal(bs), len(bs))
		for i, bk := range bs {
			if i == top {
				fmt.Fprintf(&b, "  ... %d more buckets\n", len(bs)-top)
				break
			}
			fmt.Fprintf(&b, "  %5d  %s\n         e.g. %s %s\n", len(bk.files), truncate(bk.key, 150), bk.files[0].ID, bk.files[0].Name())
		}
	}
	return b.String()
}

func writeReport(b *strings.Builder, buckets map[string][]*bucket, examples int) {
	b.WriteString("Buckets: files grouped by category and probable cause (first difference),\n" +
		"largest bucket first; examples are the smallest files of the bucket.\n" +
		"Full diff: corpus-bin -diff <id>\n")
	for _, cat := range categoryOrder {
		bs := buckets[cat]
		if len(bs) == 0 {
			continue
		}
		fmt.Fprintf(b, "\n%s\n== %s: %d files in %d buckets\n%s\n", strings.Repeat("=", 78), cat, catTotal(bs), len(bs), strings.Repeat("=", 78))
		for _, bk := range bs {
			fmt.Fprintf(b, "\n[%d] %s\n", len(bk.files), bk.key)
			for i, f := range bk.files {
				if i == examples {
					fmt.Fprintf(b, "  ... %d more\n", len(bk.files)-examples)
					break
				}
				dup := ""
				if f.Duplicates > 0 {
					dup = fmt.Sprintf(", +%d copies", f.Duplicates)
				}
				fmt.Fprintf(b, "  - %s %s (%d lines%s)\n", f.ID, f.Name(), f.Lines, dup)
			}
			r := bk.results[0]
			ex := r.CompileExcerpt
			if bk.stage == "parse" {
				ex = r.ParseExcerpt
			}
			if ex != "" {
				fmt.Fprintf(b, "  first example:\n%s\n", indent(ex))
			}
		}
	}
}

func appendHistory(path, group string, s *summary, elapsed time.Duration) error {
	_, err := os.Stat(path)
	newFile := err != nil
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	var b strings.Builder
	if newFile {
		fmt.Fprintln(&b, "time\tgroup\tfilter\tparse_only\tfiles\ttree_match\tref_parsed\tperr_match\tref_perr\tlua_match\tref_ok\tcerr_match\tref_cerr\tgo_panic\tgo_internal\tcrash\ttimeout\tfull_match\tseconds")
	}
	fmt.Fprintf(&b, "%s\t%s\t%s\t%v\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%.0f\n",
		time.Now().Format(time.RFC3339), group, *flagFilter, *flagParseOnly, s.files,
		s.parse[PTreeMatch], s.refParsed, s.parse[PErrMatch], s.refParseErr,
		s.compile[CLuaMatch], s.refOK, s.compile[CErrMatch], s.refCErr,
		s.parse[PPanic]+s.compile[CPanic], s.compile[CGoInternal], s.fatal[FCrash], s.fatal[FTimeout],
		s.fullMatchFiles, elapsed.Seconds())
	_, err = f.WriteString(b.String())
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
