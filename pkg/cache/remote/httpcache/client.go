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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/juicedata/juicefs/pkg/cache/remote"
)

type Client struct {
	nodes      []string
	replicas   int
	httpClient *http.Client
}

type Options struct {
	Nodes    []string
	Timeout  time.Duration
	Replicas int
}

func NewClient(nodes []string, timeout time.Duration) remote.Client {
	return NewClientWithOptions(Options{Nodes: nodes, Timeout: timeout, Replicas: 1})
}

func NewClientWithOptions(options Options) remote.Client {
	c := &Client{httpClient: &http.Client{Timeout: options.Timeout}}
	for _, node := range options.Nodes {
		node = strings.TrimSpace(node)
		if node == "" {
			continue
		}
		if !strings.Contains(node, "://") {
			node = "http://" + node
		}
		c.nodes = append(c.nodes, strings.TrimRight(node, "/"))
	}
	c.replicas = options.Replicas
	if c.replicas <= 0 {
		c.replicas = 1
	}
	if c.replicas > len(c.nodes) {
		c.replicas = len(c.nodes)
	}
	return c
}

func (c *Client) Get(ctx context.Context, key string, off, size int) (io.ReadCloser, error) {
	nodes, err := c.replicaNodes(key)
	if err != nil {
		return nil, err
	}
	var sawMiss bool
	for _, base := range nodes {
		u, err := url.Parse(base + "/cache/" + url.PathEscape(key))
		if err != nil {
			continue
		}
		q := u.Query()
		q.Set("off", strconv.Itoa(off))
		q.Set("size", strconv.Itoa(size))
		u.RawQuery = q.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			continue
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			continue
		}
		if resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			sawMiss = true
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			continue
		}
		return resp.Body, nil
	}
	if sawMiss {
		return nil, remote.ErrMiss
	}
	return nil, remote.ErrUnavailable
}

func (c *Client) Put(ctx context.Context, key string, data []byte) error {
	nodes, err := c.replicaNodes(key)
	if err != nil {
		return err
	}
	var ok bool
	for _, base := range nodes {
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, base+"/cache/"+url.PathEscape(key), bytes.NewReader(data))
		if err != nil {
			continue
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusNoContent {
			ok = true
		}
	}
	if ok {
		return nil
	}
	return remote.ErrUnavailable
}

func (c *Client) Delete(ctx context.Context, key string) error {
	nodes, err := c.replicaNodes(key)
	if err != nil {
		return err
	}
	var ok bool
	for _, base := range nodes {
		req, err := http.NewRequestWithContext(ctx, http.MethodDelete, base+"/cache/"+url.PathEscape(key), nil)
		if err != nil {
			continue
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotFound {
			ok = true
		}
	}
	if ok {
		return nil
	}
	return remote.ErrUnavailable
}

func (c *Client) Close() error {
	return nil
}

func (c *Client) replicaNodes(key string) ([]string, error) {
	if len(c.nodes) == 0 {
		return nil, remote.ErrDisabled
	}
	nodes := append([]string(nil), c.nodes...)
	sort.Slice(nodes, func(i, j int) bool {
		return rendezvousScore(key, nodes[i]) > rendezvousScore(key, nodes[j])
	})
	return nodes[:c.replicas], nil
}

func rendezvousScore(key, node string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(node))
	return h.Sum64()
}

func unavailablef(format string, args ...interface{}) error {
	return fmt.Errorf("%w: "+format, append([]interface{}{remote.ErrUnavailable}, args...)...)
}
