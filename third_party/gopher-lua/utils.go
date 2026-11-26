package lua

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unsafe"
)

func intMin(a, b int) int {
	if a < b {
		return a
	} else {
		return b
	}
}

func intMax(a, b int) int {
	if a > b {
		return a
	} else {
		return b
	}
}

func defaultFormat(v interface{}, f fmt.State, c rune) {
	buf := make([]string, 0, 10)
	buf = append(buf, "%")
	for i := 0; i < 128; i++ {
		if f.Flag(i) {
			buf = append(buf, string(rune(i)))
		}
	}

	if w, ok := f.Width(); ok {
		buf = append(buf, strconv.Itoa(w))
	}
	if p, ok := f.Precision(); ok {
		buf = append(buf, "."+strconv.Itoa(p))
	}
	buf = append(buf, string(c))
	format := strings.Join(buf, "")
	fmt.Fprintf(f, format, v)
}

type flagScanner struct {
	flag       byte
	start      string
	end        string
	buf        []byte
	str        string
	Length     int
	Pos        int
	HasFlag    bool
	ChangeFlag bool
}

func newFlagScanner(flag byte, start, end, str string) *flagScanner {
	return &flagScanner{flag, start, end, make([]byte, 0, len(str)), str, len(str), 0, false, false}
}

func (fs *flagScanner) AppendString(str string) { fs.buf = append(fs.buf, str...) }

func (fs *flagScanner) AppendChar(ch byte) { fs.buf = append(fs.buf, ch) }

func (fs *flagScanner) String() string { return string(fs.buf) }

func (fs *flagScanner) Next() (byte, bool) {
	c := byte('\000')
	fs.ChangeFlag = false
	if fs.Pos == fs.Length {
		if fs.HasFlag {
			fs.AppendString(fs.end)
		}
		return c, true
	} else {
		c = fs.str[fs.Pos]
		if c == fs.flag {
			if fs.Pos < (fs.Length-1) && fs.str[fs.Pos+1] == fs.flag {
				fs.HasFlag = false
				fs.AppendChar(fs.flag)
				fs.Pos += 2
				return fs.Next()
			} else if fs.Pos != fs.Length-1 {
				if fs.HasFlag {
					fs.AppendString(fs.end)
				}
				fs.AppendString(fs.start)
				fs.ChangeFlag = true
				fs.HasFlag = true
			}
		}
	}
	fs.Pos++
	return c, false
}

// go-moonscript patch: strftime is os.date's formatting in Lua 5.1, which
// passes every "%<char>" to C's strftime (glibc's, in the C locale) and
// copies other characters, and a '%' at the end, as they are.
func strftime(t time.Time, cfmt string) string {
	buf := make([]byte, 0, len(cfmt)*2)
	for i := 0; i < len(cfmt); i++ {
		if cfmt[i] != '%' || i+1 == len(cfmt) {
			buf = append(buf, cfmt[i])
			continue
		}
		i++
		buf = appendStrftime(buf, t, cfmt[i])
	}
	return string(buf)
}

var strftimeDays = [...]string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}

var strftimeMonths = [...]string{"January", "February", "March", "April", "May", "June", "July",
	"August", "September", "October", "November", "December"}

