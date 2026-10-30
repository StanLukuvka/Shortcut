# shortcut

Run scripts fast; agents set them up for you.

`shortcut` is a single-file, zero-dependency CLI (Python >=3.10 standard library only) designed to save, organize, and execute scripts and commands. Agents register commands with helpful descriptions and execution scopes, and users run, inspect, and modify them with clean, single-token invocations.

---

## Installation

Requirements: Linux (or another POSIX system) with Python 3.10+, bash, and git (only for `init` from a git URL). `nano` and `notify-send` are optional.

Clone the repository and run the installer:

```bash
git clone https://github.com/StanLukuvka/Shortcut.git
cd Shortcut
./install.sh
```

The installer copies one file. Re-run it after `git pull` to upgrade.

By default, this installs:
- Executable: `~/.local/bin/shortcut`
- Bash completion: `~/.local/share/bash-completion/completions/shortcut`
- Global store: `~/.shortcuts/`

### Environment Overrides

You can customize the destination paths by setting environment variables during installation or execution:

| Variable | Default | Purpose |
|---|---|---|
| `BIN_DIR` | `~/.local/bin` | Binary installation target directory |
| `XDG_DATA_HOME` | `~/.local/share` | Data root for desktop entries and bash completions |
| `SHORTCUTS_HOME` | `~/.shortcuts` | Global shortcuts store root |

---

## Scopes

Every shortcut resolves according to a strict scope hierarchy that determines visibility and execution working directory:

| Scope | Name Rule | Stored In | Visibility | Execution Directory (`cwd`) |
|---|---|---|---|---|
| **global** | Starts with `g_`, `glob_`, or `global_` | Global store (`$SHORTCUTS_HOME`) | Everywhere | Caller's current working directory |
| **prefix** | Starts with a registered prefix (e.g. `C_`) | Global store (`$SHORTCUTS_HOME`) | Everywhere | The prefix's registered target directory |
| **static** | Unprefixed, entry contains `"dir"` | Global store (`$SHORTCUTS_HOME`) | Everywhere | Fixed directory specified by `"dir"` |
| **local** | Unprefixed, entry has no `"dir"` | Local project store (`.shortcuts/`) | Inside project tree | Project root (directory containing `.shortcuts/`) |

---

## JSON Entry Format

Shortcut registries are stored in `shortcuts.json` files within either the global store (`$SHORTCUTS_HOME/shortcuts.json`) or local project directories (`<project>/.shortcuts/shortcuts.json`).

### Storage Layout

```
<store>/
├── shortcuts.json         # Atomic JSON registry
├── scripts/               # Stored copies of managed executable scripts
│   └── <name>.<ext>
└── logs/                  # Tee execution logs from previous runs
    └── <name>.log
```

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
- `script` *(string)*: Path to script. Relative paths resolve inside `<store>/` and must not escape. Absolute paths reference external files without copying.
- `dir` *(string, optional)*: Fixed absolute directory for static shortcuts.
- `icon` *(string, optional)*: Freedesktop icon name for desktop entries (defaults to `utilities-terminal`).
- `created` *(string, optional)*: ISO date (`YYYY-MM-DD`) when created.
- `created_by` *(string, optional)*: User or agent identifier recorded with `--by`.
- `log` *(boolean, default true)*: When enabled, output streams to the terminal and tees to `logs/<name>.log`.

---

## Command Reference

### `shortcut <name> [args...]` or `shortcut run <name> [args...]`
Execute a shortcut, passing trailing arguments through verbatim:
```bash
shortcut g_search "*.py"
```

### `shortcut new <name> [--desc D] [--static DIR | --local] [--by C] [-- CMD...]`
Create an inline shortcut or open a templated script in nano:
```bash
shortcut new g_hello --desc "Greet user" -- echo "Hello, $1"
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

### `shortcut prefix rm <P_>`
Remove a registered directory prefix:
```bash
shortcut prefix rm C_
```

### `shortcut prefix list`
List all registered directory prefixes:
```bash
shortcut prefix list
```

### `shortcut init [SOURCE] [--global]`
Initialize an empty local store or import shortcuts from a directory or git repository:
```bash
shortcut init
shortcut init https://github.com/example/dev-shortcuts.git --global
```

### `shortcut migrate`
Convert legacy flat `~/.shortcuts/*.sh` scripts to global `g_<stem>` entries:
```bash
shortcut migrate
```

### `shortcut completion bash`
Output the bash completion script:
```bash
shortcut completion bash
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
   shortcut new test --local --desc "Run full integration test suite" -- pytest tests/
   ```
2. **Choose appropriate scope**:
   - Use `g_<name>` for general-purpose tools that should operate in the caller's working directory.
   - Use `--local` for project-specific operations (builds, test runners, linters).
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
rm ~/.local/share/applications/shortcut-*.desktop   # desktop launchers, if any
rm -r ~/.shortcuts                                  # your shortcuts — only if you want them gone
```

## License

MIT — see [LICENSE](LICENSE).
