# RDMA Native Productionization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the remaining RDMA productionization scope: native verbs data path, native `rdma-cache-server`, open-rdma smoke, layered CI, stress tests, alert examples, and stable operator docs.

**Architecture:** Keep the existing `rdma.Client` placement/health/probe behavior unchanged and implement native transport behind `rdma.Dialer`, `rdma.Conn`, and `rdma.ListenAndServe`. Default builds remain dependency-free; cgo/libibverbs code is isolated behind `//go:build rdma && linux && cgo`.

**Tech Stack:** Go, cgo, libibverbs/open-rdma mock mode, existing RDMA protocol JSON frames, POSIX shell smoke/stress scripts, GitHub Actions, Prometheus alert rule examples.

---

## File Structure

- Modify `pkg/cache/remote/rdma/types.go`
  - Add public server options and `ListenAndServe` API references used by command code.
- Modify `pkg/cache/remote/rdma/rdma.go`
  - Keep default build unsupported for native server and native dialer.
- Modify `pkg/cache/remote/rdma/native_rdma.go`
  - Keep `Capability()` and wire `NewClient` to the native dialer only when supported.
- Create `pkg/cache/remote/rdma/native_stub.go`
  - `//go:build rdma && (!linux || !cgo)` stub for builds that set `rdma` without cgo/Linux.
- Create `pkg/cache/remote/rdma/native_linux_cgo.go`
  - `//go:build rdma && linux && cgo` native dialer/server entry points.
- Create `pkg/cache/remote/rdma/native/`
  - cgo-backed verbs implementation files.
- Modify `cmd/rdma_cache_server.go`
  - Route `--transport=rdma` to `rdma.ListenAndServe`.
- Modify `cmd/rdma_cache_server_test.go`
  - Update default unsupported tests and add build-tag tests.
- Create `hack/rdma-native-smoke-test.sh`
  - Open-rdma direct Ping/Put/Get/Delete smoke.
- Modify `Makefile`
  - Add `test.rdma-tag` and `test.rdma-native-smoke`.
- Create `.github/workflows/rdma-cache.yml`
  - Add default unit, rdma-tag compile, RustFS smoke, and manual native smoke jobs.
- Create `hack/rdma-cache-stress.sh`
  - Direct native remote cache stress harness.
- Create `docs/superpowers/runbooks/2026-07-07-rdma-native-cache.md`
  - Native operator runbook.
- Create `docs/superpowers/runbooks/2026-07-07-rdma-cache-alerts.yml`
  - Prometheus alert examples.

## Task 1: Public Native Server Boundary

**Files:**
- Modify `pkg/cache/remote/rdma/types.go`
- Modify `pkg/cache/remote/rdma/rdma.go`
- Modify `pkg/cache/remote/rdma/native_rdma.go`
- Create `pkg/cache/remote/rdma/native_stub.go`
- Modify `cmd/rdma_cache_server.go`
- Modify `cmd/rdma_cache_server_test.go`

- [ ] Add failing default-build tests:

```go
func TestListenAndServeDefaultBuildUnsupported(t *testing.T) {
	err := rdma.ListenAndServe(context.Background(), rdma.ServeOptions{
		Listen:  "127.0.0.1:0",
		Backend: mock.NewClient(),
	})
	require.ErrorIs(t, err, rdma.ErrUnsupported)
}
```

```go
func TestRDMACacheServerTransportRDMAReturnsUnsupportedByDefault(t *testing.T) {
	handler, err := newRDMACacheServerHandler("rdma", mock.NewClient())
	require.Nil(t, handler)
	require.ErrorIs(t, err, rdma.ErrUnsupported)
}
```

- [ ] Run RED:

```bash
PATH=/usr/local/go/bin:$PATH go test ./pkg/cache/remote/rdma ./cmd -run 'TestListenAndServe|TestRDMACacheServerTransportRDMA'
```

Expected: fail because `ServeOptions` and `ListenAndServe` do not exist.

