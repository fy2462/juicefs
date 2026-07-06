#!/bin/sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
TMP_DIR="${TMPDIR:-/tmp}/jfs-three-tier-cache-rustfs.$$"
TESTS_RUN=0

cleanup() {
  stop_rustfs
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
  if [ -n "$bin" ] && [ -x "$bin" ]; then
    RUSTFS_BIN="$bin"
    RUSTFS_MODE="bin"
    export RUSTFS_BIN RUSTFS_MODE
    return
  fi
  if need_cmd docker; then
    RUSTFS_MODE="docker"
    RUSTFS_IMAGE="${RUSTFS_IMAGE:-rustfs/rustfs:latest}"
    export RUSTFS_MODE RUSTFS_IMAGE
    return
  fi
  if [ -n "$bin" ]; then
    cat <<'EOF'
SKIP: rustfs binary is not executable and docker is not available.
Set RUSTFS_BIN=/path/to/rustfs, put rustfs in PATH, or install docker.
EOF
    exit 0
  fi
  cat <<'EOF'
SKIP: rustfs runtime is required for this smoke.
Set RUSTFS_BIN=/path/to/rustfs, put rustfs in PATH, or install docker.
EOF
  exit 0
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

wait_for_path() {
  path="$1"
  i=0
  while [ "$i" -lt 100 ]; do
    if [ -e "$path" ] || [ -d "$path" ]; then
      return
    fi
    i=$((i + 1))
    sleep 0.1
  done
  fail "timed out waiting for path: $path"
}

wait_for_mount() {
  path="$1"
  i=0
  while [ "$i" -lt 100 ]; do
    if mountpoint -q "$path"; then
      return
    fi
    i=$((i + 1))
    sleep 0.1
  done
  fail "timed out waiting for mount: $path"
}

wait_for_http() {
  host="$1"
  port="$2"
  need_cmd curl || fail "curl is required to wait for rustfs"
  i=0
  while [ "$i" -lt 100 ]; do
    if curl -sS --connect-timeout 1 "http://$host:$port/" >/dev/null 2>&1; then
      return
    fi
    i=$((i + 1))
    sleep 0.1
  done
  fail "timed out waiting for http endpoint: $host:$port"
}

start_rustfs() {
  data_dir="$TMP_DIR/rustfs-data"
  log="$TMP_DIR/rustfs.log"
  mkdir -p "$data_dir"
  chmod 0777 "$data_dir"
  export RUSTFS_ACCESS_KEY="${RUSTFS_ACCESS_KEY:-rustfsadmin}"
  export RUSTFS_SECRET_KEY="${RUSTFS_SECRET_KEY:-rustfsadmin}"
  export MINIO_ROOT_USER="$RUSTFS_ACCESS_KEY"
  export MINIO_ROOT_PASSWORD="$RUSTFS_SECRET_KEY"
  endpoint="${RUSTFS_ENDPOINT:-127.0.0.1:9000}"
  case "${RUSTFS_MODE:-bin}" in
    docker)
      RUSTFS_CONTAINER="jfs-rustfs-$$"
      docker run -d --rm \
        --name "$RUSTFS_CONTAINER" \
        --user "$(id -u):$(id -g)" \
        -p "$endpoint:9000" \
        -e RUSTFS_ACCESS_KEY="$RUSTFS_ACCESS_KEY" \
        -e RUSTFS_SECRET_KEY="$RUSTFS_SECRET_KEY" \
        -v "$data_dir:/data" \
        "$RUSTFS_IMAGE" server --address :9000 /data >"$log"
      export RUSTFS_CONTAINER
      ;;
    *)
      "$RUSTFS_BIN" server --address "$endpoint" "$data_dir" >"$log" 2>&1 &
      RUSTFS_PID=$!
      export RUSTFS_PID
      ;;
  esac
  RUSTFS_BUCKET_URL="http://$endpoint/jfs-three-tier"
  export RUSTFS_BUCKET_URL
  wait_for_http 127.0.0.1 9000
}

stop_rustfs() {
  if [ -n "${RUSTFS_CONTAINER:-}" ]; then
    docker rm -f "$RUSTFS_CONTAINER" >/dev/null 2>&1 || true
  fi
  if [ -n "${RUSTFS_PID:-}" ]; then
    kill "$RUSTFS_PID" 2>/dev/null || true
    wait "$RUSTFS_PID" 2>/dev/null || true
  fi
}

run_s3_baseline() {
  meta="$TMP_DIR/meta.db"
  mountpoint="$TMP_DIR/mnt"
  cache_dir="$TMP_DIR/l1-cache"
  bucket_url="$RUSTFS_BUCKET_URL"
  mkdir -p "$mountpoint" "$cache_dir"

  "$ROOT_DIR/juicefs" format \
    --storage s3 \
    --bucket "$bucket_url" \
    --access-key "$RUSTFS_ACCESS_KEY" \
    --secret-key "$RUSTFS_SECRET_KEY" \
    --trash-days 0 \
    "sqlite3://$meta" rustfs-jfs

  "$ROOT_DIR/juicefs" mount \
    --cache-dir "$cache_dir" \
    --cache-size 64 \
    "sqlite3://$meta" "$mountpoint" &
  mount_pid=$!
  wait_for_mount "$mountpoint"

  printf 'three-tier-rustfs\n' > "$mountpoint/payload.txt"
  sync
  "$ROOT_DIR/juicefs" umount "$mountpoint"
  wait "$mount_pid" 2>/dev/null || true

  "$ROOT_DIR/juicefs" mount \
    --cache-dir "$cache_dir" \
    --cache-size 64 \
    "sqlite3://$meta" "$mountpoint" &
  mount_pid=$!
  wait_for_mount "$mountpoint"
  wait_for_path "$mountpoint/payload.txt"
  grep -F 'three-tier-rustfs' "$mountpoint/payload.txt" >/dev/null
  "$ROOT_DIR/juicefs" umount "$mountpoint"
  wait "$mount_pid" 2>/dev/null || true
}

main() {
  rm -rf "$TMP_DIR"
  mkdir -p "$TMP_DIR"
  require_rustfs
  ensure_juicefs
  assert_file "$ROOT_DIR/juicefs"
  pass "juicefs binary is available"
  RUSTFS_ACCESS_KEY="${RUSTFS_ACCESS_KEY:-rustfsadmin}"
  RUSTFS_SECRET_KEY="${RUSTFS_SECRET_KEY:-rustfsadmin}"
  RUSTFS_BUCKET_URL="${RUSTFS_BUCKET_URL:-http://127.0.0.1:9000/jfs-three-tier}"
  export RUSTFS_ACCESS_KEY RUSTFS_SECRET_KEY RUSTFS_BUCKET_URL
  start_rustfs
  run_s3_baseline
  pass "rustfs S3 baseline survives remount"
  echo "passed $TESTS_RUN tests"
}

main "$@"
