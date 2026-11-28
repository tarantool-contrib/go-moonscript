package lua

import (
	"os"
	"syscall"
	"testing"
)

// BenchmarkVM runs a CPU-bound script (testdata/bench.lua) with no hook set,
// to check that hook support costs nothing when hooks are off. Besides the
// wall time it reports the process CPU time per run (cpu-ns/op), which a
// busy machine disturbs less.
func BenchmarkVM(b *testing.B) {
	src, err := os.ReadFile("testdata/bench.lua")
	if err != nil {
		b.Fatal(err)
	}
	// the options moonscript/runtime uses
	L := NewState(Options{
		CallStackSize:       20000,
		MinimizeStackMemory: true,
		RegistrySize:        RegistrySize,
		RegistryMaxSize:     1 << 22,
		RegistryGrowStep:    1024,
	})
	defer L.Close()
	fn, err := L.LoadString(string(src))
	if err != nil {
		b.Fatal(err)
	}
	cpu := func() int64 {
		var ru syscall.Rusage
		if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
			b.Fatal(err)
		}
		return ru.Utime.Nano() + ru.Stime.Nano()
	}
	b.ResetTimer()
	start := cpu()
	for i := 0; i < b.N; i++ {
		L.Push(fn)
		if err := L.PCall(0, 1, nil); err != nil {
			b.Fatal(err)
		}
		L.Pop(1)
	}
	b.ReportMetric(float64(cpu()-start)/float64(b.N), "cpu-ns/op")
}
