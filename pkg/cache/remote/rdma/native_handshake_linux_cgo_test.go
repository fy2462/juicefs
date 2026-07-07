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
	"io"
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
		serveNativeConnWithResourceFactory(context.Background(), serverConn, NewServer(mock.NewClient()), defaultRDMAFrameBytes, fakeNativeResourceFactory{resource: serverResource}, false, 0, 1)
	}()

	require.NoError(t, clientNativeHandshake(context.Background(), clientConn, clientResource))
	<-done
	require.Equal(t, serverResource.endpoint, clientResource.remote.Load().(native.Endpoint))
	require.Equal(t, clientResource.endpoint, serverResource.remote.Load().(native.Endpoint))
	require.Equal(t, int32(1), serverResource.closes.Load())
}

func TestServeNativeConnUsesResourceFrameExchange(t *testing.T) {
	serverResource := newFakeNativeResource(2)
	clientResource := newFakeNativeResource(1)
	serverResource.queueReceived(encodedPingFrame(t))
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		serveNativeConnWithResourceFactory(context.Background(), serverConn, NewServer(mock.NewClient()), defaultRDMAFrameBytes, fakeNativeResourceFactory{resource: serverResource}, false, 0, 1)
	}()

	require.NoError(t, clientNativeHandshake(context.Background(), clientConn, clientResource))
	<-done
	require.Equal(t, int32(2), serverResource.postRecvs.Load())
	require.Equal(t, int32(1), serverResource.postSends.Load())
	require.Equal(t, int32(3), serverResource.polls.Load())
	require.Len(t, serverResource.sentPayloads, 1)
	respPayload, err := readPayloadFromEncodedFrame(serverResource.sentPayloads[0])
	require.NoError(t, err)
	resp, err := protocol.DecodeResponse(respPayload)
	require.NoError(t, err)
	require.Equal(t, protocol.StatusOK, resp.Status)
}

func TestServeNativeConnHandlesMultipleResourceFrames(t *testing.T) {
	serverResource := newFakeNativeResource(2)
	clientResource := newFakeNativeResource(1)
	serverResource.queueReceived(encodedPingFrame(t))
	serverResource.queueReceived(encodedPingFrame(t))
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		serveNativeConnWithResourceFactory(context.Background(), serverConn, NewServer(mock.NewClient()), defaultRDMAFrameBytes, fakeNativeResourceFactory{resource: serverResource}, false, 0, 1)
	}()

	require.NoError(t, clientNativeHandshake(context.Background(), clientConn, clientResource))
	<-done
	require.Equal(t, int32(3), serverResource.postRecvs.Load())
	require.Equal(t, int32(2), serverResource.postSends.Load())
	require.Equal(t, int32(5), serverResource.polls.Load())
	require.Len(t, serverResource.sentPayloads, 2)
}

func TestServeNativeConnRequiresDeviceWhenConfigured(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		serveNativeConnWithResourceFactory(context.Background(), serverConn, NewServer(mock.NewClient()), defaultRDMAFrameBytes, fakeNativeResourceFactory{
			err: native.ErrNoDevice,
		}, true, 0, 1)
	}()

	payload, err := protocol.EncodeRequest(protocol.Request{Op: protocol.OpPing})
	require.NoError(t, err)
	frame, err := encodeFrame(payload, defaultRDMAFrameBytes)
	require.NoError(t, err)
	require.Error(t, writeAll(clientConn, frame))
	<-done
}

func TestServeNativeConnUsesConfiguredDeviceIndex(t *testing.T) {
	serverResource := newFakeNativeResource(2)
	clientResource := newFakeNativeResource(1)
	factory := &recordingNativeResourceFactory{resource: serverResource}
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		serveNativeConnWithResourceFactory(context.Background(), serverConn, NewServer(mock.NewClient()), defaultRDMAFrameBytes, factory, false, 7, 3)
	}()

	require.NoError(t, clientNativeHandshake(context.Background(), clientConn, clientResource))
	<-done
	require.Equal(t, int32(7), factory.deviceIndex.Load())
	require.Equal(t, uint32(3), factory.portNum.Load())
}

