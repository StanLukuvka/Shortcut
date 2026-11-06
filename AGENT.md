# shortcut for agents

`shortcut` names shell commands and scripts, and runs them from any directory. The [README](README.md) is the full reference.

Agents use the same commands as people. Inside a project, work from the project root: `--local` uses `.shortcuts/` in the current directory, and refuses if there isn't one. `shortcut init` creates it. `--cwd .` records the current directory. To set up a shortcut for a user:

1. **Always pass `--desc`.** `--by <your name>` records who made it.

   ```bash
   shortcut new test --local --desc "Run full integration test suite" --cwd . -- 'pytest tests/ "$@"'
   ```

2. **Choose where it lives and runs.** [Stores](README.md#stores) shows which store each command uses.
   - General-purpose tool: use the defaults, the global store and `cwd: caller`.
   - Project-specific work (builds, test runners, linters): use `--local` for the project store, from the project root.
   - A process that must run in a specific directory: use `--cwd <dir>`.

3. **Single-quote the command.** Read arguments with `$1` or `"$@"`. A shortcut doesn't append them. `--arg NAME` declares a required argument and `--arg NAME=DEFAULT` an optional one.

4. **Leave approval to the user.** A shortcut made with `--local` or `--namespace` doesn't run until the user has run `shortcut update`; `new` prints the exact command. Any later change needs `shortcut update` again. Don't approve on their behalf with `-y`.

5. **Tell the user how to run it.**

   > Run it: `shortcut r <name>`

   If it needs approving, give that command first.

6. **Diagnose failures with `shortcut fix`.** See [Look around](README.md#look-around).

Without a terminal:

- `shortcut new <name>` without `-- CMD` skips the editor, creates a script from the template and prints its path after `file:`. Write to that file.
- `shortcut edit` fails. Change the script or the JSON file directly.
- `shortcut rm` needs `-y`.
- A shortcut with `confirm` fails without `shortcut r -y <name>`. Check with the user first.

To see what exists, use `shortcut list` and `shortcut show <name>`. `list` shows an unapproved group only as a line saying how to review it, and `list --names` leaves it out. `show` works before approval. Writes to a store take a lock, and agents working at the same time don't overwrite each other.
