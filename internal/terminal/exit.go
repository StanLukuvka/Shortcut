package terminal

import "github.com/StanLukuvka/Shortcut/internal/terminal/argparse"

// Die returns an error that prints "error: msg" on stderr and exits 1.
func Die(msg string) error { return &argparse.ExitError{Code: 1, Stderr: "error: " + msg + "\n"} }
