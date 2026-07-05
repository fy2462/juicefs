# Open RDMA Smoke Test Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a safe `hack/open-rdma-smoke-test.sh` helper that validates whether an Ubuntu host can use `open-rdma/open-rdma-driver` mock mode as a future JuiceFS RDMA test provider.

**Architecture:** Keep this phase outside the JuiceFS RDMA transport path. The helper is a standalone POSIX shell script that performs read-only checks by default, prints manual privileged setup commands, and only runs user-space build or upstream example commands when `--build` or `--run` are explicitly provided. A small shell test harness creates fake open-rdma checkouts and stub commands so behavior can be verified without RDMA hardware.

**Tech Stack:** POSIX shell, standard Unix tools, existing `hack/` directory, no new Go dependencies.

---

## File Structure

- Create: `hack/open-rdma-smoke-test.sh`
  - Parses arguments.
  - Checks host prerequisites and open-rdma checkout layout.
  - Prints non-privileged status and privileged remediation commands.
  - Optionally runs open-rdma mock build and loopback example.
- Create: `hack/open-rdma-smoke-test-test.sh`
  - Runs focused shell tests against fake driver directories and fake command stubs.
  - Verifies safety defaults, argument parsing, missing checkout reporting, `--build`, and `--run`.
- Modify: none of `pkg/cache/remote/rdma`.

## Task 1: Add Shell Test Harness

**Files:**
- Create: `hack/open-rdma-smoke-test-test.sh`

- [ ] **Step 1: Write the failing shell tests**

Create `hack/open-rdma-smoke-test-test.sh` with:

```sh
#!/bin/sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
SCRIPT="$ROOT_DIR/hack/open-rdma-smoke-test.sh"
TMP_DIR="${TMPDIR:-/tmp}/open-rdma-smoke-test.$$"
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

assert_contains() {
  file="$1"
  pattern="$2"
  if ! grep -F "$pattern" "$file" >/dev/null 2>&1; then
    echo "----- output -----" >&2
    cat "$file" >&2
    echo "------------------" >&2
    fail "expected output to contain: $pattern"
  fi
}

assert_not_contains() {
  file="$1"
  pattern="$2"
  if grep -F "$pattern" "$file" >/dev/null 2>&1; then
    echo "----- output -----" >&2
    cat "$file" >&2
    echo "------------------" >&2
    fail "expected output not to contain: $pattern"
  fi
}

make_driver_dir() {
  dir="$1"
  mkdir -p "$dir/dtld-ibverbs/rdma-core-55.0" "$dir/examples" "$dir/scripts"
  printf '#!/bin/sh\nexit 0\n' > "$dir/dtld-ibverbs/rdma-core-55.0/build.sh"
  chmod +x "$dir/dtld-ibverbs/rdma-core-55.0/build.sh"
  printf 'all:\n\t@echo examples built\n' > "$dir/examples/Makefile"
  printf '#!/bin/sh\necho loopback "$@"\n' > "$dir/examples/loopback"
  chmod +x "$dir/examples/loopback"
  printf '#!/bin/sh\n' > "$dir/scripts/setup-env.sh"
  chmod +x "$dir/scripts/setup-env.sh"
}

make_fake_path() {
  bin="$1"
  mkdir -p "$bin"
  for cmd in cargo cmake pkg-config make cc uname lsmod awk grep cat; do
    cat > "$bin/$cmd" <<'EOF'
#!/bin/sh
case "$(basename "$0")" in
  uname)
    if [ "${1:-}" = "-s" ]; then echo Linux; exit 0; fi
    if [ "${1:-}" = "-r" ]; then echo 6.8.0-test; exit 0; fi
    ;;
  pkg-config)
    exit 0
    ;;
  lsmod)
    echo "bluerdma 1 0"
    exit 0
    ;;
  cat)
    if [ "${1:-}" = "/proc/sys/vm/nr_hugepages" ]; then echo 512; exit 0; fi
    exec /bin/cat "$@"
    ;;
  cargo)
    echo "cargo $*"
    exit 0
    ;;
  make)
    echo "make $*"
    exit 0
    ;;
esac
exec "/usr/bin/$(basename "$0")" "$@"
EOF
    chmod +x "$bin/$cmd"
  done
}

run_script() {
  out="$1"
  shift
  PATH="$TMP_DIR/bin:$PATH" "$SCRIPT" "$@" >"$out" 2>&1
}

rm -rf "$TMP_DIR"
mkdir -p "$TMP_DIR"
make_fake_path "$TMP_DIR/bin"

out="$TMP_DIR/help.out"
if run_script "$out" --help; then
  assert_contains "$out" "Usage:"
  assert_contains "$out" "--driver-dir"
  pass "help output describes usage"
else
  cat "$out" >&2
  fail "help command failed"
fi

out="$TMP_DIR/missing.out"
if run_script "$out" --driver-dir "$TMP_DIR/missing"; then
  cat "$out" >&2
  fail "missing driver dir unexpectedly passed"
else
  assert_contains "$out" "missing open-rdma checkout"
  pass "missing driver directory fails clearly"
fi

driver="$TMP_DIR/open-rdma-driver"
make_driver_dir "$driver"

out="$TMP_DIR/check.out"
if run_script "$out" --driver-dir "$driver"; then
  assert_contains "$out" "open-rdma smoke test"
  assert_contains "$out" "manual privileged setup"
  assert_not_contains "$out" "cargo build"
  assert_not_contains "$out" "loopback 8192"
  pass "default mode checks without build or run"
else
  cat "$out" >&2
  fail "default check failed"
fi

out="$TMP_DIR/build.out"
if run_script "$out" --driver-dir "$driver" --build; then
  assert_contains "$out" "building open-rdma mock provider"
  assert_contains "$out" "cargo build --no-default-features --features mock"
  assert_contains "$out" "building open-rdma examples"
  pass "build mode runs user-space build steps"
else
  cat "$out" >&2
  fail "build mode failed"
fi

out="$TMP_DIR/run.out"
if run_script "$out" --driver-dir "$driver" --run; then
  assert_contains "$out" "running open-rdma loopback example"
  assert_contains "$out" "loopback 8192"
  pass "run mode executes loopback example"
else
  cat "$out" >&2
  fail "run mode failed"
fi

echo "passed $TESTS_RUN tests"
```

