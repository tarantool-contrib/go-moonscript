package main

import "testing"

func TestPathToTarget(t *testing.T) {
	cases := []struct {
		path, targetDir, baseDir string
		hasTarget, hasBase       bool
		want                     string
	}{
		{"ok.moon", "", "", false, false, "ok.lua"},
		{"file", "", "", false, false, "file.lua"},
		{"ok.moon", "outdir", "", true, false, "outdir/ok.lua"},
		{"d/a.moon", "out//", "d", true, true, "out/d/a.lua"},
		{"d/sub/b.moon", "out", "d/sub", true, true, "out/sub/b.lua"},
		{"d/sub/b.moon", "out", "d/sub/", true, true, "out/sub/b.lua"},
		{"/abs/x.moon", "out", "", true, false, "out/x.lua"},
	}
	for _, c := range cases {
		if got := pathToTarget(c.path, c.targetDir, c.baseDir, c.hasTarget, c.hasBase); got != c.want {
			t.Errorf("pathToTarget(%q, %q, %q) = %q, want %q", c.path, c.targetDir, c.baseDir, got, c.want)
		}
	}
}

func TestGetTip(t *testing.T) {
	cases := map[string]string{
		"-q":         "\nDid you mean '-'?",
		"--correlat": "\nDid you mean '--correlate'?",
		"--foo":      "",
		"--versio":   "\nDid you mean '--version'?",
	}
	for in, want := range cases {
		if got := getTip(in); got != want {
			t.Errorf("getTip(%q) = %q, want %q", in, got, want)
		}
	}
}
