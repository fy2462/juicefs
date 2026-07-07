# RDMA Native Productionization Design

Date: 2026-07-07

Branch: `feature/rdma-distributed-cache`

## Goal

Finish the remaining productionization work for RDMA distributed cache:

1. Real RDMA verbs data path.
2. `rdma-cache-server --transport=rdma` native transport.
3. End-to-end RDMA smoke under open-rdma mock mode.
4. Layered CI gates.
5. Performance and stress tests.
6. Alerting examples for operators.
7. Stable configuration documentation.

This phase turns the current protocol/client/server boundary into a real native
transport while preserving the existing safety invariant:

```text
L1 local cache -> L2 RDMA remote cache -> L3 S3/RustFS object storage
```

L3 remains authoritative. RDMA cache entries remain best-effort copies.

## Current State

The branch already has:

- `pkg/cache/remote/rdma.Client` with placement, health, active probe, and
  `Dialer`/`Conn` interfaces.
- `pkg/cache/remote/rdma/protocol` with `PING`, `GET`, `PUT`, and `DELETE`
  request/response semantics.
- `pkg/cache/remote/rdma.Server` frame executor over a backend
  `remote.Client`.
- `rdma-cache-server --transport=http` for HTTP L2 smoke.
- `rdma-cache-server --transport=rdma` returning `rdma.ErrUnsupported`.
- `-tags rdma` capability gate that verifies `OPEN_RDMA_DRIVER`.
- `hack/open-rdma-smoke-test.sh` proving the open-rdma mock environment is
  ready.

The missing part is the native data path between `rdma.Client` and
`rdma.Server`.

## Design Principles

### Keep the Existing Remote Cache Contract

The native transport must implement the existing `rdma.Conn` contract:

```go
type Conn interface {
    RoundTrip(ctx context.Context, req protocol.Request) (protocol.Response, error)
    Close() error
}
```

Placement, health, active probe, retry, and L1+L3 fallback stay unchanged. This
limits the RDMA work to a transport implementation and keeps tests focused.

### Default Builds Stay Dependency-Free

Without `-tags rdma`, the code must not require cgo, libibverbs headers,
open-rdma libraries, kernel modules, or RDMA devices.

Native verbs files use:

```go
//go:build rdma && linux && cgo
```

The existing `rdma` tag file remains the capability boundary. If cgo is not
enabled, `-tags rdma` can still compile the protocol and readiness tests, but
native transport tests are skipped with an explicit reason.

### Start With Framed Messages, Not Zero-Copy

The first native data path sends framed protocol payloads through RDMA buffers.
It does not try to expose JuiceFS block buffers directly as registered memory.
Zero-copy and memory registration pooling are later performance work.

The first implementation optimizes for correctness and observability:

- one request at a time per connection
- bounded frame size
- deterministic timeout behavior
- clean close and resource release
- protocol compatibility with existing in-memory tests

## Native Transport Architecture

Add a build-tagged package boundary under `pkg/cache/remote/rdma/native`.

```text
pkg/cache/remote/rdma
  Client
    -> Dialer
       -> native.Dialer       (-tags rdma,linux,cgo)
          -> native.Conn
             -> protocol.Request/Response frames over verbs

cmd/rdma-cache-server
  --transport=rdma
    -> rdma.ListenAndServe
       -> native.Server       (-tags rdma,linux,cgo)
          -> rdma.Server.HandleFrame
```

`rdma` package owns the public API. The native subpackage owns cgo and verbs
details so normal package tests do not import cgo code.

## Connection Setup

Use a TCP control plane for initial connection setup, matching the open-rdma
examples:

1. Client dials the server's configured `host:port` over TCP.
2. Both sides initialize verbs resources:
   - device
   - protection domain
   - memory region
   - completion queue
   - RC queue pair
3. Both sides exchange QP number, rkey, and buffer address over TCP.
4. Both sides transition QP through INIT, RTR, and RTS.
5. TCP remains open only as the connection owner and shutdown path.

The configured node address is reused for the TCP control plane. RDMA transfer
uses the open-rdma/mock device selected by native options and environment.

## Frame Protocol

Native RDMA sends the existing JSON protocol frame with a length prefix:

```text
uint32 big-endian request_length
request JSON bytes
uint32 big-endian response_length
response JSON bytes
```

This preserves compatibility with `protocol.EncodeRequest` and
`protocol.DecodeResponse`.

The first implementation uses one registered send/receive buffer per connection
with a fixed maximum frame size:

```text
default: 4 MiB
minimum: 64 KiB
operator override: JFS_RDMA_MAX_FRAME_BYTES
```

If a block is larger than the frame limit, the client returns
`remote.ErrUnavailable`, which triggers the existing object storage fallback.
Chunked RDMA transfer is a follow-up after the framed path is stable.

## Server Transport

Expose a native server entry point from `pkg/cache/remote/rdma`:

```go
type ServeOptions struct {
    Listen        string
    Backend       remote.Client
    MaxFrameBytes int
}

func ListenAndServe(ctx context.Context, options ServeOptions) error
```

