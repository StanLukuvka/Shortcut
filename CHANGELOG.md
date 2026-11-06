# Changelog

## 2.0.0 — 2026-11-05

Changes how a shortcut is run, where it runs and how a project is approved. `shortcut migrate` converts existing stores. See "Upgrading from 1.x" in the README.

### Changed

- Shortcuts run only with `shortcut run <name>` or `shortcut r <name>`. Bare `shortcut <name>` is an error that points there. Shortcuts can be named `new`, `list` and so on.
- The new `cwd` field sets where a shortcut runs: `caller` (the default), a path relative to the file that defines it, or an absolute path. `--cwd` on `new`, `add` and `link` replaces `--static`. `g_`, `glob_`, `global_`, registered prefixes, the `dir` field and `shortcut prefix` are removed.
- `new`, `add` and `link` use the global store unless you pass `--local`. A project's shortcuts run at its root only if their `cwd` says so.
- `shortcut allow` is replaced by `shortcut update [group] [-y]`, which shows a diff against the last approval and saves a snapshot in `approvals/` in the global store. Each project store and namespace is approved on its own, a change blocks only that group, and an unapproved project shortcut is refused, not replaced by a global one of the same name. Any change needs approving, including changes made through `shortcut`. 1.x approvals don't carry over and `allowed.json` is no longer read: run `shortcut update` in each project.
- `location` prints a bare path. For an inline shortcut it fails, and no longer prints an `(inline)` suffix. `location --log` prints the path of the log.
- `shortcut log` and `shortcut desktop` are now the bundled shortcuts `log` and `desktop`, with a new `nano`. They use only core commands. Get them with `shortcut init --global templates`. The bundled shortcuts no longer have a `g_` prefix.
- The unknown-command error reads `unknown command 'x' (to run a shortcut: shortcut run x)`.
- Unknown fields are errors, at the top of a file, in an entry and in `args`. A file from a newer format is refused, with a prompt to upgrade.
- `shortcut update` refuses a group with an entry that couldn't run: an invalid entry, or a missing script.
- If a project's `shortcuts.json` can't be read, `run` refuses any name the project had when last approved. `run --global` reaches the global one. Any other name falls through to the global store with a warning.
- With an unreadable project `shortcuts.json`, `edit` and `rm` refuse a name in the global store unless you pass `--global`.
- Declining a prompt from `run`, `rm` or `update` exits 1 and prints `Aborted.` to stderr. The exit code for `rm` changes from 0 to 1.
- Run logs of project shortcuts live in the global store, under `logs/<id>/`. A new project's `.gitignore` lists only `.lock`. Logs already in `.shortcuts/logs/` are left there and no longer read. A project's store is never written through a symlink: `.shortcuts/` itself, `.lock`, `.gitignore`, or a script `new` or `add` would create.
- `init --global SOURCE` shows what it would add and asks. Without a terminal it needs `-y`. It copies what it showed, writes nothing if you decline, and never replaces a file already in the store.
- `shortcut update` approves the resolved target of a relative `cwd`. A symlink moved under it needs approving. The run line and the confirm prompt show the real path.
- `shortcut update` shows a script with a bad byte or a NUL as text, with the bytes escaped, and hides only compiled (ELF) programs. Approving one means typing `binary` at the prompt. `-y` skips that.
- `shortcut fix` says `definitions unchanged since approved` instead of `ok` for an approved group, and `update` says what approval covers: definitions, not what they call.
- Nothing `shortcut` prints from a file can move the cursor or rewrite the screen. Control characters show as `^[`. Only a shortcut's own output reaches the terminal unchanged.
- `shortcut version` and `init -g` are removed. Use `shortcut --version` and `init --global`.

### Added

