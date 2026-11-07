#!/usr/bin/env bash
# shortcut r edit <name>
# Opens a shortcut's script, or for an inline shortcut the file that defines it.
# This reads show's label, so renaming "Defined in" there breaks it.
path="$(shortcut location "$@" 2>/dev/null)" || path="$(shortcut show "$@" | sed -n 's/^Defined in: *//p')"
[ -n "$path" ] || exit 1
# Unquoted: VISUAL and EDITOR may include arguments
exec ${VISUAL:-${EDITOR:-nano}} "$path"
