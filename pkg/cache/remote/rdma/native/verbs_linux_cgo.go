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
#include <stdint.h>
#include <stdlib.h>
#include <infiniband/verbs.h>

static int jfs_ibv_query_port(struct ibv_context *context, uint8_t port_num, struct ibv_port_attr *port_attr) {
	return ibv_query_port(context, port_num, port_attr);
}
*/
import "C"

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"unsafe"
)

const (
	minFrameBytes     = 64 << 10
	defaultFrameBytes = 4 << 20
)

type Resources struct {
	deviceIndex       int
	maxFrameBytes     int
	portNum           uint8
	psn               uint32
	context           *C.struct_ibv_context
	protectionDomain  *C.struct_ibv_pd
	completionQueue   *C.struct_ibv_cq
	queuePair         *C.struct_ibv_qp
	memoryRegion      *C.struct_ibv_mr
	bufferPtr         unsafe.Pointer
	buffer            []byte
	completionEntries int
}

type Endpoint struct {
	LID   uint16
	QPN   uint32
	PSN   uint32
	RKey  uint32
	VAddr uint64
	Port  uint8
}

func NewResources(deviceIndex, maxFrameBytes int) (*Resources, error) {
	if deviceIndex < 0 {
		return nil, ErrInvalidDeviceIndex
	}
	limit := frameLimit(maxFrameBytes)
	deviceList, count, err := getDeviceList()
	if err != nil {
		return nil, err
	}
	defer C.ibv_free_device_list(deviceList)
	if count == 0 || deviceIndex >= count {
		return nil, ErrNoDevice
	}

	device := *(**C.struct_ibv_device)(unsafe.Pointer(uintptr(unsafe.Pointer(deviceList)) + uintptr(deviceIndex)*unsafe.Sizeof(uintptr(0))))
	resources := &Resources{
		deviceIndex:       deviceIndex,
		maxFrameBytes:     limit,
		portNum:           1,
		completionEntries: 32,
	}
	if err := resources.open(device); err != nil {
		_ = resources.Close()
		return nil, err
	}
	return resources, nil
}

func (r *Resources) Close() error {
	if r == nil {
		return nil
	}
	var err error
	if r.queuePair != nil {
		if rc := C.ibv_destroy_qp(r.queuePair); rc != 0 {
			err = errors.Join(err, fmt.Errorf("destroy RDMA queue pair: %w", errnoError()))
		}
		r.queuePair = nil
	}
	if r.memoryRegion != nil {
		if rc := C.ibv_dereg_mr(r.memoryRegion); rc != 0 {
			err = errors.Join(err, fmt.Errorf("deregister RDMA memory region: %w", errnoError()))
		}
		r.memoryRegion = nil
	}
	if r.bufferPtr != nil {
		C.free(r.bufferPtr)
		r.bufferPtr = nil
		r.buffer = nil
	}
	if r.completionQueue != nil {
		if rc := C.ibv_destroy_cq(r.completionQueue); rc != 0 {
			err = errors.Join(err, fmt.Errorf("destroy RDMA completion queue: %w", errnoError()))
		}
		r.completionQueue = nil
	}
	if r.protectionDomain != nil {
		if rc := C.ibv_dealloc_pd(r.protectionDomain); rc != 0 {
			err = errors.Join(err, fmt.Errorf("deallocate RDMA protection domain: %w", errnoError()))
		}
		r.protectionDomain = nil
	}
	if r.context != nil {
		if rc := C.ibv_close_device(r.context); rc != 0 {
			err = errors.Join(err, fmt.Errorf("close RDMA device: %w", errnoError()))
		}
		r.context = nil
	}
	return err
}

func (r *Resources) LocalEndpoint() (Endpoint, error) {
	if r == nil || r.queuePair == nil || r.memoryRegion == nil || r.bufferPtr == nil {
		return Endpoint{}, ErrNoDevice
	}
	var portAttr C.struct_ibv_port_attr
	if rc := C.jfs_ibv_query_port(r.context, C.uint8_t(r.portNum), &portAttr); rc != 0 {
		return Endpoint{}, fmt.Errorf("query RDMA port %d: %w", r.portNum, errnoError())
	}
	return Endpoint{
		LID:   uint16(portAttr.lid),
		QPN:   uint32(r.queuePair.qp_num),
		PSN:   r.psn,
		RKey:  uint32(r.memoryRegion.rkey),
		VAddr: uint64(uintptr(r.bufferPtr)),
		Port:  r.portNum,
	}, nil
}

func DeviceCount() (int, error) {
	deviceList, count, err := getDeviceList()
	if err != nil {
		return 0, err
	}
	defer C.ibv_free_device_list(deviceList)
	return count, nil
}

func (r *Resources) open(device *C.struct_ibv_device) error {
	r.context = C.ibv_open_device(device)
	if r.context == nil {
		return fmt.Errorf("open RDMA device %d: %w", r.deviceIndex, errnoError())
	}
	r.protectionDomain = C.ibv_alloc_pd(r.context)
	if r.protectionDomain == nil {
		return fmt.Errorf("allocate RDMA protection domain: %w", errnoError())
	}
	r.completionQueue = C.ibv_create_cq(r.context, C.int(r.completionEntries), nil, nil, 0)
	if r.completionQueue == nil {
		return fmt.Errorf("create RDMA completion queue: %w", errnoError())
	}
	r.bufferPtr = C.calloc(C.size_t(r.maxFrameBytes), 1)
	if r.bufferPtr == nil {
		return fmt.Errorf("allocate RDMA frame buffer: %w", errnoError())
	}
	r.buffer = unsafe.Slice((*byte)(r.bufferPtr), r.maxFrameBytes)
	access := C.IBV_ACCESS_LOCAL_WRITE | C.IBV_ACCESS_REMOTE_READ | C.IBV_ACCESS_REMOTE_WRITE
	r.memoryRegion = C.ibv_reg_mr(r.protectionDomain, r.bufferPtr, C.size_t(r.maxFrameBytes), C.int(access))
	if r.memoryRegion == nil {
		return fmt.Errorf("register RDMA memory region: %w", errnoError())
	}
	psn, err := randomPSN()
	if err != nil {
		return err
	}
	r.psn = psn
	if err := r.createQueuePair(); err != nil {
		return err
	}
	return nil
}

func (r *Resources) createQueuePair() error {
	var initAttr C.struct_ibv_qp_init_attr
	initAttr.send_cq = r.completionQueue
	initAttr.recv_cq = r.completionQueue
	initAttr.qp_type = C.IBV_QPT_RC
	initAttr.cap.max_send_wr = 16
	initAttr.cap.max_recv_wr = 16
	initAttr.cap.max_send_sge = 1
	initAttr.cap.max_recv_sge = 1
	r.queuePair = C.ibv_create_qp(r.protectionDomain, &initAttr)
	if r.queuePair == nil {
		return fmt.Errorf("create RDMA queue pair: %w", errnoError())
	}
	return nil
}

func getDeviceList() (**C.struct_ibv_device, int, error) {
	var count C.int
	deviceList := C.ibv_get_device_list(&count)
	if deviceList == nil {
		return nil, 0, fmt.Errorf("list RDMA devices: %w", errnoError())
	}
	return deviceList, int(count), nil
}

func errnoError() error {
	return errors.New("libibverbs returned an error")
}

func randomPSN() (uint32, error) {
	var data [4]byte
	if _, err := rand.Read(data[:]); err != nil {
		return 0, fmt.Errorf("generate RDMA packet sequence number: %w", err)
	}
	return binary.BigEndian.Uint32(data[:]) & 0xffffff, nil
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
