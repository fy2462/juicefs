# RDMA Distributed Cache Phase 2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a remote cache server skeleton and wire `--remote-cache-nodes` into a real client/server cache path while object storage remains authoritative.

**Architecture:** Phase 2 uses an HTTP/TCP development transport behind the existing `remote.Client` interface. A new server package exposes `GET`, `PUT`, and `DELETE` over HTTP and stores blocks in a thread-safe in-memory backend; a new client package talks to one or more nodes with per-request timeout and fallback-friendly errors. `pkg/chunk` creates this client when `RemoteCacheMode=rdma` and nodes are configured; real RDMA verbs remain a Phase 3 transport swap.

**Tech Stack:** Go standard `net/http`, JuiceFS `pkg/cache/remote`, `pkg/chunk`, `urfave/cli`, Go unit tests.

---

## Tasks

### Task 1: HTTP Remote Cache Protocol

**Files:**
- Create `pkg/cache/remote/httpcache/client.go`
- Create `pkg/cache/remote/httpcache/server.go`
- Create `pkg/cache/remote/httpcache/httpcache_test.go`

Implement:
- `NewClient(nodes []string, timeout time.Duration) remote.Client`
- `NewHandler(cache remote.Client) http.Handler`
- Endpoints:
  - `GET /cache/{escaped-key}?off=<n>&size=<n>`
  - `PUT /cache/{escaped-key}`
  - `DELETE /cache/{escaped-key}`
- Map 404 to `remote.ErrMiss`; non-2xx and network errors to `remote.ErrUnavailable`.
- Use rendezvous-style deterministic selection by hashing key across node URLs.

Verify:

```bash
GOTOOLCHAIN=go1.25.0 GOPROXY=https://goproxy.cn,direct /Users/fy2462/sdk/go1.26.2/bin/go test ./pkg/cache/remote/...
```

### Task 2: Command Skeleton

**Files:**
- Create `cmd/rdma_cache_server.go`
- Modify `cmd/main.go`
- Create `cmd/rdma_cache_server_test.go`

Implement:
- CLI command `rdma-cache-server`
- Flags: `--listen`, `--cache-size`, `--cache-dir`
- First implementation uses in-memory backend and logs that `--cache-dir` is reserved for later disk backend.
- Command starts an HTTP server using `httpcache.NewHandler(mock.NewClient())`.
- Unit test checks the command is registered in `NewApp()`.

Verify:

```bash
GOTOOLCHAIN=go1.25.0 GOPROXY=https://goproxy.cn,direct /Users/fy2462/sdk/go1.26.2/bin/go test ./cmd -run TestRDMACacheServerCommandRegistered
```

### Task 3: Chunk Wiring

**Files:**
- Modify `pkg/chunk/cached_store.go`
- Modify `pkg/chunk/cached_store_test.go`

Implement:
- When `RemoteCacheMode == "rdma"` and `RemoteCacheNodes` is not empty, create `httpcache.NewClient`.
- Keep `RemoteCacheMode=mock` behavior unchanged.
- If `RemoteCacheMode == "rdma"` with empty nodes, remote cache remains disabled and reads fall back to object storage.
- Add an integration-style test with `httptest.Server` proving remote hit avoids object storage through `RemoteCacheNodes`.
- Add timeout/fallback test with a slow `httptest.Server`.

Verify:

```bash
GOTOOLCHAIN=go1.25.0 GOPROXY=https://goproxy.cn,direct /Users/fy2462/sdk/go1.26.2/bin/go test ./pkg/chunk -run 'TestRemoteCache.*(HTTP|Timeout|Fallback|Hit|Miss|Error)'
```

### Task 4: Final Verification

Run:

```bash
GOTOOLCHAIN=go1.25.0 GOPROXY=https://goproxy.cn,direct /Users/fy2462/sdk/go1.26.2/bin/go test ./pkg/cache/remote/... ./pkg/chunk -run 'TestRemoteCache|TestStoreRetry|TestStoreDefault|TestFillCache'
GOTOOLCHAIN=go1.25.0 GOPROXY=https://goproxy.cn,direct /Users/fy2462/sdk/go1.26.2/bin/go test ./cmd -run 'TestHandleSysMountArgs|TestRDMACacheServerCommandRegistered'
git diff --check
git status --short --branch
```

Expected:
- All commands exit 0.
- Worktree is clean after commits.

