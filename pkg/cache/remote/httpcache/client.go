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
	"sync"
	"time"

	"github.com/juicedata/juicefs/pkg/cache/remote"
	"github.com/juicedata/juicefs/pkg/cache/remote/cluster"
)

type Client struct {
	nodes     []string
	placement *cluster.Placement
	health    *cluster.Health

	httpClient   *http.Client
	probeTimeout time.Duration
	stopCh       chan struct{}
	closeOnce    sync.Once
	wg           sync.WaitGroup
}

type Options struct {
	Nodes         []string
	Timeout       time.Duration
	Replicas      int
	FailThreshold int
	NodeCooldown  time.Duration
	ProbeInterval time.Duration
	ProbeTimeout  time.Duration
	Observer      cluster.Observer
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
	client := &Client{
		nodes:        nodes,
		placement:    cluster.NewPlacement(nodes, options.Replicas),
		health:       cluster.NewHealth(cluster.Options{FailThreshold: options.FailThreshold, Cooldown: options.NodeCooldown, Observer: options.Observer}),
		httpClient:   &http.Client{Timeout: options.Timeout},
		probeTimeout: remoteCacheProbeTimeout(options.ProbeTimeout, options.Timeout),
		stopCh:       make(chan struct{}),
	}
	if options.ProbeInterval > 0 {
		client.startProber(options.ProbeInterval)
	}
	return client
}

func (c *Client) Get(ctx context.Context, key string, off, size int) (io.ReadCloser, error) {
	nodes, err := c.replicaNodes(key, "get")
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
	nodes, err := c.replicaNodes(key, "put")
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
	nodes, err := c.replicaNodes(key, "delete")
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
	c.closeOnce.Do(func() {
		close(c.stopCh)
		c.wg.Wait()
	})
	return nil
}

func (c *Client) startProber(interval time.Duration) {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				c.probeUnhealthy()
			case <-c.stopCh:
				return
			}
		}
	}()
}

func (c *Client) probeUnhealthy() {
	for _, base := range c.health.Unhealthy(c.nodes) {
		ctx, cancel := context.WithTimeout(context.Background(), c.probeTimeout)
		ok := c.probeNode(ctx, base)
		cancel()
		c.health.MarkProbe(base, ok)
	}
}

func (c *Client) probeNode(ctx context.Context, base string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/healthz", nil)
	if err != nil {
		return false
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusNoContent
}

func remoteCacheProbeTimeout(probeTimeout, requestTimeout time.Duration) time.Duration {
	if probeTimeout > 0 {
		return probeTimeout
	}
	if requestTimeout > 0 && requestTimeout < 10*time.Millisecond {
		return requestTimeout
	}
	return 10 * time.Millisecond
}

func (c *Client) replicaNodes(key, op string) ([]string, error) {
	if len(c.nodes) == 0 {
		return nil, remote.ErrDisabled
	}
	nodes := c.health.AvailableForOp(c.placement.Candidates(key), op)
	if len(nodes) == 0 {
		return nil, remote.ErrUnavailable
	}
	return nodes, nil
}

func unavailablef(format string, args ...interface{}) error {
	return fmt.Errorf("%w: "+format, append([]interface{}{remote.ErrUnavailable}, args...)...)
}
