package pyos

import (
	"errors"
	"strings"
)

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

// ShellSplit is shlex.split(s) (POSIX mode, no comments): the state machine of shlex.read_token with
// whitespace_split on. Errors carry Python's messages.
func ShellSplit(s string) ([]string, error) {
	const (
		start = iota // between words
		word         // inside a word
		single
		double
		escape // after a backslash; back is where to return
	)
	var (
		out      []string
		tok      []rune
		state    = start
		back     int  // state an escape returns to
		escQuote rune // the quote an escape sits in, 0 outside quotes
		inWord   bool // a word has begun; a quote or a backslash begins one, so '' is an empty word
		emit     = func() { out = append(out, string(tok)); tok, inWord = nil, false }
		isSpace  = func(r rune) bool { return r == ' ' || r == '\t' || r == '\r' || r == '\n' }
	)
	for _, r := range s {
		switch state {
		case start, word:
			switch {
			case isSpace(r):
				if inWord {
					emit()
				}
				state = start
			case r == '\\':
				back, escQuote, state, inWord = word, 0, escape, true
			case r == '\'':
				state, inWord = single, true
			case r == '"':
				state, inWord = double, true
			default:
				tok = append(tok, r)
				state, inWord = word, true
			}
		case single:
			if r == '\'' {
				state = word
			} else {
				tok = append(tok, r)
			}
		case double:
			switch r {
			case '"':
				state = word
			case '\\':
				back, escQuote, state = double, '"', escape
			default:
				tok = append(tok, r)
			}
		case escape:
			// In double quotes only the quote itself or the backslash may be escaped; otherwise the backslash stays.
			if escQuote != 0 && r != '\\' && r != escQuote {
				tok = append(tok, '\\')
			}
			tok = append(tok, r)
			state = back
		}
	}
	switch state {
	case single, double:
		return nil, errors.New("No closing quotation")
	case escape:
		return nil, errors.New("No escaped character")
	}
	if inWord {
		emit()
	}
	return out, nil
}
