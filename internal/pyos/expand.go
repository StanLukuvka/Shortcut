// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (G1-2). Don't hand-edit without checking the plan.

package pyos

import (
	"os"
	"os/user"
	"strings"
)

// ExpandUser is posixpath.expanduser: "~" and "~/x" use $HOME (even when empty), falling back to the account's
// home; "~name" is that account's home. Anything it cannot place comes back unchanged. The only user of os/user
// (with CGO_ENABLED=0 it reads /etc/passwd).
func ExpandUser(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	i := strings.Index(path[1:], "/") + 1
	if i == 0 {
		i = len(path)
	}
	var home string
	if i == 1 {
		h, ok := os.LookupEnv("HOME")
		if !ok {
			u, err := user.Current()
			if err != nil {
				return path
			}
			h = u.HomeDir
		}
		home = h
	} else {
		u, err := user.Lookup(path[1:i])
		if err != nil {
			return path
		}
		home = u.HomeDir
	}
	if out := strings.TrimRight(home, "/") + path[i:]; out != "" {
		return out
	}
	return "/"
}
