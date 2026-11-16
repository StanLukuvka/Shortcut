# Errors

Every error is one line on stderr that starts with `error: `, and shortcut exits 1. The exceptions are in [Exit codes](#exit-codes).

In the tables, `<word>` stands for a value filled in when the error happens. A message is listed as the code writes it; `internal/commands/errors_doc_test.go` fails when a message in the Go code under `internal/` isn't here.

## Exit codes

| Code | When |
|---|---|
| 0 | Done |
| 1 | Any error below, or no to a question; `Aborted.` is printed. Ctrl-C at a question counts as no |
| 2 | A usage error: an unknown command or option, a bad flag for a command |
| 141 | shortcut was ended by SIGPIPE because what it was printing to stopped reading, as in `shortcut list \| head -1`. A shell shows this as 141. Nothing more is printed |
| anything else | `run` passes the shortcut's own exit code through, and a signal that kills it |

Usage errors, which exit 2:

| Message | Meaning |
|---|---|
| `unknown command '<word>' (to run a shortcut: shortcut run <word>)` | The first word isn't a command. Shortcuts run with `shortcut r <name>` |
| `unrecognized option '<option>'` | An option before the command that shortcut doesn't have. A second line points to `shortcut --help` |
| `missing shortcut name` | `shortcut run` with no name. The usage lines follow |

A command's own arguments print `error: <message>` and a second line, `Try 'shortcut <command> -h' for more information.`, and exit 2:

| Message | Meaning |
|---|---|
| `unrecognized option '<option>'` | The command has no such option. Options must be spelled in full, and short flags can't be combined (`-y -h`, not `-yh`) |
| `option '<option>' needs a value` | The option needs a value and the next word is missing or starts with `-`. Write `--desc=-v` for a value that starts with a dash |
| `option '<option>' takes no value` | A flag written as `--flag=value` |
| `missing <name>` | A required argument is left out: `name`, `shell` |
| `unexpected argument '<word>'` | More arguments than the command takes |
| `invalid choice '<word>' (choose from <list>)` | `shortcut completion` takes `bash`, `fish` or `zsh` |

A word that starts with `-` is an option. To pass one as a name or a value, put it after `--`.

## Store files

`shortcuts.json`, a project's `.shortcuts` and `.shortcuts.local`, and namespace `<ns>.shortcut` files.

| Message | Meaning |
|---|---|
| `registry not found: <path>` | A namespace file that's imported doesn't exist |
| `corrupt registry <path>: <json error>` | The file isn't valid JSON. The text after the path is Go's; for a syntax error it ends with `(at byte N)`, the byte where it went wrong |
| `corrupt registry <path>: contains text that is not valid UTF-8 (invalid bytes or a lone surrogate escape)` | A string in the file is not valid UTF-8: a bad byte, or a `\udNNN` escape for a lone surrogate. Shortcut won't guess which name it meant; rename the file and edit the entry |
| `corrupt registry <path>: root must be a JSON object` | The file is JSON, but not `{...}` |
| `corrupt registry <path>: 'shortcuts' must be an object` | |
| `corrupt registry <path>: 'description' must be a string` | |
| `cannot overwrite corrupt registry <path>: <json error>` | shortcut won't write over a file it can't read, including one that isn't valid UTF-8. The text after the path is as in the row above, ending `(at byte N)` for a syntax error. Fix or move it |
| `<path> is format version <n>, newer than this shortcut reads; upgrade shortcut` | |
| `corrupt registry <path>: 'version' must be a whole number, such as 1` | `"version"` is a string, a fraction (`1.0`), `null` or `true` |
| `<path> needs "version": 1` | No `version`, or one below 1, in any store, project or namespace file |
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
| `expected a dictionary` | The entry is a string, list or `null`, not `{...}` |
| `unknown field: <fields>` | |
| `'description' must be a string` | |
| `'<field>' must be a string` | `inline`, `script` or `cwd`. A `null` counts as the wrong type |
| `'confirm' must be a boolean` | |
| `it needs exactly one of 'inline' and 'script'` | |
| `script not found: <script>` | The entry's script file is missing |

| Message | Meaning |
|---|---|
| `invalid shortcut name '<name>'. Must match ^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$ (alphanumeric start, up to 64 characters of letters, digits, underscores, and dashes).` | |
| `path traversal detected: '<script>' resolves to '<target>', outside '<base>'.` | A `script` path leads out of its store |

## Finding and running

| Message | Meaning |
|---|---|
| `shortcut '<name>' not found.` | Not in this project or your global store |
| `shortcut or namespace '<name>' not found. A global namespace is the file <store>/imports/<name>.shortcut.` | The same, with words after the name, so it may be meant as a namespace |
| `... The project in <dir> has its own '<name>': leave out --global to use it.` | Added to either one above when `--global` skipped a project entry by that name |
| `... The project in <outer> has a '<name>', but only the nearest project, <dir>, is used here.` | Added when a project holds another, and only the outer one has the name |
| `'<name>' not found in namespace '<ns>'. It has: <names>` | When the project's namespace hides a global one that has the name, `. Your global '<ns>' has it: shortcut r --global <ns> <name>` follows |
| `'<ns>' is a namespace; run one of its shortcuts: <names>` | |
| `too many names: <words>` | `show` and `rm` take a name, or a namespace and a name |
| `<file error>. Nothing runs inside this project until it's fixed; for your own: shortcut r --global <name>` | The project's file is broken, so no name is looked up through it |
| `<group> isn't approved yet; review it with: <command>` | Run the `shortcut update` command it names |
| `<group> changed since you approved it; run: <command>` | |
| `cannot approve: "<path>" is not valid UTF-8; rename it` | A file name in the project or the namespace's scripts folder, or a project's own folder name, is not valid UTF-8, so it can't be recorded. `run` and `update` stop there until it's renamed |
| `run directory does not exist: <dir>` | The entry's `cwd` is gone |
| `script file not found: <path>` | |
| `<path> has Windows line endings, so its #! line can't start it. Convert it: sed -i 's/\r$//' <path>` | The script's lines end in `\r\n` |
| `'<name>' asks for confirmation: run it in a terminal, or pass -y (shortcut r -y <name> <args>)` | A `confirm` entry run without a terminal. The command is the one that was run, with `-y` |

## new

| Message | Meaning |
|---|---|
| `command expected after '--'` | |
| `--script needs a file name` | |
| `use one of: '-- CMD', --script` | |
| `--desc can't be empty` | `new` |
| `--namespace can't be empty` | `new` |
| `script file not found: <path>` | The file given to `--script` |
| `--cwd directory does not exist: <dir>` | |
| `--namespace can't be used with --local: .shortcuts.local is one file, and .shortcuts is yours to edit` | |
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

## show

| Message | Meaning |
|---|---|
| `unknown field '<key>' (one of: <list>)` | `show --field` takes a top-level key of `show --json`, or `description`, `inline`, `script` |
| `--field and --json can't be used together` | |

## rm

| Message | Meaning |
|---|---|
| `--local and --global can't be used together` | |
| `--local takes one name: namespaces aren't in .shortcuts.local` | |
| `'<name>' isn't in .shortcuts or .shortcuts.local here` | |
| `'<name>' isn't in .shortcuts.local, and shortcut doesn't change .shortcuts` | `rm --local` of a name only in `.shortcuts` |
| `'<name>' is a project shortcut, and shortcut doesn't change .shortcuts. Edit it by hand, or use: shortcut rm --local <name>` | |
| `<file error>. Inside this project rm only takes --local until it's fixed; for your own: shortcut rm --global <name>` | |
| `--namespace takes the namespace alone: shortcut rm --namespace <ns>` | |
| `--namespace takes the namespace alone: shortcut rm --namespace <ns>. To remove one entry: shortcut rm <ns> <name>` | A name was given too |
| `no namespace named '<ns>' in your global store (<store>/imports)` | `rm --namespace` removes only global namespaces |
| `remove namespace '<ns>' in a terminal, or pass -y` | |
| `remove '<name>' in a terminal, or pass -y` | No terminal to ask in. `rm --local` says `<verb> '<name>' ...` with what it would do |

## update

`update <group>` and `update .` print `error: cannot approve <group>: <reason>` and exit 1, where the reason is a store file or entry error above.

| Message | Meaning |
|---|---|
| `warning: skipped <group>: <reason>` | `update` or `update -y` with no group named found a group whose file or entries have errors. It is skipped and the exit status stays 0 |
| `no project shortcuts here: no .shortcuts or .shortcuts.local in this folder or its parents` | `update .` outside a project |
| `no namespace named '<ns>' here` | |
| `approve <group> in a terminal, or pass -y` | No terminal to show the review in |
