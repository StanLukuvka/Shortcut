package cli

import (
	"slices"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/pyjson"
)

func argSpec(fields ...map[string]any) []*pyjson.Object {
	var spec []*pyjson.Object
	for _, f := range fields {
		o := &pyjson.Object{Vals: map[string]pyjson.Value{}}
		for k, v := range f {
			o.Keys = append(o.Keys, k)
			o.Vals[k] = v
		}
		spec = append(spec, o)
	}
	return spec
}

// bindArgs and joinCommand had no unit tests in Python (joinCommand is only reached through `new`); this
// is the smallest check that fails if the binding logic breaks.
func TestBindArgs(t *testing.T) {
	a, b := map[string]any{"name": "a", "required": true}, map[string]any{"name": "b", "default": "x"}
	rest := map[string]any{"name": "files", "rest": true}
	cases := []struct {
		what      string
		spec      []map[string]any
		argv, out []string
		env       []string
		fail      string
	}{
		{"required given", []map[string]any{a}, []string{"1"}, []string{"1"}, []string{model.ArgVar("a") + "=1"}, ""},
		{"default fills", []map[string]any{a, b}, []string{"1"}, []string{"1", "x"}, []string{model.ArgVar("a") + "=1", model.ArgVar("b") + "=x"}, ""},
		{"flags don't count", []map[string]any{a}, []string{"-v", "1"}, []string{"-v", "1"}, []string{model.ArgVar("a") + "=1"}, ""},
		{"-- lets a value start with a dash", []map[string]any{a}, []string{"--", "-x"}, []string{"-x"}, []string{model.ArgVar("a") + "=-x"}, ""},
		{"rest is shell-quoted", []map[string]any{rest}, []string{"x y", "z"}, []string{"x y", "z"}, []string{model.ArgVar("files") + "='x y' z"}, ""},
		{"missing", []map[string]any{a}, nil, nil, nil, "missing <a>"},
		{"empty", []map[string]any{a}, []string{""}, nil, nil, "<a> can't be empty"},
		{"too many", []map[string]any{a}, []string{"1", "2"}, nil, nil, "too many arguments"},
	}
	for _, c := range cases {
		out, env, err := bindArgs("go", argSpec(c.spec...), c.argv)
		if c.fail != "" {
			ee, ok := err.(*ExitError)
			if !ok || ee.Code != 2 || ee.Stderr[:len("error: "+c.fail)] != "error: "+c.fail {
				t.Errorf("%s: err %v", c.what, err)
			}
			continue
		}
		if err != nil || !slices.Equal(out, c.out) || !slices.Equal(env, c.env) {
			t.Errorf("%s: got %q %q %v, want %q %q", c.what, out, env, err, c.out, c.env)
		}
	}
}

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
