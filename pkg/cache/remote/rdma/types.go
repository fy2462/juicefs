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
	"errors"
	"io"
	"time"
)

var ErrUnsupported = errors.New("RDMA remote cache transport is not built")

type Options struct {
	Nodes    []string
	Timeout  time.Duration
	Replicas int
}

type unsupportedClient struct{}

func (unsupportedClient) Get(ctx context.Context, key string, off, size int) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, ErrUnsupported
}

func (unsupportedClient) Put(ctx context.Context, key string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrUnsupported
}

func (unsupportedClient) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrUnsupported
}

func (unsupportedClient) Close() error {
	return nil
}
