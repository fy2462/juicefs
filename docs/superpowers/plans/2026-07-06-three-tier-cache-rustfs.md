# Three-Tier Cache RustFS Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a repeatable rustfs-backed validation path for JuiceFS' L1 local disk cache, L2 remote cache server, and L3 S3 object storage hierarchy.

**Architecture:** Start with shell smoke tests because they can exercise real JuiceFS CLI flows, rustfs, cache directories, and remote cache server processes without changing production code. Keep the first implementation opt-in: missing `rustfs` produces a clear prerequisite result, while environments with rustfs run end-to-end format/write/read/remount checks. Use existing `rdma-cache-server --transport=http` as the L2 semantic stand-in until native RDMA is implemented.

**Tech Stack:** POSIX shell, JuiceFS CLI, rustfs S3-compatible server, SQLite metadata, existing `rdma-cache-server`, existing remote cache HTTP transport.

---

## File Structure

- Create `hack/three-tier-cache-rustfs-test.sh`
  - Owns M1-M4 smoke orchestration.
  - Starts/stops rustfs when `RUSTFS_BIN` or `rustfs` is available.
  - Builds `./juicefs` when missing.
  - Runs JuiceFS format/mount/read/write/remount flows against rustfs.
  - Starts/stops `juicefs rdma-cache-server` for L2 tests.
  - Uses temporary local cache, remote cache, metadata, mount, and object data directories.
- Modify `Makefile`
  - Add `test.three-tier-cache-rustfs` target that runs the smoke script.
- Create `docs/superpowers/runbooks/2026-07-06-three-tier-cache-rustfs.md`
  - Documents prerequisites, commands, expected tier paths, and troubleshooting.

No production Go code should change in the first pass unless the smoke exposes a missing test hook that cannot be solved cleanly in shell.

## Task 1: RustFS Smoke Harness Skeleton

**Files:**
- Create: `hack/three-tier-cache-rustfs-test.sh`

- [ ] **Step 1: Write the failing harness executable test**

Create `hack/three-tier-cache-rustfs-test.sh` with this initial content:

```sh
#!/bin/sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
TMP_DIR="${TMPDIR:-/tmp}/jfs-three-tier-cache-rustfs.$$"
TESTS_RUN=0

cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT INT TERM

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

pass() {
  TESTS_RUN=$((TESTS_RUN + 1))
  echo "ok $TESTS_RUN - $*"
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1
}

rustfs_bin() {
  if [ -n "${RUSTFS_BIN:-}" ]; then
    printf '%s\n' "$RUSTFS_BIN"
    return
  fi
  command -v rustfs 2>/dev/null || true
}

require_rustfs() {
  bin="$(rustfs_bin)"
  if [ -z "$bin" ] || [ ! -x "$bin" ]; then
    cat <<'EOF'
SKIP: rustfs binary is required for this smoke.
Set RUSTFS_BIN=/path/to/rustfs or put rustfs in PATH.
EOF
    exit 0
  fi
  RUSTFS_BIN="$bin"
  export RUSTFS_BIN
}

main() {
  rm -rf "$TMP_DIR"
  mkdir -p "$TMP_DIR"
  require_rustfs
  pass "rustfs prerequisite is available"
  echo "passed $TESTS_RUN tests"
}

main "$@"
```

- [ ] **Step 2: Run the harness before chmod to verify it fails clearly**

Run:

```bash
./hack/three-tier-cache-rustfs-test.sh
```

Expected: FAIL from the shell with `Permission denied` because the script is not executable yet.

- [ ] **Step 3: Mark the harness executable**

Run:

```bash
chmod +x hack/three-tier-cache-rustfs-test.sh
```

- [ ] **Step 4: Run the harness to verify prerequisite handling**

Run:

```bash
./hack/three-tier-cache-rustfs-test.sh
```

Expected when rustfs is unavailable:

```text
SKIP: rustfs binary is required for this smoke.
Set RUSTFS_BIN=/path/to/rustfs or put rustfs in PATH.
```

Expected when rustfs is available:

```text
ok 1 - rustfs prerequisite is available
passed 1 tests
```

- [ ] **Step 5: Commit**

```bash
git add hack/three-tier-cache-rustfs-test.sh
git commit -m "test: add rustfs three tier smoke harness"
```

## Task 2: Build and Process Helpers

**Files:**
- Modify: `hack/three-tier-cache-rustfs-test.sh`

