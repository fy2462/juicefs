#!/bin/sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
TMP_DIR="${TMPDIR:-/tmp}/jfs-three-tier-cache-rustfs.$$"
TESTS_RUN=0

cleanup() {
  stop_remote_cache_servers
  stop_rustfs
  stop_redis
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

redis_bin() {
  command -v redis-server 2>/dev/null || true
}

require_redis() {
  bin="$(redis_bin)"
  if [ -n "$bin" ] && [ -x "$bin" ]; then
    need_cmd redis-cli || fail "redis-cli is required when using local redis-server"
    REDIS_BIN="$bin"
    REDIS_MODE="bin"
    export REDIS_BIN REDIS_MODE
    return
  fi
  if need_cmd docker; then
    REDIS_MODE="docker"
    REDIS_IMAGE="${REDIS_IMAGE:-redis:7-alpine}"
    export REDIS_MODE REDIS_IMAGE
    return
  fi
  cat <<'EOF'
SKIP: redis runtime is required for this smoke.
Install redis-server, put redis-server in PATH, or install docker.
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

remote_cache_entry_count() {
  dir="$1"
  find "$dir" -name '*.data' -type f 2>/dev/null | wc -l | tr -d ' '
}

wait_for_remote_cache_entries_gt() {
  dir="$1"
  previous="$2"
  i=0
  while [ "$i" -lt 100 ]; do
    entries="$(remote_cache_entry_count "$dir")"
    if [ "$entries" -gt "$previous" ]; then
      return
    fi
    i=$((i + 1))
    sleep 0.1
  done
  fail "timed out waiting for remote cache entries in $dir to exceed $previous"
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

start_redis() {
  REDIS_ENDPOINT="${REDIS_ENDPOINT:-127.0.0.1:16379}"
  REDIS_HOST="${REDIS_ENDPOINT%:*}"
  REDIS_PORT="${REDIS_ENDPOINT##*:}"
  redis_log="$TMP_DIR/redis.log"
  case "${REDIS_MODE:-bin}" in
    docker)
      REDIS_CONTAINER="jfs-redis-$$"
      docker run -d --rm \
        --name "$REDIS_CONTAINER" \
        -p "$REDIS_ENDPOINT:6379" \
        "$REDIS_IMAGE" redis-server --save "" --appendonly no >"$redis_log"
      export REDIS_CONTAINER
      ;;
    *)
      "$REDIS_BIN" --bind "$REDIS_HOST" --port "$REDIS_PORT" --save "" --appendonly no --dir "$TMP_DIR" >"$redis_log" 2>&1 &
      REDIS_PID=$!
      export REDIS_PID
      ;;
  esac
  export REDIS_ENDPOINT REDIS_HOST REDIS_PORT
  wait_for_redis
}

stop_redis() {
  if [ -n "${REDIS_CONTAINER:-}" ]; then
    docker rm -f "$REDIS_CONTAINER" >/dev/null 2>&1 || true
  fi
  if [ -n "${REDIS_PID:-}" ]; then
    kill "$REDIS_PID" 2>/dev/null || true
    wait "$REDIS_PID" 2>/dev/null || true
  fi
}

redis_cli() {
  db="$1"
  shift
  case "${REDIS_MODE:-bin}" in
    docker)
      docker exec "$REDIS_CONTAINER" redis-cli -n "$db" "$@"
      ;;
    *)
      redis-cli -h "$REDIS_HOST" -p "$REDIS_PORT" -n "$db" "$@"
      ;;
  esac
}

wait_for_redis() {
  i=0
  while [ "$i" -lt 100 ]; do
    if redis_cli 0 PING >/dev/null 2>&1; then
      return
    fi
    i=$((i + 1))
    sleep 0.1
  done
  fail "timed out waiting for redis endpoint: $REDIS_ENDPOINT"
}

flush_redis_db() {
  redis_cli "$1" FLUSHDB >/dev/null
}

meta_url() {
  db="$1"
  flush_redis_db "$db"
  printf 'redis://%s/%s\n' "$REDIS_ENDPOINT" "$db"
}

