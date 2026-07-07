#!/bin/sh
set -eu

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
TMP_DIR="${TMPDIR:-/tmp}/jfs-rdma-native-smoke.$$"
ADDR="${JFS_RDMA_SMOKE_ADDR:-127.0.0.1:19568}"
TESTS_RUN=0
SERVER_PID=""

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

write_client() {
  cat > "$TMP_DIR/client.go" <<'EOF'
package main

import (
	"bytes"
	"context"
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
	fmt.Printf("completed %d rdma native cache operations with concurrency %d in %s\n", ops, concurrency, time.Since(start).Round(time.Millisecond))
}

func waitForServer(addr string) error {
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
  mkdir -p "$cache_dir"
  "$bin" rdma-cache-server \
    --listen "$ADDR" \
    --transport rdma \
    --cache-dir "$cache_dir" \
    --cache-size 64M >"$TMP_DIR/rdma-cache-server.log" 2>&1 &
  SERVER_PID="$!"
}

run_client() {
  (cd "$ROOT_DIR" && go run -tags rdma "$TMP_DIR/client.go" "$ADDR")
}

mkdir -p "$TMP_DIR"
ensure_go_env
require_native_build
write_client
server_bin="$(build_server)"
pass "built rdma-tagged juicefs binary"
start_server "$server_bin"
pass "started rdma-cache-server with native transport"
run_client || fail "native rdma client round trip failed"
pass "native rdma client put/get/delete round trip succeeds"
