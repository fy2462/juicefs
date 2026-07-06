# RDMA Cache Node Health Fallback Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add health-aware L2 remote cache node selection so a failed RDMA/cache node is skipped and reads safely fall back to L1+L3 object storage.

**Architecture:** Introduce a shared `pkg/cache/remote/cluster` helper for deterministic rendezvous placement and per-client in-memory node health. HTTP and RDMA clients reuse this helper, while `pkg/chunk` continues to treat remote cache errors as cache misses and falls back to object storage. L2 placement and health remain non-authoritative and never touch JuiceFS metadata.

**Tech Stack:** Go, `pkg/cache/remote`, existing HTTP remote cache transport, existing RDMA protocol test dialer, `pkg/chunk` remote cache fallback tests, POSIX shell RustFS smoke.

---

## File Structure

- Create `pkg/cache/remote/cluster/placement.go`
  - Owns node normalization and rendezvous ordering.
  - Replaces duplicate placement logic currently embedded in `httpcache.Client`.
- Create `pkg/cache/remote/cluster/health.go`
  - Owns local in-memory node health, failure counters, cooldown, and recovery marking.
  - Provides request candidate filtering without storing any JuiceFS metadata.
- Create `pkg/cache/remote/cluster/cluster_test.go`
  - Verifies placement stability, cooldown skip, recovery, and replica filtering.
- Modify `pkg/cache/remote/httpcache/client.go`
  - Use `cluster.Placement` and `cluster.Health` for Get/Put/Delete.
  - Preserve current public constructor behavior.
- Modify `pkg/cache/remote/httpcache/httpcache_test.go`
  - Add single-node skip, replica fallback, all-down, and partial Put success tests.
- Modify `pkg/cache/remote/rdma/types.go`
  - Add cluster fields to `Client`.
  - Preserve public `Options`.
- Modify `pkg/cache/remote/rdma/rdma_test.go`
  - Add multi-node fallback tests with the existing in-memory dialer.
- Modify `pkg/cache/remote/rdma/rdma.go` only if default build behavior needs constructor adjustment.
- Modify `pkg/chunk/cached_store_test.go`
  - Add behavior-level single-node L2 unavailable fallback coverage if existing tests do not prove the new skip path.
- Modify `hack/three-tier-cache-rustfs-test.sh`
  - Add a smoke stage proving an empty-L1 read succeeds from RustFS after L2 server shutdown.
- Modify `docs/superpowers/runbooks/2026-07-06-three-tier-cache-rustfs.md`
  - Document the L2-down fallback check.

## Task 1: Shared Cluster Placement

**Files:**
- Create: `pkg/cache/remote/cluster/placement.go`
- Create: `pkg/cache/remote/cluster/cluster_test.go`

- [ ] **Step 1: Write failing placement tests**

Create `pkg/cache/remote/cluster/cluster_test.go` with these tests:

```go
package cluster

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPlacementNormalizesNodesAndCapsReplicas(t *testing.T) {
	p := NewPlacement([]string{" n2 ", "", "n1", "n3"}, 10)

	nodes := p.Candidates("chunks/0/0/1_0_4")

	require.Len(t, nodes, 3)
	require.ElementsMatch(t, []string{"n1", "n2", "n3"}, nodes)
}

func TestPlacementIsStableForSameKey(t *testing.T) {
	p := NewPlacement([]string{"n1", "n2", "n3"}, 2)

	first := p.Candidates("chunks/0/0/1_0_4")
	second := p.Candidates("chunks/0/0/1_0_4")

	require.Equal(t, first, second)
}

func TestPlacementDefaultsReplicaToOne(t *testing.T) {
	p := NewPlacement([]string{"n1", "n2"}, 0)

	nodes := p.Candidates("chunks/0/0/1_0_4")

	require.Len(t, nodes, 1)
}

func TestHealthAllowsHealthyCandidates(t *testing.T) {
	h := NewHealth(Options{FailThreshold: 1, Cooldown: time.Second})

	nodes := h.Available([]string{"n1", "n2"})

	require.Equal(t, []string{"n1", "n2"}, nodes)
}
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```bash
PATH=/usr/local/go/bin:$PATH go test ./pkg/cache/remote/cluster
```

Expected: FAIL because package `pkg/cache/remote/cluster` or functions `NewPlacement`, `NewHealth`, and `Options` do not exist.

- [ ] **Step 3: Implement minimal placement and health constructor**

Create `pkg/cache/remote/cluster/placement.go`:

```go
package cluster

