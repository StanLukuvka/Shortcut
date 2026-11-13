# Working on shortcut

## Setup

Python 3.10+, bash and git. Nothing to install:

```bash
git clone https://github.com/StanLukuvka/Shortcut.git && cd Shortcut
python3 src --help
```

`python3 src <command>` runs the code in the clone.

To try it by hand without touching your own `~/.shortcuts`, give it a home of its own. Set `HOME` first and `SHORTCUTS_HOME` in a separate statement, so `$HOME` is already the new one:

```bash
HOME=$(mktemp -d)
SHORTCUTS_HOME=$HOME/.shortcuts
export HOME SHORTCUTS_HOME
```

## Tests

```bash
python3 -m unittest discover -s tests -t tests
```

One file, or the tests whose names match:

```bash
python3 -m unittest discover -s tests -t tests -p test_import.py
```

```bash
python3 -m unittest discover -s tests -t tests -k test_confirm
```

[tests/fixtures.py](tests/fixtures.py) is the hub: one sandbox and the fixtures every test builds from. Its docstring shows how. Each `test_*.py` covers one behaviour, and `tests/test_fixtures.py` checks that each one:

- subclasses `ShortcutTestCase`;
- makes no temp dirs, doesn't change `os.environ` and doesn't import `subprocess`.

Add a fixture to `fixtures.py` once a second test file needs it.

CI also runs:

```bash
ruff check --select F,E9 .
```

```bash
mypy --python-version 3.10 src/shortcut
```

## What a change updates

- A new error or warning: a row in [docs/errors.md](docs/errors.md). `tests/test_errors.py` fails without one.
- A new entry field: [docs/shortcut.schema.json](docs/shortcut.schema.json).
- A new command or flag: the bash, zsh and fish scripts in `completion.py`, the README command list, `print_usage` in `cli.py`, and AGENT.md if agents use it.
- Anything a user would notice: a line under Unreleased in [CHANGELOG.md](CHANGELOG.md).

## What doesn't change

- The standard library only.
- The README's Security model, statements 1–6, word for word.
- Entries are immutable: no edit command. To change one, `rm` it and make it again.
- shortcut never writes a project's `.shortcuts`.
- Text from a file is printed through `safe()` or `SafeOutput`.
- Every question goes through `Confirm`.

## Layout

- `src/__main__.py` checks the Python version and starts `shortcut.cli`.
- `src/shortcut/`:
  - `cli.py`: the commands.
  - A file per class: `shortcut_entry.py`, `group.py`, `store.py`, `resolved_shortcut.py`, `parser.py`, `confirm.py`, `safe_output.py`.
  - A file per area: `stores.py`, `lookup.py`, `run.py`, `writes.py`, `approval.py`, `importing.py`, `completion.py`.
  - `helpers.py`: constants and the functions that need none of the classes. Its docstring maps it.
- `templates/`: the bundled shortcuts. `examples/`: namespaces to copy from.
- `docs/`: the diagrams, the schema and the error table.

## Docs and commits

- Docs say what something is and what it does, plainly, without arguing for it.
- Diagrams are edited in `docs/shortcut-overview.drawio` and exported again as the numbered PNGs.
- A commit message is one line that says what the change does, as in `git log`. One change per pull request.

## Agents

[AGENT.md](AGENT.md) is for agents that use shortcut. An agent working on this repo follows this file, and runs anything that writes a store in the separate home above.

## Releasing

1. Set `VERSION` in `src/shortcut/helpers.py`, and the version in the diagram titles if it changed there.
2. Move Unreleased in CHANGELOG.md under the new version and date.
3. Tag `v<version>` on main and publish a GitHub release from it. The release workflow attaches the zipapp.
