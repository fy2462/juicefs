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

	"github.com/juicedata/juicefs/pkg/cache/remote"
	"github.com/juicedata/juicefs/pkg/cache/remote/mock"
	"github.com/juicedata/juicefs/pkg/cache/remote/rdma/protocol"
	"github.com/stretchr/testify/require"
)

func TestListenAndServeNativeRoundTrip(t *testing.T) {
	listen := reserveLocalAddress(t)
	backend := mock.NewClient()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- ListenAndServe(ctx, ServeOptions{
			Listen:  listen,
			Backend: backend,
		})
	}()

	conn := dialNativeServer(t, listen)
	defer conn.Close()

	resp, err := conn.RoundTrip(context.Background(), protocol.Request{Op: protocol.OpPing})
	require.NoError(t, err)
	require.Equal(t, protocol.StatusOK, resp.Status)

	resp, err = conn.RoundTrip(context.Background(), protocol.Request{
		Op:      protocol.OpPut,
		Key:     "k",
		Payload: []byte("native-data"),
	})
	require.NoError(t, err)
	require.Equal(t, protocol.StatusOK, resp.Status)

	resp, err = conn.RoundTrip(context.Background(), protocol.Request{
		Op:   protocol.OpGet,
		Key:  "k",
		Size: -1,
	})
	require.NoError(t, err)
	require.Equal(t, protocol.StatusOK, resp.Status)
	require.Equal(t, []byte("native-data"), resp.Payload)

	resp, err = conn.RoundTrip(context.Background(), protocol.Request{Op: protocol.OpDelete, Key: "k"})
	require.NoError(t, err)
	require.Equal(t, protocol.StatusOK, resp.Status)
	_, err = backend.Get(context.Background(), "k", 0, -1)
	require.True(t, errors.Is(err, remote.ErrMiss))

	cancel()
	require.Eventually(t, func() bool {
		select {
		case err := <-errCh:
			return err == nil || errors.Is(err, context.Canceled)
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
}

func dialNativeServer(t *testing.T, listen string) Conn {
	t.Helper()
	dialer := &nativeDialer{options: nativeOptions{maxFrameBytes: defaultRDMAFrameBytes}}
	var conn Conn
	require.Eventually(t, func() bool {
		var err error
		conn, err = dialer.Dial(context.Background(), listen, Options{Timeout: 10 * time.Millisecond})
		return err == nil
	}, time.Second, 10*time.Millisecond)
	return conn
}

func reserveLocalAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	return listener.Addr().String()
}
