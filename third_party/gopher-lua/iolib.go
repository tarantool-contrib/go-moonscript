package lua

// go-moonscript patch: the io library is Lua 5.1's liolib.c over C stdio
// (glibc's), rewritten. Files are buffered as stdio streams are: fully,
// the standard output line by line on a terminal, the standard error not
// at all; setvbuf changes it. print writes to the buffer of the standard
// output too (LState.Stdout), which is flushed by io.flush and file:flush,
// before the standard input is read (glibc does only for a line-buffered
// output, a terminal: here prompts appear whatever the output is), and
// when the state is closed (os.exit closes it), with the other files the
// io library opened; os.execute and io.popen do not flush it, as system
// and popen don't. Results and errors are liolib.c's: true or nil,
// "<strerror>" and errno; "attempt to use a closed file", "cannot close
// standard file", "file (0x...)"; the close of a process is true;
// io.input(nil) and io.output(nil) return the current file, and an
// io.input or io.output that can't open its file raises the error;
// read("*n") scans as fscanf's "%lf" does; the lines iterator returns
// nothing at the end of the file. Under DialectLuaJIT, read takes its
// formats with or without '*', and "*L" (lib_io.c). Unused files are
// flushed and closed when the Go collector frees them (a runtime cleanup,
// where Lua 5.1 has __gc).

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"weak"
)

var ioFuncs = map[string]LGFunction{
	"close":   ioClose,
	"flush":   ioFlush,
	"lines":   ioLines,
	"input":   ioInput,
	"output":  ioOutput,
	"open":    ioOpenFile,
	"popen":   ioPopen,
	"read":    ioRead,
	"type":    ioType,
	"tmpfile": ioTmpFile,
	"write":   ioWrite,
}

const lFileClass = "FILE*"

const (
	bufNo = iota
	bufFull
	bufLine
)

// fileBufferSize is BUFSIZ, the size of a stream's buffer.
const fileBufferSize = 8192

// stream is the C FILE of a file: its descriptor and its buffers. It is
// what the cleanup of a file that is not used any more flushes and closes,
// so it does not point back to the lFile.
type stream struct {
	fp       *os.File
	w        *bufio.Writer // nil if not writable
	r        *bufio.Reader // nil if not readable
	mode     int           // bufNo, bufFull or bufLine
	writing  bool          // the last operation was a write (update streams)
	closed   bool
	writable bool
	readable bool
}

type lFile struct {
	s      *stream
	pp     *exec.Cmd // the process of a popen'd file
	std    bool      // stdin, stdout, stderr: can't be closed
	name   string
	cancel runtime.Cleanup
}

// ioState is the io library of a state: its standard streams and the files
// it opened, flushed when the state is closed.
type ioState struct {
	stdin, stdout *lFile
	files         []weak.Pointer[lFile]
}

const fileDefOutIndex = 1
const fileDefInIndex = 2

func newStream(fp *os.File, writable, readable bool) *stream {
	s := &stream{fp: fp, mode: bufFull, writable: writable, readable: readable}
	if writable {
		s.w = bufio.NewWriterSize(fp, fileBufferSize)
	}
	if readable {
		s.r = bufio.NewReaderSize(fp, fileBufferSize)
	}
	return s
}

// flush is fflush: the pending output written.
func (s *stream) flush() error {
	if s.w != nil && s.w.Buffered() > 0 {
		err := s.w.Flush()
		if err != nil {
			s.w.Reset(s.fp) // an error is reported once, the data lost
		}
		return err
	}
	return nil
}

// toRead prepares an update stream for reading (the output written).
func (s *stream) toRead() error {
	if s.writing {
		s.writing = false
		return s.flush()
	}
	return nil
}

// toWrite prepares an update stream for writing: the input buffered but
// not read is given back to the file.
func (s *stream) toWrite() {
	if s.r != nil && !s.writing && s.r.Buffered() > 0 {
		if _, err := s.fp.Seek(-int64(s.r.Buffered()), io.SeekCurrent); err == nil {
			s.r.Reset(s.fp)
		}
	}
	s.writing = true
}

