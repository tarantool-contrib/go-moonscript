package main

// Emulation of the argparse 0.7.2 parser bin/moon.moon builds:
//
//	argparser = argparse! name: "moon"
//	argparser\argument("script")\args "?"
//	argparser\argument("args")\args "*"
//	argparser\flag "--coverage -c", "Collect and print code coverage"
//	argparser\flag "-d", "Disable stack trace rewriting"
//	argparser\option "--execute -e", "Execute MoonScript code string"
//	argparser\flag "--version -v", "Print version information"
//
// parseArgs follows ParseState:parse (short option bundling, "-evalue",
// "--execute=value", "--", repeated options overwrite) and prints the same
// usage, help and error texts, with the same exit codes.

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

const usage = `Usage: moon [-h] [--coverage] [-d] [--execute <execute>] [--version]
       [<script>] [<args>] ...`

const help = usage + `

Arguments:
   script
   args

Options:
   -h, --help            Show this help message and exit.
   --coverage, -c        Collect and print code coverage
   -d                    Disable stack trace rewriting
   --execute <execute>,  Execute MoonScript code string
          -e <execute>
   --version, -v         Print version information`

type element struct {
	target  string // result key
	maxargs int    // 0 for flags
	help    bool
}

var (
	optHelp     = &element{target: "help", help: true}
	optCoverage = &element{target: "coverage"}
	optD        = &element{target: "d"}
	optExecute  = &element{target: "execute", maxargs: 1}
	optVersion  = &element{target: "version"}
)

var optionNames = map[string]*element{
	"-h": optHelp, "--help": optHelp,
	"--coverage": optCoverage, "-c": optCoverage,
	"-d":        optD,
	"--execute": optExecute, "-e": optExecute,
	"--version": optVersion, "-v": optVersion,
}

// opts is the argparse result moon.moon reads.
type opts struct {
	script   *string
	args     []string
	flags    map[string]bool // coverage, d, version
	execute  *string
	exitCode int // >= 0: stop with this exit status (help or usage error)
}

// parseError is ParseState:error -> Parser:error.
type parseError string

type parseState struct {
	stdout, stderr io.Writer
	res            *opts
	option         *element // open option awaiting its argument
	optionName     string
	argumentI      int // 0: script, 1: args
	handleOptions  bool
}

// parseArgs parses args (bin/moon.moon passes the leading flags and the
// script name). On help or a usage error it has already printed the text
// and res.exitCode is the exit status.
func parseArgs(args []string, stdout, stderr io.Writer) (res *opts) {
	p := &parseState{stdout: stdout, stderr: stderr, handleOptions: true,
		res: &opts{flags: map[string]bool{}, exitCode: -1}}
	defer func() {
		if r := recover(); r != nil {
			msg, ok := r.(parseError)
			if !ok {
				panic(r)
			}
			_, _ = fmt.Fprintf(stderr, "%s\n\nError: %s\n", usage, string(msg))
			res = &opts{exitCode: 1}
		}
	}()
	for _, arg := range args {
		if p.res.exitCode >= 0 {
			return p.res
		}
		plain := true
		if p.handleOptions && strings.HasPrefix(arg, "-") && len(arg) > 1 {
			plain = false
			if arg[1] == '-' {
				if len(arg) == 2 {
					p.close()
					p.handleOptions = false
				} else if eq := strings.IndexByte(arg, '='); eq >= 0 {
					name := arg[:eq]
					o := p.getOption(name)
					if o.maxargs <= 0 {
						p.error("option '%s' does not take arguments", name)
					}
					p.invoke(o, name)
					p.pass(arg[eq+1:])
				} else {
					p.invoke(p.getOption(arg), arg)
				}
			} else {
				for i := 1; i < len(arg); i++ {
					name := "-" + arg[i:i+1]
					o := p.getOption(name)
					p.invoke(o, name)
					if p.res.exitCode >= 0 {
						return p.res
					}
					if i != len(arg)-1 && o.maxargs > 0 {
						p.pass(arg[i+1:])
						break
					}
				}
			}
		}
		if plain {
			p.pass(arg)
		}
	}
	if p.res.exitCode >= 0 {
		return p.res
	}
	p.close()
	return p.res
}

func (p *parseState) error(format string, args ...any) {
	panic(parseError(fmt.Sprintf(format, args...)))
}

func (p *parseState) getOption(name string) *element {
	o, ok := optionNames[name]
	if !ok {
		p.error("unknown option '%s'%s", name, getTip(name))
	}
	return o
}

func (p *parseState) invoke(o *element, name string) {
	p.close()
	if o.maxargs == 0 {
		// flags close immediately; their action runs on close
		if o.help {
			_, _ = fmt.Fprintln(p.stdout, help)
			p.res.exitCode = 0
			return
		}
		p.res.flags[o.target] = true
		return
	}
	p.option, p.optionName = o, name
}

func (p *parseState) pass(arg string) {
	switch {
	case p.option != nil:
		v := arg
		p.res.execute = &v
		p.option = nil
	case p.argumentI == 0:
		v := arg
		p.res.script = &v
		p.argumentI = 1
	default:
		p.res.args = append(p.res.args, arg)
	}
}

func (p *parseState) close() {
	if p.option != nil {
		p.error("option '%s' requires an argument", p.optionName)
	}
}

// getTip is argparse's get_tip: names one deletion away from wrongName.
func getTip(wrongName string) string {
	pool := map[string][]string{}
	for name := range optionNames {
		for i := 0; i < len(name); i++ {
			possible := name[:i] + name[i+1:]
			pool[possible] = append(pool[possible], name)
		}
	}
	possible := map[string]bool{}
	for i := 0; i <= len(wrongName); i++ {
		var cand string
		if i < len(wrongName) {
			cand = wrongName[:i] + wrongName[i+1:]
		} else {
			cand = wrongName
		}
		if _, ok := optionNames[cand]; ok {
			possible[cand] = true
		} else {
			for _, name := range pool[cand] {
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
	var names []string
	for name := range possible {
		names = append(names, "'"+name+"'")
	}
	sort.Strings(names)
	return "\nDid you mean one of these: " + strings.Join(names, " ") + "?"
}
