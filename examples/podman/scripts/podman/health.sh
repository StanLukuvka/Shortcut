#!/usr/bin/env bash
# Health check status of each running container.
set -euo pipefail
ids=()
while IFS= read -r id; do ids+=("$id"); done < <(podman ps -q)  # not mapfile: macOS has bash 3.2
if [ ${#ids[@]} -eq 0 ]; then
    echo "No running containers."
    exit 0
fi
podman inspect --format '{{.Name}}{{"\t"}}{{if .State.Health}}{{.State.Health.Status}}{{else}}no health check{{end}}' "${ids[@]}" \
    | sort | column -t -s $'\t'
