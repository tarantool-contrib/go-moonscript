package ast

import (
	"fmt"
	"strconv"
	"strings"
)

// FlatValue renders a value the way moonscript.dump.value does: this is the
// format printed by `moonc -T`.
func FlatValue(v any) string {
	switch t := v.(type) {
	case string:
		return `"` + t + `"`
	case *Node:
		if t == nil {
			return "nil"
		}
		var b strings.Builder
		b.WriteByte('{')
		if t.Pos != 0 {
			b.WriteString("[" + strconv.Itoa(t.Pos) + "] ")
		}
		for i, item := range t.Items {
			if item == nil {
				// `for item in *op` stops at the first hole
				break
			}
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(FlatValue(item))
		}
		b.WriteByte('}')
		return b.String()
	case nil:
		return "nil"
	case bool:
		return strconv.FormatBool(t)
	case int:
		return strconv.Itoa(t)
	case fmt.Stringer:
		return t.String()
	}
	return fmt.Sprint(v)
}

// Tree renders a statement list like moonscript.dump.tree.
func Tree(block *Node) string {
	parts := make([]string, 0, block.Len())
	for _, v := range block.Items {
		parts = append(parts, FlatValue(v))
	}
	return strings.Join(parts, "\n")
}
