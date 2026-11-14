package osutil

import (
	"os"
	"strings"
)

// ExpandUser expands "~" and "~/x" with $HOME. "~name" is not expanded, and a path with an unset or empty $HOME is left as typed.
func ExpandUser(path string) string { return expandWith(path, os.UserHomeDir) }

// expandWith is ExpandUser with the home lookup passed in (the test fixtures refuse an empty or unset HOME).
func expandWith(path string, userHomeDir func() (string, error)) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := userHomeDir() // os.UserHomeDir: $HOME; an error when unset or empty
	if err != nil {
		return path
	}
	if out := strings.TrimRight(home, "/") + path[1:]; out != "" {
		return out
	}
	return "/"
}
