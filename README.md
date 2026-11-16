<img src="docs/icon/shortcut.svg" width="96" alt="shortcut icon: a snaking tilde">

# shortcut

Names shell commands and scripts, and runs them from any directory. One binary, no dependencies.

Unlike an alias or a file in `~/bin`, a shortcut can belong to a project and be committed with it, runs only after you've reviewed it, and can set the folder it runs in and a confirm prompt.

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

To install a given version, set `SHORTCUT_VERSION`, with or without the `v`:

```bash
curl -fsSL https://github.com/StanLukuvka/Shortcut/releases/latest/download/install.sh | SHORTCUT_VERSION=0.1.0 sh
```

To upgrade, run the install command again. It replaces the binary and rewrites the completion files.

To uninstall, delete `~/.local/bin/shortcut`, the completion files (`~/.local/share/bash-completion/completions/shortcut`, `~/.config/fish/completions/shortcut.fish` and the `~/.local/share/shortcut/` folder), and the lines from `# >>> shortcut completion >>>` to `# <<< shortcut completion <<<` in `~/.zshrc`. Your shortcuts in `~/.shortcuts` stay.

Requirements:

- bash: inline shortcuts run with `bash -c`.
- `curl` or `wget`, and `sha256sum` or `shasum`, for the installer.
- For [examples/tools](examples/tools/): `$EDITOR` for `edit` (`$VISUAL` first if set, nano if neither), and `desktop` is for Linux only.

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

shortcut new <name> -- CMD            inline shortcut
shortcut new <name> --script PATH     use a script where it is; a path inside the store (or a project or namespace folder) is kept relative

shortcut list                         everything visible; warns about invalid entries and missing scripts
shortcut list --names [ns]            bare names
shortcut show <name>                  details and script
shortcut show --json <name>           entry and where it resolves, as JSON
shortcut show --field KEY <name>      one field, bare, for scripts

shortcut rm <name> [-y]               remove a global or namespace shortcut; says what goes, with -y too
shortcut rm --local <name>            undo your override, or remove a local entry. Never changes .shortcuts
shortcut rm --namespace <ns> [-y]     remove a global namespace and its scripts/<ns>/ folder; one imported
                                      from elsewhere is only no longer imported

