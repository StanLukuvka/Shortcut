# shortcut

Run scripts fast; agents set them up for you.

`shortcut` is a single-file, zero-dependency CLI (Python >=3.10 standard library only) designed to save, organize, and execute scripts and commands. Agents register commands with helpful descriptions and execution scopes, and users run, inspect, and modify them with clean, single-token invocations.

---

## Installation

Requirements: Linux (or another POSIX system) with Python 3.10+, bash, and git (only for `init --global` from a git URL). `nano` and `notify-send` are optional.

With [uv](https://docs.astral.sh/uv/) or [pipx](https://pipx.pypa.io/):

```bash
uv tool install git+https://github.com/StanLukuvka/Shortcut
pipx install git+https://github.com/StanLukuvka/Shortcut
```

Then set up completion yourself, for example `source <(shortcut completion bash)` in `~/.bashrc`.

Or clone the repository and run the installer, which also sets up completion:

```bash
git clone https://github.com/StanLukuvka/Shortcut.git
cd Shortcut
./install.sh
```

The installer copies one file (`shortcut.py`, installed as `shortcut`). Re-run it after `git pull` to upgrade.

By default, `install.sh` installs:
- Executable: `~/.local/bin/shortcut`
- Bash completion: `~/.local/share/bash-completion/completions/shortcut`
- Fish completion, if fish is installed: `~/.config/fish/completions/shortcut.fish`
- Global store: `~/.shortcuts/`

### Environment Overrides

You can customize the destination paths by setting environment variables during installation or execution:

| Variable | Default | Purpose |
|---|---|---|
| `BIN_DIR` | `~/.local/bin` | Binary installation target directory |
| `XDG_DATA_HOME` | `~/.local/share` | Data root for desktop entries and bash completions |
| `SHORTCUTS_HOME` | `~/.shortcuts` | Global shortcuts store root |

---

## Names and where they run

The name tells you where a shortcut runs:

| Name | Stored in | Visible | Runs in |
|---|---|---|---|
| `g_<name>` | Global store (`$SHORTCUTS_HOME`) | Everywhere | Your current directory |
| `<P_><name>`, with a registered prefix (e.g. `C_`) | Global store | Everywhere | The prefix's directory |
| any other name, with `--static DIR` | Global store | Everywhere | Its fixed `dir` |
| any other name, in a project | The project's `.shortcuts/` | Inside that project, once allowed | The project root (the folder containing `.shortcuts/`), or its `dir` relative to the root |

It holds both ways: a `g_` shortcut always runs in your current directory and can't have a `dir`, and a shortcut that should run in your current directory must be named `g_…`. A global shortcut without the prefix needs a fixed `dir`. A project store can't define `g_` or prefix names; those come only from your global store.

Before it runs anything, `shortcut` prints the run directory as one dim line on stderr, so it never mixes with the output:

```
→ /home/you/code/c-app
```

Set `SHORTCUT_SHOW_DIR=0` to turn it off.

### Project shortcuts and `shortcut allow`

A project's `.shortcuts/` is read in place, never copied, so it can be committed with the project. Relative script paths and `dir` resolve against the project root and can't escape it with `../`.

A project store is ignored until you allow it. `shortcut allow` prints every entry (name, run directory, and the full script or command) and asks once. It stores a hash of `shortcuts.json` and every script it uses in your global store. If any of them changes, for example after a `git pull`, the project is ignored again until you run `shortcut allow` again. Shortcuts you add to a project with `shortcut new`, `add` or `link` keep it allowed. `shortcut allow --revoke` takes the permission back.

Allowing is consent, not a check:

> shortcut never inspects scripts or commands. It runs exactly what the definition says, in the directory it says. Read a shortcut (`shortcut show <name>`) before you run it.

Inside a project, a project shortcut wins over a global one with the same name, so a project can define its own `test` or `build`. The run line says so whenever that happens, `list` marks both entries, and `shortcut run --global <name>` runs yours.

---

## JSON Entry Format

Shortcut registries are stored in `shortcuts.json` files within either the global store (`$SHORTCUTS_HOME/shortcuts.json`) or a project (`<project>/.shortcuts/shortcuts.json`).

### Storage Layout

```
<store>/
├── shortcuts.json         # Atomic JSON registry
├── scripts/               # Stored copies of managed executable scripts
│   └── <name>.<ext>
└── logs/                  # Tee execution logs from previous runs
    └── <name>.log
```

The global store also holds `allowed.json` (the projects you allowed, with their hashes). A project store gets a `.gitignore` for its `logs/`.

### Registry Example

```json
{
  "version": 1,
  "prefixes": {
    "C_": "/home/you/code/c-app"
  },
  "shortcuts": {
    "g_search": {
      "description": "Search files under cwd",
      "script": "scripts/g_search.sh",
      "created": "2026-10-30",
      "log": true
    },
    "penpot": {
      "description": "Start Penpot (http://localhost:9001)",
      "inline": "podman pod start pod_penpot",
      "dir": "/home/you",
      "created": "2026-10-30",
      "log": true
    },
    "C_build": {
      "description": "Build C project",
      "script": "/home/you/code/c-app/build.sh",
      "created": "2026-10-30",
      "log": true
    }
  }
}
```

### Fields

- `description` *(string, required on add/link)*: Human-readable summary of the shortcut.
- `inline` *(string)*: Direct command executed via `bash -c "<inline>" <name> "$@"`. Mutually exclusive with `script`.
- `script` *(string)*: Path to script. Relative paths resolve inside the global store, or inside the project root for a project shortcut, and must not escape it. Absolute paths reference external files without copying. A script with a `#!` line runs with that interpreter even without +x, and an executable binary runs directly. A missing script is an error.
- `dir` *(string, optional)*: Fixed absolute directory for an unprefixed global shortcut. In a project, a directory relative to the project root (an absolute `dir` is used as written; it is shown in `shortcut allow`). Not allowed on `g_` or prefix names.
- `icon` *(string, optional)*: Freedesktop icon name for desktop entries (defaults to `utilities-terminal`).
- `created` *(string, optional)*: ISO date (`YYYY-MM-DD`) when created.
- `created_by` *(string, optional)*: User or agent identifier recorded with `--by`.
- `log` *(boolean, default true)*: When enabled, output streams to the terminal and is also written to `logs/<name>.log` with the exit code. In a terminal the script runs on its own pseudo-terminal (like `script(1)`), so colours, progress bars and full-screen programs work as usual; when output is piped it is plain. Your keystrokes are passed through to that pseudo-terminal too: a full-screen program reads keys from the same terminal it draws on, so it would not see them otherwise. Set `"log": false` to give the script the terminal directly, for example if you need Ctrl+Z job control, which logged runs turn off.

---

## Command Reference

### `shortcut <name> [args...]` or `shortcut run [--global] <name> [args...]`
Execute a shortcut, passing trailing arguments through verbatim. `--global` skips the project's store:
```bash
shortcut g_search "*.py"
```

### `shortcut new <name> [--desc D] [--static DIR | --local] [--by C] [-- CMD...]`
Create an inline shortcut, or a script from a template that opens in `$VISUAL` / `$EDITOR` (nano if neither is set). The shortcut is created only if the editor exits successfully. Without a terminal (for example when an agent runs it), the editor is skipped and the script's path is printed.

An unprefixed name without `--static` goes into the project: the nearest `.shortcuts/` above you, or a new one in the current directory. That never happens in your home directory; use `g_<name>` or `--static DIR` there.

Put the command in single quotes, so `$1` reaches the shortcut instead of being expanded by your shell right now:
```bash
shortcut new g_hello --desc "Greet user" -- 'echo "Hello, $1"'
shortcut g_hello world    # Hello, world
```

### `shortcut add <name> <file|-> --desc D [--static DIR | --local] [--by C]`
Copy an existing script or stdin stream into the store:
```bash
shortcut add deploy ./deploy.sh --desc "Deploy service" --static /var/www
```

### `shortcut link <name> <path> --desc D [--static DIR | --local] [--by C]`
Reference an external script by absolute path without copying:
```bash
shortcut link build ~/src/build.sh --desc "Build project binary"
```

### `shortcut list [--names]`
List visible shortcuts and registered prefixes:
```bash
shortcut list
shortcut list --names
```

### `shortcut show <name>`
Inspect metadata and script or inline contents:
```bash
shortcut show g_search
```

### `shortcut nano <name>`
Edit the stored script or inline command using nano:
```bash
shortcut nano g_search
```

### `shortcut edit <name>`
Edit using `$VISUAL`, `$EDITOR`, or fallback to nano:
```bash
shortcut edit g_search
```

### `shortcut location <name>`
Print the path of the script file or registry location:
```bash
shortcut location g_search
```

### `shortcut rm <name> [-y]`
Remove the shortcut, its stored script copy, log file, and desktop launcher:
```bash
shortcut rm g_hello -y
```

### `shortcut log <name>`
Display the output and exit status of the previous run:
```bash
shortcut log g_search
```

### `shortcut fix <name>`
Print one complete diagnostic block for agent troubleshooting:
```bash
shortcut fix g_search
```

### `shortcut desktop <name>`
Generate a Freedesktop `.desktop` launcher under `$XDG_DATA_HOME/applications/`:
```bash
shortcut desktop penpot
```

### `shortcut prefix add <P_> <dir>`
Register a directory prefix in the global store:
```bash
shortcut prefix add C_ /home/you/code/c-app
```

### `shortcut prefix rm <P_> [--force]`
Remove a registered directory prefix. It refuses while shortcuts still use the prefix; remove them first, or pass `--force` to keep them (they won't run until the prefix is added back):
```bash
shortcut prefix rm C_
```

### `shortcut prefix list`
List all registered directory prefixes:
```bash
shortcut prefix list
```

### `shortcut init` / `shortcut init --global [SOURCE]`
`shortcut init` creates an empty, allowed project store (`.shortcuts/`) in the current directory. To use a project that already has one, clone it and run `shortcut allow` there; it is never copied.

`--global SOURCE` copies a collection (a folder or git URL) into your global store. Nothing is run. Names that already exist, symlinks, paths that escape the source, and `g_` or prefix names with a `dir` are skipped and reported:
```bash
shortcut init
shortcut init --global https://github.com/example/dev-shortcuts.git
shortcut init --global templates          # the examples in this repository
```

### `shortcut allow [-y] [--revoke]`
Review the current project's shortcuts and let them run (see [Project shortcuts](#project-shortcuts-and-shortcut-allow)). `-y` skips the question, for scripts; without a terminal it is required:
```bash
shortcut allow
```

### `shortcut migrate`
Convert legacy flat `~/.shortcuts/*.sh` scripts to global `g_<stem>` entries:
```bash
shortcut migrate
```

### `shortcut completion bash|zsh|fish`
Output a shell completion script. The installer sets up bash, and fish when it is installed. For zsh, add this to `~/.zshrc` after `compinit`:
```bash
source <(shortcut completion zsh)
```

### `shortcut help` / `shortcut --version`
Display the CLI usage manual or version number:
```bash
shortcut help
shortcut --version
```

---

## For Agents

When creating shortcuts for users:
1. **Register with description**: Always supply `--desc` so users understand what the shortcut accomplishes:
   ```bash
   shortcut new test --local --desc "Run full integration test suite" -- 'pytest tests/'
   ```
2. **Choose appropriate scope**:
   - Use `g_<name>` for general-purpose tools that should operate in the caller's working directory.
   - Use `--local` for project-specific operations (builds, test runners, linters). The user may need to run `shortcut allow` in that project; `new` says so.
   - Use `--static <dir>` when a process must execute in a specific fixed folder.
3. **Notify the user**: When a task is ready, tell the user:
   > Run it: `shortcut <name>`
4. **Diagnose failures**: If a shortcut fails, run `shortcut fix <name>` to inspect the registered JSON, script contents, and the latest execution log ending with the exit code.

---

## Development

Run the test suite (it never touches your real `~/.shortcuts`; every test uses a temporary `HOME`):

```bash
python3 -m unittest discover -s tests -v
```

## Uninstall

```bash
rm ~/.local/bin/shortcut ~/.local/share/bash-completion/completions/shortcut
rm -f ~/.config/fish/completions/shortcut.fish
rm ~/.local/share/applications/shortcut-*.desktop   # desktop launchers, if any
rm -r ~/.shortcuts                                  # your shortcuts — only if you want them gone
```

## License

MIT — see [LICENSE](LICENSE).
