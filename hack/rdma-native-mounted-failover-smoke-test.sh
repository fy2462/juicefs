#!/bin/sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
TMP_DIR="${TMPDIR:-/tmp}/jfs-rdma-native-mounted-failover-smoke.$$"
TESTS_RUN=0
MOCK_RDMA_DIR=""
SERVER1_PID=""
SERVER2_PID=""

usage() {
  cat <<'EOF'
Usage: hack/rdma-native-mounted-failover-smoke-test.sh --mock-rdma DIR

Runs a mounted JuiceFS three-tier failover smoke with Redis metadata, RustFS S3
L3, local L1 cache, and two rdma-cache-server --transport=rdma L2 nodes through
open-rdma mock mode.
EOF
}

fail() {
  echo "FAIL: $*" >&2
  for log in "$TMP_DIR"/rdma-cache-server-*.log; do
    [ -f "$log" ] || continue
    echo "----- $(basename "$log") -----" >&2
    cat "$log" >&2
    echo "---------------------------------" >&2
  done
  exit 1
}

pass() {
  TESTS_RUN=$((TESTS_RUN + 1))
  echo "ok $TESTS_RUN - $*"
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --mock-rdma)
      [ "$#" -ge 2 ] || fail "--mock-rdma requires a driver directory"
      MOCK_RDMA_DIR="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage >&2
      exit 2
      ;;
  esac
done

cleanup() {
  stop_mount "${MOUNT_PID:-}" "${MOUNTPOINT:-}"
  stop_native_l2 1
  stop_native_l2 2
  stop_rustfs
  stop_redis
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT INT TERM

need_cmd() {
  command -v "$1" >/dev/null 2>&1
}

ensure_go_env() {
  if ! need_cmd go && [ -x /usr/local/go/bin/go ]; then
    PATH="/usr/local/go/bin:$PATH"
    export PATH
  fi
  need_cmd go || fail "go is required"
  GOPATH="${GOPATH:-$HOME/go}"
  export GOPATH
}

require_native_build() {
  os="$(go env GOOS)"
  cgo="$(go env CGO_ENABLED)"
  [ "$os" = "linux" ] || { echo "SKIP: native RDMA mounted failover smoke requires linux; got $os"; exit 0; }
  [ "$cgo" = "1" ] || { echo "SKIP: native RDMA mounted failover smoke requires CGO_ENABLED=1"; exit 0; }
}

enable_mock_rdma() {
  [ -n "$MOCK_RDMA_DIR" ] || fail "--mock-rdma is required for mounted native RDMA failover smoke"
  [ -d "$MOCK_RDMA_DIR" ] || fail "missing open-rdma checkout: $MOCK_RDMA_DIR"
  "$ROOT_DIR/hack/open-rdma-smoke-test.sh" --driver-dir "$MOCK_RDMA_DIR" --strict
  OPEN_RDMA_DRIVER="$MOCK_RDMA_DIR"
  mock_libs="$MOCK_RDMA_DIR/dtld-ibverbs/target/debug:$MOCK_RDMA_DIR/dtld-ibverbs/rdma-core-55.0/build/lib"
  [ -d "$MOCK_RDMA_DIR/dtld-ibverbs/target/debug" ] || fail "missing open-rdma mock library dir"
  [ -d "$MOCK_RDMA_DIR/dtld-ibverbs/rdma-core-55.0/build/lib" ] || fail "missing open-rdma rdma-core library dir"
  LD_LIBRARY_PATH="$mock_libs:${LD_LIBRARY_PATH:-}"
  export OPEN_RDMA_DRIVER LD_LIBRARY_PATH
}

require_rustfs() {
  if [ -n "${RUSTFS_BIN:-}" ] && [ -x "$RUSTFS_BIN" ]; then
    RUSTFS_MODE="bin"
  elif command -v rustfs >/dev/null 2>&1; then
    RUSTFS_BIN="$(command -v rustfs)"
    RUSTFS_MODE="bin"
  elif need_cmd docker; then
    RUSTFS_MODE="docker"
    RUSTFS_IMAGE="${RUSTFS_IMAGE:-rustfs/rustfs:latest}"
  else
    echo "SKIP: rustfs binary or docker is required"
    exit 0
  fi
  export RUSTFS_MODE RUSTFS_BIN RUSTFS_IMAGE
}

require_redis() {
  if command -v redis-server >/dev/null 2>&1; then
    need_cmd redis-cli || fail "redis-cli is required with local redis-server"
    REDIS_MODE="bin"
    REDIS_BIN="$(command -v redis-server)"
  elif need_cmd docker; then
    REDIS_MODE="docker"
    REDIS_IMAGE="${REDIS_IMAGE:-redis:7-alpine}"
  else
    echo "SKIP: redis-server or docker is required"
    exit 0
  fi
  export REDIS_MODE REDIS_BIN REDIS_IMAGE
}

wait_for_http() {
  host="$1"
  port="$2"
  need_cmd curl || fail "curl is required"
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

wait_for_remote_cache_entries() {
  dir="$1"
  i=0
  while [ "$i" -lt 100 ]; do
    entries="$(find "$dir" -name '*.data' -type f 2>/dev/null | wc -l | tr -d ' ')"
    if [ "$entries" -ge 1 ]; then
      return
    fi
    i=$((i + 1))
    sleep 0.1
  done
  fail "timed out waiting for native RDMA L2 cache entries in $dir"
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
  fail "timed out waiting for redis endpoint"
}

start_redis() {
  REDIS_ENDPOINT="${REDIS_ENDPOINT:-127.0.0.1:16389}"
  REDIS_HOST="${REDIS_ENDPOINT%:*}"
  REDIS_PORT="${REDIS_ENDPOINT##*:}"
  case "${REDIS_MODE:-bin}" in
    docker)
      REDIS_CONTAINER="jfs-rdma-failover-redis-$$"
      docker run -d --rm --name "$REDIS_CONTAINER" -p "$REDIS_ENDPOINT:6379" "$REDIS_IMAGE" redis-server --save "" --appendonly no >"$TMP_DIR/redis.log"
      ;;
    *)
      "$REDIS_BIN" --bind "$REDIS_HOST" --port "$REDIS_PORT" --save "" --appendonly no --dir "$TMP_DIR" >"$TMP_DIR/redis.log" 2>&1 &
      REDIS_PID=$!
      ;;
  esac
  export REDIS_ENDPOINT REDIS_HOST REDIS_PORT REDIS_CONTAINER REDIS_PID
  wait_for_redis
}

