#!/usr/bin/env bash
# shortcut r logged <name> [args...]
# Runs a shortcut through tee. The log holds stdout and stderr together, and each run replaces it.
dir="${XDG_STATE_HOME:-$HOME/.local/state}/shortcut"
mkdir -p "$dir"
log="$dir/${1//\//_}.log"
shortcut run "$@" 2>&1 | tee "$log"
exit "${PIPESTATUS[0]}"
