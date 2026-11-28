package parse

import (
	"strings"

	"github.com/tarantool/go-luamerge/internal/lex"
)

// LuaJIT 2.1 builds of 2026 read syntax of their own, which LuaJIT's own
// jit.* modules use: "!=" and "!", "&&" and "||", the conditional expression
// `c ? a : b`, "??", which gives its left operand unless that is nil, the
// arithmetic shift "~>>", compound assignments (`x += 1`, `s ..= t`,
// `x ~>>= 2`; `x ~= 1` assigns x its exclusive or with 1 where it starts a
// statement), and a continue statement. The lexer reads "!" and "?" as
// Invalid tokens and each of the other operators as the operators it is made
// of: "!=" is "!" and "=", "&&" is "&" and "&", "~>>=" is "~", ">>" and "=".

// planLuaJIT records the edits of the LuaJIT extension at i, if there is one.
// "!=" becomes "~=" and "!" becomes not; "&&" becomes and, "||" and "??"
// become or; `c ? a : b` becomes `c and a or b`; "~>>" becomes "+"; and the
// operator of a compound assignment is left out, so `x += 1` is the
// assignment `x = 1`, with the same calls.
func (n *normalizer) planLuaJIT(i int) {
	if n.edits[i].drop {
		// The second half of an operator planned at the first.
		return
	}

	text := string(n.text(i))

	switch n.tokens[i].Kind {
	case lex.Invalid:
		switch {
		case text == "!" && n.joins(i, "="):
			n.replace(i, "~=")
		case text == "!":
			n.edits[i].text = []byte("not")
		case text == "?" && n.joins(i, "?"):
			n.replace(i, "or")
		case text == "?":
			n.edits[i].text = []byte("and")
			if colon := n.ternaryColon(i); colon >= 0 {
				n.edits[colon].text = []byte("or")
			}
		}
	case lex.Symbol:
		switch {
		case text == "&" && n.joins(i, "&"):
			n.replace(i, "and")
		case text == "|" && n.joins(i, "|"):
			n.replace(i, "or")
		case text == "~" && n.joins(i, ">>") && n.joins(i+1, "="):
			// x ~>>= 2: "~" and ">>" go, "=" stays.
			n.edits[i].drop = true
			n.edits[i+1].drop = true
		case text == "~" && n.joins(i, ">>"):
			n.replace(i, "+")
		case isCompoundOperator(text) && n.joins(i, "="):
			n.edits[i].drop = true
		case text == "~=" && n.assignsExclusiveOr(i):
			n.edits[i].text = []byte("=")
		}
	case lex.EOF, lex.Name, lex.Number, lex.String, lex.LongString, lex.Comment, lex.LongComment:
	}
}

// planContinue records the edits of a continue statement at i: it becomes
// ";", which does nothing else either. continue is a statement where a
// statement can start and another statement or the end of the block follows;
// anywhere else it is a name, as in every other dialect: `local continue`,
// `goto continue`, `::continue::`, `continue = 1`.
func (n *normalizer) planContinue(i int) {
	if !n.startsStatement(i) {
		return
	}

	next := i + 1
	name := n.name(next)

	if next < len(n.tokens) && !n.isSymbol(next, ";") && !n.isIdentifier(next) &&
		(name == "" || !strings.Contains(statementKeywords, " "+name+" ")) {
		return
	}

	n.edits[i].text = []byte(";")
}

// statementKeywords are the keywords that start a statement or end a block,
// each between spaces.
const statementKeywords = " end else elseif until local if for while repeat function return break goto do "

// replace writes text in place of the operator of two tokens at i.
func (n *normalizer) replace(i int, text string) {
	n.edits[i].text = []byte(text)
	n.edits[i+1].drop = true
}

// joins reports whether the token right after the one at i, with nothing
// between them, is text.
func (n *normalizer) joins(i int, text string) bool {
	next := i + 1

	return next < len(n.tokens) && n.tokens[next].Start == n.tokens[i].End && string(n.text(next)) == text
}

// ternaryColon returns the index of the ":" of the conditional expression
// whose "?" is at i, or -1 when there is none: the first ":" after it that is
// neither inside brackets or a block nor the ":" of a conditional expression
// nested in it. As in LuaJIT, a method call there, `c ? t:m() : x`, is none:
// write it in parentheses.
func (n *normalizer) ternaryColon(i int) int {
	depth, pending := 0, 0

	for j := i + 1; j < len(n.tokens); j++ {
		switch {
		case n.isKind(j, lex.Invalid) && string(n.text(j)) == "?":
			if n.joins(j, "?") {
				j++
			} else {
				pending++
			}
		case depth == 0 && n.isSymbol(j, ":"):
			if pending == 0 {
				return j
			}

			pending--
		default:
			depth += nesting(n.tokens[j].Kind, string(n.text(j)))
			if depth < 0 {
				return -1
			}
		}
	}

	return -1
}

// assignsExclusiveOr reports whether the "~=" at i follows a variable that
// starts a statement, `x ~= 1`: an assignment of x's exclusive or with 1.
// Anywhere else "~=" compares.
func (n *normalizer) assignsExclusiveOr(i int) bool {
	start := n.variableStart(i - 1)

	return start >= 0 && n.startsStatement(start)
}

// variableStart returns the index of the first token of the variable that
// ends at i, `a.b[c].d`, or -1 when no variable ends there.
func (n *normalizer) variableStart(i int) int {
	for i >= 0 {
		switch {
		case n.isSymbol(i, "]"):
			i = n.matchingOpen(i) - 1
		case n.isIdentifier(i):
			if !n.isSymbol(i-1, ".") {
				return i
			}

			i -= 2
		default:
			return -1
		}
	}

	return -1
}

// matchingOpen returns the index of the bracket that the bracket at i
// closes, or -1 when there is none.
func (n *normalizer) matchingOpen(i int) int {
	depth := 0

	for ; i >= 0; i-- {
		depth -= nesting(n.tokens[i].Kind, string(n.text(i)))
		if depth == 0 {
			return i
		}
	}

	return -1
}

// isCompoundOperator reports whether the Symbol text is the operator of a
// compound assignment when "=" follows it right away.
func isCompoundOperator(text string) bool {
	switch text {
	case "+", "-", "*", "/", "%", "^", "..", "//", "&", "|", "<<", ">>":
		return true
	default:
		return false
	}
}

// nesting returns 1 when a token of kind with text opens a bracket or a block
// that a later token closes, -1 when it closes one, and 0 otherwise.
func nesting(kind lex.Kind, text string) int {
	switch {
	case kind == lex.Symbol && (text == "(" || text == "[" || text == "{"):
		return 1
	case kind == lex.Symbol && (text == ")" || text == "]" || text == "}"):
		return -1
	case kind == lex.Name && (text == keywordFunction || text == "if" || text == "do" || text == "repeat"):
		return 1
	case kind == lex.Name && (text == keywordEnd || text == "until"):
		return -1
	default:
		return 0
	}
}
