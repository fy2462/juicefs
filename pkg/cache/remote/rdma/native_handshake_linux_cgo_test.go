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
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/cache/remote/mock"
	"github.com/juicedata/juicefs/pkg/cache/remote/rdma/native"
	"github.com/juicedata/juicefs/pkg/cache/remote/rdma/protocol"
	"github.com/stretchr/testify/require"
)

func TestNativeEndpointHandshakeExchangesAndConnects(t *testing.T) {
	left := newFakeNativeResource(1)
	right := newFakeNativeResource(2)
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	errCh := make(chan error, 2)
	go func() {
		errCh <- clientNativeHandshake(context.Background(), clientConn, left)
	}()
	go func() {
		errCh <- serverNativeHandshake(context.Background(), serverConn, right)
	}()

	require.NoError(t, <-errCh)
	require.NoError(t, <-errCh)
	require.Equal(t, right.endpoint, left.remote.Load().(native.Endpoint))
	require.Equal(t, left.endpoint, right.remote.Load().(native.Endpoint))
	require.Equal(t, int32(1), left.connects.Load())
	require.Equal(t, int32(1), right.connects.Load())
}

func TestNativeDialerRunsEndpointHandshakeWhenResourcesExist(t *testing.T) {
	serverResource := newFakeNativeResource(2)
	clientResource := newFakeNativeResource(1)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	serverDone := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()
		serverDone <- serverNativeHandshake(context.Background(), conn, serverResource)
	}()

	dialer := &nativeDialer{options: nativeOptions{
		maxFrameBytes: defaultRDMAFrameBytes,
		resourceFactory: fakeNativeResourceFactory{
			resource: clientResource,
		},
	}}
	conn, err := dialer.Dial(context.Background(), ln.Addr().String(), Options{Timeout: time.Second})
	require.NoError(t, err)
	require.NoError(t, <-serverDone)
	require.Equal(t, serverResource.endpoint, clientResource.remote.Load().(native.Endpoint))
	require.Equal(t, clientResource.endpoint, serverResource.remote.Load().(native.Endpoint))
	require.Equal(t, int32(0), clientResource.closes.Load())
	require.NoError(t, conn.Close())
	require.Equal(t, int32(1), clientResource.closes.Load())
}

func TestServeNativeConnRunsEndpointHandshakeWhenResourcesExist(t *testing.T) {
	serverResource := newFakeNativeResource(2)
	clientResource := newFakeNativeResource(1)
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		serveNativeConnWithResourceFactory(context.Background(), serverConn, NewServer(mock.NewClient()), defaultRDMAFrameBytes, fakeNativeResourceFactory{resource: serverResource})
	}()

	require.NoError(t, clientNativeHandshake(context.Background(), clientConn, clientResource))
	require.Equal(t, int32(0), serverResource.closes.Load())
	payload, err := protocol.EncodeRequest(protocol.Request{Op: protocol.OpPing})
	require.NoError(t, err)
	frame, err := encodeFrame(payload, defaultRDMAFrameBytes)
	require.NoError(t, err)
	require.NoError(t, writeAll(clientConn, frame))
	respFrame, err := readFrame(clientConn, defaultRDMAFrameBytes)
	require.NoError(t, err)
	resp, err := protocol.DecodeResponse(respFrame)
	require.NoError(t, err)
	require.Equal(t, protocol.StatusOK, resp.Status)
	require.NoError(t, clientConn.Close())
	<-done
	require.Equal(t, serverResource.endpoint, clientResource.remote.Load().(native.Endpoint))
	require.Equal(t, clientResource.endpoint, serverResource.remote.Load().(native.Endpoint))
	require.Equal(t, int32(1), serverResource.closes.Load())
}

type fakeNativeResource struct {
	endpoint native.Endpoint
	remote   atomic.Value
	connects atomic.Int32
	closes   atomic.Int32
}

func newFakeNativeResource(id uint32) *fakeNativeResource {
	return &fakeNativeResource{endpoint: native.Endpoint{
		LID:   uint16(id),
		QPN:   id + 100,
		PSN:   id + 200,
		RKey:  id + 300,
		VAddr: uint64(id + 400),
		Port:  1,
	}}
}

func (r *fakeNativeResource) LocalEndpoint() (native.Endpoint, error) {
	return r.endpoint, nil
}

func (r *fakeNativeResource) Connect(endpoint native.Endpoint) error {
	r.remote.Store(endpoint)
	r.connects.Add(1)
	return nil
}

func (r *fakeNativeResource) Close() error {
	r.closes.Add(1)
	return nil
}

type fakeNativeResourceFactory struct {
	resource nativeResource
	err      error
}

func (f fakeNativeResourceFactory) New(deviceIndex, maxFrameBytes int) (nativeResource, error) {
	return f.resource, f.err
}
