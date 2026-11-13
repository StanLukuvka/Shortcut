# Changelog

## Unreleased

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
