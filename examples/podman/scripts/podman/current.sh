#!/usr/bin/env bash
# Print the container a number (a row of the last list), a name, or this terminal's pick (nothing or .) refers to.
# On a terminal it prints the container's row as list shows it; captured, as in $(...), just the name.
set -euo pipefail
state=${XDG_STATE_HOME:-$HOME/.local/state}/shortcut-podman
if t=$(tty 2>/dev/null); then key=${t#/dev/}; key=${key//\//-}.$(ps -o sid= -p $$ | tr -d ' '); else key=shared; fi
target=${SC_ARG_TARGET:-}
[ "$target" = . ] && target=  # . is the pick, for commands that take more after it

if [ -z "$target" ]; then
    target=$(cat "$state/pick.$key" 2>/dev/null) || {
        echo "error: no container picked in this terminal: name one, or pick one with: shortcut r podman use N" >&2
        exit 1
    }
elif [[ $target =~ ^[0-9]+$ ]]; then
    [ -f "$state/list.$key" ] || {
        echo "error: no list to count from in this terminal: run shortcut r podman list first" >&2
        exit 1
    }
    count=$(wc -l < "$state/list.$key")
    if (( target < 1 || target > count )); then
        echo "error: the last list has containers 1 to $count, not $target" >&2
        exit 1
    fi
    target=$(sed -n "${target}p" "$state/list.$key")
fi

podman container exists "$target" || { echo "error: no container named '$target'" >&2; exit 1; }
if [ ! -t 1 ]; then
    printf '%s\n' "$target"
    exit 0
fi
# Its number in the last list, if it's there, and * if it's this terminal's pick
n=$(grep -nxF -- "$target" "$state/list.$key" 2>/dev/null | cut -d: -f1 || true)
mark=' '; [ "$target" = "$(cat "$state/pick.$key" 2>/dev/null || true)" ] && mark='*'
podman ps -a --format '{{.Names}}{{"\t"}}{{.Status}}{{"\t"}}{{.Image}}' |
    awk -F'\t' -v name="$target" -v n="${n:--}" -v mark="$mark" -v OFS='\t' '$1 == name { print mark, n, $0 }' |
    column -t -s $'\t'
