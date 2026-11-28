package main

import (
	"runtime/debug"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildInfo is the build information of a binary built from module version
// with the given build settings.
func buildInfo(version string, settings ...debug.BuildSetting) *debug.BuildInfo {
	return &debug.BuildInfo{
		GoVersion: "go1.27.1",
		Path:      "github.com/tarantool-contrib/go-moonscript/luamerge/cmd/luamerge",
		Main: debug.Module{
			Path: "github.com/tarantool-contrib/go-moonscript", Version: version, Sum: "", Replace: nil,
		},
		Deps:     nil,
		Settings: settings,
	}
}

// TestVersionLine checks what --version prints. GoReleaser sets version,
// commit and date with -ldflags; a binary built with go install or go build
// has only what the toolchain recorded in it, and used to print
// "version dev (commit unknown, built on unknown)" although the toolchain
// records the module version, and in a git checkout the commit.
func TestVersionLine(t *testing.T) {
	t.Parallel()

	revision := debug.BuildSetting{Key: "vcs.revision", Value: "5f4b58c9fee0a1b2c3d4e5f60718293a4b5c6d7e"}
	commitTime := debug.BuildSetting{Key: "vcs.time", Value: "2026-06-26T22:06:39Z"}
	checkout := buildInfo("v0.0.0-20260626220639-5f4b58c9fee0+dirty", revision, commitTime,
		debug.BuildSetting{Key: "vcs.modified", Value: "true"})

	tests := []struct {
		name                  string
		version, commit, date string // Set with -ldflags.
		info                  *debug.BuildInfo
		want                  string
	}{
		{"goreleaser", "1.2.3", "5f4b58c", "2026-09-28T10:00:00Z", checkout,
			"luamerge version 1.2.3 (commit 5f4b58c, built on 2026-09-28T10:00:00Z)"},
		{"go install of a release", "", "", "", buildInfo("v1.2.3"),
			"luamerge version v1.2.3"},
		{"go install of a commit", "", "", "", buildInfo("v0.2.2-0.20260626220639-5f4b58c9fee0"),
			"luamerge version v0.2.2-0.20260626220639-5f4b58c9fee0"},
		{"go build in a checkout", "", "", "", checkout,
			"luamerge version v0.0.0-20260626220639-5f4b58c9fee0+dirty " +
				"(commit 5f4b58c9fee0a1b2c3d4e5f60718293a4b5c6d7e, committed on 2026-06-26T22:06:39Z)"},
		{"go build without VCS stamping", "", "", "", buildInfo("(devel)"),
			"luamerge version dev"},
		{"no build information", "", "", "", nil,
			"luamerge version dev"},
		{"only the version set", "1.2.3", "", "", checkout,
			"luamerge version 1.2.3 (commit 5f4b58c9fee0a1b2c3d4e5f60718293a4b5c6d7e, " +
				"committed on 2026-06-26T22:06:39Z)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, versionLine(tt.version, tt.commit, tt.date, tt.info))
		})
	}
}

// TestCLI_Version checks that --version prints one line and exits 0. The
// binary the tests build carries no -ldflags, so this is the build
// information path.
func TestCLI_Version(t *testing.T) {
	t.Parallel()

	stdout, stderr, code := runCLI(t, "--version")

	require.Equal(t, 0, code, stderr)
	assert.Empty(t, stderr)
	assert.True(t, strings.HasPrefix(stdout, "luamerge version "), "%q", stdout)
	assert.Equal(t, 1, strings.Count(stdout, "\n"), "%q", stdout)
	assert.NotContains(t, stdout, "unknown")
}
