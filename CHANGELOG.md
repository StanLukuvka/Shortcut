# Changelog

## 2.0.0 — 2026-11-05

Running a shortcut, where it runs, and how a project is trusted all change. `shortcut migrate` converts existing stores; see "Upgrading from 1.x" in the README.

### Changed

- Running is only `shortcut run <name>` or `shortcut r <name>`. Bare `shortcut <name>` is an error that points there, for this release. Shortcuts can be named `new`, `list` and so on.
- Where a shortcut runs is the new `cwd` field: `caller` (the default), a path relative to the file that defines it, or an absolute path. `--cwd` on `new`, `add` and `link` replaces `--static`. `g_`, `glob_`, `global_`, registered prefixes and the `dir` field are gone, along with `shortcut prefix`.
- `new`, `add` and `link` use the global store unless you pass `--local`. A project's shortcuts no longer run at its root unless their `cwd` says so.
- `shortcut allow` is replaced by `shortcut update [group] [-y]`, which shows a diff against the last approval and saves a snapshot in `approvals/` in the global store. Every project store and namespace is approved on its own, a change blocks only that group, and an unapproved project shortcut is refused, not swapped for a global one of the same name. Any change needs approving, including ones made through `shortcut`. 1.x approvals don't carry over; `allowed.json` is no longer read.
- `location` prints a bare path. For an inline shortcut it fails, with no `(inline)` suffix. `location --log` prints the path of the log.
- `shortcut log` and `shortcut desktop` are now the bundled shortcuts `log` and `desktop`, next to a new `nano`. They're built only from core commands and come with `shortcut init --global templates`. The templates lost their `g_` too.
- The unknown-command error reads `unknown command 'x' (to run a shortcut: shortcut run x)`.
- Unknown fields are errors, at the top of a file, in an entry and in `args`, so a safety setting from a newer version is never silently ignored. A file from a newer format is refused, with a prompt to upgrade.
- `shortcut update` refuses a group with an entry that couldn't run: an invalid entry, or a missing script.

### Added

- Namespaces: each `.shortcuts/imports/*.shortcut` file, or an entry in `"imports"`, is a group run as `shortcut r docker delete XYZ`. One level only. A namespace and a shortcut sharing a name in one store is an error.
- The `.shortcut` format: the same JSON as `shortcuts.json`, with a required `version` and an optional `description` shown in `list`. The filename is the namespace's name.
- `--group NAME` on `new`, `add` and `link` adds to a namespace, creating `imports/NAME.shortcut` if needed.
- Declared arguments: an optional `args` list, checked for presence and count before running, passed as `$1`… and `SC_ARG_<NAME>`. `--flags` are never parsed and values never validated. `--arg name` and `--arg name=default` on `new`, `add` and `link`; `show` prints the usage line.
- `"confirm": true` (`--confirm`) asks before running; `shortcut r -y <name>` skips the question, and without a terminal the run fails without it.
- `list` has a column showing each shortcut's `cwd`.
- `shortcut fix` with no name checks every visible store and namespace, including entries that couldn't run.
- `migrate` also converts `g_` names, prefixes and `dir`, in the global store and the project you're in, and reports collisions without overwriting.

### Fixed

- `~/.shortcuts` could be taken for a project's store when `SHORTCUTS_HOME` pointed elsewhere, sending writes to the wrong store.

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
