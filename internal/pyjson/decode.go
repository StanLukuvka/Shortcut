// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (G1-1). Don't hand-edit without checking the plan.

package pyjson

import (
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"
)

// SyntaxError is Python's json.JSONDecodeError. Error() is str(e): "<msg>: line L column C (char N)", where N counts
// code points, as Python does.
type SyntaxError struct {
	Msg       string
	Line, Col int
	Char      int
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("%s: line %d column %d (char %d)", e.Msg, e.Line, e.Col, e.Char)
}

// UTF8Error is Python's UnicodeDecodeError for the strict 'utf-8' codec; Error() is str(e).
type UTF8Error struct{ msg string }

func (e *UTF8Error) Error() string { return e.msg }

// ErrDepth stands for Python's RecursionError on deeply nested input.
// lazy: Python's real limit depends on the interpreter's stack; this one is a fixed 1000 levels.
var ErrDepth = errors.New("maximum recursion depth exceeded")

const maxDepth = 1000

// Decode is json.load on a file's bytes: strict UTF-8 first (UnicodeDecodeError text), then Parse.
func Decode(raw []byte) (Value, error) {
	if err := checkUTF8(raw); err != nil {
		return nil, err
	}
	return Parse(string(raw))
}

// Parse is json.loads(s). s may hold WTF-8 lone surrogates.
// lazy: an integer literal over 4300 digits parses here; Python 3.11+ raises ValueError for it.
func Parse(s string) (Value, error) {
	d := &decoder{s: s}
	if strings.HasPrefix(s, "\xef\xbb\xbf") {
		return nil, d.errAt("Unexpected UTF-8 BOM (decode using utf-8-sig)", 0)
	}
	v, end, err := d.value(d.skipWS(0), 0)
	if err != nil {
		return nil, err
	}
	if end = d.skipWS(end); end != len(s) {
		return nil, d.errAt("Extra data", end)
	}
	return v, nil
}

type decoder struct{ s string }

// errAt builds the error for the byte offset pos, reporting it in code points.
func (d *decoder) errAt(msg string, pos int) error {
	char := countChars(d.s[:pos])
	line := strings.Count(d.s[:pos], "\n") + 1
	col := char + 1
	if nl := strings.LastIndexByte(d.s[:pos], '\n'); nl >= 0 {
		col = char - countChars(d.s[:nl])
	}
	return &SyntaxError{Msg: msg, Line: line, Col: col, Char: char}
}

// countChars counts code points of WTF-8 (or UTF-8) text: every byte that is not a continuation byte starts one.
func countChars(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i]&0xC0 != 0x80 {
			n++
		}
	}
	return n
}

func (d *decoder) skipWS(i int) int {
	for i < len(d.s) && (d.s[i] == ' ' || d.s[i] == '\t' || d.s[i] == '\n' || d.s[i] == '\r') {
		i++
	}
	return i
}

// value is py_scanner's scan_once: the value at i and the offset after it.
func (d *decoder) value(i, depth int) (Value, int, error) {
	s := d.s
	if i >= len(s) {
		return nil, 0, d.errAt("Expecting value", i)
	}
	switch c := s[i]; {
	case c == '"':
		str, end, err := d.str(i + 1)
		return str, end, err
	case c == '{':
		return d.object(i+1, depth+1)
	case c == '[':
		return d.array(i+1, depth+1)
	case strings.HasPrefix(s[i:], "null"):
		return nil, i + 4, nil
	case strings.HasPrefix(s[i:], "true"):
		return true, i + 4, nil
	case strings.HasPrefix(s[i:], "false"):
		return false, i + 5, nil
	case strings.HasPrefix(s[i:], "NaN"):
		return nan(), i + 3, nil
	case strings.HasPrefix(s[i:], "Infinity"):
		return inf(1), i + 8, nil
	case strings.HasPrefix(s[i:], "-Infinity"):
		return inf(-1), i + 9, nil
	}
	return d.number(i)
}

