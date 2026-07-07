#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$ROOT_DIR/hack/rdma-native-smoke-test.sh"
MAKEFILE="$ROOT_DIR/Makefile"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_contains() {
  pattern="$1"
  grep -F -- "$pattern" "$SCRIPT" >/dev/null ||
    fail "expected rdma native smoke script to contain: $pattern"
}

assert_not_contains() {
  pattern="$1"
  if grep -F -- "$pattern" "$SCRIPT" >/dev/null; then
    fail "rdma native smoke script should not contain: $pattern"
  fi
}

assert_makefile_contains() {
  pattern="$1"
  grep -F -- "$pattern" "$MAKEFILE" >/dev/null ||
    fail "expected Makefile to contain: $pattern"
}

assert_contains "JFS_RDMA_SMOKE_REPORT"
assert_contains "ops_per_second"
assert_contains "duration_ms"
assert_contains "DurationMS"
assert_contains "OpsPerSecond"
assert_contains "--mock-rdma"
assert_contains "OPEN_RDMA_DRIVER"
assert_contains "LD_LIBRARY_PATH"
assert_contains "JFS_RDMA_SMOKE_SKIP_TCP_PROBE"
assert_contains "JFS_RDMA_SMOKE_SERVER_DEVICE_INDEX"
assert_contains "JFS_RDMA_SMOKE_CLIENT_DEVICE_INDEX"
assert_not_contains '. "$MOCK_RDMA_DIR/scripts/setup-env.sh"'
assert_makefile_contains "test.rdma-native-mock:"
assert_makefile_contains '--mock-rdma "$${OPEN_RDMA_DRIVER'
assert_makefile_contains "test.rdma-native-mock-stress:"
assert_makefile_contains 'JFS_RDMA_SMOKE_OPS=$${JFS_RDMA_STRESS_OPS:-1} JFS_RDMA_SMOKE_CONCURRENCY=$${JFS_RDMA_STRESS_CONCURRENCY:-1}'
assert_makefile_contains 'JFS_RDMA_SMOKE_OPS=$${JFS_RDMA_STRESS_OPS:-500} JFS_RDMA_SMOKE_CONCURRENCY=$${JFS_RDMA_STRESS_CONCURRENCY:-8}'

echo "ok - rdma native smoke script documents performance report output"
