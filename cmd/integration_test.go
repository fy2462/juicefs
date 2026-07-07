/*
 * JuiceFS, Copyright 2022 Juicedata, Inc.
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

package cmd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

const gatewayMeta = "redis://127.0.0.1:6379/14"
const gatewayVolume = "gateway-volume"
const gatewayAddr = "localhost:9008"
const webdavMeta = "redis://127.0.0.1:6379/15"
const webdavVolume = "webdav-volume"
const webdavAddr = "localhost:9009"

func startGateway(t *testing.T) {
	opt, _ := redis.ParseURL(gatewayMeta)
	rdb := redis.NewClient(opt)
	_ = rdb.FlushDB(context.Background())
	testDir := t.TempDir()
	if err := Main([]string{"", "format", "--bucket", testDir, gatewayMeta, gatewayVolume}); err != nil {
		t.Fatalf("format failed: %s", err)
	}

	// must do reset, otherwise will panic
	ResetHttp()

	go func() {
		if err := Main([]string{"", "gateway", gatewayMeta, gatewayAddr, "--multi-buckets", "--keep-etag", "--object-tag", "--no-usage-report"}); err != nil {
			t.Errorf("gateway failed: %s", err)
		}
	}()
	time.Sleep(2 * time.Second)
}

func startWebdav(t *testing.T) {
	opt, _ := redis.ParseURL(webdavMeta)
	rdb := redis.NewClient(opt)
	_ = rdb.FlushDB(context.Background())
	testDir := t.TempDir()
	if err := Main([]string{"", "format", "--bucket", testDir, webdavMeta, webdavVolume}); err != nil {
		t.Fatalf("format failed: %s", err)
	}

	// must do reset, otherwise will panic
	ResetHttp()

	go func() {
		os.Setenv("WEBDAV_USER", "root")
		os.Setenv("WEBDAV_PASSWORD", "1234")
		if err := Main([]string{"", "webdav", webdavMeta, webdavAddr, "--no-usage-report"}); err != nil {
			t.Errorf("gateway failed: %s", err)
		}
	}()
	time.Sleep(2 * time.Second)
}

func TestIntegration(t *testing.T) {
	if _, err := exec.LookPath("pip"); err != nil {
		t.Skip("integration test requires pip because integration/Makefile invokes pip")
	}
	if _, err := exec.LookPath("sudo"); err != nil {
		t.Skip("integration test requires sudo because integration/Makefile invokes litmus via sudo")
	}
	litmusDir := "/home/travis/.m2/litmus-0.13"
	if st, err := os.Stat(litmusDir); err != nil || !st.IsDir() {
		t.Skipf("integration test requires litmus directory %s", litmusDir)
	}
	t.Setenv("MINIO_ROOT_USER", "admin")
	t.Setenv("MINIO_ROOT_PASSWORD", "12345678")

	mountTemp(t, nil, nil, []string{"--enable-ioctl"})
	defer umountTemp(t)
	startGateway(t)
	startWebdav(t)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %s", err)
	}
	if err := os.Chdir(filepath.Join(wd, "../integration")); err != nil {
		t.Fatalf("change to integration directory: %s", err)
	}
	defer func() {
		_ = os.Chdir(wd)
	}()
	makeCmd := exec.Command("make")
	makeCmd.Env = append(os.Environ(), "MOUNT_POINT="+testMountPoint)
	out, err := makeCmd.CombinedOutput()
	if err != nil {
		t.Logf("std out:\n%s\n", string(out))
		t.Fatalf("std err failed with %s\n", err)
	} else {
		t.Logf("std out:\n%s\n", string(out))
	}
}