start_remote_cache_server() {
  remote_dir="$TMP_DIR/l2-cache"
  remote_log="$TMP_DIR/rdma-cache-server.log"
  start_remote_cache_server_at 9568 "$remote_dir" "$remote_log"
  REMOTE_CACHE_DIR="$remote_dir"
  REMOTE_CACHE_PID="$LAST_REMOTE_CACHE_PID"
  export REMOTE_CACHE_DIR REMOTE_CACHE_PID
}

start_remote_cache_server_at() {
  port="$1"
  remote_dir="$2"
  remote_log="$3"
  mkdir -p "$remote_dir"
  "$ROOT_DIR/juicefs" rdma-cache-server \
    --listen "127.0.0.1:$port" \
    --transport http \
    --cache-dir "$remote_dir" \
    --cache-size 64M >"$remote_log" 2>&1 &
  LAST_REMOTE_CACHE_PID=$!
  REMOTE_CACHE_PIDS="${REMOTE_CACHE_PIDS:-} $LAST_REMOTE_CACHE_PID"
  export LAST_REMOTE_CACHE_PID REMOTE_CACHE_PIDS
}

stop_remote_cache_pid() {
  pid="$1"
  if [ -n "$pid" ]; then
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  fi
}

stop_remote_cache_server() {
  stop_remote_cache_pid "${REMOTE_CACHE_PID:-}"
}

stop_remote_cache_servers() {
  for pid in ${REMOTE_CACHE_PIDS:-}; do
    stop_remote_cache_pid "$pid"
  done
}

run_s3_baseline() {
  meta="$(meta_url 10)"
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
    "$meta" rustfs-jfs

  "$ROOT_DIR/juicefs" mount \
    --cache-dir "$cache_dir" \
    --cache-size 64 \
    "$meta" "$mountpoint" &
  mount_pid=$!
  wait_for_mount "$mountpoint"

  printf 'three-tier-rustfs\n' > "$mountpoint/payload.txt"
  sync
  "$ROOT_DIR/juicefs" umount "$mountpoint"
  wait "$mount_pid" 2>/dev/null || true

  "$ROOT_DIR/juicefs" mount \
    --cache-dir "$cache_dir" \
    --cache-size 64 \
    "$meta" "$mountpoint" &
  mount_pid=$!
  wait_for_mount "$mountpoint"
  wait_for_path "$mountpoint/payload.txt"
  grep -F 'three-tier-rustfs' "$mountpoint/payload.txt" >/dev/null
  "$ROOT_DIR/juicefs" umount "$mountpoint"
  wait "$mount_pid" 2>/dev/null || true
}

run_three_tier_read_path() {
  meta="$(meta_url 11)"
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
    "$meta" three-tier-jfs

  "$ROOT_DIR/juicefs" mount \
    --cache-dir "$l1a" \
    --cache-size 64 \
    --remote-cache rdma \
    --remote-cache-transport http \
    --remote-cache-nodes 127.0.0.1:9568 \
    --remote-cache-timeout 2s \
    --remote-cache-fill-local=true \
    --remote-cache-fill-remote=true \
    "$meta" "$mountpoint" &
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
    --remote-cache-timeout 2s \
    --remote-cache-fill-local=true \
    --remote-cache-fill-remote=true \
    "$meta" "$mountpoint" &
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
    --remote-cache-timeout 2s \
    --remote-cache-fill-local=true \
    --remote-cache-fill-remote=true \
    "$meta" "$mountpoint" &
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
  meta="$(meta_url 12)"
  mountpoint="$TMP_DIR/l2-down-mnt"
  l1a="$TMP_DIR/l2-down-l1-a"
  l1b="$TMP_DIR/l2-down-l1-b"
  l1c="$TMP_DIR/l2-down-l1-c"
  mkdir -p "$mountpoint" "$l1a" "$l1b" "$l1c"

  start_rustfs
  start_remote_cache_server
  wait_for_http 127.0.0.1 9568

  "$ROOT_DIR/juicefs" format \
    --storage s3 \
    --bucket "$RUSTFS_BUCKET_URL" \
    --access-key "$RUSTFS_ACCESS_KEY" \
    --secret-key "$RUSTFS_SECRET_KEY" \
    --trash-days 0 \
    "$meta" l2-down-jfs

  "$ROOT_DIR/juicefs" mount \
    --cache-dir "$l1a" \
    --cache-size 64 \
    --remote-cache rdma \
    --remote-cache-transport http \
    --remote-cache-nodes 127.0.0.1:9568 \
    --remote-cache-timeout 2s \
    --remote-cache-fill-local=true \
    --remote-cache-fill-remote=true \
    "$meta" "$mountpoint" &
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
    "$meta" "$mountpoint" &
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
    "$meta" "$mountpoint" &
  mount_pid=$!
  wait_for_mount "$mountpoint"
  if grep -F 'l2-down-fallback' "$mountpoint/payload.txt" >/dev/null 2>&1; then
    unmount_jfs "$mountpoint" "$mount_pid"
    fail "l2-down fallback read unexpectedly succeeded after rustfs stopped"
  fi
  unmount_jfs "$mountpoint" "$mount_pid"
}

