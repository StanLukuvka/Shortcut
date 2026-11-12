#!/usr/bin/env bash
set -euo pipefail

remove=0
if [ "${1-}" = "--remove" ]; then remove=1; shift; fi
name="${1-}"
case "$name" in ""|*/*) echo "usage: shortcut r desktop [--remove] <shortcut>" >&2; exit 2;; esac

apps_dir="${XDG_DATA_HOME:-$HOME/.local/share}/applications"
desktop_file="$apps_dir/shortcut-$name.desktop"
marker="X-Shortcut-Name=$name"

if [ "$remove" = 1 ]; then
    # Only a launcher this script made: it carries the marker line.
    if [ -f "$desktop_file" ] && grep -qxF "$marker" "$desktop_file"; then
        rm -f -- "$desktop_file"
        echo "Removed desktop entry: $desktop_file"
        exit 0
    fi
    echo "error: no desktop entry made by shortcut at $desktop_file" >&2
    exit 1
fi

# A launcher runs outside any project, so it needs a global shortcut; show fails if there is none.
info="$(shortcut show --global --json "$name")"
field() {
    python3 -I -c '
import json, sys
value = json.loads(sys.argv[2])["entry"].get(sys.argv[1]) or ""
print(" ".join(str(value).split()))
' "$1" "$info"
}

# Desktop sessions often lack ~/.local/bin in PATH, so the launcher uses the absolute path.
tool="$(printf '%q' "$(command -v shortcut)")"
inner="$tool run --global $name; rc=\$?; if command -v notify-send >/dev/null 2>&1; then if [ \$rc -eq 0 ]; then notify-send \"shortcut: $name\" \"Finished\"; else notify-send -u critical \"shortcut: $name\" \"Failed (exit \$rc)\"; fi; fi; echo; read -r -p \"Press Enter to close...\""
# Exec= escaping from the Desktop Entry spec: backslash-escape inside the double quotes, then double every %.
exec_line="$(sed -e 's/[\\"`$]/\\&/g' -e 's/%/%%/g' <<<"$inner")"

mkdir -p "$apps_dir"
cat > "$desktop_file" <<DESKTOP
[Desktop Entry]
Version=1.0
Type=Application
Name=$(field description | grep . || echo "$name")
Exec=bash -c "$exec_line"
Icon=$(field icon | grep . || echo utilities-terminal)
Terminal=true
Categories=Utility;
$marker
DESKTOP

echo "Created desktop entry: $desktop_file"
