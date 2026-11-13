// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (G1-2). Don't hand-edit without checking the plan.

//go:build linux || darwin

package pyos

import (
	"os"
	"os/signal"
	"strings"
	"syscall"
)

// ExecChild is exec_child (os.execvpe): flush, chdir, default SIGPIPE and SIGXFSZ, then replace this process, so the
// exit code, signals and terminal are the child's. It returns only on failure. env is "K=V" strings; the PATH
// search uses env's PATH (see pathOf), not this process's.
func ExecChild(argv []string, cwd string, env []string, flush func()) error {
	flush() // stdout buffer, before the image is replaced
	if err := os.Chdir(cwd); err != nil {
		return err
	}
	path, err := lookPath(argv[0], pathOf(env))
	if err != nil {
		return err
	}
	signal.Reset(syscall.SIGPIPE, syscall.SIGXFSZ) // a Notify'd handler is reset by exec anyway; this ends the runtime's too
	if err := syscall.Exec(path, argv, env); err != nil {
		return &os.PathError{Op: "exec", Path: path, Err: err}
	}
	return nil
}

// pathOf is the PATH value in env, or /bin:/usr/bin (os.defpath) when env has none. PATH= is a search path with one
// empty entry, which means the cwd.
func pathOf(env []string) string {
	for i := len(env) - 1; i >= 0; i-- { // last duplicate wins, as in a dict built from the list
		if v, ok := strings.CutPrefix(env[i], "PATH="); ok {
			return v
		}
	}
	return "/bin:/usr/bin"
}

// lookPath is os._execvpe's search: a name with a / is used as it is; otherwise each entry of path (empty = cwd) is
// tried in order and the first file that can be executed wins. Not exec.LookPath: that refuses "." entries and
// execvpe does not. An entry that exists but is not executable is remembered: EACCES if nothing better turns up.
// lazy: executability is access(X_OK) on a non-directory, not a trial execve (Python tries each); upgrade: loop syscall.Exec.
func lookPath(file, path string) (string, error) {
	if strings.Contains(file, "/") {
		return file, nil
	}
	denied := false
	for _, dir := range strings.Split(path, ":") {
		full := pyJoin(dir, file)
		st, err := os.Stat(full)
		if err != nil || st.IsDir() {
			continue
		}
		if syscall.Access(full, 1) != nil {
			denied = true
			continue
		}
		return full, nil
	}
	if denied {
		return "", &os.PathError{Op: "exec", Path: file, Err: syscall.EACCES}
	}
	return "", &os.PathError{Op: "exec", Path: file, Err: syscall.ENOENT}
}
