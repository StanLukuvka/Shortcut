#!/usr/bin/env bash
# shortcut r edit <name>
# Opens a shortcut's script, or for an inline shortcut the file that defines it.
path="$(shortcut show --json "$@" | python3 -I -c '
import json, sys
info = json.load(sys.stdin)
print(info["script_path"] or info["defined_in"])
')" || exit 1
[ -n "$path" ] || exit 1
# Unquoted: VISUAL and EDITOR may include arguments
exec ${VISUAL:-${EDITOR:-nano}} "$path"