func (s *stream) write(b []byte) error {
	if !s.writable {
		return syscall.EBADF
	}
	s.toWrite()
	switch s.mode {
	case bufNo:
		if err := s.flush(); err != nil {
			return err
		}
		_, err := s.fp.Write(b)
		return err
	case bufLine: // up to the last end of line written, the rest buffered
		if i := bytes.LastIndexByte(b, '\n'); i >= 0 {
			if _, err := s.w.Write(b[:i+1]); err != nil {
				return err
			}
			if err := s.flush(); err != nil {
				return err
			}
			b = b[i+1:]
		}
	}
	_, err := s.w.Write(b)
	return err
}

func (s *stream) writeString(str string) error {
	return s.write(unsafeFastStringToReadOnlyBytes(str))
}

func (s *stream) close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	err := s.flush()
	if err2 := s.fp.Close(); err == nil {
		err = err2
	}
	return err
}

// closeStream is the cleanup of a file no longer used: fclose.
func closeStream(s *stream) { _ = s.close() }

func (L *LState) ioState() *ioState {
	if L.G.io == nil {
		L.G.io = &ioState{}
	}
	return L.G.io
}

// isTerminal reports whether f is a terminal (a character device other
// than /dev/null), whose output is line buffered.
func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	if err != nil || st.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	if null, err := os.Stat(os.DevNull); err == nil && os.SameFile(st, null) {
		return false
	}
	return true
}

func newFileUD(L *LState, f *lFile) *LUserData {
	ud := L.NewUserData()
	ud.Value = f
	L.SetMetatable(ud, L.GetTypeMetatable(lFileClass))
	if !f.std {
		st := L.ioState()
		f.cancel = runtime.AddCleanup(f, closeStream, f.s)
		live := st.files[:0]
		for _, wp := range st.files {
			if lf := wp.Value(); lf != nil && !lf.s.closed {
				live = append(live, wp)
			}
		}
		st.files = append(live, weak.Make(f))
	}
	return ud
}

// newFile opens a file as fopen does.
func newFile(L *LState, path, mode string) (*LUserData, error) {
	flag, writable, readable, ok := fopenMode(mode)
	if !ok {
		return nil, syscall.EINVAL
	}
	fp, err := os.OpenFile(path, flag, 0666)
	if err != nil {
		return nil, err
	}
	return newFileUD(L, &lFile{s: newStream(fp, writable, readable), name: path}), nil
}

// closeIO is called by LState.Close: the files are closed, the standard
// ones flushed, as at the exit of a C program.
func (L *LState) closeIO() {
	st := L.G.io
	if st == nil {
		return
	}
	for _, wp := range st.files {
		if lf := wp.Value(); lf != nil && !lf.s.closed {
			lf.cancel.Stop()
			_ = lf.s.close()
			if lf.pp != nil {
				_ = lf.pp.Wait()
			}
		}
	}
	st.files = nil
	if st.stdout != nil {
		_ = st.stdout.s.flush()
	}
}

// Stdout is the standard output of the state, the buffered stream of
// io.stdout (os.Stdout when the state was made) that print also writes to;
// written to directly when the io library is not open (go-moonscript
// patch).
func (ls *LState) Stdout() io.Writer {
	if st := ls.G.io; st != nil && st.stdout != nil {
		return stdoutWriter{st.stdout.s}
	}
	return os.Stdout
}

// FlushStdout writes what the standard output of the state has buffered
// (go-moonscript patch).
func (ls *LState) FlushStdout() error {
	if st := ls.G.io; st != nil && st.stdout != nil {
		return st.stdout.s.flush()
	}
	return nil
}

// Stdin is the standard input of the state, the buffered stream of
// io.stdin, which reading flushes the standard output before (go-moonscript
// patch).
func (ls *LState) Stdin() io.Reader {
	if st := ls.G.io; st != nil && st.stdin != nil {
		return stdinReader{ls, st.stdin.s}
	}
	return os.Stdin
}

type stdoutWriter struct{ s *stream }

func (w stdoutWriter) Write(b []byte) (int, error) {
	if w.s.closed {
		return 0, os.ErrClosed
	}
	if err := w.s.write(b); err != nil {
		return 0, err
	}
	return len(b), nil
}

