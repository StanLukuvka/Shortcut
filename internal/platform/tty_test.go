package platform

import (
	"testing"

	"github.com/StanLukuvka/Shortcut/internal/testkit"
)

func TestTTY_StdinAndStdout(t *testing.T) {
	e := testkit.New(t)
	opts := func(env map[string]string) testkit.Opts {
		env["PLATFORM_HELPER"] = "tty"
		return testkit.Opts{Env: env}
	}
	if res := e.RunCmd(opts(map[string]string{}), testkit.SelfArgs()...); res.Stdout != "false false\n" { // /dev/null is not a terminal
		t.Errorf("no terminal: %q", res.Stdout)
	}
	code, out := e.InTerminalCmd(opts(map[string]string{}), nil, testkit.SelfArgs()...)
	if code != 0 || out != "true true\r\n" {
		t.Errorf("terminal: %d %q", code, out)
	}
}
