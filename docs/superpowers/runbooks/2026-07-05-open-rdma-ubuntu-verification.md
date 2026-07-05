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
