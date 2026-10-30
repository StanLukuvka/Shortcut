#!/usr/bin/env bash
set -euo pipefail

port="${1:-8000}"
echo "Serving HTTP on port $port (cwd: $(pwd))..."
exec python3 -m http.server "$port"
