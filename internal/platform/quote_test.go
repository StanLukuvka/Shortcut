package platform

import "testing"

func TestQuote(t *testing.T) {
	for in, want := range map[string]string{
		"": "''", "a": "a", "a b": "'a b'", "it's": `'it'"'"'s'`, "a/b-c_d.e": "a/b-c_d.e", "é": "'é'", "$x": "'$x'",
		"a=b,c:d@e%f+g": "a=b,c:d@e%f+g", "a\nb": "'a\nb'",
	} {
		if got := ShellQuote(in); got != want {
			t.Errorf("ShellQuote(%q) = %q, want %q", in, got, want)
		}
	}
	if got := ShellJoin([]string{"shortcut", "r", "-y", "a b", ""}); got != "shortcut r -y 'a b' ''" {
		t.Errorf("ShellJoin = %q", got)
	}
	if ShellJoin(nil) != "" {
		t.Error("ShellJoin(nil)")
	}
}
