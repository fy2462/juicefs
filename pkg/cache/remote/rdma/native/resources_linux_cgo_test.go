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

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewResourcesRejectsNegativeDeviceIndex(t *testing.T) {
	resources, err := NewResources(-1, 0)

	require.Nil(t, resources)
	require.ErrorIs(t, err, ErrInvalidDeviceIndex)
}

func TestFrameLimitDefaultsAndMinimum(t *testing.T) {
	require.Equal(t, defaultFrameBytes, frameLimit(0))
	require.Equal(t, minFrameBytes, frameLimit(1))
	require.Equal(t, 128<<10, frameLimit(128<<10))
}
