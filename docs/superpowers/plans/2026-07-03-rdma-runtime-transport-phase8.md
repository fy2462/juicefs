# RDMA Runtime Transport Phase 8 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add RDMA capability detection and a minimal native transport request/response abstraction while keeping default builds dependency-free.

**Architecture:** `pkg/cache/remote/rdma` gains `Capability`, `Dialer`, and `Conn`. The RDMA client maps `remote.Client` operations into `protocol.Request` round trips and uses `protocol.StatusToError` for responses. Default and `rdma` tagged builds both compile, but still fail clearly until real native RDMA verbs are implemented.

**Tech Stack:** Go standard library, existing `remote.Client`, existing RDMA protocol executor/server skeleton, Go unit tests.

---

## Task 1: Capability Detection

**Files:**
- Modify: `pkg/cache/remote/rdma/types.go`
- Modify: `pkg/cache/remote/rdma/rdma.go`
- Modify: `pkg/cache/remote/rdma/native_rdma.go`
- Modify: `pkg/cache/remote/rdma/rdma_test.go`

- [ ] **Step 1: Add tests**

Add `TestCapabilityDefaultBuild` asserting default build has `Built=false`, `Available=false`, and non-empty `Reason`.

- [ ] **Step 2: Implement capability**

Add:

```go
type CapabilityInfo struct {
    Built bool
    Available bool
    Reason string
}
```

Default `rdma.go` returns not built. `native_rdma.go` returns built but unavailable.

- [ ] **Step 3: Verify and commit**

```bash
GOTOOLCHAIN=go1.25.0 GOPROXY=https://goproxy.cn,direct /Users/fy2462/sdk/go1.26.2/bin/go test ./pkg/cache/remote/rdma
GOTOOLCHAIN=go1.25.0 GOPROXY=https://goproxy.cn,direct /Users/fy2462/sdk/go1.26.2/bin/go test -tags rdma ./pkg/cache/remote/rdma
git add pkg/cache/remote/rdma docs/superpowers/specs/2026-07-03-rdma-runtime-transport-design.md docs/superpowers/plans/2026-07-03-rdma-runtime-transport-phase8.md
git commit -m "feat: add RDMA runtime capability detection"
```

## Task 2: Dialer and Client Round Trip

**Files:**
- Modify: `pkg/cache/remote/rdma/types.go`
- Modify: `pkg/cache/remote/rdma/rdma.go`
- Modify: `pkg/cache/remote/rdma/native_rdma.go`
- Modify: `pkg/cache/remote/rdma/rdma_test.go`

- [ ] **Step 1: Add tests**

Add tests for:
- `TestClientRoundTripGet`
- `TestClientRoundTripPutDelete`
- `TestClientRoundTripMiss`

Use a test dialer whose connection calls `NewServer(mock.NewClient()).HandleFrame`.

- [ ] **Step 2: Implement transport abstractions**

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

Extend `Options` with `Dialer Dialer`.

Implement a client that:
- selects the first configured node
- dials lazily on first operation
- encodes `Get`, `Put`, and `Delete` as protocol requests
- returns payload as `io.NopCloser(bytes.NewReader(resp.Payload))`
- maps status with `protocol.StatusToError`
- closes the connection on `Close`

If no dialer is configured, use default unsupported dialer.

- [ ] **Step 3: Verify and commit**

```bash
GOTOOLCHAIN=go1.25.0 GOPROXY=https://goproxy.cn,direct /Users/fy2462/sdk/go1.26.2/bin/go test ./pkg/cache/remote/rdma
GOTOOLCHAIN=go1.25.0 GOPROXY=https://goproxy.cn,direct /Users/fy2462/sdk/go1.26.2/bin/go test -tags rdma ./pkg/cache/remote/rdma
git add pkg/cache/remote/rdma
git commit -m "feat: add RDMA client round trip abstraction"
```

## Final Verification

Run:

```bash
GOTOOLCHAIN=go1.25.0 GOPROXY=https://goproxy.cn,direct /Users/fy2462/sdk/go1.26.2/bin/go test ./pkg/cache/remote/... ./pkg/chunk -run 'TestRemoteCache|TestStoreRetry|TestStoreDefault|TestFillCache|TestClient|TestExecutor|TestStatusToError|TestServerHandleFrame|TestCapability'
GOTOOLCHAIN=go1.25.0 GOPROXY=https://goproxy.cn,direct /Users/fy2462/sdk/go1.26.2/bin/go test -tags rdma ./pkg/cache/remote/rdma
GOTOOLCHAIN=go1.25.0 GOPROXY=https://goproxy.cn,direct /Users/fy2462/sdk/go1.26.2/bin/go test ./cmd -run 'TestHandleSysMountArgs|TestRDMACacheServer'
git diff --check
git status --short --branch
```
