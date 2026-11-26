package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/bits"
)

// Mutants are derived from real files by small deterministic edits (seeded
// by the file hash, see mutantRand: the same file always gives the same
// mutants, whatever the Go release, so that the CI corpus check can
// regenerate them and compare with the committed expected results). Real code rarely fails to compile, so they provide
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
	rng := &mutantRand{hi: s, lo: 0x6d6f6f6e}
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

// mutantRand is a PCG generator with 128-bit state and DXSM output
// (https://www.pcg-random.org) with Lemire's unbiased bounded integers
// (https://arxiv.org/abs/1805.10941): the stream of math/rand/v2's
// rand.New(rand.NewPCG(hi, lo)).IntN, which made the mutants of the
// existing corpus references, but fixed here, as math/rand/v2 does not
// promise the same stream in every Go release. TestMutantsStable pins it.
type mutantRand struct{ hi, lo uint64 }

func (r *mutantRand) uint64() uint64 {
	const (
		mulHi    = 2549297995355413924
		mulLo    = 4865540595714422341
		incHi    = 6364136223846793005
		incLo    = 1442695040888963407
		cheapMul = 0xda942042e4dd58b5
	)
	// state = state*mul + inc (mod 2^128)
	hi, lo := bits.Mul64(r.lo, mulLo)
	hi += r.hi*mulLo + r.lo*mulHi
	lo, c := bits.Add64(lo, incLo, 0)
	hi, _ = bits.Add64(hi, incHi, c)
	r.hi, r.lo = hi, lo
	// DXSM output
	hi ^= hi >> 32
	hi *= cheapMul
	hi ^= hi >> 48
	return hi * (lo | 1)
}

// IntN returns a uniform integer in [0, n), n > 0.
func (r *mutantRand) IntN(n int) int {
	m := uint64(n)
	if m&(m-1) == 0 {
		return int(r.uint64() & (m - 1))
	}
	hi, lo := bits.Mul64(r.uint64(), m)
	if lo < m {
		thresh := -m % m
		for lo < thresh {
			hi, lo = bits.Mul64(r.uint64(), m)
		}
	}
	return int(hi)
}
