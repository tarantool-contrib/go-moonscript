package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"strings"
)

// report is the machine-readable result of a run (-json, -compare).
type report struct {
	Version     int               `json:"version"`
	Date        string            `json:"date"`
	Revision    string            `json:"revision,omitempty"` // git describe of the checkout
	SuiteCommit string            `json:"suite_commit"`
	Go          string            `json:"go"`
	GOMAXPROCS  int               `json:"gomaxprocs"`
	NumCPU      int               `json:"num_cpu"`
	Reps        int               `json:"reps"`
	Images      map[string]string `json:"images"`
	Engines     []string          `json:"engines"`
	LoadStart   float64           `json:"load_start"`
	LoadEnd     float64           `json:"load_end"`
	RoundLoad   []float64         `json:"round_load"` // 1-minute load average at the start of each round
	Benchmarks  []*benchResult    `json:"benchmarks"`
	Skipped     []skipJSON        `json:"skipped"`
}

type skipJSON struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type benchResult struct {
	Name    string                   `json:"name"`
	Param   string                   `json:"param"`
	Input   string                   `json:"input,omitempty"`
	Engines map[string]*engineResult `json:"engines"`
}

type engineResult struct {
	Status string    `json:"status"`           // "ok" or what went wrong
	Times  []float64 `json:"times"`            // CPU seconds of each run
	Wall   []float64 `json:"wall"`             // wall seconds of each process
	Median float64   `json:"median"`           // of Times
	Min    float64   `json:"min"`              // of Times
	MD5    string    `json:"md5"`              // of the output
	Verify string    `json:"verify,omitempty"` // TEST_md5sum.txt check
}

