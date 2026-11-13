//go:build linux || darwin

package pyos

import (
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

// ExecChild runs a child as os.execvpe does: flush, chdir, default SIGPIPE and SIGXFSZ, then replace this process, so the
// exit code, signals and terminal are the child's. It returns only on failure. env is "K=V" strings; the PATH
// search uses env's PATH (see pathOf), not this process's.
func ExecChild(argv []string, cwd string, env []string, flush func()) error {
	flush() // stdout buffer, before the image is replaced
	if err := os.Chdir(cwd); err != nil {
		return err
	}
	path := argv[0]
	if !strings.Contains(path, "/") {
		p, err := lookPathIn(pathOf(env), path)
		if err != nil {
			return err
		}
		path = p
	}
	signal.Reset(syscall.SIGPIPE, syscall.SIGXFSZ) // a Notify'd handler is reset by exec anyway; this ends the runtime's too
	if err := syscall.Exec(path, argv, env); err != nil {
		return &os.PathError{Op: "exec", Path: path, Err: err}
	}
	return nil
}

// pathOf is the PATH value in env, or /bin:/usr/bin (os.defpath) when env has none.
func pathOf(env []string) string {
	for i := len(env) - 1; i >= 0; i-- { // last duplicate wins, as in a dict built from the list
		if v, ok := strings.CutPrefix(env[i], "PATH="); ok {
			return v
		}
	}
	return "/bin:/usr/bin"
}

// lookPathIn is exec.LookPath against the absolute entries of searchPath; empty and relative entries ("", ".", "bin")
// never match, and the search goes on to the later entries. On failure: EACCES if an absolute entry holds a
// non-directory of that name (it exists but cannot be run), ENOENT otherwise. PATH is set for the call and restored;
// fine only because the process is about to exec.
func lookPathIn(searchPath, file string) (string, error) {
	var abs []string
	for _, dir := range filepath.SplitList(searchPath) {
		if filepath.IsAbs(dir) {
			abs = append(abs, dir)
		}
	}
	old, had := os.LookupEnv("PATH")
	os.Setenv("PATH", strings.Join(abs, string(os.PathListSeparator)))
	defer func() {
		if had {
			os.Setenv("PATH", old)
		} else {
			os.Unsetenv("PATH")
		}
	}()
	p, err := exec.LookPath(file)
	if err == nil {
		return p, nil
	}
	for _, dir := range abs {
		if st, serr := os.Stat(filepath.Join(dir, file)); serr == nil && !st.IsDir() {
			return "", &os.PathError{Op: "exec", Path: file, Err: syscall.EACCES}
		}
	}
	return "", &os.PathError{Op: "exec", Path: file, Err: syscall.ENOENT}
}
