# Three-Tier Cache with RustFS S3 Design

Date: 2026-07-06

Branch: `feature/rdma-distributed-cache`

## Goal

Build a repeatable local validation path for the intended three-tier data path:

```text
L1 local disk cache -> L2 RDMA remote cache -> L3 S3 object storage
```

`rustfs` provides the local S3-compatible L3 environment. The current JuiceFS object
storage contract remains authoritative. The L2 RDMA layer is still a cache layer:
it may be missed, restarted, evicted, or disabled without changing file-system
correctness.

This phase proves the cache hierarchy and failure behavior before implementing the
real native RDMA verbs transport.

## Current State

The branch already has the core remote-cache pieces:

- `pkg/cache/remote` defines the cache client interface and standard errors.
- `pkg/cache/remote/mock` provides an in-memory test implementation.
- `pkg/cache/remote/httpcache` provides a development transport.
- `pkg/cache/remote/diskcache` provides a disk-backed server backend.
- `pkg/cache/remote/rdma` provides the protocol/client boundary and an unsupported
  native transport skeleton.
- `cmd/rdma-cache-server` can serve a remote cache over HTTP with memory or disk
  backend.
- `pkg/chunk` already checks remote cache after local cache miss and before object
  storage reads.
- `hack/open-rdma-smoke-test.sh` validates whether open-rdma mock mode is usable
  for future native RDMA tests.

The missing piece is a local, repeatable integration environment that demonstrates
L1/L2/L3 behavior against an S3-compatible object store.

## Terminology

- **L1 local disk cache:** the existing JuiceFS cache managed by `pkg/chunk`.
- **L2 RDMA remote cache:** remote cache server nodes. In this phase HTTP transport
  is used as a semantic stand-in; the same `remote.Client` and RDMA protocol
  boundary remain the target for native RDMA transport.
- **L3 S3 object storage:** authoritative object storage. Local tests use rustfs.

The name "RDMA remote storage" means the L2 cache server stores remote cached block
copies on memory or disk. It does not replace S3 durability in this phase.

## Architecture

### RustFS Environment

Add a local harness that starts rustfs with deterministic credentials, endpoint,
and bucket setup. The harness should be usable by developers and CI-like local
checks without real cloud credentials.

The harness owns only test infrastructure:

- start rustfs on a local endpoint
- create or prepare a test bucket
- export the object-storage settings needed by `juicefs format`
- clean up temporary data after the test

The exact launcher may be a shell script, a compose file, or both. The first
implementation should prefer the smallest path that is reliable in this repository.

### Three-Tier Test Topology

The local topology is:

```text
juicefs mount/test process
  L1: --cache-dir=<tmp local cache dir>
  L2: --remote-cache=rdma --remote-cache-transport=http --remote-cache-nodes=<server>
  L3: --storage=s3 --bucket=<rustfs bucket URL>

rdma-cache-server
  --transport=http
  --cache-dir=<tmp remote cache dir>

rustfs
  S3-compatible endpoint
```

Using HTTP for L2 in this phase is intentional. It proves cache ordering,
fallback, fill, and disk-backed remote-server behavior without blocking on
native RDMA verbs integration.

### Read Flow

The expected read flow is:

1. Check L1 local disk cache.
2. On L1 miss, query L2 remote cache.
3. On L2 hit, return data and optionally fill L1.
4. On L2 miss, timeout, or unavailable, read from L3 rustfs/S3.
5. After a successful L3 full-block read, best-effort fill L2 if configured.
6. Normal local cache rules decide whether L1 is filled.

### Write Flow

Writes remain durable only after object storage upload succeeds.

After a block is uploaded to L3, L2 fill may happen as best effort. Failure to
fill L2 is logged and counted but does not fail the write.

## Test Milestones

### M1: RustFS S3 Baseline

Add a local rustfs-backed smoke test that proves JuiceFS can format, write, read,
unmount, and remount using S3-compatible storage.

Acceptance criteria:

- no real cloud S3 credentials are needed
- object storage is `s3` pointed at rustfs
- a write survives remount
- the test is skipped or reports a clear prerequisite error if rustfs cannot be
  started

### M2: L1 Cache Baseline

Add a focused test that proves local disk cache behavior with rustfs as L3.

Acceptance criteria:

- first read can populate local cache from L3
- second read can be served from L1 without requiring L2
- cache directories are temporary and isolated per test

### M3: L2 Remote Cache Baseline

Add a focused test that proves `rdma-cache-server` with disk backend can serve
remote cache entries independently of the mounted client's local cache.

Acceptance criteria:

- L1 is empty
- L2 contains the block
- read returns data without fetching from L3 where instrumentation can prove it
- remote cache server restart preserves disk-backed entries

### M4: Full Three-Tier Path

Add an integration test or scripted smoke that proves the full hierarchy:

- cold read: L1 miss -> L2 miss -> L3 hit
- remote warm read: L1 miss -> L2 hit -> optional L1 fill
- local warm read: L1 hit -> no L2/L3 dependency
- L2 unavailable: read falls back to L3

Acceptance criteria:

- rustfs is the L3 object store
- `rdma-cache-server` is the L2 server
- local cache directory is the L1 cache
- the test can be run repeatedly without shared state
- failures identify which tier failed

### M5: Native RDMA Transport

Only after M1-M4 are stable, implement the build-tagged native RDMA `Dialer` and
`Conn` backed by open-rdma mock mode.

Acceptance criteria:

- default builds remain dependency-free
- `-tags rdma` builds do not affect non-RDMA tests
- the existing open-rdma smoke test is the prerequisite gate
- native RDMA round trips use the same request/response semantics already tested
  by HTTP and in-memory transports

## Observability

Use existing metrics where possible:

- `remote_cache_gets_total{result="hit|miss|error|timeout"}`
- `remote_cache_puts_total{result="ok|error|timeout"}`
- `remote_cache_fallbacks_total`
- local cache hit/miss metrics
- object request metrics

If tests need stronger tier attribution, add test-only instrumentation or small
server-side counters before adding user-facing metrics. The first goal is
correctness of tier ordering, not a new metrics surface.

## Non-Goals

- Do not make L2 authoritative for user data.
- Do not add write-back semantics to L2.
- Do not require RDMA hardware for M1-M4.
- Do not introduce metadata format changes.
- Do not replace existing object storage tests.
- Do not tune performance before correctness and fallback behavior are covered.

## Open Decisions

1. **RustFS launcher:** choose between a shell script, compose file, or direct
   binary invocation based on what is easiest to run in this repository.
2. **S3 setup method:** decide whether the harness creates buckets through rustfs
   tooling, S3 API calls, or a preconfigured rustfs data directory.
3. **Tier attribution:** decide whether to use existing metrics, server counters,
   or test doubles to prove which tier served each read.
4. **CI scope:** decide whether rustfs-backed tests run in normal CI, nightly CI,
   or only as an opt-in local smoke test at first.

## Recommended Implementation Order

1. Add the rustfs S3 baseline smoke.
2. Add a local-cache baseline using rustfs.
3. Add remote cache server start/stop helpers for tests.
4. Add the full three-tier scripted smoke using HTTP L2 transport.
5. Document the exact command sequence for developers.
6. Start native RDMA transport only after the three-tier HTTP path is stable.

This order keeps the correctness model observable while avoiding a hard dependency
on RDMA verbs before the cache hierarchy itself is proven.