- Namespaces: each `.shortcuts/imports/*.shortcut` file, or an entry in `"imports"`, is a group run as `shortcut r docker delete XYZ`. Namespaces don't nest. A namespace and a shortcut with the same name in one store is an error.
- The `.shortcut` format: the same JSON as `shortcuts.json`, with a required `version` and an optional `description` shown in `list`. The filename is the namespace's name.
- `--namespace NAME` on `new`, `add` and `link` adds to a namespace, creating `imports/NAME.shortcut` if needed.
- Declared arguments: an optional `args` list, checked for presence and count before running, passed as `$1`… and `SC_ARG_<NAME>`. `--flags` are never parsed and values are never validated. `new`, `add` and `link` take `--arg name` and `--arg name=default`. `show` prints the usage line.
- `"confirm": true` (`--confirm`) asks before running. `shortcut r -y <name>` skips the question. Without a terminal the run fails without `-y`.
- `list` shows each shortcut's `cwd` in a column.
- `--global` on `show`, `edit`, `location`, `rm` and `fix`, to reach a global shortcut that a project one hides.
- `new` prints a note when an inline command won't see its arguments.
- `list --names NAMESPACE` prints the names in a namespace. Completion works after `run --global` and `-y`, offers those flags, and completes a namespace's shortcuts.
- `shortcut fix` with no name checks every visible store and namespace, including entries that couldn't run.
- `migrate` also converts `g_` names, prefixes and `dir`, in the global store and the project you're in, and reports collisions without overwriting.

### Fixed

- An entry with both `inline` and `script`, or neither, is refused everywhere. With both, `run` ran the inline and ignored the script, and `fix` reported the entry as broken.
- `new x -- echo '$1'` stored `$1` in single quotes, where it never expanded. `$1`, `$@`, `$*` and `${1}` in a several-word command now stay live.
- `~/.shortcuts` could be taken for a project's store when `SHORTCUTS_HOME` pointed elsewhere, sending writes to the wrong store.
- An `args` entry needs a `name` that is a string. A missing name, or `true`, passed the check.
- `init --global SOURCE` skips a name that is already a namespace in the global store.
- `init --global` with no source on an existing store says it already exists. It said it had created an empty one.

## 1.0.0 — 2026-10-30

First public release.

- Registry: one `shortcuts.json` per store; each entry has a name, a description, and either an `inline` command or a `script` (relative = stored copy, absolute = linked).
- Names say where a shortcut runs: `g_` in your current directory (and only `g_` names do), a registered prefix (`shortcut prefix add C_ /C`) in that directory, `--static DIR` in a fixed directory, and a project shortcut (`.shortcuts/`, read in place) at the project root.
- A project's shortcuts are ignored until you run `shortcut allow`, which shows every entry in full and remembers a hash of `shortcuts.json` and its scripts; any change needs allowing again. shortcut never inspects what it runs.
- Before running, the run directory is printed as one dim line on stderr (`SHORTCUT_SHOW_DIR=0` hides it).
- Commands: `run` (or `r`), `new` (incl. one-liners with `--`), `add`, `link`, `list`, `show`, `edit`, `location`, `rm`, `log`, `fix`, `desktop`, `prefix`, `init`, `allow`, `migrate`, `completion bash|zsh|fish`.
- Every run is logged to `logs/<name>.log` with the exit code; Ctrl+C and signal exit codes propagate.
- `init` creates a project store; `init --global` imports shortcuts from a template folder or git repository into the global store without running anything, skipping invalid entries, symlinks, and paths that escape the source.
- `desktop` writes a launcher that keeps the terminal open and sends a desktop notification.
- In a terminal, logged runs use a pseudo-terminal, so colours, progress bars and full-screen programs keep working while the output is logged.
- A project shortcut that hides a global one of the same name says so on the run line, is marked in `list`, and `run --global` runs the global one.
- Registry writes are locked, so two agents adding shortcuts at once don't overwrite each other.
- Install with `uv tool install` or `pipx install` from the git URL, or with `install.sh`, which copies `shortcut.py` to `~/.local/bin/shortcut` and sets up bash completion (and fish completion when fish is installed). `migrate` converts old flat `~/.shortcuts/*.sh` files.
