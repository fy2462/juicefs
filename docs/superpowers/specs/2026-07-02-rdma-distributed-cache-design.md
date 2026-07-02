# RDMA Distributed Read Cache Design

Date: 2026-07-02

Branch: `feature/rdma-distributed-cache`

## Goal

Build a JuiceFS Community Edition experiment that strengthens distributed caching and high-performance data-path capabilities, while preserving the open-source architecture and safety model.

目标是增强 JuiceFS 社区版的分布式缓存和高性能能力，作为后续实现依据。

This is not intended to clone the full Enterprise feature set; it is a scoped community experiment that starts with a safe read-through distributed cache.

The first milestone is an optional RDMA-backed distributed read cache:

```text
local disk cache -> RDMA distributed cache -> object storage
```

Object storage remains the authoritative persistence layer. The RDMA cache is a performance layer that can be missed, evicted, restarted, or disabled without changing file-system correctness.

## Non-Goals

- Do not replace S3, OSS, MinIO, or other object stores as the durable data layer.
- Do not implement a 3FS-style persistent NVMe chain-replication storage system in the first milestone.
- Do not change JuiceFS metadata semantics, POSIX behavior, file layout, chunk IDs, slice IDs, or object key format.
- Do not make write success depend on RDMA cache availability.
- Do not introduce write-back distributed cache in the first milestone.
- Do not require RDMA hardware for unit tests or normal builds.

## Background

JuiceFS currently separates metadata from data. Metadata is stored in an external metadata engine, and file blocks are persisted into object storage through `pkg/object.ObjectStorage`. The chunk layer in `pkg/chunk` manages block layout, local cache, upload, download, retries, and prefetch.

The existing read path in `pkg/chunk/cached_store.go` is:

```text
read block
  -> local disk cache hit: return data
  -> local disk cache miss: fetch from object storage
  -> optionally store into local disk cache
```

The proposed feature adds an optional distributed cache lookup between local cache and object storage:

```text
read block
  -> local disk cache hit: return data
  -> RDMA cache hit: return data and optionally fill local cache
  -> RDMA cache miss/error: fetch from object storage
  -> optionally publish fetched block into RDMA cache
```

This mirrors the performance motivation of RDMA systems such as 3FS, but keeps JuiceFS' object-storage-backed durability model.

## Architecture

### Components

1. `pkg/cache/remote`

   Defines a small cache client interface independent of RDMA implementation details:

   ```go
   type Client interface {
       Get(ctx context.Context, key string, off, size int) (io.ReadCloser, error)
       Put(ctx context.Context, key string, data []byte) error
       Delete(ctx context.Context, key string) error
       Close() error
   }
   ```

   The package also defines standard errors such as `ErrMiss`, `ErrUnavailable`, and `ErrDisabled`.

2. `pkg/cache/remote/mock`

   Provides an in-memory implementation for tests and non-RDMA development. This is the first implementation used by unit tests.

3. `pkg/cache/remote/rdma`

   Provides the eventual RDMA transport implementation. It is guarded by build tags so the default build does not require RDMA libraries.

4. `cmd/rdma-cache-server`

   Runs cache nodes that hold block objects on local SSD or memory and serve RDMA read requests. The first design only requires data-plane operations for immutable JuiceFS block keys.

5. `pkg/chunk` integration

   Extends `chunk.Config` with remote cache settings and updates the block miss path in `rSlice.ReadAt` / `cachedStore.load` so remote cache lookup occurs before object storage fetch.

### Cache Key

The remote cache key is the existing JuiceFS block object key, for example:

```text
chunks/<dir>/<slice_id>_<block_index>_<block_size>
```

The key already includes the slice ID and block size. JuiceFS slices are immutable after upload, so the cache can treat a block key as immutable.

### Data Ownership

Object storage is authoritative. The remote cache owns only temporary copies of object blocks.

If remote cache data is unavailable, expired, corrupted, or slow, the client falls back to object storage.

### Discovery

The first milestone uses static configuration:

```text
--remote-cache=rdma
--remote-cache-nodes=host1:port,host2:port
--remote-cache-timeout=50ms
--remote-cache-replicas=1
```

Node selection uses rendezvous hashing over the block key. This keeps client behavior deterministic and avoids adding a new metadata dependency.

Dynamic membership, health gossip, and rebalancing are deferred.

## Read Data Flow

1. Application reads a file through FUSE or SDK.
2. `pkg/vfs` resolves file metadata and slice layout as it does today.
3. `pkg/chunk` maps the read to block keys.
4. For each block:
   - Check local disk cache.
   - If missed and remote cache is enabled, ask the selected RDMA cache node.
   - If RDMA cache hits, return the data and optionally fill local disk cache.
   - If RDMA cache misses or errors, fetch from object storage.
   - After object storage fetch succeeds, asynchronously publish the full block to remote cache when the block is cacheable.

Remote cache failures are counted in metrics but do not fail the read unless object storage also fails.

## Write Data Flow

The first milestone does not change write commit semantics.

