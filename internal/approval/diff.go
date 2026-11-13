package approval

import (
	"maps"
	"slices"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/difflib"
	"github.com/StanLukuvka/Shortcut/internal/pyjson"
)

// DiffFiles returns a unified diff of two {path: content} maps. No rename detection: a path on one side only
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
	for _, path := range slices.SortedFunc(maps.Keys(paths), pyjson.Compare) {
		before, inOld := old[path]
		after, inNew := new[path]
		if inOld && inNew && before == after {
			continue
		}
		if strings.HasPrefix(after, binaryMark) {
			lines = append(lines, "Binary file "+path+" changed")
			continue
		}
		if strings.HasPrefix(before, binaryMark) {
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
