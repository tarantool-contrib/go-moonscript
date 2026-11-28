package lua

// go-moonscript patch: Go tests of the string library and pattern matching
// fixes listed in moonscript.patch.

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Pattern matching stops when the state's context is done, also inside a
// coroutine; it ran to the end of a backtracking match inside one Go call
// (yuin/gopher-lua#559, #521, #566; mdogan/gopher-lua 64abd4bac7).
func TestPatternMatchContext(t *testing.T) {
	for _, script := range []string{
		`string.find(string.rep("a", 60), string.rep("a-", 30) .. "b")`,
		`string.find(string.rep("(", 1e6), "%b()")`,
		`string.match(string.rep("a", 60), string.rep("a*", 30) .. "b")`,
		`string.gsub(string.rep("a", 60), string.rep("a-", 30) .. "b", "")`,
		`string.gmatch(string.rep("a", 60), string.rep("a?", 30) .. string.rep("a", 30) .. "b")()`,
		`coroutine.wrap(function() string.find(string.rep("a", 60), string.rep("a-", 30) .. "b") end)()`,
	} {
		L := NewState()
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		L.SetContext(ctx)
		done := make(chan error, 1)
		go func() { done <- L.DoString(script) }()
		select {
		case err := <-done:
			if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
				t.Errorf("%s: %v", script, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: still matching 5s after a 50ms deadline", script)
		}
		cancel()
		L.Close()
	}
}
