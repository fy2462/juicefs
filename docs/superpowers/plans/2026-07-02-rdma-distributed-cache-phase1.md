# RDMA Distributed Cache Phase 1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a safe Phase 1 remote read-through cache abstraction to JuiceFS so local cache misses can use a mock/RDMA-ready distributed cache before falling back to object storage.

**Architecture:** Keep JuiceFS object storage as the authoritative persistence layer and local disk cache as L1. Add a small `pkg/cache/remote` interface, an in-memory mock implementation for tests, and a `pkg/chunk` integration that checks remote cache only after local cache misses and before full-block object storage reads. Writes and range reads keep existing semantics in Phase 1.

**Tech Stack:** Go, JuiceFS `pkg/chunk`, JuiceFS `pkg/object`, Prometheus client metrics, `urfave/cli`, Go unit tests.

---

## Scope

This plan implements Phase 1 from `docs/superpowers/specs/2026-07-02-rdma-distributed-cache-design.md`.

Included:

- Remote cache interface and mock implementation.
- `chunk.Config` remote cache fields.
- Full-block read-through integration in `cachedStore.load`.
- Best-effort remote cache fill after object storage full-block reads.
- Remote cache metrics.
- Mount flags and config parsing.
- Unit tests that do not require RDMA hardware.

Excluded:

- Real RDMA verbs implementation.
- `rdma-cache-server`.
- Write-back distributed cache.
- Dynamic membership and health gossip.
- Range-read remote cache support.

## File Structure

- Create `pkg/cache/remote/remote.go`
  - Defines the transport-neutral remote cache client interface and sentinel errors.
- Create `pkg/cache/remote/mock/mock.go`
  - Provides a thread-safe in-memory client used by tests and the `--remote-cache=mock` mode.
- Create `pkg/cache/remote/mock/mock_test.go`
  - Tests exact get, range get, miss, put copy semantics, delete, and close behavior.
- Modify `pkg/chunk/cached_store.go`
  - Adds remote cache fields to `Config` and `cachedStore`.
  - Adds remote cache metrics.
  - Adds helper methods for remote get and put.
  - Integrates remote cache into `load` after local cache miss and before object storage.
- Modify `pkg/chunk/cached_store_test.go`
  - Adds tests for remote cache hit, remote miss fallback, remote error fallback, remote fill after object storage, and local cache precedence.
- Modify `cmd/flags.go`
  - Adds mount flags for the Phase 1 remote cache.
- Modify `cmd/mount.go`
  - Parses mount flags into `chunk.Config`.

## Task 1: Remote Cache Interface

**Files:**
- Create: `pkg/cache/remote/remote.go`
- Test: `go test ./pkg/cache/remote/...`

- [ ] **Step 1: Create the remote cache package**

Create `pkg/cache/remote/remote.go` with this content:

```go
/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package remote

import (
	"context"
	"errors"
	"io"
)

var (
	ErrMiss        = errors.New("remote cache miss")
	ErrUnavailable = errors.New("remote cache unavailable")
	ErrDisabled    = errors.New("remote cache disabled")
)

type Client interface {
	Get(ctx context.Context, key string, off, size int) (io.ReadCloser, error)
	Put(ctx context.Context, key string, data []byte) error
	Delete(ctx context.Context, key string) error
	Close() error
}
```

- [ ] **Step 2: Run package tests**

Run:

```bash
go test ./pkg/cache/remote/...
```

Expected: package builds and reports no test files.

- [ ] **Step 3: Commit**

```bash
git add pkg/cache/remote/remote.go
git commit -m "feat: add remote cache interface"
```

## Task 2: Mock Remote Cache

**Files:**
- Create: `pkg/cache/remote/mock/mock.go`
- Create: `pkg/cache/remote/mock/mock_test.go`

- [ ] **Step 1: Write mock tests**

Create `pkg/cache/remote/mock/mock_test.go` with this content:

