# Open RDMA Smoke Test Design

Date: 2026-07-05

Branch: `feature/rdma-distributed-cache`

## Goal

Add a repeatable local validation path for the `open-rdma/open-rdma-driver` mock provider on Ubuntu before JuiceFS implements a native `libibverbs` RDMA transport.

The validation should answer one question clearly:

```text
Can this Ubuntu host build and run open-rdma mock-mode verbs examples well enough to use it as a future JuiceFS RDMA test provider?
```

## Background

The current JuiceFS RDMA cache implementation has a transport-independent protocol, executor, server frame handler, and `rdma.Client` abstraction. The `rdma` build tag still reports native RDMA as unavailable and does not call `libibverbs`.

`open-rdma/open-rdma-driver` provides a mock mode that implements the provider path without real RDMA hardware. That makes it useful for validating future verbs-based client and server code, but only after the host environment can load the driver module, expose the provider, allocate huge pages, and run the upstream examples.

## Scope

This phase adds:

- A documented Ubuntu smoke-test workflow.
- A helper script under `hack/` that checks prerequisites and optionally runs open-rdma mock examples.

This phase does not add:

- JuiceFS native RDMA transport code.
- cgo bindings.
- `libibverbs` calls from `pkg/cache/remote/rdma`.
- open-rdma source code vendoring or submodules.
- automatic system package installation.
- automatic kernel module installation.
- performance benchmarking.

## User Workflow

The user prepares or points to an existing `open-rdma-driver` checkout:

```bash
git clone --recursive https://github.com/open-rdma/open-rdma-driver.git
cd open-rdma-driver
git checkout dev
```

The JuiceFS helper can then inspect the environment:

```bash
hack/open-rdma-smoke-test.sh --driver-dir /path/to/open-rdma-driver
hack/open-rdma-smoke-test.sh --driver-dir /path/to/open-rdma-driver --strict
```

By default, the script is read-only. It reports checks and prints remediation commands for missing prerequisites. With `--strict`, the script exits non-zero when readiness warnings are present, which is useful for automation and for deciding whether to proceed to build and run steps.

When the user intentionally wants build or run actions:

```bash
hack/open-rdma-smoke-test.sh --driver-dir /path/to/open-rdma-driver --build
hack/open-rdma-smoke-test.sh --driver-dir /path/to/open-rdma-driver --run
```

`--build` runs user-space build steps that do not require `sudo`.

`--run` runs the upstream mock-mode `examples/loopback 8192` program with the required `LD_LIBRARY_PATH`.

## Script Behavior

The helper script should:

1. Parse `--driver-dir`, `--build`, `--run`, `--strict`, and `--help`.
2. Verify it is running on Linux.
3. Print the kernel version and warn when the version is lower than 6.6.
4. Check whether `/lib/modules/$(uname -r)/build` exists, because the open-rdma kernel module build needs matching kernel headers.
5. Check that the open-rdma checkout exists and contains `dtld-ibverbs`, `examples`, and `scripts/setup-env.sh`.
6. Warn when the open-rdma checkout path is long, because open-rdma documents an rdma-core build failure mode in deep paths.
7. Check for command prerequisites: `cargo`, `cmake`, `pkg-config`, `make`, `cc`, and `ip`.
8. Check for likely system dependencies by looking for `libibverbs` and libnl pkg-config entries.
9. Check whether the `bluerdma` module is loaded.
10. Check whether `blue0` and `blue1` interfaces are visible and print the documented IP configuration commands.
11. Check whether huge pages are available.
12. Check whether `sudo` can run non-interactively for privileged setup.
13. Print the exact manual commands for missing privileged setup:

```bash
sudo apt install cmake pkg-config libnl-3-dev libnl-route-3-dev libclang-dev libibverbs-dev
make
sudo make install
sudo ./scripts/hugepages.sh alloc 512
```

14. Print a final readiness summary:

```text
smoke check summary: READY
smoke check summary: NOT READY (<n> warnings)
```

With `--strict`, `NOT READY` exits non-zero after the summary.

15. When `--build` is set, run:

```bash
cd "$DRIVER_DIR/dtld-ibverbs"
cargo build --no-default-features --features mock
cd "$DRIVER_DIR/dtld-ibverbs/rdma-core-55.0"
./build.sh
cd "$DRIVER_DIR/examples"
make
```

16. When `--run` is set, run:

```bash
LD_LIBRARY_PATH="$DRIVER_DIR/dtld-ibverbs/target/debug:$DRIVER_DIR/dtld-ibverbs/rdma-core-55.0/build/lib:${LD_LIBRARY_PATH:-}" \
  "$DRIVER_DIR/examples/loopback" 8192
```

The script exits non-zero when a requested build or run step fails.

## Safety

The script must not:

- run `sudo`
- install packages
- clone repositories
- modify shell startup files
- allocate huge pages
- load or unload kernel modules

It can print commands for the user to run manually.

## Success Criteria

The smoke test is successful when:

- The host passes prerequisite checks or reports precise missing setup.
- `--strict` exits 0 only when the host is ready and exits non-zero when readiness warnings remain.
- `--build` can build the open-rdma mock provider, rdma-core tree, and examples.
- `--run` can execute `examples/loopback 8192`.
- The output is clear enough to decide whether this Ubuntu environment is ready for JuiceFS native RDMA transport work.

## Future Work

After this smoke test passes on the target Ubuntu host, the next phase can implement a JuiceFS `rdma` build-tag transport that calls `libibverbs` and uses open-rdma mock mode as the first functional test backend.
