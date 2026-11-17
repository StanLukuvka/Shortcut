package commands

import (
	"slices"

	"github.com/StanLukuvka/Shortcut/internal/commands/completion"
	"github.com/StanLukuvka/Shortcut/internal/commands/list"
	"github.com/StanLukuvka/Shortcut/internal/commands/new"
	"github.com/StanLukuvka/Shortcut/internal/commands/remove"
	"github.com/StanLukuvka/Shortcut/internal/commands/run"
	"github.com/StanLukuvka/Shortcut/internal/commands/show"

	"github.com/StanLukuvka/Shortcut/internal/store"
	"github.com/StanLukuvka/Shortcut/internal/terminal"
)

// handler is a command: it gets what follows the command word and its own description, and ends with nil (exit 0),
// a *argparse.ExitError or any other error, which dispatch prints as "error: <text>" with exit 1.
type handler func(c *store.Context, s *terminal.Streams, cmd terminal.Command, args []string) error

// entry is one row of the table: a command's description and what runs it.
type entry struct {
	Run handler // gets the words after the name
	terminal.Command
}

// runUsage is run's usage line. Before the name run reads only -y and --yes, and a . that selects the project; everything after the name is the shortcut's.
const runUsage = "run|r [-y] [.] <name> [args...]\n       shortcut run|r [-y] [.] <namespace> <name> [args...]"

// commands is the table. It is a function so that handlers may read it without an initialization cycle.
func commands() []entry {
	yes := terminal.Arg{Dest: "yes", Flags: []string{"-y", "--yes"}, Help: "Do not ask for confirmation"}
	return []entry{
		{Run: run.Run, Command: terminal.Command{Name: "run", Aliases: []string{"r"}, Usage: runUsage,
			Summary: "Run a shortcut, or a namespace's",
			Help:    "Options go before the name; everything after the name is the shortcut's.\nWithout a dot it runs your global shortcut; with a dot (shortcut r . <name>), the project's.",
			Args:    []terminal.Arg{yes, {Dest: "name", Nargs: '+'}}}},
		{Run: new.Run, Command: terminal.Command{Name: "new", Summary: "Create a shortcut: an inline command after --, or a script with --script",
			Help: "Give a command after --, or a script file with --script.",
			Args: []terminal.Arg{
				{Dest: "desc", Flags: []string{"--desc"}, Takes: terminal.OneValue, Meta: "TEXT", Help: "Description"},
				{Dest: "script", Flags: []string{"--script"}, Takes: terminal.OneValue, Meta: "PATH", Help: "Run this file where it is"},
				{Dest: "cwd", Flags: []string{"--cwd"}, Takes: terminal.OneValue, Meta: "DIR", Help: "Where it runs: caller (default) or a directory"},
				{Dest: "local", Flags: []string{"--local"}, Help: "Store it in .shortcuts.local in this directory, not globally"},
				{Dest: "namespace", Flags: []string{"--namespace"}, Takes: terminal.OneValue, Meta: "NAME", Help: "Put it in this namespace, creating imports/NAME.shortcut if needed"},
				{Dest: "confirm", Flags: []string{"--confirm"}, Help: "Ask before running it"},
				{Dest: "name", Help: "Shortcut name"}}}},
		{Run: list.Run, Command: terminal.Command{Name: "list", Summary: "List global and project shortcuts, with where each runs",
			Args: []terminal.Arg{{Dest: "names", Flags: []string{"--names"}, Takes: terminal.OptValue, Meta: "NAMESPACE", Help: "Print bare global names only, or the names in one global namespace; with . the project's names"}}}},
		{Run: show.Run, Command: terminal.Command{Name: "show", Summary: "Show a shortcut's metadata and script, or the entry as JSON",
			Usage: "show [--json] [--field KEY] [.] name...",
			Help:  "Without a dot it shows your global shortcut; with a dot (shortcut show . <name>), the project's.",
			Args: []terminal.Arg{
				{Dest: "json", Flags: []string{"--json"}, Help: "Print the entry and where it resolves, as one JSON object"},
				{Dest: "field", Flags: []string{"--field"}, Takes: terminal.OneValue, Meta: "KEY", Help: "Print one field, bare"},
				{Dest: "name", Nargs: '+', Help: "Shortcut name, or namespace and name"}}}},
		{Run: remove.Run, Command: terminal.Command{Name: "rm", Summary: "Remove a shortcut, or a whole namespace",
			Help: "Takes it out of your global store. With --local, out of .shortcuts.local: undo an override, or remove a local shortcut.",
			Args: []terminal.Arg{
				{Dest: "local", Flags: []string{"--local"}, Help: "Remove it from .shortcuts.local"},
				{Dest: "namespace", Flags: []string{"--namespace"}, Takes: terminal.OneValue, Meta: "NS", Help: "Remove this namespace from the global store, with its scripts folder"},
				yes,
				{Dest: "name", Nargs: '*', Help: "Shortcut name, or namespace and name"}}}},
		{Run: completion.Run, Command: terminal.Command{Name: "completion", Summary: "Print a shell completion script",
			Args: []terminal.Arg{{Dest: "shell", Choices: []string{"bash", "fish", "zsh"}, Help: "bash, fish or zsh"}}}},
		{Run: cmdHelp, Command: terminal.Command{Name: "help", Summary: "Show this help, or a command's",
			Args: []terminal.Arg{{Dest: "command", Nargs: '?', Help: "A command"}}}},
	}
}

// find is the command a name or alias picks, or nil.
func find(word string) *entry {
	cs := commands()
	for i := range cs {
		if cs[i].Name == word || slices.Contains(cs[i].Aliases, word) {
			return &cs[i]
		}
	}
	return nil
}
