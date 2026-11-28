package moonscript

import "testing"

// TestDebugPosmap compares with `moonc -X` output from the reference.
func TestDebugPosmap(t *testing.T) {
	src := "x = 1\nprint x\nif x\n  y = 2\n"
	code, posmap, err := ToLua(src, Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := "1\t 1:[ local x = 1 ] >> 1:[ x = 1 ]\n" +
		"7\t 2:[ print(x) ] >> 2:[ print x ]\n" +
		"17\t 3:[ if x then ] >> 3:[ if x ]\n" +
		"22\t 4:[ local y = 2 ] >> 4:[ y = 2 ]\n" +
		"15\t 5:[ end ] >> 3:[ if x ]"
	if got := DebugPosmap(posmap, src, code); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}
