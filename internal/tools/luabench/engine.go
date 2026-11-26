package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// engine is a Lua implementation the benchmarks run on.
type engine struct {
	name  string // "vm", "puc", "luajit-joff", "luajit"
	image string // Docker image; "" for our VM (a host process)
	argv  []string

	container string // the running container (docker exec target)
}

func newEngines(names []string, pucImage, jitImage string) ([]*engine, error) {
	all := map[string]*engine{
		"vm":          {name: "vm"},
		"puc":         {name: "puc", image: pucImage, argv: []string{"lua5.1"}},
		"luajit-joff": {name: "luajit-joff", image: jitImage, argv: []string{"luajit", "-joff"}},
		"luajit":      {name: "luajit", image: jitImage, argv: []string{"luajit"}},
	}
	var es []*engine
	for _, n := range names {
		e, ok := all[n]
		if !ok {
			return nil, fmt.Errorf("unknown engine %q (vm, puc, luajit-joff, luajit)", n)
		}
		es = append(es, e)
	}
	return es, nil
}

// startContainers starts one long-lived container per image the engines
// use, with the work directory mounted read-only at the same path (so
// script paths are the same for every engine); runs are docker exec's into
// it, timed inside by the wrapper.
func startContainers(es []*engine, work string) (stop func(), err error) {
	byImage := map[string]string{}
	var names []string
	stop = func() {
		for _, n := range names {
			_ = exec.Command("docker", "rm", "-f", n).Run() // best effort
		}
	}
	for _, e := range es {
		if e.image == "" {
			continue
		}
		if c, ok := byImage[e.image]; ok {
			e.container = c
			continue
		}
		name := fmt.Sprintf("luabench-%d-%d", os.Getpid(), len(names))
		cmd := exec.Command("docker", "run", "-d", "--rm", "--name", name,
			"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
			"--network", "none", "-v", work+":"+work+":ro", "-w", work,
			"--entrypoint", "sleep", e.image, "infinity")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			stop()
			return nil, fmt.Errorf("starting %s: %w: %s", e.image, err, strings.TrimSpace(stderr.String()))
		}
		names = append(names, name)
		byImage[e.image] = name
		e.container = name
	}
	return stop, nil
}

// runResult is one run of a script.
type runResult struct {
	cpu    float64 // CPU seconds of the script's run (the wrapper's clock)
	wall   float64 // wall seconds of the whole process (startup included)
	md5    string  // of the (masked) standard output
	outLen int64
	err    error
}

var cpuLine = regexp.MustCompile(`(?m)^LUABENCH_CPU ([0-9.eE+-]+)\s*$`)

// run runs `<engine> wrap.lua script args` with stdin from the file stdin
// (if not ""), writing the standard output to the file out. self is the
// luabench executable (our VM runner); profile, if set, is where our VM
// writes a CPU profile.
func (e *engine) run(ctx context.Context, self, work, script string, args []string, stdin, out, profile string, mask *regexp.Regexp) runResult {
	wrap := work + "/wrap.lua"
	var argv []string
	if e.image == "" {
		argv = []string{self, "vm"}
		if profile != "" {
			argv = append(argv, "-cpuprofile", profile)
		}
		argv = append(argv, wrap, script)
	} else {
		argv = []string{"docker", "exec", "-i", e.container}
		argv = append(argv, e.argv...)
		argv = append(argv, wrap, script)
	}
	argv = append(argv, args...)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if stdin != "" {
		f, err := os.Open(stdin)
		if err != nil {
			return runResult{err: err}
		}
		defer func() { _ = f.Close() }()
		cmd.Stdin = f
	}
	of, err := os.Create(out)
	if err != nil {
		return runResult{err: err}
	}
	cmd.Stdout = of
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	t0 := time.Now()
	err = cmd.Run()
	wall := time.Since(t0).Seconds()
	if cerr := of.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = errors.New("timeout")
		}
		return runResult{err: fmt.Errorf("%w: %s", err, tail(stderr.String(), 400))}
	}
	m := cpuLine.FindStringSubmatch(stderr.String())
	if m == nil {
		return runResult{err: fmt.Errorf("no LUABENCH_CPU line: %s", tail(stderr.String(), 400))}
	}
	cpu, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return runResult{err: err}
	}
	sum, n, err := outputMD5(out, mask)
	if err != nil {
		return runResult{err: err}
	}
	return runResult{cpu: cpu, wall: wall, md5: sum, outLen: n}
}

// outputMD5 is the md5 of the file, after removing the matches of mask.
func outputMD5(path string, mask *regexp.Regexp) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = f.Close() }()
	h := md5.New()
	var n int64
	if mask == nil {
		if n, err = io.Copy(h, f); err != nil {
			return "", 0, err
		}
	} else {
		data, err := io.ReadAll(f)
		if err != nil {
			return "", 0, err
		}
		data = mask.ReplaceAll(data, []byte("<masked>"))
		n = int64(len(data))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = "..." + s[len(s)-n:]
	}
	return strings.ReplaceAll(s, "\n", " | ")
}