// number is _match_number_unicode.
func (d *decoder) number(start int) (Value, int, error) {
	s := d.s
	i := start
	if i < len(s) && s[i] == '-' {
		i++
	}
	switch {
	case i < len(s) && s[i] >= '1' && s[i] <= '9':
		for i < len(s) && isDigit(s[i]) {
			i++
		}
	case i < len(s) && s[i] == '0':
		i++
	default:
		return nil, 0, d.errAt("Expecting value", start)
	}
	isFloat := false
	// A '.' or 'e' only counts when something can follow it, as in the C scanner (idx < end_idx).
	if i+1 < len(s) && s[i] == '.' && isDigit(s[i+1]) {
		i++
		for i < len(s) && isDigit(s[i]) {
			i++
		}
		isFloat = true
	}
	if i+1 < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if s[j] == '-' || s[j] == '+' {
			j++
		}
		if j < len(s) && isDigit(s[j]) {
			for j < len(s) && isDigit(s[j]) {
				j++
			}
			i, isFloat = j, true
		}
	}
	lit := s[start:i]
	if !isFloat {
		n, _ := new(big.Int).SetString(lit, 10)
		return n, i, nil
	}
	f, _ := strconv.ParseFloat(lit, 64) // out of range gives +-Inf, as Python's float()
	return f, i, nil
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// object is _parse_object_unicode; i is just after the '{'.
func (d *decoder) object(i, depth int) (Value, int, error) {
	if depth > maxDepth {
		return nil, 0, ErrDepth
	}
	s := d.s
	obj := &Object{}
	i = d.skipWS(i)
	if i >= len(s) || s[i] != '}' {
		for {
			if i >= len(s) || s[i] != '"' {
				return nil, 0, d.errAt("Expecting property name enclosed in double quotes", i)
			}
			key, end, err := d.str(i + 1)
			if err != nil {
				return nil, 0, err
			}
			i = d.skipWS(end)
			if i >= len(s) || s[i] != ':' {
				return nil, 0, d.errAt("Expecting ':' delimiter", i)
			}
			val, end, err := d.value(d.skipWS(i+1), depth)
			if err != nil {
				return nil, 0, err
			}
			obj.Set(key, val)
			i = d.skipWS(end)
			if i < len(s) && s[i] == '}' {
				break
			}
			if i >= len(s) || s[i] != ',' {
				return nil, 0, d.errAt("Expecting ',' delimiter", i)
			}
			comma := i
			i = d.skipWS(i + 1)
			if i < len(s) && s[i] == '}' {
				return nil, 0, d.errAt("Illegal trailing comma before end of object", comma)
			}
		}
	}
	return obj, i + 1, nil
}

// array is _parse_array_unicode; i is just after the '['.
func (d *decoder) array(i, depth int) (Value, int, error) {
	if depth > maxDepth {
		return nil, 0, ErrDepth
	}
	s := d.s
	arr := []Value{}
	i = d.skipWS(i)
	if i < len(s) && s[i] != ']' {
		for {
			val, end, err := d.value(i, depth)
			if err != nil {
				return nil, 0, err
			}
			arr = append(arr, val)
			i = d.skipWS(end)
			if i < len(s) && s[i] == ']' {
				break
			}
			if i >= len(s) || s[i] != ',' {
				return nil, 0, d.errAt("Expecting ',' delimiter", i)
			}
			comma := i
			i = d.skipWS(i + 1)
			if i < len(s) && s[i] == ']' {
				return nil, 0, d.errAt("Illegal trailing comma before end of array", comma)
			}
		}
	}
	if i >= len(s) || s[i] != ']' {
		return nil, 0, d.errAt("Expecting value", i)
	}
	return arr, i + 1, nil
}