- [ ] Add `ServeOptions` to `pkg/cache/remote/rdma/types.go`:

```go
type ServeOptions struct {
	Listen        string
	Backend       remote.Client
	MaxFrameBytes int
}
```

- [ ] Add default unsupported `ListenAndServe` to `pkg/cache/remote/rdma/rdma.go`:

```go
func ListenAndServe(ctx context.Context, options ServeOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrUnsupported
}
```

- [ ] Add `rdma` non-cgo stub in `pkg/cache/remote/rdma/native_stub.go`:

```go
//go:build rdma && (!linux || !cgo)

package rdma

import "context"

func ListenAndServe(ctx context.Context, options ServeOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrUnsupported
}
```

- [ ] Keep `cmd/rdma_cache_server.go` helper behavior unchanged for this task: `transport=rdma` returns the unsupported handler error path. Task 5 replaces the command flow with `rdma.ListenAndServe` for native builds.

```go
case "rdma":
	return nil, rdma.ErrUnsupported
```

- [ ] Run GREEN:

```bash
PATH=/usr/local/go/bin:$PATH go test ./pkg/cache/remote/rdma ./cmd -run 'TestListenAndServe|TestRDMACacheServerTransportRDMA'
PATH=/usr/local/go/bin:$PATH go test -tags rdma ./pkg/cache/remote/rdma
```

- [ ] Commit:

```bash
git add pkg/cache/remote/rdma cmd/rdma_cache_server.go cmd/rdma_cache_server_test.go
git commit -m "feat: add rdma native server boundary"
```

## Task 2: Frame Codec Limits

**Files:**
- Create `pkg/cache/remote/rdma/frame.go`
- Create `pkg/cache/remote/rdma/frame_test.go`

- [ ] Add failing tests for framed protocol encoding:
  - length prefix is big-endian uint32
  - payload round-trips through `protocol.DecodeRequest`
  - frames larger than max return `remote.ErrUnavailable`
  - short reads return `io.ErrUnexpectedEOF`

- [ ] Implement helpers:

```go
func encodeFrame(payload []byte, maxFrameBytes int) ([]byte, error)
func readFrame(r io.Reader, maxFrameBytes int) ([]byte, error)
func maxFrameBytes(value int) int
```

- [ ] Defaults:

```text
default max frame: 4 MiB
minimum max frame: 64 KiB
```

- [ ] Run:

```bash
PATH=/usr/local/go/bin:$PATH go test ./pkg/cache/remote/rdma -run 'TestFrame'
```

- [ ] Commit:

```bash
git add pkg/cache/remote/rdma/frame.go pkg/cache/remote/rdma/frame_test.go
git commit -m "feat: add rdma protocol frame codec"
```

## Task 3: Native Cgo Verbs Skeleton

**Files:**
- Create `pkg/cache/remote/rdma/native/verbs_linux_cgo.go`
- Create `pkg/cache/remote/rdma/native/errors.go`
- Create `pkg/cache/remote/rdma/native/doc.go`

- [ ] Add build-tagged cgo file with libibverbs compile guard:

```go
//go:build rdma && linux && cgo

package native

/*
#cgo LDFLAGS: -libverbs
#include <infiniband/verbs.h>
*/
import "C"
```

- [ ] Define minimal resource wrapper types:

```go
type Resources struct {
	deviceIndex int
	maxFrameBytes int
}
```

- [ ] Define explicit setup API:

```go
func NewResources(deviceIndex, maxFrameBytes int) (*Resources, error)
func (r *Resources) Close() error
```

- [ ] Run compile gate:

```bash
PATH=/usr/local/go/bin:$PATH go test -tags rdma ./pkg/cache/remote/rdma/...
```

Expected in environments without cgo/libibverbs: skip native cgo package or fail with a clear missing dependency. Fix build tags until default and rdma-tag package tests remain usable.

- [ ] Commit:

```bash
git add pkg/cache/remote/rdma/native
git commit -m "feat: add rdma verbs resource skeleton"
```

## Task 4: Native Dialer and Conn

**Files:**
- Modify `pkg/cache/remote/rdma/native_linux_cgo.go`
- Create `pkg/cache/remote/rdma/native_conn_test.go`
- Modify `pkg/cache/remote/rdma/native_rdma.go`

- [ ] Add tests proving `NewClient(Options{Nodes: ...})` uses native dialer under `-tags rdma,linux,cgo` when no explicit dialer is provided.
- [ ] Implement native dialer option parsing:

```text
JFS_RDMA_DEVICE_INDEX
JFS_RDMA_MAX_FRAME_BYTES
JFS_RDMA_CQ_TIMEOUT
```

- [ ] Implement `nativeConn.RoundTrip` using frame encode/decode and one request at a time.
- [ ] Keep the existing explicit `Options.Dialer` override for unit tests.
- [ ] Run:

```bash
PATH=/usr/local/go/bin:$PATH go test -tags rdma ./pkg/cache/remote/rdma
```

- [ ] Commit:

```bash
git add pkg/cache/remote/rdma
git commit -m "feat: add native rdma dialer"
```

## Task 5: Native Server Transport

**Files:**
- Modify `pkg/cache/remote/rdma/native_linux_cgo.go`
- Modify `cmd/rdma_cache_server.go`
- Modify `cmd/rdma_cache_server_test.go`

- [ ] Add build-tagged tests proving `rdma.ListenAndServe` starts a listener and exits on context cancellation.
- [ ] Change command flow so `transport=rdma` does not require an `http.Handler`.
- [ ] Implement:

```go
func ListenAndServe(ctx context.Context, options ServeOptions) error
```

Behavior:

- bind TCP control listener to `options.Listen`
- accept connections until context cancellation
- create one native connection per accepted TCP connection
- pass received frames to `rdma.NewServer(options.Backend).HandleFrame`
- close all connections on shutdown

- [ ] Run:

```bash
PATH=/usr/local/go/bin:$PATH go test ./cmd -run 'TestRDMACacheServer'
PATH=/usr/local/go/bin:$PATH go test -tags rdma ./pkg/cache/remote/rdma
```

- [ ] Commit:

```bash
git add cmd/rdma_cache_server.go cmd/rdma_cache_server_test.go pkg/cache/remote/rdma
git commit -m "feat: serve rdma cache over native transport"
```

## Task 6: Native RDMA Smoke

**Files:**
- Create `hack/rdma-native-smoke-test.sh`
- Modify `Makefile`
- Create or modify `cmd/rdma_cache_smoke_test.go` only if a small helper command is needed.

- [ ] Script stages:

```sh
hack/open-rdma-smoke-test.sh --driver-dir "$OPEN_RDMA_DRIVER" --strict
PATH=/usr/local/go/bin:$PATH go test -tags rdma ./pkg/cache/remote/rdma
PATH=/usr/local/go/bin:$PATH make juicefs GO_TAGS=rdma
./juicefs rdma-cache-server --transport=rdma --listen 127.0.0.1:9568 --cache-dir "$TMP_DIR/l2" &
```

- [ ] Add direct native cache smoke using a tiny Go test binary or `go test -tags rdma -run TestNativeSmoke` gated by `OPEN_RDMA_NATIVE_SMOKE=1`.
- [ ] Prove:
  - PING succeeds
  - PUT succeeds
  - GET returns bytes
  - DELETE removes bytes
  - server shutdown exits cleanly
- [ ] Add Makefile target:

```make
test.rdma-native-smoke:
	./hack/rdma-native-smoke-test.sh
```

- [ ] Run:

```bash
OPEN_RDMA_DRIVER=/media/psf/Home/github/PFS/open-rdma-driver PATH=/usr/local/go/bin:$PATH make test.rdma-native-smoke
```

- [ ] Commit:

