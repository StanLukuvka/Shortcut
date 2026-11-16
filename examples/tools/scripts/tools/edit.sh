#!/usr/bin/env bash
# shortcut r tools edit <name>
# Opens a shortcut's script, or for an inline shortcut the file that defines it.
path="$(shortcut show --field script_path "$@")" || exit 1
[ -n "$path" ] || path="$(shortcut show --field defined_in "$@")" || exit 1
[ -n "$path" ] || exit 1
# Unquoted: VISUAL and EDITOR may include arguments
exec ${VISUAL:-${EDITOR:-nano}} "$path"
