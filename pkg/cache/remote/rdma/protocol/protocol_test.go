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
	"testing"

	"github.com/juicedata/juicefs/pkg/cache/remote"
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
