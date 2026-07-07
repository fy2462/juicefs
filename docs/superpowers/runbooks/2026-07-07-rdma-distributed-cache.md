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
PATH=/usr/local/go/bin:$PATH go test -tags rdma ./pkg/cache/remote/rdma
```

The `rdma` build tag now exposes a native capability gate. It does not add a cgo
or libibverbs data path yet; it verifies that the open-rdma checkout boundary is
present and keeps protocol/client tests hardware-independent.

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
