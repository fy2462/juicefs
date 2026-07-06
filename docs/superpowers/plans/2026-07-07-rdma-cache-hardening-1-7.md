# RDMA Cache Hardening 1-7 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete configuration, metrics, active probing, open-rdma native boundary, multi-node smoke, failure recovery coverage, and the user runbook for the three-tier cache.

**Architecture:** Keep L3 object storage authoritative and keep L2 health in local client memory. Extend the existing `pkg/cache/remote/cluster` helper with observer and probe support, wire options through `cmd` and `pkg/chunk`, then reuse the shared behavior from HTTP and RDMA clients.

**Tech Stack:** Go, JuiceFS CLI flags, Prometheus metrics, existing remote cache HTTP/RDMA packages, POSIX shell smoke tests, RustFS Docker S3 endpoint.

---

## File Structure

- Modify `cmd/flags.go`
  - Add user-facing remote cache health/probe flags.
- Modify `cmd/mount.go`
  - Copy new CLI flags into `chunk.Config`.
- Modify `pkg/chunk/cached_store.go`
  - Add config fields and pass them to HTTP/RDMA remote clients.
  - Provide the Prometheus-backed cluster health observer.
- Modify `pkg/cache/remote/cluster/health.go`
  - Add node state snapshots, observer callbacks, skip/probe accounting, and recovery marking.
- Modify `pkg/cache/remote/cluster/cluster_test.go`
  - Cover state transitions and observer events.
- Modify `pkg/cache/remote/httpcache/client.go`
  - Add active probe loop and `/healthz` probe request.
- Modify `pkg/cache/remote/httpcache/server.go`
  - Add `/healthz`.
- Modify `pkg/cache/remote/httpcache/httpcache_test.go`
  - Cover HTTP active recovery and metric observer events.
- Modify `pkg/cache/remote/rdma/protocol/protocol.go`
  - Add `PING` request/response.
- Modify `pkg/cache/remote/rdma/executor.go` and `pkg/cache/remote/rdma/server.go`
  - Execute/respond to `PING`.
- Modify `pkg/cache/remote/rdma/types.go`
  - Add active probe loop and options.
- Modify `pkg/cache/remote/rdma/rdma_test.go`
  - Cover `PING`, active recovery, and multi-node fallback.
- Modify `pkg/cache/remote/rdma/native_rdma.go`
  - Add build-tagged open-rdma capability/readiness boundary.
- Modify `hack/three-tier-cache-rustfs-test.sh`
  - Add multi-node L2 and recovery stages.
- Modify `docs/superpowers/runbooks/2026-07-06-three-tier-cache-rustfs.md`
  - Keep the existing RustFS runbook current.
- Create `docs/superpowers/runbooks/2026-07-07-rdma-distributed-cache.md`
  - Add the full operator runbook for local disk + RDMA L2 + S3/RustFS.

## Task 1: Configuration Entrypoints

- [ ] Add failing tests proving mount flags flow into `chunk.Config` and client options.
- [ ] Add flags in `cmd/flags.go`:
  - `remote-cache-fail-threshold`
  - `remote-cache-node-cooldown`
  - `remote-cache-probe-interval`
  - `remote-cache-probe-timeout`
- [ ] Add matching fields to `pkg/chunk.Config`.
- [ ] Wire `cmd/mount.go` into `chunk.Config`.
- [ ] Pass options into `httpcache.Options` and `rdma.Options`.
- [ ] Run:

```bash
PATH=/usr/local/go/bin:$PATH go test ./cmd ./pkg/chunk ./pkg/cache/remote/httpcache ./pkg/cache/remote/rdma
```

- [ ] Commit:

```bash
git add cmd pkg/chunk pkg/cache/remote
git commit -m "feat: expose remote cache health config"
```

## Task 2: Health Metrics Observer

- [ ] Add failing tests in `pkg/cache/remote/cluster` using a recording observer:
  - failure threshold emits node down
  - skipped down node emits skip
  - success emits recovery
  - probe result emits probe metric
- [ ] Add observer types to `pkg/cache/remote/cluster/health.go`:

```go
type State string

const (
	StateHealthy State = "healthy"
	StateDown    State = "down"
)

type ProbeResult string

const (
	ProbeSuccess ProbeResult = "success"
	ProbeFailure ProbeResult = "failure"
)

type Observer interface {
	NodeDown(node string)
	NodeRecovered(node string)
	NodeSkipped(node, op string)
	NodeProbe(node string, result ProbeResult)
}
```

- [ ] Add observer callbacks to `MarkFailure`, `MarkSuccess`, `Available`, and probe helpers.
- [ ] Add Prometheus metrics in `pkg/chunk/cached_store.go` through a small observer adapter.
- [ ] Run:

```bash
PATH=/usr/local/go/bin:$PATH go test ./pkg/cache/remote/cluster ./pkg/chunk
```

