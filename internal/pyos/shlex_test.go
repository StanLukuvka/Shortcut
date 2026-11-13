package pyos

import (
	"slices"
	"testing"
)

func TestShlex_Quote(t *testing.T) { // values from Python 3.14 shlex.quote
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

func TestShlex_Split(t *testing.T) { // values from Python 3.14 shlex.split
	for in, want := range map[string][]string{
		"": nil, "   ": nil, "less -R": {"less", "-R"}, "a  b\tc": {"a", "b", "c"}, "'a b' c": {"a b", "c"},
		`"a b" c`: {"a b", "c"}, `a\ b`: {"a b"}, `"a\"b"`: {`a"b`}, `"a\nb"`: {`a\nb`}, `'a\nb'`: {`a\nb`},
		"''": {""}, `x""y`: {"xy"}, "a'b c'd": {"ab cd"}, `"a\\b"`: {`a\b`}, "a\nb": {"a", "b"}, "é ü": {"é", "ü"},
		`--x="1 2"`: {"--x=1 2"}, "a#b # c": {"a#b", "#", "c"}, `\'`: {"'"}, `"'"`: {"'"}, "a\\\nb": {"a\nb"},
	} {
		got, err := ShellSplit(in)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("ShellSplit(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for in, want := range map[string]string{
		"\\": "No escaped character", "'abc": "No closing quotation", `"abc`: "No closing quotation", `"abc\\`: "No closing quotation",
	} {
		if _, err := ShellSplit(in); err == nil || err.Error() != want {
			t.Errorf("ShellSplit(%q) error = %v, want %q", in, err, want)
		}
	}
}
