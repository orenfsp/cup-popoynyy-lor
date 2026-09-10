#!/bin/sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PROJECT_DIR=$(dirname -- "$SCRIPT_DIR")
BUILD_DIR=$(mktemp -d "${TMPDIR:-/tmp}/molva-build.XXXXXX")

cleanup() {
  rm -rf -- "$BUILD_DIR"
}
trap cleanup EXIT HUP INT TERM

cp -R \
  "$PROJECT_DIR/backend" \
  "$PROJECT_DIR/frontend" \
  "$PROJECT_DIR/go.mod" \
  "$PROJECT_DIR/go.sum" \
  "$PROJECT_DIR/.dockerignore" \
  "$BUILD_DIR/"

docker build \
  -f "$BUILD_DIR/backend/Dockerfile" \
  --build-arg TARGET=otklik \
  -t molva-api:latest \
  "$BUILD_DIR"

docker build \
  -f "$BUILD_DIR/backend/Dockerfile" \
  --build-arg TARGET=worker \
  -t molva-worker:latest \
  "$BUILD_DIR"

docker build \
  -f "$BUILD_DIR/frontend/Dockerfile" \
  -t molva-frontend:latest \
  "$BUILD_DIR"

docker build \
  -f "$BUILD_DIR/frontend/Dockerfile.staff" \
  -t molva-staff-frontend:latest \
  "$BUILD_DIR"

docker compose \
  --project-directory "$PROJECT_DIR" \
  -f "$PROJECT_DIR/docker-compose.yml" \
  up -d --no-build
