#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$ROOT_DIR/hack/rdma-compose-three-node-test.sh"
CLIENT_SCRIPT="$ROOT_DIR/hack/rdma-compose/three-node-client.sh"
COMPOSE="$ROOT_DIR/hack/rdma-compose/docker-compose.yml"
DOCKERFILE="$ROOT_DIR/hack/rdma-compose/Dockerfile"
MAKEFILE="$ROOT_DIR/Makefile"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_file_contains() {
  file="$1"
  pattern="$2"
  grep -F -- "$pattern" "$file" >/dev/null ||
    fail "expected $file to contain: $pattern"
}

test -x "$SCRIPT" || fail "missing executable compose smoke script: $SCRIPT"
test -x "$CLIENT_SCRIPT" || fail "missing executable compose client script: $CLIENT_SCRIPT"
test -f "$COMPOSE" || fail "missing compose file: $COMPOSE"
test -f "$DOCKERFILE" || fail "missing compose Dockerfile: $DOCKERFILE"

assert_file_contains "$COMPOSE" "l2-node-1"
assert_file_contains "$COMPOSE" "l2-node-2"
assert_file_contains "$COMPOSE" "client-node"
assert_file_contains "$COMPOSE" "redis"
assert_file_contains "$COMPOSE" "rustfs"
assert_file_contains "$COMPOSE" "/dev/fuse"
assert_file_contains "$COMPOSE" "rdma-cache-server"
assert_file_contains "$COMPOSE" "--transport"
assert_file_contains "$COMPOSE" "http"
assert_file_contains "$CLIENT_SCRIPT" "redis://redis:6379/15"
assert_file_contains "$CLIENT_SCRIPT" "http://rustfs:9000/jfs-compose-three-node"
assert_file_contains "$CLIENT_SCRIPT" "--remote-cache-nodes"
assert_file_contains "$CLIENT_SCRIPT" "l2-node-1:9568,l2-node-2:9568"
assert_file_contains "$CLIENT_SCRIPT" "--remote-cache-replicas"
assert_file_contains "$CLIENT_SCRIPT" "docker compose three-node read survives one L2 node and L3 outage"
assert_file_contains "$CLIENT_SCRIPT" "docker compose three-node read falls back to L3 when all L2 nodes stop"
assert_file_contains "$SCRIPT" "docker compose"
assert_file_contains "$SCRIPT" "three-node-client.sh"
assert_file_contains "$MAKEFILE" "test.rdma-compose-three-node:"
assert_file_contains "$MAKEFILE" "rdma-compose-three-node-test.sh"

echo "ok - docker compose three-node smoke is wired"
