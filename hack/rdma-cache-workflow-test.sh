#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
workflow="$ROOT_DIR/.github/workflows/rdma-cache.yml"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

grep -F "hack/three-tier-cache-rustfs-test.sh" "$workflow" >/dev/null ||
  fail "rdma-cache workflow does not trigger on the RustFS three-tier smoke script"

grep -F "hack/rdma-cache-runbook-test.sh" "$workflow" >/dev/null ||
  fail "rdma-cache workflow does not trigger on the runbook guard script"

grep -F "hack/rdma-native-smoke-test-test.sh" "$workflow" >/dev/null ||
  fail "rdma-cache workflow does not trigger on the native smoke guard script"

grep -F "hack/three-tier-cache-rustfs-test-test.sh" "$workflow" >/dev/null ||
  fail "rdma-cache workflow does not trigger on the three-tier smoke guard script"

grep -F "docs/superpowers/runbooks/2026-07-07-rdma-distributed-cache.md" "$workflow" >/dev/null ||
  fail "rdma-cache workflow does not trigger on the RDMA distributed cache runbook"

grep -F "three-tier-rustfs:" "$workflow" >/dev/null ||
  fail "rdma-cache workflow is missing the three-tier-rustfs job"

grep -F "make test.three-tier-cache-rustfs" "$workflow" >/dev/null ||
  fail "rdma-cache workflow does not run the RustFS three-tier smoke"

"$ROOT_DIR/hack/rdma-cache-runbook-test.sh"

echo "ok - rdma-cache workflow covers RustFS three-tier smoke"