func (w stdoutWriter) WriteString(s string) (int, error) {
	return w.Write(unsafeFastStringToReadOnlyBytes(s))
}

type stdinReader struct {
	L *LState
	s *stream
}

func (r stdinReader) Read(b []byte) (int, error) {
	if r.s.closed {
		return 0, os.ErrClosed
	}
	_ = r.L.FlushStdout()
	return r.s.r.Read(b)
}

func OpenIo(L *LState) int {
	mod := L.RegisterModule(IoLibName, map[string]LGFunction{}).(*LTable)
	mt := L.NewTypeMetatable(lFileClass)
	mt.RawSetString("__index", mt)
	L.SetFuncs(mt, fileMethods)

	st := L.ioState()
	if st.stdout == nil {
		st.stdin = &lFile{s: newStream(os.Stdin, false, true), std: true, name: "stdin"}
		st.stdout = &lFile{s: newStream(os.Stdout, true, false), std: true, name: "stdout"}
		if isTerminal(os.Stdout) {
			st.stdout.s.mode = bufLine
		}
	}
	stderr := &lFile{s: newStream(os.Stderr, true, false), std: true, name: "stderr"}
	stderr.s.mode = bufNo
	stdin, stdout := newFileUD(L, st.stdin), newFileUD(L, st.stdout)
	mod.RawSetString("stdin", stdin)
	mod.RawSetString("stdout", stdout)
	mod.RawSetString("stderr", newFileUD(L, stderr))
	uv := L.CreateTable(2, 0)
	uv.RawSetInt(fileDefOutIndex, stdout)
	uv.RawSetInt(fileDefInIndex, stdin)
	for _, name := range funcNames(ioFuncs) { // sorted
		mod.RawSetString(name, L.NewClosure(ioFuncs[name], uv))
	}
	L.Push(mod)
	return 1
}

var fileMethods = map[string]LGFunction{
	"__gc":       fileGC,
	"__tostring": fileToString,
	"write":      fileWrite,
	"close":      fileClose,
	"flush":      fileFlush,
	"lines":      fileLines,
	"read":       fileRead,
	"seek":       fileSeek,
	"setvbuf":    fileSetVBuf,
}

// topFile is topfile: the file argument 1 is, open or not.
func topFile(L *LState) *lFile {
	if ud, ok := L.Get(1).(*LUserData); ok {
		if f, ok := ud.Value.(*lFile); ok {
			return f
		}
	}
	L.ArgError(1, lFileClass+" expected, got "+L.Get(1).Type().String()) // luaL_checkudata
	return nil
}

// toFile is tofile: the open file argument 1 is.
func toFile(L *LState) *lFile {
	f := topFile(L)
	if f.s.closed {
		L.RaiseError("attempt to use a closed file")
	}
	return f
}

// ioFile is getiofile: the default input or output file, open.
func ioFile(L *LState, index int) *lFile {
	f := L.Get(UpvalueIndex(1)).(*LTable).RawGetInt(index).(*LUserData).Value.(*lFile)
	if f.s.closed {
		name := "output"
		if index == fileDefInIndex {
			name = "input"
		}
		L.RaiseError("standard %s file is closed", name)
	}
	return f
}

// pushResult is pushresult: true, or nil, strerror and errno.
func pushResult(L *LState, err error) int {
	return pushFileResult(L, err, "")
}

func fileToString(L *LState) int {
	f := topFile(L)
	if f.s.closed {
		L.Push(LString("file (closed)"))
	} else {
		L.Push(LString(fmt.Sprintf("file (%p)", f)))
	}
	return 1
}

func fileGC(L *LState) int {
	f := topFile(L)
	if !f.s.closed && !f.std {
		auxClose(L, f)
	}
	return 0
}

func fileWrite(L *LState) int {
	return gWrite(L, toFile(L), 2)
}

func ioWrite(L *LState) int {
	return gWrite(L, ioFile(L, fileDefOutIndex), 1)
}

