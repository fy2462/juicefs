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
  for cmd in cargo cmake pkg-config make cc uname lsmod awk grep cat sudo ip; do
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
  sudo)
    echo "sudo unavailable in test"
    exit 1
    ;;
  lsmod)
    echo "bluerdma 1 0"
    exit 0
    ;;
  ip)
    if [ "${1:-}" = "route" ] && [ "${2:-}" = "get" ] && [ "${3:-}" = "17.34.51.10" ]; then
      echo "local 17.34.51.10 dev lo src 17.34.51.10"
      exit 0
    fi
    if [ "${1:-}" = "link" ] && [ "${2:-}" = "show" ]; then
      if [ "${3:-}" = "blue0" ] || [ "${3:-}" = "blue1" ]; then
        echo "7: ${3}: <BROADCAST,MULTICAST> mtu 1500"
        exit 0
      fi
    fi
    exit 1
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

make_fake_uname_kernel() {
  bin="$1"
  kernel="$2"
  cat > "$bin/uname" <<EOF
#!/bin/sh
if [ "\${1:-}" = "-s" ]; then echo Linux; exit 0; fi
if [ "\${1:-}" = "-r" ]; then echo "$kernel"; exit 0; fi
exec /usr/bin/uname "\$@"
EOF
  chmod +x "$bin/uname"
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

out="$TMP_DIR/evidence.out"
if run_script "$out" --driver-dir /path/to/open-rdma-driver --evidence; then
  assert_contains "$out" "Evidence commands to paste back"
  assert_contains "$out" 'hack/open-rdma-smoke-test.sh --driver-dir "$OPEN_RDMA_DRIVER" --strict'
  assert_contains "$out" 'hack/open-rdma-smoke-test.sh --driver-dir "$OPEN_RDMA_DRIVER" --build'
  assert_contains "$out" 'hack/open-rdma-smoke-test.sh --driver-dir "$OPEN_RDMA_DRIVER" --run'
  pass "evidence mode prints paste-back commands"
else
  cat "$out" >&2
  fail "evidence command failed"
fi

driver="$TMP_DIR/open-rdma-driver"
make_driver_dir "$driver"

out="$TMP_DIR/check.out"
if run_script "$out" --driver-dir "$driver"; then
  assert_contains "$out" "open-rdma smoke test"
  assert_contains "$out" "missing kernel build directory"
  assert_contains "$out" "sudo is not available for privileged setup"
  assert_contains "$out" "blue0 interface is present"
  assert_contains "$out" "blue1 interface is present"
  assert_contains "$out" "smoke check summary: NOT READY"
  assert_contains "$out" "manual privileged setup"
  assert_not_contains "$out" "cargo build"
  assert_not_contains "$out" "loopback 8192"
  pass "default mode checks without build or run"
else
  cat "$out" >&2
  fail "default check failed"
fi

out="$TMP_DIR/strict.out"
if run_script "$out" --driver-dir "$driver" --strict; then
  cat "$out" >&2
  fail "strict mode unexpectedly passed with warnings"
else
  assert_contains "$out" "smoke check summary: NOT READY"
  assert_contains "$out" "strict mode failed because smoke check is not ready"
  pass "strict mode fails when warnings are present"
fi

out="$TMP_DIR/ready-without-sudo.out"
make_fake_uname_kernel "$TMP_DIR/bin" "$(/usr/bin/uname -r)"
if run_script "$out" --driver-dir "$driver" --strict; then
  assert_contains "$out" "smoke check summary: READY"
  assert_contains "$out" "open-rdma mock target route is local: 17.34.51.10"
  assert_not_contains "$out" "sudo is not available for privileged setup"
  pass "strict mode passes when runtime prerequisites are ready without sudo"
else
  cat "$out" >&2
  fail "strict mode should pass when runtime prerequisites are ready without sudo"
fi

cat > "$TMP_DIR/bin/ip" <<'EOF'
#!/bin/sh
if [ "${1:-}" = "link" ] && [ "${2:-}" = "show" ]; then
  if [ "${3:-}" = "blue0" ] || [ "${3:-}" = "blue1" ]; then
    echo "7: ${3}: <BROADCAST,MULTICAST> mtu 1500"
    exit 0
  fi
fi
if [ "${1:-}" = "route" ] && [ "${2:-}" = "get" ] && [ "${3:-}" = "17.34.51.10" ]; then
  echo "17.34.51.10 via 10.211.55.1 dev enp0s5 src 10.211.55.4"
  exit 0
fi
exit 1
EOF
chmod +x "$TMP_DIR/bin/ip"

out="$TMP_DIR/non-local-route.out"
if run_script "$out" --driver-dir "$driver" --strict; then
  cat "$out" >&2
  fail "strict mode unexpectedly passed with non-local mock target route"
else
  assert_contains "$out" "open-rdma mock target 17.34.51.10 is not routed locally"
  assert_contains "$out" "strict mode failed because smoke check is not ready"
  pass "strict mode fails when mock target route is not local"
fi

make_fake_path "$TMP_DIR/bin"
make_fake_uname_kernel "$TMP_DIR/bin" "$(/usr/bin/uname -r)"

long_driver="$TMP_DIR/very-long-open-rdma-path-segment/another-long-open-rdma-path-segment/open-rdma-driver"
make_driver_dir "$long_driver"

out="$TMP_DIR/long-path.out"
if run_script "$out" --driver-dir "$long_driver"; then
  assert_contains "$out" "open-rdma checkout path is long"
  pass "long checkout path warns before rdma-core build"
else
  cat "$out" >&2
  fail "long path check failed"
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

printf '#!/bin/sh\necho "round: 1,No differences found between the two memory regions."\necho "received bytes count: 8192"\nexit 124\n' > "$driver/examples/loopback"
chmod +x "$driver/examples/loopback"

out="$TMP_DIR/run-timeout-success.out"
if run_script "$out" --driver-dir "$driver" --run; then
  assert_contains "$out" "loopback produced a successful RDMA write before timeout"
  assert_contains "$out" "received bytes count: 8192"
  pass "run mode treats timed-out successful loopback as pass"
else
  cat "$out" >&2
  fail "run mode should pass when loopback reports success before timeout"
fi

echo "passed $TESTS_RUN tests"
