// Package scope resolves the names of a Lua syntax tree: for every
// occurrence of a name, the local variable it refers to, or the global it
// reads or writes, with the scopes, the upvalues, the shadowing and, under
// LuaJIT, the labels and gotos. go-moonscript patch.
//
// The package reads a tree of package ast and nothing else: it imports
// neither the parser nor the compiler, so it works on a tree from
// parse.ParseChunk as well as on one that other code built or transformed
// (a bundler, a compiler to Lua), with spans or without. It resolves names
// as Lua's and LuaJIT's parsers do (lparser.c, lj_parse.c) and as the
// compiler of package lua does. Its tests check it against the compiler's
// debug information (the upvalues, the globals and the locals of every
// function) over the compatibility scripts, the luasuites archives and the
// fork's other test scripts, and against go-luascope's resolution.
//
//	chunk, err := parse.ParseChunk(src, "name", parse.Options{Dialect: parse.LuaJIT})
//	info, err := scope.Resolve(chunk, scope.LuaJIT)
//	for _, g := range info.Globals { ... } // names read and written, with their occurrences
//	for _, v := range info.Vars { ... }    // every declared variable
//
// # Keys
//
// An occurrence of a name in the tree is a node, an *ast.IdentExpr, or the
// label of a goto; a declared name is a string with a span next to it (the
// names of a local statement, of a for loop, the parameters, labels). The
// result is keyed as follows, without a map unless one is asked for:
//
//   - An occurrence is a Ref, in Info.Refs in source order (the order of
//     ast.Walk). Its identity is its node: Ref.Ident for a name,
//     Ref.Goto for a goto. Info.RefOf(ident) finds the Ref of a node (a
//     map built on first use), Info.RefAt(offset) that of the name at a byte
//     offset of the source (a binary search of the spans; a tree without
//     spans has no offsets). The Ref says what the name refers to: Ref.Var,
//     nil for a global.
//   - A declaration is a Var, in Info.Vars in the order the names appear in
//     the source. Its identity is the pointer. It knows its declaring node
//     (Var.Decl), the index of the name in that node's list of names
//     (Var.Index, in LocalAssignStmt.Names, GenericForStmt.Names or
//     ParList.Names) and the span of the name (Var.NameSpan);
//     Info.VarAt(offset) finds the variable whose name is at an offset.
//   - The implicit variables have no name in the source, so no span (the
//     zero Span): the "self" of a method (KindSelf: of "function a.b:c()"
//     and of nothing else) and the "arg" of a vararg function under Lua51
//     (KindArg). Their Decl is the *ast.FunctionExpr and their Index is -1.
//     They are declared with the parameters: self first, arg after them
//     (Scope.Vars). Their occurrences in the body are Refs like any other.
//   - A scope is a Scope: Info.Scopes in the order they open (a scope
//     before its children), Info.ScopeAt(offset) the innermost one at an
//     offset, Info.ScopeOf(node) the one of a node.
//
// # Occurrences
//
// Ref.Kind is Read (a name in an expression, which includes the object of
// "a.b = 1" and of "function a.b()"), Write (a name that is assigned: a
// target of an assignment, the name of "function f()") or Jump (the label
// of a goto; its Var is the label, nil if there is none). A Ref whose Var
// is nil and that is no Jump is a global; Info.Globals groups them by name.
// A Ref in a function other than the one of its Var is an upvalue
// (Ref.IsUpvalue): the Var is Captured, and the function and those
// between it and the Var's function have it in Scope.Upvalues, in the
// order of the first reference, which is the order of the function
// prototype's upvalue names.
//
// # Scoping rules
//
// They are Lua's, which the parsers and the compiler follow:
//
//   - "local x = x": the right side sees the outer x; the new x is
//     visible after the statement. "local function f" sees f in its body,
//     "local f = function" does not. "local a, b = b, a" reads the outer a
//     and b; "local a, a" declares two, the second hides the first.
//   - A function has one scope for its parameters and the locals of its
//     body, so "function(x) local x end" declares a second x in the same
//     scope. A method has "self" before the parameters, a Lua51 vararg
//     function "arg" after them (not the main chunk, not under LuaJIT, not
//     with ParList.NoArg; Lua51 assumes LUA_COMPAT_VARARG, as
//     lua.CompatVarArg's default does). "function f()" without a local f is
//     a write of the global f, "function t.x()" and "function t:m()" read t.
//   - "repeat ... until c": c sees the locals of the body (ScopeRepeat's
//     span ends with it).
//   - A numeric or generic for: its variables are in a scope of their own
//     (ScopeFor, with the span of the body), around the scope of the body
//     (ScopeForBody), so a local of the body with the name of a variable
//     shadows it; the expressions of the loop are outside both. The hidden
//     control variables are no names and not in the result.
//   - The other blocks, the bodies of do and while and the then and else
//     blocks of if, are scopes. An "elseif" is an IfStmt alone in the Else
//     of the one before; an else block that holds only an IfStmt has no
//     scope of its own.
//   - Labels, under LuaJIT, as lj_parse.c has them: a label is visible in
//     its whole block, before and after it, and in the blocks, not the
//     functions, inside the block. A goto binds to the label of the
//     innermost block that has one. A block has a label once; one of the
//     same name in a block inside it is another label. A goto may not jump
//     forward into the scope of a local, but to a label at the end of a
//     block, with the labels right before it, which is out of the block's
//     locals' scope, unless the block is the body of a repeat, which
//     "until" follows. The ";" the source may have after such a label is not
//     in the tree: "::l:: ;" is taken as at the end of its block. A label is
//     a Var of KindLabel in the scope of its block, its Reads the gotos
//     that bind to it.
//
// A parsed tree has no label problems (the parser rejects them); a tree
// that other code built can have them: a duplicate label, an undefined
// label, a jump into the scope of a local, labels and gotos under Lua51.
// Resolve returns them as Info.Problems, with LuaJIT's messages, in an
// *Error, together with the Info of the rest of the tree.
//
// # Shadowing
//
// A Var shadows another if the variable that a lookup of its name finds
// just before it takes effect is a Var of the same function or of an
// enclosing one: Var.Shadows. So the parameter hidden by a local of the
// body, the outer x of "local x = x", the first of "local a, a" and the
// loop variable hidden by a local of the body are shadowed, a global is
// not. Info.Shadowing lists the Vars that shadow. Whether that is wrong is
// for the tool that uses the package (a "_" hiding another "_", a
// parameter "self" of a method).
//
// # Limits
//
// The limits of the number of locals and of upvalues are not checked: a tree
// that the compiler rejects for them resolves. What is not about names
// is not checked either (a break outside a loop, "..." outside a vararg
// function). A nil node, also a nil pointer in an interface, is skipped.
// A tree more than 100000 levels deep (the compiler's limit) stops the
// resolution with a Problem, so that a cycle in a tree doesn't loop. An
// Info is read only and safe for concurrent use.
package scope
