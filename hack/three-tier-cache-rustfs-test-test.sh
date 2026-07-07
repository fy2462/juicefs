#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="$ROOT_DIR/hack/three-tier-cache-rustfs-test.sh"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_contains() {
  pattern="$1"
  grep -F -- "$pattern" "$SCRIPT" >/dev/null ||
    fail "expected three-tier RustFS smoke script to contain: $pattern"
}

assert_not_contains() {
  pattern="$1"
  if grep -F -- "$pattern" "$SCRIPT" >/dev/null; then
    fail "three-tier RustFS smoke should not contain: $pattern"
  fi
}

assert_contains "start_redis"
assert_contains "REDIS_ENDPOINT"
assert_contains "redis://%s/%s"
assert_contains "flush_redis_db"
assert_contains "--remote-cache-timeout 2s"
assert_not_contains "sqlite3://"

echo "ok - three-tier RustFS smoke uses Redis metadata"
