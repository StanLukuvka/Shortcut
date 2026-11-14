# Changelog

## Unreleased

- Diffs split lines on `\n` only, so a `\r`, form feed or U+2028 stays inside its line. When a very large diff has few matching lines, the part not yet matched is shown as replaced.
- A single binary, no Python needed.
- `init --global --bundled` installs the bundled shortcuts.
- Windows is not supported. Use WSL.
- Run a shortcut with `shortcut run <name>` or `shortcut r <name>`.
- A shortcut runs in the caller's directory unless its `cwd` says otherwise.
- A project keeps its shared shortcuts in a `.shortcuts` file, and your own in `.shortcuts.local`.
- Projects and namespaces run only after `shortcut update`, and again after each change.
- Namespaces: `<ns>.shortcut` files, run as `shortcut r <ns> <name>`.
- Declared arguments, `confirm`, `export`, `promote`, `init --global` from a folder or a git URL.
- Every error message is listed in [docs/errors.md](docs/errors.md).
- The store format restarts at `"version": 1`. A file written by the Python version (`"version": 2`) needs that one number edited by hand; shortcut does not migrate it.
- Non-ASCII text in stores and approval records is written as the characters, not `\uXXXX`. A `.shortcuts` file with non-ASCII text asks for approval once more.
- A store, project file or approval tree with text that is not valid UTF-8 is refused, with a message that names it.
- Options must be spelled in full: `--des` is no longer short for `--desc`, and `-yh` is no longer `-y -h`. A word such as `-1` is an option, not a name.
- A command's usage errors read `error: <message>` and a `Try 'shortcut <command> -h'` line, and exit 2. `shortcut completion` needs the shell named.
- `--help=x` is an unknown option. `-h` after an unknown option reports the unknown option (exit 2) instead of printing help, as in `show --bogus -h`.
- An empty or `.` PATH entry no longer finds commands in the current directory.
- Bytes that are not valid UTF-8 print as `\xNN` instead of `\udcNN`.
- `$PAGER` is split on blanks; quotes in it are not interpreted.
- A failed `git clone` in `init --global` reports `git clone exited with status N`.
- Unreadable-file errors use Go's wording, such as `open /p: permission denied`.
- `list | head` ends shortcut by SIGPIPE (a shell shows 141). stdout is no longer buffered, so stdout and stderr keep their order.
- An executable script with no `#!` is run directly, so a Mach-O or other binary works. Only an executable text file the kernel refuses is run with bash.
- A shortcut's environment lists `SHORTCUT_NAME`, `SHORTCUT_NAMESPACE`, `SHORTCUT_CALLER_CWD` and its `SC_ARG_` variables after the caller's own variables.
- The pager shows the diff once: if it ran, shortcut does not print the diff again, whatever the pager's exit status. `$LINES` is no longer read; the row count comes from the terminal (24 if unknown).
- `update` and `update -y` with no group named skip a broken group with `warning: skipped <group>: <reason>` and exit 0. `update <group>` still exits 1 for it.
- `show --field KEY <name>` prints one field, bare, and only reads. The bundled `edit` and `desktop` use it, so they no longer need python3.
- `show` prints `Confirm:     yes` and `Export:      yes` where it printed `True`.
- An empty `--desc`, `--namespace` or `--as` is an error. Before, it counted as not given.
- `new --copy` gives the copy the source's permission bits plus execute, without setuid, setgid or sticky, and does not copy the modification time. `new --copy -` takes stdin bytes as they are, valid UTF-8 or not.
