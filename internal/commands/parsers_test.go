package commands

import (
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/terminal/argparse"
)

// Every command's parser prints the help the table renders on -h.
func TestParsers_HelpIsTheTable(t *testing.T) {
	for _, c := range commands() {
		_, err := c.Parse([]string{"-h"})
		ee, ok := err.(*argparse.ExitError)
		if !ok || ee.Code != 0 || ee.Stdout != c.HelpText() || ee.Stderr != "" {
			t.Errorf("%s -h: %v", c.Name, err)
		}
	}
}
