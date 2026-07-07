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

package native

/*
#cgo LDFLAGS: -libverbs
#include <infiniband/verbs.h>
*/
import "C"

const (
	minFrameBytes     = 64 << 10
	defaultFrameBytes = 4 << 20
)

type Resources struct {
	deviceIndex   int
	maxFrameBytes int
}

func NewResources(deviceIndex, maxFrameBytes int) (*Resources, error) {
	if deviceIndex < 0 {
		return nil, ErrInvalidDeviceIndex
	}
	return &Resources{
		deviceIndex:   deviceIndex,
		maxFrameBytes: frameLimit(maxFrameBytes),
	}, nil
}

func (r *Resources) Close() error {
	return nil
}

func frameLimit(value int) int {
	if value <= 0 {
		return defaultFrameBytes
	}
	if value < minFrameBytes {
		return minFrameBytes
	}
	return value
}
