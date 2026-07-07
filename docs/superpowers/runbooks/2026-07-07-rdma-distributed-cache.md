# RDMA Distributed Cache Runbook

This runbook covers the current local-disk + remote L2 + S3/RustFS workflow on
branch `feature/rdma-distributed-cache`.

## Environment

Use the Go toolchain installed under `/usr/local/go` and keep `GOPATH` under the
home directory:

```sh
export PATH=/usr/local/go/bin:$PATH
export GOPATH="$HOME/go"
```

Build JuiceFS:

```sh
make juicefs
```

## RustFS L3

The smoke can use a `rustfs` binary or Docker. With Docker available, no manual
RustFS startup is required:

```sh
make test.three-tier-cache-rustfs
```

The script starts RustFS, formats a JuiceFS volume on S3-compatible storage, and
starts disk-backed remote cache servers.

## L2 Remote Cache Flags

Use HTTP transport for local semantic testing:

```sh
juicefs mount \
  --cache-dir /tmp/jfs-l1 \
  --cache-size 64 \
  --remote-cache rdma \
  --remote-cache-transport http \
  --remote-cache-nodes 127.0.0.1:9568,127.0.0.1:9569 \
  --remote-cache-replicas 2 \
  --remote-cache-timeout 50ms \
  --remote-cache-fail-threshold 3 \
  --remote-cache-node-cooldown 5s \
  --remote-cache-probe-interval 1s \
  --remote-cache-probe-timeout 10ms \
  sqlite3:///tmp/jfs-meta.db /mnt/jfs
```

The important failure behavior is:

- One failed L2 node is skipped locally after the failure threshold.
- Active probe can recover a node before user traffic waits for cooldown.
- If all L2 nodes are unavailable, reads fall back to L1 and then L3.
- If both L2 and L3 are unavailable and L1 is empty, the read fails.

## Failure Metadata Model

JuiceFS authoritative metadata remains in the configured metadata engine, and
the authoritative data copy remains in L3 object storage. The RDMA remote cache
layer is not a metadata owner and does not participate in inode, slice, or
object naming decisions. L2 cache entries are disposable copies addressed by the
same block keys that can be reloaded from L3.

When a single RDMA/L2 node fails, clients count dial, handshake, send/recv, CQ
timeout, and protocol errors as remote cache node failures. After
`--remote-cache-fail-threshold`, the health manager skips that node during
`--remote-cache-node-cooldown`. Reads then try other selected L2 replicas; if no
healthy L2 replica is usable, the read falls through to L1+L3. This does not require metadata repair because the failed node never holds authoritative metadata
or the only authoritative data copy.

Recovery is also cache-local. Active probes run at
`--remote-cache-probe-interval`; when a probe succeeds, the active probe marks the node recovered and it re-enters replica selection. A recovered node may be
empty, stale, or partially warm. Misses simply refill from L3 or from normal
future reads, and deletes are best-effort against L2 because L3 plus JuiceFS
metadata define whether an object is live. If L3 is down at the same time and
the requested block is absent from L1 and all healthy L2 replicas, reads fail
instead of inventing metadata state.

Use native RDMA transport for the current `rdma` tagged build:

```sh
go build -tags rdma -o juicefs-rdma .
./juicefs-rdma rdma-cache-server \
  --listen 127.0.0.1:9568 \
  --transport rdma \
  --cache-dir /tmp/jfs-l2 \
  --cache-size 64G

./juicefs-rdma mount \
  --cache-dir /tmp/jfs-l1 \
  --cache-size 64 \
  --remote-cache rdma \
  --remote-cache-transport rdma \
  --remote-cache-nodes 127.0.0.1:9568 \
  --remote-cache-timeout 50ms \
  --remote-cache-fail-threshold 3 \
  --remote-cache-node-cooldown 5s \
  --remote-cache-probe-interval 1s \
  --remote-cache-probe-timeout 10ms \
  sqlite3:///tmp/jfs-meta.db /mnt/jfs
```

Native transport build-time and runtime knobs:

| Name | Default | Meaning |
| --- | --- | --- |
| `OPEN_RDMA_DRIVER` | unset | Optional open-rdma checkout used by the capability gate. |
| `JFS_RDMA_DEVICE_INDEX` | `0` | RDMA device index passed to native resource setup. |
| `JFS_RDMA_PORT_NUM` | `1` | RDMA port number used for local endpoint metadata and QP transitions. |
| `JFS_RDMA_MAX_FRAME_BYTES` | `4194304` | Maximum protocol frame size; values below 64 KiB are raised to 64 KiB. |
| `JFS_RDMA_CQ_TIMEOUT` | `50ms` | Deadline for polling verbs send/recv completions before failing the RDMA request. |
| `JFS_RDMA_REQUIRE_DEVICE` | `false` | When `true`, native client dial fails if no ibverbs/open-rdma device is available. Use this on hosts intended to prove real verbs readiness. |

