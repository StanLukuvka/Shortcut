# shortcut

Names shell commands and scripts, and runs them from any directory. One binary, no dependencies. Linux and macOS;
on Windows, use WSL. Inline shortcuts need bash.

```bash
curl -fsSL https://github.com/StanLukuvka/Shortcut/releases/latest/download/install.sh | sh
```

The installer writes the binary to `~/.local/bin/shortcut` (or `$BIN_DIR`) and completion for bash, and for fish and
zsh where they are installed; for zsh it adds one marked block to `~/.zshrc` (`SHORTCUT_NO_RC=1` skips it).
`SHORTCUT_VERSION=0.1.0` installs a given version.

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
shortcut rm --local <name>            remove your entry from .shortcuts.local (from the project root)
shortcut rm --namespace <ns> [-y]     remove a global namespace and its scripts/<ns>/ folder; one imported
                                      from elsewhere is only no longer imported

shortcut completion bash|zsh|fish
shortcut help [command]
shortcut --version
```

`new` takes `--desc D`, `--cwd caller|DIR` (where it runs; default: the caller's directory), `--confirm` (ask before
running), `--local` (write to `.shortcuts.local`) and `--namespace NS` (write to a global namespace).

`new <name> -- CMD`: one quoted word is kept as written (`-- 'ls "$1" | wc -l'` keeps its pipe); several words are each
quoted. `$1`, `$@` and the other arguments stay live.

## Scope

| You type | It looks in | Never looks in |
|---|---|---|
| `shortcut r build` | the global store `~/.shortcuts/` (or `$SHORTCUTS_HOME`) | any project |
| `shortcut r docker run` | the global namespace `docker` | any project |
| `shortcut r . build` | the nearest project's `.shortcuts`, with `.shortcuts.local` over it | the global store |
| `shortcut r . terraform plan` | the namespace `terraform` that the project's `.shortcuts` imports | the global store |

- The project is the nearest folder, walking up, with `.shortcuts` or `.shortcuts.local`. Only that one is read.
- `.shortcuts` is shared and committed, and shortcut never writes it. `.shortcuts.local` is yours and overrides it per
  entry; add it to your `.gitignore` if it should stay out of git.
- Nothing falls back to the other scope. A name found only in the other one is an error that gives the command to use,
  and nothing runs. `r .` outside a project is an error. A project problem never affects global lookups.
- The dot is the first word after the options; a shortcut or namespace can't be named `.`.
- A namespace is a file `<ns>.shortcut`, with its scripts in `scripts/<ns>/` next to it. `~/.shortcuts/imports/<ns>.shortcut`
  is found by its name; one anywhere else is listed under `"imports"` in `shortcuts.json` or `.shortcuts`.
- Relative `script` and `cwd` paths resolve from the folder holding the file.

## Running

- An inline shortcut runs as `bash -c CMD <name> ARGS...`. A script runs as it is: it must be executable and start
  with `#!`, or be a binary.
- It runs in its `cwd`, and gets `SHORTCUT_NAME`, `SHORTCUT_NAMESPACE` and `SHORTCUT_CALLER_CWD`. Its exit code is
  shortcut's.
- `SHORTCUT_SHOW_DIR=0` hides the `→ dir` line; `1` or unset shows it.
- shortcut does what it was told, or stops with an error. [docs/errors.md](docs/errors.md) lists every message.

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
