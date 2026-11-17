# shortcut

shortcut gives your shell commands and scripts short names, so you can run them from any directory. Instead of
remembering `df -h . | tail -1` or where you keep your backup script, you give it a name once and then run it with
`shortcut r <name>`.

```bash
shortcut new hello --desc "Say hello" -- 'echo "hello, ${1:-world}"'
shortcut r hello Sam
```

```
→ /home/sam/notes
hello, Sam
```

It's one binary with no dependencies, for Linux and macOS (on Windows, use WSL). Shortcuts written as a command need
bash.

If you're an agent setting up shortcuts for someone, read [AGENT.md](AGENT.md) as well.

## Install

```bash
curl -fsSL https://github.com/StanLukuvka/Shortcut/releases/latest/download/install.sh | sh
```

The installer needs `curl` or `wget`, and `sha256sum` or `shasum`. It checks the download against the release's
`checksums.txt` and stops if they differ.

It puts the binary in `~/.local/bin/shortcut` (set `BIN_DIR` to put it somewhere else) and sets up tab completion for
bash, and for fish and zsh if you have them. For zsh it adds one marked block to `~/.zshrc`; set `SHORTCUT_NO_RC=1` if
you'd rather it didn't. The completion files go under `$XDG_DATA_HOME` (default `~/.local/share`) and
`$XDG_CONFIG_HOME` (default `~/.config`).

To install a particular version, set `SHORTCUT_VERSION=0.1.0`. To upgrade, run the installer again.

To uninstall, delete `~/.local/bin/shortcut`, `~/.local/share/bash-completion/completions/shortcut`,
`~/.config/fish/completions/shortcut.fish` and `~/.local/share/shortcut/`, and remove the lines from
`# >>> shortcut completion >>>` to `# <<< shortcut completion <<<` in `~/.zshrc`. Your shortcuts in `~/.shortcuts` are
left where they are.

## Making shortcuts

### A command

Put the command after `--`, in single quotes:

```bash
shortcut new disk --desc "Free space here" -- 'df -h "${1:-.}" | tail -1'
shortcut r disk
shortcut r disk /home
```

The single quotes keep the command exactly as you wrote it, pipe and all, and it runs as `bash -c`. Every word you type
after the name reaches it as `$1`, `$2` and so on, and `"$@"` is all of them. So the first run above checks the current
directory, and the second checks `/home`.

If you leave out the quotes, each word is kept as one word on its own: `shortcut new home -- echo '$HOME'` prints
`$HOME`, not your home directory. The arguments still work: `$1`, `"$@"` and the rest are passed through either way.

A command that doesn't use its arguments never sees them. shortcut tells you so when you make it:

```
$ shortcut new ports --desc "What's listening" -- ss -tlnp
note: 'ports' won't see any arguments you give it; add "$@" to pass them on
Created inline shortcut 'ports'.
```

`--desc` is optional, but it's what `list` shows next to the name, so it's worth a few words.

### A script you already have

```bash
shortcut new backup --desc "Back up a folder" --script ~/bin/backup.sh
shortcut r backup Documents
```

shortcut runs the script where it is, with its own `#!` line. It doesn't copy it, so when you change the script the
shortcut runs the new version. The script must be executable and start with `#!` (or be a compiled program). If it
isn't executable, shortcut says so, with the `chmod +x` command to fix it.

### Where it runs

By default a shortcut runs in whatever directory you're in when you run it. To always run it somewhere else, give it a
`--cwd`:

```bash
shortcut new logs --desc "Recent logs" --cwd /var/log -- 'ls -t | head'
```

Before running, shortcut prints the directory it runs in:

```
→ /var/log
```

Set `SHORTCUT_SHOW_DIR=0` to hide that line.

### Asking first

For anything you don't want to run by accident, add `--confirm`. shortcut then shows the description and asks before
it runs, naming the words you gave and the directory it runs in:

```bash
shortcut new deploy --desc "Deploy to production" --confirm -- './deploy.sh "$@"'
```

To answer yes in advance, as in a script, put `-y` before the name: `shortcut r -y deploy`. When there's no terminal to
ask in, as in a cron job, a `--confirm` shortcut doesn't run unless you pass `-y`.

### Changing one

Shortcuts aren't edited in place. To change one, remove it and make it again. If it uses a script, you can just edit
the script.

## Seeing and removing shortcuts

`shortcut list` shows everything you can run from where you are, with where each one runs:

```
Project shortcuts (/home/sam/myapp), run with: shortcut r . <name>:
  serve                src     shared  Start the dev server
  test                 caller  shared  Run the tests

Global shortcuts:
  backup               caller    Back up a folder
  disk                 caller    Free space here
  logs                 /var/log  Recent logs
```

`caller` means "wherever you run it from". `list` also warns you about entries it can't use and scripts that have gone
missing.

`shortcut show <name>` shows one shortcut in full: where it's defined, where it runs, and its command or the text of
its script.

`shortcut rm <name>` removes one of your global shortcuts (`shortcut rm <namespace> <name>` one in a namespace). It
tells you what it's about to remove and asks first; `-y` skips the question but it still tells you. It removes only
the entry: a script you pointed it at stays where it is.

