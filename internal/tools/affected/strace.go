package main

import (
	"bufio"
	"io"
	"path"
	"strconv"
	"strings"
)

// The output of strace -f -y (strace 6 or newer, which decorates
// AT_FDCWD with the working directory): the paths that file system calls
// name.

// tracedSyscalls are the calls a test can read a file or a directory, or
// probe for one, with.
var tracedSyscalls = []string{
	"open", "openat", "openat2", "creat",
	"stat", "lstat", "newfstatat", "statx",
	"access", "faccessat", "faccessat2",
	"readlink", "readlinkat",
	"execve", "execveat",
	// the processes: a thread or a child runs the program of its parent
	// until it calls execve
	"clone", "clone3", "fork", "vfork",
}

// forkSyscalls create a process or a thread, whose id they return.
var forkSyscalls = map[string]bool{"clone": true, "clone3": true, "fork": true, "vfork": true}

// atSyscalls take a directory file descriptor before the path.
var atSyscalls = map[string]bool{
	"openat": true, "openat2": true, "newfstatat": true, "statx": true,
	"faccessat": true, "faccessat2": true, "readlinkat": true, "execveat": true,
}

// An Access is a path a traced process named.
type Access struct {
	Path string // absolute, cleaned
	OK   bool   // the call succeeded
	Exe  string // the program of the process, "" for the traced one's
}

// ParseStrace reads the output of strace -f -y and returns the absolute
// paths it names, in order. dir is the working directory of the first
// process, for a relative path of a call without a directory descriptor
// before the process made any *at call (whose AT_FDCWD names its working
// directory).
func ParseStrace(r io.Reader, dir string) ([]Access, error) {
	type access struct {
		Access
		pid string
	}
	var accesses []access
	parent := map[string]string{} // pid: the process that created it
	exe := map[string]string{}    // pid: the program it executed
	first := ""
	pending := map[string]string{} // pid: the start of an unfinished call
	cwd := map[string]string{}     // pid: its working directory, as last seen
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		line := sc.Text()
		pid, rest, ok := strings.Cut(line, " ")
		if !ok || pid == "" || pid[0] < '0' || pid[0] > '9' {
			continue
		}
		rest = strings.TrimLeft(rest, " ")
		if start, ok := strings.CutSuffix(rest, " <unfinished ...>"); ok {
			pending[pid] = start
			continue
		}
		if strings.HasPrefix(rest, "<... ") {
			_, after, ok := strings.Cut(rest, " resumed>")
			start, had := pending[pid]
			if !ok || !had {
				continue
			}
			delete(pending, pid)
			rest = start + after
		}
		name, args, ok := strings.Cut(rest, "(")
		if !ok || strings.ContainsAny(name, " -+") {
			continue // a signal, an exit
		}
		// the result, after ")", spaces that align it, "= "
		i := strings.LastIndex(args, " = ")
		if i < 0 {
			continue
		}
		ret := strings.TrimSpace(args[i+len(" = "):])
		args = strings.TrimRight(args[:i], " ")
		if args, ok = strings.CutSuffix(args, ")"); !ok {
			continue
		}
		succeeded := !strings.HasPrefix(ret, "-")
		if first == "" {
			first = pid
		}
		if forkSyscalls[name] {
			if child, _, _ := strings.Cut(ret, " "); succeeded && child != "0" {
				parent[child] = pid
			}
			continue
		}

		base := cwd[pid]
		if base == "" {
			base = dir
		}
		if atSyscalls[name] {
			fd, more, found := strings.Cut(args, ", ")
			if !found {
				continue
			}
			args = more
			if d, isDir := decoration(fd); isDir {
				if strings.HasPrefix(fd, "AT_FDCWD") {
					cwd[pid] = d
				}
				base = d
			}
		}
		p, isString := cString(args)
		if !isString || p == "" {
			continue // a descriptor's fstat (AT_EMPTY_PATH)
		}
		if !path.IsAbs(p) {
			p = path.Join(base, p)
		}
		p = path.Clean(p)
		if succeeded && (name == "execve" || name == "execveat") && pid != first {
			exe[pid] = p
		}
		accesses = append(accesses, access{Access{Path: p, OK: succeeded}, pid})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	program := func(pid string) string {
		for range len(parent) + 1 {
			if e, ok := exe[pid]; ok {
				return e
			}
			if pid = parent[pid]; pid == "" {
				break
			}
		}
		return ""
	}
	out := make([]Access, len(accesses))
	for i, a := range accesses {
		out[i] = a.Access
		out[i].Exe = program(a.pid)
	}
	return out, nil
}

// decoration is the path of a descriptor as strace -y prints it,
// "3</a/b>" or "AT_FDCWD</a/b>".
func decoration(fd string) (string, bool) {
	i := strings.IndexByte(fd, '<')
	if i < 0 || !strings.HasSuffix(fd, ">") {
		return "", false
	}
	return fd[i+1 : len(fd)-1], true
}

// cString reads the C string literal at the start of s, as strace prints
// it.
func cString(s string) (string, bool) {
	if !strings.HasPrefix(s, `"`) {
		return "", false
	}
	var b strings.Builder
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			return b.String(), true
		case '\\':
			i++
			if i >= len(s) {
				return "", false
			}
			switch e := s[i]; e {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case 'v':
				b.WriteByte('\v')
			case 'f':
				b.WriteByte('\f')
			case 'x':
				if i+2 >= len(s) {
					return "", false
				}
				v, err := strconv.ParseUint(s[i+1:i+3], 16, 8)
				if err != nil {
					return "", false
				}
				b.WriteByte(byte(v))
				i += 2
			case '0', '1', '2', '3', '4', '5', '6', '7':
				j := i
				for j < len(s) && j < i+3 && s[j] >= '0' && s[j] <= '7' {
					j++
				}
				v, _ := strconv.ParseUint(s[i:j], 8, 8)
				b.WriteByte(byte(v))
				i = j - 1
			default:
				b.WriteByte(e)
			}
		default:
			b.WriteByte(c)
		}
	}
	return "", false // truncated
}
