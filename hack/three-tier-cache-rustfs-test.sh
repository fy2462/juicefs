#!/bin/sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
TMP_DIR="${TMPDIR:-/tmp}/jfs-three-tier-cache-rustfs.$$"
TESTS_RUN=0

cleanup() {
  stop_remote_cache_server
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

wait_for_http_down() {
  host="$1"
  port="$2"
  need_cmd curl || fail "curl is required to wait for rustfs"
  i=0
  while [ "$i" -lt 100 ]; do
    if ! curl -sS --connect-timeout 1 "http://$host:$port/" >/dev/null 2>&1; then
      return
    fi
    i=$((i + 1))
    sleep 0.1
  done
  fail "timed out waiting for http endpoint to stop: $host:$port"
}

wait_for_remote_cache_entries() {
  dir="$1"
  min_entries="$2"
  i=0
  while [ "$i" -lt 100 ]; do
    entries="$(find "$dir" -name '*.data' -type f 2>/dev/null | wc -l | tr -d ' ')"
    if [ "$entries" -ge "$min_entries" ]; then
      return
    fi
    i=$((i + 1))
    sleep 0.1
  done
  fail "timed out waiting for $min_entries remote cache entries in $dir"
}

unmount_jfs() {
  mountpoint="$1"
  mount_pid="${2:-}"
  "$ROOT_DIR/juicefs" umount "$mountpoint" >/dev/null 2>&1 || umount "$mountpoint" >/dev/null 2>&1 || true
  if [ -n "$mount_pid" ]; then
    wait "$mount_pid" 2>/dev/null || true
  fi
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

start_remote_cache_server() {
  remote_dir="$TMP_DIR/l2-cache"
  remote_log="$TMP_DIR/rdma-cache-server.log"
  mkdir -p "$remote_dir"
  REMOTE_CACHE_DIR="$remote_dir"
  export REMOTE_CACHE_DIR
  "$ROOT_DIR/juicefs" rdma-cache-server \
    --listen 127.0.0.1:9568 \
    --transport http \
    --cache-dir "$remote_dir" \
    --cache-size 64M >"$remote_log" 2>&1 &
  REMOTE_CACHE_PID=$!
  export REMOTE_CACHE_PID
}

stop_remote_cache_server() {
  if [ -n "${REMOTE_CACHE_PID:-}" ]; then
    kill "$REMOTE_CACHE_PID" 2>/dev/null || true
    wait "$REMOTE_CACHE_PID" 2>/dev/null || true
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

run_three_tier_read_path() {
  meta="$TMP_DIR/three-tier-meta.db"
  mountpoint="$TMP_DIR/three-tier-mnt"
  l1a="$TMP_DIR/l1-client-a"
  l1fill="$TMP_DIR/l1-client-fill"
  l1b="$TMP_DIR/l1-client-b"
  mkdir -p "$mountpoint" "$l1a" "$l1fill" "$l1b"

  "$ROOT_DIR/juicefs" format \
    --storage s3 \
    --bucket "$RUSTFS_BUCKET_URL" \
    --access-key "$RUSTFS_ACCESS_KEY" \
    --secret-key "$RUSTFS_SECRET_KEY" \
    --trash-days 0 \
    "sqlite3://$meta" three-tier-jfs

  "$ROOT_DIR/juicefs" mount \
    --cache-dir "$l1a" \
    --cache-size 64 \
    --remote-cache rdma \
    --remote-cache-transport http \
    --remote-cache-nodes 127.0.0.1:9568 \
    --remote-cache-fill-local=true \
    --remote-cache-fill-remote=true \
    "sqlite3://$meta" "$mountpoint" &
  mount_pid=$!
  wait_for_mount "$mountpoint"
  dd if=/dev/zero bs=1048576 count=1 2>/dev/null | tr '\000' 'A' > "$mountpoint/blob.bin"
  sync
  unmount_jfs "$mountpoint" "$mount_pid"

  "$ROOT_DIR/juicefs" mount \
    --cache-dir "$l1fill" \
    --cache-size 64 \
    --remote-cache rdma \
    --remote-cache-transport http \
    --remote-cache-nodes 127.0.0.1:9568 \
    --remote-cache-fill-local=true \
    --remote-cache-fill-remote=true \
    "sqlite3://$meta" "$mountpoint" &
  mount_pid=$!
  wait_for_mount "$mountpoint"
  cat "$mountpoint/blob.bin" >/dev/null
  unmount_jfs "$mountpoint" "$mount_pid"
  wait_for_remote_cache_entries "$REMOTE_CACHE_DIR" 1

  stop_rustfs
  wait_for_http_down 127.0.0.1 9000

  "$ROOT_DIR/juicefs" mount \
    --cache-dir "$l1b" \
    --cache-size 64 \
    --remote-cache rdma \
    --remote-cache-transport http \
    --remote-cache-nodes 127.0.0.1:9568 \
    --remote-cache-fill-local=true \
    --remote-cache-fill-remote=true \
    "sqlite3://$meta" "$mountpoint" &
  mount_pid=$!
  wait_for_mount "$mountpoint"
  wait_for_path "$mountpoint/blob.bin"
  if ! cat "$mountpoint/blob.bin" > "$TMP_DIR/l2-blob.bin"; then
    unmount_jfs "$mountpoint" "$mount_pid"
    fail "fresh L1 read failed after rustfs stopped"
  fi
  size="$(wc -c < "$TMP_DIR/l2-blob.bin" | tr -d ' ')"
  [ "$size" = "1048576" ] || fail "unexpected blob size from three-tier read: $size"
  non_a_bytes="$(tr -d 'A' < "$TMP_DIR/l2-blob.bin" | wc -c | tr -d ' ')"
  [ "$non_a_bytes" = "0" ] || fail "unexpected blob content from three-tier read"
  unmount_jfs "$mountpoint" "$mount_pid"
}

run_l2_down_fallback_path() {
  meta="$TMP_DIR/l2-down-meta.db"
  mountpoint="$TMP_DIR/l2-down-mnt"
  l1a="$TMP_DIR/l2-down-l1-a"
  l1b="$TMP_DIR/l2-down-l1-b"
  l1c="$TMP_DIR/l2-down-l1-c"
  mkdir -p "$mountpoint" "$l1a" "$l1b" "$l1c"

  start_rustfs

  "$ROOT_DIR/juicefs" format \
    --storage s3 \
    --bucket "$RUSTFS_BUCKET_URL" \
    --access-key "$RUSTFS_ACCESS_KEY" \
    --secret-key "$RUSTFS_SECRET_KEY" \
    --trash-days 0 \
    "sqlite3://$meta" l2-down-jfs

  "$ROOT_DIR/juicefs" mount \
    --cache-dir "$l1a" \
    --cache-size 64 \
    --remote-cache rdma \
    --remote-cache-transport http \
    --remote-cache-nodes 127.0.0.1:9568 \
    --remote-cache-fill-local=true \
    --remote-cache-fill-remote=true \
    "sqlite3://$meta" "$mountpoint" &
  mount_pid=$!
  wait_for_mount "$mountpoint"
  printf 'l2-down-fallback\n' > "$mountpoint/payload.txt"
  sync
  unmount_jfs "$mountpoint" "$mount_pid"

  stop_remote_cache_server

  "$ROOT_DIR/juicefs" mount \
    --cache-dir "$l1b" \
    --cache-size 64 \
    --remote-cache rdma \
    --remote-cache-transport http \
    --remote-cache-nodes 127.0.0.1:9568 \
    --remote-cache-timeout 25ms \
    --remote-cache-fill-local=true \
    --remote-cache-fill-remote=true \
    "sqlite3://$meta" "$mountpoint" &
  mount_pid=$!
  wait_for_mount "$mountpoint"
  wait_for_path "$mountpoint/payload.txt"
  grep -F 'l2-down-fallback' "$mountpoint/payload.txt" >/dev/null
  unmount_jfs "$mountpoint" "$mount_pid"

  stop_rustfs
  wait_for_http_down 127.0.0.1 9000

  "$ROOT_DIR/juicefs" mount \
    --cache-dir "$l1c" \
    --cache-size 64 \
    --remote-cache rdma \
    --remote-cache-transport http \
    --remote-cache-nodes 127.0.0.1:9568 \
    --remote-cache-timeout 25ms \
    "sqlite3://$meta" "$mountpoint" &
  mount_pid=$!
  wait_for_mount "$mountpoint"
  if grep -F 'l2-down-fallback' "$mountpoint/payload.txt" >/dev/null 2>&1; then
    unmount_jfs "$mountpoint" "$mount_pid"
    fail "l2-down fallback read unexpectedly succeeded after rustfs stopped"
  fi
  unmount_jfs "$mountpoint" "$mount_pid"
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
  start_remote_cache_server
  wait_for_http 127.0.0.1 9568
  pass "remote cache server starts"
  run_three_tier_read_path
  pass "three-tier read path returns data with fresh L1 after rustfs stops"
  run_l2_down_fallback_path
  pass "read falls back to rustfs when remote cache server is down"
  echo "passed $TESTS_RUN tests"
}

main "$@"
