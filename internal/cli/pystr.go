package cli

import (
	"strings"
	"unicode/utf8"
)

// padRight is f"{s:<n}": padded to n code points, never cut.
func padRight(s string, n int) string {
	if w := utf8.RuneCountInString(s); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s
}
