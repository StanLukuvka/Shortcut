package store

import (
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/platform"
)

// mustReal is the resolved path of one that exists.
func mustReal(t *testing.T, p string) string {
	t.Helper()
	real, err := platform.Realpath(p)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func doc(t *testing.T, s string) platform.Doc {
	t.Helper()
	d, err := platform.ParseDoc([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return d
}
