# Working on shortcut

## Setup

Go (the version in [go.mod](go.mod)), bash and git. Nothing else to install:

```bash
git clone https://github.com/StanLukuvka/Shortcut.git && cd Shortcut
go build -o bin/shortcut ./cmd/shortcut
bin/shortcut --help
```

To try it by hand without touching your own `~/.shortcuts`, give it a home of its own. Set `HOME` first and `SHORTCUTS_HOME` in a separate statement, so `$HOME` is already the new one:

```bash
HOME=$(mktemp -d)
SHORTCUTS_HOME=$HOME/.shortcuts
export HOME SHORTCUTS_HOME
```

## Tests

```bash
go test ./...
```

One package, or the tests whose names match:

```bash
go test ./internal/commands -run TestName
```

CI also checks `gofmt` and runs `go vet ./...`.

[internal/testkit](internal/testkit/) is the hub. Each test gets its own sandbox from `testkit.New(t)`: a temp dir with `home/` as `HOME` and `home/.shortcuts` as `SHORTCUTS_HOME`. `XDG_DATA_HOME`, `XDG_CONFIG_HOME` and `BIN_DIR` are under the same home. Before any child process starts, `RefuseUnlessSandboxed` checks its environment and working directory, and refuses if they reach outside the temp dir. Tests start processes and make temp dirs only through testkit: `rules_test.go` fails a test file that imports `os/exec`, or calls `t.TempDir`, `t.Setenv`, `os.Setenv` or `os.MkdirTemp`.

## What a change updates

- A new error or warning: a row in [docs/errors.md](docs/errors.md). `internal/commands/errors_doc_test.go` fails without one.
- A new entry field: a row in `Fields()` in `internal/store/entry.go`, and [docs/shortcut.schema.json](docs/shortcut.schema.json) (a test fails if they differ).
- A new command or flag: its row in `commands()` in `internal/commands/commands.go` (dispatch, parsing, usage and help follow from it), the three completion scripts in `internal/commands/completion/` (a test fails if they disagree with `commands()`), and the README command list.
- Anything a user would notice: a line under Unreleased in [CHANGELOG.md](CHANGELOG.md).

## How it fails

shortcut does what it was told, or stops with an error. It does not guess,
repair, or carry on.

- A wrong input is an error that names the thing and, where there is one,
  the command that fixes it. Then shortcut exits.
- No fallbacks. A file that is missing, unreadable, not executable or not
  what was expected is refused. shortcut does not try something else in its
  place.
- No convenience that acts without being asked. If saving the user one
  command means doing something they did not type, they type the command.
- Every branch exists because of something the README promises. A branch
  for a case nobody has had is removed, not kept in case.
- Before adding a branch that handles a case, ask whether refusing it would
  do. It usually would.
- An error is not dropped. Where one is ignored on purpose, the same line
  says why.
- A change that adds a case says which sentence of the README needs it. A
  change that removes more than it adds needs no such reason.

## What doesn't change

- The standard library only: `go.mod` has no `require`.
- The README's Security model, statements 1–4, word for word.
- Entries are immutable: no edit command. To change one, `rm` it and make it again.
- shortcut never writes a project's `.shortcuts`.
- Every question goes through `terminal.Confirm`.
- The file format is `"version": 1`. Adding, removing or changing the meaning of any field, in an entry or at a
  file's top level, bumps `"version"`; a shortcut that reads an older version keeps reading it, and one that meets a
  newer version refuses it with the existing "upgrade shortcut" error. `internal/store/testdata/format-v1.txt` is the
  frozen list, and a test fails when the fields differ from it.

## Layout

- `cmd/shortcut/`: the main package.
- `internal/commands/`: `commands.go` is the command table, from which `dispatch.go`, each command's parser and the usage and help text (`help.go`, `terminal`) are read. Each command is a package below it: `run/`, `new/`, `list/`, `show/`, `remove/` and `completion/` (which holds the bash, zsh and fish scripts). No command imports another command.
- `internal/store/`: store files, entries, groups, lookup and the run's context. It never prints. `write/` is everything that changes a store: adding an entry and the registry lock.
- `internal/terminal/`: printed text and questions, the exit error and `Die` that commands share, and the description of a command that its parser, usage line and help are read from. `argparse/` is the argument parser; it imports nothing of this repository.
- `internal/platform/`: the parts of the standard library's file, process, lock and JSON behaviour that shortcut's output depends on, including JSON read as `map[string]json.RawMessage` and written with sorted keys. A file that is not valid UTF-8 is refused when it is read. It imports nothing of this repository.
- `internal/testkit/`: the test sandbox and helpers. A package's frozen golden data is in its own `testdata/` folder.
- `installer/`: `install.sh` and its tests.
- `examples/`: namespaces to copy from.
- `docs/`: the schema and the error table.

## Imports

Imports point down this tree and never up: `cmd/shortcut`, `internal/commands`, one command, `internal/store/write`, `internal/store`, `internal/terminal`, `internal/platform`. A command may import `internal/store`, `internal/store/write`, `internal/terminal`, `internal/terminal/argparse` and `internal/platform`; it imports no other command and not `internal/commands`. `internal/store` imports only `internal/platform`; `internal/store/write` also imports `internal/terminal`. `internal/platform` imports nothing of this repository. The table is in [internal/testkit/layers_test.go](internal/testkit/layers_test.go). The test fails on an import the table doesn't list and on a package that isn't in it, so a new package or import is a row there.

## Docs and commits

- Docs say what something is and what it does, plainly, without arguing for it.
- A commit message is one line that says what the change does, as in `git log`. One change per pull request.

## Agents

An agent working on this repo follows this file, and runs anything that writes a store in the separate home above.

The usual mistake is adding a handler where an error would do; see [How it fails](#how-it-fails).

## Releasing

1. Set `const Version` in [internal/commands/version.go](internal/commands/version.go) to the version without the `v`, such as `0.1.0`. The release workflow fails if `shortcut --version` doesn't match the tag.
2. Move Unreleased in CHANGELOG.md under the new version and date.
3. Commit, then tag `v<version>` on main and push the tag. The release workflow runs `go vet` and `go test ./...`, builds the archives for linux and darwin on amd64 and arm64, and publishes the GitHub release with `checksums.txt`, `install.sh` and `VERSION`. A tag with a hyphen, such as `v0.1.0-rc.1`, is published as a prerelease.
