//go:build linux || darwin

package platform

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// ExecChild runs argv in cwd by replacing this process, so the exit code, signals and terminal are the child's.
// It returns only on failure. env is "K=V" strings for the child; argv[0] without a "/" is found with
// exec.LookPath on this process's PATH, which refuses a match reached through an empty, "." or relative entry.
func ExecChild(argv []string, cwd string, env []string) error {
	if err := os.Chdir(cwd); err != nil {
		return err
	}
	path := argv[0]
	if !strings.Contains(path, "/") {
		p, err := exec.LookPath(path)
		if err != nil {
			return err
		}
		path = p
	}
	if err := syscall.Exec(path, argv, env); err != nil {
		return &os.PathError{Op: "exec", Path: path, Err: err}
	}
	return nil
}
