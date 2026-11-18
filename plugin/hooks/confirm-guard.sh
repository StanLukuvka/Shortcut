#!/bin/sh
# PreToolUse hook on Bash. Makes Claude Code ask the user before a command that answers one of
# shortcut's own questions for them:
#   - shortcut r or run with -y or --yes before the name, which answers a --confirm shortcut's prompt;
#   - shortcut rm with -y or --yes, which answers the question before a removal;
#   - shortcut under script, unbuffer, expect, empty, socat or a Python pty, which give it a
#     terminal to answer in.
# The user answers in Claude Code's permission prompt. Any other command gets no decision, and
# Claude Code's own permission rules apply. It reads the hook's JSON on stdin with sed and grep.

input=$(tr '\n' ' ')
cmd=$(printf '%s' "$input" | sed -n -E 's/.*"command"[[:space:]]*:[[:space:]]*"(([^"\\]|\\.)*)".*/\1/p')
[ -n "$cmd" ] || cmd=$input
# Undo the JSON escapes. A newline separates commands, as ; does.
cmd=$(printf '%s' "$cmd" | sed -e 's/\\n/;/g' -e 's/\\[rt]/ /g' -e 's/\\"/"/g' -e 's/\\\\/\\/g')

has() { printf '%s' "$cmd" | grep -Eq -- "$1"; }

ask() {
	printf '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask","permissionDecisionReason":"%s"}}\n' "$1"
	exit 0
}

sc='(^|[^[:alnum:]_.-])shortcut[[:space:]]+'
args='[^;&|()]*'
yes='(-y|--yes)([[:space:]]|$)'

if has "${sc}(r|run)[[:space:]]+${yes}"; then
	ask "shortcut plugin: -y answers this shortcut's confirm prompt for you."
fi
if has "${sc}rm${args}[[:space:]]${yes}"; then
	ask "shortcut plugin: -y answers shortcut rm's question for you."
fi
if has '(^|[^[:alnum:]_.-])shortcut([^[:alnum:]_./-]|$)' &&
	has '(^|[;&|(`[:space:]])(script|unbuffer|expect|empty|socat)[[:space:]]|pty\.|openpty|forkpty'; then
	ask "shortcut plugin: this gives shortcut a terminal, so its questions could be answered for you."
fi
exit 0
