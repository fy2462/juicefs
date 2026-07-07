#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
COMPOSE_FILE="$ROOT_DIR/hack/rdma-compose/docker-compose.yml"
PROJECT_NAME="${JFS_RDMA_COMPOSE_PROJECT:-jfs-rdma-three-node-perf-$$}"
REPORT="${JFS_RDMA_COMPOSE_PERF_REPORT:-$ROOT_DIR/docs/superpowers/runbooks/2026-07-07-l1-l2-l3-three-node-performance.md}"
BUILD_TMPDIR="${TMPDIR:-$ROOT_DIR/.tmp/rdma-compose-three-node-perf-build}"

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
  if [ -n "${tmp_report:-}" ]; then
    rm -f "$tmp_report"
  fi
  if [ "$status" -ne 0 ]; then
    compose ps >&2 || true
    compose logs --no-color --tail=120 >&2 || true
  fi
  compose down -v --remove-orphans >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

if ! command -v docker >/dev/null 2>&1; then
  echo "SKIP: docker is required for compose three-node performance"
  exit 0
fi

if [ ! -e /dev/fuse ]; then
  echo "SKIP: /dev/fuse is required for compose three-node mounted performance"
  exit 0
fi

mkdir -p "$BUILD_TMPDIR"
PATH="/usr/local/go/bin:$PATH" GOPATH="${GOPATH:-$HOME/go}" TMPDIR="$BUILD_TMPDIR" make -C "$ROOT_DIR" juicefs

test -x "$ROOT_DIR/juicefs" || fail "missing executable juicefs binary: $ROOT_DIR/juicefs"

mkdir -p "$(dirname "$REPORT")"
tmp_report="$(mktemp "$REPORT.tmp.XXXXXX")"

compose build
compose up -d redis rustfs l2-node-1 l2-node-2 client-node
compose exec -T client-node /work/hack/rdma-compose/three-node-client.sh performance-report 2>&1 |
  awk 'seen || /^# L1\/L2\/L3 3-Node Cache Performance$/ { seen = 1; print }' >"$tmp_report"
grep -F '# L1/L2/L3 3-Node Cache Performance' "$tmp_report" >/dev/null ||
  fail "performance report header was not found"
test -s "$tmp_report" || fail "performance report is empty"
mv "$tmp_report" "$REPORT"

echo "ok - docker compose three-node L1/L2/L3 performance report written to $REPORT"
