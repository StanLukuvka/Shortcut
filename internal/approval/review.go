package approval

import (
	"slices"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/textio"
)

// ConfirmReview is the question after a diff. A binary file can't be shown, so taking one unseen has
// to be typed out.
func ConfirmReview(verb string, old, new map[string]string, noTerminal string) textio.Confirm {
	var unseen []string
	for path, text := range new {
		if strings.HasPrefix(text, binaryMark) && old[path] != text {
			unseen = append(unseen, path)
		}
	}
	if len(unseen) > 0 {
		return textio.Confirm{
			Question:   "can't show " + strings.Join(slices.Sorted(slices.Values(unseen)), ", ") + ". Type binary to " + verb + " it unseen:",
			NoTerminal: noTerminal, Accept: []string{"binary"},
		}
	}
	return textio.Confirm{Question: verb + "?", NoTerminal: noTerminal}
}
