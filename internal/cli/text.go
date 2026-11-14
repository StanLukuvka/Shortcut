package cli

import (
	"strings"
	"unicode/utf8"
)

// padRight pads s with spaces to n code points; a longer s is returned whole.
func padRight(s string, n int) string {
	if w := utf8.RuneCountInString(s); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s
}
