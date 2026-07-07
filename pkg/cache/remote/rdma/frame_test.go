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
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"

	"github.com/juicedata/juicefs/pkg/cache/remote"
	"github.com/juicedata/juicefs/pkg/cache/remote/rdma/protocol"
	"github.com/stretchr/testify/require"
)

func TestFrameCodecRoundTripsProtocolRequest(t *testing.T) {
	payload, err := protocol.EncodeRequest(protocol.Request{Op: protocol.OpPing})
	require.NoError(t, err)

	frame, err := encodeFrame(payload, 64<<10)
	require.NoError(t, err)
	require.Equal(t, uint32(len(payload)), binary.BigEndian.Uint32(frame[:4]))

	got, err := readFrame(bytes.NewReader(frame), 64<<10)
	require.NoError(t, err)
	req, err := protocol.DecodeRequest(got)
	require.NoError(t, err)
	require.Equal(t, protocol.OpPing, req.Op)
}

func TestFrameCodecRejectsOversizedPayload(t *testing.T) {
	_, err := encodeFrame(bytes.Repeat([]byte("x"), 65<<10), 64<<10)
	require.ErrorIs(t, err, remote.ErrUnavailable)

	var length [4]byte
	binary.BigEndian.PutUint32(length[:], 65<<10)
	_, err = readFrame(bytes.NewReader(length[:]), 64<<10)
	require.ErrorIs(t, err, remote.ErrUnavailable)
}

func TestFrameCodecShortRead(t *testing.T) {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], 4)
	_, err := readFrame(io.MultiReader(bytes.NewReader(length[:]), bytes.NewReader([]byte("ab"))), 64<<10)
	require.True(t, errors.Is(err, io.ErrUnexpectedEOF))
}

func TestMaxFrameBytesDefaultsAndMinimum(t *testing.T) {
	require.Equal(t, defaultRDMAFrameBytes, maxFrameBytes(0))
	require.Equal(t, minRDMAFrameBytes, maxFrameBytes(1))
	require.Equal(t, 128<<10, maxFrameBytes(128<<10))
}
