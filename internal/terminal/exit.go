package terminal

import "github.com/StanLukuvka/Shortcut/internal/terminal/argparse"

// ExitError ends a command with a code and, optionally, text for stdout and stderr, which Main prints as it is
// (so a message carries its own "error: " and newline). Code 0 is -h; 2 is a usage error; 1 is Die.
// It is the error type of the argparse package, so a parser's exit passes through unchanged.
type ExitError = argparse.ExitError

// Namespace is what a parser returns: the value of each argument by its dest.
type Namespace = argparse.Namespace

// Die returns an error that prints "error: msg" on stderr and exits 1.
func Die(msg string) error { return &ExitError{Code: 1, Stderr: "error: " + msg + "\n"} }

// Confirmed asks c and turns a "no" into exit 1; nil means go ahead.
func Confirmed(s *Streams, c Confirm, assumeYes bool) error {
	granted, err := c.Granted(s, assumeYes)
	if err != nil {
		return err
	}
	if !granted {
		return &ExitError{Code: 1}
	}
	return nil
}
