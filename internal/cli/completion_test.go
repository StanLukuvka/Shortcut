package cli

import (
	"os"
	"testing"
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
