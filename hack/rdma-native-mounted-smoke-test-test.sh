#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$ROOT_DIR/hack/rdma-native-mounted-smoke-test.sh"
MAKEFILE="$ROOT_DIR/Makefile"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_contains() {
  pattern="$1"
  grep -F -- "$pattern" "$SCRIPT" >/dev/null ||
    fail "expected native mounted smoke script to contain: $pattern"
}

assert_makefile_contains() {
  pattern="$1"
  grep -F -- "$pattern" "$MAKEFILE" >/dev/null ||
    fail "expected Makefile to contain: $pattern"
}

test -x "$SCRIPT" || fail "missing executable native mounted smoke script: $SCRIPT"
assert_contains "OPEN_RDMA_DRIVER"
assert_contains "JFS_RDMA_DEVICE_INDEX"
assert_contains "JFS_RDMA_REQUIRE_DEVICE=true"
assert_contains "start_redis"
assert_contains "start_rustfs"
assert_contains "rdma-cache-server"
assert_contains "--transport rdma"
assert_contains "--remote-cache-transport rdma"
assert_contains "--remote-cache-fill-remote=true"
assert_contains "wait_for_remote_cache_entries"
assert_contains "stop_rustfs"
assert_contains "native RDMA mounted read path returns data with fresh L1 after rustfs stops"
assert_makefile_contains "test.rdma-native-mounted-mock:"
assert_makefile_contains "rdma-native-mounted-smoke-test.sh --mock-rdma"

echo "ok - native RDMA mounted smoke covers Redis RustFS and RDMA L2"
