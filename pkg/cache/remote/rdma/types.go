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
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/juicedata/juicefs/pkg/cache/remote"
	"github.com/juicedata/juicefs/pkg/cache/remote/cluster"
	"github.com/juicedata/juicefs/pkg/cache/remote/rdma/protocol"
)

var ErrUnsupported = errors.New("RDMA remote cache transport is not built")

type CapabilityInfo struct {
	Built     bool
	Available bool
	Reason    string
}

type Options struct {
	Nodes         []string
	Timeout       time.Duration
	Replicas      int
	Dialer        Dialer
	FailThreshold int
	NodeCooldown  time.Duration
	ProbeInterval time.Duration
	ProbeTimeout  time.Duration
}

type Conn interface {
	RoundTrip(ctx context.Context, req protocol.Request) (protocol.Response, error)
	Close() error
}

type Dialer interface {
	Dial(ctx context.Context, node string, options Options) (Conn, error)
}

type Client struct {
	options   Options
	placement *cluster.Placement
	health    *cluster.Health
	mu        sync.Mutex
	conns     map[string]Conn
	closed    bool
}

type unsupportedDialer struct{}

func newClient(options Options) *Client {
	if options.Dialer == nil {
		options.Dialer = unsupportedDialer{}
	}
	return &Client{
		options:   options,
		placement: cluster.NewPlacement(options.Nodes, options.Replicas),
		health: cluster.NewHealth(cluster.Options{
			FailThreshold: options.FailThreshold,
			Cooldown:      options.NodeCooldown,
		}),
		conns: make(map[string]Conn),
	}
}

func (unsupportedDialer) Dial(ctx context.Context, node string, options Options) (Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, ErrUnsupported
}

func (c *Client) Get(ctx context.Context, key string, off, size int) (io.ReadCloser, error) {
	resp, err := c.roundTrip(ctx, protocol.Request{Op: protocol.OpGet, Key: key, Off: off, Size: size})
	if err != nil {
		return nil, err
	}
	if err := protocol.StatusToError(resp.Status); err != nil {
		return nil, err
	}
	data := make([]byte, len(resp.Payload))
	copy(data, resp.Payload)
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (c *Client) Put(ctx context.Context, key string, data []byte) error {
	payload := make([]byte, len(data))
	copy(payload, data)
	resp, err := c.roundTrip(ctx, protocol.Request{Op: protocol.OpPut, Key: key, Payload: payload})
	if err != nil {
		return err
	}
	return protocol.StatusToError(resp.Status)
}

func (c *Client) Delete(ctx context.Context, key string) error {
	resp, err := c.roundTrip(ctx, protocol.Request{Op: protocol.OpDelete, Key: key})
	if err != nil {
		return err
	}
	return protocol.StatusToError(resp.Status)
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	var err error
	for _, conn := range c.conns {
		if e := conn.Close(); e != nil && err == nil {
			err = e
		}
	}
	return err
}

func (c *Client) roundTrip(ctx context.Context, req protocol.Request) (protocol.Response, error) {
	if len(c.options.Nodes) == 0 {
		return protocol.Response{}, ErrUnsupported
	}
	nodes := c.health.Available(c.placement.Candidates(req.Key))
	if len(nodes) == 0 {
		return protocol.Response{}, remote.ErrUnavailable
	}
	var sawMiss bool
	for _, node := range nodes {
		conn, err := c.connection(ctx, node)
		if err != nil {
			if errors.Is(err, ErrUnsupported) {
				return protocol.Response{}, err
			}
			c.health.MarkFailure(node)
			continue
		}
		resp, err := conn.RoundTrip(ctx, req)
		if err != nil {
			c.health.MarkFailure(node)
			continue
		}
		switch resp.Status {
		case protocol.StatusOK:
			c.health.MarkSuccess(node)
			return resp, nil
		case protocol.StatusMiss:
			c.health.MarkSuccess(node)
			if req.Op == protocol.OpGet {
				sawMiss = true
				continue
			}
			if req.Op == protocol.OpDelete {
				return protocol.Response{Status: protocol.StatusOK}, nil
			}
		default:
			c.health.MarkFailure(node)
		}
	}
	if sawMiss {
		return protocol.Response{Status: protocol.StatusMiss}, nil
	}
	return protocol.Response{}, remote.ErrUnavailable
}

func (c *Client) connection(ctx context.Context, node string) (Conn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrUnsupported
	}
	if conn := c.conns[node]; conn != nil {
		return conn, nil
	}
	conn, err := c.options.Dialer.Dial(ctx, node, c.options)
	if err != nil {
		return nil, err
	}
	c.conns[node] = conn
	return conn, nil
}
