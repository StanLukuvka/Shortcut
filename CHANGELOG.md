# Changelog

## 1.0.0 — 2026-10-30

First public release.

- Registry: one `shortcuts.json` per store; each entry has a name, a description, and either an `inline` command or a `script` (relative = stored copy, absolute = linked).
- Names say where a shortcut runs: `g_` in your current directory (and only `g_` names do), a registered prefix (`shortcut prefix add C_ /C`) in that directory, `--static DIR` in a fixed directory, and a project shortcut (`.shortcuts/`, read in place) at the project root.
- A project's shortcuts are ignored until you run `shortcut allow`, which shows every entry in full and remembers a hash of `shortcuts.json` and its scripts; any change needs allowing again. shortcut never inspects what it runs.
- Before running, the run directory is printed as one dim line on stderr (`SHORTCUT_SHOW_DIR=0` hides it).
- Commands: `new` (incl. one-liners with `--`), `add`, `link`, `list`, `show`, `nano`, `edit`, `location`, `rm`, `log`, `fix`, `desktop`, `prefix`, `init`, `allow`, `migrate`, `completion bash|zsh|fish`.
- Every run is logged to `logs/<name>.log` with the exit code; Ctrl+C and signal exit codes propagate.
- `init` creates a project store; `init --global` imports shortcuts from a template folder or git repository into the global store without running anything, skipping invalid entries, symlinks, and paths that escape the source.
- `desktop` writes a launcher that keeps the terminal open and sends a desktop notification.
- In a terminal, logged runs use a pseudo-terminal, so colours, progress bars and full-screen programs keep working while the output is logged.
- A project shortcut that hides a global one of the same name says so on the run line, is marked in `list`, and `run --global` runs the global one.
- Registry writes are locked, so two agents adding shortcuts at once don't overwrite each other.
- Install with `uv tool install` or `pipx install` from the git URL, or with `install.sh`, which copies `shortcut.py` to `~/.local/bin/shortcut` and sets up bash completion (and fish completion when fish is installed). `migrate` converts old flat `~/.shortcuts/*.sh` files.