- [ ] **Step 1: Add failing expectations for helper behavior**

Append these functions before `main()`:

```sh
assert_file() {
  path="$1"
  [ -f "$path" ] || fail "missing file: $path"
}

assert_dir() {
  path="$1"
  [ -d "$path" ] || fail "missing directory: $path"
}
```

Replace `main()` with:

```sh
main() {
  rm -rf "$TMP_DIR"
  mkdir -p "$TMP_DIR"
  require_rustfs
  ensure_juicefs
  assert_file "$ROOT_DIR/juicefs"
  pass "juicefs binary is available"
  echo "passed $TESTS_RUN tests"
}
```

- [ ] **Step 2: Run to verify RED**

Run:

```bash
RUSTFS_BIN=/bin/true ./hack/three-tier-cache-rustfs-test.sh
```

Expected: FAIL with `ensure_juicefs: not found`.

- [ ] **Step 3: Implement minimal helper**

Add before `main()`:

```sh
ensure_juicefs() {
  if [ -x "$ROOT_DIR/juicefs" ]; then
    return
  fi
  (cd "$ROOT_DIR" && make juicefs)
}
```

- [ ] **Step 4: Run to verify GREEN**

Run:

```bash
RUSTFS_BIN=/bin/true ./hack/three-tier-cache-rustfs-test.sh
```

Expected:

```text
ok 1 - juicefs binary is available
passed 1 tests
```

- [ ] **Step 5: Commit**

```bash
git add hack/three-tier-cache-rustfs-test.sh
git commit -m "test: prepare juicefs binary for rustfs smoke"
```

## Task 3: RustFS S3 Baseline

**Files:**
- Modify: `hack/three-tier-cache-rustfs-test.sh`

- [ ] **Step 1: Write failing baseline flow**

Add this function before `main()`:

```sh
run_s3_baseline() {
  meta="$TMP_DIR/meta.db"
  mountpoint="$TMP_DIR/mnt"
  cache_dir="$TMP_DIR/l1-cache"
  bucket_url="$RUSTFS_BUCKET_URL"
  mkdir -p "$mountpoint" "$cache_dir"

  "$ROOT_DIR/juicefs" format \
    --storage s3 \
    --bucket "$bucket_url" \
    --access-key "$RUSTFS_ACCESS_KEY" \
    --secret-key "$RUSTFS_SECRET_KEY" \
    --trash-days 0 \
    "sqlite3://$meta" rustfs-jfs

  "$ROOT_DIR/juicefs" mount \
    --cache-dir "$cache_dir" \
    --cache-size 64 \
    "sqlite3://$meta" "$mountpoint" &
  mount_pid=$!
  wait_for_path "$mountpoint"

  printf 'three-tier-rustfs\n' > "$mountpoint/payload.txt"
  sync
  "$ROOT_DIR/juicefs" umount "$mountpoint"
  wait "$mount_pid" 2>/dev/null || true

  "$ROOT_DIR/juicefs" mount \
    --cache-dir "$cache_dir" \
    --cache-size 64 \
    "sqlite3://$meta" "$mountpoint" &
  mount_pid=$!
  wait_for_path "$mountpoint/payload.txt"
  grep -F 'three-tier-rustfs' "$mountpoint/payload.txt" >/dev/null
  "$ROOT_DIR/juicefs" umount "$mountpoint"
  wait "$mount_pid" 2>/dev/null || true
}
```

Change `main()` so that after `ensure_juicefs` it sets the S3 variables and calls the baseline:

```sh
  RUSTFS_ACCESS_KEY="${RUSTFS_ACCESS_KEY:-rustfsadmin}"
  RUSTFS_SECRET_KEY="${RUSTFS_SECRET_KEY:-rustfsadmin}"
  RUSTFS_BUCKET_URL="${RUSTFS_BUCKET_URL:-http://127.0.0.1:9000/jfs-three-tier}"
  export RUSTFS_ACCESS_KEY RUSTFS_SECRET_KEY RUSTFS_BUCKET_URL
  run_s3_baseline
  pass "rustfs S3 baseline survives remount"
```

- [ ] **Step 2: Run to verify RED**

Run:

```bash
RUSTFS_BIN=/bin/true ./hack/three-tier-cache-rustfs-test.sh
```

Expected: FAIL with `wait_for_path: not found`.

- [ ] **Step 3: Implement wait helper**

Add before `run_s3_baseline()`:

