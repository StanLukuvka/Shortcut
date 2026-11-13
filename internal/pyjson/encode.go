package pyjson

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Opts mirrors json.dumps(indent=, sort_keys=) with ensure_ascii always on. Indent 0 is Python's indent=None (one line,
// separators ", " and ": ").
// lazy: Python's indent=0 (newlines without indentation) is not expressible; nothing in shortcut uses it.
type Opts struct {
	Indent   int
	SortKeys bool
}

// DumpsCompact is json.dumps(v) with the default separators and ensure_ascii.
func DumpsCompact(v Value) string { return Dumps(v, Opts{}) }

// Dumps is json.dumps(v, ...) without a trailing newline. A Value of a type outside the model panics.
func Dumps(v Value, o Opts) string {
	var b strings.Builder
	encode(&b, v, o, 0)
	return b.String()
}

func encode(b *strings.Builder, v Value, o Opts, level int) {
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if x {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case string:
		WriteString(b, x)
	case json.Number:
		b.WriteString(string(x)) // the number as it was written
	case float64:
		b.WriteString(ReprFloat(x))
	case []Value:
		if len(x) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteByte('[')
		for i, e := range x {
			separator(b, o, level+1, i)
			encode(b, e, o, level+1)
		}
		closer(b, o, level, ']')
	case *Object:
		if len(x.Keys) == 0 {
			b.WriteString("{}")
			return
		}
		keys := x.Keys
		if o.SortKeys {
			keys = SortStrings(slices.Clone(keys))
		}
		b.WriteByte('{')
		for i, k := range keys {
			separator(b, o, level+1, i)
			WriteString(b, k)
			b.WriteString(": ")
			encode(b, x.Vals[k], o, level+1)
		}
		closer(b, o, level, '}')
	default:
		panic(fmt.Sprintf("pyjson: cannot encode %T", v))
	}
}

func separator(b *strings.Builder, o Opts, level, i int) {
	if i > 0 {
		b.WriteByte(',')
		if o.Indent == 0 {
			b.WriteByte(' ')
		}
	}
	if o.Indent > 0 {
		b.WriteByte('\n')
		b.WriteString(strings.Repeat(" ", o.Indent*level))
	}
}

func closer(b *strings.Builder, o Opts, level int, c byte) {
	if o.Indent > 0 {
		b.WriteByte('\n')
		b.WriteString(strings.Repeat(" ", o.Indent*level))
	}
	b.WriteByte(c)
}

const hexDigits = "0123456789abcdef"

// WriteString writes s as a JSON string literal the way Python does. s is read as Python's str: UTF-8, a WTF-8
// surrogate triple as that surrogate, any other invalid byte 0xNN as U+DC00+NN (see Compare). Every
// code point outside space..~ except the escapes below becomes \uXXXX (lower hex, astral as a surrogate pair).
func WriteString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for i := 0; i < len(s); {
		r, n := pyRune(s[i:])
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20:
			writeU(b, r)
		case r >= 0x20 && r <= 0x7e:
			b.WriteByte(byte(r))
		case r >= 0x10000:
			r -= 0x10000
			writeU(b, 0xD800+(r>>10))
			writeU(b, 0xDC00+(r&0x3FF))
		default:
			writeU(b, r)
		}
		i += n
	}
	b.WriteByte('"')
}

func writeU(b *strings.Builder, r rune) {
	b.WriteString(`\u`)
	for shift := 12; shift >= 0; shift -= 4 {
		b.WriteByte(hexDigits[(r>>uint(shift))&0xF])
	}
}
