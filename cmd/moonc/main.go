// Command moonc compiles MoonScript to Lua (Go port of moonc v0.8.0).
//
// It mirrors bin/moonc and moonscript/cmd/moonc.moon: the same flags,
// argparse-style usage errors, output paths, messages and exit codes.
// --lint runs the lint package (moonscript/cmd/lint.moon). --watch and
// --transform are recognized but not supported.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"syscall"
	"time"

	moonscript "github.com/tarantool-contrib/go-moonscript"
	"github.com/tarantool-contrib/go-moonscript/ast"
	"github.com/tarantool-contrib/go-moonscript/compiler"
	"github.com/tarantool-contrib/go-moonscript/lint"
	"github.com/tarantool-contrib/go-moonscript/parse"
)

const version = "0.8.0"

var (
	lintStages  = lint.Stages
	lintFormats = lint.Formats
)

const usageHead = `Usage: %PROG%
       ([--lint-stage {global_access,unused,constant_assign,import_overwrite}] | [--exclude-lint-stage {global_access,unused,constant_assign,import_overwrite}])
       ([-t <output_to>] | [-o <o>] | [-p] | [-T] | [-b] | [-X]) [-h]
       [-l] [--lint-format {default,compact}] [-v] [-w] [--correlate]
       [--transform <transform>] [-]`

const usageArgs = `
       <file/directory> [<file/directory>] ...`

const helpArgs = `

Arguments:
   file/directory`

const helpOptions = `

Options:
   -h, --help            Show this help message and exit.
   -l, --lint            Perform a lint on the file instead of compiling
   --lint-stage {global_access,unused,constant_assign,import_overwrite}
                         Limit lint to the given stage name (repeatable)
   --exclude-lint-stage {global_access,unused,constant_assign,import_overwrite}
                         Lint with all stages except the given stage name (repeatable)
   --lint-format {default,compact}
                         Set the output format of lint results
   -v, --version         Print version
   -w, --watch           Watch file/directory for updates
   --correlate           Write output where line numbers match the source line numbers
   --transform <transform>
                         Transform syntax tree with module
            -t <output_to>,
   --output-to <output_to>
                         Specify where to place compiled files
   -o <o>                Write output to file
   -p                    Write output to standard output
   -T                    Write parse tree instead of code (to stdout)
   -b                    Write parse and compile time instead of code(to stdout)
   -X                    Write line rewrite map instead of code (to stdout)
   -                     Read from standard in, print to standard out`

// --- argument parsing (a small subset of argparse) ---------------------------

type optSpec struct {
	key     string // result key
	takes   bool   // takes one argument
	multi   bool   // count "*" (repeatable, collects values)
	mutex   int    // mutex group, 0 for none
	choices []string
}

var optSpecs = map[string]*optSpec{}

func init() {
	add := func(spec *optSpec, names ...string) {
		for _, n := range names {
			optSpecs[n] = spec
		}
	}
	add(&optSpec{key: "help"}, "-h", "--help")
	add(&optSpec{key: "lint"}, "-l", "--lint")
	add(&optSpec{key: "lint_stage", takes: true, multi: true, mutex: 1, choices: lintStages}, "--lint-stage")
	add(&optSpec{key: "exclude_lint_stage", takes: true, multi: true, mutex: 1, choices: lintStages}, "--exclude-lint-stage")
	add(&optSpec{key: "lint_format", takes: true, choices: lintFormats}, "--lint-format")
	add(&optSpec{key: "version"}, "-v", "--version")
	add(&optSpec{key: "watch"}, "-w", "--watch")
	add(&optSpec{key: "correlate"}, "--correlate")
	add(&optSpec{key: "transform", takes: true}, "--transform")
	add(&optSpec{key: "output_to", takes: true, mutex: 2}, "-t", "--output-to")
	add(&optSpec{key: "o", takes: true, mutex: 2}, "-o")
	add(&optSpec{key: "p", mutex: 2}, "-p")
	add(&optSpec{key: "T", mutex: 2}, "-T")
	add(&optSpec{key: "b", mutex: 2}, "-b")
	add(&optSpec{key: "X", mutex: 2}, "-X")
	add(&optSpec{key: "stdin"}, "-")
}

