package cli

import (
	"embed"

	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/textio"
)

//go:embed completion/*.txt
var completionFiles embed.FS

func cmdCompletion(_ *model.Context, s *textio.Streams, args []string) error {
	ns, err := parsers["completion"].Parse(args)
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
