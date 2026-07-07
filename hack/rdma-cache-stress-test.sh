#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$ROOT_DIR/hack/rdma-cache-stress.sh"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_contains() {
  pattern="$1"
  grep -F -- "$pattern" "$SCRIPT" >/dev/null ||
    fail "expected rdma cache stress script to contain: $pattern"
}

test -x "$SCRIPT" || fail "missing executable rdma cache stress script: $SCRIPT"
assert_contains "--transport"
assert_contains "--nodes"
assert_contains "--concurrency"
assert_contains "--size"
assert_contains "--duration"
assert_contains "--json"
assert_contains "ops"
assert_contains "errors"
assert_contains "p50_ms"
assert_contains "p95_ms"
assert_contains "p99_ms"
assert_contains "rdma.NewClient"
assert_contains "httpcache.NewClientWithOptions"
if grep -F -- "latencies <-" "$SCRIPT" >/dev/null; then
  fail "rdma cache stress script must not block workers on an undrained latency channel"
fi

echo "ok - rdma cache stress script exposes transport and JSON latency fields"
