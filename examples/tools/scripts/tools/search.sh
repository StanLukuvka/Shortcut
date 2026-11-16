#!/usr/bin/env bash
set -euo pipefail

pattern="${1-}"
search_dir="${2:-.}"
if [ -z "$pattern" ]; then
    echo "usage: shortcut r tools search <pattern> [path]" >&2
    exit 1
fi

# a path starting with - is not an option to find
case "$search_dir" in -*) search_dir="./$search_dir" ;; esac
find "$search_dir" -name "*$pattern*" 2>/dev/null
