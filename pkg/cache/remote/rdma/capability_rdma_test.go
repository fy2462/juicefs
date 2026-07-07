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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCapabilityRDMABuildChecksOpenRDMADriver(t *testing.T) {
	t.Setenv("OPEN_RDMA_DRIVER", "")

	capability := Capability()
	require.True(t, capability.Built)
	require.False(t, capability.Available)
	require.Contains(t, capability.Reason, "OPEN_RDMA_DRIVER")

	driverDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(driverDir, "dtld-ibverbs"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(driverDir, "examples"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(driverDir, "scripts"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(driverDir, "scripts", "setup-env.sh"), []byte("#!/bin/sh\n"), 0o755))
	t.Setenv("OPEN_RDMA_DRIVER", driverDir)

	capability = Capability()
	require.True(t, capability.Built)
	require.True(t, capability.Available)
	require.Contains(t, capability.Reason, "open-rdma checkout ready")
}
