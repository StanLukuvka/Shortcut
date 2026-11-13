package difflib

import (
	"fmt"
	"strconv"
	"unicode/utf8"
)

// formatRange formats a hunk's line range for its @@ header.
func formatRange(start, stop int) string {
	beginning, length := start+1, stop-start
	if length == 1 {
		return strconv.Itoa(beginning)
	}
	if length == 0 {
		beginning--
	}
	return fmt.Sprintf("%d,%d", beginning, length)
}

// Unified is difflib.unified_diff(a, b, from, to, lineterm="") with n=3 and no
// dates. Lines of a and b are used as given (keep their line ends).
func Unified(a, b []string, from, to string) []string {
	var out []string
	for gi, group := range newMatcher(a, b).groupedOpcodes(3) {
		if gi == 0 {
			out = append(out, "--- "+from, "+++ "+to)
		}
		first, last := group[0], group[len(group)-1]
		out = append(out, "@@ -"+formatRange(first.I1, last.I2)+" +"+formatRange(first.J1, last.J2)+" @@")
		for _, c := range group {
			if c.Tag == 'e' {
				for _, l := range a[c.I1:c.I2] {
					out = append(out, " "+l)
				}
				continue
			}
			if c.Tag == 'r' || c.Tag == 'd' {
				for _, l := range a[c.I1:c.I2] {
					out = append(out, "-"+l)
				}
			}
			if c.Tag == 'r' || c.Tag == 'i' {
				for _, l := range b[c.J1:c.J2] {
					out = append(out, "+"+l)
				}
			}
		}
	}
	return out
}

// SplitLinesKeepEnds is str.splitlines(keepends=True): it breaks after
// \n, \r, \r\n, \v, \f, \x1c-\x1e, \x85, U+2028 and U+2029.
func SplitLinesKeepEnds(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); {
		r, w := utf8.DecodeRuneInString(s[i:])
		i += w
		switch r {
		case '\r':
			if i < len(s) && s[i] == '\n' {
				i++
			}
		case '\n', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
		default:
			continue
		}
		out = append(out, s[start:i])
		start = i
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