func gWrite(L *LState, f *lFile, arg int) int {
	var err error
	for top := L.GetTop(); arg <= top; arg++ {
		var s string
		if n, ok := L.Get(arg).(LNumber); ok {
			s = L.lvAsString(n) // LUA_NUMBER_FMT, "%.14g" (LuaJIT's in its dialect)
		} else {
			s = L.CheckString(arg)
		}
		if err == nil {
			err = f.s.writeString(s)
		}
	}
	return pushResult(L, err)
}

// auxClose is aux_close: a standard file is not closed, a process is
// waited for.
func auxClose(L *LState, f *lFile) int {
	if f.std {
		L.Push(LNil)
		L.Push(LString("cannot close standard file"))
		return 2
	}
	f.cancel.Stop()
	err := f.s.close()
	if f.pp != nil {
		// pclose: true whatever the exit status
		_ = f.pp.Wait()
		err = nil
	}
	return pushResult(L, err)
}

func fileClose(L *LState) int {
	return auxClose(L, toFile(L))
}

func ioClose(L *LState) int {
	if L.Get(1) == LNil {
		ud := L.Get(UpvalueIndex(1)).(*LTable).RawGetInt(fileDefOutIndex)
		L.SetTop(0)
		L.Push(ud)
	}
	return fileClose(L)
}

func fileFlush(L *LState) int {
	return pushResult(L, toFile(L).s.flush())
}

func ioFlush(L *LState) int {
	return pushResult(L, ioFile(L, fileDefOutIndex).s.flush())
}

func fileRead(L *LState) int {
	return gRead(L, toFile(L), 2)
}

func ioRead(L *LState) int {
	return gRead(L, ioFile(L, fileDefInIndex), 1)
}

// prepareRead readies f for reading: the standard output is flushed before
// the standard input is read.
func prepareRead(L *LState, f *lFile) error {
	if !f.s.readable {
		return syscall.EBADF
	}
	if st := L.G.io; st != nil && st.stdin == f {
		_ = L.FlushStdout()
	}
	return f.s.toRead()
}

// gRead is g_read: the values of the formats from first on (a line if
// none), nil for the first that fails, or nil, strerror, errno on an
// error.
func gRead(L *LState, f *lFile, first int) int {
	top := L.GetTop()
	err := prepareRead(L, f)
	if err != nil {
		return pushResult(L, err)
	}
	r := f.s.r
	n := first
	ok := true
	if top < first {
		ok, err = readLine(L, r, false)
		n = first + 1
	} else {
		for ; n <= top && ok && err == nil; n++ {
			switch lv := L.Get(n).(type) {
			case LNumber:
				l := cvttsd2si(float64(lv))
				if l == 0 {
					ok, err = testEOF(L, r)
				} else {
					ok, err = readChars(L, r, l)
				}
			case LString:
				ok, err = readFormat(L, r, n, string(lv))
			default:
				L.ArgError(n, "invalid option")
			}
		}
	}
	if err != nil {
		L.SetTop(top)
		return pushResult(L, err)
	}
	if !ok {
		L.Pop(1)
		L.Push(LNil)
	}
	return n - first
}

// readFormat reads the value of the format p, argument n of read.
func readFormat(L *LState, r *bufio.Reader, n int, p string) (bool, error) {
	if L.Dialect() == DialectLuaJIT {
		p = strings.TrimPrefix(p, "*")
	} else if len(p) == 0 || p[0] != '*' {
		L.ArgError(n, "invalid option")
	} else {
		p = p[1:]
	}
	c := byte(0)
	if len(p) > 0 {
		c = p[0]
	}
	switch {
	case c == 'n':
		return readNumber(L, r)
	case c == 'l':
		return readLine(L, r, false)
	case c == 'L' && L.Dialect() == DialectLuaJIT:
		return readLine(L, r, true)
	case c == 'a':
		b, err := io.ReadAll(r)
		L.Push(LString(b))
		return true, err
	}
	L.ArgError(n, "invalid format")
	return false, nil
}

