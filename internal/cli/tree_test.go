package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// treeOf is every file under root: its path, mode and bytes.
func treeOf(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := os.ReadFile(p)
		rel, _ := filepath.Rel(root, p)
		out[rel] = info.Mode().String() + "\n" + string(data)
		return nil
	})
	return out
}
