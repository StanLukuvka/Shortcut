#!/usr/bin/env bash
set -euo pipefail

if command -v ss >/dev/null 2>&1; then
    ss -tulwn
elif command -v netstat >/dev/null 2>&1; then
    netstat -tuln
elif command -v lsof >/dev/null 2>&1; then
    lsof -iTCP -sTCP:LISTEN -P -n
else
    echo "Error: neither ss, netstat, nor lsof found" >&2
    exit 1
fi
