package cli

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Python-semantics shims: str and repr behaviour the frozen output depends on.

// isPySpace is str.isspace() for one character: Python counts U+001C..U+001F as white space too.
func isPySpace(r rune) bool { return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f) }

// pyRstrip is str.rstrip().
func pyRstrip(s string) string { return strings.TrimRightFunc(s, isPySpace) }

// replaceInvalidUTF8 is bytes.decode("utf-8", "replace"): one U+FFFD for each maximal invalid subpart, so a
// truncated sequence is one and each stray byte is one (strings.ToValidUTF8 would merge a run into one).
func replaceInvalidUTF8(raw []byte) string {
	var b strings.Builder
	for i := 0; i < len(raw); {
		r, n := utf8.DecodeRune(raw[i:])
		if r != utf8.RuneError || n != 1 {
			b.WriteString(string(raw[i : i+n]))
			i += n
			continue
		}
		// a lead byte followed by the continuation bytes it is allowed: that much is one bad sequence
		lo, hi, need := byte(0x80), byte(0xBF), 0
		switch c := raw[i]; {
		case c >= 0xC2 && c <= 0xDF:
			need = 1
		case c >= 0xE0 && c <= 0xEF:
			need = 2
			if c == 0xE0 {
				lo = 0xA0
			} else if c == 0xED {
				hi = 0x9F
			}
		case c >= 0xF0 && c <= 0xF4:
			need = 3
			if c == 0xF0 {
				lo = 0x90
			} else if c == 0xF4 {
				hi = 0x8F
			}
		}
		j := i + 1
		for k := 0; k < need && j < len(raw) && raw[j] >= lo && raw[j] <= hi; k++ {
			j++
			lo, hi = 0x80, 0xBF
		}
		b.WriteString("\uFFFD")
		i = j
	}
	return b.String()
}

// padRight is f"{s:<n}": padded to n code points, never cut.
func padRight(s string, n int) string {
	if w := utf8.RuneCountInString(s); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s
}

// splitOnce is str.split(None, 1): the first word and what follows it, if anything.
func splitOnce(s string) []string {
	s = strings.TrimLeftFunc(s, isPySpace)
	if s == "" {
		return nil
	}
	i := strings.IndexFunc(s, isPySpace)
	if i < 0 {
		return []string{s}
	}
	return []string{s[:i], strings.TrimLeftFunc(s[i:], isPySpace)}
}
