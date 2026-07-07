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
	"bytes"
	"context"
	"encoding/json"
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

const nativeHandshakeFrameBytes = 64 << 10

type nativeOptions struct {
	deviceIndex     int
	maxFrameBytes   int
	cqTimeout       time.Duration
	requireDevice   bool
	resourceFactory nativeResourceFactory
}

type nativeDialer struct {
	options nativeOptions
}

type nativeConn struct {
	conn          net.Conn
	resources     nativeResource
	maxFrameBytes int
	mu            sync.Mutex
}

type nativeEndpointWire struct {
	LID   uint16 `json:"lid"`
	QPN   uint32 `json:"qpn"`
	PSN   uint32 `json:"psn"`
	RKey  uint32 `json:"rkey"`
	VAddr uint64 `json:"vaddr"`
	Port  uint8  `json:"port"`
}

type nativeResource interface {
	LocalEndpoint() (native.Endpoint, error)
	Connect(native.Endpoint) error
	Buffer() []byte
	PostRecv() error
	PostSend([]byte) error
	PollCompletion() (int, error)
	Close() error
}

type nativeResourceFactory interface {
	New(deviceIndex, maxFrameBytes int) (nativeResource, error)
}

type ibverbsResourceFactory struct{}

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
	nativeOptions, err := nativeOptionsFromEnv()
	if err != nil {
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
		go serveNativeConnWithResourceFactory(ctx, conn, server, maxFrame, nativeOptions.resourceFactory, nativeOptions.requireDevice, nativeOptions.deviceIndex)
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
	requireDevice, err := parseEnvBool("JFS_RDMA_REQUIRE_DEVICE", false)
	if err != nil {
		return nativeOptions{}, err
	}
	return nativeOptions{
		deviceIndex:     deviceIndex,
		maxFrameBytes:   maxFrameBytes(maxFrame),
		cqTimeout:       cqTimeout,
		requireDevice:   requireDevice,
		resourceFactory: ibverbsResourceFactory{},
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

func parseEnvBool(name string, defaultValue bool) (bool, error) {
	value := os.Getenv(name)
	if value == "" {
		return defaultValue, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s: %w", name, err)
	}
	return parsed, nil
}

func (d *nativeDialer) Dial(ctx context.Context, node string, options Options) (Conn, error) {
	resourceFactory := d.options.resourceFactory
	if resourceFactory == nil {
		resourceFactory = ibverbsResourceFactory{}
	}
	resources, err := resourceFactory.New(d.options.deviceIndex, d.options.maxFrameBytes)
	if err != nil {
		if d.options.requireDevice || !errors.Is(err, native.ErrNoDevice) {
			return nil, err
		}
	}
	netDialer := net.Dialer{Timeout: options.Timeout}
	conn, err := netDialer.DialContext(ctx, "tcp", node)
	if err != nil {
		if resources != nil {
			_ = resources.Close()
		}
		return nil, err
	}
	if resources != nil {
		if err := clientNativeHandshake(ctx, conn, resources); err != nil {
			_ = resources.Close()
			_ = conn.Close()
			return nil, err
		}
	}
	return &nativeConn{conn: conn, resources: resources, maxFrameBytes: d.options.maxFrameBytes}, nil
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
	if c.resources != nil {
		if err := c.resources.PostRecv(); err != nil {
			return protocol.Response{}, err
		}
		if err := c.resources.PostSend(frame); err != nil {
			return protocol.Response{}, err
		}
		if _, err := c.resources.PollCompletion(); err != nil {
			return protocol.Response{}, err
		}
		n, err := c.resources.PollCompletion()
		if err != nil {
			return protocol.Response{}, err
		}
		respFrame, err := readFrame(bytes.NewReader(c.resources.Buffer()[:n]), c.maxFrameBytes)
		if err != nil {
			return protocol.Response{}, err
		}
		return protocol.DecodeResponse(respFrame)
	}
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
	var err error
	if c.resources != nil {
		err = errors.Join(err, c.resources.Close())
		c.resources = nil
	}
	err = errors.Join(err, c.conn.Close())
	return err
}

func encodeNativeEndpoint(endpoint native.Endpoint) ([]byte, error) {
	return json.Marshal(nativeEndpointWire{
		LID:   endpoint.LID,
		QPN:   endpoint.QPN,
		PSN:   endpoint.PSN,
		RKey:  endpoint.RKey,
		VAddr: endpoint.VAddr,
		Port:  endpoint.Port,
	})
}

func decodeNativeEndpoint(data []byte) (native.Endpoint, error) {
	var wire nativeEndpointWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return native.Endpoint{}, err
	}
	return native.Endpoint{
		LID:   wire.LID,
		QPN:   wire.QPN,
		PSN:   wire.PSN,
		RKey:  wire.RKey,
		VAddr: wire.VAddr,
		Port:  wire.Port,
	}, nil
}

func clientNativeHandshake(ctx context.Context, conn net.Conn, resources nativeResource) error {
	local, err := resources.LocalEndpoint()
	if err != nil {
		return err
	}
	localFrame, err := encodeNativeEndpoint(local)
	if err != nil {
		return err
	}
	if err := writeHandshakeFrame(ctx, conn, localFrame); err != nil {
		return err
	}
	remoteFrame, err := readHandshakeFrame(ctx, conn)
	if err != nil {
		return err
	}
	remote, err := decodeNativeEndpoint(remoteFrame)
	if err != nil {
		return err
	}
	return resources.Connect(remote)
}

func serverNativeHandshake(ctx context.Context, conn net.Conn, resources nativeResource) error {
	remoteFrame, err := readHandshakeFrame(ctx, conn)
	if err != nil {
		return err
	}
	remote, err := decodeNativeEndpoint(remoteFrame)
	if err != nil {
		return err
	}
	local, err := resources.LocalEndpoint()
	if err != nil {
		return err
	}
	localFrame, err := encodeNativeEndpoint(local)
	if err != nil {
		return err
	}
	if err := writeHandshakeFrame(ctx, conn, localFrame); err != nil {
		return err
	}
	return resources.Connect(remote)
}

func readHandshakeFrame(ctx context.Context, conn net.Conn) ([]byte, error) {
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetReadDeadline(deadline)
		defer conn.SetReadDeadline(time.Time{})
	}
	return readFrame(conn, nativeHandshakeFrameBytes)
}