type cliOpts struct {
	flags  map[string]bool
	values map[string]string
	multi  map[string][]string
	files  []string
}

type argParser struct {
	prog      string
	readStdin bool

	opts *cliOpts

	open     *optSpec // option waiting for its argument
	openName string
	usedName map[*optSpec]string
	mutexes  map[int]*optSpec
}

func (p *argParser) usage() string {
	u := strings.ReplaceAll(usageHead, "%PROG%", p.prog)
	if !p.readStdin {
		u += usageArgs
	}
	return u
}

func (p *argParser) help() string {
	h := p.usage()
	if !p.readStdin {
		h += helpArgs
	}
	return h + helpOptions
}

func (p *argParser) error(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s\n\nError: %s\n", p.usage(), fmt.Sprintf(format, args...))
	os.Exit(1)
}

// getTip is argparse's get_tip: suggest option names one edit away.
func getTip(wrong string) string {
	pool := map[string][]string{}
	var names []string
	for name := range optSpecs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for i := 0; i < len(name); i++ {
			pn := name[:i] + name[i+1:]
			pool[pn] = append(pool[pn], name)
		}
	}
	possible := map[string]bool{}
	for i := 0; i <= len(wrong); i++ {
		pn := wrong
		if i < len(wrong) {
			pn = wrong[:i] + wrong[i+1:]
		}
		if optSpecs[pn] != nil {
			possible[pn] = true
		} else {
			for _, name := range pool[pn] {
				possible[name] = true
			}
		}
	}
	switch len(possible) {
	case 0:
		return ""
	case 1:
		for name := range possible {
			return "\nDid you mean '" + name + "'?"
		}
	}
	var arr []string
	for name := range possible {
		arr = append(arr, "'"+name+"'")
	}
	sort.Strings(arr)
	return "\nDid you mean one of these: " + strings.Join(arr, " ") + "?"
}

func (p *argParser) getOption(name string) *optSpec {
	spec := optSpecs[name]
	if spec == nil {
		p.error("unknown option '%s'%s", name, getTip(name))
	}
	return spec
}

func (p *argParser) close() {
	if p.open != nil {
		p.error("option '%s' requires an argument", p.openName)
	}
}

func (p *argParser) invoke(spec *optSpec, name string) {
	p.close()
	p.usedName[spec] = name
	if spec.mutex != 0 {
		if used := p.mutexes[spec.mutex]; used != nil && used != spec {
			p.error("option '%s' can not be used together with option '%s'", name, p.usedName[used])
		}
		p.mutexes[spec.mutex] = spec
	}
	if spec.takes {
		p.open, p.openName = spec, name
		return
	}
	switch spec.key {
	case "help":
		fmt.Println(p.help())
		os.Exit(0)
	case "version":
		fmt.Println("MoonScript version " + version)
		os.Exit(0)
	}
	p.opts.flags[spec.key] = true
}

func (p *argParser) passValue(spec *optSpec, name, value string) {
	if spec.choices != nil {
		ok := false
		for _, c := range spec.choices {
			if c == value {
				ok = true
			}
		}
		if !ok {
			p.error("argument for option '%s' must be one of '%s'", name, strings.Join(spec.choices, "', '"))
		}
	}
	if spec.multi {
		p.opts.multi[spec.key] = append(p.opts.multi[spec.key], value)
	} else {
		p.opts.values[spec.key] = value
	}
}

func (p *argParser) pass(arg string) {
	if p.open != nil {
		spec, name := p.open, p.openName
		p.open = nil
		p.passValue(spec, name, arg)
		return
	}
	if p.readStdin {
		p.error("too many arguments")
	}
	p.opts.files = append(p.opts.files, arg)
}