```sh
wait_for_path() {
  path="$1"
  i=0
  while [ "$i" -lt 100 ]; do
    if [ -e "$path" ] || [ -d "$path" ]; then
      return
    fi
    i=$((i + 1))
    sleep 0.1
  done
  fail "timed out waiting for path: $path"
}
```

- [ ] **Step 4: Add rustfs startup wrapper**

Add before `run_s3_baseline()`:

```sh
start_rustfs() {
  data_dir="$TMP_DIR/rustfs-data"
  log="$TMP_DIR/rustfs.log"
  mkdir -p "$data_dir"
  export RUSTFS_ACCESS_KEY="${RUSTFS_ACCESS_KEY:-rustfsadmin}"
  export RUSTFS_SECRET_KEY="${RUSTFS_SECRET_KEY:-rustfsadmin}"
  export MINIO_ROOT_USER="$RUSTFS_ACCESS_KEY"
  export MINIO_ROOT_PASSWORD="$RUSTFS_SECRET_KEY"
  endpoint="${RUSTFS_ENDPOINT:-127.0.0.1:9000}"
  "$RUSTFS_BIN" "$data_dir" --address "$endpoint" >"$log" 2>&1 &
  RUSTFS_PID=$!
  export RUSTFS_PID
  RUSTFS_BUCKET_URL="http://$endpoint/jfs-three-tier"
  export RUSTFS_BUCKET_URL
}

stop_rustfs() {
  if [ -n "${RUSTFS_PID:-}" ]; then
    kill "$RUSTFS_PID" 2>/dev/null || true
    wait "$RUSTFS_PID" 2>/dev/null || true
  fi
}
```

Update `cleanup()`:

```sh
cleanup() {
  stop_rustfs
  rm -rf "$TMP_DIR"
}
```

Update `main()` to call `start_rustfs` before `run_s3_baseline`.

- [ ] **Step 5: Run against real rustfs**

Run:

```bash
./hack/three-tier-cache-rustfs-test.sh
```

Expected when rustfs is present and compatible with the launcher:

```text
ok 1 - juicefs binary is available
ok 2 - rustfs S3 baseline survives remount
passed 2 tests
```

If rustfs rejects `--address`, inspect `$TMP_DIR/rustfs.log`, update only `start_rustfs()` to match the installed rustfs CLI, and rerun this same command.

- [ ] **Step 6: Commit**

```bash
git add hack/three-tier-cache-rustfs-test.sh
git commit -m "test: add rustfs s3 baseline smoke"
```

## Task 4: L2 Remote Cache Server Smoke

**Files:**
- Modify: `hack/three-tier-cache-rustfs-test.sh`

- [ ] **Step 1: Write failing L2 server flow**

Add before `main()`:

```sh
start_remote_cache_server() {
  remote_dir="$TMP_DIR/l2-cache"
  remote_log="$TMP_DIR/rdma-cache-server.log"
  mkdir -p "$remote_dir"
  "$ROOT_DIR/juicefs" rdma-cache-server \
    --listen 127.0.0.1:9568 \
    --transport http \
    --cache-dir "$remote_dir" \
    --cache-size 64M >"$remote_log" 2>&1 &
  REMOTE_CACHE_PID=$!
  export REMOTE_CACHE_PID
}

stop_remote_cache_server() {
  if [ -n "${REMOTE_CACHE_PID:-}" ]; then
    kill "$REMOTE_CACHE_PID" 2>/dev/null || true
    wait "$REMOTE_CACHE_PID" 2>/dev/null || true
  fi
}
```

Update `cleanup()` to call `stop_remote_cache_server` before `stop_rustfs`.

Add a minimal HTTP probe:

```sh
wait_for_http() {
  url="$1"
  i=0
  while [ "$i" -lt 100 ]; do
    if need_cmd curl && curl -fsS "$url" >/dev/null 2>&1; then
      return
    fi
    i=$((i + 1))
    sleep 0.1
  done
  fail "timed out waiting for http endpoint: $url"
}
```

Update `main()` to call:

```sh
  start_remote_cache_server
  wait_for_http "http://127.0.0.1:9568/cache/__probe__"
  pass "remote cache server starts"
```

- [ ] **Step 2: Run to verify RED**

Run:

```bash
RUSTFS_BIN=/bin/true ./hack/three-tier-cache-rustfs-test.sh
```

