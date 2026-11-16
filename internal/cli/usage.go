package cli

// usageText is the usage text, verbatim. A raw string cannot hold a backtick, so those are joined in.
const usageText = `shortcut - run and manage quick scripts and commands

Usage:
  shortcut run [--global] [-y] <name> [args...]
                                            Run a shortcut, or ` + "`" + `<namespace> <name>` + "`" + `; r is short for run.
                                            --global skips project shortcuts, -y answers a confirm prompt
  shortcut new <name> [FLAGS] [-- CMD...]   Create a shortcut: an inline command after --, a new
                                            script with a bash header at the path it prints, or a
                                            file with --script or --copy
  shortcut list [--names [NAMESPACE]]       List visible shortcuts, with where each runs
  shortcut show [--global] [--json] <name>  Show shortcut metadata and script contents, or the entry as JSON
  shortcut rm [--global|--local] <name> [-y]
                                            Remove a shortcut. With --local, take it out of .shortcuts.local,
                                            or hide a shared one from you
  shortcut rm --namespace <ns> [-y]         Remove a global namespace and its scripts/<ns>/ folder
  shortcut update [group] [-y]              Review changes to project shortcuts and namespaces, and approve them
  shortcut completion bash|zsh|fish         Print a shell completion script
  shortcut help [command], -h, --help       Show this help message, or a command's
  shortcut --version                        Show version

FLAGS for new:
  --script PATH             Run this file where it is
  --copy FILE|-             Copy this file, or stdin, into the store
  --desc D                  Description
  --cwd caller|DIR          Where it runs (default: caller, your current directory)
  --arg NAME[=DEFAULT]      Declare a required argument, or an optional one with a default.
                            NAME... takes the rest of the arguments; NAME...= makes it optional
  --arg-desc NAME=TEXT      Describe a declared argument, for show and usage errors
  --confirm                 Ask before running it
  --local                   Store it in .shortcuts.local in this directory (default: the global store)
  --namespace NAME          Put it in namespace NAME in the global store, creating it if needed

Project shortcuts and namespaces don't run until approved with ` + "`" + `shortcut update` + "`" + `,
and must be approved again after any change to their definitions. Approval covers
.shortcuts, .shortcuts.local, imports, the scripts they name and each namespace's
scripts/<ns>/ folder, not the programs those call. A change shortcut makes to .shortcuts.local or a namespace keeps its approval.
Global shortcuts have no approval step, so a global link target can change freely.
Set SHORTCUT_SHOW_DIR=0 to hide the "→ <run directory>" line.
`
