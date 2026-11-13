package cli

import "embed"

// helpFiles are the `-h` texts of the commands, captured once at 80 columns.
//
//go:embed help/*.txt
var helpFiles embed.FS

// runUsage is what `run -h` prints, with a newline from print.
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
