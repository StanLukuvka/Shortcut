#!/usr/bin/env bash
set -euo pipefail

if [ $# -lt 1 ]; then
    echo "Usage: g_search <pattern> [path]" >&2
    exit 1
fi

pattern="$1"
search_dir="${2:-.}"

find "$search_dir" -name "*$pattern*" 2>/dev/null
