package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/StanLukuvka/Shortcut/internal/approval"
	"github.com/StanLukuvka/Shortcut/internal/model"
	"github.com/StanLukuvka/Shortcut/internal/pyos"
	"github.com/StanLukuvka/Shortcut/internal/textio"
)

// cmdRun runs a shortcut. --global, -y and --yes are the only options it reads, and only before the name; everything
// after that, -h included, is the shortcut's.
func cmdRun(s *textio.Streams, args []string) error {
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		_, err := s.Out.WriteString(runUsage + "\n")
		return err
	}
	skipLocal, assumeYes := false, false
	for len(args) > 0 && (args[0] == "--global" || args[0] == "-y" || args[0] == "--yes") {
		skipLocal = skipLocal || args[0] == "--global"
		assumeYes = assumeYes || args[0] != "--global"
		args = args[1:]
	}
	if len(args) == 0 {
		return &ExitError{Code: 2, Stderr: "error: missing shortcut name\n" + runUsage + "\n"}
	}
	return runShortcut(s, args, skipLocal, assumeYes)
}

// runShortcut handles approval, the confirmation, arguments and the child process, which replaces this one.
// It returns only when something stops the run.
func runShortcut(s *textio.Streams, words []string, skipLocal, assumeYes bool) error {
	callerCwd, err := pyos.Getwd()
	if err != nil {
		return err
	}
	resolved, args, err := model.ResolveShortcut(words, callerCwd, skipLocal, true)
	if err != nil {
		return err
	}
	if err := approval.Require(resolved.Group); err != nil {
		return err
	}
	entry, name := resolved.Entry, resolved.Display()
	if err := model.ValidateName(entry.Name); err != nil { // list warns about a hand-written one; run refuses it
		return err
	}
	typed := args // as given, for the question and the command that answers it
	var argEnv []string
	if entry.Args != nil {
		if args, argEnv, err = bindArgs(name, entry.Args, args); err != nil {
			return err
		}
	}
	if !pyos.IsDir(resolved.RunCwd) {
		return errors.New("run directory does not exist: " + resolved.RunCwd)
	}
	withArgs := ""
	if len(typed) > 0 {
		withArgs = " with " + oneLine(pyos.ShellJoin(typed))
	}
	again := []string{"shortcut", "r", "-y"}
	if skipLocal {
		again = append(again, "--global")
	}
	again = append(append(again, strings.Fields(name)...), typed...)
	if resolved.HidesGlobal { // before any question, and also with SHORTCUT_SHOW_DIR=0
		first := strings.Fields(name)[0]
		s.Err.WriteString("note: '" + first + "' here is this project's; your global '" + first + "' runs with: " +
			"shortcut run --global " + name + "\n")
	}
	realCwd := pyos.Realpath(resolved.RunCwd)
	if entry.Confirm {
		message := oneLine(entry.Description)
		if entry.Description == "(no description)" || (entry.Inline != nil && entry.Description == *entry.Inline) {
			message = ""
		}
		if err := confirmed(s, textio.Confirm{
			Question:   "run '" + name + "'" + withArgs + " in " + realCwd + "?",
			NoTerminal: "'" + name + "' asks for confirmation: run it in a terminal, or pass -y (" + oneLine(pyos.ShellJoin(again)) + ")",
			Message:    message,
		}, assumeYes); err != nil {
			return err
		}
		// again: the files could change while the question waited
		if err := approval.Require(resolved.Group); err != nil {
			return err
		}
	}
	if show, ok := os.LookupEnv("SHORTCUT_SHOW_DIR"); !ok || show != "0" {
		line := textio.Safe("\u2192 " + realCwd)
		if s.Err.IsTerminal() {
			line = "\x1b[2m" + line + "\x1b[0m"
		}
		if err := s.Err.WriteStyled(line + "\n"); err != nil {
			return err
		}
	}

	cmd, err := childCommand(resolved, entry, name, args)
	if err != nil {
		return err
	}
	// A shortcut run from another one gets its own SC_ARG_ variables, not the caller's
	var env []string
	index := map[string]int{}
	set := func(kv string) {
		k, _, _ := strings.Cut(kv, "=")
		if i, ok := index[k]; ok {
			env[i] = kv
			return
		}
		index[k] = len(env)
		env = append(env, kv)
	}
	for _, kv := range os.Environ() {
		if k, _, ok := strings.Cut(kv, "="); ok && !strings.HasPrefix(k, "SC_ARG_") {
			set(kv)
		}
	}
	set("SHORTCUT_NAME=" + name)
	set("SHORTCUT_NAMESPACE=" + resolved.Group.Name)
	set("SHORTCUT_CALLER_CWD=" + callerCwd)
	for _, kv := range argEnv {
		set(kv)
	}
	flush := func() { // a reader that has gone ends the run with 141, as a failed flush does
		if err := s.Out.Flush(); errors.Is(err, syscall.EPIPE) {
			s.Abort141()
		}
	}
	return pyos.ExecChild(cmd, resolved.RunCwd, env, flush)
}

// childCommand is the argv of the child: bash -c for an inline command, else the script, run through its #! line,
// or bash when it has none.
func childCommand(resolved *model.ResolvedShortcut, entry *model.Entry, name string, args []string) ([]string, error) {
	if entry.Inline != nil {
		return append([]string{"bash", "-c", *entry.Inline, name}, args...), nil
	}
	path, err := model.ResolveScriptPath(resolved.Group.ScriptBase(), *entry.Script)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err != nil {
		return nil, errors.New("script file not found: " + path)
	}
	isExec := syscall.Access(path, 1) == nil
	header := make([]byte, 1024)
	n := 0
	if f, err := os.Open(path); err == nil {
		n, _ = f.Read(header) // lazy: one read; a short read on a slow file only shortens what is looked at
		f.Close()
	}
	header = header[:max(n, 0)]
	isElf := strings.HasPrefix(string(header), "\x7fELF")
	hasShebang := strings.HasPrefix(string(header), "#!")
	firstLine, _, _ := strings.Cut(string(header), "\n")
	if hasShebang && strings.HasSuffix(firstLine, "\r") {
		return nil, fmt.Errorf("%s has Windows line endings, so its #! line can't start it. Convert it: sed -i 's/\\r$//' %s",
			path, pyos.ShellQuote(path))
	}
	switch {
	case isExec && (hasShebang || isElf):
		return append([]string{path}, args...), nil
	case hasShebang:
		parts := splitOnce(strings.TrimFunc(replaceInvalidUTF8([]byte(firstLine[2:])), isPySpace))
		if len(parts) == 0 {
			parts = []string{"bash"}
		}
		return append(append(parts, path), args...), nil
	}
	return append([]string{"bash", path}, args...), nil
}
