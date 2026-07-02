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

package mock

import (
	"bytes"
	"context"
	"io"
	"sync"

	"github.com/juicedata/juicefs/pkg/cache/remote"
)

type Client struct {
	mu     sync.RWMutex
	closed bool
	data   map[string][]byte
}

func NewClient() *Client {
	return &Client{data: make(map[string][]byte)}
}

func (c *Client) Get(ctx context.Context, key string, off, size int) (io.ReadCloser, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return nil, remote.ErrUnavailable
	}
	value, ok := c.data[key]
	if !ok {
		return nil, remote.ErrMiss
	}
	if off < 0 || off > len(value) {
		return nil, remote.ErrMiss
	}
	end := len(value)
	if size >= 0 {
		end = off + size
		if end > len(value) {
			return nil, remote.ErrMiss
		}
	}
	out := make([]byte, end-off)
	copy(out, value[off:end])
	return io.NopCloser(bytes.NewReader(out)), nil
}

func (c *Client) Put(ctx context.Context, key string, data []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return remote.ErrUnavailable
	}
	value := make([]byte, len(data))
	copy(value, data)
	c.data[key] = value
	return nil
}

func (c *Client) Delete(ctx context.Context, key string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return remote.ErrUnavailable
	}
	delete(c.data, key)
	return nil
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}