func parseArgs(prog string, args []string) *cliOpts {
	p := &argParser{
		prog: prog,
		opts: &cliOpts{
			flags:  map[string]bool{},
			values: map[string]string{},
			multi:  map[string][]string{},
		},
		usedName: map[*optSpec]string{},
		mutexes:  map[int]*optSpec{},
	}

	var rest []string
	for _, a := range args {
		if a == "-" {
			p.readStdin = true
		} else {
			rest = append(rest, a)
		}
	}

	handleOptions := true
	for _, arg := range rest {
		if handleOptions && len(arg) > 1 && arg[0] == '-' {
			switch {
			case arg == "--":
				p.close()
				handleOptions = false
			case arg[1] == '-':
				if eq := strings.IndexByte(arg, '='); eq >= 0 {
					name := arg[:eq]
					spec := p.getOption(name)
					if !spec.takes {
						p.error("option '%s' does not take arguments", name)
					}
					p.invoke(spec, name)
					p.pass(arg[eq+1:])
				} else {
					p.invoke(p.getOption(arg), arg)
				}
			default:
				for i := 1; i < len(arg); i++ {
					name := "-" + arg[i:i+1]
					spec := p.getOption(name)
					p.invoke(spec, name)
					if i != len(arg)-1 && spec.takes {
						p.pass(arg[i+1:])
						break
					}
				}
			}
			continue
		}
		p.pass(arg)
	}
	p.close()

	if !p.readStdin && len(p.opts.files) == 0 {
		p.error("missing argument 'file/directory'")
	}
	p.opts.flags["stdin"] = p.readStdin
	return p.opts
}

// --- moonscript/cmd/moonc.moon -------------------------------------------------

const dirsep = "/"

// normalizeDir strips excess / and ensures path ends with /
func normalizeDir(path string) string {
	return strings.TrimRight(path, dirsep) + dirsep
}

// parseDir parses the directory out of a path
func parseDir(path string) string {
	if i := strings.LastIndex(path, dirsep); i >= 0 {
		return path[:i+1]
	}
	return ""
}

// parseFile parses the filename out of a path
func parseFile(path string) string {
	return path[len(parseDir(path)):]
}

// convertPath converts .moon to a .lua path for calculating compile target
func convertPath(path string) string {
	if strings.HasSuffix(path, ".moon") {
		return strings.TrimSuffix(path, ".moon") + ".lua"
	}
	return path + ".lua"
}

func isAbsPath(path string) bool {
	return strings.HasPrefix(path, dirsep)
}

// pathToTarget calculates where a path should be compiled to. targetDir:
// the directory to place the file (-t flag, "" for none); baseDir: the
// directory where the file came from when globbing recursively ("" for
// none).
func pathToTarget(path, targetDir, baseDir string, hasTarget, hasBase bool) string {
	target := convertPath(path)

	if hasTarget {
		targetDir = normalizeDir(targetDir)
	}

	if hasBase && hasTarget {
		// one directory back: ^(.-)[^/]*[/]?$
		head := strings.TrimSuffix(baseDir, dirsep)
		head = parseDir(head)
		target = strings.TrimPrefix(target, head)
	}

	if hasTarget {
		if isAbsPath(target) {
			target = parseFile(target)
		}
		target = targetDir + target
	}
	return target
}

func formatTime(d time.Duration) string {
	return fmt.Sprintf("%.3fms", d.Seconds()*1000)
}

type compileOpts struct {
	fname         string
	print         bool
	benchmark     bool
	showPosmap    bool
	showParseTree bool
	correlate     bool
}

// errHandled is compile_file_text's `true` result: an option like -T
// already handled the output.
var errHandled = errors.New("handled")

