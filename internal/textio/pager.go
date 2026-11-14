package textio

import (
	"os"
	"os/exec"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/osutil"
)

// ShowDiff shows a diff: each line made Safe; on a terminal added lines green and removed red (the +++ and
// --- headers and context stay plain); then the text goes to $PAGER when stdout is a terminal, PAGER is set and
// there are at least as many lines as terminal rows, else straight to stdout. A pager that cannot start or a blank
// PAGER value falls through to printing the text directly; a pager that ran has shown it, whatever its exit status.
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
	if pager := os.Getenv("PAGER"); tty && pager != "" && len(shown) >= osutil.TerminalRows() {
		if runPager(s, pager, text) {
			return
		}
	}
	s.Out.WriteStyled(text)
}

func runPager(s *Streams, pager, text string) bool {
	argv := strings.Fields(pager) // lazy: no quoting, so a pager path with a space does not work; shell-split if it is ever missed
	if len(argv) == 0 {
		return false
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = os.Environ()
	if _, ok := os.LookupEnv("LESS"); !ok {
		cmd.Env = append(cmd.Env, "LESS=FRX")
	}
	cmd.Stdin = strings.NewReader(text)
	cmd.Stdout, cmd.Stderr = s.Out.File(), s.Err.File()
	if err := cmd.Start(); err != nil {
		return false // could not start: the caller prints the text itself
	}
	cmd.Wait() // a pager that ran has shown it; its exit status does not matter (a quit by signal is not a failure)
	return true
}