```bash
git add hack/rdma-native-smoke-test.sh Makefile pkg/cache/remote/rdma
git commit -m "test: add native rdma cache smoke"
```

## Task 7: CI Layers

**Files:**
- Modify `Makefile`
- Create `.github/workflows/rdma-cache.yml`
- Modify docs if workflow is documented elsewhere.

- [ ] Add targets:

```make
test.rdma-tag:
	go test -tags rdma ./pkg/cache/remote/rdma

test.rdma-cache-unit:
	go test ./pkg/cache/remote/... ./pkg/chunk
```

- [ ] Add workflow jobs:
  - `rdma-cache-unit`
  - `rdma-tag-compile`
  - `three-tier-rustfs` as manual or scheduled if Docker/RustFS is available
  - `native-open-rdma` as `workflow_dispatch`

- [ ] Run:

```bash
PATH=/usr/local/go/bin:$PATH make test.rdma-cache-unit
PATH=/usr/local/go/bin:$PATH make test.rdma-tag
```

- [ ] Commit:

```bash
git add Makefile .github/workflows/rdma-cache.yml
git commit -m "ci: add rdma cache test layers"
```

## Task 8: Stress Harness

**Files:**
- Create `hack/rdma-cache-stress.sh`
- Create `pkg/cache/remote/rdma/stress_test.go` if Go-side helpers are needed.

- [ ] Stress script options:

```text
--transport http|rdma
--nodes host:port[,host:port]
--concurrency 1|4|16|64
--size 4096|65536|1048576
--duration 30s
--json
```

- [ ] Output fields:

```json
{"ops":1000,"errors":0,"p50_ms":1.2,"p95_ms":4.5,"p99_ms":8.9}
```

- [ ] Run a short HTTP baseline locally:

```bash
PATH=/usr/local/go/bin:$PATH ./hack/rdma-cache-stress.sh --transport http --nodes 127.0.0.1:9568 --concurrency 1 --size 4096 --duration 3s --json
```

- [ ] Commit:

```bash
git add hack/rdma-cache-stress.sh pkg/cache/remote/rdma
git commit -m "test: add rdma cache stress harness"
```

## Task 9: Alerts and Operator Docs

**Files:**
- Create `docs/superpowers/runbooks/2026-07-07-rdma-native-cache.md`
- Create `docs/superpowers/runbooks/2026-07-07-rdma-cache-alerts.yml`
- Modify `docs/superpowers/runbooks/2026-07-07-rdma-distributed-cache.md`

- [ ] Add native cache runbook sections:
  - prerequisites
  - environment variables
  - native server start
  - mount flags
  - failure behavior
  - smoke and stress commands
- [ ] Add alert examples for:
  - node down
  - repeated probe failure
  - all replicas skipped
  - fallback rate spike
  - native server restart loop
- [ ] Run a docs sanity check:

```bash
rg -n "remote-cache-transport|JFS_RDMA|juicefs_remote_cache" docs/superpowers/runbooks/2026-07-07-rdma-native-cache.md docs/superpowers/runbooks/2026-07-07-rdma-cache-alerts.yml
```

- [ ] Commit:

```bash
git add docs/superpowers/runbooks
git commit -m "docs: add native rdma cache operations guide"
```

## Final Verification

Run:

```bash
PATH=/usr/local/go/bin:$PATH go test ./pkg/cache/remote/... ./pkg/chunk
PATH=/usr/local/go/bin:$PATH go test ./cmd -run 'TestRDMACacheServer|TestHandleSysMountArgs'
PATH=/usr/local/go/bin:$PATH go test -tags rdma ./pkg/cache/remote/rdma
PATH=/usr/local/go/bin:$PATH make test.three-tier-cache-rustfs
OPEN_RDMA_DRIVER=/media/psf/Home/github/PFS/open-rdma-driver PATH=/usr/local/go/bin:$PATH make test.rdma-native-smoke
```

Then inspect:

```bash
git status --short
git log --oneline -15
```
