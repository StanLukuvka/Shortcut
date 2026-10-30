# Changelog

## 1.0.0 — 2026-10-30

First public release.

- Registry: one `shortcuts.json` per store; each entry has a name, a description, and either an `inline` command or a `script` (relative = stored copy, absolute = linked).
- Scopes: global (`g_` / `glob_` / `global_`, runs in your current directory), prefix (`shortcut prefix add C_ /C` — runs in that directory), static (`--static DIR`), and local (project `.shortcuts/`, visible only inside the project).
- Commands: `new` (incl. one-liners with `--`), `add`, `link`, `list`, `show`, `nano`, `edit`, `location`, `rm`, `log`, `fix`, `desktop`, `prefix`, `init`, `migrate`, `completion bash`.
- Every run is logged to `logs/<name>.log` with the exit code; Ctrl+C and signal exit codes propagate.
- `init` imports shortcuts from a template folder or git repository without running anything, skipping invalid entries, symlinks, and paths that escape the source.
- `desktop` writes a launcher that keeps the terminal open and sends a desktop notification.
- In a terminal, logged runs use a pseudo-terminal, so colours, progress bars and full-screen programs keep working while the output is logged.
- A local shortcut that hides a global one of the same name prints a warning, is marked in `list`, and `run --global` runs the global one.
- Registry writes are locked, so two agents adding shortcuts at once don't overwrite each other.
- `install.sh` installs to `~/.local/bin` and sets up bash completion (and fish completion when fish is installed); `completion zsh` and `completion fish` are available. `migrate` converts old flat `~/.shortcuts/*.sh` files.
