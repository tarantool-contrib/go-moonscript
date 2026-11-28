// Package flow builds the control-flow graphs of the functions of a Lua
// syntax tree and the data flow over its local variables: the base of lint
// rules. go-moonscript patch.
//
// It reads a tree of package ast with its resolution by package scope, and
// nothing else:
//
//	chunk, err := parse.ParseChunk(src, "name", parse.Options{Dialect: parse.LuaJIT})
//	info, err := scope.Resolve(chunk, scope.LuaJIT)
//	p := flow.Analyze(info, nil)
//	for _, g := range p.Graphs { ... g.Unreachable() ... } // per function
//	for _, d := range p.Defs { ... d.Used ... }            // per value of a local
//
// # Graphs
//
// A Graph is the graph of a function: the main chunk, and each
// FunctionExpr (Program.Graphs, in source order). Its basic blocks
// (Block) hold nodes (Node) in execution order: the statements, and the
// parts of the compound ones that do something of their own: the
// condition of an if, elseif, while or repeat (a Condition node, whose
// block ends with it and a True and a False edge), the iteration of a for
// (an Iteration: its test and the values of its variables), the end of an
// iteration of a while's or a for's body (a LoopEnd, going back to the
// loop's head). A compound statement has a Statement node where it starts
// (a for's evaluates its expressions); an elseif has only its Condition. A
// function starts with an Entry block holding its Enter node (the values
// of the parameters) and ends in an Exit block, which the returns and the
// end of the body lead to. Every statement is in one node, in one block;
// the nodes are in source order (Graph.Nodes).
//
// The edges are those of Lua: the branches of if and elseif, the loops,
// break (to after the innermost loop), return (to the Exit), goto (to its
// label, as package scope binds it, under LuaJIT). A repeat's condition is
// in the body's scope and goes back to the body's start when false.
//
// A condition that is a literal (nil, false, true, a number, a string, a
// table or a function constructor, in parentheses or not) is a constant:
// its block has only the edge that it takes. So "while true do" without a
// break doesn't end, what follows it is unreachable, as are the branch
// "if false then" and the else of "if true then". This is luacheck's rule;
// "not x", "1 == 1" and other expressions are not constants.
//
// The short-circuit evaluation of "and" and "or" is not in the graph: an
// expression is evaluated within its node. Lua has no assignment in an
// expression, so where in an expression a variable is read doesn't change
// which values reach it; what an expression can do to the control flow is
// a call, and calls are opaque. Splitting expressions would make many more
// nodes for nothing the queries use (luacheck doesn't split them either).
//
// Calls are opaque: a call returns, error(...) and os.exit() too.
// Options.NoReturn names functions that don't (globals or fields of
// globals, "error", "os.exit"): a call statement of one ends its path as a
// return does. None by default.
//
// # Queries
//
//   - Graph.Unreachable: the unreachable code, at the first node of each
//     unreachable stretch (luacheck's W511); a LoopEnd among them is a loop
//     whose body runs at most once (W512, Graph.AtMostOnce). Breaks and
//     labels are not reported by themselves.
//   - Graph.Returns: the reachable returns with values, without, and
//     whether the end of the body is reachable, for a rule against
//     functions that return values on some paths only
//     (Returns.Inconsistent).
//   - Program.Empty: the empty blocks: do (W541), then and else (W542),
//     loop bodies, function bodies.
//   - The data flow below: values never used and overwritten before use
//     (W311, W312, W313), uses of uninitialized variables (W321, W341),
//     unused recursive functions (Program.UnusedRecursive, W211/W311).
//
// # Data flow
//
// A Def is a value assigned to a local: the value of its declaration (a
// local's, an empty one for "local x"; a parameter's at Enter; a loop
// variable's at each Iteration; a local function's) and of each assignment
// and function statement. A Use is an occurrence that reads the variable
// (Read) or the table in it to assign a field (Mutate: t in "t.k = v",
// "function t:m()"). Each use knows the values it may see (Use.Defs); each
// value whether a read (Def.Used) or a mutation (Def.Mutated) may see it.
// A value that nothing uses is overwritten (Def.Overwritten) if every path
// from it reaches the same assignment before any use and before the end of
// the variable's scope. A use is uninitialized (Use.Uninitialized) if all
// the values it may see are empty declarations.
//
// The values a node sees are those before its assignments: in "x = x + 1"
// the x read is the old one. A node assigning a variable twice ("x, x = 1,
// 2") keeps its last value; the first is overwritten.
//
// For a local of a function, the uses and assignments in the function's
// own nodes are its main ones; those in the functions inside it, at any
// depth, are taken per closure: the function directly inside the
// variable's function that has them, at the node that creates it
// (Graph.Creator). A closure may be called at any time after it is
// created, any number of times, and its uses and assignments of the
// variable may happen in any order (luacheck's rule):
//
//  1. a main value reaches the main uses that a path from it leads to
//     without another main assignment of the variable (the reaching
//     definitions); assignments in closures never stop it;
//  2. a closure's uses see the main values that reach its creation, and
//     those assigned at the nodes that its creation leads to (it may be
//     called after them);
//  3. a main use sees the values a closure assigns if the closure's
//     creation leads to the use: in a node before it, or in an expression
//     before it in the same node (the expressions of a return or a for's
//     header are evaluated one by one; luacheck has them as items of their
//     own);
//  4. a closure's uses see the values another closure, or itself, assigns
//     if the creation of either leads to the creation of the other.
//
// A declaration makes a new instance of its variable: the values from
// before it, from an earlier pass through it, are not those of the
// closure it creates ("local function f" run again after a goto back).
// The creation of a closure leads wherever a path goes, also through a
// loop into the next instance of the variable, as in luacheck: that is
// conservative (more values seen, fewer warnings).
//
// A read of t in "t.k = e", with one target, one value and no call in
// them, doesn't make t's value used (luacheck's circular assignment); the
// assignment does mutate it. "function t.f()" and "function t:m()" are
// such assignments: a read of t in the function, through its creation
// there, doesn't use the value either.
//
// # luacheck
//
// The queries are checked against luacheck 1.2 (TestLuacheck): its
// warnings 511, 512, 541, 542, 311, 312, 313, 321 and 341 on the fork's
// testdata, the luasuites archives and thousands of random programs are
// those of a lint pass over this package, at the same lines and columns,
// but for four approximations of luacheck that the package doesn't make
// (its tests reproduce them to explain every difference):
//
//   - for a local of a function other than the main chunk, luacheck
//     connects every value that any of the function's closures assigns
//     with every use in any of them, also between closures that can never
//     share an instance of the variable (created in branches that exclude
//     each other): it resolves the closures inside a closure again at the
//     levels above. The package applies rule 4 at the variable's function
//     only, so it finds unused values and uninitialized uses there that
//     luacheck misses;
//   - luacheck keeps the parentheses of the last value of an assignment
//     ("x = (function() end)") and doesn't see a function in it;
//   - luacheck ends the scope of a variable at a declaration of the same
//     name in the same scope: a value that a goto takes back to before the
//     declaration doesn't reach the uses there;
//   - in the functions that a circular assignment of t creates ("function
//     t:m() ... t ... end"), luacheck connects the reads of t with no value
//     that reaches the assignment, so it finds no uninitialized read
//     there; the package gives them those values (they are what t holds
//     when the function is created).
//
// # Limits
//
// The graph and the data flow are of a resolved tree: a tree that is not
// the resolution's gives nonsense. A tree with label problems (a goto
// without its label, from a tree that other code built) has a goto that
// leads nowhere. Nil nodes are skipped, a tree deeper than 100000 levels is
// cut there, as by the resolver. A Program is read only and safe for
// concurrent use.
package flow