## Smoke Coverage

Run:

```sh
make test.three-tier-cache-rustfs
```

Expected numbered checks:

1. JuiceFS binary is available.
2. RustFS S3 baseline survives remount.
3. Remote cache server starts.
4. Fresh-L1 read succeeds from L2 after RustFS stops.
5. Multi-node L2 survives one-node failure and recovers after restart.
6. Read falls back to RustFS when the remote cache server is down.

## Open-RDMA Gate

Point `OPEN_RDMA_DRIVER` at an open-rdma checkout:

```sh
export OPEN_RDMA_DRIVER=/media/psf/Home/github/PFS/open-rdma-driver
hack/open-rdma-smoke-test.sh --driver-dir "$OPEN_RDMA_DRIVER"
PATH=/usr/local/go/bin:$PATH go test -tags rdma ./pkg/cache/remote/rdma/...
```

The `rdma` build tag now compiles the native transport boundary, a libibverbs
resource lifecycle that opens devices and allocates PD/CQ/MR buffers when an
RDMA device is available, creates an RC QP, exports local endpoint metadata, and
can exchange endpoint metadata between native client/server connections and move
QPs through INIT/RTR/RTS. The native package also has minimal verbs
`PostRecv`/`PostSend`/`PollCompletion` wrappers, with a device-backed send/recv
test that runs when an ibverbs/open-rdma device exists. The data movement path
has a resource-backed frame exchange path on both client and server when native
resources exist. The default native smoke can still fall back to the staged TCP
frame path when no RDMA device exists. The strict native smoke sets
`JFS_RDMA_REQUIRE_DEVICE=true` for both client and `rdma-cache-server`; on a host
with an ibverbs/open-rdma device, this proves payloads travel through verbs
instead of TCP fallback. On a host without such a device, it prints a clear SKIP
before starting the server.

## Native Smoke And Stress

Run the direct native transport smoke:

```sh
make test.rdma-native-smoke
```

Run strict native transport smoke on an open-rdma or real RDMA host:

```sh
make test.rdma-native-strict
```

Run a configurable local stress pass:

```sh
JFS_RDMA_SMOKE_REPORT=/tmp/rdma-native-stress.json \
JFS_RDMA_STRESS_OPS=5000 \
JFS_RDMA_STRESS_CONCURRENCY=16 \
make test.rdma-native-stress
```

Run the same stress workload in strict native mode on an RDMA-capable host:

```sh
JFS_RDMA_STRESS_OPS=5000 JFS_RDMA_STRESS_CONCURRENCY=16 make test.rdma-native-strict-stress
```

The stress harness builds a `rdma` tagged `juicefs`, starts
`rdma-cache-server --transport=rdma`, and runs concurrent PUT/GET/DELETE
round trips through the RDMA client. It is a correctness and regression stress,
not a final bandwidth benchmark; use the strict target when the host has
open-rdma or real RDMA devices and you need proof that fallback is disabled.
When `JFS_RDMA_SMOKE_REPORT` is set, the harness writes a JSON summary with
`ops`, `concurrency`, `duration_ms`, and `ops_per_second` so CI artifacts can be
compared across builds.

## Metrics

With the normal JuiceFS metrics prefix, the remote cache health metrics are:

```text
juicefs_remote_cache_node_down{transport,node}
juicefs_remote_cache_node_failures_total{transport,node}
juicefs_remote_cache_node_recoveries_total{transport,node}
juicefs_remote_cache_node_skips_total{transport,node,op}
juicefs_remote_cache_node_probe_total{transport,node,result}
```

Alert on sustained `node_down == 1`, rising skips for all replicas, or repeated
probe failures. Node labels are configured node addresses, so do not include
secrets in `--remote-cache-nodes`.

Prometheus alert rule examples are in:

```text
docs/superpowers/runbooks/rdma-cache-alerts.prometheus.yml
```

Operational guidance:

- Page on `JuiceFSRemoteCacheAllReplicasSkipped`; clients are likely falling
  through to L3 object storage and losing L2 latency protection.
- Treat `JuiceFSRemoteCacheNodeDown` as a node-level repair signal; metadata
  remains authoritative in JuiceFS metadata plus object storage, and L2 cache
  entries are disposable.
- In a single L2 node failure, keep the mounted clients running. The health
  manager skips the failed node, active probes detect recovery, and reads fall
  back to L1/L3 when no selected remote replica is healthy.
- If fallback alerts rise while S3/RustFS latency also rises, increase remote
  cache replicas or restore the failed L2 node before adding client concurrency.