// compileFileText compiles moon text to lua; returns errHandled when an
// option already printed the output.
func compileFileText(text string, opts compileOpts) (string, error) {
	parseStart := time.Now()

	tree, err := parse.String(text)
	if err != nil {
		return "", err
	}
	parseTime := time.Since(parseStart)

	if opts.showParseTree {
		fmt.Println(ast.Tree(tree))
		return "", errHandled
	}

	compileStart := time.Now()

	code, posmap, cerr := compiler.Tree(tree, compiler.Options{
		Correlate: opts.correlate,
		Source:    text,
	})
	if cerr != nil {
		return "", errors.New(compiler.FormatError(cerr.Msg, cerr.Pos, text))
	}
	compileTime := time.Since(compileStart)

	if opts.showPosmap {
		fmt.Println("Pos\tLua\t>>\tMoon")
		fmt.Println(moonscript.DebugPosmap(posmap, text, code))
		return "", errHandled
	}

	if opts.benchmark {
		fname := opts.fname
		if fname == "" {
			fname = "stdin"
		}
		fmt.Println(strings.Join([]string{
			fname,
			"Parse time  \t" + formatTime(parseTime),
			"Compile time\t" + formatTime(compileTime),
			"",
		}, "\n"))
		return "", errHandled
	}

	return code, nil
}

// luaIOError formats an io.open failure like Lua: "<fname>: <strerror>".
func luaIOError(fname string, err error) error {
	msg := err.Error()
	var errno syscall.Errno
	if errors.As(err, &errno) {
		msg = errno.Error()
	}
	if msg != "" {
		msg = strings.ToUpper(msg[:1]) + msg[1:]
	}
	return errors.New(fname + ": " + msg)
}

func writeFile(fname, code string) error {
	if dir := parseDir(fname); dir != "" {
		_ = os.MkdirAll(dir, 0o777)
	}
	if err := os.WriteFile(fname, []byte(code+"\n"), 0o666); err != nil {
		return luaIOError(fname, err)
	}
	return nil
}

func compileAndWrite(src, dest string, opts compileOpts) error {
	text, err := os.ReadFile(src)
	if err != nil {
		return errors.New("Can't find file") //nolint:staticcheck // ST1005: user-visible message of upstream moonc
	}

	code, err := compileFileText(string(text), opts)
	if err != nil {
		if errors.Is(err, errHandled) {
			return nil
		}
		return err
	}

	if opts.print {
		fmt.Println(code)
		return nil
	}

	return writeFile(dest, code)
}

// --- bin/moonc -------------------------------------------------------------------

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

// readDirNames lists a directory in the order the OS returns entries, like
// lfs.dir.
func readDirNames(dir string) []string {
	f, err := os.Open(dir)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	names, _ := f.Readdirnames(-1)
	return names
}

func scanDirectory(root string, collected []string) []string {
	root = normalizeDir(root)
	for _, fname := range readDirNames(root) {
		if strings.HasPrefix(fname, ".") {
			continue
		}
		fullPath := root + fname
		if isDir(fullPath) {
			collected = scanDirectory(fullPath, collected)
		} else if strings.HasSuffix(fname, ".moon") {
			collected = append(collected, fullPath)
		}
	}
	return collected
}

type fileTuple struct{ src, target string }

func unsupported(what string) {
	fmt.Fprintf(os.Stderr, "%s is not supported by this port of moonc\n", what)
	os.Exit(1)
}

// lintOptions are the options bin/moonc passes to lint_file: stages nil
// runs all of them.
func lintOptions(opts *cliOpts) lint.Options {
	var stages []string
	included, excluded := opts.multi["lint_stage"], opts.multi["exclude_lint_stage"]
	if len(included) > 0 {
		stages = included
	} else if len(excluded) > 0 {
		skip := map[string]bool{}
		for _, name := range excluded {
			skip[name] = true
		}
		stages = []string{}
		for _, name := range lintStages {
			if !skip[name] {
				stages = append(stages, name)
			}
		}
	}
	return lint.Options{Stages: stages, Format: opts.values["lint_format"]}
}

// lintStdin is the `moonc -l -` branch; returns the exit status.
func lintStdin(text string, stderr io.Writer, opts lint.Options) int {
	res, ok, err := lint.Code(text, "stdin", nil, opts)
	var crash *lint.Crash
	switch {
	case errors.As(err, &crash):
		_, _ = fmt.Fprint(stderr, crash.InterpreterMessage())
		return 1
	case ok:
		_, _ = fmt.Fprint(stderr, res+"\n")
		return 1
	case err != nil:
		_, _ = fmt.Fprint(stderr, err.Error()+"\n")
		return 1
	}
	return 0
}