- [ ] **Step 2: Make the test executable**

Run:

```bash
chmod +x hack/open-rdma-smoke-test-test.sh
```

- [ ] **Step 3: Run test to verify it fails**

Run:

```bash
hack/open-rdma-smoke-test-test.sh
```

Expected: FAIL because `hack/open-rdma-smoke-test.sh` does not exist.

## Task 2: Implement Read-Only Smoke Checks

**Files:**
- Create: `hack/open-rdma-smoke-test.sh`
- Modify: `hack/open-rdma-smoke-test-test.sh`

- [ ] **Step 1: Create the initial script**

Create `hack/open-rdma-smoke-test.sh` with:

```sh
#!/bin/sh
set -eu

DRIVER_DIR=""
DO_BUILD=0
DO_RUN=0

usage() {
  cat <<'USAGE'
Usage: hack/open-rdma-smoke-test.sh --driver-dir /path/to/open-rdma-driver [--build] [--run]

Checks whether an Ubuntu/Linux host is prepared to use open-rdma mock mode
as a future JuiceFS RDMA test provider.

Options:
  --driver-dir DIR   Path to an existing open-rdma-driver checkout
  --build            Build mock provider, rdma-core tree, and examples
  --run              Run examples/loopback 8192
  --help             Show this help
USAGE
}

info() {
  printf '[INFO] %s\n' "$*"
}

warn() {
  printf '[WARN] %s\n' "$*"
}

error() {
  printf '[ERROR] %s\n' "$*" >&2
}

die() {
  error "$*"
  exit 1
}

have_cmd() {
  command -v "$1" >/dev/null 2>&1
}

parse_args() {
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --driver-dir)
        [ "$#" -ge 2 ] || die "--driver-dir requires a value"
        DRIVER_DIR="$2"
        shift 2
        ;;
      --build)
        DO_BUILD=1
        shift
        ;;
      --run)
        DO_RUN=1
        shift
        ;;
      --help|-h)
        usage
        exit 0
        ;;
      *)
        die "unknown argument: $1"
        ;;
    esac
  done
}

kernel_major() {
  printf '%s' "$1" | awk -F. '{print $1}'
}

kernel_minor() {
  printf '%s' "$1" | awk -F. '{print $2}'
}

check_linux() {
  os="$(uname -s)"
  [ "$os" = "Linux" ] || die "open-rdma smoke test requires Linux; got $os"
  kernel="$(uname -r)"
  info "kernel: $kernel"
  major="$(kernel_major "$kernel")"
  minor="$(kernel_minor "$kernel")"
  if [ "${major:-0}" -lt 6 ] || { [ "${major:-0}" -eq 6 ] && [ "${minor:-0}" -lt 6 ]; }; then
    warn "open-rdma documentation expects kernel >= 6.6"
  fi
}

check_driver_dir() {
  [ -n "$DRIVER_DIR" ] || die "--driver-dir is required"
  [ -d "$DRIVER_DIR" ] || die "missing open-rdma checkout: $DRIVER_DIR"
  [ -d "$DRIVER_DIR/dtld-ibverbs" ] || die "missing $DRIVER_DIR/dtld-ibverbs"
  [ -d "$DRIVER_DIR/examples" ] || die "missing $DRIVER_DIR/examples"
  [ -f "$DRIVER_DIR/scripts/setup-env.sh" ] || die "missing $DRIVER_DIR/scripts/setup-env.sh"
  info "open-rdma checkout: $DRIVER_DIR"
}

check_commands() {
  missing=""
  for cmd in cargo cmake pkg-config make cc; do
    if have_cmd "$cmd"; then
      info "found command: $cmd"
    else
      warn "missing command: $cmd"
      missing="$missing $cmd"
    fi
  done
  if [ -n "$missing" ]; then
    warn "install missing commands before using --build"
  fi
}

check_pkg_config() {
  for pkg in libibverbs libnl-3.0 libnl-route-3.0; do
    if have_cmd pkg-config && pkg-config --exists "$pkg"; then
      info "found pkg-config entry: $pkg"
    else
      warn "missing pkg-config entry: $pkg"
    fi
  done
}

check_module() {
  if have_cmd lsmod && lsmod | grep '^bluerdma[[:space:]]' >/dev/null 2>&1; then
    info "kernel module loaded: bluerdma"
  else
    warn "kernel module is not loaded: bluerdma"
  fi
}

check_hugepages() {
  hugepages="unknown"
  if [ -r /proc/sys/vm/nr_hugepages ]; then
    hugepages="$(cat /proc/sys/vm/nr_hugepages)"
  fi
  info "nr_hugepages: $hugepages"
  case "$hugepages" in
    ''|0|unknown)
      warn "huge pages may need allocation before running open-rdma examples"
      ;;
  esac
}

print_manual_setup() {
  cat <<EOF

manual privileged setup, run from the open-rdma checkout when needed:

  sudo apt install cmake pkg-config libnl-3-dev libnl-route-3-dev libclang-dev libibverbs-dev
  make
  sudo make install
  sudo ./scripts/hugepages.sh alloc 512

EOF
}

main() {
  parse_args "$@"
  info "open-rdma smoke test"
  check_linux
  check_driver_dir
  check_commands
  check_pkg_config
  check_module
  check_hugepages
  print_manual_setup
  if [ "$DO_BUILD" -eq 1 ]; then
    build_open_rdma
  fi
  if [ "$DO_RUN" -eq 1 ]; then
    run_loopback
  fi
}

build_open_rdma() {
  die "--build is not implemented yet"
}

run_loopback() {
  die "--run is not implemented yet"
}

main "$@"
```

