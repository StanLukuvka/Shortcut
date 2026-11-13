<img src="docs/icon/shortcut.svg" width="96" alt="shortcut icon: a snaking tilde">

# shortcut

Names shell commands and scripts, and runs them from any directory. One binary, no dependencies.

Unlike an alias or a file in `~/bin`, a shortcut can belong to a project and be committed with it, runs only after you've reviewed it, and can declare its arguments, the folder it runs in and a confirm prompt.

## Install

For Linux and macOS, on x86_64 or arm64. On Windows, use WSL.

```bash
curl -fsSL https://github.com/StanLukuvka/Shortcut/releases/latest/download/install.sh | sh
```

The installer downloads the release archive for your system, checks it against `checksums.txt`, and writes:

- the `shortcut` binary to `~/.local/bin/shortcut` (or `$BIN_DIR/shortcut`);
- bash completion to `~/.local/share/bash-completion/completions/shortcut`;
- fish completion to `~/.config/fish/completions/shortcut.fish`, if fish is installed;
- zsh completion, if zsh is installed and used (your login shell is zsh, or `~/.zshrc` exists): the file `~/.local/share/shortcut/completion.zsh`, and one marked block in `~/.zshrc`, added once. `SHORTCUT_NO_RC=1` skips the zsh part.

`$XDG_DATA_HOME` (default `~/.local/share`) and `$XDG_CONFIG_HOME` (default `~/.config`) move the completion files.

At a terminal, the installer then offers the bundled shortcuts: you review and answer `shortcut init --global --bundled`, and the installer never answers for you. `SHORTCUT_NO_BUNDLED=1` skips the offer and prints the command instead. Without a terminal, it prints the command.

To install a given version, set `SHORTCUT_VERSION`, with or without the `v`:

```bash
curl -fsSL https://github.com/StanLukuvka/Shortcut/releases/latest/download/install.sh | SHORTCUT_VERSION=0.1.0 sh
```

To upgrade, run the install command again. It replaces the binary and rewrites the completion files.

To uninstall, delete `~/.local/bin/shortcut`, the completion files (`~/.local/share/bash-completion/completions/shortcut`, `~/.config/fish/completions/shortcut.fish` and the `~/.local/share/shortcut/` folder), and the lines from `# >>> shortcut completion >>>` to `# <<< shortcut completion <<<` in `~/.zshrc`. Your shortcuts in `~/.shortcuts` stay.

Requirements:

- bash: inline shortcuts run with `bash -c`.
- `curl` or `wget`, and `sha256sum` or `shasum`, for the installer.
- git, for `init --global <git URL>`.
- python3, for the bundled `edit` and `desktop`. `edit` also needs `$EDITOR` (`$VISUAL` first if set, nano if neither).
- The bundled `desktop` is for Linux only.

## Quick start

```bash
shortcut new hi -- 'echo hello "$1"'
shortcut r hi you
shortcut list
```

`shortcut r hi you` prints `hello you`, from any directory.

A project shares its shortcuts in a `.shortcuts` file at its root, committed with the code:

```json
{"version": 1, "shortcuts": {"test": {"description": "Run the tests", "inline": "pytest \"$@\""}}}
```

Each person reviews it once with `shortcut update`, and again after it changes. Then `shortcut r test` runs it from anywhere in the project.

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
├── locks/                  lock files for projects
└── .lock                   the global store's lock file
```

[Projects and approval](#projects-and-approval) has the rules.

## Commands

```
shortcut r <name> [args]              run (also: run, r <ns> <name>)
shortcut r --global <name>            skip the project, run the global one
shortcut r -y <name>                  answer a confirm prompt
shortcut r <name> -- -5               after --, a declared argument can start with -

shortcut new <name> -- CMD            inline shortcut
shortcut new <name>                   a new bash script; prints its path
shortcut new <name> --copy FILE|-     copy a script in
shortcut new <name> --script PATH     use a script where it is; a path inside the store (or a project or namespace folder) is kept relative, except under the store's `scripts/`, which stays absolute

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
shortcut init --global --bundled [-y] the same for the shortcuts that come with shortcut
shortcut completion bash|zsh|fish
shortcut help [command]
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

`new <name> -- CMD` stores the command as text and runs it with `bash -c`. One quoted word is kept as written: `-- 'ls "$1" | wc -l'` keeps its pipe. Several words are each quoted, so each stays one word: `-- echo '$HOME'` prints `$HOME`. In both, `$1`, `$@` and the other arguments stay live.

`show`, `location` and `rm` take `--global` too. In a project, `--local` works only from the project root.

`show --json` prints one object: `name`, `namespace`, `scope`, `defined_in`, `source`, `hides_global`, `approval`, `cwd`, `run_cwd`, `script_path`, `error`, and `entry` as written. A field that can't be worked out is null.

`promote` copies the entry, not its script. A relative `script` or `cwd` becomes its absolute path in this checkout, and `from` records the project and the original name. It runs the checkout's files as they are now. If the checkout moves or is deleted, it fails with script not found or run directory does not exist.

## Projects and approval

