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
go test ./internal/cli -run TestName
```

CI also checks `gofmt` and runs `go vet ./...`.

[internal/testkit](internal/testkit/) is the hub. Each test gets its own sandbox from `testkit.New(t)`: a temp dir with `home/` as `HOME` and `home/.shortcuts` as `SHORTCUTS_HOME`. `XDG_DATA_HOME`, `XDG_CONFIG_HOME` and `BIN_DIR` are under the same home. Before any child process starts, `RefuseUnlessSandboxed` checks its environment and working directory, and refuses if they reach outside the temp dir. Tests start processes and make temp dirs only through testkit: `rules_test.go` fails a test file that imports `os/exec`, or calls `t.TempDir`, `t.Setenv`, `os.Setenv` or `os.MkdirTemp`.

## What a change updates

- A new error or warning: a row in [docs/errors.md](docs/errors.md). `internal/cli/errors_doc_test.go` fails without one.
- A new entry field: [docs/shortcut.schema.json](docs/shortcut.schema.json).
- A new command or flag: the bash, zsh and fish scripts in `internal/cli/completion/`, the README command list, `internal/cli/usage.go`, and AGENT.md if agents use it.
- Anything a user would notice: a line under Unreleased in [CHANGELOG.md](CHANGELOG.md).

## What doesn't change

- The standard library only: `go.mod` has no `require`.
- The README's Security model, statements 1–5, word for word.
- Entries are immutable: no edit command. To change one, `rm` it and make it again.
- shortcut never writes a project's `.shortcuts`.
- Text from a file is printed through `textio.Safe`.
- Every question goes through `textio.Confirm`.

## Layout

- `cmd/shortcut/`: the main package.
- `internal/cli/`: dispatch and the commands. `dispatch.go` is the entry, `parsers.go` each command's arguments, `cmd_*.go` one file per command and `store_write.go` what changes the stores; `usage.go` is the usage text, `help/` the per-command help, and `completion/` the bash, zsh and fish scripts.
- `internal/cli/argparse/`: the argument parser. It imports nothing of the CLI.
- `internal/model/`: store files, entries, groups and lookup. It never prints.
- `internal/approval/`: what a project or namespace is approved for, the approval records, and the review diff.
- `internal/textio/`: printed text, questions and the pager.
- `internal/osutil/`, `internal/difflib/`: the parts of the standard library's file and diff behaviour that shortcut's output depends on. `internal/ordjson/`: ordered JSON on encoding/json; it refuses text that is not valid UTF-8.
- `internal/testkit/`: the test sandbox and helpers. `internal/testdata/`: frozen golden data that the tests compare against.
- `installer/`: `install.sh` and its tests.
- `examples/`: namespaces to copy from.
- `docs/`: the diagrams, the schema and the error table.

## Imports

Which package may import which is the table in [internal/testkit/layers_test.go](internal/testkit/layers_test.go). The test fails on an import the table doesn't list and on a package that isn't in it, so a new package or import is a row there.

## Docs and commits

- Docs say what something is and what it does, plainly, without arguing for it.
- Diagrams are edited in `docs/shortcut-overview.drawio` and exported again as the numbered PNGs.
- A commit message is one line that says what the change does, as in `git log`. One change per pull request.

## Agents

[AGENT.md](AGENT.md) is for agents that use shortcut. An agent working on this repo follows this file, and runs anything that writes a store in the separate home above.

## Releasing

1. Set `const Version` in [internal/cli/version.go](internal/cli/version.go) to the version without the `v`, such as `0.1.0`. The release workflow fails if `shortcut --version` doesn't match the tag.
2. Move Unreleased in CHANGELOG.md under the new version and date.
3. Commit, then tag `v<version>` on main and push the tag. The release workflow runs `go vet` and `go test ./...`, builds the archives for linux and darwin on amd64 and arm64, and publishes the GitHub release with `checksums.txt`, `install.sh` and `VERSION`. A tag with a hyphen, such as `v0.1.0-rc.1`, is published as a prerelease.
