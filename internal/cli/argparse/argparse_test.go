package argparse

import (
	"reflect"
	"testing"
)

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

func TestParse(t *testing.T) {
	help := func(cmd string) string { return "help of " + cmd + "\n" }
	yes := Flag("yes", "-y", "--yes")
	opts := func(extra ...*Action) *Parser {
		return New("t", help, append([]*Action{yes, Value("desc", "--desc"), Repeated("arg", "--arg"), Optional("names", "--names")}, extra...)...)
	}
	choice := Choice("shell", "bash", "bash", "fish", "zsh")
	const try = "\nTry 'shortcut t -h' for more information.\n"
	for _, c := range []struct {
		name string
		p    *Parser
		argv []string
		want Namespace // nil when an error is wanted
		err  string    // stderr, or "help"
	}{
		{"nothing", opts(Positional("n", '*')), nil, Namespace{"yes": false, "desc": nil, "arg": []string{}, "names": nil, "n": []string{}}, ""},
		{"value space", opts(), []string{"--desc", "v"}, Namespace{"yes": false, "desc": "v", "arg": []string{}, "names": nil}, ""},
		{"value equals", opts(), []string{"--desc=v"}, Namespace{"yes": false, "desc": "v", "arg": []string{}, "names": nil}, ""},
		{"empty value kept", opts(), []string{"--desc="}, Namespace{"yes": false, "desc": "", "arg": []string{}, "names": nil}, ""},
		{"empty word value", opts(), []string{"--desc", ""}, Namespace{"yes": false, "desc": "", "arg": []string{}, "names": nil}, ""},
		{"equals with dash", opts(), []string{"--desc=-v"}, Namespace{"yes": false, "desc": "-v", "arg": []string{}, "names": nil}, ""},
		{"value is a lone dash", opts(), []string{"--desc", "-"}, Namespace{"yes": false, "desc": "-", "arg": []string{}, "names": nil}, ""},
		{"short flag", opts(), []string{"-y"}, Namespace{"yes": true, "desc": nil, "arg": []string{}, "names": nil}, ""},
		{"long flag", opts(), []string{"--yes"}, Namespace{"yes": true, "desc": nil, "arg": []string{}, "names": nil}, ""},
		{"flag twice", opts(), []string{"-y", "-y"}, Namespace{"yes": true, "desc": nil, "arg": []string{}, "names": nil}, ""},
		{"repeated twice", opts(), []string{"--arg", "a", "--arg=b"}, Namespace{"yes": false, "desc": nil, "arg": []string{"a", "b"}, "names": nil}, ""},
		{"optional bare", opts(), []string{"--names"}, Namespace{"yes": false, "desc": nil, "arg": []string{}, "names": ""}, ""},
		{"optional with value", opts(), []string{"--names", "ns"}, Namespace{"yes": false, "desc": nil, "arg": []string{}, "names": "ns"}, ""},
		{"optional equals", opts(), []string{"--names=ns"}, Namespace{"yes": false, "desc": nil, "arg": []string{}, "names": "ns"}, ""},
		{"optional before option", opts(), []string{"--names", "-y"}, Namespace{"yes": true, "desc": nil, "arg": []string{}, "names": ""}, ""},
		{"optional before --", opts(), []string{"--names", "--"}, Namespace{"yes": false, "desc": nil, "arg": []string{}, "names": ""}, ""},
		{"positional between", opts(Positional("n", '+')), []string{"a", "-y", "b"}, Namespace{"yes": true, "desc": nil, "arg": []string{}, "names": nil, "n": []string{"a", "b"}}, ""},
		{"positional after option value", opts(Positional("n", '+')), []string{"--desc", "d", "a"}, Namespace{"yes": false, "desc": "d", "arg": []string{}, "names": nil, "n": []string{"a"}}, ""},
		{"dash positional", opts(Positional("n", '+')), []string{"-"}, Namespace{"yes": false, "desc": nil, "arg": []string{}, "names": nil, "n": []string{"-"}}, ""},
		{"-- ends options", opts(Positional("n", '+')), []string{"--", "-y", "--desc"}, Namespace{"yes": false, "desc": nil, "arg": []string{}, "names": nil, "n": []string{"-y", "--desc"}}, ""},
		{"-- after positional", opts(Positional("n", '+')), []string{"a", "-y", "--", "-rf"}, Namespace{"yes": true, "desc": nil, "arg": []string{}, "names": nil, "n": []string{"a", "-rf"}}, ""},
		{"nargs 0 one", New("t", help, Positional("n", 0)), []string{"a"}, Namespace{"n": "a"}, ""},
		{"nargs 0 none", New("t", help, Positional("n", 0)), nil, nil, "error: missing n" + try},
		{"nargs 0 two", New("t", help, Positional("n", 0)), []string{"a", "b"}, nil, "error: unexpected argument 'b'" + try},
		{"nargs ? none", New("t", help, Positional("n", '?')), nil, Namespace{"n": nil}, ""},
		{"nargs ? one", New("t", help, Positional("n", '?')), []string{"a"}, Namespace{"n": "a"}, ""},
		{"nargs ? two", New("t", help, Positional("n", '?')), []string{"a", "b"}, nil, "error: unexpected argument 'b'" + try},
		{"nargs + none", New("t", help, Positional("n", '+')), nil, nil, "error: missing n" + try},
		{"nargs + three", New("t", help, Positional("n", '+')), []string{"a", "b", "c"}, Namespace{"n": []string{"a", "b", "c"}}, ""},
		{"no positional", opts(), []string{"x"}, nil, "error: unexpected argument 'x'" + try},
		{"choice valid", New("t", help, choice), []string{"zsh"}, Namespace{"shell": "zsh"}, ""},
		{"choice default", New("t", help, choice), nil, Namespace{"shell": "bash"}, ""},
		{"choice invalid", New("t", help, choice), []string{"sh"}, nil, "error: invalid choice 'sh' (choose from bash, fish, zsh)" + try},
		{"choice required", New("t", help, Choice("shell", "", "bash")), nil, nil, "error: missing shell" + try},
		{"exclusive both", New("t", help, Value("a", "--a"), Value("b", "--b")).Exclusive("a", "b"), []string{"--a", "1", "--b", "2"}, nil, "error: --a and --b cannot be used together" + try},
		{"exclusive one", New("t", help, Value("a", "--a"), Value("b", "--b")).Exclusive("a", "b"), []string{"--b", "2"}, Namespace{"a": nil, "b": "2"}, ""},
		{"unknown option", opts(), []string{"--nope"}, nil, "error: unrecognized option '--nope'" + try},
		{"abbreviation", opts(), []string{"--des", "x"}, nil, "error: unrecognized option '--des'" + try},
		{"combined shorts", opts(), []string{"-yh"}, nil, "error: unrecognized option '-yh'" + try},
		{"negative number", opts(), []string{"-1"}, nil, "error: unrecognized option '-1'" + try},
		{"flag with value", opts(), []string{"--yes=1"}, nil, "error: option '--yes' takes no value" + try},
		{"value missing at end", opts(), []string{"--desc"}, nil, "error: option '--desc' needs a value" + try},
		{"value looks like option", opts(), []string{"--desc", "-v"}, nil, "error: option '--desc' needs a value" + try},
		{"value is --", opts(), []string{"--desc", "--"}, nil, "error: option '--desc' needs a value" + try},
		{"help", opts(), []string{"-y", "-h"}, nil, "help"},
		{"long help", opts(), []string{"--help", "--nope"}, nil, "help"},
	} {
		t.Run(c.name, func(t *testing.T) {
			ns, err := c.p.Parse(c.argv)
			if c.err == "" {
				if err != nil || !reflect.DeepEqual(ns, c.want) {
					t.Errorf("got %v, %v; want %v", ns, err, c.want)
				}
				return
			}
			ee, ok := err.(*ExitError)
			if !ok {
				t.Fatalf("got %v, %v; want an error", ns, err)
			}
			if c.err == "help" {
				if ee.Code != 0 || ee.Stdout != "help of t\n" || ee.Stderr != "" {
					t.Errorf("help: %+v", ee)
				}
			} else if ee.Code != 2 || ee.Stderr != c.err || ee.Stdout != "" {
				t.Errorf("got exit %d stderr %q; want exit 2 stderr %q", ee.Code, ee.Stderr, c.err)
			}
		})
	}
}