import (
	"hash/fnv"
	"sort"
	"strings"
)

type Placement struct {
	nodes    []string
	replicas int
}

func NewPlacement(nodes []string, replicas int) *Placement {
	p := &Placement{}
	for _, node := range nodes {
		node = strings.TrimSpace(node)
		if node != "" {
			p.nodes = append(p.nodes, node)
		}
	}
	p.replicas = replicas
	if p.replicas <= 0 {
		p.replicas = 1
	}
	if p.replicas > len(p.nodes) {
		p.replicas = len(p.nodes)
	}
	return p
}

func (p *Placement) Candidates(key string) []string {
	nodes := append([]string(nil), p.nodes...)
	sort.Slice(nodes, func(i, j int) bool {
		return score(key, nodes[i]) > score(key, nodes[j])
	})
	return nodes[:p.replicas]
}

func score(key, node string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(node))
	return h.Sum64()
}
```

Create `pkg/cache/remote/cluster/health.go`:

```go
package cluster

import "time"

type Options struct {
	FailThreshold int
	Cooldown      time.Duration
	Now           func() time.Time
}

type Health struct {
	options Options
}

func NewHealth(options Options) *Health {
	if options.FailThreshold <= 0 {
		options.FailThreshold = 1
	}
	if options.Cooldown <= 0 {
		options.Cooldown = 5 * time.Second
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Health{options: options}
}

func (h *Health) Available(nodes []string) []string {
	return append([]string(nil), nodes...)
}
```

- [ ] **Step 4: Run tests to verify GREEN**

Run:

```bash
PATH=/usr/local/go/bin:$PATH go test ./pkg/cache/remote/cluster
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/cache/remote/cluster
git commit -m "test: add remote cache cluster placement"
```

## Task 2: Health Cooldown and Recovery

**Files:**
- Modify: `pkg/cache/remote/cluster/health.go`
- Modify: `pkg/cache/remote/cluster/cluster_test.go`

- [ ] **Step 1: Write failing health state tests**

Append these tests to `pkg/cache/remote/cluster/cluster_test.go`:

```go
func TestHealthSkipsNodeDuringCooldown(t *testing.T) {
	now := time.Unix(100, 0)
	h := NewHealth(Options{
		FailThreshold: 2,
		Cooldown:      time.Second,
		Now:           func() time.Time { return now },
	})

	h.MarkFailure("n1")
	require.Equal(t, []string{"n1", "n2"}, h.Available([]string{"n1", "n2"}))

	h.MarkFailure("n1")
	require.Equal(t, []string{"n2"}, h.Available([]string{"n1", "n2"}))
}

func TestHealthAllowsProbeAfterCooldown(t *testing.T) {
	now := time.Unix(100, 0)
	h := NewHealth(Options{
		FailThreshold: 1,
		Cooldown:      time.Second,
		Now:           func() time.Time { return now },
	})

	h.MarkFailure("n1")
	require.Empty(t, h.Available([]string{"n1"}))

	now = now.Add(time.Second)
	require.Equal(t, []string{"n1"}, h.Available([]string{"n1"}))
}

func TestHealthSuccessRecoversNode(t *testing.T) {
	now := time.Unix(100, 0)
	h := NewHealth(Options{
		FailThreshold: 1,
		Cooldown:      time.Second,
		Now:           func() time.Time { return now },
	})

	h.MarkFailure("n1")
	require.Empty(t, h.Available([]string{"n1"}))

	h.MarkSuccess("n1")
	require.Equal(t, []string{"n1"}, h.Available([]string{"n1"}))
}
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```bash
PATH=/usr/local/go/bin:$PATH go test ./pkg/cache/remote/cluster
```

Expected: FAIL because `MarkFailure` and `MarkSuccess` are missing.

- [ ] **Step 3: Implement health state**

Replace `pkg/cache/remote/cluster/health.go` with:

```go
package cluster

import (
	"sync"
	"time"
)

type Options struct {
	FailThreshold int
	Cooldown      time.Duration
	Now           func() time.Time
}

type Health struct {
	mu      sync.Mutex
	options Options
	nodes   map[string]*nodeState
}

type nodeState struct {
	failures int
	downAt   time.Time
}

func NewHealth(options Options) *Health {
	if options.FailThreshold <= 0 {
		options.FailThreshold = 1
	}
	if options.Cooldown <= 0 {
		options.Cooldown = 5 * time.Second
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Health{options: options, nodes: make(map[string]*nodeState)}
}

func (h *Health) Available(nodes []string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.options.Now()
	available := make([]string, 0, len(nodes))
	for _, node := range nodes {
		state := h.nodes[node]
		if state == nil || state.failures < h.options.FailThreshold || !now.Before(state.downAt.Add(h.options.Cooldown)) {
			available = append(available, node)
		}
	}
	return available
}

func (h *Health) MarkFailure(node string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	state := h.nodes[node]
	if state == nil {
		state = &nodeState{}
		h.nodes[node] = state
	}
	state.failures++
	if state.failures >= h.options.FailThreshold {
		state.downAt = h.options.Now()
	}
}

func (h *Health) MarkSuccess(node string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.nodes, node)
}
```

- [ ] **Step 4: Run tests to verify GREEN**

Run:

```bash
PATH=/usr/local/go/bin:$PATH go test ./pkg/cache/remote/cluster
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/cache/remote/cluster
git commit -m "feat: add remote cache node health cooldown"
```

## Task 3: HTTP Client Health-Aware Fallback

**Files:**
- Modify: `pkg/cache/remote/httpcache/client.go`
- Modify: `pkg/cache/remote/httpcache/httpcache_test.go`

- [ ] **Step 1: Write failing HTTP client tests**

Append these tests to `pkg/cache/remote/httpcache/httpcache_test.go`:

```go
func TestClientSkipsFailedSingleNodeDuringCooldown(t *testing.T) {
	var gets atomic.Int32
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gets.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer failing.Close()

	client := NewClientWithOptions(Options{
		Nodes:         []string{failing.URL},
		Timeout:       time.Second,
		Replicas:      1,
		FailThreshold: 1,
		NodeCooldown:  time.Minute,
	})

	_, err := client.Get(context.Background(), "k", 0, -1)
	require.ErrorIs(t, err, remote.ErrUnavailable)
	_, err = client.Get(context.Background(), "k", 0, -1)
	require.ErrorIs(t, err, remote.ErrUnavailable)
	require.Equal(t, int32(1), gets.Load())
}

func TestClientReplicaFallbackMarksFailedNodeAndUsesHealthyReplica(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer failing.Close()
	backend := mock.NewClient()
	require.NoError(t, backend.Put(context.Background(), "k", []byte("value")))
	healthy := httptest.NewServer(NewHandler(backend))
	defer healthy.Close()

	client := NewClientWithOptions(Options{
		Nodes:         []string{failing.URL, healthy.URL},
		Timeout:       time.Second,
		Replicas:      2,
		FailThreshold: 1,
		NodeCooldown:  time.Minute,
	})

	r, err := client.Get(context.Background(), "k", 0, -1)
	require.NoError(t, err)
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.Equal(t, []byte("value"), data)
}
```

- [ ] **Step 2: Run tests to verify RED**

Run:

```bash
PATH=/usr/local/go/bin:$PATH go test ./pkg/cache/remote/httpcache -run 'TestClientSkipsFailed|TestClientReplicaFallback'
```

Expected: FAIL because `Options.FailThreshold` and `Options.NodeCooldown` are missing, and failed nodes are not skipped.

- [ ] **Step 3: Wire cluster helper into HTTP client**

Update `pkg/cache/remote/httpcache/client.go`:

```go
type Client struct {
	placement  *cluster.Placement
	health     *cluster.Health
	httpClient *http.Client
}

type Options struct {
	Nodes         []string
	Timeout       time.Duration
	Replicas      int
	FailThreshold int
	NodeCooldown  time.Duration
}
```

In `NewClientWithOptions`, create:

```go
placement := cluster.NewPlacement(normalizedNodes, options.Replicas)
health := cluster.NewHealth(cluster.Options{
	FailThreshold: options.FailThreshold,
	Cooldown:      options.NodeCooldown,
})
return &Client{
	placement:  placement,
	health:     health,
	httpClient: &http.Client{Timeout: options.Timeout},
}
```

Replace `replicaNodes` usage with:

```go
nodes := c.health.Available(c.placement.Candidates(key))
if len(nodes) == 0 {
	return nil, remote.ErrUnavailable
}
```

For each node:

```go
if requestSucceeded {
	c.health.MarkSuccess(base)
}
if requestFailed {
	c.health.MarkFailure(base)
}
```

Remove local `rendezvousScore` after the cluster helper is used.

- [ ] **Step 4: Run HTTP tests to verify GREEN**

Run:

```bash
PATH=/usr/local/go/bin:$PATH go test ./pkg/cache/remote/httpcache
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/cache/remote/httpcache pkg/cache/remote/cluster
git commit -m "feat: skip unhealthy http remote cache nodes"
```

## Task 4: RDMA Client Multi-Node Health Fallback

**Files:**
- Modify: `pkg/cache/remote/rdma/types.go`
- Modify: `pkg/cache/remote/rdma/rdma_test.go`

- [ ] **Step 1: Write failing RDMA client tests**

Append this dialer and test to `pkg/cache/remote/rdma/rdma_test.go`:

```go
type routingDialer struct {
	servers map[string]*Server
	dials   map[string]*atomic.Int32
}

func (d routingDialer) Dial(ctx context.Context, node string, options Options) (Conn, error) {
	if counter := d.dials[node]; counter != nil {
		counter.Add(1)
	}
	server := d.servers[node]
	if server == nil {
		return nil, remote.ErrUnavailable
	}
	return memoryConn{server: server}, nil
}

func TestClientSkipsFailedNodeAndUsesHealthyReplica(t *testing.T) {
	backend := mock.NewClient()
	require.NoError(t, backend.Put(context.Background(), "k", []byte("value")))
	badDials := &atomic.Int32{}
	goodDials := &atomic.Int32{}
	client := NewClient(Options{
		Nodes:        []string{"bad", "good"},
		Replicas:     2,
		FailThreshold: 1,
		NodeCooldown: time.Minute,
		Dialer: routingDialer{
			servers: map[string]*Server{"good": NewServer(backend)},
			dials: map[string]*atomic.Int32{
				"bad":  badDials,
				"good": goodDials,
			},
		},
	})

	r, err := client.Get(context.Background(), "k", 0, -1)
	require.NoError(t, err)
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.Equal(t, []byte("value"), data)

	r, err = client.Get(context.Background(), "k", 0, -1)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.Equal(t, int32(1), badDials.Load())
	require.GreaterOrEqual(t, goodDials.Load(), int32(1))
	require.NoError(t, client.Close())
}
```

Also add imports for `sync/atomic`.

- [ ] **Step 2: Run test to verify RED**

Run:

```bash
PATH=/usr/local/go/bin:$PATH go test ./pkg/cache/remote/rdma -run TestClientSkipsFailedNodeAndUsesHealthyReplica
```

Expected: FAIL because `Options.FailThreshold` and `Options.NodeCooldown` are missing, and the RDMA client only dials the first node.

- [ ] **Step 3: Implement RDMA cluster state**

Update `pkg/cache/remote/rdma/types.go`:

```go
type Options struct {
	Nodes         []string
	Timeout       time.Duration
	Replicas      int
	Dialer        Dialer
	FailThreshold int
	NodeCooldown  time.Duration
}

type Client struct {
	options   Options
	placement *cluster.Placement
	health    *cluster.Health
	mu        sync.Mutex
	conns     map[string]Conn
	closed    bool
}
```

In `newClient`:

```go
return &Client{
	options:   options,
	placement: cluster.NewPlacement(options.Nodes, options.Replicas),
	health: cluster.NewHealth(cluster.Options{
		FailThreshold: options.FailThreshold,
		Cooldown:      options.NodeCooldown,
	}),
	conns: make(map[string]Conn),
}
```

Replace single `connection(ctx)` with node-specific connection:

```go
func (c *Client) connection(ctx context.Context, node string) (Conn, error)
```

Make `roundTrip` iterate:

```go
nodes := c.health.Available(c.placement.Candidates(req.Key))
if len(nodes) == 0 {
	return protocol.Response{}, remote.ErrUnavailable
}
for _, node := range nodes {
	conn, err := c.connection(ctx, node)
	if err != nil {
		c.health.MarkFailure(node)
		continue
	}
	resp, err := conn.RoundTrip(ctx, req)
	if err != nil {
		c.health.MarkFailure(node)
		continue
	}
	c.health.MarkSuccess(node)
	return resp, nil
}
return protocol.Response{}, remote.ErrUnavailable
```

On `Close`, close every cached node connection.

- [ ] **Step 4: Run RDMA tests to verify GREEN**

Run:

```bash
PATH=/usr/local/go/bin:$PATH go test ./pkg/cache/remote/rdma
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/cache/remote/rdma pkg/cache/remote/cluster
git commit -m "feat: skip unhealthy rdma remote cache nodes"
```

## Task 5: Chunk Fallback and RustFS Smoke for L2 Down

**Files:**
- Modify: `pkg/chunk/cached_store_test.go`
- Modify: `hack/three-tier-cache-rustfs-test.sh`
- Modify: `docs/superpowers/runbooks/2026-07-06-three-tier-cache-rustfs.md`

- [ ] **Step 1: Add chunk behavior test if missing**

If `TestRemoteCacheErrorFallsBackToObjectStorage` still uses a generic failing remote cache, add this focused single-node unavailable test:

```go
func TestRemoteCacheUnavailableFallsBackToObjectStorage(t *testing.T) {
	blob, _ := object.CreateStorage("mem", "", "", "", "")
	key := "chunks/0/0/135_0_4"
	require.NoError(t, blob.Put(context.Background(), key, bytes.NewReader([]byte("safe"))))
	counting := &countingStore{ObjectStorage: blob}
	conf := defaultConf
	conf.CacheSize = 0
	conf.RemoteCacheMode = "mock"
	store := NewCachedStore(counting, conf, nil).(*cachedStore)
	store.remoteCache = unavailableRemoteCache{}

	p := NewPage(make([]byte, 4))
	defer p.Release()
	require.NoError(t, store.load(context.Background(), key, p, false, false))
	require.Equal(t, []byte("safe"), p.Data)
	require.Equal(t, int32(1), counting.gets.Load())
}
```

Add this helper:

```go
type unavailableRemoteCache struct{}

func (u unavailableRemoteCache) Get(ctx context.Context, key string, off, size int) (io.ReadCloser, error) {
	return nil, remote.ErrUnavailable
}

func (u unavailableRemoteCache) Put(ctx context.Context, key string, data []byte) error {
	return remote.ErrUnavailable
}

func (u unavailableRemoteCache) Delete(ctx context.Context, key string) error {
	return remote.ErrUnavailable
}

func (u unavailableRemoteCache) Close() error {
	return nil
}
```

- [ ] **Step 2: Run chunk test to verify RED/GREEN**

Run:

```bash
PATH=/usr/local/go/bin:$PATH TMPDIR=/home/fy2462/tmp go test -gcflags="all=-N -l" ./pkg/chunk -run 'TestRemoteCacheUnavailableFallsBackToObjectStorage|TestRemoteCacheErrorFallsBackToObjectStorage'
```

Expected: PASS. This test documents the L1+L3 behavior above the transport layer.

- [ ] **Step 3: Extend RustFS smoke**

In `hack/three-tier-cache-rustfs-test.sh`, add a new function after `run_three_tier_read_path`:

```sh
run_l2_down_fallback_path() {
  meta="$TMP_DIR/l2-down-meta.db"
  mountpoint="$TMP_DIR/l2-down-mnt"
  l1a="$TMP_DIR/l2-down-l1-a"
  l1b="$TMP_DIR/l2-down-l1-b"
  mkdir -p "$mountpoint" "$l1a" "$l1b"

  if ! curl -sS --connect-timeout 1 "http://127.0.0.1:9000/" >/dev/null 2>&1; then
    start_rustfs
  fi

  "$ROOT_DIR/juicefs" format \
    --storage s3 \
    --bucket "$RUSTFS_BUCKET_URL" \
    --access-key "$RUSTFS_ACCESS_KEY" \
    --secret-key "$RUSTFS_SECRET_KEY" \
    --trash-days 0 \
    "sqlite3://$meta" l2-down-jfs

  "$ROOT_DIR/juicefs" mount \
    --cache-dir "$l1a" \
    --cache-size 64 \
    --remote-cache rdma \
    --remote-cache-transport http \
    --remote-cache-nodes 127.0.0.1:9568 \
    --remote-cache-fill-local=true \
    --remote-cache-fill-remote=true \
    "sqlite3://$meta" "$mountpoint" &
  mount_pid=$!
  wait_for_mount "$mountpoint"
  printf 'l2-down-fallback\n' > "$mountpoint/payload.txt"
  sync
  unmount_jfs "$mountpoint" "$mount_pid"

  stop_remote_cache_server
  "$ROOT_DIR/juicefs" mount \
    --cache-dir "$l1b" \
    --cache-size 64 \
    --remote-cache rdma \
    --remote-cache-transport http \
    --remote-cache-nodes 127.0.0.1:9568 \
    --remote-cache-timeout 25ms \
    --remote-cache-fill-local=true \
    --remote-cache-fill-remote=true \
    "sqlite3://$meta" "$mountpoint" &
  mount_pid=$!
  wait_for_mount "$mountpoint"
  wait_for_path "$mountpoint/payload.txt"
  grep -F 'l2-down-fallback' "$mountpoint/payload.txt" >/dev/null
  unmount_jfs "$mountpoint" "$mount_pid"

  stop_rustfs
  "$ROOT_DIR/juicefs" mount \
    --cache-dir "$TMP_DIR/l2-down-l1-c" \
    --cache-size 64 \
    --remote-cache rdma \
    --remote-cache-transport http \
    --remote-cache-nodes 127.0.0.1:9568 \
    --remote-cache-timeout 25ms \
    "sqlite3://$meta" "$mountpoint" &
  mount_pid=$!
  wait_for_mount "$mountpoint"
  if grep -F 'l2-down-fallback' "$mountpoint/payload.txt" >/dev/null 2>&1; then
    unmount_jfs "$mountpoint" "$mount_pid"
    fail "l2-down fallback read unexpectedly succeeded after rustfs stopped"
  fi
  unmount_jfs "$mountpoint" "$mount_pid"
}
```

Call it in `main()` after `run_three_tier_read_path` and add:

```sh
pass "read falls back to rustfs when remote cache server is down"
```

- [ ] **Step 4: Run smoke to verify GREEN**

Run:

```bash
PATH=/usr/local/go/bin:$PATH TMPDIR=/home/fy2462/tmp make test.three-tier-cache-rustfs
```

Expected: PASS with an additional numbered `ok` line for L2-down fallback.

- [ ] **Step 5: Update runbook**

Add a short section to `docs/superpowers/runbooks/2026-07-06-three-tier-cache-rustfs.md` explaining that the smoke also stops L2 and proves empty-L1 reads fall back to RustFS, then stops RustFS to prove the prior success was L3 fallback.

- [ ] **Step 6: Commit**

```bash
git add pkg/chunk/cached_store_test.go hack/three-tier-cache-rustfs-test.sh docs/superpowers/runbooks/2026-07-06-three-tier-cache-rustfs.md
git commit -m "test: cover l2 down fallback to rustfs"
```

## Task 6: Final Verification

**Files:**
- No source changes.

- [ ] **Step 1: Run formatting and static checks**

Run:

```bash
/usr/local/go/bin/gofmt -w pkg/cache/remote/cluster/*.go pkg/cache/remote/httpcache/*.go pkg/cache/remote/rdma/*.go pkg/chunk/cached_store_test.go
git diff --check
```

Expected: exit 0.

- [ ] **Step 2: Run targeted Go tests**

Run:

```bash
PATH=/usr/local/go/bin:$PATH TMPDIR=/home/fy2462/tmp go test -gcflags="all=-N -l" ./pkg/cache/remote/... ./pkg/chunk -run 'TestRemoteCache|TestClient|TestHealth|TestPlacement|TestDisk|TestHTTP|TestRDM'
```

Expected: exit 0.

- [ ] **Step 3: Run RustFS smoke**

Run:

```bash
PATH=/usr/local/go/bin:$PATH TMPDIR=/home/fy2462/tmp make test.three-tier-cache-rustfs
```

Expected: exit 0 and numbered ok lines including:

```text
three-tier read path returns data with fresh L1 after rustfs stops
read falls back to rustfs when remote cache server is down
```

- [ ] **Step 4: Verify no residual state**

Run:

```bash
git status --short --branch
mount | rg 'jfs-three-tier-cache-rustfs|three-tier-mnt|l2-down-mnt' || true
docker ps --format '{{.Names}} {{.Status}}'
```

Expected:

```text
## feature/rdma-distributed-cache
```

and no mount or Docker container output.

- [ ] **Step 5: Commit final verification notes only if docs changed**

If no docs changed during verification, do not create a commit.

## Self-Review

- Spec coverage: Tasks cover shared health state, stable placement, single-node skip, replica fallback, RDMA multi-node fallback, L1+L3 fallback, RustFS smoke, and no metadata changes.
- Placeholder scan: no placeholder markers or unspecified implementation steps remain.
- Type consistency: `cluster.Options`, `cluster.NewPlacement`, `cluster.NewHealth`, `MarkFailure`, `MarkSuccess`, and `Available` are consistently named across tasks.
