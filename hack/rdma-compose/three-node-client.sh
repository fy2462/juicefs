#!/bin/sh
set -eu

META_URL="redis://redis:6379/15"
BUCKET_URL="http://rustfs:9000/jfs-compose-three-node"
ACCESS_KEY="rustfsadmin"
SECRET_KEY="rustfsadmin"
ROOT="/tmp/jfs-compose-three-node"
MNT="$ROOT/mnt"
PAYLOAD="docker-compose-three-node"
METRICS_PORT="${METRICS_PORT:-}"

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

wait_for_http() {
  host="$1"
  port="$2"
  i=0
  last=""
  while [ "$i" -lt 300 ]; do
    if curl -sS --connect-timeout 1 "http://$host:$port/" >/dev/null 2>&1; then
      return
    fi
    last="$(curl -sS --connect-timeout 1 "http://$host:$port/" 2>&1 >/dev/null || true)"
    i=$((i + 1))
    sleep 0.1
  done
  fail "timed out waiting for http endpoint: $host:$port ${last}"
}

wait_for_redis() {
  i=0
  last=""
  while [ "$i" -lt 300 ]; do
    if redis-cli -h redis -n 15 PING >/dev/null 2>&1; then
      return
    fi
    last="$(redis-cli -h redis -n 15 PING 2>&1 >/dev/null || true)"
    i=$((i + 1))
    sleep 0.1
  done
  fail "timed out waiting for redis ${last}"
}

wait_for_mount() {
  path="$1"
  i=0
  while [ "$i" -lt 300 ]; do
    if mountpoint -q "$path"; then
      return
    fi
    i=$((i + 1))
    sleep 0.1
  done
  fail "timed out waiting for mount: $path"
}

wait_for_metrics() {
  port="$1"
  i=0
  last=""
  while [ "$i" -lt 300 ]; do
    if curl -fsS "http://127.0.0.1:$port/metrics" >/dev/null 2>&1; then
      return
    fi
    last="$(curl -fsS "http://127.0.0.1:$port/metrics" 2>&1 >/dev/null || true)"
    i=$((i + 1))
    sleep 0.1
  done
  fail "timed out waiting for mount metrics on port $port ${last}"
}

metric_value() {
  port="$1"
  metric="$2"
  labels="${3:-}"
  curl -fsS "http://127.0.0.1:$port/metrics" |
    awk -v metric="$metric" -v labels="$labels" '
      $0 ~ /^#/ {
        next
      }
      $1 ~ ("^" metric "(\\{|$)") {
        if (labels == "" || index($0, labels) > 0) {
          value = $NF
        }
      }
      END {
        if (value == "") {
          print "0"
        } else {
          print value
        }
      }'
}

assert_metric_gt() {
  after="$1"
  before="$2"
  message="$3"
  awk -v after="$after" -v before="$before" 'BEGIN { exit !(after > before) }' ||
    fail "$message: before=$before after=$after"
}

wait_for_l2_entries() {
  dir="$1"
  i=0
  while [ "$i" -lt 300 ]; do
    entries="$(find "$dir" -name '*.data' -type f 2>/dev/null | wc -l | tr -d ' ')"
    if [ "$entries" -ge 1 ]; then
      return
    fi
    i=$((i + 1))
    sleep 0.1
  done
  fail "timed out waiting for L2 entries in $dir"
}

unmount_jfs() {
  mountpoint="$1"
  mount_pid="${2:-}"
  juicefs umount "$mountpoint" >/dev/null 2>&1 || umount "$mountpoint" >/dev/null 2>&1 || true
  if [ -n "$mount_pid" ]; then
    wait "$mount_pid" >/dev/null 2>&1 || true
  fi
}

mount_jfs() {
  cache_dir="$1"
  shift
  mkdir -p "$cache_dir" "$MNT"
  if [ -n "${METRICS_PORT:-}" ]; then
    juicefs mount \
      --no-usage-report \
      --cache-dir "$cache_dir" \
      --cache-size 64 \
      --metrics "127.0.0.1:$METRICS_PORT" \
      "$@" \
      "$META_URL" "$MNT" &
  else
    juicefs mount \
      --no-usage-report \
      --cache-dir "$cache_dir" \
      --cache-size 64 \
      "$@" \
      "$META_URL" "$MNT" &
  fi
  MOUNT_PID=$!
  wait_for_mount "$MNT"
  if [ -n "${METRICS_PORT:-}" ]; then
    wait_for_metrics "$METRICS_PORT"
  fi
}

