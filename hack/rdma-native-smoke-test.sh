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
	"os"
	"time"

	"github.com/juicedata/juicefs/pkg/cache/remote"
	"github.com/juicedata/juicefs/pkg/cache/remote/rdma"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: client ADDR")
		os.Exit(2)
	}
	client := rdma.NewClient(rdma.Options{
		Nodes:   []string{os.Args[1]},
		Timeout: 50 * time.Millisecond,
	})
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := waitPut(ctx, client); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	reader, err := client.Get(context.Background(), "rdma-native-smoke", 0, -1)
	if err != nil {
		fmt.Fprintf(os.Stderr, "get failed: %v\n", err)
		os.Exit(1)
	}
	data, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		fmt.Fprintf(os.Stderr, "read failed: %v\n", err)
		os.Exit(1)
	}
	if !bytes.Equal(data, []byte("native-smoke-data")) {
		fmt.Fprintf(os.Stderr, "unexpected payload: %q\n", data)
		os.Exit(1)
	}
	if err := client.Delete(context.Background(), "rdma-native-smoke"); err != nil {
		fmt.Fprintf(os.Stderr, "delete failed: %v\n", err)
		os.Exit(1)
	}
	if _, err := client.Get(context.Background(), "rdma-native-smoke", 0, -1); err != remote.ErrMiss {
		fmt.Fprintf(os.Stderr, "expected miss after delete, got %v\n", err)
		os.Exit(1)
	}
}

func waitPut(ctx context.Context, client remote.Client) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		lastErr = client.Put(context.Background(), "rdma-native-smoke", []byte("native-smoke-data"))
		if lastErr == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("put did not succeed before timeout: %w", lastErr)
		case <-ticker.C:
		}
	}
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