run_multi_node_l2_recovery_path() {
  meta="$(meta_url 13)"
  mountpoint="$TMP_DIR/multi-l2-mnt"
  l1a="$TMP_DIR/multi-l2-l1-a"
  l1fill="$TMP_DIR/multi-l2-l1-fill"
  l1b="$TMP_DIR/multi-l2-l1-b"
  l1c="$TMP_DIR/multi-l2-l1-c"
  l1d="$TMP_DIR/multi-l2-l1-d"
  node1_dir="$TMP_DIR/l2-node-1"
  node2_dir="$TMP_DIR/l2-node-2"
  mkdir -p "$mountpoint" "$l1a" "$l1fill" "$l1b" "$l1c" "$l1d"

  start_rustfs
  start_remote_cache_server_at 9568 "$node1_dir" "$TMP_DIR/rdma-cache-node-1.log"
  node1_pid="$LAST_REMOTE_CACHE_PID"
  start_remote_cache_server_at 9569 "$node2_dir" "$TMP_DIR/rdma-cache-node-2.log"
  node2_pid="$LAST_REMOTE_CACHE_PID"
  wait_for_http 127.0.0.1 9568
  wait_for_http 127.0.0.1 9569

  "$ROOT_DIR/juicefs" format \
    --storage s3 \
    --bucket "$RUSTFS_BUCKET_URL" \
    --access-key "$RUSTFS_ACCESS_KEY" \
    --secret-key "$RUSTFS_SECRET_KEY" \
    --trash-days 0 \
    "$meta" multi-l2-jfs

  "$ROOT_DIR/juicefs" mount \
    --cache-dir "$l1a" \
    --cache-size 64 \
    --remote-cache rdma \
    --remote-cache-transport http \
    --remote-cache-nodes 127.0.0.1:9568,127.0.0.1:9569 \
    --remote-cache-replicas 2 \
    --remote-cache-timeout 2s \
    --remote-cache-fill-local=true \
    --remote-cache-fill-remote=true \
    "$meta" "$mountpoint" &
  mount_pid=$!
  wait_for_mount "$mountpoint"
  dd if=/dev/zero bs=1048576 count=1 2>/dev/null | tr '\000' 'B' > "$mountpoint/blob.bin"
  sync
  unmount_jfs "$mountpoint" "$mount_pid"

  "$ROOT_DIR/juicefs" mount \
    --cache-dir "$l1fill" \
    --cache-size 64 \
    --remote-cache rdma \
    --remote-cache-transport http \
    --remote-cache-nodes 127.0.0.1:9568,127.0.0.1:9569 \
    --remote-cache-replicas 2 \
    --remote-cache-timeout 2s \
    --remote-cache-fill-local=true \
    --remote-cache-fill-remote=true \
    "$meta" "$mountpoint" &
  mount_pid=$!
  wait_for_mount "$mountpoint"
  cat "$mountpoint/blob.bin" >/dev/null
  unmount_jfs "$mountpoint" "$mount_pid"
  wait_for_remote_cache_entries "$node1_dir" 1
  wait_for_remote_cache_entries "$node2_dir" 1

  stop_rustfs
  wait_for_http_down 127.0.0.1 9000
  stop_remote_cache_pid "$node1_pid"
  wait_for_http_down 127.0.0.1 9568

  "$ROOT_DIR/juicefs" mount \
    --cache-dir "$l1b" \
    --cache-size 64 \
    --remote-cache rdma \
    --remote-cache-transport http \
    --remote-cache-nodes 127.0.0.1:9568,127.0.0.1:9569 \
    --remote-cache-replicas 2 \
    --remote-cache-timeout 2s \
    --remote-cache-fill-local=true \
    --remote-cache-fill-remote=true \
    "$meta" "$mountpoint" &
  mount_pid=$!
  wait_for_mount "$mountpoint"
  if ! cat "$mountpoint/blob.bin" > "$TMP_DIR/multi-l2-blob.bin"; then
    unmount_jfs "$mountpoint" "$mount_pid"
    fail "multi-node L2 read failed after one L2 node and rustfs stopped"
  fi
  size="$(wc -c < "$TMP_DIR/multi-l2-blob.bin" | tr -d ' ')"
  [ "$size" = "1048576" ] || fail "unexpected multi-node blob size: $size"
  non_b_bytes="$(tr -d 'B' < "$TMP_DIR/multi-l2-blob.bin" | wc -c | tr -d ' ')"
  [ "$non_b_bytes" = "0" ] || fail "unexpected multi-node blob content"
  unmount_jfs "$mountpoint" "$mount_pid"

  node1_before="$(remote_cache_entry_count "$node1_dir")"
  start_remote_cache_server_at 9568 "$node1_dir" "$TMP_DIR/rdma-cache-node-1-restart.log"
  node1_pid="$LAST_REMOTE_CACHE_PID"
  wait_for_http 127.0.0.1 9568
  start_rustfs

  "$ROOT_DIR/juicefs" mount \
    --cache-dir "$l1c" \
    --cache-size 64 \
    --remote-cache rdma \
    --remote-cache-transport http \
    --remote-cache-nodes 127.0.0.1:9568,127.0.0.1:9569 \
    --remote-cache-replicas 2 \
    --remote-cache-timeout 2s \
    --remote-cache-fill-local=true \
    --remote-cache-fill-remote=true \
    "$meta" "$mountpoint" &
  mount_pid=$!
  wait_for_mount "$mountpoint"
  printf 'multi-node-recovery\n' > "$mountpoint/recovery.txt"
  sync
  unmount_jfs "$mountpoint" "$mount_pid"

  "$ROOT_DIR/juicefs" mount \
    --cache-dir "$l1d" \
    --cache-size 64 \
    --remote-cache rdma \
    --remote-cache-transport http \
    --remote-cache-nodes 127.0.0.1:9568,127.0.0.1:9569 \
    --remote-cache-replicas 2 \
    --remote-cache-timeout 2s \
    --remote-cache-fill-local=true \
    --remote-cache-fill-remote=true \
    "$meta" "$mountpoint" &
  mount_pid=$!
  wait_for_mount "$mountpoint"
  grep -F 'multi-node-recovery' "$mountpoint/recovery.txt" >/dev/null
  unmount_jfs "$mountpoint" "$mount_pid"
  wait_for_remote_cache_entries_gt "$node1_dir" "$node1_before"

  stop_remote_cache_pid "$node1_pid"
  stop_remote_cache_pid "$node2_pid"
  stop_rustfs
}