stop_redis() {
  if [ -n "${REDIS_CONTAINER:-}" ]; then
    docker rm -f "$REDIS_CONTAINER" >/dev/null 2>&1 || true
  fi
  if [ -n "${REDIS_PID:-}" ]; then
    kill "$REDIS_PID" >/dev/null 2>&1 || true
    wait "$REDIS_PID" >/dev/null 2>&1 || true
  fi
}

start_rustfs() {
  endpoint="${RUSTFS_ENDPOINT:-127.0.0.1:9010}"
  RUSTFS_HOST="${endpoint%:*}"
  RUSTFS_PORT="${endpoint##*:}"
  data_dir="$TMP_DIR/rustfs-data"
  mkdir -p "$data_dir"
  chmod 0777 "$data_dir"
  RUSTFS_ACCESS_KEY="${RUSTFS_ACCESS_KEY:-rustfsadmin}"
  RUSTFS_SECRET_KEY="${RUSTFS_SECRET_KEY:-rustfsadmin}"
  export RUSTFS_ACCESS_KEY RUSTFS_SECRET_KEY MINIO_ROOT_USER="$RUSTFS_ACCESS_KEY" MINIO_ROOT_PASSWORD="$RUSTFS_SECRET_KEY"
  case "${RUSTFS_MODE:-bin}" in
    docker)
      RUSTFS_CONTAINER="jfs-rdma-failover-rustfs-$$"
      docker run -d --rm \
        --name "$RUSTFS_CONTAINER" \
        --user "$(id -u):$(id -g)" \
        -p "$endpoint:9000" \
        -e RUSTFS_ACCESS_KEY="$RUSTFS_ACCESS_KEY" \
        -e RUSTFS_SECRET_KEY="$RUSTFS_SECRET_KEY" \
        -v "$data_dir:/data" \
        "$RUSTFS_IMAGE" server --address :9000 /data >"$TMP_DIR/rustfs.log"
      ;;
    *)
      "$RUSTFS_BIN" server --address "$endpoint" "$data_dir" >"$TMP_DIR/rustfs.log" 2>&1 &
      RUSTFS_PID=$!
      ;;
  esac
  RUSTFS_BUCKET_URL="http://$endpoint/jfs-rdma-failover"
  export RUSTFS_BUCKET_URL RUSTFS_HOST RUSTFS_PORT RUSTFS_CONTAINER RUSTFS_PID
  wait_for_http "$RUSTFS_HOST" "$RUSTFS_PORT"
}