// readLine is read_line: a line without its end (with it if keep), false
// at the end of the file.
func readLine(L *LState, r *bufio.Reader, keep bool) (bool, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if err == bufio.ErrBufferFull {
			buf = append(buf, chunk...)
			continue
		}
		if err == nil {
			if !keep {
				chunk = chunk[:len(chunk)-1]
			}
			if buf == nil {
				L.Push(LString(chunk))
			} else {
				L.Push(LString(append(buf, chunk...)))
			}
			return true, nil
		}
		buf = append(buf, chunk...)
		L.Push(LString(buf))
		if err == io.EOF {
			return len(buf) > 0, nil
		}
		return false, err
	}
}

// testEOF is test_eof: "" and whether there is more to read.
func testEOF(L *LState, r *bufio.Reader) (bool, error) {
	L.Push(emptyLString)
	_, err := r.Peek(1)
	if err == io.EOF {
		return false, nil
	}
	return err == nil, err
}

// readChars is read_chars: up to n bytes, false if none.
func readChars(L *LState, r *bufio.Reader, n int64) (bool, error) {
	var buf bytes.Buffer
	_, err := io.CopyN(&buf, r, n)
	if err == io.EOF {
		err = nil
	}
	L.Push(LString(buf.Bytes()))
	return buf.Len() > 0, err
}

// numScanner reads the characters of a number as scanf does: each one
// that may continue it is taken, the first that may not stays.
type numScanner struct {
	r   *bufio.Reader
	buf []byte
}

func (s *numScanner) peek() (byte, bool) {
	b, err := s.r.Peek(1)
	if err != nil {
		return 0, false
	}
	return b[0], true
}

func (s *numScanner) accept(set string) bool {
	c, ok := s.peek()
	if ok && strings.IndexByte(set, c) >= 0 {
		_, _ = s.r.ReadByte()
		s.buf = append(s.buf, c)
		return true
	}
	return false
}

// word takes word, in any case: false if a character differs.
func (s *numScanner) word(word string) bool {
	for i := 0; i < len(word); i++ {
		if !s.accept(word[i:i+1] + strings.ToUpper(word[i:i+1])) {
			return false
		}
	}
	return true
}

func (s *numScanner) digits(set string) int {
	n := 0
	for s.accept(set) {
		n++
	}
	return n
}

// scan takes a number as glibc's scanf does, and returns its value, false
// if what it took is no number: an exponent without digits is taken and
// ignored, "0x." is 0 but "0x" no number, "infin" no number.
func (s *numScanner) scan() (float64, bool) {
	neg := false
	if s.accept("+-") {
		neg = s.buf[0] == '-'
	}
	c, _ := s.peek()
	switch c {
	case 'i', 'I':
		if !s.word("inf") {
			return 0, false
		}
		if c, _ := s.peek(); (c == 'i' || c == 'I') && !s.word("inity") {
			return 0, false
		}
		if neg {
			return math.Inf(-1), true
		}
		return math.Inf(1), true
	case 'n', 'N':
		if !s.word("nan") {
			return 0, false
		}
		if neg {
			return math.Copysign(math.NaN(), -1), true
		}
		return math.NaN(), true
	}
	const dec, hex = "0123456789", "0123456789abcdefABCDEF"
	set, exp := dec, "eE"
	nd := 0
	isHex := false
	if s.accept("0") {
		nd++
		if s.accept("xX") {
			set, exp, nd, isHex = hex, "pP", 0, true
		}
	}
	nd += s.digits(set)
	dot := s.accept(".")
	if dot {
		nd += s.digits(set)
	}
	if nd == 0 {
		return 0, isHex && dot
	}
	mantissa := string(s.buf)
	if s.accept(exp) {
		start := len(s.buf)
		s.accept("+-")
		if s.digits(dec) > 0 {
			mantissa += string(s.buf[start-1:])
		}
	}
	text := mantissa
	if isHex && !strings.ContainsAny(text, "pP") {
		text += "p0"
	}
	v, err := strconv.ParseFloat(text, 64)
	if ne, isNE := err.(*strconv.NumError); err != nil && !(isNE && ne.Err == strconv.ErrRange) {
		return 0, false
	}
	return v, true
}

