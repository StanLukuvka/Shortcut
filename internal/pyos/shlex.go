package pyos

import "strings"

// ShellQuote is shlex.quote: unchanged when it is only ASCII letters, digits and _@%+=:,./-; two single quotes for empty; else
// single-quoted with ' written as '"'"'.
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

// ShellJoin quotes each word and joins them with spaces, as shlex.join does.
func ShellJoin(parts []string) string {
	q := make([]string, len(parts))
	for i, p := range parts {
		q[i] = ShellQuote(p)
	}
	return strings.Join(q, " ")
}
