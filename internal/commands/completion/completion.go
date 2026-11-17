package completion

import (
	"embed"

	"github.com/StanLukuvka/Shortcut/internal/store"
	"github.com/StanLukuvka/Shortcut/internal/terminal"
)

//go:embed *.txt
var files embed.FS

// Script is the completion script of a shell: bash, fish or zsh.
func Script(shell string) ([]byte, error) { return files.ReadFile(shell + ".txt") }

// Run prints a completion script.
func Run(_ *store.Context, s *terminal.Streams, cmd terminal.Command, args []string) error {
	ns, err := cmd.Parse(args)
	if err != nil {
		return err
	}
	script, err := Script(ns.Str("shell"))
	if err != nil {
		return err
	}
	s.Out.WriteString(string(script))
	return nil
}