- [ ] Commit:

```bash
git add pkg/cache/remote/cluster pkg/chunk
git commit -m "feat: add remote cache health metrics"
```

## Task 3: Active Probe

- [ ] Add failing HTTP tests proving a down node is actively probed and marked healthy after `/healthz` succeeds.
- [ ] Add failing RDMA tests proving `PING` succeeds through the protocol executor.
- [ ] Add `ProbeInterval` and `ProbeTimeout` to HTTP/RDMA options.
- [ ] Add `StartProber`/`stopProber` behavior inside each client constructor and `Close`.
- [ ] Add HTTP `/healthz`.
- [ ] Add RDMA protocol `PING` and server handling.
- [ ] Run:

```bash
PATH=/usr/local/go/bin:$PATH go test ./pkg/cache/remote/httpcache ./pkg/cache/remote/rdma ./pkg/cache/remote/rdma/protocol
```

- [ ] Commit:

```bash
git add pkg/cache/remote
git commit -m "feat: actively probe remote cache nodes"
```

## Task 4: Native/Open-RDMA Boundary

- [ ] Add failing build-tag tests or smoke assertions proving `-tags rdma` reports `Capability().Built == true`.
- [ ] Update `pkg/cache/remote/rdma/native_rdma.go` so the native build tag exposes an open-rdma readiness check without affecting default builds.
- [ ] Keep the default build returning `ErrUnsupported`.
- [ ] Run:

```bash
PATH=/usr/local/go/bin:$PATH go test ./pkg/cache/remote/rdma
PATH=/usr/local/go/bin:$PATH go test -tags rdma ./pkg/cache/remote/rdma
```

- [ ] Commit:

```bash
git add pkg/cache/remote/rdma
git commit -m "feat: add open-rdma native transport boundary"
```

## Task 5: Multi-Node L2 Smoke

- [ ] Extend `hack/three-tier-cache-rustfs-test.sh` to start two remote cache servers with separate disk paths and ports.
- [ ] Add a smoke stage that fills both L2 nodes with `--remote-cache-replicas=2`.
- [ ] Stop RustFS and one L2 node.
- [ ] Remount with empty L1 and verify the read succeeds from the surviving L2.
- [ ] Run:

```bash
PATH=/usr/local/go/bin:$PATH make test.three-tier-cache-rustfs
```

- [ ] Commit:

```bash
git add hack/three-tier-cache-rustfs-test.sh
git commit -m "test: cover multi-node l2 rustfs smoke"
```

## Task 6: Failure Recovery Coverage

- [ ] Add unit tests proving one failed RDMA node is skipped and the next replica is used.
- [ ] Add chunk-level tests proving all L2 down falls back to L1+L3.
- [ ] Add smoke stage proving a restarted L2 node can receive fills again.
- [ ] Run:

```bash
PATH=/usr/local/go/bin:$PATH go test -gcflags="all=-N -l" ./pkg/cache/remote/... ./pkg/chunk -run 'TestRemoteCache|TestClient|TestHealth|TestPlacement|TestDisk|TestHTTP|TestRDM'
PATH=/usr/local/go/bin:$PATH make test.three-tier-cache-rustfs
```

- [ ] Commit:

```bash
git add pkg/cache/remote pkg/chunk hack/three-tier-cache-rustfs-test.sh
git commit -m "test: cover remote cache failure recovery"
```

## Task 7: User Runbook

- [ ] Create `docs/superpowers/runbooks/2026-07-07-rdma-distributed-cache.md`.
- [ ] Document:
  - Go path assumptions: `/usr/local/go` and `~/go`.
  - RustFS Docker startup.
  - Local disk cache mount flags.
  - Multi-node remote cache flags.
  - RDMA readiness and native build-tag checks.
  - Metrics and expected failover behavior.
- [ ] Update `docs/superpowers/runbooks/2026-07-06-three-tier-cache-rustfs.md` for the new smoke stages.
- [ ] Run:

```bash
PATH=/usr/local/go/bin:$PATH make test.three-tier-cache-rustfs
```

- [ ] Commit:

```bash
git add docs/superpowers/runbooks
git commit -m "docs: add rdma distributed cache runbook"
```

## Final Verification

Run:

```bash
PATH=/usr/local/go/bin:$PATH go test -gcflags="all=-N -l" ./pkg/cache/remote/... ./pkg/chunk -run 'TestRemoteCache|TestClient|TestHealth|TestPlacement|TestDisk|TestHTTP|TestRDM'
PATH=/usr/local/go/bin:$PATH go test ./cmd
PATH=/usr/local/go/bin:$PATH go test -tags rdma ./pkg/cache/remote/rdma
PATH=/usr/local/go/bin:$PATH make test.three-tier-cache-rustfs
```

Then inspect:

```bash
git status --short
git log --oneline -10
```
