# RDMA Cache Node Health Fallback Design

Date: 2026-07-07

Branch: `feature/rdma-distributed-cache`

## Goal

Add node health detection and automatic fallback for JuiceFS remote cache clients
so a single failed RDMA/L2 cache node does not stall or fail reads. When L2 is
unavailable, the client must quickly skip remote cache and continue with the
existing safe path:

```text
L1 local cache -> L3 object storage
```

Object storage remains authoritative. L2 RDMA cache nodes hold only best-effort
copies of immutable JuiceFS block objects.

## Current State

The branch already has:

- `pkg/cache/remote.Client` with `Get`, `Put`, `Delete`, and `Close`.
- `pkg/cache/remote/httpcache.Client`, which selects replica nodes with
  rendezvous hashing and tries configured replicas for each request.
- `pkg/cache/remote/rdma.Client`, which currently maintains one connection and
  dials the first configured node.
- `pkg/chunk.cachedStore`, which treats remote cache errors as cache misses and
  falls back to object storage.
- `rdma-cache-server`, which can expose a disk-backed L2 cache over HTTP.

The missing pieces are shared node health state, fast skip for known-bad nodes,
RDMA multi-node selection, and clear metadata boundaries for node failure.

## Non-Goals

- Do not make L2 authoritative for file data.
- Do not write L2 cache placement or node health into JuiceFS metadata.
- Do not change JuiceFS metadata format, block keys, slice semantics, or object
  storage durability rules.
- Do not require distributed consensus, gossip, or a central cache directory.
- Do not rebalance existing L2 cache entries when a node fails.
- Do not require native RDMA hardware for unit tests.

## Design Principles

### L2 Is Non-Authoritative

Every L2 entry is a temporary copy of an object storage block. If L2 misses,
times out, returns a short read, or becomes unavailable, the read must continue
through L3 object storage as long as L3 is healthy.

### Stable Placement With Local Health Overlay

Node placement should remain deterministic:

```text
candidate nodes = rendezvous_hash(block_key, configured_nodes)
```

Health state overlays this placement locally in each client. A failed node is
skipped temporarily, but it is not removed from global configuration and does not
change metadata. This avoids a full hash-ring reshuffle and preserves cache hits
when the node recovers.

### Fail Fast

A slow L2 node is worse than a miss. The remote cache layer should return
`remote.ErrUnavailable` promptly when all candidate nodes are unhealthy or
timed out. `pkg/chunk` already converts this into an object storage read.

## Architecture

Add a shared cluster helper under `pkg/cache/remote` or a focused subpackage such
as `pkg/cache/remote/cluster`.

```text
pkg/chunk
  -> remote.Client
     -> httpcache.Client or rdma.Client
        -> cluster.Placement
        -> cluster.HealthManager
        -> transport-specific request execution
```

### Placement

Placement takes a block key and configured nodes and returns an ordered candidate
list.

Rules:

- Use rendezvous hashing over the existing block key.
- Preserve `--remote-cache-replicas`.
- Return at most `replicas` candidates for normal Get/Put/Delete.
- Do not remove a node from placement only because it is down; health filtering
  happens after placement.

For single-replica deployments, a failed owner node means the L2 request is
skipped and the read falls back to L3.

For multi-replica deployments, the client tries healthy candidates in order. If
node A is down and node B has the block, node B can satisfy the read without L3.

### Health Manager

Each client process keeps local in-memory health state:

```text
healthy -> suspect -> down -> probing -> healthy
```

State is per node address. It is not persisted and is not shared through JuiceFS
metadata.

Recommended transition rules:

- `healthy` to `suspect`: one transport failure, timeout, connection reset, or
  unsupported response.
- `suspect` to `down`: consecutive failures reach the configured threshold.
- `down` to `probing`: cooldown expires.
- `probing` to `healthy`: a probe or real request succeeds.
- `probing` to `down`: probe or request fails.

The first implementation can collapse `suspect` and `down` internally if tests
still prove the external behavior: failed nodes are skipped during cooldown and
can recover after a successful probe.

Suggested defaults:

```text
--remote-cache-timeout=50ms
--remote-cache-fail-threshold=3
--remote-cache-node-cooldown=5s
--remote-cache-probe-interval=1s
--remote-cache-probe-timeout=10ms
```

New flags should be optional. If not configured, defaults should preserve current
behavior except for faster skipping of repeatedly failing nodes.

### Passive Detection

Passive detection happens on normal remote cache operations:

- connection refused
- dial failure
- RDMA transport error
- HTTP 5xx
- context deadline exceeded
- short or corrupt response

Any successful request marks the node healthy and resets failure counters.

### Active Detection

Active detection should be lightweight and transport-specific:

- HTTP transport can use `GET /` or a future `/healthz` endpoint.
- RDMA transport can perform a small handshake or ping request.

