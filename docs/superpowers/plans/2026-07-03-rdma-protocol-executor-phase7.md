# RDMA Protocol Executor Phase 7 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a protocol executor that maps RDMA cache protocol frames to `remote.Client` backend operations.

**Architecture:** Keep the executor in `pkg/cache/remote/rdma/protocol` so future RDMA transports can share it. The executor depends only on `remote.Client`, returns `protocol.Response`, and keeps all cache correctness/fallback semantics aligned with the existing remote cache interface.

**Tech Stack:** Go standard library, JuiceFS `pkg/cache/remote`, existing mock remote cache, Go unit tests.

---

## Task 1: Protocol Executor

**Files:**
- Modify: `pkg/cache/remote/rdma/protocol/protocol.go`
- Modify: `pkg/cache/remote/rdma/protocol/protocol_test.go`

- [ ] **Step 1: Write executor tests**

Add tests for:
- `TestExecutorGetHitRange`
- `TestExecutorGetMiss`
- `TestExecutorPutThenGet`
- `TestExecutorDelete`
- `TestExecutorBadRequest`
- `TestExecutorBackendUnavailable`

- [ ] **Step 2: Implement executor**

Add:

```go
type Executor struct {
    Backend remote.Client
}

func (e Executor) Handle(ctx context.Context, req Request) Response
```

Rules:
- Missing backend returns `StatusUnavailable`.
- `OpGet` calls `Backend.Get(ctx, req.Key, req.Off, req.Size)`, reads all data, and returns `StatusOK`.
- `remote.ErrMiss` maps to `StatusMiss`.
- Other `Get` errors or read errors map to `StatusUnavailable`.
- `OpPut` calls `Backend.Put(ctx, req.Key, req.Payload)`.
- `OpDelete` calls `Backend.Delete(ctx, req.Key)`.
- Unknown op returns `StatusBadRequest`.

- [ ] **Step 3: Verify and commit**

```bash
GOTOOLCHAIN=go1.25.0 GOPROXY=https://goproxy.cn,direct /Users/fy2462/sdk/go1.26.2/bin/go test ./pkg/cache/remote/rdma/protocol
git add pkg/cache/remote/rdma/protocol docs/superpowers/specs/2026-07-03-rdma-protocol-executor-design.md docs/superpowers/plans/2026-07-03-rdma-protocol-executor-phase7.md
git commit -m "feat: add RDMA protocol executor"
```

## Final Verification

Run:

```bash
GOTOOLCHAIN=go1.25.0 GOPROXY=https://goproxy.cn,direct /Users/fy2462/sdk/go1.26.2/bin/go test ./pkg/cache/remote/... ./pkg/chunk -run 'TestRemoteCache|TestStoreRetry|TestStoreDefault|TestFillCache|TestClient|TestExecutor|TestStatusToError'
GOTOOLCHAIN=go1.25.0 GOPROXY=https://goproxy.cn,direct /Users/fy2462/sdk/go1.26.2/bin/go test -tags rdma ./pkg/cache/remote/rdma
GOTOOLCHAIN=go1.25.0 GOPROXY=https://goproxy.cn,direct /Users/fy2462/sdk/go1.26.2/bin/go test ./cmd -run 'TestHandleSysMountArgs|TestRDMACacheServer'
git diff --check
git status --short --branch
```
