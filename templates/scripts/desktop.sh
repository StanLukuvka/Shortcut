#!/usr/bin/env bash
set -euo pipefail

name="$1"

# A launcher runs outside any project, so it needs a global shortcut; show fails if there is none.
# This reads show's labels, so renaming Description or Icon there breaks it.
info="$(shortcut show --global "$name")"
info="${info%%$'\n\n'*}"  # the details, not the script below them
field() { sed -n "s/^$1: *//p" <<<"$info"; }

# Desktop sessions often lack ~/.local/bin in PATH, so the launcher uses the absolute path.
tool="$(printf '%q' "$(command -v shortcut)")"
inner="$tool run --global $name; rc=\$?; if command -v notify-send >/dev/null 2>&1; then if [ \$rc -eq 0 ]; then notify-send \"shortcut: $name\" \"Finished\"; else notify-send -u critical \"shortcut: $name\" \"Failed (exit \$rc)\"; fi; fi; echo; read -r -p \"Press Enter to close...\""
# Exec= escaping from the Desktop Entry spec: backslash-escape inside the double quotes, then double every %.
exec_line="$(sed -e 's/[\\"`$]/\\&/g' -e 's/%/%%/g' <<<"$inner")"

apps_dir="${XDG_DATA_HOME:-$HOME/.local/share}/applications"
mkdir -p "$apps_dir"
desktop_file="$apps_dir/shortcut-$name.desktop"
cat > "$desktop_file" <<DESKTOP
[Desktop Entry]
Version=1.0
Type=Application
Name=$(field Description)
Exec=bash -c "$exec_line"
Icon=$(field Icon | grep . || echo utilities-terminal)
Terminal=true
Categories=Utility;
DESKTOP

echo "Created desktop entry: $desktop_file"
