package commands

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func y(text string) []byte { return []byte(text) }

func TestRunning_Confirm(t *testing.T) {
	e := testkit.New(t)
	work := e.Folder("work")
	// description as new --confirm -- <cmd> writes it: the command itself, unless --desc gives one
	e.WriteStore(e.Store, map[string]any{
		"danger": map[string]any{"inline": "echo ran; pwd", "description": "echo ran; pwd", "confirm": true},
		"wipe":   map[string]any{"inline": "echo ran", "description": "Wipes the cache", "confirm": true},
		"plain":  map[string]any{"inline": "echo ran", "description": "echo ran", "confirm": true},
		"noted":  map[string]any{"script": "scripts/none.sh", "description": "(no description)", "confirm": true},
		"drop":   map[string]any{"inline": `echo "dropped $*"`, "description": "x", "confirm": true},
		"echoes": testkit.Inline(`echo "$@"`),
	})
	o := in(work)

	res := e.RunWith(o, "r", "danger")
	if res.Code != 1 || res.Stdout != "" {
		t.Errorf("no terminal: exit %d stdout %q", res.Code, res.Stdout)
	}
	testkit.Contains(t, "no terminal", res.Stderr, "pass -y")
	res = e.RunWith(o, "r", "-y", "danger")
	res.WantCode(t, 0)
	testkit.Contains(t, "-y", res.Stdout, "ran")

	prompt := "run 'danger' in " + work + "? [y/N]"
	rc, out := e.InTerminal(o, []testkit.Key{{Wait: prompt, Send: y("y\n")}}, "r", "danger")
	if rc != 0 {
		t.Errorf("y: exit %d\n%s", rc, out)
	}
	testkit.Contains(t, "y", out, "ran")
	rc, out = e.InTerminal(o, []testkit.Key{{Wait: prompt, Send: y("n\n")}}, "r", "danger")
	if rc != 1 || strings.Contains(out, "ran\r\n") {
		t.Errorf("n: exit %d\n%s", rc, out)
	}

	// The description shows above the question, and not with -y
	prompt = "run 'wipe' in " + work + "? [y/N]"
	rc, out = e.InTerminal(o, []testkit.Key{{Wait: prompt, Send: y("n\n")}}, "r", "wipe")
	if rc != 1 {
		t.Errorf("wipe: exit %d", rc)
	}
	testkit.Contains(t, "wipe", out, "Wipes the cache\r\n")
	if strings.Index(out, "Wipes the cache") > strings.Index(out, "run 'wipe'") {
		t.Errorf("the description is not above the question:\n%s", out)
	}
	if got := e.RunWith(o, "r", "-y", "wipe").Stdout; strings.Contains(got, "Wipes the cache") {
		t.Errorf("-y shows the description: %q", got)
	}
	// No description given: nothing above the question, not a placeholder or the command
	for _, name := range []string{"plain", "noted"} {
		rc, out = e.InTerminal(o, []testkit.Key{{Wait: "run '" + name + "' in " + work + "? [y/N]", Send: y("n\n")}}, "r", name)
		if rc != 1 || strings.Contains(out, "echo ran") || strings.Contains(out, "(no description)") {
			t.Errorf("%s: exit %d\n%s", name, rc, out)
		}
	}
	// The description and question go to the terminal even when stdout is a file
	file := filepath.Join(e.Tmp, "out.txt")
	toFile := o
	toFile.StdoutTo = file
	rc, out = e.InTerminal(toFile, []testkit.Key{{Wait: "[y/N]", Send: y("y\n")}}, "r", "wipe")
	if rc != 0 {
		t.Errorf("stdout to a file: exit %d", rc)
	}
	testkit.Contains(t, "stdout to a file", out, "Wipes the cache")
	if raw, _ := os.ReadFile(file); string(raw) != "ran\n" {
		t.Errorf("out.txt: %q", raw)
	}

	// The question names the arguments, and the hint is the same run with -y
	rc, out = e.InTerminal(o, []testkit.Key{{Wait: "run 'drop' with -f 'my db' in " + work + "? [y/N]", Send: y("n\n")}}, "r", "drop", "-f", "my db")
	if rc != 1 {
		t.Errorf("drop: exit %d\n%s", rc, out)
	}
	res = e.RunWith(o, "r", "drop", "-f", "my db")
	testkit.Contains(t, "hint", res.Stderr, "pass -y (shortcut r -y drop -f 'my db')\n")
	if got := e.RunWith(o, "r", "-y", "drop", "-f", "my db").Stdout; got != "dropped -f my db\n" {
		t.Errorf("drop -y: %q", got)
	}

	// After the name, -y belongs to the script
	if got := e.Run("r", "echoes", "-y").Stdout; got != "-y\n" {
		t.Errorf("echoes -y: %q", got)
	}
}

func TestRunning_ARunKeepsTheRealTerminal(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"tty": testkit.Inline(`[ -t 1 ] && echo "stdout:tty" || echo "stdout:pipe"; stty size; echo ready; read -r line; echo "got:$line"`)})
	rc, out := e.InTerminal(testkit.Opts{}, []testkit.Key{{Wait: "ready", Send: y("hello\r")}}, "run", "tty")
	if rc != 0 {
		t.Errorf("exit %d\n%s", rc, out)
	}
	for _, want := range []string{"stdout:tty", "30 100", "got:hello"} {
		testkit.Contains(t, "terminal", out, want)
	}
	input := "x\n"
	res := e.RunWith(testkit.Opts{Input: &input}, "run", "tty")
	testkit.Contains(t, "pipe", res.Stdout, "stdout:pipe")
	testkit.Contains(t, "pipe", res.Stdout, "got:x")
}

func TestRunning_CtrlCReachesTheShortcut(t *testing.T) {
	e := testkit.New(t)
	e.WriteStore(e.Store, map[string]any{"sleep": testkit.Inline("echo started; sleep 10")})
	start := time.Now()
	rc, out := e.InTerminal(testkit.Opts{}, []testkit.Key{{Wait: "started", Send: y("\x03")}}, "run", "sleep")
	if rc != -int(syscall.SIGINT) {
		t.Errorf("exit %d, want death by SIGINT\n%s", rc, out)
	}
	if time.Since(start) > 8*time.Second {
		t.Errorf("took %v", time.Since(start))
	}
}

func TestApproval_AScriptChangedWhileConfirmWaitsDoesntRun(t *testing.T) {
	e := testkit.New(t)
	proj := e.Project(map[string]any{"d": map[string]any{"description": "d", "script": "d.sh", "confirm": true}},
		testkit.Files(map[string]any{"d.sh": "#!/usr/bin/env bash\necho approved\n"}), testkit.Approved(), testkit.Mode(0o755))
	swap := func() { e.WriteFile(filepath.Join(proj, "d.sh"), "#!/usr/bin/env bash\necho swapped\n", 0) }
	rc, out := e.InTerminal(in(proj), []testkit.Key{{Wait: "[y/N]", Do: swap}, {Wait: "[y/N]", Send: y("y\n")}}, "r", ".", "d")
	if rc != 1 {
		t.Errorf("exit %d\n%s", rc, out)
	}
	testkit.Contains(t, "output", out, "changed since you approved it")
	if strings.Contains(out, "swapped") {
		t.Errorf("the swapped script ran:\n%s", out)
	}
}