// readNumber is read_number, fscanf(f, "%lf"): white space skipped, then
// the longest prefix of a number (decimal or hexadecimal, inf, nan), nil
// if it is no number.
func readNumber(L *LState, r *bufio.Reader) (bool, error) {
	s := &numScanner{r: r}
	for {
		c, ok := s.peek()
		if !ok || strings.IndexByte(" \t\n\v\f\r", c) < 0 {
			break
		}
		_, _ = r.ReadByte()
	}
	if v, ok := s.scan(); ok {
		L.Push(LNumber(v))
		return true, nil
	}
	L.Push(LNil)
	if _, err := r.Peek(1); err != nil && err != io.EOF {
		return false, err
	}
	return false, nil
}

var fileSeekOptions = []string{"set", "cur", "end"}

// fileSeek is f_seek: the new position, or nil, strerror, errno.
func fileSeek(L *LState) int {
	f := toFile(L)
	op := 1 // "cur"
	if L.Get(2) != LNil {
		op = L.CheckOption(2, fileSeekOptions)
	}
	offset := cvttsd2si(float64(L.OptNumber(3, 0)))
	s := f.s
	if err := s.flush(); err != nil {
		return pushResult(L, err)
	}
	s.writing = false
	if op == 1 && s.r != nil {
		offset -= int64(s.r.Buffered()) // what the buffer holds is not read yet
	}
	pos, err := s.fp.Seek(offset, op)
	if err != nil {
		return pushResult(L, err)
	}
	if s.r != nil {
		s.r.Reset(s.fp)
	}
	L.Push(LNumber(pos))
	return 1
}

var filebufOptions = []string{"no", "full", "line"}

// fileSetVBuf is f_setvbuf: the buffering of the file, true (the size is
// ignored, as glibc's setvbuf does when it is given no buffer).
func fileSetVBuf(L *LState) int {
	f := toFile(L)
	op := L.CheckOption(2, filebufOptions)
	L.OptInt(3, fileBufferSize) // glibc's setvbuf ignores it without a buffer
	s := f.s
	if err := s.flush(); err != nil {
		return pushResult(L, err)
	}
	s.mode = []int{bufNo, bufFull, bufLine}[op]
	L.Push(LTrue)
	return 1
}

// ioIOFile is g_iofile: io.input and io.output.
func ioIOFile(L *LState, index int, mode string) int {
	uv := L.Get(UpvalueIndex(1)).(*LTable)
	if lv := L.Get(1); lv != LNil {
		switch v := lv.(type) {
		case LString, LNumber:
			name := LVAsString(v)
			ud, err := newFile(L, name, mode)
			if err != nil {
				msg, _ := cError(err)
				L.ArgError(1, name+": "+msg)
			}
			uv.RawSetInt(index, ud)
		default:
			toFile(L)
			uv.RawSetInt(index, lv)
		}
	}
	L.Push(uv.RawGetInt(index))
	return 1
}

func ioInput(L *LState) int {
	return ioIOFile(L, fileDefInIndex, "r")
}

func ioOutput(L *LState) int {
	return ioIOFile(L, fileDefOutIndex, "w")
}

// linesIter is io_readline: the next line, nothing at the end of the file
// (closing the file if upvalue 2 is true).
func linesIter(L *LState) int {
	ud := L.Get(UpvalueIndex(1)).(*LUserData)
	f := ud.Value.(*lFile)
	if f.s.closed {
		L.RaiseError("file is already closed")
	}
	if err := prepareRead(L, f); err != nil {
		msg, _ := cError(err)
		L.RaiseError("%s", msg)
	}
	ok, err := readLine(L, f.s.r, false)
	if err != nil {
		msg, _ := cError(err)
		L.RaiseError("%s", msg)
	}
	if ok {
		return 1
	}
	L.Pop(1)
	if LVAsBool(L.Get(UpvalueIndex(2))) {
		top := L.GetTop()
		auxClose(L, f)
		L.SetTop(top)
	}
	return 0
}

func fileLines(L *LState) int {
	toFile(L)
	L.Push(L.NewClosure(linesIter, L.Get(1), LFalse))
	return 1
}

