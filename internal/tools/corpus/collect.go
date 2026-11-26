package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Occurrence is one place a corpus file was found.
type Occurrence struct {
	ID, SHA       string
	Bytes, Lines  int
	Segment       string // see segments; "mutants" for derived files
	Repo, Commit  string
	Path          string // path inside the repository
	Duplicates    int    // other occurrences of the same content (first one only)
	duplicateRefs []string
}

// Name is the human readable file reference.
func (o *Occurrence) Name() string { return o.Repo + ":" + o.Path }

const manifestHeader = "id\tsha256\tbytes\tlines\tsegment\trepo\tcommit\tpath"

// segments are the corpus parts: clones under <corpus>/<dir>/OWNER__NAME
// (made by fetch.sh) and the file extension collected from them. Summaries
// are printed per segment.
var segments = []struct{ name, dir, ext string }{
	// real-world MoonScript code
	{"moonscript", "repos", ".moon"},
	// Tarantool Lua code of github.com/moonlibs, fed to both compilers as
	// if it were MoonScript: mostly parse-error message/position cases
	{"moonlibs-lua", "repos-moonlibs", ".lua"},
}

// segmentMutants holds the derived error cases of the moonscript segment.
const segmentMutants = "mutants"

// segmentOrder is the order of the per-segment summaries.
var segmentOrder = []string{"moonscript", segmentMutants, "moonlibs-lua"}

