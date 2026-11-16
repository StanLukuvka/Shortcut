package osutil

import "strings"

// ShellQuote quotes s for a POSIX shell: unchanged when it holds only ASCII letters, digits and _@%+=:,./-;
// two single quotes when empty; otherwise single-quoted with ' written as '"'"'. Used to print commands the
// user can paste.
func ShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("_@%+=:,./-", c) >= 0) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

// ShellJoin quotes each word and joins them with spaces.
func ShellJoin(parts []string) string {
	q := make([]string, len(parts))
	for i, p := range parts {
		q[i] = ShellQuote(p)
	}
	return strings.Join(q, " ")
}
