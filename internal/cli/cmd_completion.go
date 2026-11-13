package cli

import "github.com/StanLukuvka/Shortcut/internal/textio"

func cmdCompletion(s *textio.Streams, args []string) error {
	ns, err := parsers["completion"].Parse(args)
	if err != nil {
		return err
	}
	_, err = s.Out.WriteString(completionScripts[ns.Str("shell")])
	return err
}
