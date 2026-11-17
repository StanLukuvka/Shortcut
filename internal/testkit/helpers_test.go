package testkit

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func entries(t *testing.T, dir string) []os.DirEntry {
	t.Helper()
	l, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// refused runs f and reports whether it was stopped by a SandboxEscape.
func refused(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		t.Helper()
		r := recover()
		err, _ := r.(error)
		var se SandboxEscape
		if !errors.As(err, &se) {
			t.Errorf("%s: want a SandboxEscape, got %v", what, r)
		}
	}()
	f()
}

// sandboxed is an Env whose refusals panic with the error (see Env.fail) instead of ending the test, with a
// stand-in binary, and a second temp dir for the real home to be.
func sandboxed(t *testing.T) (e *Env, outside string) {
	t.Helper()
	e = New(t)
	e.fail = func(err error) { panic(err) }
	useBinary(t, e, "exit 0\n")
	o, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return e, o
}

// useBinary makes `sh` script body the "binary" for the rest of the test.
func useBinary(t *testing.T, e *Env, body string) {
	t.Helper()
	testBinary = e.Script("fake-shortcut", "#!/bin/sh\n"+body)
	t.Cleanup(func() { testBinary = "" })
}
