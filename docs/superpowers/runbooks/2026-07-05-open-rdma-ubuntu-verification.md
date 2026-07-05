# Open RDMA Ubuntu Verification Runbook

Date: 2026-07-05

Branch: `feature/rdma-distributed-cache`

## Purpose

Use this runbook on the target Ubuntu host to prove whether `open-rdma/open-rdma-driver` mock mode is ready to serve as the first functional test provider for future JuiceFS native RDMA transport work.

Completion evidence for the current goal requires all three commands to pass:

```bash
hack/open-rdma-smoke-test.sh --driver-dir "$OPEN_RDMA_DRIVER" --strict
hack/open-rdma-smoke-test.sh --driver-dir "$OPEN_RDMA_DRIVER" --build
hack/open-rdma-smoke-test.sh --driver-dir "$OPEN_RDMA_DRIVER" --run
```

## Host Preparation

Run these commands on the target Ubuntu host, not in a restricted container that prevents `sudo`, kernel module loading, or huge page configuration.

```bash
sudo apt update
sudo apt install cmake pkg-config libnl-3-dev libnl-route-3-dev libclang-dev libibverbs-dev
```

Use a short checkout path to avoid rdma-core path-length build failures:

```bash
cd "$HOME"
git clone --recursive https://github.com/open-rdma/open-rdma-driver.git
cd open-rdma-driver
git checkout dev
git submodule update --init --recursive
export OPEN_RDMA_DRIVER="$PWD"
```

Build and load the kernel modules:

```bash
cd "$OPEN_RDMA_DRIVER"
make
sudo make install
lsmod | grep bluerdma
```

If the kernel module build fails because headers are missing, fix `/lib/modules/$(uname -r)/build` first. WSL2-like environments may need custom kernel headers or `make KBUILD_MODPOST_WARN=1`; use that only after reviewing the upstream open-rdma installation notes for your host.

Configure the virtual interfaces when they exist:

```bash
sudo ip addr add 17.34.51.10/24 dev blue0 || true
sudo ip addr add 17.34.51.11/24 dev blue1 || true
ip addr show blue0
ip addr show blue1
```

Allocate huge pages:

```bash
cd "$OPEN_RDMA_DRIVER"
sudo ./scripts/hugepages.sh alloc 512
cat /proc/meminfo | grep Huge
```

## Readiness Check

From the JuiceFS checkout:

```bash
cd /path/to/juicefs
hack/open-rdma-smoke-test.sh --driver-dir "$OPEN_RDMA_DRIVER" --strict
```

Expected pass signal:

```text
smoke check summary: READY
```

If the summary is `NOT READY`, fix the warnings before running build or loopback. Paste the full output back into the Codex thread for triage.

## Troubleshooting Checklist

Use this table to map smoke-test warnings to the next action on the Ubuntu host.

| Smoke-test warning | What it means | Next action |
|---|---|---|
| `open-rdma documentation expects kernel >= 6.6` | The kernel may be too old for the driver. | Boot a kernel version 6.6 or newer before building the module. |
| `missing kernel build directory: /lib/modules/.../build` | Matching kernel headers are missing. | Install or prepare headers for the running kernel, then rerun `make`. On WSL2-like hosts, follow upstream open-rdma WSL kernel-header guidance. |
| `open-rdma checkout path is long` | rdma-core may fail in deep paths. | Move the checkout to a short path such as `$HOME/open-rdma-driver`. |
| `missing command: cmake` or another command | Required build tool is missing. | Run `sudo apt install cmake pkg-config libnl-3-dev libnl-route-3-dev libclang-dev libibverbs-dev`. |
| `missing pkg-config entry: libibverbs` | `libibverbs-dev` is missing or not visible to pkg-config. | Run `sudo apt install libibverbs-dev`. |
| `missing pkg-config entry: libnl-3.0` | `libnl-3-dev` is missing. | Run `sudo apt install libnl-3-dev`. |
| `missing pkg-config entry: libnl-route-3.0` | `libnl-route-3-dev` is missing. | Run `sudo apt install libnl-route-3-dev`. |
| `kernel module is not loaded: bluerdma` | The open-rdma kernel module is not active. | From `$OPEN_RDMA_DRIVER`, run `make` and `sudo make install`, then verify `lsmod | grep bluerdma`. |
| `blue0 interface is missing` or `blue1 interface is missing` | The module did not expose the expected virtual interfaces. | First fix `bluerdma` module loading. Then verify `ip link show blue0` and `ip link show blue1`. |
| `huge pages may need allocation` | Huge pages are not allocated or cannot be read. | From `$OPEN_RDMA_DRIVER`, run `sudo ./scripts/hugepages.sh alloc 512`. |
| `sudo is not available for privileged setup` | The current host or container cannot run privileged setup. | Use a real Ubuntu host, VM, or WSL2 environment with permission to install packages, load kernel modules, and allocate huge pages. |
| `strict mode failed because smoke check is not ready` | At least one readiness warning remains. | Fix the warnings above, then rerun `hack/open-rdma-smoke-test.sh --driver-dir "$OPEN_RDMA_DRIVER" --strict`. |

## Build Check

From the JuiceFS checkout:

```bash
hack/open-rdma-smoke-test.sh --driver-dir "$OPEN_RDMA_DRIVER" --build
```

Expected build steps:

```text
building open-rdma mock provider
building open-rdma rdma-core tree
building open-rdma examples
```

If rdma-core fails around a generated header or array-size error, move `open-rdma-driver` to a shorter path such as `$HOME/open-rdma-driver` and rebuild.

## Runtime Check

From the JuiceFS checkout:

```bash
hack/open-rdma-smoke-test.sh --driver-dir "$OPEN_RDMA_DRIVER" --run
```

Expected run step:

```text
running open-rdma loopback example
```

The command must exit 0. Paste the full `--run` output back into the Codex thread.

## Evidence To Return

The helper can print the evidence command list:

```bash
hack/open-rdma-smoke-test.sh --driver-dir "$OPEN_RDMA_DRIVER" --evidence
```

Paste these outputs into the thread:

```bash
uname -a
lsmod | grep bluerdma
ip addr show blue0
ip addr show blue1
cat /proc/meminfo | grep Huge
hack/open-rdma-smoke-test.sh --driver-dir "$OPEN_RDMA_DRIVER" --strict
hack/open-rdma-smoke-test.sh --driver-dir "$OPEN_RDMA_DRIVER" --build
hack/open-rdma-smoke-test.sh --driver-dir "$OPEN_RDMA_DRIVER" --run
```

## Next Phase Gate

Only start the JuiceFS native RDMA transport design after:

- `--strict` reports `READY`
- `--build` exits 0
- `--run` exits 0

Those three results prove the target Ubuntu host can run open-rdma mock mode well enough to become the first no-hardware functional backend for native RDMA transport tests.
