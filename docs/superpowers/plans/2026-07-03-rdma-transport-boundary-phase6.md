# RDMA Transport Boundary Phase 6 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a dependency-free RDMA protocol boundary, build-tagged native skeleton, and cache-server transport selection.

**Architecture:** `pkg/cache/remote/rdma/protocol` owns transport-independent request/response semantics. `pkg/cache/remote/rdma` keeps default builds unsupported and dependency-free while adding an `rdma` build-tag file for the future native implementation. `rdma-cache-server --transport` chooses HTTP today and fails clearly for RDMA in default builds.

**Tech Stack:** Go standard library, JuiceFS `remote.Client`, `urfave/cli`, Go unit tests.

---

## Task 1: Protocol Package

**Files:**
- Create: `pkg/cache/remote/rdma/protocol/protocol.go`
- Create: `pkg/cache/remote/rdma/protocol/protocol_test.go`

- [ ] **Step 1: Write protocol tests**

Create tests:

```go
func TestRequestRoundTrip(t *testing.T) {
    req := Request{Op: OpGet, Key: "chunks/0/0/1_0_4", Off: 1, Size: 2}
    data, err := EncodeRequest(req)
    require.NoError(t, err)
    got, err := DecodeRequest(data)
    require.NoError(t, err)
    require.Equal(t, req, got)
}

func TestResponseRoundTripWithPayload(t *testing.T) {
    resp := Response{Status: StatusOK, Payload: []byte("data")}
    data, err := EncodeResponse(resp)
    require.NoError(t, err)
    got, err := DecodeResponse(data)
    require.NoError(t, err)
    require.Equal(t, resp, got)
}

func TestStatusToError(t *testing.T) {
    require.NoError(t, StatusToError(StatusOK))
    require.ErrorIs(t, StatusToError(StatusMiss), remote.ErrMiss)
    require.ErrorIs(t, StatusToError(StatusUnavailable), remote.ErrUnavailable)
    require.ErrorIs(t, StatusToError(StatusBadRequest), remote.ErrUnavailable)
}
```

- [ ] **Step 2: Implement protocol**

Define:

```go
type Op string
const (
    OpGet Op = "GET"
    OpPut Op = "PUT"
    OpDelete Op = "DELETE"
)

type Status string
const (
    StatusOK Status = "OK"
    StatusMiss Status = "MISS"
    StatusUnavailable Status = "UNAVAILABLE"
    StatusBadRequest Status = "BAD_REQUEST"
)

type Request struct {
    Op Op `json:"op"`
    Key string `json:"key"`
    Off int `json:"off,omitempty"`
    Size int `json:"size,omitempty"`
    Payload []byte `json:"payload,omitempty"`
}

type Response struct {
    Status Status `json:"status"`
    Payload []byte `json:"payload,omitempty"`
}
```

Implement JSON encode/decode helpers and `StatusToError`.

- [ ] **Step 3: Verify and commit**

```bash
GOTOOLCHAIN=go1.25.0 GOPROXY=https://goproxy.cn,direct /Users/fy2462/sdk/go1.26.2/bin/go test ./pkg/cache/remote/rdma/protocol
git add pkg/cache/remote/rdma/protocol
git commit -m "feat: add RDMA cache protocol boundary"
```

## Task 2: RDMA Native Build-Tag Skeleton

**Files:**
- Modify: `pkg/cache/remote/rdma/rdma.go`
- Create: `pkg/cache/remote/rdma/native_rdma.go`
- Modify: `pkg/cache/remote/rdma/rdma_test.go`

- [ ] **Step 1: Split default build file**

Add `//go:build !rdma` to `rdma.go` so the default unsupported implementation is excluded from RDMA builds.

- [ ] **Step 2: Add native skeleton**

Create `native_rdma.go` with `//go:build rdma`. It defines the same public API:

```go
func NewClient(options Options) remote.Client
```

For now it returns an unsupported client. The file imports only standard library and existing JuiceFS packages, so `go test -tags rdma ./pkg/cache/remote/rdma` works without RDMA headers.

- [ ] **Step 3: Verify and commit**

```bash
GOTOOLCHAIN=go1.25.0 GOPROXY=https://goproxy.cn,direct /Users/fy2462/sdk/go1.26.2/bin/go test ./pkg/cache/remote/rdma
GOTOOLCHAIN=go1.25.0 GOPROXY=https://goproxy.cn,direct /Users/fy2462/sdk/go1.26.2/bin/go test -tags rdma ./pkg/cache/remote/rdma
git add pkg/cache/remote/rdma
git commit -m "feat: add RDMA build tag skeleton"
```

## Task 3: Server Transport Selection

**Files:**
- Modify: `cmd/rdma_cache_server.go`
- Modify: `cmd/rdma_cache_server_test.go`

- [ ] **Step 1: Write server transport tests**

Add:

```go
func TestRDMACacheServerTransportHTTPCreatesHandler(t *testing.T)
func TestRDMACacheServerTransportRDMAReturnsUnsupported(t *testing.T)
```

The HTTP test should call a helper that returns an `http.Handler` for transport `http`. The RDMA test should call the same helper with `rdma` and assert `rdma.ErrUnsupported`.

- [ ] **Step 2: Implement helper and flag**

Add command flag:

```go
&cli.StringFlag{Name: "transport", Value: "http", Usage: "server transport (http, rdma)"}
```

Add:

```go
func newRDMACacheServerHandler(transport string, backend remote.Client) (http.Handler, error)
```

Rules:
- `transport == "" || transport == "http"` returns `httpcache.NewHandler(backend)`.
- `transport == "rdma"` returns `nil, rdma.ErrUnsupported`.
- any other value returns an error.
- `rdmaCacheServer` uses the helper before starting `http.Server`.

- [ ] **Step 3: Verify and commit**

```bash
GOTOOLCHAIN=go1.25.0 GOPROXY=https://goproxy.cn,direct /Users/fy2462/sdk/go1.26.2/bin/go test ./cmd -run 'TestRDMACacheServer'
git add cmd/rdma_cache_server.go cmd/rdma_cache_server_test.go
git commit -m "feat: add RDMA cache server transport selection"
```

## Final Verification

Run:

```bash
GOTOOLCHAIN=go1.25.0 GOPROXY=https://goproxy.cn,direct /Users/fy2462/sdk/go1.26.2/bin/go test ./pkg/cache/remote/... ./pkg/chunk -run 'TestRemoteCache|TestStoreRetry|TestStoreDefault|TestFillCache|TestClient|TestNewClientReturnsUnsupportedByDefault|TestStatusToError'
GOTOOLCHAIN=go1.25.0 GOPROXY=https://goproxy.cn,direct /Users/fy2462/sdk/go1.26.2/bin/go test -tags rdma ./pkg/cache/remote/rdma
GOTOOLCHAIN=go1.25.0 GOPROXY=https://goproxy.cn,direct /Users/fy2462/sdk/go1.26.2/bin/go test ./cmd -run 'TestHandleSysMountArgs|TestRDMACacheServer'
git diff --check
git status --short --branch
```

Expected:
- All commands exit 0.
- Default builds do not require RDMA libraries.
- Worktree is clean after commits.