- [ ] **Step 2: Make the script executable**

Run:

```bash
chmod +x hack/open-rdma-smoke-test.sh
```

- [ ] **Step 3: Run the harness**

Run:

```bash
hack/open-rdma-smoke-test-test.sh
```

Expected: the help, missing-driver, and default-check tests pass; build and run tests fail with `--build is not implemented yet` or `--run is not implemented yet`.

## Task 3: Implement `--build` and `--run`

**Files:**
- Modify: `hack/open-rdma-smoke-test.sh`

- [ ] **Step 1: Replace the build and run stubs**

In `hack/open-rdma-smoke-test.sh`, replace `build_open_rdma` and `run_loopback` with:

```sh
run_cmd() {
  info "running: $*"
  "$@"
}

build_open_rdma() {
  info "building open-rdma mock provider"
  (
    cd "$DRIVER_DIR/dtld-ibverbs"
    run_cmd cargo build --no-default-features --features mock
  )

  info "building open-rdma rdma-core tree"
  (
    cd "$DRIVER_DIR/dtld-ibverbs/rdma-core-55.0"
    run_cmd ./build.sh
  )

  info "building open-rdma examples"
  (
    cd "$DRIVER_DIR/examples"
    run_cmd make
  )
}

run_loopback() {
  loopback="$DRIVER_DIR/examples/loopback"
  [ -x "$loopback" ] || die "missing executable example: $loopback"
  info "running open-rdma loopback example"
  LD_LIBRARY_PATH="$DRIVER_DIR/dtld-ibverbs/target/debug:$DRIVER_DIR/dtld-ibverbs/rdma-core-55.0/build/lib:${LD_LIBRARY_PATH:-}" \
    "$loopback" 8192
}
```

