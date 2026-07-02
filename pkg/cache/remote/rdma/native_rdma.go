//go:build rdma

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

import "github.com/juicedata/juicefs/pkg/cache/remote"

func NewClient(options Options) remote.Client {
	return unsupportedClient{}
}

func Capability() CapabilityInfo {
	return CapabilityInfo{
		Built:     true,
		Available: false,
		Reason:    "native RDMA implementation is not available yet",
	}
}
