# shortcut plugin for Claude Code

A Claude Code plugin for the [`shortcut`](https://github.com/StanLukuvka/Shortcut) CLI. Claude starts each session knowing the project's shortcuts, runs them with `shortcut r .`, and saves commands as new ones, in the project's `.shortcuts.local` by default. Claude Code asks you first when a command's text runs `shortcut r` or `shortcut rm` with `-y`. A command run through a variable, an alias, a wrapper script or `eval` can still get past it.

It needs `shortcut` on your `PATH`, and `sh`, `sed`, `grep`, `tr` and `cut` for the hooks.

## What's in it

| Piece | Path | What it does |
|---|---|---|
| `shortcut:use` skill | `skills/use/SKILL.md` | How to find, run and save shortcuts: `list`, `show`, `r` and `r .`, `new --local`, `--desc`, `--script`, `--confirm`. Claude uses it when a request is about shortcuts, or you invoke it as `/shortcut:use`. |
| Project list | `hooks/project-shortcuts.sh` | A SessionStart hook. In a project, it adds the project's shortcut names and descriptions to Claude's context, up to 40, and says when `shortcut list` has warnings about the project's files. Outside a project it adds nothing. Global shortcuts aren't listed; Claude runs `shortcut list` for those. |
| Confirm guard | `hooks/confirm-guard.sh` | A PreToolUse hook on Bash. It matches the command's text, quotes removed, and makes Claude Code ask you before the commands below. |
| Hook tests | `tests/hooks-test.sh` | Commands the guard should send to you or leave alone, and what the project list prints. |

The project list runs when a session starts, after `/clear` and after compaction. It doesn't run again after a `cd`.

Descriptions are the repository's text. The list labels them as data and turns control characters into spaces, but a repository can still write anything into them, as it can into its CLAUDE.md.

## What Claude Code asks you about

The guard returns "ask" for:

- `shortcut r -y` or `--yes` before the name, which answers a `--confirm` shortcut's prompt;
- `shortcut rm` with `-y` or `--yes`, which answers the question before a removal;
- shortcut under `script`, `unbuffer`, `expect`, `empty`, `socat` or a Python pty, which give it a terminal to answer in.

Claude Code then shows its permission prompt, and you allow or deny the command. Auto mode sends it to the prompt too. `-y` after the name goes to the shortcut, and the guard leaves it alone. Every other command gets no decision from the guard, and your permission rules apply as usual.

The guard is a best-effort match on the command's text. It doesn't catch a command run through a variable, a shell alias, a wrapper script or `eval`, or a script that runs `shortcut rm -y`. It never blocks a command: it asks, or it gives no decision. The skill tells Claude the same rule.

## Install

```bash
claude plugin marketplace add StanLukuvka/Shortcut
```

```bash
claude plugin install shortcut@shortcut
```

Then start a new session. To get a newer version later:

```bash
claude plugin marketplace update shortcut
```

```bash
claude plugin update shortcut@shortcut
```

## Working on it

To load it from a clone without installing it, from the repository root:

```bash
claude --plugin-dir plugin
```

In a project with shortcuts, ask Claude what shortcuts it knows about. `/plugin` shows the plugin and its hooks. A change to the plugin raises `version` in `.claude-plugin/plugin.json`, so installed copies update.

To check the marketplace and the plugin:

```bash
claude plugin validate --strict .
```

```bash
claude plugin validate --strict plugin
```

The hook tests use the `shortcut` on your `PATH`, in a temp HOME of their own:

```bash
sh plugin/tests/hooks-test.sh
```
