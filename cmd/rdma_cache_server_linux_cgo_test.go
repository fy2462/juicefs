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

package cmd

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/cache/remote/mock"
	"github.com/juicedata/juicefs/pkg/cache/remote/rdma"
	"github.com/stretchr/testify/require"
)

func TestRDMACacheServerTransportRDMAUsesNativeServer(t *testing.T) {
	listen := reserveRDMACacheServerAddress(t)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- runRDMACacheServer(ctx, listen, "rdma", mock.NewClient())
	}()

	client := rdma.NewClient(rdma.Options{
		Nodes:   []string{listen},
		Timeout: 10 * time.Millisecond,
	})
	require.Eventually(t, func() bool {
		return client.Put(context.Background(), "k", []byte("data")) == nil
	}, time.Second, 10*time.Millisecond)
	defer client.Close()

	r, err := client.Get(context.Background(), "k", 0, -1)
	require.NoError(t, err)
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.Equal(t, []byte("data"), data)

	cancel()
	require.Eventually(t, func() bool {
		select {
		case err := <-errCh:
			return err == nil
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
}

func reserveRDMACacheServerAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	return listener.Addr().String()
}