- The project is the nearest folder, walking up, with `.shortcuts` or `.shortcuts.local`. Only that one is read, also inside another project.
- `.shortcuts.local` overrides `.shortcuts` per entry. `"name": null` hides a shared one.
- Inside a project, its shortcuts and namespaces hide your global ones of the same name. `update`, `list`, `show` and `run` say so, and `shortcut r --global <name>` runs yours.
- Relative `script` and `cwd` paths resolve from the folder holding the file.
- A namespace file is `<name>.shortcut`, such as `docker.shortcut`. A bare `.shortcut` is an error.
- `"export": true` on a project entry marks it as made to work outside the repo. `shortcut update` warns if its `cwd` is relative. Only `.shortcuts` and `.shortcuts.local` can export.
- Projects and namespaces run only after `shortcut update`. A namespace name pointed at another file needs approving again. Global shortcuts need no approval.
- What the CLI writes keeps an approval that held before. So `new --local` in a folder with no `.shortcuts`, or with an approved one, runs at once. A namespace made with `new --namespace` needs `shortcut update <ns>` once, and after that `new` and `rm` keep it approved. A change made by hand needs `shortcut update`.
- If a project's file can't be read, nothing runs inside the project until it's fixed, and `rm` there only takes `--local`. `--global` reaches your own shortcuts.

## What a shortcut runs with

- An inline shortcut runs as `bash -c CMD <name> ARGS...`, so `$0` is its name. A script runs by its shebang, or with bash.
- It runs in its `cwd`, the caller's directory by default.
- Every word after the name reaches it as `$1`, `$2`..., flags included. A first `--` is dropped only when the shortcut declares `args`.
- With `args` declared, a missing optional argument is filled with its default, or with an empty word if a later one has a default. A required one can't be an empty word.
- A declared argument is also in `SC_ARG_<NAME>`, which a flag before it doesn't move. `$1` would be the flag.
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

A namespace is a file named `<ns>.shortcut`: `{"version": 1, "shortcuts": {...}}`, with entries as in `shortcuts.json`. [docs/shortcut.schema.json](docs/shortcut.schema.json) describes them. Relative `script` and `cwd` paths resolve from the file's folder.

- `~/.shortcuts/imports/<ns>.shortcut` is found by its name.
- `shortcut init --global <dir|git URL>` copies a folder's `<ns>.shortcut` files there, each with its `scripts/<ns>/` folder, after showing them. Every script an entry names has to be in that folder. `--replace` puts a newer copy in place of the installed one, showing what changed.
- A file anywhere else is listed in `shortcuts.json`, or in a project's `.shortcuts`: `"imports": {"<ns>": "path/to/<ns>.shortcut"}`. The path is relative to the file that lists it, and can start with `~`.
- It runs only after `shortcut update <ns>`, and again after each change to its file, a script it names, or anything in its `scripts/<ns>/` folder.
- `shortcut new --namespace <ns> <name> ...` adds an entry, and keeps an approved namespace approved.
- `scripts/<ns>/`, next to the file, is the namespace's own folder: shortcut keeps its copied scripts there, and helpers its scripts run belong there too. `rm` deletes an entry's script there with the entry.
- A script runs a sibling with `shortcut r "$SHORTCUT_NAMESPACE" <name>`. `SHORTCUT_SHOW_DIR=0` keeps the `→ dir` line out when capturing it.

[examples/podman](examples/podman/) is one, with scripts.

## Bundled shortcuts

`shortcut init --global --bundled` installs them from the copy inside the program, with the same review as any `init --global`. From a clone, `shortcut init --global templates` does the same. `init --global <repo URL>` doesn't reach them: they're in `templates/`, not at the repo's root.

```
shortcut r edit <name>       open its script, or the file defining an inline one
shortcut r nano <name>       same, in nano
shortcut r logged <name>     run through tee, keep the output
shortcut r desktop <name>    desktop launcher for a global shortcut or namespace member; --remove deletes it
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

- [AGENT.md](AGENT.md): for coding agents using shortcut.
- [CONTRIBUTING.md](CONTRIBUTING.md): working on shortcut itself.
- [SECURITY.md](SECURITY.md): reporting a vulnerability.
- [CHANGELOG.md](CHANGELOG.md): what changed in each version.
- [docs/](docs/): diagrams, the JSON Schema for shortcut files, and every error message.
- [examples/podman](examples/podman/): a namespace for podman containers.
- Code, in Go: [cmd/shortcut](cmd/shortcut/) is the entry point. `internal/cli` has the commands, one `cmd_*.go` file each, with `usage.go` and the completion scripts in `completion/`. `internal/model` has the store files, entries and lookup; `internal/approval` the approval records and review; `internal/textio` terminal output and prompts. `internal/pyos`, `internal/pyjson` and `internal/difflib` reproduce the file, JSON and diff behaviour that shortcut's output depends on; `internal/testkit` is the test sandbox. [installer/](installer/) is the install script. [bundled.go](bundled.go) embeds [templates/](templates/) in the binary.

## License

MIT. See [LICENSE](LICENSE).
