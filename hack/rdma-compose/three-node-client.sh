#!/bin/sh
set -eu

META_URL="redis://redis:6379/15"
BUCKET_URL="http://rustfs:9000/jfs-compose-three-node"
ACCESS_KEY="rustfsadmin"
SECRET_KEY="rustfsadmin"
ROOT="/tmp/jfs-compose-three-node"
MNT="$ROOT/mnt"
PAYLOAD="docker-compose-three-node"

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
  juicefs mount \
    --no-usage-report \
    --cache-dir "$cache_dir" \
    --cache-size 64 \
    "$@" \
    "$META_URL" "$MNT" &
  MOUNT_PID=$!
  wait_for_mount "$MNT"
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
  mount_with_remote_cache "$ROOT/l1-read-surviving-l2" "l2-node-1:9568,l2-node-2:9568" 2
  grep -F "$PAYLOAD" "$MNT/payload.txt" >/dev/null
  unmount_jfs "$MNT" "$MOUNT_PID"
  echo "ok - docker compose three-node read survives one L2 node and L3 outage"
}

read_l3_fallback() {
  wait_for_http rustfs 9000
  mount_with_remote_cache "$ROOT/l1-read-l3-fallback" "l2-node-1:9568,l2-node-2:9568" 2
  grep -F "$PAYLOAD" "$MNT/payload.txt" >/dev/null
  unmount_jfs "$MNT" "$MOUNT_PID"
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
