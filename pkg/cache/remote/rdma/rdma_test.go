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
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/cache/remote/mock"
	"github.com/juicedata/juicefs/pkg/cache/remote/rdma/protocol"
	"github.com/stretchr/testify/require"
)

func TestNewClientReturnsUnsupportedByDefault(t *testing.T) {
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

func TestServerHandleFrameBadRequest(t *testing.T) {
	frame, err := NewServer(mock.NewClient()).HandleFrame(context.Background(), []byte("{"))
	require.NoError(t, err)
	resp, err := protocol.DecodeResponse(frame)
	require.NoError(t, err)
	require.Equal(t, protocol.StatusBadRequest, resp.Status)
}
