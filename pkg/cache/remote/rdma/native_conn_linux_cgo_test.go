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
	"net"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/cache/remote/mock"
	"github.com/juicedata/juicefs/pkg/cache/remote/rdma/native"
	"github.com/juicedata/juicefs/pkg/cache/remote/rdma/protocol"
	"github.com/stretchr/testify/require"
)

func TestNewClientUsesNativeDialerWhenRDMAEnabled(t *testing.T) {
	client := NewClient(Options{Nodes: []string{"127.0.0.1:9568"}})
	defer client.Close()

	rdmaClient, ok := client.(*Client)
	require.True(t, ok)
	_, ok = rdmaClient.options.Dialer.(*nativeDialer)
	require.True(t, ok)
}

func TestNewClientPreservesExplicitDialerWhenRDMAEnabled(t *testing.T) {
	explicit := memoryDialer{server: NewServer(mock.NewClient())}
	client := NewClient(Options{Nodes: []string{"node-a"}, Dialer: explicit})
	defer client.Close()

	rdmaClient, ok := client.(*Client)
	require.True(t, ok)
	require.Equal(t, explicit, rdmaClient.options.Dialer)
}

func TestNativeDialerRoundTripPing(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reqFrame, err := readFrame(conn, defaultRDMAFrameBytes)
		if err != nil {
			return
		}
		respFrame, err := NewServer(mock.NewClient()).HandleFrame(context.Background(), reqFrame)
		if err != nil {
			return
		}
		encoded, err := encodeFrame(respFrame, defaultRDMAFrameBytes)
		if err != nil {
			return
		}
		_, _ = conn.Write(encoded)
	}()

	dialer := &nativeDialer{options: nativeOptions{maxFrameBytes: defaultRDMAFrameBytes}}
	conn, err := dialer.Dial(context.Background(), ln.Addr().String(), Options{Timeout: time.Second})
	require.NoError(t, err)
	defer conn.Close()

	resp, err := conn.RoundTrip(context.Background(), protocol.Request{Op: protocol.OpPing})
	require.NoError(t, err)
	require.Equal(t, protocol.StatusOK, resp.Status)
	<-done
}

func TestNativeOptionsFromEnv(t *testing.T) {
	t.Setenv("JFS_RDMA_DEVICE_INDEX", "2")
	t.Setenv("JFS_RDMA_PORT_NUM", "3")
	t.Setenv("JFS_RDMA_MAX_FRAME_BYTES", "131072")
	t.Setenv("JFS_RDMA_CQ_TIMEOUT", "25ms")
	t.Setenv("JFS_RDMA_REQUIRE_DEVICE", "true")

	options, err := nativeOptionsFromEnv()
	require.NoError(t, err)
	require.Equal(t, 2, options.deviceIndex)
	require.Equal(t, uint8(3), options.portNum)
	require.Equal(t, 131072, options.maxFrameBytes)
	require.Equal(t, 25*time.Millisecond, options.cqTimeout)
	require.True(t, options.requireDevice)
}

func TestNativeDialerRequiresDeviceWhenConfigured(t *testing.T) {
	dialer := &nativeDialer{options: nativeOptions{
		deviceIndex:   1 << 20,
		maxFrameBytes: defaultRDMAFrameBytes,
		requireDevice: true,
	}}

	conn, err := dialer.Dial(context.Background(), "127.0.0.1:1", Options{Timeout: time.Millisecond})
	require.Nil(t, conn)
	require.True(t, errors.Is(err, native.ErrNoDevice))
}

func TestNativeDialerFallsBackWhenDeviceIsUnavailable(t *testing.T) {
	resources, err := native.NewResources(1<<20, defaultRDMAFrameBytes)
	require.Nil(t, resources)
	require.True(t, errors.Is(err, native.ErrNoDevice), "unexpected resource error: %v", err)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			accepted <- conn
		}
	}()

	dialer := &nativeDialer{options: nativeOptions{
		deviceIndex:   1 << 20,
		maxFrameBytes: defaultRDMAFrameBytes,
	}}
	conn, err := dialer.Dial(context.Background(), ln.Addr().String(), Options{Timeout: time.Second})
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	(<-accepted).Close()
}
