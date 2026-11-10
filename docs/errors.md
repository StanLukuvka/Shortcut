# Errors

Every error is one line on stderr that starts with `error: `, and shortcut exits 1. The exceptions are in [Exit codes](#exit-codes).

In the tables, `<word>` stands for a value filled in when the error happens. A message is listed as the code writes it; `tests/test_errors.py` fails when a message in `shortcut.py` isn't here.

## Exit codes

| Code | When |
|---|---|
| 0 | Done |
| 1 | Any error below, or no to a question; `Aborted.` is printed. Ctrl-C at a question counts as no |
| 2 | A usage error: an unknown command or option, a bad flag for a command, or the wrong arguments for a shortcut that declares `args` |
| 130 | Ctrl-C while shortcut itself was waiting (Windows runs only) |
| anything else | `run` passes the shortcut's own exit code through, and a signal that kills it |

Usage errors, which exit 2:

| Message | Meaning |
|---|---|
| `unknown command '<word>' (to run a shortcut: shortcut run <word>)` | The first word isn't a command. Shortcuts run with `shortcut r <name>` |
| `unrecognized option '<option>'` | An option before the command that shortcut doesn't have. A second line points to `shortcut --help` |

A flag a command doesn't take, or one missing its value, prints the command's `usage:` line and then `shortcut <command>: error: <message>`.

## Store files

`shortcuts.json`, a project's `.shortcuts` and `.shortcuts.local`, and namespace `<ns>.shortcut` files.

| Message | Meaning |
|---|---|
| `registry not found: <path>` | A namespace file that's imported doesn't exist |
| `corrupt registry <path>: <json error>` | The file isn't valid JSON |
| `corrupt registry <path>: root must be a JSON object` | The file is JSON, but not `{...}` |
| `corrupt registry <path>: 'shortcuts' must be an object` | |
| `corrupt registry <path>: 'description' must be a string` | |
| `cannot overwrite corrupt registry <path>: <json error>` | shortcut won't write over a file it can't read. Fix or move it |
| `<path> is format version <n>, newer than this shortcut reads; upgrade shortcut` | |
| `<path> needs "version": 2` | |
| `<path> needs 'shortcuts'` | |
| `<path> has an unknown field: <fields>` | |
| `<path> has its own 'imports', but namespaces don't nest` | Only a store's main file can import namespaces |
| `<path> can't have 'imports': namespaces belong in .shortcuts` | `.shortcuts.local` can't import |
| `import '<ns>' in <file> must be a path` | |
| `import '<ns>' in <file> must be a .shortcut file` | |
| `import '<ns>' in <file>: a namespace file needs a name, e.g. docker.shortcut` | |
| `<path>: a namespace file needs a name, e.g. docker.shortcut` | A file named just `.shortcut` in `imports/` |
| `namespace '<ns>' is imported twice in <store>` | |
| `'<name>' is both a shortcut and a namespace in <store>; rename one` | |

## Entries

What's wrong with one entry in a store file. Each starts with `'<name>' is not a valid entry: `.

| Message | Meaning |
|---|---|
| `expected a dictionary` | The entry is a string or list, not `{...}` |
| `unknown field: <fields>` | |
| `'description' must be a string` | |
| `'<field>' must be a string` | `inline`, `script`, `cwd`, `icon`, `created_by` or `created` |
| `'<field>' must be a boolean` | `confirm` or `export` |
| `it needs exactly one of 'inline' and 'script'` | |
| `'from' must be an object with a string 'source' and 'name'` | |
| `script not found: <script>` | The entry's script file is missing |
| `'args' must be a list` | |
| `every entry in 'args' needs a 'name' (letters, digits, _ and -)` | |
| `unknown args field: <fields>` | |
| `args '<field>' must be a string` | `default` or `desc` |
| `args '<field>' must be a boolean` | `required` or `rest` |
| `only the last entry in 'args' can be 'rest'` | |
| `arg '<arg>' can't be both 'required' and have a 'default'` | |
| `arg '<arg>' is declared twice` | |

| Message | Meaning |
|---|---|
| `invalid shortcut name '<name>'. Must match ^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$ (alphanumeric start, up to 64 characters of letters, digits, underscores, and dashes).` | |
| `path traversal detected: '<script>' resolves to '<target>', outside '<base>'.` | A `script` path leads out of its store |
| `<path> is a symlink, so this would write somewhere else` | shortcut won't write through a symlink in a store |

## Finding and running

| Message | Meaning |
|---|---|
| `shortcut '<name>' not found.` | Not in this project or your global store |
| `shortcut or namespace '<name>' not found. A global namespace is the file <store>/imports/<name>.shortcut.` | The same, with words after the name, so it may be meant as a namespace |
| `... The project in <dir> has its own '<name>': leave out --global to use it.` | Added to either one above when `--global` skipped a project entry by that name |
| `'<name>' not found in namespace '<ns>'. It has: <names>` | |
| `'<ns>' is a namespace; run one of its shortcuts: <names>` | |
| `too many names: <words>` | `show`, `location` and `rm` take a name, or a namespace and a name |
| `<file error>. Nothing runs inside this project until it's fixed; for your own: shortcut r --global <name>` | The project's file is broken, so no name is looked up through it |
| `<group> isn't approved yet; review it with: <command>` | Run the `shortcut update` command it names |
| `<group> changed since you approved it; run: <command>` | |
| `run directory does not exist: <dir>` | The entry's `cwd` is gone |
| `script file not found: <path>` | |
| `'<name>' asks for confirmation: run it in a terminal, or pass -y (shortcut r -y <name>)` | A `confirm` entry run without a terminal |

A shortcut that declares `args` checks them before it runs. These print `error: <message>`, then `usage: shortcut r <name> <args>`, then each argument and its `desc` if any argument has one, and exit 2.

| Message | Meaning |
|---|---|
| `missing <<arg>>` | A required argument wasn't given |
| `too many arguments` | More words than declared, and no `rest` argument |

## new

| Message | Meaning |
|---|---|
| `command expected after '--'` | |
| `--script and --copy need a file name` | |
| `use one of: '-- CMD', --script, --copy` | |
| `--copy can't be used with --local: .shortcuts.local is one file. Use --script` | |
| `a local shortcut needs a command after '--', or --script PATH` | |
| `script file not found: <path>` | The file given to `--script` or `--copy` |
| `--cwd directory does not exist: <dir>` | |
| `--namespace can't be used with --local: .shortcuts.local is one file, and .shortcuts is yours to edit` | |
| `--arg-desc <flag>: needs NAME=TEXT, with NAME declared by --arg` | |
| `--arg-desc <flag>: '<arg>' already has a desc` | |
| `'<ns>' is a shortcut in store <store>, so it can't be a namespace` | |
| `shortcut '<name>' already exists in <file>` | `new` never replaces an entry. Remove it first |
| `'<name>' is a namespace in store <store>` | |
| `'<name>' is a namespace in <store>` | The same, for `--local` |

A new local shortcut needs a project folder:

| Message | Meaning |
|---|---|
| `<dir> is inside the project at <root>. Run this from there.` | |
| `<dir> holds the global store, so it can't hold local shortcuts. Run this inside a project folder.` | |
| `<dir> is inside the global store, so it can't hold local shortcuts. Run this inside a project folder.` | |
| `<dir>/.shortcuts.local is a symlink, so writing it would go somewhere else.` | |

## show and location

| Message | Meaning |
|---|---|
| ``'<name>' is an inline shortcut, so it has no file. Change it in the file `shortcut show` names.`` | `location` |

## rm

| Message | Meaning |
|---|---|
| `--local and --global can't be used together` | |
| `--local takes one name: namespaces aren't in .shortcuts.local` | |
| `'<name>' isn't in .shortcuts or .shortcuts.local here` | |
| `'<name>' is a project shortcut, and shortcut doesn't change .shortcuts. Edit it by hand, or use: shortcut rm --local <name>` | |
| `<file error>. Inside this project rm only takes --local until it's fixed; for your own: shortcut rm --global <name>` | |
| `remove '<name>' in a terminal, or pass -y` | No terminal to ask in. `rm --local` says `<verb> '<name>' ...` with what it would do |

## promote

| Message | Meaning |
|---|---|
| `promote runs inside a project: there's no .shortcuts or .shortcuts.local here or above` | |
| `'<name>' isn't a project shortcut here` | |
| `'<name>' is marked project-only; promote anyway with --force` | The entry has no `"export": true` |
| `you already have a global '<name>'; give this one another name: shortcut promote <name> --as NEW` | |
| `you already have a global '<new>'` | The name given to `--as` is taken |
| `promote '<name>' in a terminal, or pass -y` | |

## update

These print `error: cannot approve <group>: <reason>`, where the reason is a store file or entry error above.

| Message | Meaning |
|---|---|
| `no project shortcuts here: no .shortcuts or .shortcuts.local in this folder or its parents` | `update .` outside a project |
| `no namespace named '<ns>' here` | |
| `approve <group> in a terminal, or pass -y` | No terminal to show the review in |

## init

| Message | Meaning |
|---|---|
| ``a project's .shortcuts is used in place, never copied: clone the project and run `shortcut update` there. To copy its exported shortcuts into your global store, add --global.`` | `init` without `--global` |
| `a project's .shortcuts is a file you write by hand, starting from: {"version": 2, "shortcuts": {}}. For your own shortcuts, use new --local.` | |
| `failed to clone git repository from <url>: <git error>` | |
| `source directory does not exist: <dir>` | |
| `no .shortcuts found in <dir>` | `init --global` copies only from a source's `.shortcuts` |
| `import <source> in a terminal, or pass -y` | |
