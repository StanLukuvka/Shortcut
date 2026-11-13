// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (P-1). Don't hand-edit without checking the plan.

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
		got, ok := completionScripts[shell]
		if !ok {
			t.Errorf("completionScripts[%q] missing", shell)
			continue
		}
		if got == "" {
			t.Errorf("completionScripts[%q] is empty", shell)
		}
		if got != string(want) {
			t.Errorf("completionScripts[%q] differs from %s", shell, path)
		}
	}
}

// py: test_completion.py::test_completion_bash
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
	if res.Stdout != completionScripts["bash"] || e.Run("completion").Stdout != res.Stdout {
		t.Error("bash is the script of `completion bash` and of `completion`")
	}
	// The shell is a choice; argparse's message and exit 2 come from the parser (argparse_test.go).
	if r := e.Run("completion", "powershell"); r.Code != 2 || r.Stdout != "" {
		t.Errorf("completion powershell: exit %d stdout %q", r.Code, r.Stdout)
	}
}

// py: test_completion.py::test_completion_zsh_and_fish
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

// py: test_completion.py::test_bash_completion_past_flags_and_into_namespaces
func TestCompletion_BashPastFlagsAndIntoNamespaces(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"other": map[string]any{"inline": "true"}})
	e.WriteImport(e.Store, "docker", map[string]any{"version": 2, "shortcuts": map[string]any{
		"delete": map[string]any{"inline": "true"}, "ps": map[string]any{"inline": "true"}}})
	approveNamespace(t, e, "docker") // Python ran `update docker -y` (P-3); namespaces list once approved
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
