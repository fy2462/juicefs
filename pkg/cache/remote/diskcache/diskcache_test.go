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

package diskcache

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/cache/remote"
	"github.com/stretchr/testify/require"
)

func TestClientGetPutRangeDelete(t *testing.T) {
	client, err := NewClient(Options{Dir: t.TempDir(), Capacity: 8})
	require.NoError(t, err)

	require.NoError(t, client.Put(context.Background(), "a/b", []byte("abcdef")))
	r, err := client.Get(context.Background(), "a/b", 2, 3)
	require.NoError(t, err)
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.Equal(t, []byte("cde"), data)

	require.NoError(t, client.Delete(context.Background(), "a/b"))
	_, err = client.Get(context.Background(), "a/b", 0, -1)
	require.True(t, errors.Is(err, remote.ErrMiss))
}

func TestClientPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	client, err := NewClient(Options{Dir: dir, Capacity: 8})
	require.NoError(t, err)
	require.NoError(t, client.Put(context.Background(), "k", []byte("data")))
	require.NoError(t, client.Close())

	client, err = NewClient(Options{Dir: dir, Capacity: 8})
	require.NoError(t, err)
	r, err := client.Get(context.Background(), "k", 0, -1)
	require.NoError(t, err)
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.Equal(t, []byte("data"), data)
}

func TestClientEvictsLeastRecentlyUsed(t *testing.T) {
	client, err := NewClient(Options{Dir: t.TempDir(), Capacity: 6})
	require.NoError(t, err)

	require.NoError(t, client.Put(context.Background(), "a", []byte("abc")))
	time.Sleep(time.Millisecond)
	require.NoError(t, client.Put(context.Background(), "b", []byte("def")))
	time.Sleep(time.Millisecond)
	r, err := client.Get(context.Background(), "a", 0, -1)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	time.Sleep(time.Millisecond)
	require.NoError(t, client.Put(context.Background(), "c", []byte("ghi")))

	_, err = client.Get(context.Background(), "b", 0, -1)
	require.True(t, errors.Is(err, remote.ErrMiss))
	r, err = client.Get(context.Background(), "a", 0, -1)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	r, err = client.Get(context.Background(), "c", 0, -1)
	require.NoError(t, err)
	require.NoError(t, r.Close())
}

func TestClientRejectsOversizedPut(t *testing.T) {
	client, err := NewClient(Options{Dir: t.TempDir(), Capacity: 3})
	require.NoError(t, err)

	err = client.Put(context.Background(), "k", []byte("data"))
	require.True(t, errors.Is(err, remote.ErrUnavailable))
}