mount_with_remote_cache() {
  cache_dir="$1"
  nodes="$2"
  replicas="$3"
  mount_jfs "$cache_dir" \
    --remote-cache rdma \
    --remote-cache-transport http \
    --remote-cache-nodes "$nodes" \
    --remote-cache-replicas "$replicas" \
    --remote-cache-timeout 2s \
    --remote-cache-fail-threshold 1 \
    --remote-cache-node-cooldown 30s \
    --remote-cache-fill-local=true \
    --remote-cache-fill-remote=true
}

mount_without_remote_cache() {
  cache_dir="$1"
  mount_jfs "$cache_dir" --get-timeout 2s --io-retries 1
}

prepare() {
  rm -rf "$ROOT"
  mkdir -p "$MNT"
  wait_for_redis
  wait_for_http rustfs 9000
  wait_for_http l2-node-1 9568
  wait_for_http l2-node-2 9568
  redis-cli -h redis -n 15 FLUSHDB >/dev/null
  juicefs format \
    --storage s3 \
    --bucket "$BUCKET_URL" \
    --access-key "$ACCESS_KEY" \
    --secret-key "$SECRET_KEY" \
    --trash-days 0 \
    "$META_URL" compose-three-node-jfs

  mount_without_remote_cache "$ROOT/l1-writer"
  printf '%s\n' "$PAYLOAD" > "$MNT/payload.txt"
  sync
  unmount_jfs "$MNT" "$MOUNT_PID"

  mount_with_remote_cache "$ROOT/l1-warm-survivor" "l2-node-2:9568" 1
  grep -F "$PAYLOAD" "$MNT/payload.txt" >/dev/null
  wait_for_l2_entries /l2-node-2-cache
  unmount_jfs "$MNT" "$MOUNT_PID"
  echo "ok - docker compose three-node prepared L3 and surviving L2"
}

read_surviving_l2() {
  METRICS_PORT=9571
  mount_with_remote_cache "$ROOT/l1-read-surviving-l2" "l2-node-1:9568,l2-node-2:9568" 2
  hit_before="$(metric_value "$METRICS_PORT" "juicefs_remote_cache_gets_total" 'result="hit"')"
  grep -F "$PAYLOAD" "$MNT/payload.txt" >/dev/null
  hit_after="$(metric_value "$METRICS_PORT" "juicefs_remote_cache_gets_total" 'result="hit"')"
  assert_metric_gt "$hit_after" "$hit_before" "remote cache hit metric did not increase for surviving L2 read"
  unmount_jfs "$MNT" "$MOUNT_PID"
  METRICS_PORT=
  echo "ok - docker compose three-node read survives one L2 node and L3 outage"
}

read_l3_fallback() {
  wait_for_http rustfs 9000
  METRICS_PORT=9572
  mount_with_remote_cache "$ROOT/l1-read-l3-fallback" "l2-node-1:9568,l2-node-2:9568" 2
  fallback_before="$(metric_value "$METRICS_PORT" "juicefs_remote_cache_fallbacks_total")"
  grep -F "$PAYLOAD" "$MNT/payload.txt" >/dev/null
  fallback_after="$(metric_value "$METRICS_PORT" "juicefs_remote_cache_fallbacks_total")"
  assert_metric_gt "$fallback_after" "$fallback_before" "remote cache fallback metric did not increase for all-L2-down L3 read"
  unmount_jfs "$MNT" "$MOUNT_PID"
  METRICS_PORT=
  echo "ok - docker compose three-node read falls back to L3 when all L2 nodes stop"
}

read_l2_l3_down_fails() {
  mount_without_remote_cache "$ROOT/l1-read-l2-l3-down"
  if grep -F "$PAYLOAD" "$MNT/payload.txt" >/dev/null 2>&1; then
    unmount_jfs "$MNT" "$MOUNT_PID"
    fail "read unexpectedly succeeded after L2 and L3 stopped"
  fi
  unmount_jfs "$MNT" "$MOUNT_PID"
  echo "ok - docker compose three-node read fails when L2 and L3 are unavailable"
}

case "${1:-}" in
  prepare)
    prepare
    ;;
  read-surviving-l2)
    read_surviving_l2
    ;;
  read-l3-fallback)
    read_l3_fallback
    ;;
  read-l2-l3-down-fails)
    read_l2_l3_down_fails
    ;;
  *)
    echo "usage: $0 prepare|read-surviving-l2|read-l3-fallback|read-l2-l3-down-fails" >&2
    exit 2
    ;;
esac
