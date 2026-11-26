package pm

// go-moonscript patch: tests of the cache of compiled patterns.

import (
	"fmt"
	"math/rand"
	"strings"
	"sync/atomic"
	"testing"
)

func captures(ms []*MatchData) string {
	var b strings.Builder
	for _, m := range ms {
		for i := 0; i < m.CaptureLength(); i++ {
			fmt.Fprintf(&b, "%d:%v ", m.Capture(i), m.IsPosCapture(i))
		}
		b.WriteString("| ")
	}
	return b.String()
}

func TestPatternCache(t *testing.T) {
	for _, c := range []struct{ pat, src string }{
		{"()a(b)", "xxab ab"},
		{"(a)()b", "aXbaab"},
		{"((a)(b))c", "abcabc abd"},
		{"(x)(y)%2%1", "xyx xyyx"},
		{"^(%w+)=(%w+)$", "k=v"},
		{"%b()", "f(a(b)c)d()"},
		{"a-b", "aaab"},
	} {
		first, err := Find(c.pat, []byte(c.src), 0, -1)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 3; i++ { // from the cache
			again, err := Find(c.pat, []byte(c.src), 0, -1)
			if err != nil {
				t.Fatal(err)
			}
			if g, w := captures(again), captures(first); g != w {
				t.Fatalf("%q: %s, first %s", c.pat, g, w)
			}
		}
	}
	for i := 0; i < maxCachedPatterns*2; i++ {
		pat := fmt.Sprintf("y%d", i)
		ms, err := Find(pat, []byte("a "+pat+" b"), 0, -1)
		if err != nil || len(ms) != 1 {
			t.Fatalf("pattern %q: %d matches, %v", pat, len(ms), err)
		}
	}
	if n := atomic.LoadInt32(&nCached); n > maxCachedPatterns {
		t.Fatalf("%d patterns cached", n)
	}
	for i := 0; i < 2; i++ { // invalid patterns aren't cached
		if _, err := Find("(%", []byte("a"), 0, -1); err == nil {
			t.Fatal("no error")
		}
	}
}

// TestFindLiteral checks that the matches of a pattern without special
// characters are the ones the VM finds.
func TestFindLiteral(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	alphabet := "ab\x00\xff"
	str := func(n int) string {
		b := make([]byte, n)
		for i := range b {
			b[i] = alphabet[rnd.Intn(len(alphabet))]
		}
		return string(b)
	}
	for i := 0; i < 20000; i++ {
		pat := str(1 + rnd.Intn(3))
		src := []byte(str(rnd.Intn(12)))
		cp := compiledPattern(pat)
		if !cp.literal {
			t.Fatalf("%q isn't literal", pat)
		}
		offset, limit := rnd.Intn(len(src)+2), rnd.Intn(4)-1
		got := captures(findLiteral(cp.lit, src, offset, limit))
		want := captures(findVM(&matcher{}, cp, src, offset, limit))
		if got != want {
			t.Fatalf("Find(%q, %q, %d, %d) = %s, want %s", pat, src, offset, limit, got, want)
		}
	}
	for _, p := range []string{"a.", "a%a", "(a)", "a*", "[a]", "^a", "a$", "a-", "a+", "a?"} {
		if compiledPattern(p).literal {
			t.Fatalf("%q is literal", p)
		}
	}
}