- [ ] **Step 2: Run the harness**

Run:

```bash
hack/open-rdma-smoke-test-test.sh
```

Expected: all tests pass and output ends with `passed 5 tests`.

- [ ] **Step 3: Run shell syntax checks**

Run:

```bash
sh -n hack/open-rdma-smoke-test.sh
sh -n hack/open-rdma-smoke-test-test.sh
```

Expected: both commands exit 0.

## Task 4: Manual Dry Run and Commit

**Files:**
- Add: `hack/open-rdma-smoke-test.sh`
- Add: `hack/open-rdma-smoke-test-test.sh`
- Add: `docs/superpowers/plans/2026-07-05-open-rdma-smoke-test.md`

- [ ] **Step 1: Run a missing-driver dry run**

Run:

```bash
hack/open-rdma-smoke-test.sh --driver-dir /tmp/does-not-exist
```

Expected: exits non-zero and prints `missing open-rdma checkout: /tmp/does-not-exist`.

- [ ] **Step 2: Run formatting check**

Run:

```bash
git diff --check
```

Expected: exits 0.

- [ ] **Step 3: Inspect final status**

Run:

```bash
git status --short
```

Expected: only these files are changed:

```text
?? docs/superpowers/plans/2026-07-05-open-rdma-smoke-test.md
?? hack/open-rdma-smoke-test-test.sh
?? hack/open-rdma-smoke-test.sh
```

- [ ] **Step 4: Commit**

Run:

```bash
git add docs/superpowers/plans/2026-07-05-open-rdma-smoke-test.md hack/open-rdma-smoke-test-test.sh hack/open-rdma-smoke-test.sh
git commit -m "test: add open rdma smoke test helper"
```

Expected: commit succeeds.

## Final Verification

Run:

```bash
hack/open-rdma-smoke-test-test.sh
sh -n hack/open-rdma-smoke-test.sh
sh -n hack/open-rdma-smoke-test-test.sh
git diff --check
git status --short --branch
```

Expected:

- shell harness passes 5 tests
- syntax checks exit 0
- diff check exits 0
- branch is `feature/rdma-distributed-cache`
- worktree is clean after commit

## Self-Review

- Spec coverage: Covers read-only checks, `--build`, `--run`, manual privileged commands, safety constraints, and no JuiceFS native RDMA transport work.
- Placeholder scan: No unresolved markers or unspecified implementation steps remain.
- Type and command consistency: Script names, option names, paths, and commands match the approved spec.
