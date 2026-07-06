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

## Tier Meaning

- L1 is the JuiceFS local cache directory created under the smoke temporary
  directory.
- L2 is `juicefs rdma-cache-server --transport=http` with a disk backend.
- L3 is rustfs serving the S3 bucket used by JuiceFS object storage.

Native RDMA transport is not required for this smoke. HTTP transport is used to
validate cache ordering and fallback behavior before verbs integration.
