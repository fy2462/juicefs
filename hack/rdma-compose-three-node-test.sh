#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="$ROOT_DIR/hack/rdma-compose/docker-compose.yml"
PROJECT_NAME="${JFS_RDMA_COMPOSE_PROJECT:-jfs-rdma-three-node-$$}"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

compose() {
  if docker compose version >/dev/null 2>&1; then
    docker compose -p "$PROJECT_NAME" -f "$COMPOSE_FILE" "$@"
    return
  fi
  if command -v docker-compose >/dev/null 2>&1; then
    docker-compose -p "$PROJECT_NAME" -f "$COMPOSE_FILE" "$@"
    return
  fi
  fail "docker compose or docker-compose is required"
}

cleanup() {
  status=$?
  if [ "$status" -ne 0 ]; then
    compose ps >&2 || true
    compose logs --no-color --tail=120 >&2 || true
  fi
  compose down -v --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

if ! command -v docker >/dev/null 2>&1; then
  echo "SKIP: docker is required for compose three-node smoke"
  exit 0
fi

if [ ! -e /dev/fuse ]; then
  echo "SKIP: /dev/fuse is required for compose three-node mounted smoke"
  exit 0
fi

PATH="/usr/local/go/bin:$PATH" GOPATH="${GOPATH:-$HOME/go}" make -C "$ROOT_DIR" juicefs

test -x "$ROOT_DIR/juicefs" || fail "missing executable juicefs binary: $ROOT_DIR/juicefs"

compose build
compose up -d redis rustfs l2-node-1 l2-node-2 client-node
compose exec -T client-node /work/hack/rdma-compose/three-node-client.sh prepare

compose stop l2-node-1
compose stop rustfs
compose exec -T client-node /work/hack/rdma-compose/three-node-client.sh read-surviving-l2

compose start rustfs
compose stop l2-node-2
compose exec -T client-node /work/hack/rdma-compose/three-node-client.sh read-l3-fallback

compose stop rustfs
compose exec -T client-node /work/hack/rdma-compose/three-node-client.sh read-l2-l3-down-fails

echo "ok - docker compose three-node distributed cache smoke passed"
