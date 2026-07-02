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

package protocol

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/juicedata/juicefs/pkg/cache/remote"
	"github.com/juicedata/juicefs/pkg/cache/remote/mock"
	"github.com/stretchr/testify/require"
)

func TestRequestRoundTrip(t *testing.T) {
	req := Request{Op: OpGet, Key: "chunks/0/0/1_0_4", Off: 1, Size: 2}
	data, err := EncodeRequest(req)
	require.NoError(t, err)
	got, err := DecodeRequest(data)
	require.NoError(t, err)
	require.Equal(t, req, got)
}

func TestResponseRoundTripWithPayload(t *testing.T) {
	resp := Response{Status: StatusOK, Payload: []byte("data")}
	data, err := EncodeResponse(resp)
	require.NoError(t, err)
	got, err := DecodeResponse(data)
	require.NoError(t, err)
	require.Equal(t, resp, got)
}

func TestStatusToError(t *testing.T) {
	require.NoError(t, StatusToError(StatusOK))
	require.ErrorIs(t, StatusToError(StatusMiss), remote.ErrMiss)
	require.ErrorIs(t, StatusToError(StatusUnavailable), remote.ErrUnavailable)
	require.ErrorIs(t, StatusToError(StatusBadRequest), remote.ErrUnavailable)
}

func TestExecutorGetHitRange(t *testing.T) {
	backend := mock.NewClient()
	require.NoError(t, backend.Put(context.Background(), "k", []byte("abcdef")))

	resp := Executor{Backend: backend}.Handle(context.Background(), Request{
		Op:   OpGet,
		Key:  "k",
		Off:  2,
		Size: 3,
	})

	require.Equal(t, StatusOK, resp.Status)
	require.Equal(t, []byte("cde"), resp.Payload)
}

func TestExecutorGetMiss(t *testing.T) {
	resp := Executor{Backend: mock.NewClient()}.Handle(context.Background(), Request{
		Op:   OpGet,
		Key:  "missing",
		Size: -1,
	})

	require.Equal(t, StatusMiss, resp.Status)
}

func TestExecutorPutThenGet(t *testing.T) {
	backend := mock.NewClient()
	executor := Executor{Backend: backend}

	resp := executor.Handle(context.Background(), Request{Op: OpPut, Key: "k", Payload: []byte("data")})
	require.Equal(t, StatusOK, resp.Status)
	resp = executor.Handle(context.Background(), Request{Op: OpGet, Key: "k", Size: -1})
	require.Equal(t, StatusOK, resp.Status)
	require.Equal(t, []byte("data"), resp.Payload)
}

func TestExecutorDelete(t *testing.T) {
	backend := mock.NewClient()
	require.NoError(t, backend.Put(context.Background(), "k", []byte("data")))
	executor := Executor{Backend: backend}

	resp := executor.Handle(context.Background(), Request{Op: OpDelete, Key: "k"})
	require.Equal(t, StatusOK, resp.Status)
	_, err := backend.Get(context.Background(), "k", 0, -1)
	require.True(t, errors.Is(err, remote.ErrMiss))
}

func TestExecutorBadRequest(t *testing.T) {
	resp := Executor{Backend: mock.NewClient()}.Handle(context.Background(), Request{Op: Op("BAD"), Key: "k"})

	require.Equal(t, StatusBadRequest, resp.Status)
}

func TestExecutorBackendUnavailable(t *testing.T) {
	backend := mock.NewClient()
	require.NoError(t, backend.Close())

	resp := Executor{Backend: backend}.Handle(context.Background(), Request{Op: OpGet, Key: "k", Size: -1})

	require.Equal(t, StatusUnavailable, resp.Status)
}

type badReaderBackend struct {
	remote.Client
}

func (b badReaderBackend) Get(ctx context.Context, key string, off, size int) (io.ReadCloser, error) {
	return io.NopCloser(errReader{}), nil
}

type errReader struct{}

func (errReader) Read(p []byte) (int, error) {
	return 0, errors.New("read failed")
}