`shortcut help` lists every command, and `shortcut help <command>` shows one command's options.

## Shortcuts for a project

A project can keep its own shortcuts in a file called `.shortcuts` at its top level, committed with the code, so
everyone working on it gets the same ones:

```json
{
  "version": 1,
  "shortcuts": {
    "test":  {"description": "Run the tests", "inline": "go test \"$@\" ./..."},
    "serve": {"description": "Start the dev server", "inline": "npm run dev", "cwd": "web"}
  }
}
```

You write this file by hand; shortcut never changes it. A relative `cwd` or `script` is relative to the folder the file
is in, so `serve` above runs in the project's `web` folder from wherever you are inside the project.

To run a project's shortcut, put a dot before the name:

```bash
shortcut r . test -v
shortcut r . serve
```

The dot means "this project". Without it, shortcut looks only at your own global shortcuts, and with it, only at the
project's. It never falls back from one to the other, so a project can't take over a name you use, and your own
shortcuts don't change what `r .` runs in a project. If you use the wrong one, the error tells you the right command:

```
$ shortcut r test
error: shortcut 'test' not found. The project in /home/sam/myapp has it: shortcut r . test
```

The project is the nearest folder, going up from where you are, that has a `.shortcuts` or `.shortcuts.local`. Only
that one is used. `r .` outside any project is an error. If the project's file can't be read, nothing runs with the
dot until it's fixed; your global shortcuts still work.

### Your own shortcuts in a project

For shortcuts that are yours alone, run `shortcut new` with `--local` from the project's top folder:

```bash
shortcut new --local mine --desc "My debug run" -- 'DEBUG=1 go run . "$@"'
shortcut r . mine
```

They go in `.shortcuts.local`, next to `.shortcuts`. Add `.shortcuts.local` to your `.gitignore` if it should stay out
of git. An entry in `.shortcuts.local` overrides the one with the same name in `.shortcuts`. To remove one, use
`shortcut rm --local <name>`, again from the top folder.

A project's shortcuts run that project's code. Before running one in a repository you didn't write, look at it with
`shortcut show . <name>`.

## Namespaces

A namespace is a named group of shortcuts, run with its name first:

```bash
shortcut new --namespace git st --desc "Short status" -- 'git status -sb'
shortcut r git st
```

Each namespace is its own file, `~/.shortcuts/imports/<namespace>.shortcut`, laid out like any other shortcut file. Its
scripts go in `scripts/<namespace>/` next to it. A script can run another shortcut from its own namespace with
`shortcut r "$SHORTCUT_NAMESPACE" <name>`.

Namespaces are easy to share. To install one, copy its `<namespace>.shortcut` file and its `scripts/<namespace>/`
folder into `~/.shortcuts/imports/`. [examples/tools](examples/tools/) and [examples/podman](examples/podman/) are two
to start from:

```bash
mkdir -p ~/.shortcuts/imports
cp -R examples/tools/tools.shortcut examples/tools/scripts ~/.shortcuts/imports/
shortcut r tools search README
```

A namespace file can also live somewhere else, such as in a repository. List it under `"imports"` in
`~/.shortcuts/shortcuts.json`, or in a project's `.shortcuts`:

```json
{"version": 1, "imports": {"team": "../team-tools/team.shortcut"}, "shortcuts": {}}
```

The path is relative to the file that lists it, and can start with `~`. A namespace imported by a project is run with
the dot: `shortcut r . team <name>`.

`shortcut rm --namespace <namespace>` removes a namespace in `~/.shortcuts/imports/` together with its scripts folder.
For one listed under `"imports"`, it only removes the listing and leaves the file alone.

## Reference

### Commands

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
- Options go before the dot: `shortcut r -y . <name>`.
- `show --json` prints one object: `name`, `namespace`, `scope`, `defined_in`, `source`, `cwd`, `run_cwd`,
  `script_path`, `error` and `entry`, with its own keys and the entry's sorted. A field that can't be worked out is null.
- `show --field KEY` prints one field of that object bare, or of the entry (`description`, `inline`, `script`). `true`
  and `false` print as words, and a null or absent field prints nothing.

### Files

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

### Scope

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

### Namespaces

- A namespace is a file `<ns>.shortcut`, with entries as in `shortcuts.json`. A bare `.shortcut` is an error.
- `scripts/<ns>/`, next to the file, holds its scripts. `rm <name>` removes only the entry, never a script file;
  `rm --namespace` deletes the folder.
- `~/.shortcuts/imports/<ns>.shortcut` is found by its name. To install one, copy `<ns>.shortcut` and `scripts/<ns>/`
  into `~/.shortcuts/imports/`.
- A file anywhere else is listed in `shortcuts.json` or a project's `.shortcuts`:
  `"imports": {"<ns>": "path/to/<ns>.shortcut"}`. The path is relative to the file that lists it, and can start with `~`.
- A script runs a sibling with `shortcut r "$SHORTCUT_NAMESPACE" <name>`.

### Running

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
