#!/usr/bin/env bash
# Numbered list of all containers, sorted by name. Saves the names in that order, so
# `shortcut r podman use N` picks the row you saw. * marks this terminal's pick.
set -euo pipefail
state=${XDG_STATE_HOME:-$HOME/.local/state}/shortcut-podman
# One pick per terminal: its tty and session, so a new terminal starts with none
if t=$(tty 2>/dev/null); then key=${t#/dev/}; key=${key//\//-}.$(ps -o sid= -p $$ | tr -d ' '); else key=shared; fi
mkdir -p "$state"
pick=$(cat "$state/pick.$key" 2>/dev/null || true)

rows=$(podman ps -a --format '{{.Names}}{{"\t"}}{{.Status}}{{"\t"}}{{.Image}}' | sort -t $'\t' -k1,1)
if [ -z "$rows" ]; then
    rm -f "$state/list.$key"
    echo "No containers."
    exit 0
fi
cut -f1 <<<"$rows" > "$state/list.$key"
{
    printf ' \t#\tNAME\tSTATUS\tIMAGE\n'
    n=0
    while IFS=$'\t' read -r name status image; do
        n=$((n + 1))
        mark=' '; [ "$name" = "$pick" ] && mark='*'
        printf '%s\t%d\t%s\t%s\t%s\n' "$mark" "$n" "$name" "$status" "$image"
    done <<<"$rows"
} | column -t -s $'\t'
