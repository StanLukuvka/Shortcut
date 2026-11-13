#!/usr/bin/env bash
set -euo pipefail

# Run as a shortcut, the declared arguments are in SC_ARG_*, where a flag before them can't move them
pattern="${SC_ARG_PATTERN-${1-}}"
search_dir="${SC_ARG_PATH-${2:-.}}"
if [ -z "$pattern" ]; then
    echo "usage: shortcut r search <pattern> [path]" >&2
    exit 1
fi

find "$search_dir" -name "*$pattern*" 2>/dev/null
