package textio

import (
	"os"
	"os/exec"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/pyos"
)

// ShowDiff is helpers.show_diff: each line made Safe; on a terminal added lines green and removed red (the +++ and
// --- headers and context stay plain); then the text goes to $PAGER when stdout is a terminal, PAGER is set and
// there are at least as many lines as terminal rows, else straight to stdout. A pager that cannot start, a bad
// PAGER value or a non-zero exit falls through to printing the text directly (so it can print twice, C24).
func ShowDiff(s *Streams, lines []string) {
	tty := s.Out.IsTerminal()
	shown := make([]string, len(lines))
	for i, line := range lines {
		line = Safe(line)
		if tty && !strings.HasPrefix(line, "+++") && !strings.HasPrefix(line, "---") {
			switch {
			case strings.HasPrefix(line, "+"):
				line = "\x1b[32m" + line + "\x1b[0m"
			case strings.HasPrefix(line, "-"):
				line = "\x1b[31m" + line + "\x1b[0m"
			}
		}
		shown[i] = line
	}
	text := backslashReplace(strings.Join(shown, "\n") + "\n") // the pager gets the same escapes as a Writer, so no raw 0x9b reaches the terminal
	if pager := os.Getenv("PAGER"); tty && pager != "" && len(shown) >= pyos.TerminalRows() {
		if runPager(s, pager, text) {
			return
		}
	}
	s.Out.WriteStyled(text)
	s.Out.flushOrAbort()
}

func runPager(s *Streams, pager, text string) bool {
	argv, err := pyos.ShellSplit(pager)
	if err != nil || len(argv) == 0 { // lazy: Python's empty split raises IndexError (a traceback); printing is kinder
		return false
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = os.Environ()
	if _, ok := os.LookupEnv("LESS"); !ok {
		cmd.Env = append(cmd.Env, "LESS=FRX")
	}
	cmd.Stdin = strings.NewReader(text)
	cmd.Stdout, cmd.Stderr = s.Out.File(), s.Err.File()
	s.Out.flushOrAbort()
	return cmd.Run() == nil
}
