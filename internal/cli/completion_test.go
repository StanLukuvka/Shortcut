package cli

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func TestCompletion(t *testing.T) {
	for shell, path := range map[string]string{
		"bash": "completion/bash.txt",
		"zsh":  "completion/zsh.txt",
		"fish": "completion/fish.txt",
	} {
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		got, err := completionFiles.ReadFile(path)
		if err != nil {
			t.Errorf("completionFiles %q missing: %v", path, err)
			continue
		}
		if len(got) == 0 {
			t.Errorf("completion %q is empty", shell)
		}
		if string(got) != string(want) {
			t.Errorf("completion %q differs from %s", shell, path)
		}
	}
}

func TestCompletion_Bash(t *testing.T) {
	e := testkit.New(t)
	res := e.Run("completion", "bash")
	if res.Code != 0 {
		t.Fatalf("exit %d: %s", res.Code, res.Stderr)
	}
	for _, want := range []string{"_shortcut", "complete -F _shortcut shortcut", "list --names", "run r", "show", "location", "rm", "update"} {
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
	check([]string{"--global", "--json"}, "show", "-")
	check([]string{"--global"}, "location", "--")
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
