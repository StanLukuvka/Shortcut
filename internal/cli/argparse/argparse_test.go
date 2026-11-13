package argparse

import "testing"

func TestPyRepr(t *testing.T) {
	for in, want := range map[string]string{
		"a":           "'a'",
		"it's":        "\"it's\"",
		"a\"'b":       "'a\"\\'b'",
		"\\n":         "'\\\\n'",
		"x\ny\t":      "'x\\ny\\t'",
		"\x01\u0085é": "'\\x01\\x85é'",
		"\xff":        "'\\udcff'",
	} {
		if got := PyRepr(in); got != want {
			t.Errorf("PyRepr(%q) = %s, want %s", in, got, want)
		}
	}
}
