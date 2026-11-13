// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (P-1). Don't hand-edit without checking the plan.

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