func readReport(path string) (*report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r report
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &r, nil
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

func minimum(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	return slices.Min(xs)
}

var titles = map[string]string{"vm": "ours", "puc": "PUC 5.1", "luajit-joff": "LJ -joff", "luajit": "LuaJIT"}

// cell is an engine's median, or why there is none.
func cell(er *engineResult) string {
	if er == nil {
		return ""
	}
	if er.Status != "ok" {
		if strings.HasPrefix(er.Status, "wrong") {
			return "WRONG"
		}
		return "FAIL"
	}
	return fmt.Sprintf("%.3f", er.Median)
}

func ratio(br *benchResult, a, b string) (float64, bool) {
	x, y := br.Engines[a], br.Engines[b]
	if x == nil || y == nil || x.Status != "ok" || y.Status != "ok" || y.Median <= 0 {
		return 0, false
	}
	return x.Median / y.Median, true
}

// printTable writes the results as a markdown table: the median CPU
// seconds per engine and our ratios versus PUC Lua and LuaJIT -joff, then
// the failures and the TEST_md5sum checks.
func printTable(w io.Writer, rep *report) {
	_, _ = fmt.Fprintf(w, "Median CPU seconds of %d runs (load %.2f at start, %.2f at end; rounds: %s)\n\n",
		rep.Reps, rep.LoadStart, rep.LoadEnd, fmtLoads(rep.RoundLoad))
	hdr := []string{"benchmark", "param"}
	for _, e := range rep.Engines {
		hdr = append(hdr, titles[e])
	}
	hasVM := slices.Contains(rep.Engines, "vm")
	ratios := []string{}
	for _, e := range []string{"puc", "luajit-joff", "luajit"} {
		if hasVM && slices.Contains(rep.Engines, e) {
			ratios = append(ratios, e)
			hdr = append(hdr, "ours/"+titles[e])
		}
	}
	_, _ = fmt.Fprintf(w, "| %s |\n|%s\n", strings.Join(hdr, " | "), strings.Repeat("---|", len(hdr)))
	logs := map[string]map[bool][]float64{}
	for _, br := range rep.Benchmarks {
		row := []string{br.Name, br.Param}
		for _, e := range rep.Engines {
			row = append(row, cell(br.Engines[e]))
		}
		for _, e := range ratios {
			if r, ok := ratio(br, "vm", e); ok && br.Name != "empty" {
				row = append(row, fmt.Sprintf("%.2f", r))
				if logs[e] == nil {
					logs[e] = map[bool][]float64{}
				}
				moon := strings.HasPrefix(br.Name, "moon/")
				logs[e][moon] = append(logs[e][moon], math.Log(r))
			} else {
				row = append(row, "")
			}
		}
		_, _ = fmt.Fprintf(w, "| %s |\n", strings.Join(row, " | "))
	}
	for _, moon := range []bool{false, true} {
		label := "geomean (LuaJIT suite)"
		if moon {
			label = "geomean (MoonScript)"
		}
		row := []string{"**" + label + "**", ""}
		for range rep.Engines {
			row = append(row, "")
		}
		any := false
		for _, e := range ratios {
			if l := logs[e][moon]; len(l) > 0 {
				any = true
				s := 0.0
				for _, x := range l {
					s += x
				}
				row = append(row, fmt.Sprintf("**%.2f**", math.Exp(s/float64(len(l)))))
			} else {
				row = append(row, "")
			}
		}
		if any {
			_, _ = fmt.Fprintf(w, "| %s |\n", strings.Join(row, " | "))
		}
	}
	var notes []string
	for _, br := range rep.Benchmarks {
		for _, e := range rep.Engines {
			er := br.Engines[e]
			if er.Status != "ok" {
				notes = append(notes, fmt.Sprintf("- %s on %s: %s", br.Name, titles[e], er.Status))
			}
		}
	}
	if len(notes) > 0 {
		_, _ = fmt.Fprintf(w, "\nFailures:\n%s\n", strings.Join(notes, "\n"))
	}
	var checks []string
	for _, br := range rep.Benchmarks {
		var cs []string
		for _, e := range rep.Engines {
			if v := br.Engines[e].Verify; v != "" {
				cs = append(cs, titles[e]+": "+v)
			}
		}
		if len(cs) > 0 {
			checks = append(checks, fmt.Sprintf("- %s: %s", br.Name, strings.Join(cs, ", ")))
		}
	}
	if len(checks) > 0 {
		_, _ = fmt.Fprintf(w, "\nTEST_md5sum.txt checks:\n%s\n", strings.Join(checks, "\n"))
	}
}

func fmtLoads(ls []float64) string {
	var s []string
	for _, l := range ls {
		s = append(s, fmt.Sprintf("%.2f", l))
	}
	return strings.Join(s, " ")
}

// printCompare writes the medians of an earlier run next to this run's,
// for the benchmarks and engines both have.
func printCompare(w io.Writer, old, cur *report, oldPath string) {
	_, _ = fmt.Fprintf(w, "\nCompared with %s (%s, %s):\n\n", oldPath, old.Date, old.Revision)
	_, _ = fmt.Fprintln(w, "| benchmark | engine | old | new | new/old |\n|---|---|---|---|---|")
	oldBy := map[string]*benchResult{}
	for _, br := range old.Benchmarks {
		oldBy[br.Name] = br
	}
	for _, br := range cur.Benchmarks {
		ob := oldBy[br.Name]
		if ob == nil {
			continue
		}
		for _, e := range cur.Engines {
			n, o := br.Engines[e], ob.Engines[e]
			if n == nil || o == nil {
				continue
			}
			name := br.Name
			if ob.Param != br.Param {
				name += fmt.Sprintf(" (param %s, was %s)", br.Param, ob.Param)
			}
			r := ""
			if n.Status == "ok" && o.Status == "ok" && o.Median >= 0.01 && ob.Param == br.Param { // no ratios of noise
				r = fmt.Sprintf("%.2f", n.Median/o.Median)
			}
			_, _ = fmt.Fprintf(w, "| %s | %s | %s | %s | %s |\n", name, titles[e], cell(o), cell(n), r)
		}
	}
}
