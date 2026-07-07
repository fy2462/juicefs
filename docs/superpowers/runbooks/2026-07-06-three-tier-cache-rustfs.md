# Three-Tier Cache RustFS Runbook

This runbook validates the local three-tier cache hierarchy:

```text
L1 local disk cache -> L2 remote cache server -> L3 rustfs S3
```

## Prerequisites

- Buildable JuiceFS checkout.
- A `rustfs` binary in `PATH`, `RUSTFS_BIN=/path/to/rustfs`, or Docker access.
- A `redis-server`/`redis-cli` pair in `PATH`, or Docker access. The smoke uses
  Redis metadata and starts Docker Redis from `redis:7-alpine` when a local
  Redis binary is not available.
- Linux or macOS host that can run JuiceFS mount tests.

## Command

```sh
make test.three-tier-cache-rustfs
```

## Expected Result

The smoke prints numbered `ok` lines and exits zero. If neither RustFS nor Docker
is installed, or neither Redis nor Docker is installed, the smoke prints a clear
prerequisite message and exits zero without running the integration path.

Each scenario uses a fresh Redis logical database, for example
`redis://127.0.0.1:16379/10`, so metadata state does not leak across the
baseline, L2 fill, L2 failure, or multi-node recovery checks.

The three-tier check proves L2 serving rather than only proving that data
can be read:

1. Client A writes a 1 MiB file with its own L1 cache.
2. A filler client with a separate empty L1 reads the file from rustfs and fills
   L2 remote cache.
3. The smoke waits for a disk-backed L2 cache entry to appear.
4. rustfs is stopped and the S3 endpoint is verified unavailable.
5. Client B with another empty L1 reads the file and validates size and content.

Because Client B has a fresh L1 and L3 is offline, a successful final read proves
the data came from L2 remote cache.

The smoke also validates multi-node L2 behavior:

1. Two disk-backed remote cache servers start on `127.0.0.1:9568` and
   `127.0.0.1:9569`.
2. A filler client reads through RustFS with `--remote-cache-replicas=2`, so both
   L2 nodes receive cache entries.
3. RustFS and one L2 node are stopped.
4. A fresh-L1 client reads the file successfully through the surviving L2 node.
5. The failed L2 node is restarted.
6. A new object is read through RustFS and the smoke waits for the restarted L2
   node to receive a new cache entry.

The smoke also validates the opposite fallback direction:

1. A file is written while RustFS L3 and the L2 remote cache server are running.
2. The L2 remote cache server is stopped.
3. A client with a fresh L1 reads the file successfully from RustFS.
4. RustFS is then stopped too.
5. Another fresh-L1 read is expected to fail, proving the previous success came
   from L3 fallback rather than hidden local cache state.

## Tier Meaning

- L1 is the JuiceFS local cache directory created under the smoke temporary
  directory.
- L2 is `juicefs rdma-cache-server --transport=http` with a disk backend.
- L3 is rustfs serving the S3 bucket used by JuiceFS object storage.

Native RDMA transport is not required for this smoke. HTTP transport is used to
validate cache ordering and fallback behavior before verbs integration.
