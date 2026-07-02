# RDMA Distributed Cache Phase 3-5 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the next three RDMA distributed cache stages: disk-backed cache server storage, multi-node replica policy, and a build-tag-friendly RDMA transport slot.

**Architecture:** Keep object storage authoritative and keep `remote.Client` as the cache contract. The cache server gets a separate disk backend under `pkg/cache/remote/diskcache`; the HTTP client gains deterministic replica-aware node ordering; the RDMA package introduces explicit transport options and default no-RDMA behavior so normal builds and tests do not require RDMA hardware.

**Tech Stack:** Go standard library (`net/http`, `os`, `path/filepath`, `crypto/sha256`), existing JuiceFS CLI/config patterns, existing `pkg/cache/remote` interface, Go unit tests.

---

## File Structure

- `pkg/cache/remote/diskcache/diskcache.go`: disk-backed `remote.Client` implementation for cache server storage.
- `pkg/cache/remote/diskcache/diskcache_test.go`: persistence, range read, delete, and capacity eviction tests.
- `cmd/rdma_cache_server.go`: instantiate disk backend when `--cache-dir` is set and parse `--cache-size`.
- `cmd/rdma_cache_server_test.go`: command registration plus backend selection helper tests.
- `pkg/cache/remote/httpcache/client.go`: replica-aware node order, fallback reads, replicated puts/deletes.
- `pkg/cache/remote/httpcache/httpcache_test.go`: multi-node hit/fallback/put replication tests.
- `cmd/flags.go`, `cmd/mount.go`, `pkg/chunk/cached_store.go`, `pkg/chunk/cached_store_test.go`: add and wire `--remote-cache-replicas`.
- `pkg/cache/remote/rdma/rdma.go`: RDMA transport placeholder with explicit unsupported error in default builds.
- `pkg/cache/remote/rdma/rdma_test.go`: verify default no-RDMA behavior is explicit and fallback-friendly.

---

## Task 1: Disk Backend

**Files:**
- Create: `pkg/cache/remote/diskcache/diskcache.go`
- Create: `pkg/cache/remote/diskcache/diskcache_test.go`
- Modify: `cmd/rdma_cache_server.go`
- Modify: `cmd/rdma_cache_server_test.go`

- [ ] **Step 1: Write disk backend tests**

Add tests that use `t.TempDir()` and `diskcache.NewClient(diskcache.Options{Dir: dir, Capacity: 8})`.

Required behaviors:
- `TestClientGetPutRangeDelete`: `Put("a/b", "abcdef")`, `Get("a/b", 2, 3)` returns `cde`, `Delete` makes later `Get` return `remote.ErrMiss`.
- `TestClientPersistsAcrossRestart`: write with one client, close it, create another client with the same dir, read the same key.
- `TestClientEvictsLeastRecentlyUsed`: capacity 6, write `a=abc`, `b=def`, read `a`, write `c=ghi`; `a` and `c` remain, `b` is evicted.
- `TestClientRejectsOversizedPut`: capacity 3, write a 4-byte value; the call returns `remote.ErrUnavailable`.

Run:

```bash
GOTOOLCHAIN=go1.25.0 GOPROXY=https://goproxy.cn,direct /Users/fy2462/sdk/go1.26.2/bin/go test ./pkg/cache/remote/diskcache
```

Expected: fail because the package does not exist yet.

- [ ] **Step 2: Implement disk backend**

Implement:

```go
type Options struct {
    Dir      string
    Capacity int64
}

func NewClient(options Options) (*Client, error)
```

Rules:
- Store data files by `sha256(key)` at `<dir>/<first-two-hex>/<full-hex>.data`.
- Keep an in-memory index `map[string]*entry` with `key`, `path`, `size`, `atime`.
- Write files through a temp file and `os.Rename`.
- On startup, scan `*.data`; the key is stored in a sidecar `<full-hex>.key` file so restart can rebuild the original key index.
- `Get` checks range bounds, updates `atime`, calls `os.Chtimes`, and returns a read closer that closes the underlying file.
- `Put` rejects values larger than capacity with `remote.ErrUnavailable`.
- `Put` evicts least-recently-used entries until `used <= capacity`.
- `Delete` removes both `.data` and `.key`.
- All public methods respect `ctx.Done()` before taking action.

