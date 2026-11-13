// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (G1-2). Don't hand-edit without checking the plan.

package textio

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

// Every code point, against helpers.CONTROL_CHARS and helpers.safe written out independently of Safe.
func TestSafe_EveryCodePoint(t *testing.T) {
	control := regexp.MustCompile(`[\x00-\x08\x0b-\x1f\x7f-\x9f\x{2028}\x{2029}\x{202a}-\x{202e}\x{2066}-\x{2069}]`)
	expect := func(r rune) string {
		if !control.MatchString(string(r)) {
			return string(r)
		}
		if r < 0x80 {
			return "^" + string(r^0x40)
		}
		return fmt.Sprintf("\\u%04x", r)
	}
	changed := 0
	for r := rune(0); r <= utf8.MaxRune; r++ {
		if r >= 0xd800 && r <= 0xdfff {
			continue // not a code point a Go string can hold
		}
		want := expect(r)
		if want != string(r) {
			changed++
		}
		if got := Safe(string(r)); got != want {
			t.Fatalf("Safe(U+%04X) = %q, want %q", r, got, want)
		}
	}
	// 0x00-08 (9) + 0x0b-1f (21) + 0x7f-9f (33) + 2028, 2029 (2) + 202a-202e (5) + 2066-2069 (4)
	if changed != 9+21+33+2+5+4 {
		t.Errorf("%d code points are replaced, want 74", changed)
	}
}

func TestSafe_Text(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"plain text, tabs\tand\nnewlines stay", "plain text, tabs\tand\nnewlines stay"},
		{"clean\x1b[2K\rfaked \u009b1m \u202egnp", "clean^[[2K^Mfaked \\u009b1m \\u202egnp"}, // test_output_safety.py's description
		{"\x00\x7f", "^@^?"},
		{"é日本\U0001F600", "é日本\U0001F600"},
		{"a\u2027b\u202fc\u2065d\u206ae", "a\u2027b\u202fc\u2065d\u206ae"}, // just outside the ranges
		{"a\xffb\xc3", "a\xffb\xc3"},                                       // bytes that are not UTF-8 pass through
		{"\xffend\x1b", "\xffend^["},
		{"\u2028x\u2029", "\\u2028x\\u2029"},
	} {
		if got := Safe(c.in); got != c.want {
			t.Errorf("Safe(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if s := strings.Repeat("ordinary ", 1000); Safe(s) != s {
		t.Error("long text changed")
	}
}
