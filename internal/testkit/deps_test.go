// FLOW-PRODUCED: temporary marker; written by the LAD flow for .lad/plans/20261113-0238-go-rewrite.md (Q-2). Don't hand-edit without checking the plan.
package testkit

import (
	"os"
	"regexp"
	"testing"
)

// The port is stdlib only: go.mod has no require line or block.
func TestGoModHasNoRequire(t *testing.T) {
	raw, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if regexp.MustCompile(`(?m)^\s*require\b`).Match(raw) {
		t.Fatalf("go.mod has a require:\n%s", raw)
	}
}
