# shortcut

Names shell commands and scripts, and runs them from any directory. One binary, no dependencies. Linux and macOS;
on Windows, use WSL. Inline shortcuts need bash.

```bash
curl -fsSL https://github.com/StanLukuvka/Shortcut/releases/latest/download/install.sh | sh
```

- The installer needs `curl` or `wget`, and `sha256sum` or `shasum`. It checks the archive against the release's
  `checksums.txt` and stops if they differ.
- It writes the binary to `~/.local/bin/shortcut` (or `$BIN_DIR/shortcut`), and completion for bash, and for fish and
  zsh where they are installed. For zsh it adds one marked block to `~/.zshrc`; `SHORTCUT_NO_RC=1` skips it.
  `$XDG_DATA_HOME` (default `~/.local/share`) and `$XDG_CONFIG_HOME` (default `~/.config`) move the completion files.
- `SHORTCUT_VERSION=0.1.0` installs a given version. Running it again upgrades.
- To uninstall, delete `~/.local/bin/shortcut`, `~/.local/share/bash-completion/completions/shortcut`,
  `~/.config/fish/completions/shortcut.fish`, `~/.local/share/shortcut/`, and the lines from
  `# >>> shortcut completion >>>` to `# <<< shortcut completion <<<` in `~/.zshrc`. `~/.shortcuts` stays.

```bash
shortcut new hi -- 'echo hello "$1"'
shortcut r hi you
shortcut list
```

## Commands

```
shortcut r <name> [args]              run your global shortcut (also: run, r <ns> <name>)
shortcut r . <name> [args]            run the project's shortcut (also: r . <ns> <name>)
shortcut r -y <name>                  answer a confirm prompt

shortcut new <name> -- CMD            inline shortcut, run with bash -c
shortcut new <name> --script PATH     use a script where it is; a path inside the store (or a project or namespace folder) is kept relative

shortcut list                         your global shortcuts and the project's; warns about invalid entries and missing scripts
shortcut list --names [ns|.]          bare global names, a global namespace's, or with . the project's
shortcut show [.] <name>              details and script; with . the project's
shortcut show --json [.] <name>       entry and where it resolves, as JSON
shortcut show --field KEY [.] <name>  one field, bare, for scripts

shortcut rm <name> [-y]               remove a global or global namespace shortcut; says what goes, with -y too
shortcut rm --local <name>            remove your entry from .shortcuts.local
shortcut rm --namespace <ns> [-y]     remove a global namespace and its scripts/<ns>/ folder; one imported
                                      from elsewhere is only no longer imported

shortcut completion bash|zsh|fish
shortcut help [command]
shortcut --version
```

- `new` takes `--desc D`, `--cwd caller|DIR` (where it runs; default: the caller's directory), `--confirm` (ask
  before running, showing the description and the words given), `--local` (write to `.shortcuts.local`) and
  `--namespace NS` (write to a global namespace).
- `new <name> -- CMD`: one quoted word is kept as written, so `-- 'ls "$1" | wc -l'` keeps its pipe. Several words are
  each quoted, so each stays one word: `-- echo '$HOME'` prints `$HOME`. `$1`, `$@` and the other arguments stay live.
- `--local`, on `new` and `rm`, works only from the project root.
- `show --json` prints one object: `name`, `namespace`, `scope`, `defined_in`, `source`, `cwd`, `run_cwd`,
  `script_path`, `error` and `entry`, with its own keys and the entry's sorted. A field that can't be worked out is null.
- `show --field KEY` prints one field of that object bare, or of the entry (`description`, `inline`, `script`). `true`
  and `false` print as words, and a null or absent field prints nothing.

## Files

| Place | What | Written by |
|---|---|---|
| `~/.shortcuts/` (or `$SHORTCUTS_HOME`) | the global store | shortcut |
| `<project>/.shortcuts` | the project's shared shortcuts, committed | you, by hand; shortcut never writes it |
| `<project>/.shortcuts.local` | your own entries; add it to your `.gitignore` if it should stay out of git | shortcut, with `--local` |

```
~/.shortcuts/
├── shortcuts.json          entries
├── imports/<ns>.shortcut   namespaces
└── .lock                   the lock every write takes
```

