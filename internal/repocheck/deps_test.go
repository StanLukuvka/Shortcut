package repocheck

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// shortcut is stdlib only: go.mod has no require line or block.
func TestGoModHasNoRequire(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if regexp.MustCompile(`(?m)^\s*require\b`).Match(raw) {
		t.Fatalf("go.mod has a require:\n%s", raw)
	}
}
