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
	"errors"
	"io"
	"testing"

	"github.com/juicedata/juicefs/pkg/cache/remote/mock"
	"github.com/juicedata/juicefs/pkg/cache/remote/rdma"
	"github.com/stretchr/testify/require"
)

func TestRDMACacheServerCommandRegistered(t *testing.T) {
	app := NewApp()
	for _, cmd := range app.Commands {
		if cmd.Name == "rdma-cache-server" {
			require.NotEmpty(t, cmd.Flags)
			return
		}
	}
	t.Fatal("rdma-cache-server command is not registered")
}

func TestRDMACacheServerBackendDefaultsToMemory(t *testing.T) {
	backend, err := newRDMACacheBackend("", "8")
	require.NoError(t, err)
	_, ok := backend.(*mock.Client)
	require.True(t, ok)
}

func TestRDMACacheServerBackendUsesDiskCache(t *testing.T) {
	backend, err := newRDMACacheBackend(t.TempDir(), "8")
	require.NoError(t, err)
	defer backend.Close()

	require.NoError(t, backend.Put(context.Background(), "k", []byte("data")))
	r, err := backend.Get(context.Background(), "k", 0, -1)
	require.NoError(t, err)
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.Equal(t, []byte("data"), data)
}

func TestRDMACacheServerTransportHTTPCreatesHandler(t *testing.T) {
	handler, err := newRDMACacheServerHandler("http", mock.NewClient())
	require.NoError(t, err)
	require.NotNil(t, handler)
}

func TestRDMACacheServerTransportRDMAReturnsUnsupported(t *testing.T) {
	handler, err := newRDMACacheServerHandler("rdma", mock.NewClient())
	require.Nil(t, handler)
	require.True(t, errors.Is(err, rdma.ErrUnsupported))
}
