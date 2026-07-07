#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_DIR="${TMPDIR:-/tmp}/jfs-rdma-cache-stress.$$"

usage() {
  cat <<'EOF'
Usage: hack/rdma-cache-stress.sh --transport http|rdma --nodes host:port[,host:port] [options]

Options:
  --concurrency N   Concurrent workers. Default: 1.
  --size BYTES      Payload size per operation. Default: 4096.
  --duration D      Stress duration, such as 3s or 30s. Default: 3s.
  --timeout D       Per-request timeout. Default: 200ms.
  --json            Emit JSON only.
EOF
}

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

TRANSPORT="http"
NODES=""
CONCURRENCY="1"
SIZE="4096"
DURATION="3s"
TIMEOUT="200ms"
JSON_ONLY="false"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --transport)
      [ "$#" -ge 2 ] || fail "--transport requires a value"
      TRANSPORT="$2"
      shift 2
      ;;
    --nodes)
      [ "$#" -ge 2 ] || fail "--nodes requires a value"
      NODES="$2"
      shift 2
      ;;
    --concurrency)
      [ "$#" -ge 2 ] || fail "--concurrency requires a value"
      CONCURRENCY="$2"
      shift 2
      ;;
    --size)
      [ "$#" -ge 2 ] || fail "--size requires a value"
      SIZE="$2"
      shift 2
      ;;
    --duration)
      [ "$#" -ge 2 ] || fail "--duration requires a value"
      DURATION="$2"
      shift 2
      ;;
    --timeout)
      [ "$#" -ge 2 ] || fail "--timeout requires a value"
      TIMEOUT="$2"
      shift 2
      ;;
    --json)
      JSON_ONLY="true"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      fail "unknown argument: $1"
      ;;
  esac
done

[ "$TRANSPORT" = "http" ] || [ "$TRANSPORT" = "rdma" ] || fail "--transport must be http or rdma"
[ -n "$NODES" ] || fail "--nodes is required"

cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT INT TERM

mkdir -p "$TMP_DIR"
client="$TMP_DIR/stress.go"
cat >"$client" <<'EOF'
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/juicedata/juicefs/pkg/cache/remote"
	"github.com/juicedata/juicefs/pkg/cache/remote/httpcache"
	"github.com/juicedata/juicefs/pkg/cache/remote/rdma"
)

type result struct {
	Transport   string  `json:"transport"`
	Nodes       string  `json:"nodes"`
	Concurrency int     `json:"concurrency"`
	Size        int     `json:"size"`
	DurationMS  int64   `json:"duration_ms"`
	Ops         int64   `json:"ops"`
	Errors      int64   `json:"errors"`
	OpsPerSec   float64 `json:"ops_per_second"`
	P50MS       float64 `json:"p50_ms"`
	P95MS       float64 `json:"p95_ms"`
	P99MS       float64 `json:"p99_ms"`
}

func main() {
	transport := flag.String("transport", "http", "transport")
	nodesRaw := flag.String("nodes", "", "comma-separated nodes")
	concurrency := flag.Int("concurrency", 1, "concurrency")
	size := flag.Int("size", 4096, "payload size")
	duration := flag.Duration("duration", 3*time.Second, "duration")
	timeout := flag.Duration("timeout", 200*time.Millisecond, "request timeout")
	jsonOnly := flag.Bool("json", false, "json only")
	flag.Parse()

	if *nodesRaw == "" || *concurrency < 1 || *size < 1 || *duration <= 0 || *timeout <= 0 {
		fmt.Fprintln(os.Stderr, "invalid stress options")
		os.Exit(2)
	}
	nodes := splitNodes(*nodesRaw)
	if len(nodes) == 0 {
		fmt.Fprintln(os.Stderr, "no usable nodes")
		os.Exit(2)
	}
	client, err := newClient(*transport, nodes, *timeout)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	defer client.Close()

	payload := bytes.Repeat([]byte("x"), *size)
	deadline := time.Now().Add(*duration)
	var latenciesMu sync.Mutex
	latencies := make([]time.Duration, 0, *concurrency*1024)
	var ops atomic.Int64
	var errors atomic.Int64
	var wg sync.WaitGroup
	start := time.Now()
	for worker := 0; worker < *concurrency; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; time.Now().Before(deadline); i++ {
				key := fmt.Sprintf("stress-%d-%d", worker, i)
				used, err := runOp(client, *timeout, key, payload)
				if err != nil {
					errors.Add(1)
					continue
				}
				ops.Add(1)
				latenciesMu.Lock()
				latencies = append(latencies, used)
				latenciesMu.Unlock()
			}
		}(worker)
	}
	wg.Wait()
	elapsed := time.Since(start)
	report := buildReport(*transport, *nodesRaw, *concurrency, *size, elapsed, ops.Load(), errors.Load(), latencies)
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if !*jsonOnly {
		fmt.Fprintf(os.Stderr, "completed %d stress ops with %d errors in %s\n", report.Ops, report.Errors, elapsed.Round(time.Millisecond))
	}
	fmt.Println(string(data))
}