```go
/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package mock

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/juicedata/juicefs/pkg/cache/remote"
	"github.com/stretchr/testify/require"
)

func TestClientGetPutRangeAndDelete(t *testing.T) {
	c := NewClient()
	require.NoError(t, c.Put(context.Background(), "k", []byte("abcdef")))

	r, err := c.Get(context.Background(), "k", 2, 3)
	require.NoError(t, err)
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.Equal(t, []byte("cde"), data)

	r, err = c.Get(context.Background(), "k", 0, -1)
	require.NoError(t, err)
	data, err = io.ReadAll(r)
	require.NoError(t, err)
	require.Equal(t, []byte("abcdef"), data)
	require.NoError(t, r.Close())

	require.NoError(t, c.Delete(context.Background(), "k"))
	_, err = c.Get(context.Background(), "k", 0, -1)
	require.True(t, errors.Is(err, remote.ErrMiss))
}

func TestClientPutCopiesData(t *testing.T) {
	c := NewClient()
	data := []byte("abc")
	require.NoError(t, c.Put(context.Background(), "k", data))
	data[0] = 'z'

	r, err := c.Get(context.Background(), "k", 0, -1)
	require.NoError(t, err)
	got, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Equal(t, []byte("abc"), got)
	require.NoError(t, r.Close())
}

func TestClientCloseDisablesOperations(t *testing.T) {
	c := NewClient()
	require.NoError(t, c.Close())

	require.ErrorIs(t, c.Put(context.Background(), "k", []byte("v")), remote.ErrUnavailable)
	_, err := c.Get(context.Background(), "k", 0, -1)
	require.ErrorIs(t, err, remote.ErrUnavailable)
	require.ErrorIs(t, c.Delete(context.Background(), "k"), remote.ErrUnavailable)
}
```

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
go test ./pkg/cache/remote/mock
```

Expected: FAIL because `NewClient` is not defined.

- [ ] **Step 3: Implement mock client**

Create `pkg/cache/remote/mock/mock.go` with this content:

```go
/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package mock

import (
	"bytes"
	"context"
	"io"
	"sync"

	"github.com/juicedata/juicefs/pkg/cache/remote"
)

type Client struct {
	mu     sync.RWMutex
	closed bool
	data   map[string][]byte
}

func NewClient() *Client {
	return &Client{data: make(map[string][]byte)}
}

func (c *Client) Get(ctx context.Context, key string, off, size int) (io.ReadCloser, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return nil, remote.ErrUnavailable
	}
	value, ok := c.data[key]
	if !ok {
		return nil, remote.ErrMiss
	}
	if off < 0 || off > len(value) {
		return nil, remote.ErrMiss
	}
	end := len(value)
	if size >= 0 {
		end = off + size
		if end > len(value) {
			return nil, remote.ErrMiss
		}
	}
	out := make([]byte, end-off)
	copy(out, value[off:end])
	return io.NopCloser(bytes.NewReader(out)), nil
}

func (c *Client) Put(ctx context.Context, key string, data []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return remote.ErrUnavailable
	}
	value := make([]byte, len(data))
	copy(value, data)
	c.data[key] = value
	return nil
}

func (c *Client) Delete(ctx context.Context, key string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return remote.ErrUnavailable
	}
	delete(c.data, key)
	return nil
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}
```

- [ ] **Step 4: Run tests and verify pass**

Run:

```bash
go test ./pkg/cache/remote/...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/cache/remote
git commit -m "test: add mock remote cache"
```

## Task 3: Chunk Config and Metrics

**Files:**
- Modify: `pkg/chunk/cached_store.go`
- Test: `go test ./pkg/chunk -run TestRemoteCacheConfigValidation`

- [ ] **Step 1: Add config validation test**

Append this test to `pkg/chunk/cached_store_test.go`:

```go
func TestRemoteCacheConfigValidation(t *testing.T) {
	conf := defaultConf
	conf.RemoteCacheMode = "bad"
	conf.SelfCheck("test-uuid")

	require.Equal(t, "none", conf.RemoteCacheMode)
	require.Equal(t, 50*time.Millisecond, conf.RemoteCacheTimeout)
}
```

- [ ] **Step 2: Run test and verify failure**

Run:

```bash
go test ./pkg/chunk -run TestRemoteCacheConfigValidation
```

Expected: FAIL because `RemoteCacheMode` and `RemoteCacheTimeout` are missing.

- [ ] **Step 3: Add config fields and defaults**

In `pkg/chunk/cached_store.go`, add these fields to `type Config struct` after `Prefetch int`:

```go
	RemoteCacheMode       string
	RemoteCacheNodes      string
	RemoteCacheTimeout    time.Duration
	RemoteCacheFillLocal  bool
	RemoteCacheFillRemote bool
