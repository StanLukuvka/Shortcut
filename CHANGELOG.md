# Changelog

## 1.0.0 - 2026-11-18

- A Claude Code plugin, in `plugin/`, installed with `claude plugin marketplace add StanLukuvka/Shortcut` and `claude plugin install shortcut@shortcut`.
- `new --script` and `new --cwd` no longer check the path first: a missing one shows the system's error, and the path is stored as given otherwise.
- A store or namespace file that can't be read, and an `imports` that isn't a folder, now show the system's error. A folder where a store file should be is no longer read as an empty store.
- A relative `script` path is no longer checked against its store: it is joined to the file's folder and run as it is, links and `..` included.
- A script is run as it is: when the system won't start it, shortcut shows the system's error. `new --script` and `list` no longer check the execute bit.
- `new` takes the command after `--` as one word, in single quotes; more than one word is refused (exit 2) and nothing is written.
- Output is no longer escaped: control characters and bytes that are not UTF-8 are printed as they are.
- Plain messages: a missing script makes `show` fail instead of printing `[file missing: ...]`, `help <unknown command>` is an error (exit 2), and not-found, `rm`, `new --local`, `run` and usage errors no longer add member lists, notes or hints.
- `show --field` with an unknown field no longer lists the valid ones.
- Ctrl-C at a question ends shortcut, as it does anywhere else.

## 0.1.0 - 2026-11-17

- The first release.
