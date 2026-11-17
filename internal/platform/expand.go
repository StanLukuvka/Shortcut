package platform

import (
	"fmt"
	"os"
	"strings"
)

// ExpandUser expands "~" and "~/x" with $HOME. "~name" is not expanded. A "~" with $HOME unset, empty or not
// absolute is an error.
func ExpandUser(path string) (string, error) { return expandWith(path, os.UserHomeDir) }

// expandWith is ExpandUser with the home lookup passed in (the test fixtures refuse an empty or unset HOME).
func expandWith(path string, userHomeDir func() (string, error)) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path, nil
	}
	home, err := userHomeDir() // os.UserHomeDir: $HOME; an error when unset or empty
	if err != nil || !strings.HasPrefix(home, "/") {
		return "", fmt.Errorf("HOME must be set to an absolute path, so '~' in '%s' can't be expanded", path)
	}
	if out := strings.TrimRight(home, "/") + path[1:]; out != "" {
		return out, nil
	}
	return "/", nil
}
