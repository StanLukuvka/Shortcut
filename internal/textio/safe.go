// Package textio is where shortcut's text leaves and enters: everything printed is made safe first, a question
// cannot be forged, and a diff can use the pager.
package textio

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Safe makes control characters safe: they are printed, never obeyed. Kept: tab and newline. Replaced: U+0000-0008,
// U+000B-001F and U+007F as ^X (the character xor 0x40), U+0080-009F, U+2028, U+2029, U+202A-202E and U+2066-2069
// as \uXXXX (lowercase hex). A byte that is not UTF-8 passes through untouched.
func Safe(text string) string {
	i := 0
	for i < len(text) && !control(rune(text[i]), text[i:]) {
		i++
	}
	if i == len(text) {
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
			const hex = "0123456789abcdef"
			b.WriteString("\\u")
			for _, shift := range [...]uint{12, 8, 4, 0} {
				b.WriteByte(hex[r>>shift&0xf])
			}
		}
		i += n
	}
	return b.String()
}

// control reports whether the character starting rest is one to replace (the quick scan before any copying).
func control(first rune, rest string) bool {
	if first < 0x80 {
		return isControl(first)
	}
	r, _ := utf8.DecodeRuneInString(rest)
	return isControl(r)
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

// backslashReplace prints each byte that is not UTF-8 as \udcNN and each lone surrogate (WTF-8 ED A0..BF 80..BF) as
// \udXXX, the way Python's backslashreplace prints the surrogateescape'd str.
func backslashReplace(text string) string {
	var b strings.Builder
	for i := 0; i < len(text); {
		r, n := utf8.DecodeRuneInString(text[i:])
		switch {
		case r != utf8.RuneError || n != 1:
			b.WriteString(text[i : i+n])
		case i+2 < len(text) && text[i] == 0xed && text[i+1] >= 0xa0 && text[i+1] <= 0xbf && text[i+2]&0xc0 == 0x80:
			fmt.Fprintf(&b, "\\u%04x", 0xd000|int(text[i+1]&0x3f)<<6|int(text[i+2]&0x3f))
			n = 3
		default:
			fmt.Fprintf(&b, "\\udc%02x", text[i])
		}
		i += n
	}
	return b.String()
}
