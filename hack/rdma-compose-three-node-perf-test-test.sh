#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$ROOT_DIR/hack/rdma-compose-three-node-perf-test.sh"
CLIENT_SCRIPT="$ROOT_DIR/hack/rdma-compose/three-node-client.sh"
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

test -x "$SCRIPT" || fail "missing executable compose performance script: $SCRIPT"
test -x "$CLIENT_SCRIPT" || fail "missing executable compose client script: $CLIENT_SCRIPT"

assert_file_contains "$SCRIPT" "three-node-client.sh performance-report"
assert_file_contains "$SCRIPT" "JFS_RDMA_COMPOSE_PERF_REPORT"
assert_file_contains "$SCRIPT" "docker compose three-node L1/L2/L3 performance"
assert_file_contains "$SCRIPT" "awk 'seen || /^# L1\\/L2\\/L3 3-Node Cache Performance$/"
assert_file_contains "$SCRIPT" "tmp_report="
assert_file_contains "$SCRIPT" "grep -F '# L1/L2/L3 3-Node Cache Performance'"
assert_file_contains "$SCRIPT" 'mv "$tmp_report" "$REPORT"'
assert_file_contains "$CLIENT_SCRIPT" "performance_report()"
assert_file_contains "$CLIENT_SCRIPT" "measure_read()"
assert_file_contains "$CLIENT_SCRIPT" "L3 cold read"
assert_file_contains "$CLIENT_SCRIPT" "L2 hot read"
assert_file_contains "$CLIENT_SCRIPT" "L1 hot read"
assert_file_contains "$CLIENT_SCRIPT" "juicefs_remote_cache_gets_total"
assert_file_contains "$CLIENT_SCRIPT" "remote_hits_delta"
assert_file_contains "$CLIENT_SCRIPT" "# L1/L2/L3 3-Node Cache Performance"
assert_file_contains "$MAKEFILE" "test.rdma-compose-three-node-perf:"
assert_file_contains "$MAKEFILE" "rdma-compose-three-node-perf-test.sh"

echo "ok - docker compose three-node performance report is wired"
