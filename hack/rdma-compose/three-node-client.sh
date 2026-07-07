#!/bin/sh
set -eu

META_URL="redis://redis:6379/15"
BUCKET_URL="http://rustfs:9000/jfs-compose-three-node"
ACCESS_KEY="rustfsadmin"
SECRET_KEY="rustfsadmin"
ROOT="/tmp/jfs-compose-three-node"
MNT="$ROOT/mnt"
PAYLOAD="docker-compose-three-node"
SECOND_PAYLOAD="docker-compose-three-node-second"
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

assert_metric_eq() {
  actual="$1"
  expected="$2"
  message="$3"
  awk -v actual="$actual" -v expected="$expected" 'BEGIN { exit !(actual == expected) }' ||
    fail "$message: expected=$expected actual=$actual"
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
  printf '%s\n' "$SECOND_PAYLOAD" > "$MNT/payload2.txt"
  sync
  unmount_jfs "$MNT" "$MOUNT_PID"

  mount_with_remote_cache "$ROOT/l1-warm-survivor" "l2-node-2:9568" 1
  grep -F "$PAYLOAD" "$MNT/payload.txt" >/dev/null
  grep -F "$SECOND_PAYLOAD" "$MNT/payload2.txt" >/dev/null
  wait_for_l2_entries /l2-node-2-cache
  unmount_jfs "$MNT" "$MOUNT_PID"
  echo "ok - docker compose three-node prepared L3 and surviving L2"
}

read_surviving_l2() {
  METRICS_PORT=9571
  mount_with_remote_cache "$ROOT/l1-read-surviving-l2" "l2-node-1:9568,l2-node-2:9568" 2
  hit_before="$(metric_value "$METRICS_PORT" "juicefs_remote_cache_gets_total" 'result="hit"')"
  grep -F "$PAYLOAD" "$MNT/payload.txt" >/dev/null
  grep -F "$SECOND_PAYLOAD" "$MNT/payload2.txt" >/dev/null
  hit_after="$(metric_value "$METRICS_PORT" "juicefs_remote_cache_gets_total" 'result="hit"')"
  assert_metric_gt "$hit_after" "$hit_before" "remote cache hit metric did not increase for surviving L2 read"
  unmount_jfs "$MNT" "$MOUNT_PID"
  METRICS_PORT=
  echo "ok - docker compose three-node read survives one L2 node and L3 outage"
}

read_node_health_fallback() {
  wait_for_http rustfs 9000
  METRICS_PORT=9573
  mount_with_remote_cache "$ROOT/l1-read-node-health-fallback" "l2-node-1:9568" 1
  fallback_before="$(metric_value "$METRICS_PORT" "juicefs_remote_cache_fallbacks_total")"
  skip_before="$(metric_value "$METRICS_PORT" "juicefs_remote_cache_node_skips_total" 'node="http://l2-node-1:9568"')"
  grep -F "$PAYLOAD" "$MNT/payload.txt" >/dev/null
  node1_down="$(metric_value "$METRICS_PORT" "juicefs_remote_cache_node_down" 'node="http://l2-node-1:9568"')"
  assert_metric_eq "$node1_down" "1" "remote cache node_down metric did not mark stopped L2 node down"
  grep -F "$SECOND_PAYLOAD" "$MNT/payload2.txt" >/dev/null
  fallback_after="$(metric_value "$METRICS_PORT" "juicefs_remote_cache_fallbacks_total")"
  skip_after="$(metric_value "$METRICS_PORT" "juicefs_remote_cache_node_skips_total" 'node="http://l2-node-1:9568"')"
  assert_metric_gt "$fallback_after" "$fallback_before" "remote cache fallback metric did not increase for stopped L2 read"
  assert_metric_gt "$skip_after" "$skip_before" "remote cache skip metric did not increase for stopped L2 node"
  unmount_jfs "$MNT" "$MOUNT_PID"
  METRICS_PORT=
  echo "ok - docker compose three-node marks stopped L2 down and skips it"
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

measure_read() {
  label="$1"
  file="$2"
  bytes="$3"
  start_ns="$(date +%s%N)"
  cat "$MNT/$file" >/dev/null
  end_ns="$(date +%s%N)"
  elapsed_ms="$(awk -v start="$start_ns" -v end="$end_ns" 'BEGIN { printf "%.3f", (end - start) / 1000000 }')"
  mbps="$(awk -v bytes="$bytes" -v start="$start_ns" -v end="$end_ns" 'BEGIN {
    seconds = (end - start) / 1000000000
    if (seconds <= 0) {
      printf "0.00"
    } else {
      printf "%.2f", (bytes / 1048576) / seconds
    }
  }')"
  printf '%s|%s|%s|%s\n' "$label" "$bytes" "$elapsed_ms" "$mbps"
}

print_result_row() {
  result="$1"
  remote_hits_delta="$2"
  IFS='|' read -r label bytes elapsed_ms mbps <<EOF
$result
EOF
  printf '| %s | %s | %s | %s | %s |\n' "$label" "$bytes" "$elapsed_ms" "$mbps" "$remote_hits_delta"
}

metric_delta() {
  after="$1"
  before="$2"
  awk -v after="$after" -v before="$before" 'BEGIN { printf "%.0f", after - before }'
}

