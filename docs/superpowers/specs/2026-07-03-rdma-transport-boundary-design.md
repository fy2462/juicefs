# RDMA Transport Boundary Design

Date: 2026-07-03

Branch: `feature/rdma-distributed-cache`

## Goal

Phase 6 prepares the remote cache for a real RDMA implementation without making normal builds depend on RDMA hardware, kernel modules, or `rdma-core` headers.

The deliverable is an explicit protocol boundary and build-tagged native skeleton:

```text
pkg/chunk
  -> remote.Client
  -> pkg/cache/remote/rdma
  -> protocol package
  -> native implementation only when built with -tags rdma
```

Object storage remains authoritative. RDMA cache failures continue to fall back to object storage.

## Architecture

### Protocol Package

Add `pkg/cache/remote/rdma/protocol` for transport-independent wire semantics.

The protocol defines:

- Opcodes: `GET`, `PUT`, `DELETE`
- Status codes: `OK`, `MISS`, `UNAVAILABLE`, `BAD_REQUEST`
- Request fields: operation, key, offset, size, payload
- Response fields: status, payload

The first encoding uses JSON plus base64 for payloads. This is not the final high-performance RDMA wire format. It is a stable semantic contract that lets tests validate request/response behavior before native RDMA verbs are wired in.

### RDMA Package

The existing default `pkg/cache/remote/rdma` implementation remains dependency-free and returns `ErrUnsupported`.

Add a build-tagged file:

```go
//go:build rdma
```

This file exposes the same `NewClient(options Options) remote.Client` entry point under RDMA builds. The first native skeleton can still return `ErrUnsupported`, but the file boundary is now ready for future `rdma-core` integration.

### Server CLI

Extend `juicefs rdma-cache-server` with:

```text
--transport=http|rdma
```

Default is `http`. In normal builds, `--transport=rdma` fails fast with a clear unsupported error. This avoids silently starting an HTTP server when an operator explicitly requested RDMA.

## Error Handling

Protocol status maps to cache errors:

- `MISS` -> `remote.ErrMiss`
- `UNAVAILABLE` -> `remote.ErrUnavailable`
- `BAD_REQUEST` -> `remote.ErrUnavailable`
- malformed frames -> `remote.ErrUnavailable`

The chunk read path already treats these errors as remote cache failures and falls back to object storage.

## Testing

Normal builds test:

- protocol request/response encode/decode round trips
- protocol status-to-error mapping
- default `rdma.NewClient` returns `rdma.ErrUnsupported`
- `rdma-cache-server --transport=http` keeps working
- `rdma-cache-server --transport=rdma` returns `rdma.ErrUnsupported`

No test requires RDMA hardware.

## Non-Goals

- Do not implement real queue pairs, memory registration, completion queues, or RDMA CM in this phase.
- Do not change cache consistency semantics.
- Do not make reads fail when RDMA cache is unavailable and object storage is healthy.
- Do not introduce a non-standard dependency into default builds.