```

In `func (c *Config) SelfCheck(uuid string)`, add this block near the end before the cache eviction checks:

```go
	if c.RemoteCacheMode == "" {
		c.RemoteCacheMode = "none"
	}
	if c.RemoteCacheMode != "none" && c.RemoteCacheMode != "mock" && c.RemoteCacheMode != "rdma" {
		logger.Warnf("remote-cache should be one of [none, mock, rdma], setting it to none")
		c.RemoteCacheMode = "none"
	}
	if c.RemoteCacheTimeout == 0 {
		c.RemoteCacheTimeout = 50 * time.Millisecond
	}
```

Add remote metric fields to `type cachedStore struct` after `stageBlockErrors prometheus.Counter`:

```go
	remoteCacheGets      *prometheus.CounterVec
	remoteCacheGetBytes  *prometheus.CounterVec
	remoteCachePuts      *prometheus.CounterVec
	remoteCachePutBytes  *prometheus.CounterVec
	remoteCacheGetHist   prometheus.Histogram
	remoteCachePutHist   prometheus.Histogram
	remoteCacheFallbacks prometheus.Counter
```

In `initMetrics`, append:

```go
	store.remoteCacheGets = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "remote_cache_gets_total",
		Help: "remote cache get requests",
	}, []string{"result"})
	store.remoteCacheGetBytes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "remote_cache_get_bytes_total",
		Help: "remote cache get bytes",
	}, []string{"result"})
	store.remoteCachePuts = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "remote_cache_puts_total",
		Help: "remote cache put requests",
	}, []string{"result"})
	store.remoteCachePutBytes = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "remote_cache_put_bytes_total",
		Help: "remote cache put bytes",
	}, []string{"result"})
	store.remoteCacheGetHist = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "remote_cache_get_seconds",
		Help:    "remote cache get latency distribution",
		Buckets: prometheus.ExponentialBuckets(0.00001, 2, 20),
	})
	store.remoteCachePutHist = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "remote_cache_put_seconds",
		Help:    "remote cache put latency distribution",
		Buckets: prometheus.ExponentialBuckets(0.00001, 2, 20),
	})
	store.remoteCacheFallbacks = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "remote_cache_fallbacks_total",
		Help: "remote cache fallbacks to object storage",
	})
```

In `regMetrics`, register those metrics after `stageBlockErrors`:

```go
	reg.MustRegister(store.remoteCacheGets)
	reg.MustRegister(store.remoteCacheGetBytes)
	reg.MustRegister(store.remoteCachePuts)
	reg.MustRegister(store.remoteCachePutBytes)
	reg.MustRegister(store.remoteCacheGetHist)
	reg.MustRegister(store.remoteCachePutHist)
	reg.MustRegister(store.remoteCacheFallbacks)
```

- [ ] **Step 4: Run test and verify pass**

Run:

```bash
go test ./pkg/chunk -run TestRemoteCacheConfigValidation
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/chunk/cached_store.go pkg/chunk/cached_store_test.go
git commit -m "feat: add remote cache chunk config"
```

## Task 4: Remote Cache Read-Through Integration

**Files:**
- Modify: `pkg/chunk/cached_store.go`
- Modify: `pkg/chunk/cached_store_test.go`

- [ ] **Step 1: Add chunk tests for read-through behavior**

Append these helper types and tests to `pkg/chunk/cached_store_test.go`:

```go
type countingStore struct {
	object.ObjectStorage
	gets atomic.Int32
}

func (s *countingStore) Get(ctx context.Context, key string, off, limit int64, getters ...object.AttrGetter) (io.ReadCloser, error) {
	s.gets.Add(1)
	return s.ObjectStorage.Get(ctx, key, off, limit, getters...)
}

