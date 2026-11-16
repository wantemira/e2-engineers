#!/bin/sh
set -eu

if ! command -v docker >/dev/null 2>&1; then
    echo 'Docker is not installed or not on PATH. Install Docker and run make up again.' >&2
    exit 1
fi
if ! engine_type=$(docker info --format '{{.OSType}}' 2>/dev/null); then
    echo 'Docker Engine is unavailable. Start Docker Desktop or your Docker service, then run make up again.' >&2
    exit 1
fi
if [ "$engine_type" != linux ]; then
    echo 'AI-Buddy requires a Linux Docker Engine. Select Linux containers and run make up again.' >&2
    exit 1
fi
if ! docker compose version --short >/dev/null 2>&1; then
    echo 'Docker Compose v2 or newer is required.' >&2
    exit 1
fi
echo 'Docker Engine and Compose are ready (Linux containers).'