Expected: FAIL because `/bin/true` is not a real rustfs server and the S3 baseline cannot pass. To isolate this task during development, temporarily run with a real rustfs binary or move the remote-cache startup before `run_s3_baseline` only for the RED check, then restore order.

- [ ] **Step 3: Implement stable readiness check without requiring curl**

Replace `wait_for_http()` with:

```sh
wait_for_tcp() {
  host="$1"
  port="$2"
  i=0
  while [ "$i" -lt 100 ]; do
    if (exec 3<>"/dev/tcp/$host/$port") >/dev/null 2>&1; then
      exec 3>&-
      return
    fi
    i=$((i + 1))
    sleep 0.1
  done
  fail "timed out waiting for tcp endpoint: $host:$port"
}
```

Update `main()`:

```sh
  start_remote_cache_server
  wait_for_tcp 127.0.0.1 9568
  pass "remote cache server starts"
```

- [ ] **Step 4: Run to verify GREEN**

Run:

```bash
./hack/three-tier-cache-rustfs-test.sh
```

Expected with rustfs available:

```text
ok 1 - juicefs binary is available
ok 2 - rustfs S3 baseline survives remount
ok 3 - remote cache server starts
passed 3 tests
```

- [ ] **Step 5: Commit**

```bash
git add hack/three-tier-cache-rustfs-test.sh
git commit -m "test: start remote cache server in rustfs smoke"
```

## Task 5: Full Three-Tier Read Path Smoke

**Files:**
- Modify: `hack/three-tier-cache-rustfs-test.sh`

- [ ] **Step 1: Write failing three-tier read flow**

Add before `main()`:

```sh
run_three_tier_read_path() {
  meta="$TMP_DIR/three-tier-meta.db"
  mountpoint="$TMP_DIR/three-tier-mnt"
  l1a="$TMP_DIR/l1-client-a"
  l1b="$TMP_DIR/l1-client-b"
  mkdir -p "$mountpoint" "$l1a" "$l1b"

  "$ROOT_DIR/juicefs" format \
    --storage s3 \
    --bucket "$RUSTFS_BUCKET_URL" \
    --access-key "$RUSTFS_ACCESS_KEY" \
    --secret-key "$RUSTFS_SECRET_KEY" \
    --trash-days 0 \
    "sqlite3://$meta" three-tier-jfs

  "$ROOT_DIR/juicefs" mount \
    --cache-dir "$l1a" \
    --cache-size 64 \
    --remote-cache rdma \
    --remote-cache-transport http \
    --remote-cache-nodes 127.0.0.1:9568 \
    --remote-cache-fill-local true \
    --remote-cache-fill-remote true \
    "sqlite3://$meta" "$mountpoint" &
  mount_pid=$!
  wait_for_path "$mountpoint"
  dd if=/dev/zero bs=1048576 count=1 2>/dev/null | tr '\000' 'A' > "$mountpoint/blob.bin"
  sync
  cat "$mountpoint/blob.bin" >/dev/null
  "$ROOT_DIR/juicefs" umount "$mountpoint"
  wait "$mount_pid" 2>/dev/null || true

  "$ROOT_DIR/juicefs" mount \
    --cache-dir "$l1b" \
    --cache-size 64 \
    --remote-cache rdma \
    --remote-cache-transport http \
    --remote-cache-nodes 127.0.0.1:9568 \
    --remote-cache-fill-local true \
    --remote-cache-fill-remote true \
    "sqlite3://$meta" "$mountpoint" &
  mount_pid=$!
  wait_for_path "$mountpoint/blob.bin"
  size="$(wc -c < "$mountpoint/blob.bin" | tr -d ' ')"
  [ "$size" = "1048576" ] || fail "unexpected blob size from three-tier read: $size"
  "$ROOT_DIR/juicefs" umount "$mountpoint"
  wait "$mount_pid" 2>/dev/null || true
}
```

Update `main()` to call:

```sh
  run_three_tier_read_path
  pass "three-tier read path returns data with fresh L1"
```

- [ ] **Step 2: Run to verify RED if remote cache fill is not exercised**

Run:

```bash
./hack/three-tier-cache-rustfs-test.sh
```

Expected before any script fixes: FAIL if mount options are malformed or remote cache server readiness is insufficient. If it passes immediately, keep the test because it still locks the end-to-end behavior.

- [ ] **Step 3: Fix script issues only**

If mount options are rejected, inspect `juicefs mount --help` and use the existing flag names from `cmd/flags.go`:

