#!/bin/sh
# PreToolUse hook on Bash. Makes Claude Code ask the user before a command that answers one of
# shortcut's own questions for them:
#   - shortcut r or run with -y or --yes before the name, which answers a --confirm shortcut's prompt;
#   - shortcut rm with -y or --yes, which answers the question before a removal, except a removal
#     from the namespace temp, an agent's scratch pad: rm -y temp <name>, or rm --namespace temp -y;
#   - shortcut under script, unbuffer, expect, empty, socat or a Python pty, which give it a
#     terminal to answer in.
# The user answers in Claude Code's permission prompt. Any other command gets no decision, and
# Claude Code's own permission rules apply. It reads the hook's JSON on stdin with sed and grep.

input=$(tr '\n' ' ')
cmd=$(printf '%s' "$input" | sed -n -E 's/.*"command"[[:space:]]*:[[:space:]]*"(([^"\\]|\\.)*)".*/\1/p')
[ -n "$cmd" ] || cmd=$input
# Undo the JSON escapes. A newline separates commands, as ; does.
cmd=$(printf '%s' "$cmd" | sed -e 's/\\n/;/g' -e 's/\\[rt]/ /g' -e 's/\\"/"/g' -e 's/\\\\/\\/g')
# The shell removes quotes too, so 'shortcut' r "-y" runs as shortcut r -y.
cmd=$(printf '%s' "$cmd" | tr -d "'\"")

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
# Do rm's words, after rm, remove only from the namespace temp? Two names, temp and one more, can
# only remove from that namespace: shortcut rm refuses them when it doesn't exist. A redirection is
# skipped. Any other word, --local included, is a name here, so the guard asks.
temp_only() {
	names='' ns='' want='' skip=''
	set -f
	# shellcheck disable=SC2086
	set -- $1
	for w do
		if [ -n "$skip" ]; then skip=''; continue; fi
		if [ -n "$want" ]; then ns=$w want=''; continue; fi
		case $w in
		-y | --yes | --) ;;
		--namespace) want=1 ;;
		--namespace=*) ns=${w#--namespace=} ;;
		[\<\>]* | [0-9]*[\<\>]*) case $w in *[\<\>]) skip=1 ;; esac ;;
		*) names="$names $w" ;;
		esac
	done
	# shellcheck disable=SC2086
	set -- $names
	set +f
	if [ -n "$ns" ]; then
		[ "$ns" = temp ] && [ $# = 0 ]
	else
		[ $# = 2 ] && [ "$1" = temp ]
	fi
}

asks=$(printf '%s' "$cmd" | grep -Eo -- "${sc}rm${args}" | sed -E 's/^[^[:alnum:]_.-]?shortcut[[:space:]]+rm//' |
	while IFS= read -r words; do
		if printf '%s' "$words" | grep -Eq -- "[[:space:]]${yes}" && ! temp_only "$words"; then echo ask; fi
	done)
if [ -n "$asks" ]; then
	ask "shortcut plugin: -y answers shortcut rm's question for you."
fi
if has '(^|[^[:alnum:]_.-])shortcut([^[:alnum:]_./-]|$)' &&
	has '(^|[;&|(`[:space:]])(script|unbuffer|expect|empty|socat)[[:space:]]|pty\.|openpty|forkpty'; then
	ask "shortcut plugin: this gives shortcut a terminal, so its questions could be answered for you."
fi
exit 0