`shortcuts.json`, `.shortcuts`, `.shortcuts.local` and every `<ns>.shortcut` are JSON with `"version": 1`.
[docs/shortcut.schema.json](docs/shortcut.schema.json) describes the entries:

```json
{"version": 1, "shortcuts": {"test": {"description": "Run the tests", "inline": "go test \"$@\""}}}
```

## Scope

| You type | It looks in | Never looks in |
|---|---|---|
| `shortcut r build` | the global store | any project |
| `shortcut r docker run` | the global namespace `docker` | any project |
| `shortcut r . build` | the nearest project's `.shortcuts`, with `.shortcuts.local` over it | the global store |
| `shortcut r . terraform plan` | the namespace `terraform` that the project's `.shortcuts` imports | the global store |

- The project is the nearest folder, walking up, with `.shortcuts` or `.shortcuts.local`. Only that one is read.
- `.shortcuts.local` overrides `.shortcuts` per entry.
- Nothing falls back to the other scope. A name found only in the other one is an error that gives the command to use,
  and nothing runs. `r .` outside a project is an error.
- If the project's file can't be read, nothing runs with the dot until it's fixed. Global shortcuts are not affected.
- The dot is the first word after the options; a shortcut or namespace can't be named `.`.
- Relative `script` and `cwd` paths resolve from the folder holding the file.

## Namespaces

- A namespace is a file `<ns>.shortcut`, with entries as in `shortcuts.json`. A bare `.shortcut` is an error.
- `scripts/<ns>/`, next to the file, holds its scripts. `rm <name>` removes only the entry, never a script file;
  `rm --namespace` deletes the folder.
- `~/.shortcuts/imports/<ns>.shortcut` is found by its name. To install one, copy `<ns>.shortcut` and `scripts/<ns>/`
  into `~/.shortcuts/imports/`.
- A file anywhere else is listed in `shortcuts.json` or a project's `.shortcuts`:
  `"imports": {"<ns>": "path/to/<ns>.shortcut"}`. The path is relative to the file that lists it, and can start with `~`.
- A script runs a sibling with `shortcut r "$SHORTCUT_NAMESPACE" <name>`.
- [examples/podman](examples/podman/) and [examples/tools](examples/tools/) are namespaces to copy.

## Running

- An inline shortcut runs as `bash -c CMD <name> ARGS...`. A script runs as it is: it must be executable and start
  with `#!`, or be a binary. One that is not executable is refused with the `chmod +x` command; `new --script` and
  `list` say so too.
- Every word after the name reaches it as `$1`, `$2`..., flags included. A first `--` is passed on as it is.
- It runs in its `cwd`, and gets `SHORTCUT_NAME`, `SHORTCUT_NAMESPACE` and `SHORTCUT_CALLER_CWD`. Its exit code is
  shortcut's.
- `SHORTCUT_SHOW_DIR=0` hides the `→ dir` line; `1` or unset shows it; any other value is an error.
- `SHORTCUTS_HOME` must be an absolute path (or start with `~/`); unset, the global store is `~/.shortcuts`. `~` needs
  an absolute `$HOME`. Anything else is an error, and nothing runs.
- Symlinks in paths are followed. A path that does not exist where one is needed, or can't be read, is an error.
- shortcut does what it was told, or stops with an error. [docs/errors.md](docs/errors.md) lists every message.

## Agents

Agents write most shortcuts, for the person they work with. An agent:

- always passes `--desc`;
- in a project, uses `--local` from the project root, and proposes entries for `.shortcuts` (`show --json` prints one
  as `entry`) for the person to commit;
- never passes `-y` for the person: `rm` and a `--confirm` shortcut are theirs to answer;
- reads a project's shortcut with `shortcut show . <name>` before running it;
- ends with how to run it: `Run it: shortcut r <name>`, or `shortcut r . <name>` for one in the project.

## Security model

> 1. shortcut runs exactly what its definitions say, without inspecting them.
> 2. `shortcut r <name>` only runs your own global shortcuts. A project's
>    shortcuts run only when you name the project: `shortcut r . <name>`.
> 3. shortcut does not review a project's shortcuts or notice when they
>    change. Running one is running that repo's code, as with `make` or
>    `npm run`. Read it first.
> 4. Anyone who can write to your shortcut store can already run code as
>    you; that's out of scope.

MIT licence.
