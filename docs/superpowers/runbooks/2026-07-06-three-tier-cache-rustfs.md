# Three-Tier Cache RustFS Runbook

This runbook validates the local three-tier cache hierarchy:

```text
L1 local disk cache -> L2 remote cache server -> L3 rustfs S3
```

## Prerequisites

- Buildable JuiceFS checkout.
- A `rustfs` binary in `PATH`, `RUSTFS_BIN=/path/to/rustfs`, or Docker access.
- Linux or macOS host that can run JuiceFS mount tests.

## Command

```sh
make test.three-tier-cache-rustfs
```

## Expected Result

The smoke prints numbered `ok` lines and exits zero. If neither rustfs nor Docker
is installed, the smoke prints a clear prerequisite message and exits zero
without running the integration path.

The final three-tier check proves L2 serving rather than only proving that data
can be read:

1. Client A writes a 1 MiB file with its own L1 cache.
2. A filler client with a separate empty L1 reads the file from rustfs and fills
   L2 remote cache.
3. The smoke waits for a disk-backed L2 cache entry to appear.
4. rustfs is stopped and the S3 endpoint is verified unavailable.
5. Client B with another empty L1 reads the file and validates size and content.

Because Client B has a fresh L1 and L3 is offline, a successful final read proves
the data came from L2 remote cache.

## Tier Meaning

- L1 is the JuiceFS local cache directory created under the smoke temporary
  directory.
- L2 is `juicefs rdma-cache-server --transport=http` with a disk backend.
- L3 is rustfs serving the S3 bucket used by JuiceFS object storage.

Native RDMA transport is not required for this smoke. HTTP transport is used to
validate cache ordering and fallback behavior before verbs integration.
