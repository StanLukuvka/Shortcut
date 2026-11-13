// Package cli is shortcut's command line: dispatch, argument parsing and the commands.
package cli

import (
	"errors"
	"os"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/cli/argparse"
	"github.com/StanLukuvka/Shortcut/internal/textio"
)

// ExitError ends a command with a code and, optionally, text for stdout and stderr, which Main prints as it is
// (so a message carries its own "error: " and newline). Code 0 is argparse's -h; 2 is a usage error; 1 is Die.
// It is argparse's error type, so a parser's exit passes through unchanged.
type ExitError = argparse.ExitError

// Namespace is what a parser returns: the value of each argument by its dest.
type Namespace = argparse.Namespace

// Die returns an error that prints "error: msg" on stderr and exits 1.
func Die(msg string) error { return &ExitError{Code: 1, Stderr: "error: " + msg + "\n"} }

// handler is a command: it gets what follows the command word, and ends with nil (exit 0), an *ExitError or any
// other error, which dispatch prints as "error: <text>" with exit 1.
type handler func(s *textio.Streams, args []string) error

var handlers map[string]handler

func init() {
	handlers = map[string]handler{"run": cmdRun, "r": cmdRun, "completion": cmdCompletion,
		"list": cmdList, "show": cmdShow, "location": cmdLocation, "update": cmdUpdate, "new": cmdNew, "rm": cmdRm, "promote": cmdPromote, "init": cmdInit}
}

// Main runs the command and returns the process's exit code. A write to a closed stdout or stderr ends the
// process by SIGPIPE (the Go runtime's default; this program does not Notify it).
func Main(args []string, in, out, errw *os.File) int {
	return dispatch(textio.NewStreams(in, out, errw), args)
}

// dispatch runs the command the first word picks.
func dispatch(s *textio.Streams, args []string) int {
	if len(args) == 0 {
		s.Out.WriteString(usageText)
		return 0
	}
	first := args[0]
	switch {
	case first == "help" && len(args) > 1 && handlers[args[1]] != nil:
		return finish(s, handlers[args[1]](s, []string{"--help"}))
	case first == "--help" || first == "-h" || first == "help":
		s.Out.WriteString(usageText)
		return 0
	case first == "--version":
		s.Out.WriteString("shortcut " + Version + "\n")
		return 0
	case handlers[first] != nil:
		return finish(s, handlers[first](s, args[1:]))
	case strings.HasPrefix(first, "-"):
		s.Err.WriteString("error: unrecognized option '" + first + "'\nTry 'shortcut --help' for more information.\n")
		return 2
	}
	s.Err.WriteString("error: unknown command '" + first + "' (to run a shortcut: shortcut run " + first + ")\n")
	return 2
}

// finish turns a command's result into its exit code, printing what an *ExitError or an error carries.
func finish(s *textio.Streams, err error) int {
	var ee *ExitError
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
