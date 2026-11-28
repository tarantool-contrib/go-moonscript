//go:build unix

package luatest_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/luatest"
)

// TestProbe_RefusesBrokenInterpreters checks that an interpreter which cannot
// run the probe script, because it fails or because it reports no version, is
// refused rather than tested on: every run of it would fail alike, for the
// original and the bundle.
func TestProbe_RefusesBrokenInterpreters(t *testing.T) {
	t.Parallel()

	for name, script := range map[string]string{
		"fails":  "#!/bin/sh\necho broken >&2\nexit 1\n",
		"silent": "#!/bin/sh\nexit 0\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "fake-lua")
			// #nosec G306 -- the fake interpreter must be executable.
			require.NoError(t, os.WriteFile(path, []byte(script), 0o700))

			_, err := luatest.ProbeForTest(luatest.Interpreter{Name: "fake-lua", Path: path, Version: ""})

			require.ErrorContains(t, err, "the probe script did not report a version")
		})
	}
}
