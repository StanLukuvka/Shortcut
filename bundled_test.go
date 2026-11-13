package shortcut

import (
	"bytes"
	"os"
	"testing"
)

func TestBundled(t *testing.T) {
	names := []string{
		"templates/.shortcuts",
		"templates/scripts/desktop.sh",
		"templates/scripts/edit.sh",
		"templates/scripts/logged.sh",
		"templates/scripts/search.sh",
	}
	for _, name := range names {
		want, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s from disk: %v", name, err)
		}
		got, err := Bundled.ReadFile(name)
		if err != nil {
			t.Errorf("Bundled.ReadFile(%s): %v", name, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("Bundled.ReadFile(%s): bytes differ from disk", name)
		}
	}
}