// lintFiles is the `moonc -l files...` branch; returns the exit status. A
// crash (compile error) ends the run like the reference's uncaught error.
func lintFiles(files []fileTuple, stderr io.Writer, opts lint.Options) int {
	failed := false
	for _, f := range files {
		res, ok, err := lint.File(f.src, opts)
		var crash *lint.Crash
		switch {
		case errors.As(err, &crash):
			_, _ = fmt.Fprint(stderr, crash.InterpreterMessage())
			return 1
		case ok:
			failed = true
			// compact format lines identify their file, no separator needed
			sep := "\n\n"
			if opts.Format == "compact" {
				sep = "\n"
			}
			_, _ = fmt.Fprint(stderr, res+sep)
		case err != nil:
			failed = true
			_, _ = fmt.Fprint(stderr, f.src+"\n"+err.Error()+"\n\n")
		}
	}
	if failed {
		return 1
	}
	return 0
}

// collectFiles creates the tuples of input and target for the file/directory
// arguments, without duplicate targets.
func collectFiles(opts *cliOpts) []fileTuple {
	outputTo, hasOutputTo := opts.values["output_to"]
	var files []fileTuple
	for _, input := range opts.files {
		if isDir(input) {
			for _, sub := range scanDirectory(input, nil) {
				files = append(files, fileTuple{sub, pathToTarget(sub, outputTo, input, hasOutputTo, true)})
			}
		} else {
			files = append(files, fileTuple{input, pathToTarget(input, outputTo, "", hasOutputTo, false)})
		}
	}

	// remove_dups by target
	seen := map[string]bool{}
	var unique []fileTuple
	for _, f := range files {
		if !seen[f.target] {
			unique = append(unique, f)
			seen[f.target] = true
		}
	}
	return unique
}

func main() {
	opts := parseArgs(os.Args[0], os.Args[1:])
	flags := opts.flags
	_, hasOutputTo := opts.values["output_to"]
	outFile, hasOutFile := opts.values["o"]
	_, hasTransform := opts.values["transform"]

	if flags["stdin"] {
		if flags["watch"] || hasOutputTo {
			fmt.Fprint(os.Stderr, "stdin can't be used with --watch or --output-to\n")
			os.Exit(1)
		}

		textBytes, _ := io.ReadAll(os.Stdin)
		text := string(textBytes)

		if flags["lint"] {
			os.Exit(lintStdin(text, os.Stderr, lintOptions(opts)))
		}
		if hasTransform {
			unsupported("--transform")
		}

		code, err := compileFileText(text, compileOpts{
			fname:         "stdin",
			benchmark:     flags["b"],
			showPosmap:    flags["X"],
			showParseTree: flags["T"],
			correlate:     flags["correlate"],
		})
		if err != nil && !errors.Is(err, errHandled) {
			fmt.Fprint(os.Stderr, err.Error()+"\n")
			os.Exit(1)
		}

		if err == nil {
			if hasOutFile {
				if werr := writeFile(outFile, code); werr != nil {
					fmt.Fprint(os.Stderr, werr.Error()+"\n")
					os.Exit(1)
				}
			} else {
				fmt.Println(code)
			}
		}
		os.Exit(0)
	}

	files := collectFiles(opts)

	if flags["watch"] {
		unsupported("--watch")
	}
	if flags["lint"] {
		os.Exit(lintFiles(files, os.Stderr, lintOptions(opts)))
	}
	if hasTransform {
		unsupported("--transform")
	}

	for _, f := range files {
		target := f.target
		if hasOutFile {
			target = outFile
		}

		err := compileAndWrite(f.src, target, compileOpts{
			print:         flags["p"],
			fname:         f.src,
			benchmark:     flags["b"],
			showPosmap:    flags["X"],
			showParseTree: flags["T"],
			correlate:     flags["correlate"],
		})
		if err != nil {
			fmt.Fprint(os.Stderr, f.src+"\t"+err.Error()+"\n")
			os.Exit(1)
		}
	}
}