shortcut update [ns|.] [-y]           list what is new, changed or removed, and approve
shortcut completion bash|zsh|fish
shortcut help [command]
shortcut --version
```

Flags for `new`:

```
--desc D              description
--cwd caller|DIR      where it runs (default: caller)
--confirm             ask before running, showing the description and the words given
--local               write to .shortcuts.local here
--namespace NS        write to a global namespace
```

`new <name> -- CMD` stores the command as text and runs it with `bash -c`. One quoted word is kept as written: `-- 'ls "$1" | wc -l'` keeps its pipe. Several words are each quoted, so each stays one word: `-- echo '$HOME'` prints `$HOME`. In both, `$1`, `$@` and the other arguments stay live.

`show` and `rm` take `--global` too. In a project, `--local` works only from the project root.

`show --field KEY` prints one field of that object bare, or of the entry (`description`, `inline`, `script`); `true` and `false` print as words, and a null or absent field prints nothing. It only reads: it never approves. `show --json` prints one object: `name`, `namespace`, `scope`, `defined_in`, `source`, `hides_global`, `approval`, `cwd`, `run_cwd`, `script_path`, `error`, and `entry` as written. A field that can't be worked out is null.

## Projects and approval

- The project is the nearest folder, walking up, with `.shortcuts` or `.shortcuts.local`. Only that one is read, also inside another project.
- `.shortcuts.local` overrides `.shortcuts` per entry.
- Inside a project, its shortcuts and namespaces hide your global ones of the same name. `update`, `list`, `show` and `run` say so, and `shortcut r --global <name>` runs yours.
- Relative `script` and `cwd` paths resolve from the folder holding the file.
- A namespace file is `<name>.shortcut`, such as `docker.shortcut`. A bare `.shortcut` is an error.
- Projects and namespaces run only after `shortcut update`. A namespace name pointed at another file needs approving again. Global shortcuts need no approval.
- Any change to a project's or namespace's files, by `new`, `rm` or by hand, needs `shortcut update` before the group runs again. `new` and `rm` print the command.
- If a project's file can't be read, nothing runs inside the project until it's fixed, and `rm` there only takes `--local`. `--global` reaches your own shortcuts.

## What a shortcut runs with

- An inline shortcut runs as `bash -c CMD <name> ARGS...`, so `$0` is its name. A script runs by its shebang. An executable file with no shebang runs as it is (a binary, or a text file the kernel refuses, which bash then runs); a file that is not executable runs with bash.
- It runs in its `cwd`, the caller's directory by default.
- Every word after the name reaches it as `$1`, `$2`..., flags included. A first `--` is passed on as it is.
- Its exit code is shortcut's.

```
SHORTCUT_NAME          what ran: <name>, or <ns> <name>
SHORTCUT_NAMESPACE     its namespace, or empty
SHORTCUT_CALLER_CWD    the directory shortcut was run from
```

## Writing a namespace

A namespace is a file named `<ns>.shortcut`: `{"version": 1, "shortcuts": {...}}`, with entries as in `shortcuts.json`. [docs/shortcut.schema.json](docs/shortcut.schema.json) describes them. Relative `script` and `cwd` paths resolve from the file's folder.

- `~/.shortcuts/imports/<ns>.shortcut` is found by its name.
- To install one, copy its `<ns>.shortcut` and its `scripts/<ns>/` folder into `~/.shortcuts/imports/`, then run `shortcut update <ns>`. Every script an entry names has to be in that folder.
- A file anywhere else is listed in `shortcuts.json`, or in a project's `.shortcuts`: `"imports": {"<ns>": "path/to/<ns>.shortcut"}`. The path is relative to the file that lists it, and can start with `~`.
- It runs only after `shortcut update <ns>`, and again after each change to its file, a script it names, or anything in its `scripts/<ns>/` folder.
- `shortcut new --namespace <ns> <name> ...` adds an entry. The namespace needs `shortcut update <ns>` again.
- `scripts/<ns>/`, next to the file, is the namespace's own folder: scripts and helpers belong there. `rm <name>` removes only the entry, never a script file; `rm --namespace` deletes the folder.
- A script runs a sibling with `shortcut r "$SHORTCUT_NAMESPACE" <name>`. `SHORTCUT_SHOW_DIR=0` keeps the `→ dir` line out when capturing it.

[examples/podman](examples/podman/) and [examples/tools](examples/tools/) are two, with scripts. `tools` has `edit`, `nano`, `logged`, `desktop` and `search`.

## Settings

```
SHORTCUTS_HOME        global store (default ~/.shortcuts)
SHORTCUT_SHOW_DIR=0   hide the "→ dir" line
```

## Security model

> 1. shortcut runs exactly what its definitions say, without inspecting them.
> 2. A project's or import's shortcuts don't run until you approve them with `shortcut update`, and must be re-approved after any change to their definitions.
> 3. Approval covers definitions and the scripts they name, not the programs those call.
> 4. Global shortcuts have no approval step, so a global link target can change freely.
> 5. Anyone who can write to your shortcut store can already run code as you; that's out of scope.

## More

- [AGENT.md](AGENT.md): for coding agents using shortcut.
- [CONTRIBUTING.md](CONTRIBUTING.md): working on shortcut itself.
- [SECURITY.md](SECURITY.md): reporting a vulnerability.
- [CHANGELOG.md](CHANGELOG.md): what changed in each version.
- [docs/](docs/): diagrams, the JSON Schema for shortcut files, and every error message.
- [examples/podman](examples/podman/): a namespace for podman containers.
- Code, in Go: [cmd/shortcut](cmd/shortcut/) is the entry point. `internal/commands` has the command table, dispatch and the usage text, with one folder per command (`run`, `new`, `list`, `show`, `rm`, `update`, and `completion`, which holds the completion scripts). `internal/store` has the store files, entries, groups and lookup, `store/approval` the approval records and `store/write` everything that changes a store. `internal/term` has terminal output, prompts and the argument parser; `internal/platform` the file, process, lock and JSON behaviour that shortcut's output depends on, including JSON that refuses text that is not valid UTF-8. `internal/testkit` is the test sandbox. [installer/](installer/) is the install script.

## License

MIT. See [LICENSE](LICENSE).