func TestNativeConnRoundTripUsesResourceFrameExchange(t *testing.T) {
	resource := newFakeNativeResource(1)
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	resource.onSend = func(payload []byte) {
		reqPayload, err := readPayloadFromEncodedFrame(payload)
		require.NoError(t, err)
		respPayload, err := NewServer(mock.NewClient()).HandleFrame(context.Background(), reqPayload)
		require.NoError(t, err)
		respFrame, err := encodeFrame(respPayload, defaultRDMAFrameBytes)
		require.NoError(t, err)
		resource.queueReceived(respFrame)
	}
	conn := &nativeConn{
		conn:          clientConn,
		resources:     resource,
		maxFrameBytes: defaultRDMAFrameBytes,
	}

	resp, err := conn.RoundTrip(context.Background(), protocol.Request{Op: protocol.OpPing})
	require.NoError(t, err)
	require.Equal(t, protocol.StatusOK, resp.Status)
	require.Equal(t, int32(1), resource.postRecvs.Load())
	require.Equal(t, int32(1), resource.postSends.Load())
	require.Equal(t, int32(2), resource.polls.Load())
}

type fakeNativeResource struct {
	endpoint     native.Endpoint
	remote       atomic.Value
	connects     atomic.Int32
	closes       atomic.Int32
	postRecvs    atomic.Int32
	postSends    atomic.Int32
	polls        atomic.Int32
	buffer       []byte
	recvSizes    []int
	sentPayloads [][]byte
	onSend       func([]byte)
}

func newFakeNativeResource(id uint32) *fakeNativeResource {
	return &fakeNativeResource{endpoint: native.Endpoint{
		LID:   uint16(id),
		QPN:   id + 100,
		PSN:   id + 200,
		RKey:  id + 300,
		VAddr: uint64(id + 400),
		Port:  1,
	}, buffer: make([]byte, defaultRDMAFrameBytes)}
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

func (r *fakeNativeResource) Buffer() []byte {
	return r.buffer
}

func (r *fakeNativeResource) PostRecv() error {
	r.postRecvs.Add(1)
	return nil
}

func (r *fakeNativeResource) PostSend(payload []byte) error {
	r.postSends.Add(1)
	r.recvSizes = append([]int{0}, r.recvSizes...)
	data := make([]byte, len(payload))
	copy(data, payload)
	r.sentPayloads = append(r.sentPayloads, data)
	if r.onSend != nil {
		r.onSend(data)
	}
	return nil
}

func (r *fakeNativeResource) PollCompletion() (int, error) {
	r.polls.Add(1)
	if len(r.recvSizes) == 0 {
		return 0, errors.New("no fake RDMA completion")
	}
	size := r.recvSizes[0]
	r.recvSizes = r.recvSizes[1:]
	return size, nil
}

func (r *fakeNativeResource) queueReceived(payload []byte) {
	copy(r.buffer, payload)
	r.recvSizes = append(r.recvSizes, len(payload))
}

type fakeNativeResourceFactory struct {
	resource nativeResource
	err      error
}

func (f fakeNativeResourceFactory) New(deviceIndex int, portNum uint8, maxFrameBytes int) (nativeResource, error) {
	return f.resource, f.err
}

type recordingNativeResourceFactory struct {
	resource    nativeResource
	deviceIndex atomic.Int32
	portNum     atomic.Uint32
}

func (f *recordingNativeResourceFactory) New(deviceIndex int, portNum uint8, maxFrameBytes int) (nativeResource, error) {
	f.deviceIndex.Store(int32(deviceIndex))
	f.portNum.Store(uint32(portNum))
	return f.resource, nil
}

func readPayloadFromEncodedFrame(frame []byte) ([]byte, error) {
	conn := bytesConn{data: frame}
	return readFrame(&conn, defaultRDMAFrameBytes)
}

func encodedPingFrame(t *testing.T) []byte {
	t.Helper()
	reqPayload, err := protocol.EncodeRequest(protocol.Request{Op: protocol.OpPing})
	require.NoError(t, err)
	reqFrame, err := encodeFrame(reqPayload, defaultRDMAFrameBytes)
	require.NoError(t, err)
	return reqFrame
}

type bytesConn struct {
	data []byte
}

func (c *bytesConn) Read(p []byte) (int, error) {
	if len(c.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, c.data)
	c.data = c.data[n:]
	return n, nil
}
