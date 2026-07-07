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

import "errors"

var (
	ErrInvalidDeviceIndex = errors.New("invalid RDMA device index")
	ErrInvalidPort        = errors.New("invalid RDMA port")
	ErrNoDevice           = errors.New("RDMA device is not available")
	ErrInvalidEndpoint    = errors.New("invalid RDMA endpoint")
	ErrFrameTooLarge      = errors.New("RDMA frame exceeds registered buffer")
)