1. `pkg/chunk` writes blocks using the existing upload path.
2. Object storage upload success remains the condition for durable write success.
3. After upload succeeds, the client may publish the block to remote cache as a best-effort operation.
4. Remote cache publish failure is logged and counted, but ignored for file-system correctness.

This avoids dirty distributed cache state and crash-recovery requirements in the first milestone.

## Consistency Model

Remote cache entries are safe because JuiceFS block objects are immutable. Overwrite operations create new slice/block objects and update metadata to point at the new objects.

The cache does not need invalidation for normal overwrites because old keys remain old data and new reads use new keys from metadata.

Delete can be best-effort:

- When JuiceFS deletes a block object, the client may send remote cache delete.
- If delete fails, the stale cache entry is harmless because no current metadata should reference it.
- Capacity pressure or TTL eventually removes old entries.

## Failure Handling

Remote cache must fail fast.

- Timeout: fall back to object storage.
- Node unavailable: mark the node unhealthy for a short backoff period and fall back.
- Corrupt or short read: drop the cache response and fall back.
- Publish failure: record metric and continue.
- Server restart: cache is cold; clients fall back to object storage.

The first milestone should prefer lower latency over maximum cache hit rate. A slow cache is worse than a miss.

## Configuration

Proposed mount options:

```text
--remote-cache=none|mock|rdma
--remote-cache-nodes=<host:port>[,<host:port>...]
--remote-cache-timeout=50ms
--remote-cache-replicas=1
--remote-cache-fill-local=true
--remote-cache-fill-remote=true
```

Defaults keep current JuiceFS behavior:

```text
--remote-cache=none
```

Server options:

```text
juicefs rdma-cache-server \
  --listen=<host:port> \
  --cache-dir=/var/jfsRdmaCache \
  --cache-size=1T \
  --device=<rdma-device>
```

The mock transport does not require a server.

## Metrics

Add Prometheus counters and histograms:

- `juicefs_remote_cache_gets_total{result="hit|miss|error|timeout"}`
- `juicefs_remote_cache_get_bytes_total{result="hit"}`
- `juicefs_remote_cache_puts_total{result="ok|error|timeout"}`
- `juicefs_remote_cache_put_bytes_total{result="ok"}`
- `juicefs_remote_cache_get_seconds`
- `juicefs_remote_cache_put_seconds`
- `juicefs_remote_cache_fallbacks_total`

Metrics should make it obvious whether RDMA cache improves reads or only adds overhead.

## Testing Strategy

1. Unit tests with mock remote cache:
   - local cache hit bypasses remote cache
   - local miss and remote hit avoids object storage
   - remote miss falls back to object storage
   - remote timeout falls back to object storage
   - object storage fetch asynchronously fills remote cache
   - delete is best-effort

2. Integration tests without RDMA hardware:
   - mount with `--remote-cache=mock`
   - read a file twice and verify the second read can come from remote cache
   - inject mock failures and verify reads still succeed through object storage

3. RDMA transport tests:
   - build-tagged and skipped unless RDMA device/configuration is present
   - verify connection setup, block transfer, timeout, and server restart fallback

4. Benchmarks:
   - cold read from object storage
   - warm read from local cache
   - warm read from remote cache
   - parallel multi-client read of shared dataset

## Implementation Phases

### Phase 1: Interface and Mock Cache

- Add remote cache interface and errors.
- Add config fields and mount flags.
- Insert remote cache lookup between local cache and object storage.
- Add mock cache unit tests.
- Add metrics.

### Phase 2: Cache Server Skeleton

- Add `rdma-cache-server` command with disk-backed storage and non-RDMA mock/TCP development transport.
- Add server-side eviction and capacity controls.
- Add static node selection and health backoff in the client.

### Phase 3: RDMA Transport

- Add build-tagged RDMA client/server implementation.
- Implement fixed-size block transfer optimized for JuiceFS block objects.
- Add RDMA-specific timeout and memory registration handling.
- Add hardware-gated integration tests.

### Phase 4: Operational Hardening

- Add node health metrics.
- Add better server discovery options.
- Add cache warming tools.
- Add documentation for AI training dataset workloads.

## Open Decisions

1. RDMA library choice for Go integration:
   - cgo wrapper around `libibverbs`
   - a maintained Go RDMA binding if one is acceptable
   - external helper process that exposes a local protocol to Go

2. Server storage backend:
   - memory only for initial transport testing
   - disk-backed cache using JuiceFS-style cache file layout
   - embedded key-value index with files for block payloads

3. Maximum block size:
   - default JuiceFS block size is typically 4 MiB
   - larger configured blocks may need chunked RDMA transfer

4. Security:
   - first milestone can assume a trusted private RDMA network
   - later milestones need authentication and optional encryption for non-isolated deployments

## Acceptance Criteria

The design is ready for implementation when:

- Current behavior remains unchanged with `--remote-cache=none`.
- The mock remote cache proves the read-through behavior in tests.
- Remote cache errors never make a read fail if object storage is healthy.
- No metadata format changes are required.
- No RDMA hardware is required for normal development or CI.
- The implementation path is incremental enough to stop after Phase 1 with a useful tested abstraction.