stop_rustfs() {
  if [ -n "${RUSTFS_CONTAINER:-}" ]; then
    docker rm -f "$RUSTFS_CONTAINER" >/dev/null 2>&1 || true
    RUSTFS_CONTAINER=""
  fi
  if [ -n "${RUSTFS_PID:-}" ]; then
    kill "$RUSTFS_PID" >/dev/null 2>&1 || true
    wait "$RUSTFS_PID" >/dev/null 2>&1 || true
    RUSTFS_PID=""
  fi
}

stop_mount() {
  pid="${1:-}"
  mountpoint="${2:-}"
  if [ -n "$mountpoint" ]; then
    if [ -n "${BIN:-}" ]; then
      "$BIN" umount "$mountpoint" >/dev/null 2>&1 || umount "$mountpoint" >/dev/null 2>&1 || true
    else
      umount "$mountpoint" >/dev/null 2>&1 || true
    fi
  fi
  if [ -n "$pid" ]; then
    wait "$pid" >/dev/null 2>&1 || true
  fi
}

build_juicefs() {
  BIN="${JFS_RDMA_BIN:-$TMP_DIR/juicefs-rdma}"
  if [ -n "${JFS_RDMA_BIN:-}" ]; then
    [ -x "$BIN" ] || fail "JFS_RDMA_BIN is not executable: $BIN"
    export BIN
    return
  fi
  (cd "$ROOT_DIR" && go build -tags rdma -o "$BIN" .)
  export BIN
}

start_native_l2() {
  id="$1"
  listen="$2"
  cache_dir="$TMP_DIR/native-l2-cache-$id"
  mkdir -p "$cache_dir"
  JFS_RDMA_REQUIRE_DEVICE=true JFS_RDMA_DEVICE_INDEX="${JFS_RDMA_SMOKE_SERVER_DEVICE_INDEX:-0}" "$BIN" rdma-cache-server \
    --listen "$listen" \
    --transport rdma \
    --cache-dir "$cache_dir" \
    --cache-size 64M >"$TMP_DIR/rdma-cache-server-$id.log" 2>&1 &
  pid=$!
  case "$id" in
    1)
      SERVER1_PID="$pid"
      L2_1_DIR="$cache_dir"
      ;;
    2)
      SERVER2_PID="$pid"
      L2_2_DIR="$cache_dir"
      ;;
    *)
      fail "unknown L2 id: $id"
      ;;
  esac
  export SERVER1_PID SERVER2_PID L2_1_DIR L2_2_DIR
  sleep 0.5
}

stop_native_l2() {
  id="$1"
  case "$id" in
    1) pid="${SERVER1_PID:-}" ;;
    2) pid="${SERVER2_PID:-}" ;;
    *) return 0 ;;
  esac
  if [ -n "$pid" ]; then
    kill "$pid" >/dev/null 2>&1 || true
    wait "$pid" >/dev/null 2>&1 || true
  fi
  case "$id" in
    1) SERVER1_PID="" ;;
    2) SERVER2_PID="" ;;
  esac
}

mount_with_native_l2_nodes() {
  cache_dir="$1"
  mountpoint="$2"
  nodes="$3"
  JFS_RDMA_REQUIRE_DEVICE=true JFS_RDMA_DEVICE_INDEX="${JFS_RDMA_SMOKE_CLIENT_DEVICE_INDEX:-1}" "$BIN" mount \
    --cache-dir "$cache_dir" \
    --cache-size 64 \
    --remote-cache rdma \
    --remote-cache-transport rdma \
    --remote-cache-nodes "$nodes" \
    --remote-cache-replicas 2 \
    --remote-cache-timeout 2s \
    --remote-cache-fail-threshold 1 \
    --remote-cache-node-cooldown 30s \
    --remote-cache-fill-local=true \
    --remote-cache-fill-remote=true \
    "$META_URL" "$mountpoint" &
  MOUNT_PID=$!
  MOUNTPOINT="$mountpoint"
  export MOUNT_PID MOUNTPOINT
  wait_for_mount "$mountpoint"
}

mount_without_remote_cache() {
  cache_dir="$1"
  mountpoint="$2"
  "$BIN" mount \
    --cache-dir "$cache_dir" \
    --cache-size 64 \
    --get-timeout 2s \
    --io-retries 1 \
    "$META_URL" "$mountpoint" &
  MOUNT_PID=$!
  MOUNTPOINT="$mountpoint"
  export MOUNT_PID MOUNTPOINT
  wait_for_mount "$mountpoint"
}

