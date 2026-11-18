package platform

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func TestFiles_CantTellIsAnErrorNotMissing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can look in any folder")
	}
	e := testkit.New(t)
	locked := e.Folder("locked")
	f := e.WriteFile(filepath.Join(locked, "f"), "x", 0)
	if ok, err := IsFile(f); !ok || err != nil {
		t.Fatalf("readable: %v, %v", ok, err)
	}
	if ok, err := IsFile(filepath.Join(locked, "none")); ok || err != nil {
		t.Errorf("missing: %v, %v", ok, err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	for name, probe := range map[string]func(string) (bool, error){"IsFile": IsFile, "Exists": Exists, "Lexists": Lexists} {
		if ok, err := probe(f); ok || err == nil {
			t.Errorf("%s in a folder that can't be searched: %v, %v", name, ok, err)
		}
	}
}
