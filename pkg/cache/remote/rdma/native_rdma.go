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

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/juicedata/juicefs/pkg/cache/remote"
)

func NewClient(options Options) remote.Client {
	if options.Dialer == nil {
		options.Dialer = newNativeDialerFromEnv()
	}
	return newClient(options)
}

func ListenAndServe(ctx context.Context, options ServeOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrUnsupported
}

func Capability() CapabilityInfo {
	driverDir := os.Getenv("OPEN_RDMA_DRIVER")
	if driverDir == "" {
		return CapabilityInfo{
			Built:     true,
			Available: false,
			Reason:    "OPEN_RDMA_DRIVER is not set; run hack/open-rdma-smoke-test.sh --driver-dir /path/to/open-rdma-driver",
		}
	}
	if err := checkOpenRDMADriverDir(driverDir); err != nil {
		return CapabilityInfo{
			Built:     true,
			Available: false,
			Reason:    err.Error(),
		}
	}
	return CapabilityInfo{
		Built:     true,
		Available: true,
		Reason:    fmt.Sprintf("open-rdma checkout ready: %s", driverDir),
	}
}

func checkOpenRDMADriverDir(driverDir string) error {
	requiredDirs := []string{
		"dtld-ibverbs",
		"examples",
		"scripts",
	}
	for _, name := range requiredDirs {
		path := filepath.Join(driverDir, name)
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("open-rdma checkout missing %s: %w", path, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("open-rdma checkout path is not a directory: %s", path)
		}
	}
	setup := filepath.Join(driverDir, "scripts", "setup-env.sh")
	info, err := os.Stat(setup)
	if err != nil {
		return fmt.Errorf("open-rdma checkout missing %s: %w", setup, err)
	}
	if info.IsDir() {
		return fmt.Errorf("open-rdma setup path is a directory: %s", setup)
	}
	return nil
}
