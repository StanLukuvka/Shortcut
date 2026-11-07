# shortcut for agents

`shortcut` names shell commands and scripts, and runs them from any directory. The [README](README.md) lists the commands.

Agents use the same commands as people. In a project, `--local` writes to `.shortcuts.local` in the project root, and refuses from a subfolder. `.shortcuts` is shared and edited by hand: `shortcut` doesn't write it. Its `entry` field in `shortcut show --json <name>` is what to paste there. `--cwd .` records the current directory. To set up a shortcut for a user:

1. **Always pass `--desc`.** `--by <your name>` records who made it.

   ```bash
   shortcut new test --local --desc "Run full integration test suite" --cwd . -- 'pytest tests/ "$@"'
   ```

2. **Choose where it lives and runs.** See [Where shortcuts live](README.md#where-shortcuts-live).
   - General-purpose tool: use the defaults, the global store and `cwd: caller`.
   - Your own project-specific work: use `--local`, from the project root. For something the whole project shares, propose an entry for `.shortcuts` and let the user commit it.
   - A process that must run in a specific directory: use `--cwd <dir>`.
   - A script that already exists: `--script PATH` uses it where it is, `--copy FILE` copies it into the global store. With `--local`, only `--script`.

3. **Single-quote the command.** Read arguments with `$1` or `"$@"`. A shortcut doesn't append them. `--arg NAME` declares a required argument and `--arg NAME=DEFAULT` an optional one.

4. **Leave approval to the user.** A shortcut made with `--namespace`, or anything in `.shortcuts`, doesn't run until the user has run `shortcut update`; `new` prints the exact command. A change made with `--local` keeps an approved `.shortcuts.local` approved, and in a project that isn't approved yet it needs `shortcut update` too. Don't approve on their behalf with `-y`.

5. **Tell the user how to run it.**

   > Run it: `shortcut r <name>`

   If it needs approving, give that command first.

6. **Diagnose failures with `shortcut show <name>`.** It prints the entry, run directory, usage line and script. `shortcut list` reports a broken file, and warns about invalid entries and missing scripts. `shortcut show --json <name>` prints the entry and where it resolves as JSON, even when the entry is invalid. See [Commands](README.md#commands).

Without a terminal:

- `shortcut new <name>` without `-- CMD` skips the editor, creates a script from the template and prints `Edit it at: <path>`. Write to that file.
- Editors don't open. Change the script or the JSON file directly.
- `shortcut rm` needs `-y`.
- A shortcut with `confirm` fails without `shortcut r -y <name>`. Check with the user first.

To see what exists, use `shortcut list` and `shortcut show <name>`. `list` shows an unapproved group only as a line saying how to review it, and `list --names` leaves it out. `show` works before approval. Writes to a store take a lock, and agents working at the same time don't overwrite each other.
