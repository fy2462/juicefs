#!/bin/sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
TMP_DIR="${TMPDIR:-/tmp}/jfs-three-tier-cache-rustfs.$$"
TESTS_RUN=0

cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT INT TERM

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

pass() {
  TESTS_RUN=$((TESTS_RUN + 1))
  echo "ok $TESTS_RUN - $*"
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1
}

rustfs_bin() {
  if [ -n "${RUSTFS_BIN:-}" ]; then
    printf '%s\n' "$RUSTFS_BIN"
    return
  fi
  command -v rustfs 2>/dev/null || true
}

require_rustfs() {
  bin="$(rustfs_bin)"
  if [ -z "$bin" ] || [ ! -x "$bin" ]; then
    cat <<'EOF'
SKIP: rustfs binary is required for this smoke.
Set RUSTFS_BIN=/path/to/rustfs or put rustfs in PATH.
EOF
    exit 0
  fi
  RUSTFS_BIN="$bin"
  export RUSTFS_BIN
}

assert_file() {
  path="$1"
  [ -f "$path" ] || fail "missing file: $path"
}

assert_dir() {
  path="$1"
  [ -d "$path" ] || fail "missing directory: $path"
}

ensure_juicefs() {
  if [ -x "$ROOT_DIR/juicefs" ]; then
    return
  fi
  (cd "$ROOT_DIR" && make juicefs)
}

main() {
  rm -rf "$TMP_DIR"
  mkdir -p "$TMP_DIR"
  require_rustfs
  ensure_juicefs
  assert_file "$ROOT_DIR/juicefs"
  pass "juicefs binary is available"
  echo "passed $TESTS_RUN tests"
}

main "$@"
