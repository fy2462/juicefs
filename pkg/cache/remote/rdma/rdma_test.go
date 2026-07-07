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
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/cache/remote"
	"github.com/juicedata/juicefs/pkg/cache/remote/mock"
	"github.com/juicedata/juicefs/pkg/cache/remote/rdma/protocol"
	"github.com/stretchr/testify/require"
)

func TestNewClientReturnsUnsupportedByDefault(t *testing.T) {
	if Capability().Built {
		t.Skip("default unsupported client behavior only applies without the rdma build tag")
	}
	client := NewClient(Options{
		Nodes:    []string{"127.0.0.1:9568"},
		Timeout:  time.Millisecond,
		Replicas: 1,
	})

	require.ErrorIs(t, client.Put(context.Background(), "k", []byte("v")), ErrUnsupported)
	_, err := client.Get(context.Background(), "k", 0, -1)
	require.ErrorIs(t, err, ErrUnsupported)
	require.ErrorIs(t, client.Delete(context.Background(), "k"), ErrUnsupported)
	require.NoError(t, client.Close())
}

func TestCapabilityDefaultBuild(t *testing.T) {
	capability := Capability()

	require.False(t, capability.Available)
	require.NotEmpty(t, capability.Reason)
}

func TestListenAndServeDefaultBuildUnsupported(t *testing.T) {
	if Capability().Built {
		t.Skip("default unsupported server behavior only applies without the rdma build tag")
	}
	err := ListenAndServe(context.Background(), ServeOptions{
		Listen:  "127.0.0.1:0",
		Backend: mock.NewClient(),
	})

	require.ErrorIs(t, err, ErrUnsupported)
}

func TestServerHandleFrameUsesProtocolExecutor(t *testing.T) {
	backend := mock.NewClient()
	server := NewServer(backend)
	req, err := protocol.EncodeRequest(protocol.Request{
		Op:      protocol.OpPut,
		Key:     "k",
		Payload: []byte("data"),
	})
	require.NoError(t, err)
	frame, err := server.HandleFrame(context.Background(), req)
	require.NoError(t, err)
	resp, err := protocol.DecodeResponse(frame)
	require.NoError(t, err)
	require.Equal(t, protocol.StatusOK, resp.Status)

	req, err = protocol.EncodeRequest(protocol.Request{Op: protocol.OpGet, Key: "k", Size: -1})
	require.NoError(t, err)
	frame, err = server.HandleFrame(context.Background(), req)
	require.NoError(t, err)
	resp, err = protocol.DecodeResponse(frame)
	require.NoError(t, err)
	require.Equal(t, protocol.StatusOK, resp.Status)
	require.Equal(t, []byte("data"), resp.Payload)
}

func TestServerHandleFramePing(t *testing.T) {
	req, err := protocol.EncodeRequest(protocol.Request{Op: protocol.OpPing})
	require.NoError(t, err)
	frame, err := NewServer(mock.NewClient()).HandleFrame(context.Background(), req)
	require.NoError(t, err)
	resp, err := protocol.DecodeResponse(frame)
	require.NoError(t, err)
	require.Equal(t, protocol.StatusOK, resp.Status)
}

func TestServerHandleFrameBadRequest(t *testing.T) {
	frame, err := NewServer(mock.NewClient()).HandleFrame(context.Background(), []byte("{"))
	require.NoError(t, err)
	resp, err := protocol.DecodeResponse(frame)
	require.NoError(t, err)
	require.Equal(t, protocol.StatusBadRequest, resp.Status)
}

func TestClientRoundTripGet(t *testing.T) {
	backend := mock.NewClient()
	require.NoError(t, backend.Put(context.Background(), "k", []byte("abcdef")))
	client := NewClient(Options{
		Nodes:  []string{"node-a"},
		Dialer: memoryDialer{server: NewServer(backend)},
	})

	r, err := client.Get(context.Background(), "k", 2, 3)
	require.NoError(t, err)
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.Equal(t, []byte("cde"), data)
	require.NoError(t, client.Close())
}

func TestClientRoundTripPutDelete(t *testing.T) {
	backend := mock.NewClient()
	client := NewClient(Options{
		Nodes:  []string{"node-a"},
		Dialer: memoryDialer{server: NewServer(backend)},
	})

	require.NoError(t, client.Put(context.Background(), "k", []byte("data")))
	r, err := backend.Get(context.Background(), "k", 0, -1)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.NoError(t, client.Delete(context.Background(), "k"))
	_, err = backend.Get(context.Background(), "k", 0, -1)
	require.ErrorIs(t, err, remote.ErrMiss)
	require.NoError(t, client.Close())
}

func TestClientRoundTripMiss(t *testing.T) {
	client := NewClient(Options{
		Nodes:  []string{"node-a"},
		Dialer: memoryDialer{server: NewServer(mock.NewClient())},
	})

	_, err := client.Get(context.Background(), "missing", 0, -1)
	require.ErrorIs(t, err, remote.ErrMiss)
	require.NoError(t, client.Close())
}

func TestClientReleasesNonReusableConnAfterSuccess(t *testing.T) {
	conn := &nonReusableConn{server: NewServer(mock.NewClient())}
	client := newClient(Options{
		Nodes:    []string{"node-a"},
		Replicas: 1,
		Dialer:   staticDialer{conn: conn},
	})
	defer client.Close()

	require.NoError(t, client.Put(context.Background(), "k", []byte("v")))

	require.Equal(t, int32(1), conn.closes.Load())
	client.mu.Lock()
	_, exists := client.conns["node-a"]
	client.mu.Unlock()
	require.False(t, exists)
}

