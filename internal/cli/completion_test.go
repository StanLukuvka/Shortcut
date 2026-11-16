package cli

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func TestCompletion_Bash(t *testing.T) {
	e := testkit.New(t)
	res := e.Run("completion", "bash")
	if res.Code != 0 {
		t.Fatalf("exit %d: %s", res.Code, res.Stderr)
	}
	for _, want := range []string{"_shortcut", "complete -F _shortcut shortcut", "list --names", "run r", "show", "rm", "update"} {
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("bash script lacks %q", want)
		}
	}
	if bash, _ := completionFiles.ReadFile("completion/bash.txt"); res.Stdout != string(bash) {
		t.Error("bash is the script of `completion bash`")
	}
	// The shell is a required choice.
	for _, c := range []struct {
		argv []string
		want string
	}{
		{[]string{"completion", "powershell"}, "error: invalid choice 'powershell' (choose from bash, fish, zsh)\nTry 'shortcut completion -h' for more information.\n"},
		{[]string{"completion"}, "error: missing shell\nTry 'shortcut completion -h' for more information.\n"},
	} {
		if r := e.Run(c.argv...); r.Code != 2 || r.Stdout != "" || r.Stderr != c.want {
			t.Errorf("%v: exit %d stdout %q stderr %q", c.argv, r.Code, r.Stdout, r.Stderr)
		}
	}
}

func TestCompletion_ZshAndFish(t *testing.T) {
	e := testkit.New(t)
	zsh := e.Run("completion", "zsh")
	if zsh.Code != 0 || !strings.Contains(zsh.Stdout, "compdef _shortcut shortcut") {
		t.Errorf("zsh: exit %d", zsh.Code)
	}
	fish := e.Run("completion", "fish")
	if fish.Code != 0 || !strings.Contains(fish.Stdout, "complete -c shortcut") {
		t.Errorf("fish: exit %d", fish.Code)
	}
}

func TestCompletion_BashPastFlagsAndIntoNamespaces(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"other": map[string]any{"inline": "true"}})
	e.WriteImport(e.Store, "docker", testkit.NS(map[string]any{
		"delete": map[string]any{"inline": "true"}, "ps": map[string]any{"inline": "true"}}))
	runOK(t, e, testkit.Opts{}, "update", "docker", "-y") // namespaces list once approved
	check := func(want []string, words ...string) {
		t.Helper()
		got := e.BashComplete(words...)
		if !slices.Equal(got, want) && !(len(got) == 0 && len(want) == 0) {
			t.Errorf("complete %v: got %v, want %v", words, got, want)
		}
	}
	// Flags: the script offers them without asking the binary.
	check([]string{"--global", "-y"}, "r", "-")
	check([]string{"--global", "--json", "--field"}, "show", "-")
	check(nil, "r", "other", "--")
	// Names: the script runs `shortcut list --names`.
	if !slices.Contains(e.BashComplete("r", "o"), "other") {
		t.Errorf("complete r o: no other")
	}
	check([]string{"other"}, "r", "--global", "ot")
	check([]string{"docker"}, "r", "-y", "--global", "doc")
	check([]string{"other"}, "show", "--global", "ot")
	check([]string{"delete", "ps"}, "r", "docker", "")
	check([]string{"delete"}, "rm", "--global", "docker", "d")
	check(nil, "r", "docker", "ps", "")
	check(nil, "r", "other", "")
}

// The static completion scripts name every command and alias, the options of the commands that take names, and the
// shells `completion` offers.
func TestCompletion_FollowsTheTable(t *testing.T) {
	has := func(text, word string) bool { // a whole word: "r" is not the "-r" of a fish option
		return regexp.MustCompile(`(^|[^\w-])` + regexp.QuoteMeta(word) + `([^\w-]|$)`).MatchString(text)
	}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		b, err := completionFiles.ReadFile("completion/" + shell + ".txt")
		if err != nil {
			t.Fatal(err)
		}
		text := string(b)
		for _, c := range commands() {
			for _, w := range append([]string{c.Name}, c.Aliases...) {
				if !has(text, w) {
					t.Errorf("%s: no command word %q", shell, w)
				}
			}
			var words []string
			for _, a := range c.Args {
				switch {
				case a.Choices != nil:
					words = append(words, a.Choices...)
				case a.Flags != nil && slices.ContainsFunc(c.Args, func(p Arg) bool { return p.Flags == nil && (p.Nargs == '+' || p.Nargs == '*') }):
					flag := a.Flags[0]
					if shell == "fish" { // fish spells -y as -s y and --json as -l json
						flag = "-l " + strings.TrimPrefix(flag, "--")
						if !strings.HasPrefix(a.Flags[0], "--") {
							flag = "-s " + strings.TrimPrefix(a.Flags[0], "-")
						}
					}
					words = append(words, flag)
				}
			}
			for _, w := range words {
				if !has(text, w) {
					t.Errorf("%s: %s: no %q", shell, c.Name, w)
				}
			}
		}
	}
}