func appendStrftime(buf []byte, t time.Time, c byte) []byte {
	num := func(n, width int, pad byte) []byte {
		s := strconv.Itoa(n)
		for len(s) < width {
			s = string(pad) + s
		}
		return append(buf, s...)
	}
	hour12 := t.Hour() % 12
	if hour12 == 0 {
		hour12 = 12
	}
	yday := t.YearDay() - 1
	wday := int(t.Weekday())
	switch c {
	case 'a':
		return append(buf, strftimeDays[wday][:3]...)
	case 'A':
		return append(buf, strftimeDays[wday]...)
	case 'b', 'h':
		return append(buf, strftimeMonths[t.Month()-1][:3]...)
	case 'B':
		return append(buf, strftimeMonths[t.Month()-1]...)
	case 'c':
		buf = appendStrftime(buf, t, 'a')
		buf = append(buf, ' ')
		buf = appendStrftime(buf, t, 'b')
		buf = append(buf, ' ')
		buf = appendStrftime(buf, t, 'e')
		buf = append(buf, ' ')
		buf = appendStrftime(buf, t, 'T')
		buf = append(buf, ' ')
		return appendStrftime(buf, t, 'Y')
	case 'C':
		return num(floorDiv(t.Year(), 100), 2, '0')
	case 'd':
		return num(t.Day(), 2, '0')
	case 'D', 'x':
		buf = appendStrftime(buf, t, 'm')
		buf = append(buf, '/')
		buf = appendStrftime(buf, t, 'd')
		buf = append(buf, '/')
		return appendStrftime(buf, t, 'y')
	case 'e':
		return num(t.Day(), 2, ' ')
	case 'F':
		buf = appendStrftime(buf, t, 'Y')
		buf = append(buf, '-')
		buf = appendStrftime(buf, t, 'm')
		buf = append(buf, '-')
		return appendStrftime(buf, t, 'd')
	case 'G':
		year, _ := t.ISOWeek()
		return num(year, 1, '0')
	case 'g':
		year, _ := t.ISOWeek()
		return num((year%100+100)%100, 2, '0')
	case 'H':
		return num(t.Hour(), 2, '0')
	case 'I':
		return num(hour12, 2, '0')
	case 'j':
		return num(yday+1, 3, '0')
	case 'k':
		return num(t.Hour(), 2, ' ')
	case 'l':
		return num(hour12, 2, ' ')
	case 'm':
		return num(int(t.Month()), 2, '0')
	case 'M':
		return num(t.Minute(), 2, '0')
	case 'n':
		return append(buf, '\n')
	case 'p':
		if t.Hour() < 12 {
			return append(buf, "AM"...)
		}
		return append(buf, "PM"...)
	case 'P':
		if t.Hour() < 12 {
			return append(buf, "am"...)
		}
		return append(buf, "pm"...)
	case 'r':
		buf = appendStrftime(buf, t, 'I')
		buf = append(buf, ':')
		buf = appendStrftime(buf, t, 'M')
		buf = append(buf, ':')
		buf = appendStrftime(buf, t, 'S')
		buf = append(buf, ' ')
		return appendStrftime(buf, t, 'p')
	case 'R':
		buf = appendStrftime(buf, t, 'H')
		buf = append(buf, ':')
		return appendStrftime(buf, t, 'M')
	case 's':
		return append(buf, strconv.FormatInt(t.Unix(), 10)...)
	case 'S':
		return num(t.Second(), 2, '0')
	case 't':
		return append(buf, '\t')
	case 'T', 'X':
		buf = appendStrftime(buf, t, 'H')
		buf = append(buf, ':')
		buf = appendStrftime(buf, t, 'M')
		buf = append(buf, ':')
		return appendStrftime(buf, t, 'S')
	case 'u':
		return num((wday+6)%7+1, 1, '0')
	case 'U':
		return num((yday+7-wday)/7, 2, '0')
	case 'V':
		_, week := t.ISOWeek()
		return num(week, 2, '0')
	case 'w':
		return num(wday, 1, '0')
	case 'W':
		return num((yday+7-(wday+6)%7)/7, 2, '0')
	case 'y':
		return num((t.Year()%100+100)%100, 2, '0')
	case 'Y':
		return num(t.Year(), 1, '0')
	case 'z':
		_, off := t.Zone()
		sign := byte('+')
		if off < 0 {
			sign, off = '-', -off
		}
		buf = append(buf, sign)
		return num(off/3600*100+off%3600/60, 4, '0')
	case 'Z':
		name, _ := t.Zone()
		if name == "UTC" && t.Location() == time.UTC {
			name = "GMT" // what glibc's gmtime gives
		}
		return append(buf, name...)
	case '%':
		return append(buf, '%')
	case '3', '4', '5', '6', '7', '8', '9':
		// glibc reads a width with no conversion after it and pads "%<digit>"
		for n := int(c - '0'); n > 2; n-- {
			buf = append(buf, ' ')
		}
		return append(buf, '%', c)
	}
	return append(buf, '%', c) // not a conversion: as it is
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

func isInteger(v LNumber) bool {
	return float64(v) == float64(int64(v))
	//_, frac := math.Modf(float64(v))
	//return frac == 0.0
}

func isArrayKey(v LNumber) bool {
	return isInteger(v) && v < LNumber(int((^uint(0))>>1)) && v > LNumber(0) && v < LNumber(MaxArrayIndex)
}

// parseNumber converts a string to a number as Lua 5.1 does (luaO_str2d,
// lua_tonumber): what C's strtod accepts (a decimal or C99 hexadecimal
// number, inf, nan), with spaces around it, up to a NUL byte. go-moonscript
// patch (yuin/gopher-lua#478, halogencapital/gopher-lua d3de08565a): it
// took Go's syntax, base prefixes and underscores included, so "010" was 8,
// "0b11" 3 and "1_0" 10, and it rejected "0x1p4", "0x1.8" and "-0x10".
func parseNumber(number string) (LNumber, error) {
	if i := strings.IndexByte(number, 0); i >= 0 {
		number = number[:i]
	}
	s := strings.TrimLeft(number, cSpaces)
	n := scanCNumber(s)
	if n == 0 || strings.TrimLeft(s[n:], cSpaces) != "" {
		return LNumber(0), fmt.Errorf("invalid number: %q", number)
	}
	s = s[:n]
	neg := false
	if s[0] == '-' || s[0] == '+' {
		neg = s[0] == '-'
		s = s[1:]
	}
	var v float64
	switch c := s[0] | 0x20; {
	case c == 'i':
		v = math.Inf(1)
	case c == 'n':
		v = math.NaN()
	default:
		if len(s) > 1 && (s[1]|0x20) == 'x' && !strings.ContainsAny(s, "pP") {
			s += "p0" // Go's hexadecimal floats need an exponent
		}
		var err error
		if v, err = strconv.ParseFloat(s, LNumberBit); err != nil {
			// out of range: strtod gives HUGE_VAL or 0 too
			if ne, ok := err.(*strconv.NumError); !ok || ne.Err != strconv.ErrRange {
				return LNumber(0), err
			}
		}
	}
	if neg {
		v = -v
	}
	return LNumber(v), nil
}

// cSpaces are the characters C's isspace accepts.
const cSpaces = " \t\n\v\f\r"

// scanCNumber returns the length of the longest prefix of s that C's strtod
// converts (0 if none): [+-] then a decimal number with an optional
// exponent, "0x" and a hexadecimal number with an optional binary
// exponent, "inf", "infinity" or "nan" with an optional "(chars)".
func scanCNumber(s string) int {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	rest := strings.ToLower(s[i:])
	switch {
	case strings.HasPrefix(rest, "infinity"):
		return i + 8
	case strings.HasPrefix(rest, "inf"):
		return i + 3
	case strings.HasPrefix(rest, "nan"):
		i += 3
		if j := i; j < len(s) && s[j] == '(' {
			for j++; j < len(s) && (s[j] == '_' || '0' <= s[j] && s[j] <= '9' || 'a' <= s[j]|0x20 && s[j]|0x20 <= 'z'); j++ {
			}
			if j < len(s) && s[j] == ')' {
				i = j + 1
			}
		}
		return i
	}
	isdigit := func(c byte) bool { return '0' <= c && c <= '9' }
	isxdigit := func(c byte) bool { return isdigit(c) || 'a' <= c|0x20 && c|0x20 <= 'f' }
	digit, expch := isdigit, byte('e')
	if strings.HasPrefix(rest, "0x") {
		if j := i + 2; j < len(s) && isxdigit(s[j]) || j+1 < len(s) && s[j] == '.' && isxdigit(s[j+1]) {
			i, digit, expch = j, isxdigit, 'p'
		}
	}
	ndigits := 0
	for ; i < len(s) && digit(s[i]); i++ {
		ndigits++
	}
	if i < len(s) && s[i] == '.' {
		for i++; i < len(s) && digit(s[i]); i++ {
			ndigits++
		}
	}
	if ndigits == 0 {
		return 0
	}
	if i < len(s) && s[i]|0x20 == expch {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		if j < len(s) && isdigit(s[j]) {
			for i = j; i < len(s) && isdigit(s[i]); i++ {
			}
		}
	}
	return i
}

func popenArgs(arg string) (string, []string) {
	cmd := "/bin/sh"
	args := []string{"-c"}
	if LuaOS == "windows" {
		cmd = "C:\\Windows\\system32\\cmd.exe"
		args = []string{"/c"}
	}
	args = append(args, arg)
	return cmd, args
}

func isGoroutineSafe(lv LValue) bool {
	switch v := lv.(type) {
	case *LFunction, *LUserData, *LState:
		return false
	case *LTable:
		return v.Metatable == LNil
	default:
		return true
	}
}

func readBufioSize(reader *bufio.Reader, size int64) ([]byte, error, bool) {
	result := []byte{}
	read := int64(0)
	var err error
	var n int
	for read != size {
		buf := make([]byte, size-read)
		n, err = reader.Read(buf)
		if err != nil {
			break
		}
		read += int64(n)
		result = append(result, buf[:n]...)
	}
	e := err
	if e != nil && e == io.EOF {
		e = nil
	}

	return result, e, len(result) == 0 && err == io.EOF
}

func readBufioLine(reader *bufio.Reader) ([]byte, error, bool) {
	result := []byte{}
	var buf []byte
	var err error
	var isprefix bool = true
	for isprefix {
		buf, isprefix, err = reader.ReadLine()
		if err != nil {
			break
		}
		result = append(result, buf...)
	}
	e := err
	if e != nil && e == io.EOF {
		e = nil
	}

	return result, e, len(result) == 0 && err == io.EOF
}

func int2Fb(val int) int {
	e := 0
	x := val
	for x >= 16 {
		x = (x + 1) >> 1
		e++
	}
	if x < 8 {
		return x
	}
	return ((e + 1) << 3) | (x - 8)
}

// fb2Int is int2Fb's inverse (Lua 5.1's luaO_fb2int; go-moonscript patch).
func fb2Int(x int) int {
	e := (x >> 3) & 31
	if e == 0 {
		return x
	}
	return ((x & 7) + 8) << (e - 1)
}

func strCmp(s1, s2 string) int {
	len1 := len(s1)
	len2 := len(s2)
	for i := 0; ; i++ {
		c1 := -1
		if i < len1 {
			c1 = int(s1[i])
		}
		c2 := -1
		if i != len2 {
			c2 = int(s2[i])
		}
		switch {
		case c1 < c2:
			return -1
		case c1 > c2:
			return +1
		case c1 < 0:
			return 0
		}
	}
}

func unsafeFastStringToReadOnlyBytes(s string) (bs []byte) {
	sh := (*reflect.StringHeader)(unsafe.Pointer(&s))
	bh := (*reflect.SliceHeader)(unsafe.Pointer(&bs))
	bh.Data = sh.Data
	bh.Cap = sh.Len
	bh.Len = sh.Len
	return
}