func TestClientUnavailablePreservesLastRoundTripError(t *testing.T) {
	roundTripErr := errors.New("verbs completion timed out")
	client := NewClient(Options{
		Nodes:  []string{"node-a"},
		Dialer: staticDialer{conn: errorConn{err: roundTripErr}},
	})

	err := client.Put(context.Background(), "k", []byte("v"))
	require.ErrorIs(t, err, remote.ErrUnavailable)
	require.ErrorIs(t, err, roundTripErr)
	require.Contains(t, err.Error(), "verbs completion timed out")
	require.NoError(t, client.Close())
}

func TestClientSkipsFailedNodeAndUsesHealthyReplica(t *testing.T) {
	backend := mock.NewClient()
	require.NoError(t, backend.Put(context.Background(), "k", []byte("value")))
	badDials := &atomic.Int32{}
	goodDials := &atomic.Int32{}
	client := NewClient(Options{
		Nodes:         []string{"bad", "good"},
		Replicas:      2,
		FailThreshold: 1,
		NodeCooldown:  time.Minute,
		Dialer: routingDialer{
			servers: map[string]*Server{"good": NewServer(backend)},
			dials: map[string]*atomic.Int32{
				"bad":  badDials,
				"good": goodDials,
			},
		},
	})

	r, err := client.Get(context.Background(), "k", 0, -1)
	require.NoError(t, err)
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.Equal(t, []byte("value"), data)

	r, err = client.Get(context.Background(), "k", 0, -1)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.Equal(t, int32(1), badDials.Load())
	require.GreaterOrEqual(t, goodDials.Load(), int32(1))
	require.NoError(t, client.Close())
}

func TestClientActiveProbeRecoversNodeDuringCooldown(t *testing.T) {
	backend := mock.NewClient()
	require.NoError(t, backend.Put(context.Background(), "k", []byte("value")))
	dialer := &dynamicDialer{}
	client := NewClient(Options{
		Nodes:         []string{"node-a"},
		Timeout:       50 * time.Millisecond,
		FailThreshold: 1,
		NodeCooldown:  time.Hour,
		ProbeInterval: 10 * time.Millisecond,
		ProbeTimeout:  10 * time.Millisecond,
		Dialer:        dialer,
	})
	defer client.Close()

	_, err := client.Get(context.Background(), "k", 0, -1)
	require.ErrorIs(t, err, remote.ErrUnavailable)

	dialer.SetServer(NewServer(backend))
	require.Eventually(t, func() bool {
		r, err := client.Get(context.Background(), "k", 0, -1)
		if err != nil {
			return false
		}
		defer r.Close()
		data, err := io.ReadAll(r)
		return err == nil && string(data) == "value"
	}, time.Second, 10*time.Millisecond)
}

type memoryDialer struct {
	server *Server
}

func (d memoryDialer) Dial(ctx context.Context, node string, options Options) (Conn, error) {
	return memoryConn{server: d.server}, nil
}

type staticDialer struct {
	conn Conn
}

func (d staticDialer) Dial(ctx context.Context, node string, options Options) (Conn, error) {
	return d.conn, nil
}

type errorConn struct {
	err error
}

func (c errorConn) RoundTrip(ctx context.Context, req protocol.Request) (protocol.Response, error) {
	return protocol.Response{}, c.err
}

func (c errorConn) Close() error {
	return nil
}

type routingDialer struct {
	servers map[string]*Server
	dials   map[string]*atomic.Int32
}

func (d routingDialer) Dial(ctx context.Context, node string, options Options) (Conn, error) {
	if counter := d.dials[node]; counter != nil {
		counter.Add(1)
	}
	server := d.servers[node]
	if server == nil {
		return nil, remote.ErrUnavailable
	}
	return memoryConn{server: server}, nil
}

type memoryConn struct {
	server *Server
}

func (c memoryConn) RoundTrip(ctx context.Context, req protocol.Request) (protocol.Response, error) {
	frame, err := protocol.EncodeRequest(req)
	if err != nil {
		return protocol.Response{}, err
	}
	frame, err = c.server.HandleFrame(ctx, frame)
	if err != nil {
		return protocol.Response{}, err
	}
	return protocol.DecodeResponse(frame)
}

func (c memoryConn) Close() error {
	return nil
}

type nonReusableConn struct {
	server *Server
	closes atomic.Int32
}

func (c *nonReusableConn) RoundTrip(ctx context.Context, req protocol.Request) (protocol.Response, error) {
	return memoryConn{server: c.server}.RoundTrip(ctx, req)
}

func (c *nonReusableConn) Close() error {
	c.closes.Add(1)
	return nil
}

func (c *nonReusableConn) Reusable() bool {
	return false
}

type dynamicDialer struct {
	mu     sync.Mutex
	server *Server
}

func (d *dynamicDialer) SetServer(server *Server) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.server = server
}

func (d *dynamicDialer) Dial(ctx context.Context, node string, options Options) (Conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.server == nil {
		return nil, remote.ErrUnavailable
	}
	return memoryConn{server: d.server}, nil
}
