#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$ROOT_DIR/hack/rdma-native-smoke-test.sh"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_contains() {
  pattern="$1"
  grep -F -- "$pattern" "$SCRIPT" >/dev/null ||
    fail "expected rdma native smoke script to contain: $pattern"
}

assert_contains "JFS_RDMA_SMOKE_REPORT"
assert_contains "ops_per_second"
assert_contains "duration_ms"
assert_contains "--mock-rdma"
assert_contains "OPEN_RDMA_DRIVER"
assert_contains "LD_LIBRARY_PATH"

echo "ok - rdma native smoke script documents performance report output"
