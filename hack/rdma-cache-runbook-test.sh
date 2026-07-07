#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RUNBOOK="$ROOT_DIR/docs/superpowers/runbooks/2026-07-07-rdma-distributed-cache.md"
ALERTS="$ROOT_DIR/docs/superpowers/runbooks/rdma-cache-alerts.prometheus.yml"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

assert_contains() {
  pattern="$1"
  grep -F -- "$pattern" "$RUNBOOK" >/dev/null ||
    fail "expected RDMA distributed cache runbook to contain: $pattern"
}

assert_alert_contains() {
  pattern="$1"
  grep -F -- "$pattern" "$ALERTS" >/dev/null ||
    fail "expected RDMA cache alert rules to contain: $pattern"
}

assert_contains "## Failure Metadata Model"
assert_contains "authoritative metadata"
assert_contains "L2 cache entries are disposable"
assert_contains "does not require metadata repair"
assert_contains "active probe marks the node recovered"
assert_contains "L1+L3"
assert_contains "redis://127.0.0.1:6379"
assert_contains "Docker Redis"
assert_contains "make test.rdma-native-mock"
assert_contains "make test.rdma-native-mounted-mock"
assert_contains "make test.rdma-native-mounted-failover-mock"
assert_contains "make test.rdma-native-mock-stress"
assert_contains "hack/rdma-cache-stress.sh"
assert_contains "p95_ms"
assert_contains "p99_ms"
assert_contains "without an RDMA device"
assert_contains "juicefs_remote_cache_node_down"
assert_contains "juicefs_remote_cache_node_skips_total"
assert_contains "juicefs_remote_cache_node_probe_total"
assert_contains "juicefs_remote_cache_fallbacks_total"

test -f "$ALERTS" || fail "missing RDMA cache alert rules: $ALERTS"
assert_alert_contains "JuiceFSRemoteCacheNodeDown"
assert_alert_contains "JuiceFSRemoteCacheAllReplicasSkipped"
assert_alert_contains "JuiceFSRemoteCacheProbeFailures"
assert_alert_contains "JuiceFSRemoteCacheFallbacksHigh"
assert_alert_contains "juicefs_remote_cache_node_down"
assert_alert_contains "juicefs_remote_cache_node_skips_total"
assert_alert_contains "juicefs_remote_cache_node_probe_total"
assert_alert_contains "juicefs_remote_cache_fallbacks_total"

echo "ok - rdma distributed cache runbook documents failure metadata model"
