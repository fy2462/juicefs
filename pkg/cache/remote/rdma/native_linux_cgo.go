//go:build rdma && linux && cgo

/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package rdma

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/juicedata/juicefs/pkg/cache/remote/rdma/native"
	"github.com/juicedata/juicefs/pkg/cache/remote/rdma/protocol"
)

type nativeOptions struct {
	deviceIndex   int
	maxFrameBytes int
	cqTimeout     time.Duration
}

type nativeDialer struct {
	options nativeOptions
}

type nativeConn struct {
	conn          net.Conn
	maxFrameBytes int
	mu            sync.Mutex
}

func newNativeDialerFromEnv() Dialer {
	options, err := nativeOptionsFromEnv()
	if err != nil {
		return errorDialer{err: err}
	}
	return &nativeDialer{options: options}
}

func ListenAndServe(ctx context.Context, options ServeOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", options.Listen)
	if err != nil {
		return err
	}
	defer listener.Close()

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = listener.Close()
		case <-done:
		}
	}()

	server := NewServer(options.Backend)
	maxFrame := maxFrameBytes(options.MaxFrameBytes)
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go serveNativeConn(ctx, conn, server, maxFrame)
	}
}

func nativeOptionsFromEnv() (nativeOptions, error) {
	deviceIndex, err := parseEnvInt("JFS_RDMA_DEVICE_INDEX", 0)
	if err != nil {
		return nativeOptions{}, err
	}
	maxFrame, err := parseEnvInt("JFS_RDMA_MAX_FRAME_BYTES", defaultRDMAFrameBytes)
	if err != nil {
		return nativeOptions{}, err
	}
	cqTimeout, err := parseEnvDuration("JFS_RDMA_CQ_TIMEOUT", 50*time.Millisecond)
	if err != nil {
		return nativeOptions{}, err
	}
	return nativeOptions{
		deviceIndex:   deviceIndex,
		maxFrameBytes: maxFrameBytes(maxFrame),
		cqTimeout:     cqTimeout,
	}, nil
}

func parseEnvInt(name string, defaultValue int) (int, error) {
	value := os.Getenv(name)
	if value == "" {
		return defaultValue, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return parsed, nil
}

func parseEnvDuration(name string, defaultValue time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return defaultValue, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}
	return parsed, nil
}

func (d *nativeDialer) Dial(ctx context.Context, node string, options Options) (Conn, error) {
	resources, err := native.NewResources(d.options.deviceIndex, d.options.maxFrameBytes)
	if err != nil {
		return nil, err
	}
	_ = resources.Close()
	netDialer := net.Dialer{Timeout: options.Timeout}
	conn, err := netDialer.DialContext(ctx, "tcp", node)
	if err != nil {
		return nil, err
	}
	return &nativeConn{conn: conn, maxFrameBytes: d.options.maxFrameBytes}, nil
}

func (c *nativeConn) RoundTrip(ctx context.Context, req protocol.Request) (protocol.Response, error) {
	payload, err := protocol.EncodeRequest(req)
	if err != nil {
		return protocol.Response{}, err
	}
	frame, err := encodeFrame(payload, c.maxFrameBytes)
	if err != nil {
		return protocol.Response{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.conn.SetDeadline(deadline)
		defer c.conn.SetDeadline(time.Time{})
	}
	if _, err := c.conn.Write(frame); err != nil {
		return protocol.Response{}, err
	}
	respFrame, err := readFrame(c.conn, c.maxFrameBytes)
	if err != nil {
		return protocol.Response{}, err
	}
	return protocol.DecodeResponse(respFrame)
}

func (c *nativeConn) Close() error {
	return c.conn.Close()
}

func serveNativeConn(ctx context.Context, conn net.Conn, server *Server, maxFrameBytes int) {
	defer conn.Close()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()

	for {
		reqFrame, err := readFrame(conn, maxFrameBytes)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return
			}
			return
		}
		respFrame, err := server.HandleFrame(ctx, reqFrame)
		if err != nil {
			return
		}
		frame, err := encodeFrame(respFrame, maxFrameBytes)
		if err != nil {
			return
		}
		if err := writeAll(conn, frame); err != nil {
			return
		}
	}
}

func writeAll(conn net.Conn, data []byte) error {
	for len(data) > 0 {
		n, err := conn.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

type errorDialer struct {
	err error
}

func (d errorDialer) Dial(ctx context.Context, node string, options Options) (Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, d.err
}
