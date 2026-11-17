// Package commands is shortcut's command line: the command table, dispatch and the usage text. Each command is a
// package below it.
package commands

import (
	"errors"
	"os"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/platform"
	"github.com/StanLukuvka/Shortcut/internal/store"
	"github.com/StanLukuvka/Shortcut/internal/terminal"
	"github.com/StanLukuvka/Shortcut/internal/terminal/argparse"
)

// invoke builds the run's one Context and runs the command.
func invoke(s *terminal.Streams, e *entry, args []string) int {
	cwd, err := platform.Getwd()
	c := store.NewContext(cwd, err)
	if c.GlobalErr != nil { // no command works on a store it can't name
		return finish(s, c.GlobalErr)
	}
	return finish(s, e.Run(c, s, e.Command, args))
}

// Main runs the command and returns the process's exit code. A write to a closed stdout or stderr ends the
// process by SIGPIPE (the Go runtime's default; this program does not Notify it). Any other write error is kept by
// the stream: if stdout failed, "error: writing stdout: ..." goes to stderr; either failure makes an exit code of 0 a 1.
func Main(args []string, in, out, errw *os.File) int {
	s := terminal.NewStreams(in, out, errw)
	code := dispatch(s, args)
	outErr := s.Out.Failed()
	if outErr != nil {
		s.Err.WriteString("error: writing stdout: " + outErr.Error() + "\n")
	}
	if code == 0 && (outErr != nil || s.Err.Failed() != nil) {
		return 1
	}
	return code
}

// dispatch runs the command the first word picks.
func dispatch(s *terminal.Streams, args []string) int {
	if len(args) == 0 {
		s.Out.WriteString(usageText())
		return 0
	}
	first := args[0]
	switch {
	case first == "--help" || first == "-h":
		s.Out.WriteString(usageText())
		return 0
	case first == "--version":
		s.Out.WriteString("shortcut " + Version + "\n")
		return 0
	case strings.HasPrefix(first, "-"):
		s.Err.WriteString("error: unrecognized option '" + first + "'\n")
		return 2
	}
	if cmd := find(first); cmd != nil {
		return invoke(s, cmd, args[1:])
	}
	s.Err.WriteString("error: unknown command '" + first + "'\n")
	return 2
}

// finish turns a command's result into its exit code, printing what an *ExitError or an error carries.
func finish(s *terminal.Streams, err error) int {
	var ee *argparse.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ee):
		s.Out.WriteString(ee.Stdout)
		s.Err.WriteString(ee.Stderr)
		return ee.Code
	}
	s.Err.WriteString("error: " + err.Error() + "\n")
	return 1
}
