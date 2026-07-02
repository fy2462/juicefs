# RDMA Runtime Transport Design

Date: 2026-07-03

Branch: `feature/rdma-distributed-cache`

## Goal

Phase 8 prepares the RDMA cache implementation for a real native transport without introducing RDMA dependencies into default builds.

The deliverable is a runtime capability model plus a minimal request/response transport abstraction:

```text
rdma.Client
  -> Dialer
  -> Conn.RoundTrip(protocol.Request)
  -> protocol.Response
```

The default build stays dependency-free and reports that native RDMA is unavailable. The `rdma` build tag compiles the same API and keeps the implementation explicitly unsupported until real verbs code is added.

## Architecture

### Capability Detection

Add `Capability()` to `pkg/cache/remote/rdma`.

It returns a value with:

- `Built`: whether this binary was built with native RDMA support.
- `Available`: whether runtime dependencies and devices are usable.
- `Reason`: a human-readable explanation when unavailable.

Default builds return:

```text
Built=false
Available=false
Reason="RDMA remote cache transport is not built"
```

The `rdma` build skeleton returns:

```text
Built=true
Available=false
Reason="native RDMA implementation is not available yet"
```

### Transport Abstraction

Add:

```go
type Conn interface {
    RoundTrip(ctx context.Context, req protocol.Request) (protocol.Response, error)
    Close() error
}

type Dialer interface {
    Dial(ctx context.Context, node string, options Options) (Conn, error)
}
```

The client converts `remote.Client` calls into `protocol.Request` values, calls `Conn.RoundTrip`, and maps the response through `protocol.StatusToError`.

Default builds use a dialer that returns `ErrUnsupported`.

### Test Transport

Tests use an in-memory `roundTripFunc` that calls `rdma.NewServer(backend).HandleFrame`. This validates the client request/response loop without requiring RDMA hardware.

## Error Handling

- Unsupported or failed dial returns `remote.ErrUnavailable` to callers, while direct capability checks expose `ErrUnsupported`.
- `StatusMiss` maps to `remote.ErrMiss`.
- non-OK status maps through `protocol.StatusToError`.
- payload reads copy data before returning to callers.

## Non-Goals

- Do not add cgo, `libibverbs`, `rdma-core`, or hardware probing in this phase.
- Do not implement queue pairs, completion queues, memory registration, or RDMA CM.
- Do not change object storage durability semantics.
