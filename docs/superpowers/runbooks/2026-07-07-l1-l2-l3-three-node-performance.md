# L1/L2/L3 3-Node Cache Performance

Generated: 2026-07-07T12:45:02Z

## Topology

- Client: `client-node`
- L1: per-client local disk cache under `/tmp/jfs-compose-three-node`
- L2: two remote cache servers, `l2-node-1:9568` and `l2-node-2:9568`
- L3: RustFS S3-compatible object storage at `http://rustfs:9000/jfs-compose-three-node`
- Metadata: Redis at `redis://redis:6379/15`
- L2 transport: HTTP remote-cache transport inside Docker Compose

This report measures the three-tier cache behavior in a 3-node compose topology.
It is not a real RDMA hardware throughput report; native RDMA performance still
requires a host with real RDMA devices or an open-rdma mock environment.

## Parameters

- Payload: `32 MiB` (`33554432` bytes)
- Remote cache replicas: `2`
- L1 cache size: `64 MiB`
- Remote cache metric used for L2 proof: `juicefs_remote_cache_gets_total{result="hit"}`

## Results

| Path | Bytes | Elapsed ms | Throughput MiB/s | remote_hits_delta |
| --- | ---: | ---: | ---: | ---: |
| L3 cold read | 33554432 | 106.903 | 299.34 | n/a |
| L2 hot read | 33554432 | 52.148 | 613.64 | 8 |
| L1 hot read | 33554432 | 10.888 | 2938.89 | 0 |

## Interpretation

- L3 cold read uses a fresh L1 cache and no remote cache, so data is served from
  RustFS through the object storage path.
- L2 hot read uses a fresh L1 cache after the remote cache has been warmed. A
  positive `remote_hits_delta` proves the read used L2.
- L1 hot read repeats the read on the same mounted client after the local cache
  is warm. A zero or near-zero `remote_hits_delta` means the second read stayed
  local.