`cmd/rdma_cache_server.go` changes:

- `transport=http`: unchanged.
- `transport=rdma` without native build: returns `rdma.ErrUnsupported`.
- `transport=rdma` with native build: starts `rdma.ListenAndServe`.

The server should support one connection per accepted TCP control connection.
Each connection handles one request at a time. A later performance phase can add
shared CQ polling and request pipelining.

## Error Handling

Transport errors map to `remote.ErrUnavailable` unless they are explicit local
configuration errors.

Examples:

- missing RDMA device: `ErrUnsupported` or clear native setup error
- failed QP transition: unavailable
- CQ timeout: unavailable
- frame too large: unavailable
- malformed protocol frame: bad response status
- backend miss: `StatusMiss`

Client health handling already marks failed nodes and falls back to other
replicas or L3.

## End-to-End Smoke

Add a new script:

```text
hack/rdma-native-smoke-test.sh
```

It runs only when open-rdma is available:

```sh
OPEN_RDMA_DRIVER=/path/to/open-rdma-driver \
PATH=/usr/local/go/bin:$PATH \
make test.rdma-native-smoke
```

Smoke stages:

1. Verify open-rdma readiness with `hack/open-rdma-smoke-test.sh --strict`.
2. Build JuiceFS with `-tags rdma`.
3. Start `juicefs rdma-cache-server --transport=rdma`.
4. Run a small native client operation through `rdma.Client`:
   - `PING`
   - `PUT`
   - `GET`
   - `DELETE`
5. Run a minimal mounted JuiceFS read path when stable enough:
   - L1 empty
   - RDMA L2 enabled
   - RustFS L3 available
   - verify L2 hit after fill

The first smoke may stop at direct remote cache operations. The mounted read
path is added once native client/server close behavior is stable.

## CI Layers

Add explicit CI layers rather than making every runner require RDMA:

```text
default unit:
  go test ./pkg/cache/remote/... ./pkg/chunk

rdma tag compile:
  go test -tags rdma ./pkg/cache/remote/rdma

rustfs semantic smoke:
  make test.three-tier-cache-rustfs

manual/native RDMA:
  make test.rdma-native-smoke
```

GitHub Actions should include default unit and `-tags rdma` compile coverage.
Native open-rdma smoke should be opt-in or manual until the runner has the
kernel module, device, huge pages, and mock route.

## Performance and Stress Tests

Add a benchmark/stress harness after native Ping/Get/Put/Delete works:

```text
hack/rdma-cache-stress.sh
```

Coverage:

- frame sizes: 4 KiB, 64 KiB, 1 MiB, max frame
- concurrency: 1, 4, 16, 64 clients
- operations: get hit, get miss, put, delete, ping
- failure injection: server restart, one-node failure with replicas=2
- output: p50/p95/p99 latency, ops/sec, errors/sec

The benchmark should compare:

- HTTP L2 baseline
- RDMA native L2
- L3 RustFS fallback

## Alerts

Publish Prometheus alert examples for:

- node down for more than 2 minutes
- all replicas skipped for a node set
- repeated probe failures
- remote cache fallbacks increasing while L3 latency rises
- native RDMA server restart loop

Metric names reuse existing health metrics:

```text
juicefs_remote_cache_node_down
juicefs_remote_cache_node_failures_total
juicefs_remote_cache_node_recoveries_total
juicefs_remote_cache_node_skips_total
juicefs_remote_cache_node_probe_total
juicefs_remote_cache_fallbacks_total
```

## Configuration Documentation

Document stable defaults and production recommendations:

```text
--remote-cache-transport=rdma
--remote-cache-nodes=<host:port>[,<host:port>...]
--remote-cache-replicas=2
--remote-cache-timeout=50ms
--remote-cache-fail-threshold=3
--remote-cache-node-cooldown=5s
--remote-cache-probe-interval=1s
--remote-cache-probe-timeout=10ms
```

Native-only environment:

```text
OPEN_RDMA_DRIVER=/path/to/open-rdma-driver
JFS_RDMA_DEVICE_INDEX=0
JFS_RDMA_MAX_FRAME_BYTES=4194304
JFS_RDMA_CQ_TIMEOUT=50ms
```

## Acceptance Criteria

The phase is complete only when current-state evidence proves all items:

1. `go test ./pkg/cache/remote/... ./pkg/chunk` passes in default build.
2. `go test -tags rdma ./pkg/cache/remote/rdma` passes.
3. `juicefs rdma-cache-server --transport=rdma` starts under native build.
4. Direct native RDMA `PING`, `PUT`, `GET`, and `DELETE` smoke passes with
   open-rdma mock mode.
5. Failure of one RDMA L2 node falls back to another replica or L3.
6. `make test.three-tier-cache-rustfs` still passes.
7. CI docs/workflow include default, rdma-tag, RustFS, and native smoke layers.
8. Stress harness exists and reports latency/throughput/error summaries.
9. Alert rules are documented.
10. Operator configuration docs include RDMA native flags and environment.
