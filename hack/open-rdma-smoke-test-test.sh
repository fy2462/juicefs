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
  if ! grep -F -- "$pattern" "$file" >/dev/null 2>&1; then
    echo "----- output -----" >&2
    cat "$file" >&2
    echo "------------------" >&2
    fail "expected output to contain: $pattern"
  fi
}

assert_not_contains() {
  file="$1"
  pattern="$2"
  if grep -F -- "$pattern" "$file" >/dev/null 2>&1; then
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