func splitNodes(raw string) []string {
	parts := strings.Split(raw, ",")
	nodes := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			nodes = append(nodes, part)
		}
	}
	return nodes
}

func newClient(transport string, nodes []string, timeout time.Duration) (remote.Client, error) {
	switch transport {
	case "http":
		return httpcache.NewClientWithOptions(httpcache.Options{Nodes: nodes, Timeout: timeout, Replicas: 1}), nil
	case "rdma":
		return rdma.NewClient(rdma.Options{Nodes: nodes, Timeout: timeout, Replicas: 1}), nil
	default:
		return nil, fmt.Errorf("unsupported transport %q", transport)
	}
}

func runOp(client remote.Client, timeout time.Duration, key string, payload []byte) (time.Duration, error) {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := client.Put(ctx, key, payload); err != nil {
		return 0, err
	}
	reader, err := client.Get(ctx, key, 0, len(payload))
	if err != nil {
		return 0, err
	}
	data, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil {
		return 0, readErr
	}
	if closeErr != nil {
		return 0, closeErr
	}
	if !bytes.Equal(data, payload) {
		return 0, fmt.Errorf("payload mismatch for %s", key)
	}
	if err := client.Delete(ctx, key); err != nil {
		return 0, err
	}
	return time.Since(start), nil
}

func buildReport(transport, nodes string, concurrency, size int, elapsed time.Duration, ops, errors int64, latencies []time.Duration) result {
	values := make([]float64, 0, len(latencies))
	for _, latency := range latencies {
		values = append(values, float64(latency.Microseconds())/1000.0)
	}
	sort.Float64s(values)
	opsPerSecond := 0.0
	if elapsed > 0 {
		opsPerSecond = float64(ops) / elapsed.Seconds()
	}
	return result{
		Transport:   transport,
		Nodes:       nodes,
		Concurrency: concurrency,
		Size:        size,
		DurationMS:  elapsed.Milliseconds(),
		Ops:         ops,
		Errors:      errors,
		OpsPerSec:   opsPerSecond,
		P50MS:       percentile(values, 0.50),
		P95MS:       percentile(values, 0.95),
		P99MS:       percentile(values, 0.99),
	}
}

func percentile(values []float64, quantile float64) float64 {
	if len(values) == 0 {
		return 0
	}
	index := int(quantile*float64(len(values)-1) + 0.5)
	if index < 0 {
		index = 0
	}
	if index >= len(values) {
		index = len(values) - 1
	}
	return values[index]
}
EOF

go_tags=""
if [ "$TRANSPORT" = "rdma" ]; then
  go_tags="-tags rdma"
fi

(cd "$ROOT_DIR" && go run $go_tags "$client" \
  --transport "$TRANSPORT" \
  --nodes "$NODES" \
  --concurrency "$CONCURRENCY" \
  --size "$SIZE" \
  --duration "$DURATION" \
  --timeout "$TIMEOUT" \
  --json="$JSON_ONLY")
