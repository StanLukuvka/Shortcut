package approval

import (
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/difflib"
)

// DiffFiles is diff_files: a unified diff of two {path: content} maps. No rename detection: a path on one side only
// diffs against "", so any difference in the maps gives at least one line.
func DiffFiles(old, new map[string]string) []string {
	paths := map[string]string{}
	for p := range old {
		paths[p] = ""
	}
	for p := range new {
		paths[p] = ""
	}
	lines := []string{}
	for _, path := range sortedKeys(paths) {
		before, inOld := old[path]
		after, inNew := new[path]
		if inOld && inNew && before == after {
			continue
		}
		if strings.HasPrefix(after, BinaryMark) {
			lines = append(lines, "Binary file "+path+" changed")
			continue
		}
		if strings.HasPrefix(before, BinaryMark) {
			before = ""
		}
		shown := strings.TrimPrefix(path, "./")
		changed := difflib.Unified(difflib.SplitLinesKeepEnds(before), difflib.SplitLinesKeepEnds(after), "a/"+shown, "b/"+shown)
		for _, l := range changed {
			lines = append(lines, strings.TrimRight(l, "\r\n"))
		}
		if len(changed) == 0 { // an empty file added or removed
			lines = append(lines, "--- a/"+shown, "+++ b/"+shown)
		}
	}
	return lines
}
