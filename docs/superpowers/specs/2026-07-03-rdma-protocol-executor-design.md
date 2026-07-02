# RDMA Protocol Executor Design

Date: 2026-07-03

Branch: `feature/rdma-distributed-cache`

## Goal

Phase 7 connects the RDMA protocol boundary to the cache backend semantics.

Phase 6 defined request and response frames. Phase 7 adds a protocol executor that turns those frames into `remote.Client` operations:

```text
protocol.Request
  -> executor.Handle
  -> remote.Client Get / Put / Delete
  -> protocol.Response
```

This keeps the future RDMA transport focused on moving bytes. It should not reimplement cache behavior.

## Architecture

Add `pkg/cache/remote/rdma/protocol.Executor`.

The executor owns:

- opcode dispatch
- `GET` range reads
- `PUT` payload writes
- `DELETE` requests
- mapping `remote.ErrMiss` to `StatusMiss`
- mapping all backend availability and malformed request errors to protocol statuses

The executor depends only on `remote.Client`. It is usable by tests, future RDMA client/server code, and any in-memory transport harness.

## Error Model

- `GET` hit returns `StatusOK` with payload.
- `GET` miss returns `StatusMiss`.
- `PUT` success returns `StatusOK`.
- `DELETE` success returns `StatusOK`.
- unsupported opcode returns `StatusBadRequest`.
- backend errors other than miss return `StatusUnavailable`.

Malformed JSON remains handled by `DecodeRequest`; executor only handles syntactically valid requests.

## Testing

Normal builds test:

- `GET` hit with range returns expected bytes.
- `GET` miss returns `StatusMiss`.
- `PUT` then `GET` returns written bytes.
- `DELETE` removes data.
- unknown opcode returns `StatusBadRequest`.
- backend error returns `StatusUnavailable`.

No test requires RDMA hardware.

## Non-Goals

- Do not implement real RDMA send/receive loops in this phase.
- Do not change the HTTP remote cache protocol.
- Do not change JuiceFS chunk read/write correctness semantics.
