#!/usr/bin/env bash
# Pick a container for this terminal. logs, sh, restart, ports, exec and stop act on it
# when you don't name one. No argument: show the pick. none: clear it.
set -euo pipefail
state=${XDG_STATE_HOME:-$HOME/.local/state}/shortcut-podman
if t=$(tty 2>/dev/null); then key=${t#/dev/}; key=${key//\//-}.$(ps -o sid= -p $$ | tr -d ' '); else key=shared; fi
pick_file=$state/pick.$key

case ${SC_ARG_TARGET:-} in
    "")
        [ -f "$pick_file" ] || { echo "Nothing picked in this terminal. Pick one with: shortcut r podman use N"; exit 0; }
        SHORTCUT_SHOW_DIR=0 exec shortcut r podman current ;;
    none)
        rm -f "$pick_file"
        echo "Cleared the pick for this terminal."
        exit 0 ;;
esac

name=$(SHORTCUT_SHOW_DIR=0 shortcut r podman current "$SC_ARG_TARGET")
mkdir -p "$state"
# Drop picks and lists of terminal sessions that have closed
for f in "$state"/pick.* "$state"/list.*; do
    sid=${f##*.}
    if [[ $sid =~ ^[0-9]+$ ]] && ! kill -0 "$sid" 2>/dev/null; then rm -f "$f"; fi
done
printf '%s\n' "$name" > "$pick_file"
echo "Using $name in this terminal."
