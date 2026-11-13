package cli

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/StanLukuvka/Shortcut/internal/cli/argparse"
	"github.com/StanLukuvka/Shortcut/internal/pyjson"
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

// pyTruthy is Python's bool() of a JSON value.
func pyTruthy(v pyjson.Value) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		f, _ := strconv.ParseFloat(string(x), 64)
		return f != 0
	case []pyjson.Value:
		return len(x) > 0
	case *pyjson.Object:
		return len(x.Keys) > 0
	}
	return true
}

// pyStr is Python's str() of a JSON value: a string as it is, the rest as repr shows it.
func pyStr(v pyjson.Value) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case bool:
		if x {
			return "True"
		}
		return "False"
	case string:
		return x
	case json.Number:
		return string(x)
	case []pyjson.Value:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = reprOf(e)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *pyjson.Object:
		parts := make([]string, len(x.Keys))
		for i, k := range x.Keys {
			parts[i] = pyRepr(k) + ": " + reprOf(x.Vals[k])
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return fmt.Sprint(v)
}

func reprOf(v pyjson.Value) string {
	if str, ok := v.(string); ok {
		return pyRepr(str)
	}
	return pyStr(v)
}

// padRight is f"{s:<n}": padded to n code points, never cut.
func padRight(s string, n int) string {
	if w := utf8.RuneCountInString(s); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s
}

// pyRepr is repr() of a str, which argparse needs for its messages too.
var pyRepr = argparse.PyRepr

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
