package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

// A NUL in a cwd cannot be a directory: the run is refused and the command does not start.
func TestRunning_ANulInCwdRefusesTheRun(t *testing.T) {
	e := testkit.New(t)
	marker := filepath.Join(e.Tmp, "ran")
	e.WriteStore(e.Store, map[string]any{"nul": map[string]any{"inline": "touch " + marker, "cwd": "a\x00b"}})
	res := e.Run("run", "nul")
	res.WantCode(t, 1)
	if _, err := os.Stat(marker); err == nil {
		t.Error("the command ran")
	}
}
