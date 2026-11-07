<img src="docs/icon/shortcut.svg" width="96" alt="shortcut icon: a snaking tilde">

# shortcut

Names shell commands and scripts, and runs them from any directory. One Python file, no dependencies.

## Install

Python 3.10+ and bash, on Linux or another POSIX system.

```bash
uv tool install git+https://github.com/StanLukuvka/Shortcut
```

Or from a clone, which also sets up completion and gives you the bundled shortcuts:

```bash
git clone https://github.com/StanLukuvka/Shortcut.git && cd Shortcut && ./install.sh
```

## Where shortcuts live

| Place | What | Written by |
|---|---|---|
| `~/.shortcuts/` (or `$SHORTCUTS_HOME`) | Global store, visible everywhere | the CLI |
| `<project>/.shortcuts` | Shared project file, committed | you, by hand |
| `<project>/.shortcuts.local` | Your own entries, kept out of git | the CLI, with `--local` |

```
~/.shortcuts/
├── shortcuts.json          entries
├── imports/<ns>.shortcut   namespaces
├── scripts/                stored script copies
├── approvals/              approval records
└── locks/                  lock files
```

- The project is the nearest folder, walking up, with `.shortcuts` or `.shortcuts.local`.
- `.shortcuts.local` overrides `.shortcuts` per entry. `"name": null` hides a shared one.
- Relative `script` and `cwd` paths resolve from the folder holding the file.
- Projects and namespaces run only after `shortcut update`. Global shortcuts need no approval.
- If a project's file can't be read, nothing runs inside the project. `--global` reaches your own shortcuts.

## Commands

```
shortcut r <name> [args]              run (also: run, r <ns> <name>)
shortcut r --global <name>            skip the project, run the global one
shortcut r -y <name>                  answer a confirm prompt

shortcut new <name> -- CMD            inline shortcut
shortcut new <name>                   script, opened in your editor
shortcut new <name> --copy FILE|-     copy a script in
shortcut new <name> --script PATH     use a script where it is

shortcut list                         everything visible; warns about invalid entries and missing scripts
shortcut list --names [ns]            bare names
shortcut show <name>                  details and script
shortcut show --json <name>           raw entry, also for a broken one
shortcut location <name>              script path

shortcut rm <name> [-y]               remove
shortcut rm --local <name>            undo your override, or hide a shared entry

shortcut update [ns|.] [-y]           review and approve
shortcut init --global <src> [-y]     import a folder or git URL
shortcut completion bash|zsh|fish
shortcut --version
```

Flags for `new`:

```
--desc D              description
--cwd caller|DIR      where it runs (default: caller)
--arg NAME            required argument; NAME=DEFAULT for optional
--confirm             ask before running
--local               write to .shortcuts.local here
--namespace NS        write to a global namespace
--by C                creator
```

`show`, `location` and `rm` take `--global` too. In a project, `--local` works only from the project root.

## Bundled shortcuts

From a clone: `shortcut init --global templates`.

```
shortcut r edit <name>       open its script, or the file defining an inline one
shortcut r nano <name>       same, in nano
shortcut r logged <name>     run through tee, keep the output
shortcut r desktop <name>    desktop launcher (global only)
shortcut r search <pattern>  find files by name
```

## Settings

```
SHORTCUTS_HOME        global store (default ~/.shortcuts)
SHORTCUT_SHOW_DIR=0   hide the "→ dir" line
VISUAL, EDITOR        editor for new and r edit (default nano)
PAGER                 pages long update diffs
```

## Security model

> 1. shortcut runs exactly what its definitions say, without inspecting them.
> 2. A project's or import's shortcuts don't run until you approve them with `shortcut update`, and must be re-approved after any change to their definitions.
> 3. Approval covers definitions and the scripts they name, not the programs those call.
> 4. Global shortcuts have no approval step, so a global link target can change freely. `init --global` shows a collection before copying it.
> 5. Anyone who can write to your shortcut store can already run code as you; that's out of scope.

## More

- [AGENT.md](AGENT.md): for coding agents.
- [docs/](docs/): diagrams and the `.shortcut` JSON Schema.
- Tests: `python3 -m unittest discover -s tests`.

## License

MIT. See [LICENSE](LICENSE).
