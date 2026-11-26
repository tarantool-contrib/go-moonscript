package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
)

// Mutants are derived from real files by small deterministic edits (seeded
// by the file hash). Real code rarely fails to compile, so they provide
// realistic error cases: unterminated strings and blocks, bad indentation,
// stray tokens, at real-world positions in large files. In the manifest
// their repo is "~mutant/<kind>", the commit column holds the original id
// and the path is "<orig repo>/<orig path>@<edit>".

const mutantPrefix = "~mutant/"

var mutantKinds = []string{"cut-byte", "cut-line", "drop-line", "indent-line", "dedent-line", "insert-char"}

type mutant struct {
	kind, detail string
	data         []byte
}

func makeMutants(id string, data []byte, n int) []mutant {
	if len(data) == 0 || n <= 0 {
		return nil
	}
	seed, _ := hex.DecodeString(id)
	s := binary.LittleEndian.Uint64(append(seed, make([]byte, 8)...))
	rng := rand.New(rand.NewPCG(s, 0x6d6f6f6e))
	lines := bytes.SplitAfter(data, []byte("\n"))
	nonBlank := func() int { // a random non-blank line, -1 if none
		for range 20 {
			if i := rng.IntN(len(lines)); len(bytes.TrimSpace(lines[i])) > 0 {
				return i
			}
		}
		return -1
	}
	join := func(ls [][]byte) []byte { return bytes.Join(ls, nil) }
	var out []mutant
	for k := range n {
		kind := mutantKinds[(int(s%uint64(len(mutantKinds)))+k)%len(mutantKinds)]
		var m mutant
		switch kind {
		case "cut-byte":
			p := 1 + rng.IntN(len(data))
			m = mutant{kind, fmt.Sprintf("@byte%d", p), data[:p]}
		case "cut-line":
			l := 1 + rng.IntN(len(lines))
			m = mutant{kind, fmt.Sprintf("@line%d", l), join(lines[:l])}
		case "drop-line":
			if l := nonBlank(); l >= 0 {
				ls := append(append([][]byte{}, lines[:l]...), lines[l+1:]...)
				m = mutant{kind, fmt.Sprintf("@line%d", l+1), join(ls)}
			}
		case "indent-line":
			if l := nonBlank(); l >= 0 {
				ls := append([][]byte{}, lines...)
				ls[l] = append([]byte(" "), ls[l]...)
				m = mutant{kind, fmt.Sprintf("@line%d", l+1), join(ls)}
			}
		case "dedent-line":
			if l := nonBlank(); l >= 0 {
				ls := append([][]byte{}, lines...)
				ls[l] = bytes.TrimLeft(ls[l], " \t")
				m = mutant{kind, fmt.Sprintf("@line%d", l+1), join(ls)}
			}
		case "insert-char":
			const chars = "()[]{}=,:\\@!\"'.-#$%^&*|<>~`"
			p := rng.IntN(len(data) + 1)
			c := chars[rng.IntN(len(chars))]
			d := append(append(append([]byte{}, data[:p]...), c), data[p:]...)
			m = mutant{kind, fmt.Sprintf("@byte%d%q", p+1, string(c)), d}
		}
		if len(m.data) > 0 && !bytes.Equal(m.data, data) {
			out = append(out, m)
		}
	}
	return out
}
