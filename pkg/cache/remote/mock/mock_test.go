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

package mock

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/juicedata/juicefs/pkg/cache/remote"
	"github.com/stretchr/testify/require"
)

func TestClientGetPutRangeAndDelete(t *testing.T) {
	c := NewClient()
	require.NoError(t, c.Put(context.Background(), "k", []byte("abcdef")))

	r, err := c.Get(context.Background(), "k", 2, 3)
	require.NoError(t, err)
	data, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	require.Equal(t, []byte("cde"), data)

	r, err = c.Get(context.Background(), "k", 0, -1)
	require.NoError(t, err)
	data, err = io.ReadAll(r)
	require.NoError(t, err)
	require.Equal(t, []byte("abcdef"), data)
	require.NoError(t, r.Close())

	require.NoError(t, c.Delete(context.Background(), "k"))
	_, err = c.Get(context.Background(), "k", 0, -1)
	require.True(t, errors.Is(err, remote.ErrMiss))
}

func TestClientPutCopiesData(t *testing.T) {
	c := NewClient()
	data := []byte("abc")
	require.NoError(t, c.Put(context.Background(), "k", data))
	data[0] = 'z'

	r, err := c.Get(context.Background(), "k", 0, -1)
	require.NoError(t, err)
	got, err := io.ReadAll(r)
	require.NoError(t, err)
	require.Equal(t, []byte("abc"), got)
	require.NoError(t, r.Close())
}

func TestClientCloseDisablesOperations(t *testing.T) {
	c := NewClient()
	require.NoError(t, c.Close())

	require.ErrorIs(t, c.Put(context.Background(), "k", []byte("v")), remote.ErrUnavailable)
	_, err := c.Get(context.Background(), "k", 0, -1)
	require.ErrorIs(t, err, remote.ErrUnavailable)
	require.ErrorIs(t, c.Delete(context.Background(), "k"), remote.ErrUnavailable)
}
