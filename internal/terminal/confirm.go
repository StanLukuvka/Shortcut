package terminal

import (
	"bufio"
	"errors"
	"io"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/platform"
	"github.com/StanLukuvka/Shortcut/internal/terminal/argparse"
)

// Die returns an error that prints "error: msg" on stderr and exits 1.
func Die(msg string) error { return &argparse.ExitError{Code: 1, Stderr: "error: " + msg + "\n"} }

// Confirm is every question shortcut asks before it runs or changes something. Message says what will happen and is
// shown above the question; -y answers yes; without a terminal it stops with NoTerminal.
type Confirm struct {
	Question   string
	NoTerminal string
	Message    string
	TellOnYes  bool // show Message with -y too, for a change that can't be taken back
}

// Confirmed asks c and returns nil when the answer is yes. With -y it does not ask; a Message that could not be
// written is an error. With no terminal on stdin it returns an error carrying NoTerminal. Otherwise Message and the
// question go to stderr (so they reach the terminal when stdout is redirected) and one line is read: yes is its
// trimmed lower case being y or yes. Anything else, and end of input (which also prints a newline to stdout), prints
// "Aborted." and returns an ExitError with code 1. A read error other than EOF is returned, and so is a failed write
// of the question: an answer to a question nobody saw does not count.
func Confirmed(s *Streams, c Confirm, assumeYes bool) error {
	if assumeYes {
		if c.TellOnYes && c.Message != "" {
			s.Out.WriteString(c.Message + "\n")
			return s.Out.Failed()
		}
		return nil
	}
	if !platform.IsTerminal(int(s.In.Fd())) {
		return errors.New(c.NoTerminal)
	}
	if c.Message != "" {
		s.Err.WriteString(c.Message + "\n")
	}
	s.Err.WriteString(c.Question + " [y/N] ")
	if err := s.Err.Failed(); err != nil {
		return err
	}
	line, err := bufio.NewReader(s.In).ReadString('\n')
	if err != nil && err != io.EOF {
		return err
	}
	if err != nil && line == "" {
		s.Out.WriteString("\n")
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	}
	s.Err.WriteString("Aborted.\n")
	return &argparse.ExitError{Code: 1}
}
