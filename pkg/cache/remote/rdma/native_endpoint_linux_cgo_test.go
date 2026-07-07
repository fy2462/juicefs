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

package rdma

import (
	"testing"

	"github.com/juicedata/juicefs/pkg/cache/remote/rdma/native"
	"github.com/stretchr/testify/require"
)

func TestNativeEndpointWireRoundTrip(t *testing.T) {
	endpoint := native.Endpoint{
		LID:   12,
		QPN:   34,
		PSN:   56,
		RKey:  78,
		VAddr: 90,
		Port:  1,
	}

	frame, err := encodeNativeEndpoint(endpoint)
	require.NoError(t, err)
	decoded, err := decodeNativeEndpoint(frame)
	require.NoError(t, err)
	require.Equal(t, endpoint, decoded)
}

func TestNativeEndpointWireRejectsInvalidPayload(t *testing.T) {
	_, err := decodeNativeEndpoint([]byte("{"))

	require.Error(t, err)
}
