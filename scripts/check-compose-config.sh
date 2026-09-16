#!/usr/bin/env sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
env_file="$repo_root/.env.example"

if ! command -v docker >/dev/null 2>&1; then
  echo "compose config check failed: docker is required" >&2
  exit 1
fi

for compose_file in \
  docker-compose.yml \
  docker-compose.dev.yml \
  docker-compose.app.yml \
  docker-compose.prod.yml \
  docker-compose.qdrant.yml
do
  docker compose --env-file "$env_file" -f "$repo_root/$compose_file" config --quiet
  echo "compose config passed: $compose_file"
done