func writeHandshakeFrame(ctx context.Context, conn net.Conn, payload []byte) error {
	frame, err := encodeFrame(payload, nativeHandshakeFrameBytes)
	if err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetWriteDeadline(deadline)
		defer conn.SetWriteDeadline(time.Time{})
	}
	return writeAll(conn, frame)
}

func serveNativeConn(ctx context.Context, conn net.Conn, server *Server, maxFrameBytes int) {
	serveNativeConnWithResourceFactory(ctx, conn, server, maxFrameBytes, ibverbsResourceFactory{}, false, 0)
}

func serveNativeConnWithResourceFactory(ctx context.Context, conn net.Conn, server *Server, maxFrameBytes int, resourceFactory nativeResourceFactory, requireDevice bool, deviceIndex int) {
	defer conn.Close()
	resources, err := resourceFactory.New(deviceIndex, maxFrameBytes)
	if err == nil {
		defer resources.Close()
		if err := serverNativeHandshake(ctx, conn, resources); err != nil {
			return
		}
		serveNativeResourceFrames(ctx, resources, server, maxFrameBytes)
		return
	} else if requireDevice || !errors.Is(err, native.ErrNoDevice) {
		return
	}
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

func serveNativeResourceFrames(ctx context.Context, resources nativeResource, server *Server, maxFrameBytes int) {
	for {
		if err := serveNativeResourceFrame(ctx, resources, server, maxFrameBytes); err != nil {
			return
		}
	}
}

func serveNativeResourceFrame(ctx context.Context, resources nativeResource, server *Server, maxFrameBytes int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := resources.PostRecv(); err != nil {
		return err
	}
	n, err := resources.PollCompletion()
	if err != nil {
		return err
	}
	reqFrame, err := readFrame(bytes.NewReader(resources.Buffer()[:n]), maxFrameBytes)
	if err != nil {
		return err
	}
	respFrame, err := server.HandleFrame(ctx, reqFrame)
	if err != nil {
		return err
	}
	frame, err := encodeFrame(respFrame, maxFrameBytes)
	if err != nil {
		return err
	}
	if err := resources.PostSend(frame); err != nil {
		return err
	}
	_, err = resources.PollCompletion()
	return err
}

func (ibverbsResourceFactory) New(deviceIndex, maxFrameBytes int) (nativeResource, error) {
	resources, err := native.NewResources(deviceIndex, maxFrameBytes)
	if resources == nil {
		return nil, err
	}
	return resources, err
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
