package difflib

import (
	"fmt"
	"strconv"
	"strings"
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

// SplitLinesKeepEnds splits after each \n; a last line without one is kept. A \r, form feed, U+2028 and so on stay inside
// their line (textio.Safe makes them harmless when shown).
func SplitLinesKeepEnds(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
