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
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/juicedata/juicefs/pkg/cache/remote"
	"github.com/juicedata/juicefs/pkg/cache/remote/cluster"
)

type Client struct {
	nodes     []string
	placement *cluster.Placement
	health    *cluster.Health

	httpClient *http.Client
}

type Options struct {
	Nodes         []string
	Timeout       time.Duration
	Replicas      int
	FailThreshold int
	NodeCooldown  time.Duration
}

func NewClient(nodes []string, timeout time.Duration) remote.Client {
	return NewClientWithOptions(Options{Nodes: nodes, Timeout: timeout, Replicas: 1})
}

func NewClientWithOptions(options Options) remote.Client {
	var nodes []string
	for _, node := range options.Nodes {
		node = strings.TrimSpace(node)
		if node == "" {
			continue
		}
		if !strings.Contains(node, "://") {
			node = "http://" + node
		}
		nodes = append(nodes, strings.TrimRight(node, "/"))
	}
	return &Client{
		nodes:      nodes,
		placement:  cluster.NewPlacement(nodes, options.Replicas),
		health:     cluster.NewHealth(cluster.Options{FailThreshold: options.FailThreshold, Cooldown: options.NodeCooldown}),
		httpClient: &http.Client{Timeout: options.Timeout},
	}
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
			c.health.MarkFailure(base)
			continue
		}
		q := u.Query()
		q.Set("off", strconv.Itoa(off))
		q.Set("size", strconv.Itoa(size))
		u.RawQuery = q.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			c.health.MarkFailure(base)
			continue
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			c.health.MarkFailure(base)
			continue
		}
		if resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			c.health.MarkSuccess(base)
			sawMiss = true
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			c.health.MarkFailure(base)
			continue
		}
		c.health.MarkSuccess(base)
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
			c.health.MarkFailure(base)
			continue
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			c.health.MarkFailure(base)
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusNoContent {
			c.health.MarkSuccess(base)
			ok = true
		} else {
			c.health.MarkFailure(base)
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
			c.health.MarkFailure(base)
			continue
		}
		resp, err := c.httpClient.Do(req)
		if err != nil {
			c.health.MarkFailure(base)
			continue
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotFound {
			c.health.MarkSuccess(base)
			ok = true
		} else {
			c.health.MarkFailure(base)
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
	nodes := c.health.Available(c.placement.Candidates(key))
	if len(nodes) == 0 {
		return nil, remote.ErrUnavailable
	}
	return nodes, nil
}

func unavailablef(format string, args ...interface{}) error {
	return fmt.Errorf("%w: "+format, append([]interface{}{remote.ErrUnavailable}, args...)...)
}
