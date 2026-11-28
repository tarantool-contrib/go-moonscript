package parse

// FurthestTrace parses src and returns the sequence of 0-based input
// positions at which the furthest failure position (the one parse errors
// are reported at) increased. It exists for differential testing against
// the reference parser's bookkeeping (internal/tools/parsefuzz -trace); the
// sequence must match pgen's step by step, not just in its final value.
func FurthestTrace(src string) (trace []int) {
	p := newGenParser(src)
	p.tracing = true
	defer func() {
		_ = recover() // a Cmt callback error ends the parse like in the reference
		trace = p.trace
	}()
	p.runPackrat()
	return p.trace
}