// str is scanstring_unicode (strict); i is just after the opening quote.
func (d *decoder) str(i int) (string, int, error) {
	s := d.s
	begin := i - 1
	var b strings.Builder
	for {
		start := i
		for i < len(s) && s[i] != '"' && s[i] != '\\' {
			if s[i] < 0x20 {
				return "", 0, d.errAt("Invalid control character at", i)
			}
			i++
		}
		if i >= len(s) {
			return "", 0, d.errAt("Unterminated string starting at", begin)
		}
		b.WriteString(s[start:i])
		if s[i] == '"' {
			return b.String(), i + 1, nil
		}
		i++ // the backslash
		if i >= len(s) {
			return "", 0, d.errAt("Unterminated string starting at", begin)
		}
		if c := s[i]; c != 'u' {
			esc, ok := map[byte]byte{'"': '"', '\\': '\\', '/': '/', 'b': '\b', 'f': '\f', 'n': '\n', 'r': '\r', 't': '\t'}[c]
			if !ok {
				return "", 0, d.errAt("Invalid \\escape", i-1)
			}
			b.WriteByte(esc)
			i++
			continue
		}
		i++ // the 'u'
		if i+4 >= len(s) {
			return "", 0, d.errAt("Invalid \\uXXXX escape", i-1)
		}
		c, ok := hex4(s[i : i+4])
		if !ok {
			return "", 0, d.errAt("Invalid \\uXXXX escape", i-1)
		}
		i += 4
		// A high surrogate followed by \uXXXX holding a low one is one astral character; the C scanner needs a
		// further character after that escape (end + 6 < len).
		if c >= 0xD800 && c <= 0xDBFF && i+6 < len(s) && s[i] == '\\' && s[i+1] == 'u' {
			c2, ok := hex4(s[i+2 : i+6])
			if !ok {
				return "", 0, d.errAt("Invalid \\uXXXX escape", i+1)
			}
			if c2 >= 0xDC00 && c2 <= 0xDFFF {
				c = 0x10000 + (c-0xD800)<<10 + (c2 - 0xDC00)
				i += 6
			}
		}
		if c >= 0xDC80 && c <= 0xDCFF { // surrogateescape: the raw byte, as os.fsencode gives it
			b.WriteByte(byte(c - 0xDC00))
		} else if c >= 0xD800 && c <= 0xDFFF { // any other lone surrogate: WTF-8
			b.WriteByte(0xE0 | byte(c>>12))
			b.WriteByte(0x80 | byte(c>>6)&0x3F)
			b.WriteByte(0x80 | byte(c)&0x3F)
		} else {
			b.WriteRune(c)
		}
	}
}

func hex4(s string) (rune, bool) {
	var c rune
	for i := 0; i < 4; i++ {
		var v byte
		switch ch := s[i]; {
		case ch >= '0' && ch <= '9':
			v = ch - '0'
		case ch >= 'a' && ch <= 'f':
			v = ch - 'a' + 10
		case ch >= 'A' && ch <= 'F':
			v = ch - 'A' + 10
		default:
			return 0, false
		}
		c = c<<4 | rune(v)
	}
	return c, true
}

// checkUTF8 is bytes.decode("utf-8") with Python's error text: byte positions, and the span of the bad sequence.
func checkUTF8(raw []byte) error {
	for i := 0; i < len(raw); {
		if raw[i] < 0x80 {
			i++
			continue
		}
		if r, n := utf8.DecodeRune(raw[i:]); r != utf8.RuneError || n != 1 {
			i += n
			continue
		}
		b := raw[i]
		lo, hi := byte(0x80), byte(0xBF)
		need := 0
		switch {
		case b >= 0xC2 && b <= 0xDF:
			need = 1
		case b >= 0xE0 && b <= 0xEF:
			need = 2
			if b == 0xE0 {
				lo = 0xA0
			} else if b == 0xED {
				hi = 0x9F
			}
		case b >= 0xF0 && b <= 0xF4:
			need = 3
			if b == 0xF0 {
				lo = 0x90
			} else if b == 0xF4 {
				hi = 0x8F
			}
		default:
			return utf8Err(i, i+1, "invalid start byte", raw)
		}
		end := i + 1
		for k := 0; k < need; k++ {
			if end >= len(raw) {
				return utf8Err(i, end, "unexpected end of data", raw)
			}
			if raw[end] < lo || raw[end] > hi {
				return utf8Err(i, end, "invalid continuation byte", raw)
			}
			lo, hi = 0x80, 0xBF
			end++
		}
	}
	return nil
}

func utf8Err(start, end int, reason string, raw []byte) error {
	if end-start == 1 {
		return &UTF8Error{fmt.Sprintf("'utf-8' codec can't decode byte 0x%02x in position %d: %s", raw[start], start, reason)}
	}
	return &UTF8Error{fmt.Sprintf("'utf-8' codec can't decode bytes in position %d-%d: %s", start, end-1, reason)}
}
