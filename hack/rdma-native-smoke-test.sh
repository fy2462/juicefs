#!/bin/sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
TMP_DIR="${TMPDIR:-/tmp}/jfs-rdma-native-smoke.$$"
ADDR="${JFS_RDMA_SMOKE_ADDR:-127.0.0.1:19568}"
TESTS_RUN=0
SERVER_PID=""
STRICT_DEVICE="${JFS_RDMA_REQUIRE_DEVICE:-false}"
MOCK_RDMA_DIR=""

usage() {
  cat <<'EOF'
Usage: hack/rdma-native-smoke-test.sh [--strict-device] [--mock-rdma DIR]

Runs the rdma-cache-server native transport smoke. By default the smoke can
fall back to the framed TCP path when no RDMA device exists, which keeps normal
CI hardware-independent.

Options:
  --strict-device   Require an ibverbs/open-rdma device and force payloads
                    through native RDMA resources with JFS_RDMA_REQUIRE_DEVICE.
  --mock-rdma DIR   Use an open-rdma-driver checkout in mock mode as the RDMA
                    provider and force the strict native data path.

Environment:
  JFS_RDMA_SMOKE_OPS          Total put/get/delete iterations. Default: 1.
  JFS_RDMA_SMOKE_CONCURRENCY  Concurrent workers. Default: 1.
  JFS_RDMA_BIN                Reuse an existing rdma-tagged juicefs binary.
  JFS_RDMA_SMOKE_REPORT       Optional path for a JSON performance summary.
  JFS_RDMA_SMOKE_SERVER_DEVICE_INDEX
                              RDMA device index for rdma-cache-server.
  JFS_RDMA_SMOKE_CLIENT_DEVICE_INDEX
                              RDMA device index for the smoke client.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --strict-device)
      STRICT_DEVICE=true
      shift
      ;;
    --mock-rdma)
      [ "$#" -ge 2 ] || fail "--mock-rdma requires a driver directory"
      MOCK_RDMA_DIR="$2"
      STRICT_DEVICE=true
      shift 2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      usage >&2
      exit 2
      ;;
  esac
done

