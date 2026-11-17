package run

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"syscall"

	"github.com/StanLukuvka/Shortcut/internal/platform"
	"github.com/StanLukuvka/Shortcut/internal/store"
	"github.com/StanLukuvka/Shortcut/internal/term"
)

// Run runs a shortcut. -y and --yes are the only options it reads, and only before the name, and a . after them
// selects the project; everything after that, -h included, is the shortcut's.
func Run(c *store.Context, s *term.Streams, cmd term.Command, args []string) error {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		s.Out.WriteString(cmd.HelpText())
		return nil
	}
	assumeYes := false
	for len(args) > 0 && (args[0] == "-y" || args[0] == "--yes") {
		assumeYes = true
		args = args[1:]
	}
	scope, args := store.SplitScope(args)
	if len(args) == 0 {
		return &term.ExitError{Code: 2, Stderr: "error: missing shortcut name\n" + cmd.UsageLine() + "\n"}
	}
	if scope == store.ScopeProject && strings.HasPrefix(args[0], "-") { // a name can't start with -, so this is an option
		return term.Die("'" + args[0] + "' is not a name: options go before the dot, as in " +
			strings.TrimSpace("shortcut r -y . "+platform.ShellJoin(args[1:])))
	}
	return runShortcut(c, s, args, store.Lookup{Scope: scope, Cmd: "r"}, assumeYes)
}

func oneLine(text string) string { return strings.ReplaceAll(text, "\n", "\\n") }

// runShortcut handles the confirmation and the child process, which replaces this one.
// It returns only when something stops the run.
func runShortcut(c *store.Context, s *term.Streams, words []string, lk store.Lookup, assumeYes bool) error {
	if c.CwdErr != nil {
		return c.CwdErr
	}
	resolved, args, err := store.ResolveShortcut(c, words, store.Lookup{Scope: lk.Scope, Cmd: lk.Cmd, Strict: true})
	if err != nil {
		return err
	}
	entry, name := resolved.Entry, resolved.Display()
	if err := store.ValidateName(entry.Name); err != nil { // list warns about a hand-written one; run refuses it
		return err
	}
	isDir, err := platform.IsDir(resolved.RunCwd)
	if err != nil {
		return err
	}
	if !isDir {
		return errors.New("run directory does not exist: " + resolved.RunCwd)
	}
	withArgs := ""
	if len(args) > 0 {
		withArgs = " with " + oneLine(platform.ShellJoin(args))
	}
	again := []string{"shortcut", "r", "-y"}
	if lk.Scope == store.ScopeProject {
		again = append(again, ".")
	}
	again = append(append(again, strings.Fields(name)...), args...)
	realCwd, err := platform.Realpath(resolved.RunCwd)
	if err != nil {
		return err
	}
	if entry.Confirm {
		if err := term.Confirmed(s, term.Confirm{
			Question:   "run '" + name + "'" + withArgs + " in " + realCwd + "?",
			NoTerminal: "'" + name + "' asks for confirmation: run it in a terminal, or pass -y (" + oneLine(platform.ShellJoin(again)) + ")",
			Message:    oneLine(entry.Description),
		}, assumeYes); err != nil {
			return err
		}
	}
	show, err := showDir()
	if err != nil {
		return err
	}
	if show {
		line := term.Safe("\u2192 " + realCwd)
		if s.Err.IsTerminal() {
			line = "\x1b[2m" + line + "\x1b[0m"
		}
		s.Err.WriteStyled(line + "\n")
	}

	argv, err := childCommand(resolved, entry, name, args)
	if err != nil {
		return err
	}
	// A shortcut run from another one gets its own SHORTCUT_ variables, not the caller's
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		k, _, hasEq := strings.Cut(kv, "=")
		return !hasEq || k == "SHORTCUT_NAME" || k == "SHORTCUT_NAMESPACE" || k == "SHORTCUT_CALLER_CWD"
	})
	env = append(env, "SHORTCUT_NAME="+name, "SHORTCUT_NAMESPACE="+resolved.Group.Name, "SHORTCUT_CALLER_CWD="+c.Cwd)
	// The "→" line or the confirm text did not get out: report that, not a child run blind.
	if err := cmp.Or(s.Out.Failed(), s.Err.Failed()); err != nil {
		return err
	}
	err = platform.ExecChild(argv, resolved.RunCwd, env)
	if entry.Inline == nil && errors.Is(err, syscall.ENOEXEC) { // inline shortcuts exec bash, so this is a script
		return term.Die(argv[0] + " can't run as it is: it needs a #! line, such as #!/bin/sh, or to be a binary for this system")
	}
	return err
}

// showDir reads SHORTCUT_SHOW_DIR: unset or 1 shows the folder, 0 hides it, anything else is refused.
func showDir() (bool, error) {
	v, ok := os.LookupEnv("SHORTCUT_SHOW_DIR")
	switch {
	case !ok || v == "1":
		return true, nil
	case v == "0":
		return false, nil
	}
	return false, fmt.Errorf("SHORTCUT_SHOW_DIR must be 0 or 1, not '%s'", v)
}

// childCommand is the argv of the child: bash -c for an inline command, else the script as it is, for the kernel.
func childCommand(resolved *store.ResolvedShortcut, entry *store.Entry, name string, args []string) ([]string, error) {
	if entry.Inline != nil {
		return append([]string{"bash", "-c", *entry.Inline, name}, args...), nil
	}
	path, err := store.ResolveScriptPath(resolved.Group.ScriptBase(), *entry.Script)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, errors.New("script file not found: " + path)
		}
		return nil, err
	}
	if err := platform.CheckExecutable(path); err != nil {
		return nil, err
	}
	return append([]string{path}, args...), nil
}