format_volume() {
  META_URL="redis://$REDIS_ENDPOINT/15"
  redis_cli 15 FLUSHDB >/dev/null
  "$BIN" format \
    --storage s3 \
    --bucket "$RUSTFS_BUCKET_URL" \
    --access-key "$RUSTFS_ACCESS_KEY" \
    --secret-key "$RUSTFS_SECRET_KEY" \
    --trash-days 0 \
    "$META_URL" rdma-failover-jfs
  export META_URL
}

write_payload_to_l3() {
  mountpoint="$TMP_DIR/mnt"
  writer_l1="$TMP_DIR/l1-writer"
  mkdir -p "$mountpoint" "$writer_l1"
  "$BIN" mount --cache-dir "$writer_l1" --cache-size 64 "$META_URL" "$mountpoint" &
  MOUNT_PID=$!
  MOUNTPOINT="$mountpoint"
  wait_for_mount "$mountpoint"
  printf 'native-rdma-mounted-failover\n' > "$mountpoint/payload.txt"
  sync
  stop_mount "$MOUNT_PID" "$mountpoint"
  MOUNT_PID=""
}

warm_surviving_l2() {
  mountpoint="$TMP_DIR/mnt"
  fill_l1="$TMP_DIR/l1-fill-survivor"
  mkdir -p "$mountpoint" "$fill_l1"
  mount_with_native_l2_nodes "$fill_l1" "$mountpoint" "127.0.0.1:19681"
  grep -F 'native-rdma-mounted-failover' "$mountpoint/payload.txt" >/dev/null
  wait_for_remote_cache_entries "$L2_2_DIR"
  stop_mount "$MOUNT_PID" "$mountpoint"
  MOUNT_PID=""
}

verify_surviving_l2_after_one_node_stops() {
  mountpoint="$TMP_DIR/mnt"
  read_l1="$TMP_DIR/l1-read-survivor"
  mkdir -p "$mountpoint" "$read_l1"
  stop_native_l2 1
  stop_rustfs
  wait_for_http_down "$RUSTFS_HOST" "$RUSTFS_PORT"
  mount_with_native_l2_nodes "$read_l1" "$mountpoint" "127.0.0.1:19680,127.0.0.1:19681"
  grep -F 'native-rdma-mounted-failover' "$mountpoint/payload.txt" >/dev/null
  stop_mount "$MOUNT_PID" "$mountpoint"
  MOUNT_PID=""
}

verify_l3_fallback_after_all_l2_nodes_stop() {
  mountpoint="$TMP_DIR/mnt"
  l3_l1="$TMP_DIR/l1-l3-fallback"
  mkdir -p "$mountpoint" "$l3_l1"
  start_rustfs
  stop_native_l2 2
  mount_with_native_l2_nodes "$l3_l1" "$mountpoint" "127.0.0.1:19680,127.0.0.1:19681"
  grep -F 'native-rdma-mounted-failover' "$mountpoint/payload.txt" >/dev/null
  stop_mount "$MOUNT_PID" "$mountpoint"
  MOUNT_PID=""
}

verify_l2_l3_down_fails() {
  mountpoint="$TMP_DIR/mnt"
  dead_l1="$TMP_DIR/l1-l2-l3-down"
  mkdir -p "$mountpoint" "$dead_l1"
  mount_without_remote_cache "$dead_l1" "$mountpoint"
  stop_rustfs
  wait_for_http_down "$RUSTFS_HOST" "$RUSTFS_PORT"
  if grep -F 'native-rdma-mounted-failover' "$mountpoint/payload.txt" >/dev/null 2>&1; then
    fail "read unexpectedly succeeded after all L2 nodes and RustFS stopped"
  fi
  stop_mount "$MOUNT_PID" "$mountpoint"
  MOUNT_PID=""
}

run_mounted_failover_path() {
  start_native_l2 1 127.0.0.1:19680
  start_native_l2 2 127.0.0.1:19681
  write_payload_to_l3
  warm_surviving_l2
  verify_surviving_l2_after_one_node_stops
  pass "native RDMA mounted failover uses surviving L2 after one node stops"
  verify_l3_fallback_after_all_l2_nodes_stop
  pass "native RDMA mounted path falls back to L3 when all L2 nodes stop"
  verify_l2_l3_down_fails
  pass "native RDMA mounted path fails when L2 and L3 are unavailable"
}

main() {
  rm -rf "$TMP_DIR"
  mkdir -p "$TMP_DIR"
  ensure_go_env
  require_native_build
  enable_mock_rdma
  require_rustfs
  require_redis
  build_juicefs
  pass "built rdma-tagged juicefs binary"
  start_redis
  start_rustfs
  format_volume
  pass "formatted Redis metadata volume on RustFS"
  run_mounted_failover_path
  echo "passed $TESTS_RUN tests"
}

main "$@"
