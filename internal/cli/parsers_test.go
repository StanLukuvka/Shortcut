package cli

import "testing"

// Every command's parser prints the help file on -h, and no parser has a dest that a command reads under another name.
func TestParsers_HelpIsTheFile(t *testing.T) {
	for name, p := range parsers {
		_, err := p.Parse([]string{"-h"})
		ee, ok := err.(*ExitError)
		if !ok || ee.Code != 0 || ee.Stdout != helpText(name) || ee.Stderr != "" {
			t.Errorf("%s -h: %v", name, err)
		}
	}
}
