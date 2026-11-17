# Changelog

## Unreleased

- Output is no longer escaped: control characters and bytes that are not UTF-8 are printed as they are.
- Plain messages: a missing script makes `show` fail instead of printing `[file missing: ...]`, `help <unknown command>` is an error (exit 2), and not-found, `rm`, `new --local`, `run` and usage errors no longer add member lists, notes or hints.
- `show --field` with an unknown field no longer lists the valid ones.
- Ctrl-C at a question ends shortcut, as it does anywhere else.

## 0.1.0 - 2026-11-17

- The first release.
