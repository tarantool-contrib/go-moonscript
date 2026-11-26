package grammar

// Transcription of moonscript/parse/grammar.moon (MoonScript v0.8.0). Rule
// names, ordering and structure follow the original one to one; choices
// that pgen compiles with FIRST-byte dispatch use DAlt. Callback names refer
// to the functions in parse/callbacks.go.

var keywordWords = []string{
	"continue", "extends",
	"elseif", "export", "import", "return", "switch", "unless",
	"break", "class", "local", "using", "while",
	"else", "from", "then", "when", "with",
	"and", "for", "not",
	"do", "if", "in", "or",
}

// Moon returns the MoonScript v0.8.0 grammar.
func Moon() *Grammar {
	ind := Indenter{Stack: 0, TabWidth: 4}
	dos := Indenter{Stack: 1, TabWidth: 4}
	DisableDo := dos.CPush(0)
	PopDo := dos.Pop()
	CheckDo := dos.CTopNe(0)

	AlphaNum := Range("az", "AZ", "09", "__")

	Keyword := Seq(Trie(keywordWords...), Not(AlphaNum))

	mark := func(name string, patt *Node) *Node {
		return Ct(Seq(Cc(name), patt))
	}
	pos := func(patt *Node) *Node {
		return Pos(patt)
	}
	sym := func(chars string) *Node {
		return Seq(V("Space"), Lit(chars))
	}
	op := func(chars string) *Node {
		patt := Seq(V("Space"), C(Lit(chars)))
		if isWord(chars) {
			patt = Seq(patt, Not(AlphaNum))
		}
		return patt
	}
	key := func(chars string) *Node {
		return Seq(V("Space"), Lit(chars), Not(AlphaNum))
	}
	opt := Opt
	rep0 := func(p *Node) *Node { return Rep(p, 0) }
	rep1 := func(p *Node) *Node { return Rep(p, 1) }
	empty := func() *Node { return Lit("") }

	digits := Range("09")

	g := map[string]*Node{
		"Root": Seq(V("White"), V("File"), V("White"), EOF()),

		"White":      rep0(Set(" \t\r\n")),
		"Break":      Seq(opt(Lit("\r")), Lit("\n")),
		"Stop":       Alt(V("Break"), EOF()),
		"Comment":    Seq(Lit("--"), rep0(Diff(Any(1), Set("\r\n"))), Look(V("Stop"))),
		"Space":      Seq(rep0(Set(" \t")), opt(V("Comment"))),
		"SomeSpace":  Seq(rep1(Set(" \t")), opt(V("Comment"))),
		"SpaceBreak": Seq(V("Space"), V("Break")),
		"EmptyLine":  V("SpaceBreak"),
		"Shebang":    Seq(Lit("#!"), rep0(Diff(Any(1), V("Stop")))),

		"NameRaw": C(Seq(Range("az", "AZ", "__"), rep0(AlphaNum))),
		"Name":    Seq(V("Space"), Not(Keyword), V("NameRaw")),

		"Num": Seq(V("Space"), Ct(Seq(Cc("number"), C(Alt(
			Seq(Lit("0x"), rep1(Range("09", "af", "AF")), opt(Seq(opt(Set("uU")), Rep(Set("lL"), 2)))),
			Seq(rep1(digits), Seq(opt(Set("uU")), Rep(Set("lL"), 2))),
			Seq(
				Alt(
					Seq(rep1(digits), opt(Seq(Lit("."), rep1(digits)))),
					Seq(Lit("."), rep1(digits))),
				opt(Seq(Set("eE"), opt(Lit("-")), rep1(digits)))),
		))))),

		"SelfName": Seq(V("Space"), Lit("@"), Alt(
			Seq(Lit("@"), Alt(mark("self_class", V("NameRaw")), Cc("self.__class"))),
			mark("self", V("NameRaw")),
			Cc("self"))),
		"KeyName": Alt(V("SelfName"), Seq(V("Space"), mark("key_literal", V("NameRaw")))),
		"VarArg":  Seq(V("Space"), C(Lit("..."))),

		"File":        Seq(opt(V("Shebang")), Alt(V("Block"), Ct(empty()))),
		"Block":       Ct(Seq(V("Line"), rep0(Seq(rep1(V("Break")), V("Line"))))),
		"CheckIndent": ind.Check(),
		"Line":        Alt(Seq(V("CheckIndent"), V("Statement")), Seq(V("Space"), Look(V("Stop")))),

		"Statement": Cfn(Seq(
			pos(DAlt(7,
				V("Import"), V("While"), V("With"), V("For"), V("ForEach"), V("Switch"),
				V("Return"), V("Local"), V("Export"), V("BreakLoop"),
				Cfn(Seq(Ct(V("ExpList")), opt(Alt(V("Update"), V("Assign")))), "assignTransformFn"),
			)),
			V("Space"),
			opt(Seq(Alt(
				mark("if", Seq(key("if"), V("Exp"), opt(Seq(key("else"), V("Exp"))), V("Space"))),
				mark("unless", Seq(key("unless"), V("Exp"))),
				mark("comprehension", V("CompInner"))), V("Space"))),
		), "statementFn"),

		"Body": Alt(
			Seq(V("Space"), V("Break"), rep0(V("EmptyLine")), V("InBlock")),
			Ct(V("Statement"))),

		"Advance":       ind.Advance(),
		"PushIndent":    ind.Push(),
		"PreventIndent": ind.Prevent(),
		"PopIndent":     ind.Pop(),
		"InBlock":       Seq(V("Advance"), V("Block"), V("PopIndent")),

		"Local": Seq(key("local"), Alt(
			mark("declare_glob", Alt(op("*"), op("^"))),
			mark("declare_with_shadows", Ct(V("NameList"))))),

		"Import": mark("import", Seq(
			key("import"), Ct(V("ImportNameList")), rep0(V("SpaceBreak")), key("from"), V("Exp"))),
		"ImportName": Alt(Seq(sym("\\"), Ct(Seq(Cc("colon"), V("Name")))), V("Name")),
		"ImportNameList": Seq(rep0(V("SpaceBreak")), V("ImportName"),
			rep0(Seq(Alt(rep1(V("SpaceBreak")), Seq(sym(","), rep0(V("SpaceBreak")))), V("ImportName")))),

		"BreakLoop": Alt(Ct(Seq(key("break"), Cc("break"))), Ct(Seq(key("continue"), Cc("continue")))),

		"Return": mark("return", Seq(key("return"), Alt(mark("explist", V("ExpListLow")), C(empty())))),

		"WithExp": Cfn(Seq(Ct(V("ExpList")), opt(V("Assign"))), "assignTransformFn"),
		"With": mark("with", Seq(
			key("with"), DisableDo, V("WithExp"), PopDo, opt(key("do")), V("Body"))),

		"Switch": mark("switch", Seq(
			key("switch"), DisableDo, V("Exp"), PopDo, opt(key("do")),
			V("Space"), V("Break"), V("SwitchBlock"))),
		"SwitchBlock": Seq(rep0(V("EmptyLine")), V("Advance"),
			Ct(Seq(V("SwitchCase"), rep0(Seq(rep1(V("Break")), V("SwitchCase"))),
				opt(Seq(rep1(V("Break")), V("SwitchElse"))))), V("PopIndent")),
		"SwitchCase": mark("case", Seq(key("when"), Ct(V("ExpList")), opt(key("then")), V("Body"))),
		"SwitchElse": mark("else", Seq(key("else"), V("Body"))),

		"IfCond": Cfn(Seq(V("Exp"), opt(V("Assign"))), "singleAssignFn"),
		"IfElse": mark("else", Seq(
			opt(Seq(V("Break"), rep0(V("EmptyLine")), V("CheckIndent"))), key("else"), V("Body"))),
		"IfElseIf": mark("elseif", Seq(
			opt(Seq(V("Break"), rep0(V("EmptyLine")), V("CheckIndent"))), key("elseif"),
			pos(V("IfCond")), opt(key("then")), V("Body"))),
		"If": mark("if", Seq(
			key("if"), V("IfCond"), opt(key("then")), V("Body"),
			rep0(V("IfElseIf")), opt(V("IfElse")))),
		"Unless": mark("unless", Seq(
			key("unless"), V("IfCond"), opt(key("then")), V("Body"),
			rep0(V("IfElseIf")), opt(V("IfElse")))),

		"While": mark("while", Seq(
			key("while"), DisableDo, V("Exp"), PopDo, opt(key("do")), V("Body"))),

		"For": mark("for", Seq(
			key("for"), DisableDo, V("Name"), sym("="),
			Ct(Seq(V("Exp"), sym(","), V("Exp"), opt(Seq(sym(","), V("Exp"))))), PopDo,
			opt(key("do")), V("Body"))),
		"ForEach": mark("foreach", Seq(
			key("for"), Ct(V("AssignableNameList")), key("in"), DisableDo,
			Ct(Alt(Seq(sym("*"), mark("unpack", V("Exp"))), V("ExpList"))), PopDo,
			opt(key("do")), V("Body"))),

		"Do": mark("do", Seq(key("do"), V("Body"))),

		"Comprehension": mark("comprehension", Seq(
			sym("["), V("Exp"), V("CompInner"), sym("]"))),
		"TblComprehension": mark("tblcomprehension", Seq(
			sym("{"), Ct(Seq(V("Exp"), opt(Seq(sym(","), V("Exp"))))), V("CompInner"), sym("}"))),

		"CompInner": Ct(Seq(Alt(V("CompForEach"), V("CompFor")), rep0(V("CompClause")))),
		"CompForEach": mark("foreach", Seq(
			key("for"), Ct(V("AssignableNameList")), key("in"),
			Alt(Seq(sym("*"), mark("unpack", V("Exp"))), V("Exp")))),
		"CompFor": Seq(V("Space"), Ct(Seq(Cc("for"), Lit("for"), V("Name"), sym("="),
			Ct(Seq(V("Exp"), sym(","), V("Exp"), opt(Seq(sym(","), V("Exp"))))))), Not(AlphaNum)),
		"CompClause": Alt(V("CompFor"), V("CompForEach"), mark("when", Seq(key("when"), V("Exp")))),

		"Assign": mark("assign", Seq(sym("="), Alt(
			Ct(Alt(V("With"), V("If"), V("Switch"))),
			Ct(Alt(V("TableBlock"), V("ExpListLow")))))),

		"Update": mark("update", Seq(V("Space"), C(Trie(
			"..=", "+=", "-=", "*=", "/=", "%=",
			"or=", "and=", "&=", "|=", ">>=", "<<=")), V("Exp"))),

		"CharOperators": Seq(V("Space"), C(Set("+-*/%^><|&"))),
		"WordOperators": DAlt(9,
			op("or"), op("and"), op("<="), op(">="), op("~="), op("!="), op("=="),
			op(".."), op("<<"), op(">>"), op("//")),
		"BinaryOperator": Seq(Alt(V("WordOperators"), V("CharOperators")), rep0(V("SpaceBreak"))),

		"Assignable": Alt(Cmt(V("Chain"), "assignableCmt"), V("Name"), V("SelfName")),

		"Exp": Cfn(Seq(V("Value"), rep0(Seq(V("BinaryOperator"), V("Value")))), "expFn"),

		"SimpleValue": DAlt(6,
			V("If"), V("Unless"), V("Switch"), V("With"), V("ClassDecl"),
			V("ForEach"), V("For"), V("While"),
			Seq(CheckDo, V("Do")),
			mark("minus", Seq(sym("-"), Not(V("SomeSpace")), V("Exp"))),
			mark("length", Seq(sym("#"), V("Exp"))),
			mark("bitnot", Seq(sym("~"), V("Exp"))),
			mark("not", Seq(key("not"), V("Exp"))),
			V("TblComprehension"), V("TableLit"), V("Comprehension"), V("FunLit"), V("Num")),

		"ChainValue": Cfn(Seq(Alt(V("Chain"), V("Callable")), Ct(opt(V("InvokeArgs")))), "joinChainFn"),

		"Value": pos(DAlt(8,
			V("SimpleValue"),
			mark("table", Ct(V("KeyValueList"))),
			V("ChainValue"), V("String"))),
		"SliceValue": V("Exp"),

		"String": Alt(Seq(V("Space"), V("DoubleString")), Seq(V("Space"), V("SingleString")), V("LuaString")),
		"SingleString": mark("string", Seq(
			C(Lit("'")), C(rep0(Alt(Lit("\\'"), Lit("\\\\"), Diff(Any(1), Lit("'"))))), Lit("'"))),
		"DoubleString": mark("string", Seq(
			C(Lit("\"")),
			rep0(Alt(C(rep1(Diff(V("DoubleStringInner"), Lit("#{")))), V("DoubleStringInterp"))),
			Lit("\""))),
		"DoubleStringInner":  Alt(Lit("\\\""), Lit("\\\\"), Diff(Any(1), Lit("\""))),
		"DoubleStringInterp": mark("interpolate", Seq(Lit("#{"), V("Exp"), sym("}"))),

		"LuaString": Cfn(Seq(
			V("Space"), Lit("["), Cp(), Cg(rep0(Lit("=")), "lua_eq"), Cp(), Lit("["),
			opt(V("Break")), C(rep0(Diff(Any(1), V("LuaStringClose")))), V("LuaStringClose")),
			"luaStringFn"),
		"LuaStringClose": Seq(Lit("]"), Cmb("lua_eq"), Lit("]")),

		"Callable": DAlt(1, pos(mark("ref", V("Name"))), V("SelfName"), V("VarArg"),
			mark("parens", V("Parens"))),
		"Parens": Seq(sym("("), rep0(V("SpaceBreak")), V("Exp"), rep0(V("SpaceBreak")), sym(")")),

		"FnArgs": Alt(
			Seq(Lit("("), rep0(V("SpaceBreak")), Ct(opt(V("FnArgsExpList"))),
				rep0(V("SpaceBreak")), sym(")")),
			Seq(sym("!"), Not(Lit("=")), Ct(empty()))),
		"FnArgsExpList": Seq(V("Exp"), rep0(Seq(Alt(V("Break"), sym(",")), V("White"), V("Exp")))),

		"Chain": Alt(
			Ct(Seq(Cc("chain"), Alt(V("Callable"), V("String"), Not(Set(".\\"))), V("ChainItems"))),
			Ct(Seq(Cc("chain"), V("Space"),
				Alt(Seq(V("DotChainItem"), opt(V("ChainItems"))), V("ColonChain"))))),

		"ChainItems": Alt(Seq(rep1(V("ChainItem")), opt(V("ColonChain"))), V("ColonChain")),
		"ChainItem": DAlt(2, V("Invoke"), V("DotChainItem"), V("Slice"),
			Seq(mark("index", Seq(Lit("["), V("Exp"))), sym("]"))),
		"DotChainItem":   mark("dot", Seq(Lit("."), V("NameRaw"))),
		"ColonChainItem": mark("colon", Seq(Lit("\\"), V("NameRaw"))),
		"ColonChain":     Seq(V("ColonChainItem"), opt(Seq(V("Invoke"), opt(V("ChainItems"))))),

		"Slice": mark("slice", Seq(
			Lit("["), Alt(V("SliceValue"), Cc(1)), sym(","), Alt(V("SliceValue"), Cc("")),
			opt(Seq(sym(","), V("SliceValue"))), sym("]"))),

		"Invoke": DAlt(4,
			mark("call", V("FnArgs")),
			Ct(Seq(Cc("call"), Ct(V("SingleString")))),
			Ct(Seq(Cc("call"), Ct(V("DoubleString")))),
			Seq(Look(Lit("[")), Ct(Seq(Cc("call"), Ct(V("LuaString")))))),

		"TableValue": Alt(V("KeyValue"), Ct(V("Exp"))),
		"TableLit": mark("table", Seq(
			sym("{"), Ct(Seq(
				opt(V("TableValueList")), opt(sym(",")),
				opt(Seq(V("SpaceBreak"), V("TableLitLine"),
					rep0(Seq(opt(sym(",")), V("SpaceBreak"), V("TableLitLine"))), opt(sym(",")))),
			)), V("White"), sym("}"))),
		"TableValueList": Seq(V("TableValue"), rep0(Seq(sym(","), V("TableValue")))),
		"TableLitLine":   Alt(Seq(V("PushIndent"), V("TableValueList"), V("PopIndent")), V("Space")),

		"TableBlockInner": Ct(Seq(V("KeyValueLine"), rep0(Seq(rep1(V("SpaceBreak")), V("KeyValueLine"))))),
		"TableBlock": mark("table", Seq(
			rep1(V("SpaceBreak")), V("Advance"), V("TableBlockInner"), V("PopIndent"))),

		"ClassDecl": mark("class", Seq(
			key("class"), Not(Lit(":")),
			// anonymous classes have name slot set to false
			Alt(V("Assignable"), Cc(false)),
			opt(Alt(Seq(key("extends"), V("PreventIndent"), V("Exp"), V("PopIndent")), C(empty()))),
			Alt(V("ClassBlock"), Ct(empty())))),

		"ClassBlock": Seq(rep1(V("SpaceBreak")), V("Advance"),
			Ct(Seq(V("ClassLine"), rep0(Seq(rep1(V("SpaceBreak")), V("ClassLine"))))), V("PopIndent")),
		"ClassLine": Seq(V("CheckIndent"), Seq(
			Alt(mark("props", V("KeyValueList")),
				mark("stm", V("Statement")),
				mark("stm", V("Exp"))), opt(sym(",")))),

		"Export": mark("export", Seq(key("export"), DAlt(3,
			Seq(Cc("class"), V("ClassDecl")),
			op("*"), op("^"),
			Seq(Ct(V("NameList")), opt(Seq(sym("="), Ct(V("ExpListLow")))))))),

		"KeyValue": Alt(
			// {:name} shorthand expands to the key/value pair
			Cfn(Seq(sym(":"), Not(V("SomeSpace")), V("Name"), Cp()), "keyValueShorthandFn"),
			Ct(Seq(
				DAlt(5, V("KeyName"), Seq(sym("["), V("Exp"), sym("]")),
					Seq(V("Space"), V("DoubleString")), Seq(V("Space"), V("SingleString"))),
				Lit(":"),
				Alt(V("Exp"), V("TableBlock"), Seq(rep1(V("SpaceBreak")), V("Exp")))))),
		"KeyValueList": Seq(V("KeyValue"), rep0(Seq(sym(","), V("KeyValue")))),
		"KeyValueLine": Seq(V("CheckIndent"), V("KeyValueList"), opt(sym(","))),

		"FnArgsDef": Alt(
			Seq(sym("("), V("White"), Ct(opt(V("FnArgDefList"))),
				Alt(Seq(key("using"), Ct(Alt(V("NameList"), Seq(V("Space"), Lit("nil"))))), Ct(empty())),
				V("White"), sym(")")),
			Seq(Ct(empty()), Ct(empty()))),
		"FnArgDefList": Alt(
			Seq(V("FnArgDef"),
				rep0(Seq(Alt(sym(","), V("Break")), V("White"), V("FnArgDef"))),
				rep0(Seq(Alt(sym(","), V("Break")), V("White"), Ct(V("VarArg"))))),
			Ct(V("VarArg"))),
		"FnArgDef": Ct(Seq(Alt(V("Name"), V("SelfName"), V("TableLit")), opt(Seq(sym("="), V("Exp"))))),

		"FunLit": mark("fndef", Seq(
			V("FnArgsDef"),
			Alt(Seq(sym("->"), Cc("slim")), Seq(sym("=>"), Cc("fat"))),
			Alt(V("Body"), Ct(empty())))),

		"NameList":           Seq(V("Name"), rep0(Seq(sym(","), V("Name")))),
		"NameOrDestructure":  Alt(V("Name"), V("TableLit")),
		"AssignableNameList": Seq(V("NameOrDestructure"), rep0(Seq(sym(","), V("NameOrDestructure")))),

		"ExpList":    Seq(V("Exp"), rep0(Seq(sym(","), V("Exp")))),
		"ExpListLow": Seq(V("Exp"), rep0(Seq(Alt(sym(","), sym(";")), V("Exp")))),

		"InvokeArgs": Seq(Not(Lit("-")), Alt(
			Seq(V("ExpList"), opt(Alt(
				Seq(sym(","), Alt(V("TableBlock"),
					Seq(V("SpaceBreak"), V("Advance"), V("ArgBlock"), opt(V("TableBlock"))))),
				V("TableBlock")))),
			V("TableBlock"))),
		"ArgBlock": Seq(V("ArgLine"), rep0(Seq(sym(","), V("SpaceBreak"), V("ArgLine"))), V("PopIndent")),
		"ArgLine":  Seq(V("CheckIndent"), V("ExpList")),
	}
	// the rules pgen memoizes (memo_pos/memo_end slots in slow.lua)
	return &Grammar{Start: "Root", Rules: g, Memoized: []string{"Break", "Comment",
		"DoubleStringInner", "EmptyLine", "Shebang", "SomeSpace", "Space", "SpaceBreak", "Stop", "White"},
		Packrat: []string{"Exp"}}
}

func isWord(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