type failingRemoteCache struct{}

func (f failingRemoteCache) Get(ctx context.Context, key string, off, size int) (io.ReadCloser, error) {
	return nil, errors.New("remote cache failed")
}
func (f failingRemoteCache) Put(ctx context.Context, key string, data []byte) error { return errors.New("remote cache failed") }
func (f failingRemoteCache) Delete(ctx context.Context, key string) error           { return nil }
func (f failingRemoteCache) Close() error                                           { return nil }

func TestRemoteCacheHitAvoidsObjectStorage(t *testing.T) {
	blob, _ := object.CreateStorage("mem", "", "", "", "")
	counting := &countingStore{ObjectStorage: blob}
	conf := defaultConf
	conf.CacheSize = 0
	conf.RemoteCacheMode = "mock"
	conf.RemoteCacheFillRemote = true
	store := NewCachedStore(counting, conf, nil).(*cachedStore)

	key := "chunks/0/0/123_0_4"
	require.NoError(t, store.remoteCache.Put(context.Background(), key, []byte("good")))

	p := NewPage(make([]byte, 4))
	defer p.Release()
	require.NoError(t, store.load(context.Background(), key, p, false, false))
	require.Equal(t, []byte("good"), p.Data)
	require.Equal(t, int32(0), counting.gets.Load())
}

func TestRemoteCacheMissFallsBackToObjectStorageAndFillsRemote(t *testing.T) {
	blob, _ := object.CreateStorage("mem", "", "", "", "")
	require.NoError(t, blob.Put(context.Background(), "chunks/0/0/124_0_4", bytes.NewReader([]byte("cold"))))
	counting := &countingStore{ObjectStorage: blob}
	conf := defaultConf
	conf.CacheSize = 0
	conf.RemoteCacheMode = "mock"
	store := NewCachedStore(counting, conf, nil).(*cachedStore)

	p := NewPage(make([]byte, 4))
	defer p.Release()
	require.NoError(t, store.load(context.Background(), "chunks/0/0/124_0_4", p, false, false))
	require.Equal(t, []byte("cold"), p.Data)
	require.Equal(t, int32(1), counting.gets.Load())

	p2 := NewPage(make([]byte, 4))
	defer p2.Release()
	require.NoError(t, store.load(context.Background(), "chunks/0/0/124_0_4", p2, false, false))
	require.Equal(t, []byte("cold"), p2.Data)
	require.Equal(t, int32(1), counting.gets.Load())
}

func TestRemoteCacheErrorFallsBackToObjectStorage(t *testing.T) {
	blob, _ := object.CreateStorage("mem", "", "", "", "")
	require.NoError(t, blob.Put(context.Background(), "chunks/0/0/125_0_4", bytes.NewReader([]byte("safe"))))
	counting := &countingStore{ObjectStorage: blob}
	conf := defaultConf
	conf.CacheSize = 0
	store := NewCachedStore(counting, conf, nil).(*cachedStore)
	store.remoteCache = failingRemoteCache{}

	p := NewPage(make([]byte, 4))
	defer p.Release()
	require.NoError(t, store.load(context.Background(), "chunks/0/0/125_0_4", p, false, false))
	require.Equal(t, []byte("safe"), p.Data)
	require.Equal(t, int32(1), counting.gets.Load())
}
```

- [ ] **Step 2: Run tests and verify failure**

Run:

```bash
go test ./pkg/chunk -run 'TestRemoteCache(Hit|Miss|Error)'
```

Expected: FAIL because `remoteCache` and the read-through helpers are not implemented.

- [ ] **Step 3: Implement remote cache integration**

In `pkg/chunk/cached_store.go`, add imports:

```go
	"github.com/juicedata/juicefs/pkg/cache/remote"
	"github.com/juicedata/juicefs/pkg/cache/remote/mock"
```

Add this field to `type cachedStore struct`:

```go
	remoteCache remote.Client
```

In `NewCachedStore`, after creating `store`, initialize mock mode:

```go
	if config.RemoteCacheMode == "mock" {
		store.remoteCache = mock.NewClient()
	}
