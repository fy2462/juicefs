#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RUNBOOK="$ROOT_DIR/docs/superpowers/runbooks/2026-07-07-rdma-distributed-cache.md"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_contains() {
  pattern="$1"
  grep -F -- "$pattern" "$RUNBOOK" >/dev/null ||
    fail "expected RDMA distributed cache runbook to contain: $pattern"
}

assert_contains "## Failure Metadata Model"
assert_contains "authoritative metadata"
assert_contains "L2 cache entries are disposable"
assert_contains "does not require metadata repair"
assert_contains "active probe marks the node recovered"
assert_contains "L1+L3"

echo "ok - rdma distributed cache runbook documents failure metadata model"
