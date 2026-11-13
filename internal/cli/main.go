// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (P-1). Don't hand-edit without checking the plan.

// Package cli is shortcut's command line: dispatch, argument parsing and the commands.
package cli

import (
	"errors"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/StanLukuvka/Shortcut/internal/textio"
)

// ExitError ends a command with a code and, optionally, text for stdout and stderr, which Main prints as it is
// (so a message carries its own "error: " and newline). Code 0 is argparse's -h; 2 is a usage error; 1 is Die.
type ExitError struct {
	Code           int
	Stdout, Stderr string
}

func (e *ExitError) Error() string { return e.Stderr }

// Die is die(): "error: msg" on stderr, exit 1.
func Die(msg string) error { return &ExitError{Code: 1, Stderr: "error: " + msg + "\n"} }

// handler is a command: it gets what follows the command word, and ends with nil (exit 0), an *ExitError or any
// other error, which dispatch prints as "error: <text>" with exit 1 (C5).
type handler func(s *textio.Streams, args []string) error

var handlers map[string]handler

func init() {
	handlers = map[string]handler{"run": cmdRun, "r": cmdRun, "completion": cmdCompletion,
		"list": cmdList, "show": cmdShow, "location": cmdLocation, "update": cmdUpdate}
	// lazy: these commands only parse their arguments (so -h and usage errors work) until the slice that ports each
	// one replaces its entry: new in P-4, rm in P-5, promote in P-6, init in P-7.
	for _, name := range []string{"new", "rm", "promote", "init"} {
		handlers[name] = func(s *textio.Streams, args []string) error {
			if _, err := parsers[name].Parse(args); err != nil {
				return err
			}
			return notPorted(name)
		}
	}
}

func notPorted(name string) error { return Die("shortcut " + name + " is not ported to Go yet") }

// Main is main(): it runs the command and returns the process's exit code. A reader that stops (shortcut list |
// head) ends it with 141, as a program ended by SIGPIPE does (C21); Notify turns SIGPIPE into an EPIPE error.
func Main(args []string, in, out, errw *os.File) int {
	s := textio.NewStreams(in, out, errw)
	sigpipe := make(chan os.Signal, 1)
	signal.Notify(sigpipe, syscall.SIGPIPE)
	rc := dispatch(s, args)
	if err := s.Flush(); errors.Is(err, syscall.EPIPE) {
		return 141
	}
	return rc
}

// dispatch is dispatch (cli.py): the first word picks the command.
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
