# Changelog

## Unreleased

- `shortcut r <name>` and `show <name>` look only in your global store; `shortcut r . <name>` and `show . <name>` look only in the nearest project. A name found only in the other one is an error that gives the command. `--global` and every "hides your global" note and `hides_global` are gone from `run`, `show`, `rm` and `list`; `rm` works on the global store, or with `--local` on `.shortcuts.local`; `list --names` prints global names, and `list --names .` the project's.
- A single binary.
- One lock file, `.lock` in the global store, serialises every write.
- [examples/tools](examples/tools/) is a namespace to copy: `edit`, `nano`, `logged`, `desktop` and `search`.
- Windows is not supported. Use WSL.
- Run a shortcut with `shortcut run <name>` or `shortcut r <name>`. It runs in the caller's directory unless its `cwd` says otherwise.
- A project keeps its shared shortcuts in a `.shortcuts` file, and your own in `.shortcuts.local`.
- Approval and `shortcut update` are gone. A project's shortcuts run with `shortcut r . <name>`; nothing reads `<global store>/approvals/` any more, and the folder can be deleted.
- Namespaces: `<ns>.shortcut` files, run as `shortcut r <ns> <name>`.
- `confirm` asks before a shortcut runs, showing its description and the words given.
- `show --field KEY <name>` prints one field, bare, and only reads. The `edit` and `desktop` in examples/tools use it.
- `shortcut help <command>` and `<command> -h` list a command's arguments and options.
- Every error message is listed in [docs/errors.md](docs/errors.md).
- Every store file (`shortcuts.json`, `.shortcuts`, `.shortcuts.local`, namespace `.shortcut` files) needs `"version": 1`. A missing or lower version is an error; a higher one asks you to upgrade shortcut.
- Text that is not valid UTF-8 is refused in one place only: a store or project file, when it is read. Such bytes print as `\xNN`. A value given on the command line that is not valid UTF-8 is written as U+FFFD.
- Options are spelled in full; `-yh` is not `-y -h`. A usage error reads `error: <message>` and a `Try 'shortcut <command> -h'` line, and exits 2.
- `~` and `~/...` are expanded in paths, using $HOME; `~name` is not.
- An empty `--desc` or `--namespace` is an error. A `null` for a text field is an error.
- A script runs as it is, never through a `#!` line `run` reads itself or through bash. One that is not executable is refused by `run` and `new --script`, and `list` warns about it, each with `chmod +x PATH`.
- A shortcut's environment lists `SHORTCUT_NAME`, `SHORTCUT_NAMESPACE`, `SHORTCUT_CALLER_CWD` after the caller's own variables.
- `bash` is found on the PATH shortcut runs with; one found only through an empty, `.` or relative PATH entry is refused, not passed over.
- `list | head` ends shortcut by SIGPIPE (a shell shows 141); stdout is not buffered.