```

Add helper methods near `load`:

```go
func (store *cachedStore) remoteCacheEnabled() bool {
	return store.remoteCache != nil && store.conf.RemoteCacheMode != "none"
}

func (store *cachedStore) loadRemote(ctx context.Context, key string, page *Page) bool {
	if !store.remoteCacheEnabled() {
		return false
	}
	start := time.Now()
	rcCtx, cancel := context.WithTimeout(ctx, store.conf.RemoteCacheTimeout)
	defer cancel()

	in, err := store.remoteCache.Get(rcCtx, key, 0, len(page.Data))
	used := time.Since(start)
	store.remoteCacheGetHist.Observe(used.Seconds())
	if err != nil {
		if errors.Is(err, remote.ErrMiss) {
			store.remoteCacheGets.WithLabelValues("miss").Add(1)
		} else if errors.Is(err, context.DeadlineExceeded) {
			store.remoteCacheGets.WithLabelValues("timeout").Add(1)
		} else {
			store.remoteCacheGets.WithLabelValues("error").Add(1)
		}
		store.remoteCacheFallbacks.Add(1)
		return false
	}
	defer in.Close()

	n, err := io.ReadFull(in, page.Data)
	if err != nil || n != len(page.Data) {
		store.remoteCacheGets.WithLabelValues("error").Add(1)
		store.remoteCacheFallbacks.Add(1)
		return false
	}
	store.remoteCacheGets.WithLabelValues("hit").Add(1)
	store.remoteCacheGetBytes.WithLabelValues("hit").Add(float64(n))
	return true
}

func (store *cachedStore) fillRemote(ctx context.Context, key string, page *Page) {
	if !store.remoteCacheEnabled() || !store.conf.RemoteCacheFillRemote {
		return
	}
	data := make([]byte, len(page.Data))
	copy(data, page.Data)
	go func() {
		start := time.Now()
		rcCtx, cancel := context.WithTimeout(context.Background(), store.conf.RemoteCacheTimeout)
		defer cancel()
		err := store.remoteCache.Put(rcCtx, key, data)
		used := time.Since(start)
		store.remoteCachePutHist.Observe(used.Seconds())
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				store.remoteCachePuts.WithLabelValues("timeout").Add(1)
			} else {
				store.remoteCachePuts.WithLabelValues("error").Add(1)
			}
			return
		}
		store.remoteCachePuts.WithLabelValues("ok").Add(1)
		store.remoteCachePutBytes.WithLabelValues("ok").Add(float64(len(data)))
	}()
}
```

At the start of `func (store *cachedStore) load(...)`, after `recover` setup and before `store.currentDownload <- struct{}{}`, insert:

```go
	if !forceCache && store.loadRemote(ctx, key, page) {
		if cache && store.conf.RemoteCacheFillLocal {
			store.bcache.cache(key, page, forceCache, !store.conf.OSCache)
		}
		return nil
	}
```

After a successful object storage read and decompression, before local cache write:

```go
	store.fillRemote(ctx, key, page)
```

- [ ] **Step 4: Run tests and verify pass**

Run:

```bash
go test ./pkg/chunk -run 'TestRemoteCache(Hit|Miss|Error)'
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/chunk/cached_store.go pkg/chunk/cached_store_test.go
git commit -m "feat: add remote cache read-through path"
```

## Task 5: Mount Flags

**Files:**
- Modify: `cmd/flags.go`
- Modify: `cmd/mount.go`
- Modify: `cmd/main_test.go`
- Test: `go test ./cmd -run TestHandleSysMountArgs`

- [ ] **Step 1: Add mount flag test**

In `cmd/main_test.go`, add this case to the `cases` slice in `TestHandleSysMountArgs`:

```go
		{
			[]string{"/mount.juicefs", "memkv://", "/jfs", "-o", "remote-cache=mock,remote-cache-nodes=127.0.0.1:9000,remote-cache-timeout=25ms,remote-cache-fill-local=true,remote-cache-fill-remote=false"},
			"juicefs mount -d --remote-cache=mock --remote-cache-nodes=127.0.0.1:9000 --remote-cache-timeout=25ms --remote-cache-fill-local=true --remote-cache-fill-remote=false memkv:// /jfs",
			false,
		},
