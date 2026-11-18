package e2e

import (
	"slices"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/commands/completion"
	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func TestCompletion_PrintsTheScriptOfEachShell(t *testing.T) {
	e := testkit.New(t)
	for _, c := range []struct {
		shell string
		wants []string
	}{
		{"bash", []string{"_shortcut", "complete -F _shortcut shortcut", "list --names", "run r", "show", "rm"}},
		{"zsh", []string{"compdef _shortcut shortcut"}},
		{"fish", []string{"complete -c shortcut"}},
	} {
		res := e.Run("completion", c.shell)
		if res.Code != 0 {
			t.Fatalf("%s: exit %d: %s", c.shell, res.Code, res.Stderr)
		}
		for _, want := range c.wants {
			testkit.Contains(t, c.shell+" script", res.Stdout, want)
		}
		if script, _ := completion.Script(c.shell); res.Stdout != string(script) {
			t.Errorf("%s is not the script of `completion %s`", c.shell, c.shell)
		}
	}
	// The shell is a required choice.
	for _, c := range []struct {
		argv []string
		want string
	}{
		{[]string{"completion", "powershell"}, "error: invalid choice 'powershell' (choose from bash, fish, zsh)\nTry 'shortcut completion -h' for more information.\n"},
		{[]string{"completion"}, "error: missing shell\nTry 'shortcut completion -h' for more information.\n"},
	} {
		e.Run(c.argv...).Want(t, 2, "", c.want)
	}
}

func TestCompletion_BashPastFlagsAndIntoNamespaces(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"other": map[string]any{"inline": "true"}})
	e.WriteImport(e.Store, "docker", testkit.NS(map[string]any{
		"delete": map[string]any{"inline": "true"}, "ps": map[string]any{"inline": "true"}}))
	check := func(want []string, words ...string) {
		t.Helper()
		got := e.BashComplete(words...)
		if !slices.Equal(got, want) && !(len(got) == 0 && len(want) == 0) {
			t.Errorf("complete %v: got %v, want %v", words, got, want)
		}
	}
	// Flags: the script offers them without asking the binary.
	check([]string{"-y"}, "r", "-")
	check([]string{"--json", "--field"}, "show", "-")
	check(nil, "r", "other", "--")
	// Names: the script runs `shortcut list --names`.
	if !slices.Contains(e.BashComplete("r", "o"), "other") {
		t.Errorf("complete r o: no other")
	}
	check([]string{"other"}, "r", "ot")
	check([]string{"docker"}, "r", "-y", "doc")
	check([]string{"other"}, "show", "ot")
	check([]string{"delete", "ps"}, "r", "docker", "")
	check([]string{"delete"}, "rm", "docker", "d")
	check(nil, "r", "docker", "ps", "")
	check(nil, "r", "other", "")
	// After the dot the names are the project's (`list --names .`): none outside a project, and no error
	check(nil, "r", ".", "")
	check(nil, "r", ".", "docker", "")
}
