# tools namespace

Small helpers for working with shortcuts, run as `shortcut r tools <name>`.

| Name | What |
|---|---|
| `search <pattern> [path]` | Lists files under `path` (default: the current folder) whose name contains `pattern` |
| `edit <name>` | Opens a shortcut's script, or the file that defines an inline one, in `$VISUAL`, then `$EDITOR`, then nano |
| `nano <name>` | `edit` with nano |
| `logged <name> [args...]` | Runs a shortcut and keeps its output in `$XDG_STATE_HOME/shortcut/<name>.log` (default `~/.local/state/shortcut/`); each run replaces the log |
| `desktop [--remove] <name>` | Creates a desktop launcher for a global shortcut, or removes it. Linux only |

## Install

Copy `tools.shortcut` and the `scripts/tools/` folder into `~/.shortcuts/imports/`, then approve it:

```bash
mkdir -p ~/.shortcuts/imports/scripts
cp examples/tools/tools.shortcut ~/.shortcuts/imports/
cp -r examples/tools/scripts/tools ~/.shortcuts/imports/scripts/
shortcut update tools
```

After changing either there, run `shortcut update tools` again. `shortcut rm --namespace tools` removes it.