func ioLines(L *LState) int {
	if L.Get(1) == LNil {
		ud := L.Get(UpvalueIndex(1)).(*LTable).RawGetInt(fileDefInIndex)
		L.SetTop(0)
		L.Push(ud)
		return fileLines(L)
	}
	name := L.CheckString(1)
	ud, err := newFile(L, name, "r")
	if err != nil {
		msg, _ := cError(err)
		L.ArgError(1, name+": "+msg)
	}
	L.Push(L.NewClosure(linesIter, ud, LTrue))
	return 1
}

// go-moonscript patch: io.open takes the modes C's fopen (glibc's) does,
// as Lua 5.1 passes the mode to it: 'r', 'w' or 'a', then any of '+'
// (update), 'x' (exclusive creation), 'b' and other characters (ignored)
// among the next six; any other mode fails with EINVAL (from yuin/gopher-lua
// PR #458, 0x501D, which added "r+b", "w+b" and "a+b"). New files get
// 0666 less the umask, as with fopen.
func ioOpenFile(L *LState) int {
	path := L.CheckString(1)
	mode := L.OptString(2, "r")
	ud, err := newFile(L, path, mode)
	if err != nil {
		return pushFileResult(L, err, path)
	}
	L.Push(ud)
	return 1
}

func fopenMode(mode string) (flag int, writable, readable, ok bool) {
	if mode == "" {
		return 0, false, false, false
	}
	switch mode[0] {
	case 'r':
		flag, readable = os.O_RDONLY, true
	case 'w':
		flag, writable = os.O_WRONLY|os.O_CREATE|os.O_TRUNC, true
	case 'a':
		flag, writable = os.O_WRONLY|os.O_CREATE|os.O_APPEND, true
	default:
		return 0, false, false, false
	}
	for i := 1; i < 7 && i < len(mode) && mode[i] != 0 && mode[i] != ','; i++ {
		switch mode[i] {
		case '+':
			flag = flag&^(os.O_RDONLY|os.O_WRONLY) | os.O_RDWR
			writable, readable = true, true
		case 'x':
			flag |= os.O_EXCL
		}
	}
	return flag, writable, readable, true
}

// ioPopen is io_popen, popen(3): mode "r" or "w" (and glibc's 'e' after
// it), nil, "<prog>: Invalid argument", EINVAL for another. As with
// popen(3), the process shares the standard streams that are not its pipe,
// and close closes the pipe before it waits for the process (from
// yuin/gopher-lua PR #404, scr-oath: a process reading its input never
// ended).
func ioPopen(L *LState) int {
	prog := L.CheckString(1)
	mode := L.OptString(2, "r")
	m := strings.TrimRight(mode, "e")
	if m != "r" && m != "w" {
		return pushFileResult(L, syscall.EINVAL, prog)
	}
	c, args := popenArgs(prog)
	cmd := exec.Command(c, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	pr, pw, err := os.Pipe()
	if err != nil {
		return pushFileResult(L, err, prog)
	}
	var s *stream
	var child *os.File
	if m == "r" {
		cmd.Stdout, child = pw, pw
		s = newStream(pr, false, true)
	} else {
		cmd.Stdin, child = pr, pr
		s = newStream(pw, true, false)
	}
	err = cmd.Start()
	_ = child.Close()
	if err != nil {
		_ = s.fp.Close()
		return pushFileResult(L, err, prog)
	}
	L.Push(newFileUD(L, &lFile{s: s, pp: cmd, name: prog}))
	return 1
}

func ioType(L *LState) int {
	L.CheckAny(1)
	ud, ok := L.Get(1).(*LUserData)
	if !ok || L.GetMetatable(ud) != L.GetTypeMetatable(lFileClass) {
		L.Push(LNil)
		return 1
	}
	f, ok := ud.Value.(*lFile)
	switch {
	case !ok:
		L.Push(LNil)
	case f.s.closed:
		L.Push(LString("closed file"))
	default:
		L.Push(LString("file"))
	}
	return 1
}

// ioTmpFile is tmpfile(3): a new file, open for update, already removed.
func ioTmpFile(L *LState) int {
	fp, err := os.CreateTemp("", "tmpf")
	if err != nil {
		return pushResult(L, err)
	}
	_ = os.Remove(fp.Name())
	L.Push(newFileUD(L, &lFile{s: newStream(fp, true, true), name: fp.Name()}))
	return 1
}
