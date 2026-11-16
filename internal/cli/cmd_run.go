package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"syscall"

	"github.com/StanLukuvka/Shortcut/internal/platform"
	"github.com/StanLukuvka/Shortcut/internal/store"
	"github.com/StanLukuvka/Shortcut/internal/store/approval"
	"github.com/StanLukuvka/Shortcut/internal/term"
)

// cmdRun runs a shortcut. --global, -y and --yes are the only options it reads, and only before the name; everything
// after that, -h included, is the shortcut's.
func cmdRun(c *store.Context, s *term.Streams, cmd term.Command, args []string) error {
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

	plan, err := childCommand(resolved, entry, name, args)
	if err != nil {
		return err
	}
	// A shortcut run from another one gets its own SHORTCUT_ variables, not the caller's
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		k, _, hasEq := strings.Cut(kv, "=")
		return !hasEq || k == "SHORTCUT_NAME" || k == "SHORTCUT_NAMESPACE" || k == "SHORTCUT_CALLER_CWD"
	})
	env = append(env, "SHORTCUT_NAME="+name, "SHORTCUT_NAMESPACE="+resolved.Group.Name, "SHORTCUT_CALLER_CWD="+c.Cwd)
	err = platform.ExecChild(plan.argv, resolved.RunCwd, env)
	if plan.direct && errors.Is(err, syscall.ENOEXEC) && !bytes.Contains(plan.header, []byte{0}) {
		// An executable text file with no #!: exec says "exec format error", a shell would run it with itself.
		return platform.ExecChild(append([]string{"bash", plan.path}, args...), resolved.RunCwd, env)
	}
	return err
}

// childPlan is what childCommand decides: the argv, and for an executable with no #! (direct) also the file and its
// first bytes, which cmdRun needs if the kernel refuses to exec it.
type childPlan struct {
	argv   []string
	direct bool
	path   string
	header []byte
}

// childCommand is the argv of the child: bash -c for an inline command, else the script, run through its #! line;
// an executable with no #! is exec'd as it is, and a file that is not executable and has no #! runs through bash.
func childCommand(resolved *store.ResolvedShortcut, entry *store.Entry, name string, args []string) (childPlan, error) {
	if entry.Inline != nil {
		return childPlan{argv: append([]string{"bash", "-c", *entry.Inline, name}, args...)}, nil
	}
	path, err := store.ResolveScriptPath(resolved.Group.ScriptBase(), *entry.Script)
	if err != nil {
		return childPlan{}, err
	}
	if _, err := os.Stat(path); err != nil {
		return childPlan{}, errors.New("script file not found: " + path)
	}
	isExec := syscall.Access(path, 1) == nil
	header := make([]byte, 1024)
	n := 0
	if f, err := os.Open(path); err == nil {
		n, _ = f.Read(header) // lazy: one read; a short read on a slow file only shortens what is looked at
		f.Close()
	}
	header = header[:max(n, 0)]
	hasShebang := strings.HasPrefix(string(header), "#!")
	firstLine, _, _ := strings.Cut(string(header), "\n")
	if hasShebang && strings.HasSuffix(firstLine, "\r") {
		return childPlan{}, fmt.Errorf("%s has Windows line endings, so its #! line can't start it. Convert it: sed -i 's/\\r$//' %s",
			path, platform.ShellQuote(path))
	}
	switch {
	case isExec && hasShebang:
		return childPlan{argv: append([]string{path}, args...)}, nil // the kernel runs the #! line
	case hasShebang:
		// the kernel splits at the first blank: the interpreter, and the rest as one argument
		line := strings.Trim(strings.ToValidUTF8(firstLine[2:], "\uFFFD"), " \t")
		interp, rest := line, ""
		if i := strings.IndexAny(line, " \t"); i >= 0 {
			interp, rest = line[:i], strings.TrimLeft(line[i:], " \t")
		}
		parts := []string{"bash"}
		if interp != "" {
			parts = []string{interp}
			if rest != "" {
				parts = append(parts, rest)
			}
		}
		return childPlan{argv: append(append(parts, path), args...)}, nil
	case isExec:
		return childPlan{argv: append([]string{path}, args...), direct: true, path: path, header: header}, nil // ELF, Mach-O, anything
	}
	return childPlan{argv: append([]string{"bash", path}, args...)}, nil // not executable: bash runs it
}