- [ ] **Step 3: Wire server backend selection**

Add a helper in `cmd/rdma_cache_server.go`:

```go
func newRDMACacheBackend(cacheDir string, cacheSize string) (remote.Client, error)
```

Rules:
- Empty `cacheDir` returns `mock.NewClient()`.
- Non-empty `cacheDir` parses `cacheSize` with `utils.ParseBytesStr("cache-size", cacheSize, 'B')`.
- Non-empty `cacheDir` returns `diskcache.NewClient(diskcache.Options{Dir: cacheDir, Capacity: int64(size)})`.
- `rdmaCacheServer` uses the helper and fails fast if backend creation fails.

- [ ] **Step 4: Add command helper tests**

Add:
- `TestRDMACacheServerBackendDefaultsToMemory`
- `TestRDMACacheServerBackendUsesDiskCache`

Run:

```bash
GOTOOLCHAIN=go1.25.0 GOPROXY=https://goproxy.cn,direct /Users/fy2462/sdk/go1.26.2/bin/go test ./pkg/cache/remote/diskcache ./cmd -run 'TestClient|TestRDMACacheServerBackend'
```

- [ ] **Step 5: Commit**

```bash
git add pkg/cache/remote/diskcache cmd/rdma_cache_server.go cmd/rdma_cache_server_test.go
git commit -m "feat: add disk backend for RDMA cache server"
```

---

## Task 2: Multi-Node Replica Policy

**Files:**
- Modify: `pkg/cache/remote/httpcache/client.go`
- Modify: `pkg/cache/remote/httpcache/httpcache_test.go`
- Modify: `pkg/chunk/cached_store.go`
- Modify: `pkg/chunk/cached_store_test.go`
- Modify: `cmd/flags.go`
- Modify: `cmd/mount.go`
- Modify: `cmd/main_test.go`

- [ ] **Step 1: Write replica tests**

Add tests:
- `TestClientGetFallsBackAcrossReplicas`: first selected node returns 503, second has data, `Get` succeeds.
- `TestClientPutReplicatesToConfiguredReplicas`: three nodes, `Replicas=2`, `Put` stores the value on exactly two selected nodes.
- `TestRemoteCacheReplicasMountArgs`: system mount option `remote-cache-replicas=2` becomes `--remote-cache-replicas=2`.

- [ ] **Step 2: Implement HTTP client options**

Add:

```go
type Options struct {
    Nodes    []string
    Timeout  time.Duration
    Replicas int
}

func NewClientWithOptions(options Options) remote.Client
```

Keep `NewClient(nodes, timeout)` as a compatibility wrapper with `Replicas: 1`.

Rules:
- Normalize nodes exactly as today.
- Clamp replicas to `[1, len(nodes)]`.
- Use rendezvous ordering: hash `key + "\x00" + node` with FNV-64a, sort descending.
- `Get` tries ordered replica nodes until one hits; all miss returns `remote.ErrMiss`; network/server errors continue to the next node and return `remote.ErrUnavailable` only when no node hits or misses cleanly.
- `Put` writes to ordered replica nodes and succeeds if at least one replica write succeeds.
- `Delete` sends to all replica nodes and succeeds if at least one delete succeeds or all nodes return not found/no content.

- [ ] **Step 3: Add config and CLI flag**

Add `RemoteCacheReplicas int` to `chunk.Config`, default it to `1`, add `--remote-cache-replicas` to mount flags, and pass it into `chunk.Config`.

When `RemoteCacheMode == "rdma"`, create:

```go
httpcache.NewClientWithOptions(httpcache.Options{
    Nodes: remoteCacheNodes(config.RemoteCacheNodes),
    Timeout: config.RemoteCacheTimeout,
    Replicas: config.RemoteCacheReplicas,
})
```

- [ ] **Step 4: Verify**

```bash
GOTOOLCHAIN=go1.25.0 GOPROXY=https://goproxy.cn,direct /Users/fy2462/sdk/go1.26.2/bin/go test ./pkg/cache/remote/httpcache ./pkg/chunk ./cmd -run 'TestClient.*Replica|TestRemoteCache.*Replica|TestHandleSysMountArgs'
```

- [ ] **Step 5: Commit**

```bash
git add pkg/cache/remote/httpcache pkg/chunk cmd
git commit -m "feat: add remote cache replica policy"
```

---

## Task 3: RDMA Transport Slot

**Files:**
- Create: `pkg/cache/remote/rdma/rdma.go`
- Create: `pkg/cache/remote/rdma/rdma_test.go`
- Modify: `pkg/chunk/cached_store.go`
- Modify: `pkg/chunk/cached_store_test.go`
- Modify: `cmd/flags.go`
- Modify: `cmd/mount.go`
- Modify: `cmd/main_test.go`

- [ ] **Step 1: Write RDMA slot tests**

Add:
- `TestNewClientReturnsUnsupportedByDefault`: default build `rdma.NewClient` returns a client whose operations return `rdma.ErrUnsupported`.
- `TestRemoteCacheRDMATransportFallsBackToObjectStorage`: mount config with `RemoteCacheMode=rdma`, `RemoteCacheTransport=rdma`, and object storage data; read succeeds from object storage.
- `TestRemoteCacheTransportMountArgs`: system mount option `remote-cache-transport=rdma` becomes `--remote-cache-transport=rdma`.

- [ ] **Step 2: Implement RDMA package**

Add:

```go
var ErrUnsupported = errors.New("RDMA remote cache transport is not built")

type Options struct {
    Nodes    []string
    Timeout  time.Duration
    Replicas int
}

func NewClient(options Options) remote.Client
```

Default implementation returns a client that maps all operations to `ErrUnsupported`. This keeps default builds dependency-free while making the transport boundary explicit.

- [ ] **Step 3: Add transport config**

Add `RemoteCacheTransport string` to `chunk.Config` with default `http`.

Rules:
- `RemoteCacheMode=rdma`, `RemoteCacheTransport=http`: current HTTP client path.
- `RemoteCacheMode=rdma`, `RemoteCacheTransport=rdma`: create `rdma.NewClient`.
- Unknown transport logs a warning and falls back to `http`.

Add mount flag:

```text
--remote-cache-transport=http|rdma
```

- [ ] **Step 4: Verify**

```bash
GOTOOLCHAIN=go1.25.0 GOPROXY=https://goproxy.cn,direct /Users/fy2462/sdk/go1.26.2/bin/go test ./pkg/cache/remote/... ./pkg/chunk ./cmd -run 'TestRemoteCache|TestClient|TestHandleSysMountArgs|TestNewClientReturnsUnsupportedByDefault'
```

- [ ] **Step 5: Commit**

```bash
git add pkg/cache/remote/rdma pkg/chunk cmd
git commit -m "feat: add RDMA remote cache transport slot"
```

---

## Final Verification

Run:

```bash
GOTOOLCHAIN=go1.25.0 GOPROXY=https://goproxy.cn,direct /Users/fy2462/sdk/go1.26.2/bin/go test ./pkg/cache/remote/... ./pkg/chunk -run 'TestRemoteCache|TestStoreRetry|TestStoreDefault|TestFillCache|TestClient'
GOTOOLCHAIN=go1.25.0 GOPROXY=https://goproxy.cn,direct /Users/fy2462/sdk/go1.26.2/bin/go test ./cmd -run 'TestHandleSysMountArgs|TestRDMACacheServer'
git diff --check
git status --short --branch
```

Expected:
- All commands exit 0.
- Worktree is clean after commits.
