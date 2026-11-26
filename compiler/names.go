package compiler

// Port of moonscript/transform/names.moon.

import (
	"fmt"

	"github.com/tarantool-contrib/go-moonscript/ast"
)

// LocalName always declares as local.
type LocalName struct {
	Name string
	Pos  int
}

func NewLocalName(name string) *LocalName { return &LocalName{Name: name} }

func (l *LocalName) NType() string { return "temp_name" }

func (l *LocalName) GetName(*Block, ...bool) string { return l.Name }

func (l *LocalName) String() string { return fmt.Sprintf("{%q}", "temp_name") }

// NameProxy creates a unique name when used.
type NameProxy struct {
	Prefix string
	Name   string
	named  bool
	Pos    int
}

func NewNameProxy(prefix string) *NameProxy { return &NameProxy{Prefix: prefix} }

func (p *NameProxy) NType() string { return "temp_name" }

// GetName resolves the proxy in scope; dontPut defaults to true.
func (p *NameProxy) GetName(scope *Block, dontPut ...bool) string {
	if !p.named {
		dp := true
		if len(dontPut) > 0 {
			dp = dontPut[0]
		}
		p.Name = scope.FreeName(p.Prefix, dp)
		p.named = true
	}
	return p.Name
}

// Chain builds proxy.a.b... string items become {"dot", v}.
func (p *NameProxy) Chain(items ...any) *ast.Node {
	parts := make([]any, len(items))
	for i, v := range items {
		if s, ok := v.(string); ok {
			parts[i] = node("dot", s)
		} else {
			parts[i] = v
		}
	}
	return buildChain(p, parts...)
}

// Index builds proxy[key]; a string key becomes a ref.
func (p *NameProxy) Index(key any) *ast.Node {
	if s, ok := key.(string); ok {
		key = node("ref", s)
	}
	return buildChain(p, node("index", key))
}

func (p *NameProxy) String() string {
	if p.named {
		return fmt.Sprintf("name<%s>", p.Name)
	}
	return fmt.Sprintf("name<prefix(%s)>", p.Prefix)
}

// nameHolder is implemented by NameProxy and LocalName.
type nameHolder interface {
	GetName(scope *Block, dontPut ...bool) string
}

func isNameProxy(v any) bool {
	switch v.(type) {
	case *NameProxy, *LocalName:
		return true
	}
	return false
}

// proxyIndex is NameProxy.index(obj, key) called with any receiver (the Lua
// code calls it as a plain function on proxies).
func proxyIndex(obj any, key any) *ast.Node {
	if s, ok := key.(string); ok {
		key = node("ref", s)
	}
	return buildChain(obj, node("index", key))
}

// proxyChain is NameProxy.chain(obj, ...).
func proxyChain(obj any, items ...any) *ast.Node {
	parts := make([]any, len(items))
	for i, v := range items {
		if s, ok := v.(string); ok {
			parts[i] = node("dot", s)
		} else {
			parts[i] = v
		}
	}
	return buildChain(obj, parts...)
}
