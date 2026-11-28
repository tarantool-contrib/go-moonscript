package luatest

// This file exposes unexported parts of the harness to the external
// luatest_test test package (the "export_test.go" pattern). It is only
// compiled when running tests and does not change the package's API.

import "context"

// DiscoveryForTest is what discover found, with the fields exported.
type DiscoveryForTest struct {
	Interps  []Interpreter
	Skip     string
	Problems []string
}

// DiscoverForTest is discover.
func DiscoverForTest(
	required string, set bool,
	lookPath, resolve func(name string) (string, error),
	probe func(interp Interpreter) (string, error),
) DiscoveryForTest {
	found := discover(required, set, lookPath, resolve, probe)

	return DiscoveryForTest{Interps: found.interps, Skip: found.skip, Problems: found.problems}
}

// ProbeForTest is probe, which discovery runs on every interpreter it finds.
func ProbeForTest(interp Interpreter) (string, error) {
	return probe(interp)
}

// ProjectFileForTest is projectFile.
func ProjectFileForTest(dir, rel string) (string, error) {
	return projectFile(dir, rel)
}

// RunForTest is run: unlike Run, it returns a timeout as an error instead of
// failing the test.
func RunForTest(
	ctx context.Context, interp Interpreter, dir string, opts RunOptions, script string, args []string,
) (Result, error) {
	return run(ctx, interp, dir, opts, script, args)
}
