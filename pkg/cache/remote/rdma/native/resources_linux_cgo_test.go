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
	"unsafe"

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

func TestRandomPSNUsesZeroForOpenRDMAMock(t *testing.T) {
	t.Setenv("OPEN_RDMA_DRIVER", "/tmp/open-rdma-driver")

	psn, err := randomPSN()
	require.NoError(t, err)
	require.Zero(t, psn)
}

func TestOpenRDMAMockIPv4DefaultsToExampleAddress(t *testing.T) {
	t.Setenv("OPEN_RDMA_DRIVER", "/tmp/open-rdma-driver")
	t.Setenv("JFS_RDMA_GRH_IPV4", "")

	ip, ok := rdmaGRHIPv4()

	require.True(t, ok)
	require.Equal(t, uint32(0x1122330a), ip)
}

func TestOpenRDMAMockIPv4CanBeOverridden(t *testing.T) {
	t.Setenv("OPEN_RDMA_DRIVER", "/tmp/open-rdma-driver")
	t.Setenv("JFS_RDMA_GRH_IPV4", "10.1.2.3")

	ip, ok := rdmaGRHIPv4()

	require.True(t, ok)
	require.Equal(t, uint32(0x0a010203), ip)
}

func TestOpenRDMAMockDLIDMatchesExample(t *testing.T) {
	t.Setenv("OPEN_RDMA_DRIVER", "/tmp/open-rdma-driver")

	require.Zero(t, rdmaDLID(7))
}

func TestRDMADeviceDLIDUsesRemoteLID(t *testing.T) {
	t.Setenv("OPEN_RDMA_DRIVER", "")

	require.Equal(t, uint16(7), rdmaDLID(7))
}

func TestFrameBufferFallsBackToMallocWithoutOpenRDMADriver(t *testing.T) {
	t.Setenv("OPEN_RDMA_DRIVER", "")

	buffer, err := allocateFrameBuffer(minFrameBytes * 2)
	require.NoError(t, err)
	require.NotNil(t, buffer.ptr)
	require.Len(t, buffer.bytes, minFrameBytes*2)
	require.Equal(t, minFrameBytes*2, buffer.registerBytes)
	require.NoError(t, buffer.Close())
}

func TestFrameBufferUsesHugepageWhenOpenRDMADriverIsSet(t *testing.T) {
	t.Setenv("OPEN_RDMA_DRIVER", "/tmp/open-rdma-driver")

	buffer, err := allocateFrameBuffer(minFrameBytes * 2)
	if err != nil {
		t.Skipf("hugepage mmap is unavailable on this host: %v", err)
	}
	require.NotNil(t, buffer.ptr)
	require.Len(t, buffer.bytes, minFrameBytes*2)
	require.GreaterOrEqual(t, buffer.registerBytes, minFrameBytes*2)
	require.Zero(t, uintptr(buffer.ptr)%uintptr(hugePageBytes))
	require.NoError(t, buffer.Close())
}

func TestFrameBufferCloseRejectsUnknownAllocation(t *testing.T) {
	buffer := frameBuffer{ptr: unsafe.Pointer(uintptr(1))}

	require.Error(t, buffer.Close())
}

func TestTouchFrameBufferFaultsPages(t *testing.T) {
	data := make([]byte, osPageBytes*2+1)

	touchFrameBuffer(data)

	require.Zero(t, data[0])
	require.Zero(t, data[osPageBytes])
	require.Zero(t, data[len(data)-1])
}

func TestNewResourcesRejectsUnavailableDeviceIndex(t *testing.T) {
	resources, err := NewResources(1<<20, 0)

	require.Nil(t, resources)
	require.ErrorIs(t, err, ErrNoDevice)
}

func TestNewResourcesWithOptionsRejectsZeroPort(t *testing.T) {
	resources, err := NewResourcesWithOptions(ResourceOptions{
		DeviceIndex:   0,
		PortNum:       0,
		MaxFrameBytes: 0,
	})

	require.Nil(t, resources)
	require.ErrorIs(t, err, ErrInvalidPort)
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
	require.Equal(t, uint8(1), resources.portNum)
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

func TestValidateEndpointAllowsZeroPSN(t *testing.T) {
	require.NoError(t, validateEndpoint(Endpoint{
		QPN:   1,
		PSN:   0,
		RKey:  1,
		VAddr: 1,
	}))
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
