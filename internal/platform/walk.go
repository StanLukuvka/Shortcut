package platform

import (
	"io/fs"
	"os"
)

// Walk walks top top-down without following links. visit gets each folder with its subfolders and its other
// entries (names, not paths), sorted. A symlink to a folder is listed in dirs and not entered, so approval sees
// links as folders without leaving the tree; a symlink to a file, a broken link, a FIFO or a device is in files.
// filepath.WalkDir classifies links differently. A folder that cannot be read is skipped silently. visit cannot
// prune dirs.
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