cleanup() {
  if [ -n "$SERVER_PID" ]; then
    kill "$SERVER_PID" >/dev/null 2>&1 || true
    wait "$SERVER_PID" >/dev/null 2>&1 || true
  fi
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT INT TERM

fail() {
  echo "FAIL: $*" >&2
  if [ -f "$TMP_DIR/rdma-cache-server.log" ]; then
    echo "----- rdma-cache-server.log -----" >&2
    cat "$TMP_DIR/rdma-cache-server.log" >&2
    echo "---------------------------------" >&2
  fi
  exit 1
}

pass() {
  TESTS_RUN=$((TESTS_RUN + 1))
  echo "ok $TESTS_RUN - $*"
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1
}

ensure_go_env() {
  if ! need_cmd go && [ -x /usr/local/go/bin/go ]; then
    PATH="/usr/local/go/bin:$PATH"
    export PATH
  fi
  need_cmd go || fail "go is required"
  GOPATH="${GOPATH:-$HOME/go}"
  export GOPATH
}

require_native_build() {
  os="$(go env GOOS)"
  cgo="$(go env CGO_ENABLED)"
  if [ "$os" != "linux" ]; then
    echo "SKIP: native RDMA smoke requires linux; got $os"
    exit 0
  fi
  if [ "$cgo" != "1" ]; then
    echo "SKIP: native RDMA smoke requires CGO_ENABLED=1"
    exit 0
  fi
}

enable_mock_rdma() {
  [ -n "$MOCK_RDMA_DIR" ] || return 0
  [ -d "$MOCK_RDMA_DIR" ] || fail "missing open-rdma checkout: $MOCK_RDMA_DIR"
  "$ROOT_DIR/hack/open-rdma-smoke-test.sh" --driver-dir "$MOCK_RDMA_DIR" --strict
  OPEN_RDMA_DRIVER="$MOCK_RDMA_DIR"
  export OPEN_RDMA_DRIVER
  mock_libs="$MOCK_RDMA_DIR/dtld-ibverbs/target/debug:$MOCK_RDMA_DIR/dtld-ibverbs/rdma-core-55.0/build/lib"
  [ -d "$MOCK_RDMA_DIR/dtld-ibverbs/target/debug" ] || fail "missing open-rdma mock library dir: $MOCK_RDMA_DIR/dtld-ibverbs/target/debug"
  [ -d "$MOCK_RDMA_DIR/dtld-ibverbs/rdma-core-55.0/build/lib" ] || fail "missing open-rdma rdma-core library dir: $MOCK_RDMA_DIR/dtld-ibverbs/rdma-core-55.0/build/lib"
  LD_LIBRARY_PATH="$mock_libs:${LD_LIBRARY_PATH:-}"
  JFS_RDMA_SMOKE_SERVER_DEVICE_INDEX="${JFS_RDMA_SMOKE_SERVER_DEVICE_INDEX:-0}"
  JFS_RDMA_SMOKE_CLIENT_DEVICE_INDEX="${JFS_RDMA_SMOKE_CLIENT_DEVICE_INDEX:-1}"
  export LD_LIBRARY_PATH
  export JFS_RDMA_SMOKE_SERVER_DEVICE_INDEX JFS_RDMA_SMOKE_CLIENT_DEVICE_INDEX
}

require_rdma_device_for_strict() {
  [ "$STRICT_DEVICE" = "true" ] || return 0
  checker="$TMP_DIR/check-rdma-device.go"
  cat > "$checker" <<'EOF'
package main

import (
	"fmt"
	"os"

	"github.com/juicedata/juicefs/pkg/cache/remote/rdma/native"
)

func main() {
	count, err := native.DeviceCount()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(count)
}
EOF
  count="$(cd "$ROOT_DIR" && go run -tags rdma "$checker")" || fail "strict native RDMA smoke could not inspect RDMA devices"
  if [ "$count" -gt 0 ]; then
    return 0
  fi
  echo "SKIP: strict native RDMA smoke requires an ibverbs/open-rdma device"
  exit 0
}

write_client() {
  cat > "$TMP_DIR/client.go" <<'EOF'
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/juicedata/juicefs/pkg/cache/remote"
	"github.com/juicedata/juicefs/pkg/cache/remote/rdma"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: client ADDR")
		os.Exit(2)
	}
	addr := os.Args[1]
	ops := envInt("JFS_RDMA_SMOKE_OPS", 1)
	concurrency := envInt("JFS_RDMA_SMOKE_CONCURRENCY", 1)
	if ops < 1 {
		fmt.Fprintln(os.Stderr, "JFS_RDMA_SMOKE_OPS must be >= 1")
		os.Exit(2)
	}
	if concurrency < 1 {
		fmt.Fprintln(os.Stderr, "JFS_RDMA_SMOKE_CONCURRENCY must be >= 1")
		os.Exit(2)
	}
	if concurrency > ops {
		concurrency = ops
	}
	if err := waitForServer(addr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	start := time.Now()
	if err := runWorkload(addr, ops, concurrency); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	duration := time.Since(start)
	if err := writeReport(ops, concurrency, duration); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("completed %d rdma native cache operations with concurrency %d in %s\n", ops, concurrency, duration.Round(time.Millisecond))
}

func waitForServer(addr string) error {
	if os.Getenv("JFS_RDMA_SMOKE_SKIP_TCP_PROBE") == "true" {
		time.Sleep(500 * time.Millisecond)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		conn, err := net.DialTimeout("tcp", addr, 50*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return fmt.Errorf("server did not become ready before timeout: %w", lastErr)
		case <-ticker.C:
		}
	}
}

func runWorkload(addr string, ops, concurrency int) error {
	errCh := make(chan error, concurrency)
	var wg sync.WaitGroup
	for worker := 0; worker < concurrency; worker++ {
		count := ops / concurrency
		if worker < ops%concurrency {
			count++
		}
		wg.Add(1)
		go func(worker, count int) {
			defer wg.Done()
			if err := runWorker(addr, worker, count); err != nil {
				errCh <- err
			}
		}(worker, count)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

type smokeReport struct {
	Ops          int     `json:"ops"`
	Concurrency  int     `json:"concurrency"`
	DurationMS   int64   `json:"duration_ms"`
	OpsPerSecond float64 `json:"ops_per_second"`
}

func writeReport(ops, concurrency int, duration time.Duration) error {
	path := os.Getenv("JFS_RDMA_SMOKE_REPORT")
	if path == "" {
		return nil
	}
	opsPerSecond := 0.0
	if duration > 0 {
		opsPerSecond = float64(ops) / duration.Seconds()
	}
	report := smokeReport{
		Ops:          ops,
		Concurrency:  concurrency,
		DurationMS:   duration.Milliseconds(),
		OpsPerSecond: opsPerSecond,
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode RDMA smoke report: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write RDMA smoke report %s: %w", path, err)
	}
	fmt.Printf("wrote rdma native smoke report to %s\n", path)
	return nil
}

func runWorker(addr string, worker, count int) error {
	client := rdma.NewClient(rdma.Options{
		Nodes:   []string{addr},
		Timeout: 200 * time.Millisecond,
	})
	defer client.Close()
	for i := 0; i < count; i++ {
		key := fmt.Sprintf("rdma-native-smoke-%d-%d", worker, i)
		payload := []byte(fmt.Sprintf("native-smoke-data-%d-%d", worker, i))
		if err := client.Put(context.Background(), key, payload); err != nil {
			return fmt.Errorf("put %s failed: %w", key, err)
		}
		reader, err := client.Get(context.Background(), key, 0, -1)
		if err != nil {
			return fmt.Errorf("get %s failed: %w", key, err)
		}
		data, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil {
			return fmt.Errorf("read %s failed: %w", key, err)
		}
		if !bytes.Equal(data, payload) {
			return fmt.Errorf("unexpected payload for %s: %q", key, data)
		}
		if err := client.Delete(context.Background(), key); err != nil {
			return fmt.Errorf("delete %s failed: %w", key, err)
		}
		if _, err := client.Get(context.Background(), key, 0, -1); err != remote.ErrMiss {
			return fmt.Errorf("expected miss after delete for %s, got %v", key, err)
		}
	}
	return nil
}

func envInt(name string, defaultValue int) int {
	value := os.Getenv(name)
	if value == "" {
		return defaultValue
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s must be an integer: %v\n", name, err)
		os.Exit(2)
	}
	return parsed
}

EOF
}

build_server() {
  bin="${JFS_RDMA_BIN:-$TMP_DIR/juicefs-rdma}"
  if [ -n "${JFS_RDMA_BIN:-}" ]; then
    [ -x "$bin" ] || fail "JFS_RDMA_BIN is not executable: $bin"
    printf '%s\n' "$bin"
    return
  fi
  (cd "$ROOT_DIR" && go build -tags rdma -o "$bin" .)
  printf '%s\n' "$bin"
}

start_server() {
  bin="$1"
  cache_dir="$TMP_DIR/cache"
  server_device_index="${JFS_RDMA_SMOKE_SERVER_DEVICE_INDEX:-${JFS_RDMA_DEVICE_INDEX:-0}}"
  mkdir -p "$cache_dir"
  JFS_RDMA_REQUIRE_DEVICE="$STRICT_DEVICE" JFS_RDMA_DEVICE_INDEX="$server_device_index" "$bin" rdma-cache-server \
    --listen "$ADDR" \
    --transport rdma \
    --cache-dir "$cache_dir" \
    --cache-size 64M >"$TMP_DIR/rdma-cache-server.log" 2>&1 &
  SERVER_PID="$!"
}

run_client() {
  skip_probe=false
  if [ "$STRICT_DEVICE" = "true" ]; then
    skip_probe=true
  fi
  client_device_index="${JFS_RDMA_SMOKE_CLIENT_DEVICE_INDEX:-${JFS_RDMA_DEVICE_INDEX:-0}}"
  (cd "$ROOT_DIR" && JFS_RDMA_REQUIRE_DEVICE="$STRICT_DEVICE" JFS_RDMA_DEVICE_INDEX="$client_device_index" JFS_RDMA_SMOKE_SKIP_TCP_PROBE="$skip_probe" go run -tags rdma "$TMP_DIR/client.go" "$ADDR")
}

mkdir -p "$TMP_DIR"
ensure_go_env
require_native_build
enable_mock_rdma
require_rdma_device_for_strict
write_client
server_bin="$(build_server)"
pass "built rdma-tagged juicefs binary"
start_server "$server_bin"
pass "started rdma-cache-server with native transport"
run_client || fail "native rdma client round trip failed"
pass "native rdma client put/get/delete round trip succeeds"
