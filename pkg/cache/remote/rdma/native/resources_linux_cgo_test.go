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

func TestNewResourcesRejectsUnavailableDeviceIndex(t *testing.T) {
	resources, err := NewResources(1<<20, 0)

	require.Nil(t, resources)
	require.ErrorIs(t, err, ErrNoDevice)
}

func TestNewResourcesAllocatesVerbsObjectsWhenDeviceExists(t *testing.T) {
	count, err := DeviceCount()
	require.NoError(t, err)
	if count == 0 {
		t.Skip("no RDMA devices available")
	}

	resources, err := NewResources(0, 128<<10)
	require.NoError(t, err)
	require.NotNil(t, resources)
	require.NotNil(t, resources.context)
	require.NotNil(t, resources.protectionDomain)
	require.NotNil(t, resources.completionQueue)
	require.NotNil(t, resources.memoryRegion)
	require.NotNil(t, resources.buffer)
	require.NotNil(t, resources.sendBuffer)
	require.Len(t, resources.buffer, 128<<10)
	require.Len(t, resources.sendBuffer, 128<<10)
	require.NoError(t, resources.Close())
	require.NoError(t, resources.Close())
}

func TestNewResourcesCreatesQueuePairAndEndpointWhenDeviceExists(t *testing.T) {
	count, err := DeviceCount()
	require.NoError(t, err)
	if count == 0 {
		t.Skip("no RDMA devices available")
	}

	resources, err := NewResources(0, 128<<10)
	require.NoError(t, err)
	defer resources.Close()

	require.NotNil(t, resources.queuePair)
	endpoint, err := resources.LocalEndpoint()
	require.NoError(t, err)
	require.NotZero(t, endpoint.QPN)
	require.NotZero(t, endpoint.PSN)
	require.NotZero(t, endpoint.RKey)
	require.NotZero(t, endpoint.VAddr)
}

func TestConnectRejectsInvalidEndpoint(t *testing.T) {
	resources := &Resources{}

	require.ErrorIs(t, resources.Connect(Endpoint{}), ErrInvalidEndpoint)
}

func TestConnectMovesQueuePairsToRTSWhenDeviceExists(t *testing.T) {
	count, err := DeviceCount()
	require.NoError(t, err)
	if count == 0 {
		t.Skip("no RDMA devices available")
	}

	left, err := NewResources(0, 128<<10)
	require.NoError(t, err)
	defer left.Close()
	right, err := NewResources(0, 128<<10)
	require.NoError(t, err)
	defer right.Close()

	leftEndpoint, err := left.LocalEndpoint()
	require.NoError(t, err)
	rightEndpoint, err := right.LocalEndpoint()
	require.NoError(t, err)
	require.NoError(t, left.Connect(rightEndpoint))
	require.NoError(t, right.Connect(leftEndpoint))
}

func TestSendRejectsOversizedPayload(t *testing.T) {
	resources := &Resources{buffer: make([]byte, 4), maxFrameBytes: 4}

	require.ErrorIs(t, resources.PostSend([]byte("too-large")), ErrFrameTooLarge)
}

func TestPostSendDoesNotOverwriteReceiveBuffer(t *testing.T) {
	resources := &Resources{
		buffer:        []byte("recv-data"),
		sendBuffer:    make([]byte, 16),
		maxFrameBytes: 16,
	}

	require.ErrorIs(t, resources.PostSend([]byte("send-data")), ErrNoDevice)
	require.Equal(t, []byte("recv-data"), resources.Buffer())
	require.Equal(t, []byte("send-data"), resources.sendBuffer[:len("send-data")])
}

func TestSendReceiveCompletesWhenDeviceExists(t *testing.T) {
	count, err := DeviceCount()
	require.NoError(t, err)
	if count == 0 {
		t.Skip("no RDMA devices available")
	}

	left, err := NewResources(0, 128<<10)
	require.NoError(t, err)
	defer left.Close()
	right, err := NewResources(0, 128<<10)
	require.NoError(t, err)
	defer right.Close()

	leftEndpoint, err := left.LocalEndpoint()
	require.NoError(t, err)
	rightEndpoint, err := right.LocalEndpoint()
	require.NoError(t, err)
	require.NoError(t, left.Connect(rightEndpoint))
	require.NoError(t, right.Connect(leftEndpoint))

	require.NoError(t, right.PostRecv())
	require.NoError(t, left.PostSend([]byte("verbs-payload")))
	leftBytes, err := left.PollCompletion()
	require.NoError(t, err)
	require.Zero(t, leftBytes)
	rightBytes, err := right.PollCompletion()
	require.NoError(t, err)
	require.Equal(t, len("verbs-payload"), rightBytes)
	require.Equal(t, []byte("verbs-payload"), right.Buffer()[:len("verbs-payload")])
}