```text
--remote-cache
--remote-cache-nodes
--remote-cache-transport
--remote-cache-timeout
--remote-cache-replicas
--remote-cache-fill-local
--remote-cache-fill-remote
```

Do not change production code for this task unless the production path fails while existing unit tests suggest it should work.

- [ ] **Step 4: Run to verify GREEN**

Run:

```bash
./hack/three-tier-cache-rustfs-test.sh
```

Expected:

```text
ok 1 - juicefs binary is available
ok 2 - rustfs S3 baseline survives remount
ok 3 - remote cache server starts
ok 4 - three-tier read path returns data with fresh L1
passed 4 tests
```

- [ ] **Step 5: Commit**

```bash
git add hack/three-tier-cache-rustfs-test.sh
git commit -m "test: cover rustfs three tier read path"
```

## Task 6: Make Target and Runbook

**Files:**
- Modify: `Makefile`
- Create: `docs/superpowers/runbooks/2026-07-06-three-tier-cache-rustfs.md`

- [ ] **Step 1: Add failing make target expectation**

Run:

```bash
make test.three-tier-cache-rustfs
```

Expected: FAIL with `No rule to make target 'test.three-tier-cache-rustfs'`.

- [ ] **Step 2: Add Makefile target**

Append near the existing test targets in `Makefile`:

```make
test.three-tier-cache-rustfs:
	./hack/three-tier-cache-rustfs-test.sh
```

- [ ] **Step 3: Add runbook**

Create `docs/superpowers/runbooks/2026-07-06-three-tier-cache-rustfs.md`:

```markdown
# Three-Tier Cache RustFS Runbook

This runbook validates the local three-tier cache hierarchy:

```text
L1 local disk cache -> L2 remote cache server -> L3 rustfs S3
```

## Prerequisites

- Buildable JuiceFS checkout.
- A `rustfs` binary in `PATH`, or `RUSTFS_BIN=/path/to/rustfs`.
- Linux or macOS host that can run JuiceFS mount tests.

## Command

```sh
make test.three-tier-cache-rustfs
```

## Expected Result

The smoke prints numbered `ok` lines and exits zero. If rustfs is not installed,
the smoke prints a clear prerequisite message and exits zero without running the
integration path.

## Tier Meaning

- L1 is the JuiceFS local cache directory created under the smoke temporary
  directory.
- L2 is `juicefs rdma-cache-server --transport=http` with a disk backend.
- L3 is rustfs serving the S3 bucket used by JuiceFS object storage.

Native RDMA transport is not required for this smoke. HTTP transport is used to
validate cache ordering and fallback behavior before verbs integration.
```

- [ ] **Step 4: Run make target**

Run:

```bash
make test.three-tier-cache-rustfs
```

Expected without rustfs:

```text
SKIP: rustfs binary is required for this smoke.
Set RUSTFS_BIN=/path/to/rustfs or put rustfs in PATH.
```

Expected with rustfs:

```text
passed 4 tests
```

- [ ] **Step 5: Commit**

```bash
git add Makefile docs/superpowers/runbooks/2026-07-06-three-tier-cache-rustfs.md
git commit -m "docs: add rustfs three tier cache runbook"
```

## Task 7: Final Verification

**Files:**
- No file changes expected.

- [ ] **Step 1: Run shell syntax checks**

```bash
sh -n hack/three-tier-cache-rustfs-test.sh
```

Expected: exit 0.

- [ ] **Step 2: Run existing remote cache unit tests**

```bash
go test ./pkg/cache/remote/... ./pkg/chunk -run 'TestRemoteCache|TestDisk|TestHTTP|TestRDM'
```

Expected: exit 0.

- [ ] **Step 3: Run rustfs smoke through make**

```bash
make test.three-tier-cache-rustfs
```

Expected: exit 0. It may skip when rustfs is not installed; that skip is acceptable for environments without rustfs.

- [ ] **Step 4: Confirm worktree**

```bash
git status --short --branch
```

Expected: clean worktree on `feature/rdma-distributed-cache`.

## Self-Review

- Spec coverage: M1 is covered by Tasks 1-3; M2 is partially covered by Task 5 through cache-dir isolation; M3 is covered by Task 4; M4 is covered by Task 5; developer documentation is covered by Task 6. M5 native RDMA remains intentionally outside this plan.
- Placeholder scan: no unresolved placeholder markers are used.
- Type and command consistency: script names, make target, mount flags, and remote cache command names match the current repository naming.
