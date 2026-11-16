package run

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/platform"
	"github.com/StanLukuvka/Shortcut/internal/store"
	"github.com/StanLukuvka/Shortcut/internal/store/approval"
	"github.com/StanLukuvka/Shortcut/internal/term"
)

// Run runs a shortcut. --global, -y and --yes are the only options it reads, and only before the name; everything
// after that, -h included, is the shortcut's.
func Run(c *store.Context, s *term.Streams, cmd term.Command, args []string) error {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		_, err := s.Out.WriteString(cmd.HelpText())
		return err
	}
	skipLocal, assumeYes := false, false
	for len(args) > 0 && (args[0] == "--global" || args[0] == "-y" || args[0] == "--yes") {
		skipLocal = skipLocal || args[0] == "--global"
		assumeYes = assumeYes || args[0] != "--global"
		args = args[1:]
	}
	if len(args) == 0 {
		return &term.ExitError{Code: 2, Stderr: "error: missing shortcut name\n" + cmd.UsageLine() + "\n"}
	}
	return runShortcut(c, s, args, store.Lookup{GlobalOnly: skipLocal}, assumeYes)
}

func oneLine(text string) string { return strings.ReplaceAll(text, "\n", "\\n") }

// runShortcut handles approval, the confirmation and the child process, which replaces this one.
// It returns only when something stops the run.
func runShortcut(c *store.Context, s *term.Streams, words []string, lk store.Lookup, assumeYes bool) error {
	if c.CwdErr != nil {
		return c.CwdErr
	}
	resolved, args, err := store.ResolveShortcut(c, words, store.Lookup{GlobalOnly: lk.GlobalOnly, Strict: true})
	if err != nil {
		return err
	}
	if err := approval.Require(c, resolved.Group); err != nil {
		return err
	}
	entry, name := resolved.Entry, resolved.Display()
	if err := store.ValidateName(entry.Name); err != nil { // list warns about a hand-written one; run refuses it
		return err
	}
	if !platform.IsDir(resolved.RunCwd) {
		return errors.New("run directory does not exist: " + resolved.RunCwd)
	}
	withArgs := ""
	if len(args) > 0 {
		withArgs = " with " + oneLine(platform.ShellJoin(args))
	}
	again := []string{"shortcut", "r", "-y"}
	if lk.GlobalOnly {
		again = append(again, "--global")
	}
	again = append(append(again, strings.Fields(name)...), args...)
	if resolved.HidesGlobal { // before any question, and also with SHORTCUT_SHOW_DIR=0
		first := strings.Fields(name)[0]
		s.Err.WriteString("note: '" + first + "' here is this project's; your global '" + first + "' runs with: " +
			"shortcut run --global " + name + "\n")
	}
	realCwd := platform.Realpath(resolved.RunCwd)
	if entry.Confirm {
		message := oneLine(entry.Description)
		if entry.Description == "(no description)" || (entry.Inline != nil && entry.Description == *entry.Inline) {
			message = ""
		}
		if err := term.Confirmed(s, term.Confirm{
			Question:   "run '" + name + "'" + withArgs + " in " + realCwd + "?",
			NoTerminal: "'" + name + "' asks for confirmation: run it in a terminal, or pass -y (" + oneLine(platform.ShellJoin(again)) + ")",
			Message:    message,
		}, assumeYes); err != nil {
			return err
		}
		// again, from disk: the files could change while the question waited
		c.Forget(resolved.Group.Key())
		if err := approval.Require(c, resolved.Group); err != nil {
			return err
		}
	}
	if show, ok := os.LookupEnv("SHORTCUT_SHOW_DIR"); !ok || show != "0" {
		line := term.Safe("\u2192 " + realCwd)
		if s.Err.IsTerminal() {
			line = "\x1b[2m" + line + "\x1b[0m"
		}
		if err := s.Err.WriteStyled(line + "\n"); err != nil {
			return err
		}
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
	return platform.ExecChild(argv, resolved.RunCwd, env)
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
		return nil, errors.New("script file not found: " + path)
	}
	if err := platform.CheckExecutable(path); err != nil {
		return nil, err
	}
	if f, err := os.Open(path); err == nil {
		first, _ := bufio.NewReader(f).ReadSlice('\n') // lazy: a #! line longer than the 4096-byte buffer is cut, and the kernel refuses it anyway
		f.Close()
		if bytes.HasPrefix(first, []byte("#!")) && bytes.HasSuffix(first, []byte("\r\n")) {
			return nil, fmt.Errorf("%s has Windows line endings, so its #! line can't start it. Convert it: sed -i 's/\\r$//' %s",
				path, platform.ShellQuote(path))
		}
	}
	return append([]string{path}, args...), nil
}
