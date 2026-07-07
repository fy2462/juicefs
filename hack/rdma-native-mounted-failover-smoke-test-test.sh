#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$ROOT_DIR/hack/rdma-native-mounted-failover-smoke-test.sh"
MAKEFILE="$ROOT_DIR/Makefile"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_contains() {
  pattern="$1"
  grep -F -- "$pattern" "$SCRIPT" >/dev/null ||
    fail "expected native mounted failover smoke script to contain: $pattern"
}

assert_makefile_contains() {
  pattern="$1"
  grep -F -- "$pattern" "$MAKEFILE" >/dev/null ||
    fail "expected Makefile to contain: $pattern"
}

test -x "$SCRIPT" || fail "missing executable native mounted failover smoke script: $SCRIPT"
assert_contains "OPEN_RDMA_DRIVER"
assert_contains "start_native_l2 1 127.0.0.1:19680"
assert_contains "start_native_l2 2 127.0.0.1:19681"
assert_contains "--remote-cache-nodes"
assert_contains "127.0.0.1:19680,127.0.0.1:19681"
assert_contains "--remote-cache-replicas 2"
assert_contains "--remote-cache-fail-threshold 1"
assert_contains "stop_native_l2 1"
assert_contains "stop_native_l2 2"
assert_contains "stop_rustfs"
assert_contains "native RDMA mounted failover uses surviving L2 after one node stops"
assert_contains "native RDMA mounted path falls back to L3 when all L2 nodes stop"
assert_contains "native RDMA mounted path fails when L2 and L3 are unavailable"
assert_makefile_contains "test.rdma-native-mounted-failover-mock:"
assert_makefile_contains "rdma-native-mounted-failover-smoke-test.sh --mock-rdma"

echo "ok - native RDMA mounted failover smoke covers single-node L2 failure and L3 fallback"
