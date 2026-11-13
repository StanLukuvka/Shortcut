package argparse

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// PyRepr is repr() of a str. A byte that is not UTF-8 is what Python's surrogateescape makes of it, \udcXX.
func PyRepr(s string) string {
	quote := byte('\'')
	if strings.Contains(s, "'") && !strings.Contains(s, "\"") {
		quote = '"'
	}
	var b strings.Builder
	b.WriteByte(quote)
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n == 1:
			fmt.Fprintf(&b, "\\udc%02x", s[i])
		case r == '\\':
			b.WriteString("\\\\")
		case r == rune(quote):
			b.WriteString("\\" + string(r))
		case r == '\n':
			b.WriteString("\\n")
		case r == '\r':
			b.WriteString("\\r")
		case r == '\t':
			b.WriteString("\\t")
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, "\\x%02x", r)
		case r < 0x7f || unicode.IsPrint(r):
			b.WriteRune(r)
		case r < 0x100:
			fmt.Fprintf(&b, "\\x%02x", r)
		case r < 0x10000:
			fmt.Fprintf(&b, "\\u%04x", r)
		default:
			fmt.Fprintf(&b, "\\U%08x", r)
		}
		i += n
	}
	b.WriteByte(quote)
	return b.String()
}
