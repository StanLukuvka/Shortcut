// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (P-1). Don't hand-edit without checking the plan.

package cli

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

// goldenCase is one case of argparse_errors.json or argparse_parses.json. The generator ran Python 3.14 at 80 columns.
type goldenCase struct {
	Command    string         `json:"command"`
	Argv       []string       `json:"argv"`
	ParserArgv *[]string      `json:"parser_argv"`
	Namespace  map[string]any `json:"namespace"`
	Exit       *int           `json:"exit"`
	Stdout     string         `json:"stdout"`
	Stderr     string         `json:"stderr"`
}

func loadGolden(t *testing.T, name string) []goldenCase {
	t.Helper()
	raw, err := os.ReadFile("../testdata/text/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Cases []goldenCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Cases) == 0 {
		t.Fatalf("%s has no cases", name)
	}
	return f.Cases
}

// parserWords is what the parser saw: `new` cuts its words at the first -- before argparse (cmd_new).
func parserWords(c goldenCase) []string {
	if c.ParserArgv != nil {
		return *c.ParserArgv
	}
	if c.Command == "new" {
		for i, w := range c.Argv {
			if w == "--" {
				return c.Argv[:i]
			}
		}
	}
	return c.Argv
}

// roundTrip is what the namespace looks like as JSON, the form the golden file has it in.
func roundTrip(t *testing.T, ns Namespace) map[string]any {
	t.Helper()
	raw, err := json.Marshal(ns)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// Every error case of the golden file that is the parser's: the exact stderr and exit code. (The dispatch cases are
// the binary's, in dispatch_test.go.)
func TestArgparse_ErrorsMatchPython(t *testing.T) {
	n := 0
	for _, c := range loadGolden(t, "argparse_errors.json") {
		p := parsers[c.Command]
		if p == nil {
			continue
		}
		n++
		t.Run(c.Command+" "+strings.Join(c.Argv, " "), func(t *testing.T) {
			_, err := p.Parse(parserWords(c))
			ee, ok := err.(*ExitError)
			if !ok {
				t.Fatalf("got %v, want exit %d", err, *c.Exit)
			}
			if ee.Code != *c.Exit || ee.Stderr != c.Stderr || ee.Stdout != "" {
				t.Errorf("exit %d stdout %q stderr:\n%s\nwant exit %d stderr:\n%s", ee.Code, ee.Stdout, ee.Stderr, *c.Exit, c.Stderr)
			}
		})
	}
	if n != 86 {
		t.Errorf("replayed %d parser error cases, the file has 86 for parsers", n)
	}
}

// Every case of argparse_parses.json: the namespace Python built, or the exit it made (-h: 0 and the help on stdout).
func TestArgparse_ParsesMatchPython(t *testing.T) {
	cases := loadGolden(t, "argparse_parses.json")
	for _, c := range cases {
		t.Run(c.Command+" "+strings.Join(c.Argv, " "), func(t *testing.T) {
			ns, err := parsers[c.Command].Parse(parserWords(c))
			if c.Exit != nil {
				ee, ok := err.(*ExitError)
				if !ok {
					t.Fatalf("got namespace %v, want exit %d", ns, *c.Exit)
				}
				if ee.Code != *c.Exit || ee.Stdout != c.Stdout || ee.Stderr != c.Stderr {
					t.Errorf("exit %d stdout %q stderr %q; want exit %d stdout %q stderr %q", ee.Code, ee.Stdout, ee.Stderr, *c.Exit, c.Stdout, c.Stderr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := roundTrip(t, ns); !reflect.DeepEqual(got, c.Namespace) {
				t.Errorf("namespace %v, want %v", got, c.Namespace)
			}
		})
	}
	if len(cases) != 111 {
		t.Errorf("the file has %d cases, expected 111", len(cases))
	}
}

// The help a parser prints is the file the binary embeds, and the usage above an error is that file's first lines.
func TestArgparse_UsageIsTheStartOfTheHelp(t *testing.T) {
	for name := range parsers {
		text, usage := helpText(name), usageOf(name)
		if !strings.HasPrefix(usage, "usage: shortcut "+name+" ") || !strings.HasPrefix(text, usage+"\n") || strings.Contains(usage, "\n\n") {
			t.Errorf("%s: usage %q is not the lines before the first blank one of %q", name, usage, text)
		}
	}
}

func TestArgparse_Repr(t *testing.T) {
	for in, want := range map[string]string{
		"a":           "'a'",
		"it's":        "\"it's\"",
		"a\"'b":       "'a\"\\'b'",
		"\\n":         "'\\\\n'",
		"x\ny\t":      "'x\\ny\\t'",
		"\x01\u0085é": "'\\x01\\x85é'",
		"\xff":        "'\\udcff'",
	} {
		if got := pyRepr(in); got != want {
			t.Errorf("pyRepr(%q) = %s, want %s", in, got, want)
		}
	}
}
