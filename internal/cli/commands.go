package cli

import (
	"slices"
	"strings"

	"github.com/StanLukuvka/Shortcut/internal/cli/argparse"
)

// takes is what an option does with the words after it.
type takes uint8

const (
	noValue  takes = iota // --json
	oneValue              // --desc TEXT
	optValue              // --names [NAMESPACE]
)

// Arg is an option, or a positional when Flags is empty.
type Arg struct {
	Dest    string   // the key in the parsed Namespace
	Flags   []string // the spellings of an option
	Takes   takes    // option: what follows it
	Meta    string   // option: the value's name in help
	Nargs   byte     // positional: 0 one word, '?', '*' or '+'
	Choices []string // positional: the words it may be
	Help    string   // one line, in `help <command>`
}

// Command is one command, defined once: dispatch, its parser, the usage text and `help <name>` are read
// from it. The completion scripts are static; a test fails if they disagree with it.
type Command struct {
	Name    string
	Aliases []string
	Summary string  // one line, in the usage text
	Help    string  // what `help <name>` says after the usage line
	Usage   string  // the usage line, when the arguments don't give it
	Args    []Arg   // options and positionals; run and help read their own words, so theirs only describe
	Run     handler // gets the words after the name
}

// runUsage is run's usage line. Before the name run reads only --global, -y and --yes; everything after it is the shortcut's.
const runUsage = "run|r [--global] [-y] <name> [args...]\n       shortcut run|r [--global] [-y] <namespace> <name> [args...]"

// commands is the table. It is a function so that handlers may read it without an initialization cycle.
func commands() []Command {
	global := Arg{Dest: "skipLocal", Flags: []string{"--global"}, Help: "Skip this project's shortcuts, to reach a global one it hides"}
	yes := Arg{Dest: "yes", Flags: []string{"-y", "--yes"}, Help: "Do not ask for confirmation"}
	return []Command{
		{Name: "run", Aliases: []string{"r"}, Run: cmdRun, Usage: runUsage,
			Summary: "Run a shortcut, or a namespace's",
			Help:    "Options go before the name; everything after the name is the shortcut's.",
			Args:    []Arg{global, yes, {Dest: "name", Nargs: '+'}}},
		{Name: "new", Run: cmdNew, Summary: "Create a shortcut: an inline command after --, or a script with --script",
			Help: "Give a command after --, or a script file with --script.",
			Args: []Arg{
				{Dest: "desc", Flags: []string{"--desc"}, Takes: oneValue, Meta: "TEXT", Help: "Description"},
				{Dest: "script", Flags: []string{"--script"}, Takes: oneValue, Meta: "PATH", Help: "Run this file where it is"},
				{Dest: "cwd", Flags: []string{"--cwd"}, Takes: oneValue, Meta: "DIR", Help: "Where it runs: caller (default) or a directory"},
				{Dest: "local", Flags: []string{"--local"}, Help: "Store it in .shortcuts.local in this directory, not globally"},
				{Dest: "namespace", Flags: []string{"--namespace"}, Takes: oneValue, Meta: "NAME", Help: "Put it in this namespace, creating imports/NAME.shortcut if needed"},
				{Dest: "confirm", Flags: []string{"--confirm"}, Help: "Ask before running it"},
				{Dest: "name", Help: "Shortcut name"}}},
		{Name: "list", Run: cmdList, Summary: "List visible shortcuts, with where each runs",
			Args: []Arg{{Dest: "names", Flags: []string{"--names"}, Takes: optValue, Meta: "NAMESPACE", Help: "Print bare visible names only, or the names in one namespace"}}},
		{Name: "show", Run: cmdShow, Summary: "Show a shortcut's metadata and script, or the entry as JSON",
			Args: []Arg{global,
				{Dest: "json", Flags: []string{"--json"}, Help: "Print the entry and where it resolves, as one JSON object"},
				{Dest: "field", Flags: []string{"--field"}, Takes: oneValue, Meta: "KEY", Help: "Print one field, bare"},
				{Dest: "name", Nargs: '+', Help: "Shortcut name, or namespace and name"}}},
		{Name: "rm", Run: cmdRm, Summary: "Remove a shortcut, or a whole namespace",
			Help: "With --local, take it out of .shortcuts.local: undo an override, or remove a local shortcut.",
			Args: []Arg{global,
				{Dest: "local", Flags: []string{"--local"}, Help: "Remove it from .shortcuts.local"},
				{Dest: "namespace", Flags: []string{"--namespace"}, Takes: oneValue, Meta: "NS", Help: "Remove this namespace from the global store, with its scripts folder"},
				yes,
				{Dest: "name", Nargs: '*', Help: "Shortcut name, or namespace and name"}}},
		{Name: "update", Run: cmdUpdate, Summary: "Review changes to project shortcuts and namespaces, and approve them",
			Args: []Arg{yes, {Dest: "group", Nargs: '?', Help: "A namespace, or . for this project's own shortcuts (default: all here)"}}},
		{Name: "completion", Run: cmdCompletion, Summary: "Print a shell completion script",
			Args: []Arg{{Dest: "shell", Choices: []string{"bash", "fish", "zsh"}, Help: "bash, fish or zsh"}}},
		{Name: "help", Run: cmdHelp, Summary: "Show this help, or a command's",
			Args: []Arg{{Dest: "command", Nargs: '?', Help: "A command"}}},
	}
}

// find is the command a name or alias picks, or nil.
func find(word string) *Command {
	cs := commands()
	for i := range cs {
		if cs[i].Name == word || slices.Contains(cs[i].Aliases, word) {
			return &cs[i]
		}
	}
	return nil
}

// words is the name and the aliases, joined.
func (c Command) words(sep string) string {
	return strings.Join(append([]string{c.Name}, c.Aliases...), sep)
}

// parse parses a command's words with the parser its Args describe. Its -h prints helpText.
func parse(name string, argv []string) (Namespace, error) {
	return find(name).parser().Parse(argv)
}

func (c Command) parser() *argparse.Parser {
	acts := make([]*argparse.Action, len(c.Args))
	for i, a := range c.Args {
		switch {
		case a.Choices != nil:
			acts[i] = argparse.Choice(a.Dest, a.Choices...)
		case a.Flags == nil:
			acts[i] = argparse.Positional(a.Dest, a.Nargs)
		case a.Takes == oneValue:
			acts[i] = argparse.Value(a.Dest, a.Flags...)
		case a.Takes == optValue:
			acts[i] = argparse.Optional(a.Dest, a.Flags...)
		default:
			acts[i] = argparse.Flag(a.Dest, a.Flags...)
		}
	}
	return argparse.New(c.Name, helpText, acts...)
}
