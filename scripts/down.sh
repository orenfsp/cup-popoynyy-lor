#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PROJECT_DIR=$(dirname -- "$SCRIPT_DIR")

docker compose \
  --project-directory "$PROJECT_DIR" \
  -f "$PROJECT_DIR/docker-compose.yml" \
  down --remove-orphans "$@"

printf '%s\n' \
  "Молва остановлена." \
  "Уже загруженная страница останется на экране до обновления вкладки."
