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

assert_makefile_contains() {
  pattern="$1"
  grep -F -- "$pattern" "$MAKEFILE" >/dev/null ||
    fail "expected Makefile to contain: $pattern"
}

assert_contains "JFS_RDMA_SMOKE_REPORT"
assert_contains "ops_per_second"
assert_contains "duration_ms"
assert_contains "--mock-rdma"
assert_contains "OPEN_RDMA_DRIVER"
assert_contains "LD_LIBRARY_PATH"
assert_makefile_contains "test.rdma-native-mock:"
assert_makefile_contains '--mock-rdma "$${OPEN_RDMA_DRIVER'
assert_makefile_contains "test.rdma-native-mock-stress:"

echo "ok - rdma native smoke script documents performance report output"