main() {
  rm -rf "$TMP_DIR"
  mkdir -p "$TMP_DIR"
  require_rustfs
  require_redis
  ensure_juicefs
  assert_file "$ROOT_DIR/juicefs"
  pass "juicefs binary is available"
  RUSTFS_ACCESS_KEY="${RUSTFS_ACCESS_KEY:-rustfsadmin}"
  RUSTFS_SECRET_KEY="${RUSTFS_SECRET_KEY:-rustfsadmin}"
  RUSTFS_BUCKET_URL="${RUSTFS_BUCKET_URL:-http://127.0.0.1:9000/jfs-three-tier}"
  export RUSTFS_ACCESS_KEY RUSTFS_SECRET_KEY RUSTFS_BUCKET_URL
  start_redis
  start_rustfs
  run_s3_baseline
  pass "rustfs S3 baseline survives remount"
  start_remote_cache_server
  wait_for_http 127.0.0.1 9568
  pass "remote cache server starts"
  run_three_tier_read_path
  pass "three-tier read path returns data with fresh L1 after rustfs stops"
  stop_remote_cache_server
  run_multi_node_l2_recovery_path
  pass "multi-node L2 survives one-node failure and recovers after restart"
  run_l2_down_fallback_path
  pass "read falls back to rustfs when remote cache server is down"
  echo "passed $TESTS_RUN tests"
}

main "$@"
