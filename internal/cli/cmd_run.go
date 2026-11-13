// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (P-1). Don't hand-edit without checking the plan.

package cli

import "github.com/StanLukuvka/Shortcut/internal/textio"

// cmdRun is cmd_run: --global, -y and --yes are the only options it reads, and only before the name; everything
// after that, -h included, is the shortcut's.
func cmdRun(s *textio.Streams, args []string) error {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		_, err := s.Out.WriteString(runUsage + "\n")
		return err
	}
	skipLocal, assumeYes := false, false
	for len(args) > 0 && (args[0] == "--global" || args[0] == "-y" || args[0] == "--yes") {
		skipLocal = skipLocal || args[0] == "--global"
		assumeYes = assumeYes || args[0] != "--global"
		args = args[1:]
	}
	if len(args) == 0 {
		return &ExitError{Code: 2, Stderr: "error: missing shortcut name\n" + runUsage + "\n"}
	}
	return runShortcut(s, args, skipLocal, assumeYes)
}

// lazy: runShortcut is run_shortcut (run.py), ported in P-3; until then a run says so. P-3 replaces this function.
func runShortcut(s *textio.Streams, args []string, skipLocal, assumeYes bool) error {
	return notPorted("run")
}
