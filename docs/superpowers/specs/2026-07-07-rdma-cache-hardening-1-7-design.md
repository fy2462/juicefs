# RDMA Cache Hardening 1-7 Design

Date: 2026-07-07

Branch: `feature/rdma-distributed-cache`

## Goal

Finish the next seven hardening items for the three-tier cache design:

1. Expose operator-facing configuration for remote cache health behavior.
2. Export health metrics so unhealthy L2/RDMA nodes are visible.
3. Add active probing so recovered nodes can re-enter service without waiting
   for user traffic.
4. Add a real native/open-rdma transport boundary behind the existing RDMA
   build tag, while preserving no-hardware testability.
5. Extend RustFS smoke coverage to multiple L2 cache nodes.
6. Cover failure and recovery behavior in automated tests.
7. Publish a user runbook for local disk + RDMA remote cache + S3/RustFS L3.

The completed system must keep the same data authority model:

```text
L1 local disk cache -> L2 remote cache nodes -> L3 S3/RustFS object storage
```

L3 remains authoritative. L2/RDMA cache nodes store best-effort copies only.

## Non-Goals

- Do not make L2 authoritative.
- Do not write L2 placement, node health, or probe state into JuiceFS metadata.
- Do not change object keys, slice metadata, inode metadata, or chunk layout.
- Do not require real RDMA hardware for normal unit tests or RustFS smoke tests.
- Do not introduce a consensus service, gossip protocol, or shared cache
  directory for this phase.

## Metadata Boundary

Single-node L2 failure must not change JuiceFS metadata. Each client derives L2
placement from local config and the immutable block key:

```text
candidate nodes = rendezvous_hash(block_key, configured_nodes)
available nodes = candidate nodes - locally unhealthy nodes
```

Health state is process-local and in-memory. If a node fails on one client, only
that client skips it until cooldown/probe recovery. Other clients may still use
the node if their own requests succeed. This avoids global metadata churn and
keeps L2 failure independent from file system correctness.

For immutable block objects, stale L2 entries on a failed node are acceptable:
current JuiceFS metadata controls which object keys are live. Delete requests to
down L2 nodes remain best-effort.

## Configuration

Add mount flags and `pkg/chunk.Config` fields:

```text
--remote-cache-fail-threshold       consecutive failures before a node is down
--remote-cache-node-cooldown        how long a down node is skipped
--remote-cache-probe-interval       active health probe loop interval
--remote-cache-probe-timeout        timeout for one active probe
```

Defaults should keep local development fast and avoid long tail stalls:

```text
fail threshold: 3
node cooldown: 5s
probe interval: 1s
probe timeout: min(remote-cache-timeout, 10ms) when unset
```

`probe-interval=0` disables background active probing but lazy recovery through
normal requests remains allowed after cooldown.

## Health State

Use the existing shared cluster helper as the owner of local node state. External
behavior must be:

- A node starts healthy.
- Consecutive transport failures increment its failure counter.
- Once the configured threshold is reached, the node is marked down.
- Down nodes are skipped while cooldown is active.
- A successful request or successful active probe resets the node to healthy.
- Failed probes keep the node down and refresh the last failure/probe time.

The internal implementation may keep a compact state model as long as metrics
can expose healthy/down/probing counts and tests cover transitions.

## Metrics

The remote cache layer should expose health events without forcing transport
packages to depend directly on JuiceFS chunk internals. Add a small observer
interface in `pkg/cache/remote/cluster` and a Prometheus-backed implementation in
`pkg/chunk`.

Minimum metrics:

```text
juicefs_remote_cache_node_down{transport,node}
juicefs_remote_cache_node_failures_total{transport,node}
juicefs_remote_cache_node_recoveries_total{transport,node}
juicefs_remote_cache_node_skips_total{transport,node,op}
juicefs_remote_cache_node_probe_total{transport,node,result}
```

Metric labels intentionally use configured node address strings. Operators should
not put secrets in node URLs.

## Active Probe

Add a background probe loop owned by each remote cache client when
`probe-interval > 0`.

Probe rules:

- Probe only nodes currently down or cooling down.
- Use a per-probe timeout independent from normal request timeout.
- A success marks the node healthy.
- A failure keeps the node down.
- The loop exits on client `Close()`.

Transport probe implementations:

- HTTP: `GET /healthz`; fallback to `GET /` is not required once the server has
  `/healthz`.
- RDMA protocol: add a `PING` operation returning `OK`.

## Native/Open-RDMA Transport

The default build remains hardware-independent:

- Without `-tags rdma`, `rdma.Capability()` reports not built and the native
  transport is unavailable.
- Unit tests use the existing in-memory `Dialer`.

With `-tags rdma`, add a native/open-rdma boundary:

- `Capability()` reports built.
- Availability is determined by the open-rdma runtime readiness checks already
  covered by `hack/open-rdma-smoke-test.sh`.
- The native dialer is isolated behind the existing `rdma.Dialer` interface so
  protocol execution, placement, health, and metrics tests do not depend on
  hardware.

For this phase, the implementation must provide a concrete build-tagged native
entry point and readiness proof. Full hardware performance tuning and zero-copy
buffer registration are follow-up work after the open-rdma driver and provider
interfaces are stable on the target hosts.

## Multi-Node Smoke

Extend the RustFS smoke script to run:

1. RustFS S3 baseline.
2. Two L2 HTTP cache servers with distinct disk directories.
3. Mount JuiceFS with `--remote-cache-nodes=node1,node2` and replicas `2`.
4. Populate L2.
5. Stop RustFS and one L2 node.
6. Remount with empty L1 and verify the read succeeds from the surviving L2.
7. Restart the failed L2 and verify recovery path/fill behavior.

HTTP multi-node smoke is acceptable for CI because the placement, health,
fallback, and recovery logic is shared with RDMA clients.

## Failure Recovery Tests

Add targeted unit tests:

- Config flags flow from CLI into `chunk.Config`.
- Health observer receives down, skip, probe, and recovery events.
- HTTP active probe recovers a previously down node.
- RDMA `PING` probe recovers a previously down node through the protocol
  executor.
- Multi-node RDMA client skips one failed node and reads from the next replica.
- `pkg/chunk` falls back to L1+L3 when L2 is fully down.

## Runbook

Create a single operator runbook that covers:

- Go environment assumptions used in this workspace.
- RustFS startup through Docker.
- JuiceFS format/mount commands for local disk + remote cache + S3/RustFS.
- Multi-node L2 smoke command.
- RDMA readiness command with `hack/open-rdma-smoke-test.sh`.
- Expected fallback behavior for one-node L2 failure and all-L2 failure.
- Metrics names and what operators should alert on.
