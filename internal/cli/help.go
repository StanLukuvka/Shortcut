// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (P-1). Don't hand-edit without checking the plan.

package cli

import "embed"

// helpFiles are the `-h` texts of the commands, captured once from Python's argparse at 80 columns (G11).
//
//go:embed help/*.txt
var helpFiles embed.FS

// runUsage is RUN_USAGE (cli.py): what `run -h` prints, with a newline from print.
const runUsage = "usage: shortcut run|r [--global] [-y] <name> [args...]\n       shortcut run|r [--global] [-y] <namespace> <name> [args...]"

// helpText is what `shortcut <cmd> -h` and `shortcut help <cmd>` print; cmd is a name in handlers, "r" included.
func helpText(cmd string) string {
	if cmd == "run" || cmd == "r" {
		return runUsage + "\n"
	}
	b, err := helpFiles.ReadFile("help/" + cmd + ".txt")
	if err != nil {
		panic("no help text for " + cmd) // a command without its file is a build mistake, and a test finds it
	}
	return string(b)
}

// usageOf is the usage block of a command's help: its lines up to the first blank one. argparse prints it above an error.
func usageOf(cmd string) string {
	text := helpText(cmd)
	for i := 0; i+1 < len(text); i++ {
		if text[i] == '\n' && text[i+1] == '\n' {
			return text[:i+1]
		}
	}
	return text
}
