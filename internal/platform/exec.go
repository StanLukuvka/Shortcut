//go:build linux || darwin

package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// ExecChild runs argv in cwd by replacing this process, so the exit code, signals and terminal are the child's.
// It returns only on failure. env is "K=V" strings and supplies the PATH used to find argv[0], not this
// process's PATH.
func ExecChild(argv []string, cwd string, env []string) error {
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
	if err := syscall.Exec(path, argv, env); err != nil {
		return &os.PathError{Op: "exec", Path: path, Err: err}
	}
	return nil
}

// pathOf is the last PATH value in env, or /bin:/usr/bin when env has none.
func pathOf(env []string) string {
	for i := len(env) - 1; i >= 0; i-- { // the last duplicate wins
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
