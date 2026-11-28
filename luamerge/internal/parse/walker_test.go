package parse_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yuin/gopher-lua/ast"

	"github.com/tarantool-contrib/go-moonscript/luamerge/internal/parse"
)

// testPosition is a Lua source with require("x") in one statement or
// expression position, and the line that require is on.
type testPosition struct {
	name   string
	source string
	line   int
}

// testPositions places require("x") in every position of gopher-lua's AST that
// holds an expression or a block, one position per case, so that a walker
// that skips any node type or any child of one misses x in at least one case.
// The childless cases put every node type without children next to a require,
// so a walker that does not know one of them fails on it.
func testPositions() []testPosition {
	positions := []testPosition{
		// AssignStmt.
		{"assignment value", `a = require("x")`, 1},
		{"assignment second value", `a, b = 1, require("x")`, 1},
		{"assignment target index", `t[require("x")] = 1`, 1},
		{"assignment target object", `require("x").loaded = true`, 1},
		// LocalAssignStmt.
		{"local value", `local a = require("x")`, 1},
		{"local second value", `local a, b = 1, require("x")`, 1},
		{"local function body", "local function f()\n  return require(\"x\")\nend\n", 2},
		// FuncCallStmt.
		{"call statement", `require("x")`, 1},
		// DoBlockStmt.
		{"do block", "do\n  require(\"x\")\nend\n", 2},
		// WhileStmt.
		{"while condition", `while require("x") do end`, 1},
		{"while body", "while cond do\n  require(\"x\")\nend\n", 2},
		// RepeatStmt.
		{"repeat body", "repeat\n  require(\"x\")\nuntil cond\n", 2},
		{"repeat condition", "repeat\nuntil require(\"x\")\n", 2},
		// IfStmt; an elseif is an IfStmt nested in Else.
		{"if condition", `if require("x") then end`, 1},
		{"if then block", "if cond then\n  require(\"x\")\nend\n", 2},
		{"elseif condition", "if a then\nelseif require(\"x\") then\nend\n", 2},
		{"elseif block", "if a then\nelseif b then\n  require(\"x\")\nend\n", 3},
		{"else block", "if a then\nelse\n  require(\"x\")\nend\n", 3},
		{"else block after elseif", "if a then\nelseif b then\nelse\n  require(\"x\")\nend\n", 4},
		// NumberForStmt.
		{"numeric for start", `for i = require("x"), 2 do end`, 1},
		{"numeric for limit", `for i = 1, require("x") do end`, 1},
		{"numeric for step", `for i = 1, 2, require("x") do end`, 1},
		{"numeric for body", "for i = 1, 2 do\n  require(\"x\")\nend\n", 2},
		// GenericForStmt.
		{"generic for iterator", `for k, v in pairs(require("x")) do end`, 1},
		{"generic for second expression", `for k, v in next, require("x") do end`, 1},
		{"generic for body", "for k, v in pairs(t) do\n  require(\"x\")\nend\n", 2},
		// FuncDefStmt.
		{"global function body", "function f()\n  require(\"x\")\nend\n", 2},
		{"field function body", "function M.f()\n  require(\"x\")\nend\n", 2},
		{"method body", "function M:m()\n  require(\"x\")\nend\n", 2},
		// ReturnStmt.
		{"return value", `return require("x")`, 1},
		{"return second value", `return 1, require("x")`, 1},

		// FuncCallExpr.
		{"called function", `require("x")("y")`, 1},
		{"called field", `require("x").f()`, 1},
		{"call argument", `print(require("x"))`, 1},
		{"call second argument", `print(1, require("x"))`, 1},
		{"method receiver", `require("x"):m()`, 1},
		{"method argument", `obj:m(require("x"))`, 1},
		// AttrGetExpr.
		{"indexed object", `local v = require("x").field`, 1},
		{"index key", `local v = t[require("x")]`, 1},
		// TableExpr.
		{"table array item", `local t = { require("x") }`, 1},
		{"table named field value", `local t = { k = require("x") }`, 1},
		{"table bracketed key", `local t = { [require("x")] = true }`, 1},
		{"table bracketed value", `local t = { [1] = require("x") }`, 1},
		// FunctionExpr.
		{"function expression body", "local f = function()\n  require(\"x\")\nend\n", 2},
		{"function expression argument", `pcall(function() require("x") end)`, 1},
		{
			"nested function expressions",
			`local f = function() local g = function() return require("x") end return g end`,
			1,
		},
		// Parentheses leave no node of their own; the expression inside is walked.
		{"parenthesized", `local v = (require("x"))`, 1},
		{"doubly parenthesized", `local v = ((require "x"))`, 1},
		{"parenthesized object", `local v = (require("x")).field`, 1},
		// UnaryMinusOpExpr, UnaryNotOpExpr, UnaryLenOpExpr.
		{"unary minus", `local v = -require("x")`, 1},
		{"not", `local v = not require("x")`, 1},
		{"length", `local v = #require("x")`, 1},

		// Childless nodes: BreakStmt, LabelStmt, GotoStmt, and every leaf
		// expression.
		{"next to break", "while true do\n  require(\"x\")\n  break\nend\n", 2},
		{"next to goto and label", "goto done\n::done::\nrequire(\"x\")\n", 3},
		{
			"next to leaf expressions",
			"local a, b, c, d, e, f, g = nil, true, false, 1, \"s\", name, ...\nrequire(\"x\")\n",
			2,
		},
	}

	return slices.Concat(positions, testOperatorPositions())
}

