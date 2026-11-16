package cli

import (
	"embed"

	"github.com/StanLukuvka/Shortcut/internal/store"
	"github.com/StanLukuvka/Shortcut/internal/term"
)

//go:embed completion/*.txt
var completionFiles embed.FS

func cmdCompletion(_ *store.Context, s *term.Streams, cmd term.Command, args []string) error {
	ns, err := cmd.Parse(args)
	if err != nil {
		return err
	}
	script, err := completionFiles.ReadFile("completion/" + ns.Str("shell") + ".txt")
	if err != nil {
		return err
	}
	_, err = s.Out.WriteString(string(script))
	return err
}
