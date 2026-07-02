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

package cmd

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/cache/remote"
	"github.com/juicedata/juicefs/pkg/cache/remote/diskcache"
	"github.com/juicedata/juicefs/pkg/cache/remote/httpcache"
	"github.com/juicedata/juicefs/pkg/cache/remote/mock"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/urfave/cli/v2"
)

func cmdRDMACacheServer() *cli.Command {
	return &cli.Command{
		Name:  "rdma-cache-server",
		Usage: "run a remote cache server for RDMA distributed cache development",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "listen",
				Value: "127.0.0.1:9568",
				Usage: "address to listen on",
			},
			&cli.StringFlag{
				Name:  "cache-size",
				Value: "100G",
				Usage: "reserved cache capacity for future disk backend",
			},
			&cli.StringFlag{
				Name:  "cache-dir",
				Usage: "reserved cache directory for future disk backend",
			},
		},
		Action: rdmaCacheServer,
	}
}

func rdmaCacheServer(c *cli.Context) error {
	listen := c.String("listen")
	backend, err := newRDMACacheBackend(c.String("cache-dir"), c.String("cache-size"))
	if err != nil {
		return err
	}
	defer backend.Close()
	logger.Infof("starting rdma-cache-server on %s with cache-size %s", listen, c.String("cache-size"))

	srv := &http.Server{
		Addr:              listen,
		Handler:           httpcache.NewHandler(backend),
		ReadHeaderTimeout: time.Second * 5,
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case sig := <-sigCh:
		logger.Infof("stopping rdma-cache-server after %s", sig)
		ctx, cancel := context.WithTimeout(c.Context, time.Second*10)
		defer cancel()
		return srv.Shutdown(ctx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func newRDMACacheBackend(cacheDir string, cacheSize string) (remote.Client, error) {
	if cacheDir == "" {
		return mock.NewClient(), nil
	}
	size := utils.ParseBytesStr("cache-size", cacheSize, 'B')
	return diskcache.NewClient(diskcache.Options{
		Dir:      cacheDir,
		Capacity: int64(size),
	})
}
