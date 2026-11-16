// Package commands is shortcut's command line: the command table, dispatch and the usage text. Each command is a
// package below it.
package commands

import (
	"errors"
	"os"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/platform"
	"github.com/StanLukuvka/Shortcut/internal/store"
	"github.com/StanLukuvka/Shortcut/internal/term"
)

// invoke builds the run's one Context and runs the command.
func invoke(s *term.Streams, e *entry, args []string) int {
	cwd, err := platform.Getwd()
	return finish(s, e.Run(store.NewContext(cwd, err), s, e.Command, args))
}

// Main runs the command and returns the process's exit code. A write to a closed stdout or stderr ends the
// process by SIGPIPE (the Go runtime's default; this program does not Notify it).
func Main(args []string, in, out, errw *os.File) int {
	return dispatch(term.NewStreams(in, out, errw), args)
}

// dispatch runs the command the first word picks.
func dispatch(s *term.Streams, args []string) int {
	if len(args) == 0 {
		s.Out.WriteString(usageText())
		return 0
	}
	first := args[0]
	cmd := find(first)
	switch {
	case first == "--help" || first == "-h":
		s.Out.WriteString(usageText())
		return 0
	case first == "--version":
		s.Out.WriteString("shortcut " + Version + "\n")
		return 0
	case cmd != nil:
		return invoke(s, cmd, args[1:])
	case strings.HasPrefix(first, "-"):
		s.Err.WriteString("error: unrecognized option '" + first + "'\nTry 'shortcut --help' for more information.\n")
		return 2
	}
	s.Err.WriteString("error: unknown command '" + first + "' (to run a shortcut: shortcut run " + first + ")\n")
	return 2
}

// finish turns a command's result into its exit code, printing what an *ExitError or an error carries.
func finish(s *term.Streams, err error) int {
	var ee *term.ExitError
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
