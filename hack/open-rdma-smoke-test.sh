#!/bin/sh
set -eu

DRIVER_DIR=""
DO_BUILD=0
DO_RUN=0
DO_STRICT=0
DO_EVIDENCE=0
WARNINGS=0

usage() {
  cat <<'USAGE'
Usage: hack/open-rdma-smoke-test.sh --driver-dir /path/to/open-rdma-driver [--build] [--run] [--strict] [--evidence]

Checks whether an Ubuntu/Linux host is prepared to use open-rdma mock mode
as a future JuiceFS RDMA test provider.

Options:
  --driver-dir DIR   Path to an existing open-rdma-driver checkout
  --build            Build mock provider, rdma-core tree, and examples
  --run              Run examples/loopback 8192
  --strict           Exit non-zero when readiness warnings are present
  --evidence         Print commands whose output should be pasted back
  --help             Show this help
USAGE
}

info() {
  printf '[INFO] %s\n' "$*"
}

warn() {
  WARNINGS=$((WARNINGS + 1))
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
      --strict)
        DO_STRICT=1
        shift
        ;;
      --evidence)
        DO_EVIDENCE=1
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

check_kernel_build_dir() {
  kernel="$(uname -r)"
  build_dir="/lib/modules/$kernel/build"
  if [ -d "$build_dir" ]; then
    info "kernel build directory: $build_dir"
  else
    warn "missing kernel build directory: $build_dir"
    warn "open-rdma kernel module build may fail until matching kernel headers are installed"
  fi
}

check_driver_dir() {
  [ -n "$DRIVER_DIR" ] || die "--driver-dir is required"
  [ -d "$DRIVER_DIR" ] || die "missing open-rdma checkout: $DRIVER_DIR"
  [ -d "$DRIVER_DIR/dtld-ibverbs" ] || die "missing $DRIVER_DIR/dtld-ibverbs"
  [ -d "$DRIVER_DIR/examples" ] || die "missing $DRIVER_DIR/examples"
  [ -f "$DRIVER_DIR/scripts/setup-env.sh" ] || die "missing $DRIVER_DIR/scripts/setup-env.sh"
  info "open-rdma checkout: $DRIVER_DIR"
  path_len=${#DRIVER_DIR}
  if [ "$path_len" -gt 80 ]; then
    warn "open-rdma checkout path is long ($path_len chars); rdma-core builds may fail in deep paths"
    warn "prefer a short path such as /home/${USER:-user}/open-rdma-driver"
  fi
}

check_commands() {
  missing=""
  for cmd in cargo cmake pkg-config make cc ip; do
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

check_blue_interfaces() {
  if ! have_cmd ip; then
    warn "ip command is missing; cannot inspect blue0/blue1 interfaces"
    return
  fi
  for iface in blue0 blue1; do
    if ip link show "$iface" >/dev/null 2>&1; then
      info "$iface interface is present"
    else
      warn "$iface interface is missing"
    fi
  done
  info "if blue0/blue1 are present, configure them with:"
  info "  sudo ip addr add 17.34.51.10/24 dev blue0"
  info "  sudo ip addr add 17.34.51.11/24 dev blue1"
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

check_privileged_setup() {
  if ! have_cmd sudo; then
    warn "sudo is not available for privileged setup"
    return
  fi
  if sudo -n true >/dev/null 2>&1; then
    info "sudo can run non-interactively for privileged setup"
  else
    warn "sudo is not available for privileged setup without interaction or elevated container permissions"
  fi
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

print_summary() {
  if [ "$WARNINGS" -eq 0 ]; then
    info "smoke check summary: READY"
  else
    warn_text="warnings"
    if [ "$WARNINGS" -eq 1 ]; then
      warn_text="warning"
    fi
    warn "smoke check summary: NOT READY ($WARNINGS $warn_text)"
    if [ "$DO_STRICT" -eq 1 ]; then
      die "strict mode failed because smoke check is not ready"
    fi
  fi
}

print_evidence_commands() {
  cat <<'EOF'
Evidence commands to paste back:

  uname -a
  lsmod | grep bluerdma
  ip addr show blue0
  ip addr show blue1
  cat /proc/meminfo | grep Huge
  hack/open-rdma-smoke-test.sh --driver-dir "$OPEN_RDMA_DRIVER" --strict
  hack/open-rdma-smoke-test.sh --driver-dir "$OPEN_RDMA_DRIVER" --build
  hack/open-rdma-smoke-test.sh --driver-dir "$OPEN_RDMA_DRIVER" --run
EOF
}

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

main() {
  parse_args "$@"
  if [ "$DO_EVIDENCE" -eq 1 ]; then
    print_evidence_commands
    exit 0
  fi
  info "open-rdma smoke test"
  check_linux
  check_kernel_build_dir
  check_driver_dir
  check_commands
  check_pkg_config
  check_module
  check_blue_interfaces
  check_hugepages
  check_privileged_setup
  print_manual_setup
  print_summary
  if [ "$DO_BUILD" -eq 1 ]; then
    build_open_rdma
  fi
  if [ "$DO_RUN" -eq 1 ]; then
    run_loopback
  fi
}

main "$@"