```

- [ ] **Step 2: Run test and verify failure**

Run:

```bash
go test ./cmd -run TestHandleSysMountArgs
```

Expected: FAIL because the new flags are not registered.

- [ ] **Step 3: Add flags**

In `cmd/flags.go`, add these flags to `dataCacheFlags()` after `prefetch`:

```go
		&cli.StringFlag{
			Name:  "remote-cache",
			Value: "none",
			Usage: "remote cache mode (none, mock, rdma)",
		},
		&cli.StringFlag{
			Name:  "remote-cache-nodes",
			Usage: "comma separated remote cache nodes",
		},
		&cli.StringFlag{
			Name:  "remote-cache-timeout",
			Value: "50ms",
			Usage: "timeout for remote cache requests",
		},
		&cli.BoolFlag{
			Name:  "remote-cache-fill-local",
			Value: true,
			Usage: "fill local cache after remote cache hit",
		},
		&cli.BoolFlag{
			Name:  "remote-cache-fill-remote",
			Value: true,
			Usage: "fill remote cache after object storage read",
		},
```

In `cmd/mount.go`, add these assignments in `getChunkConf`:

```go
		RemoteCacheMode:       c.String("remote-cache"),
		RemoteCacheNodes:      c.String("remote-cache-nodes"),
		RemoteCacheTimeout:    utils.Duration(c.String("remote-cache-timeout")),
		RemoteCacheFillLocal:  c.Bool("remote-cache-fill-local"),
		RemoteCacheFillRemote: c.Bool("remote-cache-fill-remote"),
```

- [ ] **Step 4: Run command tests**

Run:

```bash
go test ./cmd -run TestHandleSysMountArgs
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/flags.go cmd/mount.go cmd/main_test.go
git commit -m "feat: add remote cache mount flags"
```

## Task 6: Final Verification

**Files:**
- Verify: `pkg/cache/remote/...`
- Verify: `pkg/chunk`
- Verify: `cmd`

- [ ] **Step 1: Format changed Go files**

Run:

```bash
gofmt -w pkg/cache/remote/remote.go pkg/cache/remote/mock/mock.go pkg/cache/remote/mock/mock_test.go pkg/chunk/cached_store.go pkg/chunk/cached_store_test.go cmd/flags.go cmd/mount.go cmd/main_test.go
```

Expected: command exits with status 0.

- [ ] **Step 2: Run focused tests**

Run:

```bash
go test ./pkg/cache/remote/... ./pkg/chunk -run 'TestRemoteCache|TestStoreRetry|TestStoreDefault|TestFillCache'
```

Expected: PASS.

- [ ] **Step 3: Run command package flag test**

Run:

```bash
go test ./cmd -run TestHandleSysMountArgs
```

Expected: PASS or a documented skip if the local environment lacks a command test dependency already required by existing `cmd` tests.

- [ ] **Step 4: Inspect worktree**

Run:

```bash
git status --short --branch
git log --oneline -6
```

Expected: branch is `feature/rdma-distributed-cache`; recent commits include the Phase 1 commits above.

- [ ] **Step 5: Final commit if formatting changed files**

If `gofmt` changed files after Task 5, run:

```bash
git add pkg/cache/remote pkg/chunk/cached_store.go pkg/chunk/cached_store_test.go cmd/flags.go cmd/mount.go cmd/main_test.go
git commit -m "chore: format remote cache phase one"
```

Expected: commit is created only if `git diff --cached --quiet` is false.

## Self-Review

- Spec coverage: Phase 1 covers interface, mock implementation, chunk read-through behavior, object storage fallback, metrics, mount flags, and tests. Real RDMA transport and server skeleton remain intentionally outside Phase 1.
- Placeholder scan: This plan contains concrete file paths, code snippets, commands, and expected outcomes for each task.
- Type consistency: `remote.Client` is used by `cachedStore.remoteCache`; mock implements the same methods; tests call `store.load` with full-block pages to match Phase 1 scope.
