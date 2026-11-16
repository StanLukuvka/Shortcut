# Changelog

## Unreleased

- A single binary.
- [examples/tools](examples/tools/) is a namespace to copy: `edit`, `nano`, `logged`, `desktop` and `search`.
- Windows is not supported. Use WSL.
- Run a shortcut with `shortcut run <name>` or `shortcut r <name>`. It runs in the caller's directory unless its `cwd` says otherwise.
- A project keeps its shared shortcuts in a `.shortcuts` file, and your own in `.shortcuts.local`.
- Projects and namespaces run only after `shortcut update`, and again after each change. `update` lists each path that is new, changed or removed, one per line, and asks once; the approval record keeps one SHA-256 per covered file, not its text.
- A change `new` or `rm` makes to `.shortcuts.local` or a namespace needs `shortcut update` too; `new` and `rm` print the command.
- Namespaces: `<ns>.shortcut` files, run as `shortcut r <ns> <name>`.
- `confirm` asks before a shortcut runs, showing its description and the words given.
- `show --field KEY <name>` prints one field, bare, and only reads. The `edit` and `desktop` in examples/tools use it.
- `shortcut help <command>` and `<command> -h` list a command's arguments and options.
- Every error message is listed in [docs/errors.md](docs/errors.md).
- Every store file (`shortcuts.json`, `.shortcuts`, `.shortcuts.local`, namespace `.shortcut` files) needs `"version": 1`. A missing or lower version is an error; a higher one asks you to upgrade shortcut.
- Text that is not valid UTF-8 in a store or project file, or in a file name in an approval tree, is refused with a message that names it. Such bytes print as `\xNN`.
- Options are spelled in full; `-yh` is not `-y -h`. A usage error reads `error: <message>` and a `Try 'shortcut <command> -h'` line, and exits 2.
- `~` and `~/...` are expanded in paths, using $HOME; `~name` is not.
- An empty `--desc` or `--namespace` is an error. A `null` for a text field is an error.
- An executable script with no `#!` is run directly; only an executable text file the kernel refuses is run with bash.
- A shortcut's environment lists `SHORTCUT_NAME`, `SHORTCUT_NAMESPACE`, `SHORTCUT_CALLER_CWD` after the caller's own variables.
- An empty or `.` PATH entry does not find commands in the current directory.
- `update` and `update -y` with no group named skip a broken group with `warning: skipped <group>: <reason>` and exit 0; `update <group>` exits 1 for it.
- `list | head` ends shortcut by SIGPIPE (a shell shows 141); stdout is not buffered.
