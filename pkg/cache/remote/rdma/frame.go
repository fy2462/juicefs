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
	"encoding/binary"
	"fmt"
	"io"

	"github.com/juicedata/juicefs/pkg/cache/remote"
)

const (
	minRDMAFrameBytes     = 64 << 10
	defaultRDMAFrameBytes = 4 << 20
)

func encodeFrame(payload []byte, limit int) ([]byte, error) {
	limit = maxFrameBytes(limit)
	if len(payload) > limit {
		return nil, fmt.Errorf("%w: rdma frame payload %d exceeds limit %d", remote.ErrUnavailable, len(payload), limit)
	}
	frame := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(payload)))
	copy(frame[4:], payload)
	return frame, nil
}

func readFrame(r io.Reader, limit int) ([]byte, error) {
	limit = maxFrameBytes(limit)
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	size := int(binary.BigEndian.Uint32(header[:]))
	if size > limit {
		return nil, fmt.Errorf("%w: rdma frame payload %d exceeds limit %d", remote.ErrUnavailable, size, limit)
	}
	payload := make([]byte, size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func maxFrameBytes(value int) int {
	if value <= 0 {
		return defaultRDMAFrameBytes
	}
	if value < minRDMAFrameBytes {
		return minRDMAFrameBytes
	}
	return value
}
