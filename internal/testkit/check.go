package testkit

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// Small assertions and readers shared by every package's tests.

// Eq fails the test (and goes on) unless got and want are deeply equal.
func Eq(t testing.TB, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %#v, want %#v", what, got, want)
	}
}

func Contains(t testing.TB, what, text, want string) {
	t.Helper()
	if !strings.Contains(text, want) {
		t.Errorf("%s: %q lacks %q", what, text, want)
	}
}

func Matches(t testing.TB, text, pattern string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(text) {
		t.Errorf("no match for %s in:\n%s", pattern, text)
	}
}

// WantErr fails unless err is non-nil and its text contains want.
func WantErr(t testing.TB, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("got error %v, want one containing %q", err, want)
	}
}

// WantCode fails unless the process exited with want; the output goes in the message.
func (r Result) WantCode(t testing.TB, want int) {
	t.Helper()
	if r.Code != want {
		t.Errorf("exit %d, want %d\nstdout: %s\nstderr: %s", r.Code, want, r.Stdout, r.Stderr)
	}
}

func ReadFile(t testing.TB, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func NotExist(t testing.TB, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err == nil {
		t.Errorf("%s exists", path)
	}
}

// Golden is a frozen data file, given as a path from the module root, found whatever the test's cwd.
func Golden(t testing.TB, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(moduleRoot(), filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// Have reports whether a program is on PATH. Only testkit may import os/exec, so tests ask here.
func Have(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// Need skips the test unless every program is on PATH.
func Need(t testing.TB, programs ...string) {
	t.Helper()
	for _, p := range programs {
		if !Have(p) {
			t.Skipf("%s is not installed", p)
		}
	}
}

// Inline is a shortcut that runs this shell text.
func Inline(text string) map[string]any { return map[string]any{"inline": text} }

// NS is the body of a version 1 namespace or store file holding these shortcuts.
func NS(shortcuts map[string]any) map[string]any {
	return map[string]any{"version": 1, "shortcuts": shortcuts}
}

// HelperMain is TestMain for a package whose test binary doubles as the program under test: when envVar is set
// the binary runs job(its value) and exits; otherwise it runs the tests. Start it with SelfArgs.
func HelperMain(m *testing.M, envVar string, job func(string)) {
	if v := os.Getenv(envVar); v != "" {
		job(v)
		os.Exit(0)
	}
	Main(m)
}

// SelfArgs is the command line that starts the running test binary without running any test.
func SelfArgs() []string { return []string{os.Args[0], "-test.run=^$"} }
