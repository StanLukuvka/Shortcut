package new

import "testing"

func TestJoinCommand(t *testing.T) {
	cases := []struct {
		words []string
		want  string
	}{
		{[]string{"echo a | wc"}, "echo a | wc"},
		{[]string{"echo", "a b"}, "echo 'a b'"},
		{[]string{"echo", "$1"}, `echo "$1"`},
		{[]string{"echo", "$HOME $@"}, `echo "\$HOME $@"`},
		{[]string{"echo", "${1}x"}, `echo "${1}x"`},
	}
	for _, c := range cases {
		if got := joinCommand(c.words); got != c.want {
			t.Errorf("%q: got %s, want %s", c.words, got, c.want)
		}
	}
}