// testOperatorPositions places require("x") on either side of every binary
// operator: ArithmeticOpExpr, StringConcatOpExpr, RelationalOpExpr and
// LogicalOpExpr.
func testOperatorPositions() []testPosition {
	operators := []string{
		"+", "-", "*", "/", "%", "^",
		"..",
		"==", "~=", "<", "<=", ">", ">=",
		"and", "or",
	}
	sides := []struct{ name, format string }{
		{"left of ", `local v = require("x") %s 1`},
		{"right of ", `local v = 1 %s require("x")`},
	}

	positions := make([]testPosition, 0, len(operators)*len(sides))
	for _, operator := range operators {
		for _, side := range sides {
			positions = append(positions, testPosition{side.name + operator, fmt.Sprintf(side.format, operator), 1})
		}
	}

	return positions
}

func TestParse_RequireInEveryPosition(t *testing.T) {
	t.Parallel()

	for _, p := range testPositions() {
		t.Run(p.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, []parse.RequireInfo{testStatic("x", p.line)}, testParse(t, p.source))
		})
	}
}

// testUnknownStmt and testUnknownExpr stand for node types gopher-lua does not
// have today but a later version could add.
type (
	testUnknownStmt struct{ ast.StmtBase }
	testUnknownExpr struct{ ast.ExprBase }
)

// TestFindRequires_RejectsUnknownNodes: a node type the walker does not know
// could hold a require, so it is an error rather than a silent skip, and the
// first one is the one reported. Such nodes cannot come out of the parser, so
// the test builds them by hand and walks them the way Parse does.
func TestFindRequires_RejectsUnknownNodes(t *testing.T) {
	t.Parallel()

	stmt := new(testUnknownStmt)
	stmt.SetLine(3)

	found, err := parse.FindRequiresForTest([]ast.Stmt{stmt})
	require.ErrorIs(t, err, parse.ErrUnsupportedNodeForTest, "unknown statement")
	assert.Nil(t, found)
	assert.Contains(t, err.Error(), "line 3")
	assert.Contains(t, err.Error(), "testUnknownStmt")

	expr := new(testUnknownExpr)
	expr.SetLine(5)

	ret := new(ast.ReturnStmt)
	ret.Exprs = []ast.Expr{expr}

	later := new(testUnknownStmt)
	later.SetLine(7)

	found, err = parse.FindRequiresForTest([]ast.Stmt{ret, later})
	require.ErrorIs(t, err, parse.ErrUnsupportedNodeForTest, "unknown expression")
	assert.Nil(t, found)
	assert.Contains(t, err.Error(), "line 5", "the first unknown node is reported")
	assert.Contains(t, err.Error(), "testUnknownExpr")
}
