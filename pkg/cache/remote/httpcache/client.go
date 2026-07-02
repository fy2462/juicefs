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

package httpcache

import (
	"bytes"
	"context"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/juicedata/juicefs/pkg/cache/remote"
)

type Client struct {
	nodes      []string
	httpClient *http.Client
}

func NewClient(nodes []string, timeout time.Duration) remote.Client {
	c := &Client{httpClient: &http.Client{Timeout: timeout}}
	for _, node := range nodes {
		node = strings.TrimSpace(node)
		if node == "" {
			continue
		}
		if !strings.Contains(node, "://") {
			node = "http://" + node
		}
		c.nodes = append(c.nodes, strings.TrimRight(node, "/"))
	}
	return c
}

func (c *Client) Get(ctx context.Context, key string, off, size int) (io.ReadCloser, error) {
	base, err := c.node(key)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(base + "/cache/" + url.PathEscape(key))
	if err != nil {
		return nil, remote.ErrUnavailable
	}
	q := u.Query()
	q.Set("off", strconv.Itoa(off))
	q.Set("size", strconv.Itoa(size))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, remote.ErrUnavailable
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, remote.ErrUnavailable
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, remote.ErrMiss
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, remote.ErrUnavailable
	}
	return resp.Body, nil
}

func (c *Client) Put(ctx context.Context, key string, data []byte) error {
	base, err := c.node(key)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, base+"/cache/"+url.PathEscape(key), bytes.NewReader(data))
	if err != nil {
		return remote.ErrUnavailable
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return remote.ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return remote.ErrUnavailable
	}
	return nil
}

func (c *Client) Delete(ctx context.Context, key string) error {
	base, err := c.node(key)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, base+"/cache/"+url.PathEscape(key), nil)
	if err != nil {
		return remote.ErrUnavailable
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return remote.ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		return remote.ErrUnavailable
	}
	return nil
}

func (c *Client) Close() error {
	return nil
}

func (c *Client) node(key string) (string, error) {
	if len(c.nodes) == 0 {
		return "", remote.ErrDisabled
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return c.nodes[int(h.Sum64()%uint64(len(c.nodes)))], nil
}

func unavailablef(format string, args ...interface{}) error {
	return fmt.Errorf("%w: "+format, append([]interface{}{remote.ErrUnavailable}, args...)...)
}
