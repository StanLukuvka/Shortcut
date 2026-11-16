// Package term is where shortcut's text leaves and enters: everything printed is made safe first, a question
// cannot be forged. A command's words are described by Command and read by its parser, argparse.
package term

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Safe makes control characters safe: they are printed, never obeyed. Kept: tab and newline. Replaced: U+0000-0008,
// U+000B-001F and U+007F as ^X (the character xor 0x40), U+0080-009F, U+2028, U+2029, U+202A-202E and U+2066-2069
// as \uXXXX (lowercase hex). A byte that is not UTF-8 passes through untouched.
func Safe(text string) string {
	i := strings.IndexFunc(text, isControl)
	if i < 0 {
		return text
	}
	var b strings.Builder
	b.WriteString(text[:i])
	for i < len(text) {
		r, n := utf8.DecodeRuneInString(text[i:])
		switch {
		case r == utf8.RuneError && n == 1:
			b.WriteByte(text[i])
		case !isControl(r):
			b.WriteString(text[i : i+n])
		case r < 0x80:
			b.WriteByte('^')
			b.WriteByte(byte(r) ^ 0x40)
		default:
			fmt.Fprintf(&b, "\\u%04x", r)
		}
		i += n
	}
	return b.String()
}

// isControl reports whether r is a control character that Safe replaces.
func isControl(r rune) bool {
	switch {
	case r <= 0x08, r >= 0x0b && r <= 0x1f, r >= 0x7f && r <= 0x9f:
		return true
	case r == 0x2028, r == 0x2029, r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
		return true
	}
	return false
}

// backslashReplace prints each byte that is not UTF-8 as \xNN, never raw: 0x9b is CSI on an 8-bit terminal.
func backslashReplace(text string) string {
	var b strings.Builder
	for i := 0; i < len(text); {
		r, n := utf8.DecodeRuneInString(text[i:])
		if r == utf8.RuneError && n == 1 {
			fmt.Fprintf(&b, "\\x%02x", text[i])
		} else {
			b.WriteString(text[i : i+n])
		}
		i += n
	}
	return b.String()
}
