// Package ast holds the MoonScript syntax tree representation.
//
// The reference implementation represents every node as a plain Lua table
// such as {"assign", names, values} and the transformer freely rewrites those
// tables. To keep the port line-for-line comparable, Node mirrors a Lua table:
// Items is the array part and the accessor methods use Lua's 1-based indices,
// so node[2] in MoonScript is n.At(2) here. Pos mirrors the node[-1] slot
// holding the 1-based source byte offset (0 means unset).
//
// Values stored in a node are one of: nil, string, bool, int, *Node, or a
// compiler object implementing Typed (name proxies, Run statements, ...).
package ast

// Typed is implemented by compiler objects that live inside the tree and
// answer to ntype like a table would through their [1] slot (NameProxy has
// [1] = "temp_name", Run has [1] = "run").
type Typed interface {
	NType() string
}

// Node is a Lua-table-like syntax node or list.
type Node struct {
	Items []any
	Pos   int
}

// New builds a node from its items.
func New(items ...any) *Node {
	return &Node{Items: items}
}

// NewPos builds a node with a source position.
func NewPos(pos int, items ...any) *Node {
	return &Node{Items: items, Pos: pos}
}

// Len is Lua's # operator.
func (n *Node) Len() int {
	if n == nil {
		return 0
	}
	return len(n.Items)
}

// At returns n[i] (1-based); nil when out of range.
func (n *Node) At(i int) any {
	if n == nil || i < 1 || i > len(n.Items) {
		return nil
	}
	return n.Items[i-1]
}

// Last returns n[#n].
func (n *Node) Last() any {
	return n.At(n.Len())
}

// Set assigns n[i] = v. Assigning to #n+1 appends; assigning nil to the last
// slot removes it, as it would in Lua.
func (n *Node) Set(i int, v any) {
	switch {
	case i >= 1 && i <= len(n.Items):
		if v == nil && i == len(n.Items) {
			n.Items = n.Items[:i-1]
			return
		}
		n.Items[i-1] = v
	case i == len(n.Items)+1:
		if v != nil {
			n.Items = append(n.Items, v)
		}
	default:
		panic("ast: sparse assignment to node")
	}
}

// Append is table.insert(n, v).
func (n *Node) Append(vs ...any) {
	n.Items = append(n.Items, vs...)
}

// Insert is table.insert(n, i, v).
func (n *Node) Insert(i int, v any) {
	n.Items = append(n.Items, nil)
	copy(n.Items[i:], n.Items[i-1:])
	n.Items[i-1] = v
}

// Remove is table.remove(n) (pops the last element) when i is omitted, or
// table.remove(n, i).
func (n *Node) Remove(i ...int) any {
	if len(n.Items) == 0 {
		return nil
	}
	idx := len(n.Items)
	if len(i) > 0 {
		idx = i[0]
	}
	v := n.Items[idx-1]
	n.Items = append(n.Items[:idx-1], n.Items[idx:]...)
	return v
}

// Slice returns a new node holding n[from..to] (inclusive, 1-based); to < 0
// means #n. This is {unpack n, from, to}.
func (n *Node) Slice(from int, to ...int) *Node {
	end := n.Len()
	if len(to) > 0 && to[0] >= 0 {
		end = to[0]
	}
	out := &Node{}
	for i := from; i <= end; i++ {
		out.Items = append(out.Items, n.At(i))
	}
	return out
}

// Rest is the item list n[from..#n] as a Go slice (shared backing array not
// guaranteed); used for unpack-style splicing.
func (n *Node) Rest(from int) []any {
	if n == nil || from > len(n.Items) {
		return nil
	}
	out := make([]any, len(n.Items)-from+1)
	copy(out, n.Items[from-1:])
	return out
}

// Kind returns n[1] when it is a string, "" otherwise.
func (n *Node) Kind() string {
	if n == nil || len(n.Items) == 0 {
		return ""
	}
	s, _ := n.Items[0].(string)
	return s
}

// NType is MoonScript's types.ntype: "nil" for nil, node[1] for tables,
// "value" for anything else. Tables whose first slot is not a string (plain
// lists) report "".
func NType(v any) string {
	switch t := v.(type) {
	case nil:
		return "nil"
	case *Node:
		if t == nil {
			return "nil"
		}
		return t.Kind()
	case Typed:
		return t.NType()
	default:
		return "value"
	}
}

// IsTable reports whether a value is a Lua table in the reference
// implementation (nodes and the compiler's table-backed objects).
func IsTable(v any) bool {
	switch t := v.(type) {
	case *Node:
		return t != nil
	case Typed:
		return true
	}
	return false
}

// AsNode returns v as a *Node or nil.
func AsNode(v any) *Node {
	n, _ := v.(*Node)
	return n
}

// PosOf returns the [-1] slot of a table value, 0 otherwise.
func PosOf(v any) int {
	if n, ok := v.(*Node); ok && n != nil {
		return n.Pos
	}
	return 0
}

// Truthy is Lua truthiness.
func Truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case *Node:
		return t != nil
	}
	return true
}