The first implementation may avoid a background goroutine and instead perform a
probe when a down node's cooldown expires. That keeps the behavior deterministic
and easier to test. A background probe loop belongs in a follow-up hardening
phase if production metrics show that lazy probing delays recovery too much.

## Request Behavior

### Get

1. Resolve candidate nodes from rendezvous hashing.
2. Filter candidates:
   - use `healthy` nodes first
   - allow `probing` nodes when cooldown expired
   - skip `down` nodes whose cooldown has not expired
3. Try candidates in order.
4. On hit, return data.
5. On miss from at least one healthy candidate, return `remote.ErrMiss` after all
   candidates miss or fail.
6. If every candidate is skipped or unavailable, return `remote.ErrUnavailable`.

`pkg/chunk.loadRemote()` already converts `ErrMiss`, timeout, and unavailable
errors into fallback to object storage.

### Put

1. Resolve replica candidates.
2. Write only to healthy or probing candidates.
3. Mark failed nodes through the health manager.
4. Return nil if at least one configured replica accepts the write.
5. Return `remote.ErrUnavailable` if all candidates fail or are skipped.

Remote Put is already best-effort in `pkg/chunk.fillRemote()`, so a full L2 write
failure must not fail reads or writes.

### Delete

Delete remains best-effort:

- Send to healthy/probing candidates.
- Skip down candidates.
- Return nil if any candidate confirms deletion or not-found.
- Return `remote.ErrUnavailable` only when every candidate fails.

Stale L2 entries on down nodes are acceptable because JuiceFS block keys are
immutable. A stale cache entry is harmless if no current metadata references it.

## Metadata Management

### Authoritative Metadata

JuiceFS metadata remains the only source of truth for files, slices, and block
references. RDMA node health must not alter inode metadata, slice metadata, or
object key layout.

### Cache Placement Metadata

Cache placement is derived from:

```text
configured node list + block key + replica count
```

It is not stored centrally. Clients can compute it independently. Health state
only suppresses attempts to unhealthy nodes for a local cooldown window.

### Node-Local Metadata

Each L2 server owns only its local cache index. For the current disk backend,
that means the `.key` and `.data` files under its cache directory. On restart,
the server scans its own directory and rebuilds local cache state.

There is no distributed L2 metadata repair for a single-node failure. If the node
is down, its entries are temporarily unavailable. If replicas exist, another
replica may serve the block. If not, clients fall back to L3.

### Membership Changes

This design covers static membership plus local health. Adding or removing nodes
from `--remote-cache-nodes` is an operational configuration change and can
change placement. That is outside automatic failure handling.

## Observability

Add or extend metrics so operators can see fallback behavior:

- remote cache get results: `hit`, `miss`, `error`, `timeout`, `skipped`
- remote cache put results: `ok`, `error`, `timeout`, `skipped`
- per-node health state gauge
- node transition counter labeled by old/new state and reason
- fallback counter when all L2 candidates are unavailable

Metrics should not require new metadata fields.

## Testing Strategy

### Unit Tests

Add focused tests for the shared cluster helper:

- failed node enters cooldown and is skipped
- skipped single-replica node returns `remote.ErrUnavailable`
- cooldown expiry allows a probe request
- successful probe marks node healthy
- rendezvous placement remains stable while health state changes

Add HTTP client tests:

- single configured node is stopped; second request skips it and returns
  `remote.ErrUnavailable` quickly
- two replicas where one node fails and the other serves a hit
- all replicas down returns `remote.ErrUnavailable`
- Put succeeds when one replica accepts data and one fails

Add RDMA client tests using the existing in-memory dialer:

- client selects multiple configured nodes instead of always node zero
- failed dial marks a node unhealthy
- healthy second replica can serve after first node fails
- cooldown skip avoids repeated dial attempts to the failed node

### Chunk Tests

Keep `pkg/chunk` behavior-level tests:

- remote unavailable falls back to object storage
- remote timeout falls back to object storage
- remote Put failure does not fail object storage reads

These tests prove the L1+L3 fallback remains intact above the transport layer.

### Smoke Test

After the unit-level health manager behavior is stable, extend the RustFS smoke
with an optional case:

1. Start RustFS L3.
2. Start one L2 remote cache server.
3. Mount with remote cache enabled.
4. Stop the L2 server.
5. Read with empty L1.
6. Verify read succeeds from RustFS.

Then stop RustFS too and verify the same read fails, proving the successful read
was L3 fallback rather than hidden local state.

## Acceptance Criteria

- With `--remote-cache=none`, behavior is unchanged.
- With one failed L2 node, a read with healthy L3 succeeds through L1+L3 fallback.
- Repeated reads do not block on the same failed node during cooldown.
- With multiple replicas, a healthy replica can serve when another node is down.
- L2 node health state is local and in-memory only.
- No JuiceFS metadata format change is required.
- Existing RustFS three-tier smoke still passes.
- Unit tests cover single-node failure, replica fallback, and recovery.