// collect walks every segment's clones, stores each distinct content once
// as files/<id>.moon, adds the mutants and writes manifest.tsv.
func collect(dir string, maxBytes int64) error {
	filesDir := filepath.Join(dir, "files")
	if err := os.MkdirAll(filesDir, 0o755); err != nil {
		return err
	}
	var occs []Occurrence
	seen := map[string]bool{}
	var stats strings.Builder
	addStats := func(name, label string, from, nrepos, withFiles, skipped int) {
		var totalBytes, totalLines, uniqBytes, uniqLines, nuniq int
		uniq := map[string]bool{}
		for _, o := range occs[from:] {
			totalBytes += o.Bytes
			totalLines += o.Lines
			if !uniq[o.ID] {
				uniq[o.ID] = true
				if o.Segment == name { // not counted when an earlier segment had it
					nuniq++
					uniqBytes += o.Bytes
					uniqLines += o.Lines
				}
			}
		}
		fmt.Fprintf(&stats, "segment %s%s:\n", name, label)
		if nrepos >= 0 {
			fmt.Fprintf(&stats, "  repos cloned: %d (with files: %d), skipped files (> %d bytes): %d\n"+
				"  occurrences: %d files, %s, %d lines\n", nrepos, withFiles, maxBytes, skipped,
				len(occs)-from, human(totalBytes), totalLines)
		}
		fmt.Fprintf(&stats, "  distinct:    %d files, %s, %d lines\n", nuniq, human(uniqBytes), uniqLines)
	}

	var realStart, realEnd int
	for _, seg := range segments {
		repoDirs, err := filepath.Glob(filepath.Join(dir, seg.dir, "*"))
		if err != nil {
			return err
		}
		sort.Strings(repoDirs)
		from := len(occs)
		var nrepos, skipped int
		reposWithFiles := map[string]bool{}
		for _, rd := range repoDirs {
			commit, err := os.ReadFile(filepath.Join(rd, ".git", "DONE"))
			if err != nil {
				continue // incomplete clone
			}
			nrepos++
			repo := strings.Replace(filepath.Base(rd), "__", "/", 1)
			err = filepath.WalkDir(rd, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if d.IsDir() {
					if d.Name() == ".git" {
						return filepath.SkipDir
					}
					return nil
				}
				// only regular files: never follow symlinks out of the clone
				if !d.Type().IsRegular() || !strings.HasSuffix(d.Name(), seg.ext) {
					return nil
				}
				info, err := d.Info()
				if err != nil || info.Size() > maxBytes {
					skipped++
					return nil
				}
				data, err := os.ReadFile(path)
				if err != nil {
					return nil
				}
				sum := sha256.Sum256(data)
				sha := hex.EncodeToString(sum[:])
				id := sha[:16]
				rel, _ := filepath.Rel(rd, path)
				occs = append(occs, Occurrence{
					ID: id, SHA: sha, Bytes: len(data), Lines: countLines(data), Segment: seg.name,
					Repo: repo, Commit: strings.TrimSpace(string(commit)), Path: filepath.ToSlash(rel),
				})
				reposWithFiles[repo] = true
				if !seen[id] {
					seen[id] = true
					dst := filepath.Join(filesDir, id+".moon")
					if _, err := os.Stat(dst); err != nil {
						if err := os.WriteFile(dst, data, 0o644); err != nil {
							return err
						}
					}
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		if seg.name == "moonscript" {
			realStart, realEnd = from, len(occs)
		}
		addStats(seg.name, "", from, nrepos, len(reposWithFiles), skipped)
	}

	// derived error cases of the MoonScript files (see mutate.go)
	from := len(occs)
	if *flagMutants > 0 {
		done := map[string]bool{}
		for _, o := range occs[realStart:realEnd] {
			if done[o.ID] || o.Lines > 3000 {
				continue
			}
			done[o.ID] = true
			data, err := os.ReadFile(filepath.Join(filesDir, o.ID+".moon"))
			if err != nil {
				return err
			}
			for _, m := range makeMutants(o.ID, data, *flagMutants) {
				sum := sha256.Sum256(m.data)
				sha := hex.EncodeToString(sum[:])
				id := sha[:16]
				if seen[id] {
					continue
				}
				seen[id] = true
				if err := os.WriteFile(filepath.Join(filesDir, id+".moon"), m.data, 0o644); err != nil {
					return err
				}
				occs = append(occs, Occurrence{
					ID: id, SHA: sha, Bytes: len(m.data), Lines: countLines(m.data), Segment: segmentMutants,
					Repo: mutantPrefix + m.kind, Commit: o.ID, Path: o.Repo + "/" + o.Path + m.detail,
				})
			}
		}
	}
	addStats(segmentMutants, fmt.Sprintf(" (-mutants %d per moonscript file)", *flagMutants), from, -1, 0, 0)

	// drop stale files of earlier collections
	old, _ := filepath.Glob(filepath.Join(filesDir, "*.moon"))
	for _, f := range old {
		if !seen[strings.TrimSuffix(filepath.Base(f), ".moon")] {
			if err := os.Remove(f); err != nil {
				return err
			}
		}
	}

	var b bytes.Buffer
	b.WriteString(manifestHeader + "\n")
	for _, o := range occs {
		fmt.Fprintf(&b, "%s\t%s\t%d\t%d\t%s\t%s\t%s\t%s\n", o.ID, o.SHA, o.Bytes, o.Lines, o.Segment, o.Repo, o.Commit, o.Path)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.tsv"), b.Bytes(), 0o644); err != nil {
		return err
	}
	fmt.Print(stats.String())
	return os.WriteFile(filepath.Join(dir, "stats.txt"), []byte(stats.String()), 0o644)
}

func countLines(data []byte) int {
	n := bytes.Count(data, []byte{'\n'})
	if len(data) > 0 && data[len(data)-1] != '\n' {
		n++
	}
	return n
}

func human(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// readManifest returns the distinct files (first occurrence of each id, in
// manifest order) and all occurrences by id.
func readManifest(dir string) ([]*Occurrence, error) {
	f, err := os.Open(filepath.Join(dir, "manifest.tsv"))
	if err != nil {
		return nil, fmt.Errorf("%w (run `corpus-bin collect` first)", err)
	}
	defer func() { _ = f.Close() }()
	var files []*Occurrence
	byID := map[string]*Occurrence{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == manifestHeader || line == "" {
			continue
		}
		c := strings.SplitN(line, "\t", 8)
		if len(c) != 8 {
			return nil, fmt.Errorf("manifest.tsv: bad line %q (re-run collect)", truncate(line, 80))
		}
		nb, _ := strconv.Atoi(c[2])
		nl, _ := strconv.Atoi(c[3])
		if first := byID[c[0]]; first != nil {
			first.Duplicates++
			first.duplicateRefs = append(first.duplicateRefs, c[5]+":"+c[7])
			continue
		}
		o := &Occurrence{ID: c[0], SHA: c[1], Bytes: nb, Lines: nl, Segment: c[4], Repo: c[5], Commit: c[6], Path: c[7]}
		byID[o.ID] = o
		files = append(files, o)
	}
	return files, sc.Err()
}

// matches reports whether filter selects the file (id, repo/path, or any
// duplicate's repo/path).
func (o *Occurrence) matches(filter string) bool {
	if *flagNoMutants && o.Segment == segmentMutants {
		return false
	}
	if *flagSegment != "" && !slices.Contains(strings.Split(*flagSegment, ","), o.Segment) {
		return false
	}
	if filter == "" || strings.Contains(o.ID, filter) || strings.Contains(o.Repo+"/"+o.Path, filter) {
		return true
	}
	for _, d := range o.duplicateRefs {
		if strings.Contains(strings.Replace(d, ":", "/", 1), filter) {
			return true
		}
	}
	return false
}
