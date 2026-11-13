package pyos

import (
	"io/fs"
	"os"
)

// Walk is os.walk(top) top-down without followlinks. visit gets each folder with its subfolders and its other
// entries (names, not paths). A symlink to a folder is listed in dirs and is not entered; a symlink to a file, a
// broken link, a FIFO or a device is in files (os.DirEntry.is_dir follows links, DirEntry.Type does not, so
// filepath.WalkDir classifies differently). A folder that cannot be read is skipped silently, as os.walk does.
// Entries come sorted (Python's order is the filesystem's; every caller sorts).
// lazy: no pruning of dirs from visit; add it when a caller needs it.
func Walk(top string, visit func(dir string, dirs, files []string)) {
	entries, err := os.ReadDir(top)
	if err != nil {
		return
	}
	var dirs, files, enter []string
	for _, e := range entries {
		isDir := e.IsDir()
		link := e.Type()&fs.ModeSymlink != 0
		if link {
			st, err := os.Stat(top + "/" + e.Name())
			isDir = err == nil && st.IsDir()
		}
		if isDir {
			dirs = append(dirs, e.Name())
			if !link {
				enter = append(enter, e.Name())
			}
		} else {
			files = append(files, e.Name())
		}
	}
	visit(top, dirs, files)
	for _, name := range enter {
		Walk(Join(top, name), visit)
	}
}
