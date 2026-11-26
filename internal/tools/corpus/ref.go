package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed refbatch.lua
var refbatchLua []byte

// genRef runs the reference compiler over every corpus file that has no
// reference result yet (all files with -force): a few long-lived containers,
// each processing a shard of the file list with refbatch.lua.
func genRef(dir string) error {
	files, err := readManifest(dir)
	if err != nil {
		return err
	}
	refDir := filepath.Join(dir, "ref")
	scriptDir := filepath.Join(dir, "scripts")
	for _, d := range []string{refDir, scriptDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(scriptDir, "refbatch.lua"), refbatchLua, 0o644); err != nil {
		return err
	}

	for round := 1; ; round++ {
		var todo []string
		for _, f := range files {
			if !f.matches(*flagFilter) {
				continue
			}
			st := readStatus(refDir, f.ID)
			if st == "" || st == "pending" || (*flagForce && round == 1) {
				todo = append(todo, f.ID)
			}
		}
		if len(todo) == 0 {
			break
		}
		fmt.Printf("ref round %d: %d files, %d containers\n", round, len(todo), *flagRefJobs)
		start := time.Now()
		timedOut, failed := runRefShards(dir, todo)
		fmt.Printf("ref round %d done in %s\n", round, time.Since(start).Round(time.Second))
		if !timedOut && !failed {
			break
		}
		// a container was killed or died: the file it was working on is
		// "pending"; record it and resume with the remaining files
		status := "ref-crash"
		if timedOut {
			status = "ref-timeout"
		}
		marked := 0
		for _, id := range todo {
			if readStatus(refDir, id) == "pending" {
				fmt.Printf("ref: %s -> %s\n", id, status)
				if err := os.WriteFile(filepath.Join(refDir, id+".status"), []byte(status+"\n"), 0o644); err != nil {
					return err
				}
				marked++
			}
		}
		if marked == 0 {
			return fmt.Errorf("reference containers failed without a pending file")
		}
	}

	counts := map[string]int{}
	for _, f := range files {
		counts[readStatus(refDir, f.ID)]++
	}
	fmt.Println("reference results:")
	for _, k := range sortedKeys(counts) {
		name := k
		if name == "" {
			name = "(missing)"
		}
		fmt.Printf("  %-18s %d\n", name, counts[k])
	}
	return nil
}

func readStatus(refDir, id string) string {
	b, err := os.ReadFile(filepath.Join(refDir, id+".status"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// runRefShards runs the shards in parallel and reports whether any
// container had to be killed or failed.
func runRefShards(dir string, ids []string) (timedOut, failed bool) {
	n := max(min(*flagRefJobs, len(ids)), 1)
	shards := make([][]string, n)
	for i, id := range ids {
		shards[i%n] = append(shards[i%n], id)
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i, shard := range shards {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("moonref-corpus-%d-%d", os.Getpid(), i)
			ctx, cancel := context.WithTimeout(context.Background(), *flagRefTO)
			defer cancel()
			var list strings.Builder
			for _, id := range shard {
				list.WriteString(id + ".moon\n")
			}
			cmd := exec.CommandContext(ctx, "docker", "run", "--rm", "-i", "--name", name,
				"--network", "none", "--memory", "2g",
				"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
				"-v", filepath.Join(dir, "files")+":/files:ro",
				"-v", filepath.Join(dir, "ref")+":/ref",
				"-v", filepath.Join(dir, "scripts")+":/scripts:ro",
				*flagImage, "lua5.1", "/scripts/refbatch.lua", "/files", "/ref")
			cmd.Stdin = strings.NewReader(list.String())
			cmd.Stderr = os.Stderr
			out, err := cmd.Output()
			if ctx.Err() != nil {
				_ = exec.Command("docker", "kill", name).Run() // best effort: it may be gone already
				mu.Lock()
				timedOut = true
				mu.Unlock()
				fmt.Printf("shard %d: killed after %s\n", i, *flagRefTO)
				return
			}
			if err != nil {
				fmt.Printf("shard %d: %v\n", i, err)
				mu.Lock()
				failed = true
				mu.Unlock()
			}
			_ = out // per-shard counts; the totals are printed by genRef
		}()
	}
	wg.Wait()
	return timedOut, failed
}
