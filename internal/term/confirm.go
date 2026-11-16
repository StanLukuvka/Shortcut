package term

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/StanLukuvka/Shortcut/internal/platform"
)

// Ask asks a question: the prompt goes to stderr through Safe (so it reaches the terminal when stdout is redirected),
// one line is read, and it counts when its trimmed lower case is y or yes. End of input or Ctrl-C
// prints a newline to stdout and answers no. SIGINT is caught only while
// waiting, elsewhere it keeps its default and kills the process. A read error other than EOF is returned.
// On Ctrl-C the reading goroutine is left behind.
func Ask(s *Streams, prompt string) (bool, error) {
	// Catch SIGINT before the prompt is shown: whoever sees the prompt (a person, a test) may press Ctrl-C at once.
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, syscall.SIGINT)
	defer signal.Stop(interrupt)
	s.Err.WriteString(prompt)
	if s.in == nil {
		s.in = bufio.NewReader(s.In)
	}
	type result struct {
		line string
		err  error
	}
	got := make(chan result, 1)
	go func() {
		line, err := s.in.ReadString('\n')
		got <- result{line, err}
	}()
	select {
	case r := <-got:
		if r.err != nil && r.err != io.EOF {
			return false, r.err
		}
		if r.err != nil && r.line == "" {
			s.Out.WriteString("\n")
			return false, nil
		}
		switch strings.ToLower(strings.TrimSpace(r.line)) {
		case "y", "yes":
			return true, nil
		}
		return false, nil
	case <-interrupt:
		s.Out.WriteString("\n")
		return false, nil
	}
}

// Confirm is every question shortcut asks before it runs or changes something. Message says what will happen and is
// shown above the question; -y answers yes; without a terminal it stops with NoTerminal.
type Confirm struct {
	Question   string
	NoTerminal string
	Message    string
	TellOnYes  bool // show Message with -y too, for a change that can't be taken back
}

// Granted reports whether the answer was yes. With no terminal on stdin it returns an error carrying NoTerminal: the caller's error
// path prints it as "error: ..." and exits 1. After a "no" it has printed "Aborted." and
// returns false.
func (c Confirm) Granted(s *Streams, assumeYes bool) (bool, error) {
	if assumeYes {
		if c.TellOnYes && c.Message != "" {
			s.Out.WriteString(c.Message + "\n")
		}
		return true, nil
	}
	if !platform.IsTerminal(int(s.In.Fd())) {
		return false, errors.New(c.NoTerminal)
	}
	if c.Message != "" {
		s.Err.WriteString(c.Message + "\n")
	}
	yes, err := Ask(s, c.Question+" [y/N] ")
	if err != nil {
		return false, err
	}
	if yes {
		return true, nil
	}
	s.Err.WriteString("Aborted.\n")
	return false, nil
}
