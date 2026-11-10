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
- A namespace file is `<name>.shortcut`, such as `docker.shortcut`. A bare `.shortcut` is an error.
- `"export": true` on a project entry marks it as made to work outside the repo. `shortcut update` warns if its `cwd` is relative. Only `.shortcuts` and `.shortcuts.local` can export.
- Projects and namespaces run only after `shortcut update`. A namespace name pointed at another file needs approving again. Global shortcuts need no approval.
- If a project's file can't be read, nothing runs inside the project until it's fixed, and `rm` there only takes `--local`. `--global` reaches your own shortcuts.

## Commands

```
shortcut r <name> [args]              run (also: run, r <ns> <name>)
shortcut r --global <name>            skip the project, run the global one
shortcut r -y <name>                  answer a confirm prompt
shortcut r <name> -- -5               after --, a declared argument can start with -

shortcut new <name> -- CMD            inline shortcut
shortcut new <name>                   script from a template; prints its path
shortcut new <name> --copy FILE|-     copy a script in
shortcut new <name> --script PATH     use a script where it is; a path inside the store is kept relative

shortcut list                         everything visible; warns about invalid entries and missing scripts
shortcut list --names [ns]            bare names
shortcut show <name>                  details and script
shortcut show --json <name>           entry and where it resolves, as JSON
shortcut location <name>              script path

shortcut rm <name> [-y]               remove a global or namespace shortcut; says what goes, with -y too
shortcut rm --local <name>            undo your override, or hide a shared entry. Never changes .shortcuts
shortcut rm --namespace <ns> [-y]     remove a global namespace and its scripts/<ns>/ folder; one imported
                                      from elsewhere is only no longer imported

shortcut promote <name> [-y]          make an approved, exported project shortcut one of your global ones
shortcut promote <name> --as NEW      the same, under another global name
shortcut promote <name> --force       one that isn't exported

shortcut update [ns|.] [-y]           review and approve
shortcut init --global <src> [-y]     copy the exported entries in a folder's or git URL's .shortcuts
                                      or, with no .shortcuts, its <ns>.shortcut namespaces into imports/
shortcut init --global <src> --replace
                                      the same, in place of installed namespaces of those names
shortcut completion bash|zsh|fish
shortcut --version
```

Flags for `new`:

```
--desc D              description
--cwd caller|DIR      where it runs (default: caller)
--arg NAME            required argument; NAME=DEFAULT for optional
--arg NAME...         the rest of the arguments; NAME...= for optional, NAME...=W for a one-word default
--arg-desc NAME=T     describe an argument, shown by show and usage errors
--confirm             ask before running, showing the description and the arguments given
--local               write to .shortcuts.local here
--namespace NS        write to a global namespace
--by C                creator
```

`show`, `location` and `rm` take `--global` too. In a project, `--local` works only from the project root.

`show --json` prints one object: `name`, `namespace`, `scope`, `defined_in`, `source`, `hides_global`, `approval`, `cwd`, `run_cwd`, `script_path`, `error`, and `entry` as written. A field that can't be worked out is null.

`promote` copies the entry, not its script. A relative `script` or `cwd` becomes its absolute path in this checkout, and `from` records the project and the original name. It runs the checkout's files as they are now. If the checkout moves or is deleted, it fails with script not found or run directory does not exist.

## What a shortcut runs with

- An inline shortcut runs as `bash -c CMD <name> ARGS...`, so `$0` is its name. A script runs by its shebang, or with bash.
- It runs in its `cwd`, the caller's directory by default.
- Every word after the name reaches it as `$1`, `$2`..., flags included. A first `--` is dropped.
- With `args` declared, a missing optional argument is filled with its default, or with an empty word if a later one has a default.
- Its exit code is shortcut's.

```
SHORTCUT_NAME          what ran: <name>, or <ns> <name>
SHORTCUT_NAMESPACE     its namespace, or empty
SHORTCUT_CALLER_CWD    the directory shortcut was run from
SC_ARG_<NAME>          each declared argument, upper case with - as _. A rest argument's words are
                       shell-quoted and joined, so read "$@" for them. SC_ARG_ variables from the
                       caller's environment are removed first.
```

## Writing a namespace

A namespace is a file named `<ns>.shortcut`: `{"version": 2, "shortcuts": {...}}`, with entries as in `shortcuts.json`. [docs/shortcut.schema.json](docs/shortcut.schema.json) describes them. Relative `script` and `cwd` paths resolve from the file's folder.

- `~/.shortcuts/imports/<ns>.shortcut` is found by its name.
- `shortcut init --global <dir|git URL>` copies a folder's `<ns>.shortcut` files there, each with its `scripts/<ns>/` folder, after showing them. Every script an entry names has to be in that folder. `--replace` puts a newer copy in place of the installed one, showing what changed.
- A file anywhere else is listed in `shortcuts.json`, or in a project's `.shortcuts`: `"imports": {"<ns>": "path/to/<ns>.shortcut"}`. The path is relative to the file that lists it, and can start with `~`.
- It runs only after `shortcut update <ns>`, and again after each change to its file, a script it names, or anything in its `scripts/<ns>/` folder.
- `shortcut new --namespace <ns> <name> ...` adds an entry, and keeps an approved namespace approved.
- `scripts/<ns>/`, next to the file, is the namespace's own folder: shortcut keeps its copied scripts there, and helpers its scripts run belong there too. `rm` deletes an entry's script there with the entry.
- A script runs a sibling with `shortcut r "$SHORTCUT_NAMESPACE" <name>`. `SHORTCUT_SHOW_DIR=0` keeps the `→ dir` line out when capturing it.

[examples/podman](examples/podman/) is one, with scripts.

## Bundled shortcuts

From a clone: `shortcut init --global templates`.

```
shortcut r edit <name>       open its script, or the file defining an inline one
shortcut r nano <name>       same, in nano
shortcut r logged <name>     run through tee, keep the output
shortcut r desktop <name>    desktop launcher (global only); --remove deletes it
shortcut r search <pattern>  find files by name
```

## Settings

```
SHORTCUTS_HOME        global store (default ~/.shortcuts)
SHORTCUT_SHOW_DIR=0   hide the "→ dir" line
PAGER                 pages long update and init --global diffs
```

## Security model

> 1. shortcut runs exactly what its definitions say, without inspecting them.
> 2. A project's or import's shortcuts don't run until you approve them with `shortcut update`, and must be re-approved after any change to their definitions.
> 3. Approval covers definitions and the scripts they name, not the programs those call.
> 4. Global shortcuts have no approval step, so a global link target can change freely. `init --global` shows a collection before copying it.
> 5. Anyone who can write to your shortcut store can already run code as you; that's out of scope.
> 6. A promoted shortcut runs the project's files as they are now. If you want something immutable, don't take it from a repo.

## More

- [AGENT.md](AGENT.md): for coding agents.
- [docs/](docs/): diagrams, the JSON Schema for shortcut files, and every error message.
- [examples/podman](examples/podman/): a namespace for podman containers.
- Code: [src/shortcut/](src/shortcut/), one file per class plus `helpers.py` and `cli.py`. `python3 src` runs it from a clone.
- Tests: `python3 -m unittest discover -s tests`.

## License

MIT. See [LICENSE](LICENSE).
