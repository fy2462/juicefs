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

grep -F "three-tier-rustfs:" "$workflow" >/dev/null ||
  fail "rdma-cache workflow is missing the three-tier-rustfs job"

grep -F "make test.three-tier-cache-rustfs" "$workflow" >/dev/null ||
  fail "rdma-cache workflow does not run the RustFS three-tier smoke"

echo "ok - rdma-cache workflow covers RustFS three-tier smoke"
