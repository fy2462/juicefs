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

package httpcache

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/cache/remote"
	"github.com/juicedata/juicefs/pkg/cache/remote/mock"
	"github.com/stretchr/testify/require"
)

func TestClientGetPutRangeAndDelete(t *testing.T) {
	backend := mock.NewClient()
	server := httptest.NewServer(NewHandler(backend))
	defer server.Close()
	client := NewClient([]string{server.URL}, time.Second)

	key := "subdir/block/1"
	require.NoError(t, client.Put(context.Background(), key, []byte("abcdef")))

	r, err := client.Get(context.Background(), key, 2, 3)
	require.NoError(t, err)
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.Equal(t, []byte("cde"), data)

	require.NoError(t, client.Delete(context.Background(), key))
	_, err = client.Get(context.Background(), key, 0, -1)
	require.True(t, errors.Is(err, remote.ErrMiss))
}

func TestClientMapsServerAndNetworkErrors(t *testing.T) {
	backend := mock.NewClient()
	require.NoError(t, backend.Close())
	server := httptest.NewServer(NewHandler(backend))
	defer server.Close()
	client := NewClient([]string{server.URL}, time.Second)

	require.ErrorIs(t, client.Put(context.Background(), "k", []byte("v")), remote.ErrUnavailable)
	_, err := client.Get(context.Background(), "k", 0, -1)
	require.ErrorIs(t, err, remote.ErrUnavailable)

	closed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	node := closed.URL
	closed.Close()
	client = NewClient([]string{node}, time.Second)
	_, err = client.Get(context.Background(), "k", 0, -1)
	require.ErrorIs(t, err, remote.ErrUnavailable)
}

func TestClientWithoutNodesIsDisabled(t *testing.T) {
	client := NewClient(nil, time.Second)

	require.ErrorIs(t, client.Put(context.Background(), "k", []byte("v")), remote.ErrDisabled)
	_, err := client.Get(context.Background(), "k", 0, -1)
	require.ErrorIs(t, err, remote.ErrDisabled)
	require.ErrorIs(t, client.Delete(context.Background(), "k"), remote.ErrDisabled)
}

func TestClientGetFallsBackAcrossReplicas(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer failing.Close()
	backend := mock.NewClient()
	require.NoError(t, backend.Put(context.Background(), "k", []byte("value")))
	healthy := httptest.NewServer(NewHandler(backend))
	defer healthy.Close()

	client := NewClientWithOptions(Options{
		Nodes:    []string{failing.URL, healthy.URL},
		Timeout:  time.Second,
		Replicas: 2,
	})
	r, err := client.Get(context.Background(), "k", 0, -1)
	require.NoError(t, err)
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.Equal(t, []byte("value"), data)
}

func TestClientPutReplicatesToConfiguredReplicas(t *testing.T) {
	var puts atomic.Int32
	servers := make([]*httptest.Server, 0, 3)
	for i := 0; i < 3; i++ {
		backend := mock.NewClient()
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPut {
				puts.Add(1)
			}
			NewHandler(backend).ServeHTTP(w, r)
		}))
		defer server.Close()
		servers = append(servers, server)
	}

	client := NewClientWithOptions(Options{
		Nodes:    []string{servers[0].URL, servers[1].URL, servers[2].URL},
		Timeout:  time.Second,
		Replicas: 2,
	})
	require.NoError(t, client.Put(context.Background(), "k", []byte("value")))
	require.Equal(t, int32(2), puts.Load())
}