performance_report() {
  PERF_MB="${JFS_RDMA_COMPOSE_PERF_MB:-32}"
  PERF_FILE="perf.bin"
  PERF_BYTES=$((PERF_MB * 1048576))

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
    --compress none \
    --trash-days 0 \
    "$META_URL" compose-three-node-perf >/dev/null

  mount_without_remote_cache "$ROOT/l1-perf-writer"
  dd if=/dev/zero of="$MNT/$PERF_FILE" bs=1M count="$PERF_MB" status=none
  sync
  unmount_jfs "$MNT" "$MOUNT_PID"

  mount_without_remote_cache "$ROOT/l1-perf-l3-cold"
  l3_result="$(measure_read "L3 cold read" "$PERF_FILE" "$PERF_BYTES")"
  unmount_jfs "$MNT" "$MOUNT_PID"

  mount_with_remote_cache "$ROOT/l1-perf-fill-l2" "l2-node-1:9568,l2-node-2:9568" 2
  cat "$MNT/$PERF_FILE" >/dev/null
  wait_for_l2_entries /l2-node-1-cache
  wait_for_l2_entries /l2-node-2-cache
  unmount_jfs "$MNT" "$MOUNT_PID"

  METRICS_PORT=9574
  mount_with_remote_cache "$ROOT/l1-perf-l2-hot" "l2-node-1:9568,l2-node-2:9568" 2
  l2_hit_before="$(metric_value "$METRICS_PORT" "juicefs_remote_cache_gets_total" 'result="hit"')"
  l2_result="$(measure_read "L2 hot read" "$PERF_FILE" "$PERF_BYTES")"
  l2_hit_after="$(metric_value "$METRICS_PORT" "juicefs_remote_cache_gets_total" 'result="hit"')"
  l2_hits_delta="$(metric_delta "$l2_hit_after" "$l2_hit_before")"
  unmount_jfs "$MNT" "$MOUNT_PID"
  METRICS_PORT=

  METRICS_PORT=9575
  mount_with_remote_cache "$ROOT/l1-perf-l1-hot" "l2-node-1:9568,l2-node-2:9568" 2
  cat "$MNT/$PERF_FILE" >/dev/null
  l1_hit_before="$(metric_value "$METRICS_PORT" "juicefs_remote_cache_gets_total" 'result="hit"')"
  l1_result="$(measure_read "L1 hot read" "$PERF_FILE" "$PERF_BYTES")"
  l1_hit_after="$(metric_value "$METRICS_PORT" "juicefs_remote_cache_gets_total" 'result="hit"')"
  l1_hits_delta="$(metric_delta "$l1_hit_after" "$l1_hit_before")"
  unmount_jfs "$MNT" "$MOUNT_PID"
  METRICS_PORT=

  cat <<EOF
# L1/L2/L3 3-Node Cache Performance

Generated: $(date -u +%Y-%m-%dT%H:%M:%SZ)

## Topology

- Client: \`client-node\`
- L1: per-client local disk cache under \`$ROOT\`
- L2: two remote cache servers, \`l2-node-1:9568\` and \`l2-node-2:9568\`
- L3: RustFS S3-compatible object storage at \`$BUCKET_URL\`
- Metadata: Redis at \`$META_URL\`
- L2 transport: HTTP remote-cache transport inside Docker Compose

This report measures the three-tier cache behavior in a 3-node compose topology.
It is not a real RDMA hardware throughput report; native RDMA performance still
requires a host with real RDMA devices or an open-rdma mock environment.

## Parameters

- Payload: \`$PERF_MB MiB\` (\`$PERF_BYTES\` bytes)
- Remote cache replicas: \`2\`
- L1 cache size: \`64 MiB\`
- Remote cache metric used for L2 proof: \`juicefs_remote_cache_gets_total{result="hit"}\`

## Results

| Path | Bytes | Elapsed ms | Throughput MiB/s | remote_hits_delta |
| --- | ---: | ---: | ---: | ---: |
EOF
  print_result_row "$l3_result" "n/a"
  print_result_row "$l2_result" "$l2_hits_delta"
  print_result_row "$l1_result" "$l1_hits_delta"
  cat <<EOF

## Interpretation

- L3 cold read uses a fresh L1 cache and no remote cache, so data is served from
  RustFS through the object storage path.
- L2 hot read uses a fresh L1 cache after the remote cache has been warmed. A
  positive \`remote_hits_delta\` proves the read used L2.
- L1 hot read repeats the read on the same mounted client after the local cache
  is warm. A zero or near-zero \`remote_hits_delta\` means the second read stayed
  local.
EOF
}

case "${1:-}" in
  prepare)
    prepare
    ;;
  read-surviving-l2)
    read_surviving_l2
    ;;
  read-node-health-fallback)
    read_node_health_fallback
    ;;
  read-l3-fallback)
    read_l3_fallback
    ;;
  read-l2-l3-down-fails)
    read_l2_l3_down_fails
    ;;
  performance-report)
    performance_report
    ;;
  *)
    echo "usage: $0 prepare|read-node-health-fallback|read-surviving-l2|read-l3-fallback|read-l2-l3-down-fails|performance-report" >&2
    exit 2
    ;;
esac
