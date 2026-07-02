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

package diskcache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/juicedata/juicefs/pkg/cache/remote"
)

type Options struct {
	Dir      string
	Capacity int64
}

type Client struct {
	mu       sync.Mutex
	dir      string
	capacity int64
	used     int64
	closed   bool
	entries  map[string]*entry
}

type entry struct {
	key  string
	path string
	size int64
	at   time.Time
}

type sectionReadCloser struct {
	*io.SectionReader
	file *os.File
}

func (r *sectionReadCloser) Close() error {
	return r.file.Close()
}

func NewClient(options Options) (*Client, error) {
	if options.Capacity <= 0 {
		options.Capacity = 1 << 30
	}
	if options.Dir == "" {
		return nil, errors.New("disk cache dir is empty")
	}
	if err := os.MkdirAll(options.Dir, 0755); err != nil {
		return nil, err
	}
	c := &Client{
		dir:      options.Dir,
		capacity: options.Capacity,
		entries:  make(map[string]*entry),
	}
	if err := c.scan(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) Get(ctx context.Context, key string, off, size int) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, remote.ErrUnavailable
	}
	e := c.entries[key]
	if e == nil || off < 0 || int64(off) > e.size {
		c.mu.Unlock()
		return nil, remote.ErrMiss
	}
	length := e.size - int64(off)
	if size >= 0 {
		length = int64(size)
		if int64(off)+length > e.size {
			c.mu.Unlock()
			return nil, remote.ErrMiss
		}
	}
	e.at = time.Now()
	path := e.path
	at := e.at
	c.mu.Unlock()

	file, err := os.Open(path)
	if err != nil {
		return nil, remote.ErrMiss
	}
	_ = os.Chtimes(path, at, at)
	return &sectionReadCloser{
		SectionReader: io.NewSectionReader(file, int64(off), length),
		file:          file,
	}, nil
}

func (c *Client) Put(ctx context.Context, key string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if int64(len(data)) > c.capacity {
		return remote.ErrUnavailable
	}

	dataPath, keyPath := c.paths(key)
	if err := os.MkdirAll(filepath.Dir(dataPath), 0755); err != nil {
		return remote.ErrUnavailable
	}
	tmp, err := os.CreateTemp(filepath.Dir(dataPath), ".tmp-")
	if err != nil {
		return remote.ErrUnavailable
	}
	tmpName := tmp.Name()
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return remote.ErrUnavailable
	}
	if err = tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return remote.ErrUnavailable
	}
	if err = os.WriteFile(keyPath, []byte(key), 0644); err != nil {
		_ = os.Remove(tmpName)
		return remote.ErrUnavailable
	}
	if err = os.Rename(tmpName, dataPath); err != nil {
		_ = os.Remove(tmpName)
		return remote.ErrUnavailable
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		_ = os.Remove(dataPath)
		_ = os.Remove(keyPath)
		return remote.ErrUnavailable
	}
	if old := c.entries[key]; old != nil {
		c.used -= old.size
	}
	now := time.Now()
	c.entries[key] = &entry{key: key, path: dataPath, size: int64(len(data)), at: now}
	c.used += int64(len(data))
	_ = os.Chtimes(dataPath, now, now)
	c.evictLocked()
	return nil
}

func (c *Client) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return remote.ErrUnavailable
	}
	c.deleteLocked(key)
	return nil
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *Client) scan() error {
	return filepath.WalkDir(c.dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".data") {
			return err
		}
		keyPath := strings.TrimSuffix(path, ".data") + ".key"
		keyBytes, err := os.ReadFile(keyPath)
		if err != nil {
			_ = os.Remove(path)
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		key := string(keyBytes)
		c.entries[key] = &entry{
			key:  key,
			path: path,
			size: info.Size(),
			at:   info.ModTime(),
		}
		c.used += info.Size()
		return nil
	})
}

func (c *Client) paths(key string) (string, string) {
	sum := sha256.Sum256([]byte(key))
	encoded := hex.EncodeToString(sum[:])
	dataPath := filepath.Join(c.dir, encoded[:2], encoded+".data")
	return dataPath, strings.TrimSuffix(dataPath, ".data") + ".key"
}

func (c *Client) evictLocked() {
	for c.used > c.capacity && len(c.entries) > 0 {
		victims := make([]*entry, 0, len(c.entries))
		for _, e := range c.entries {
			victims = append(victims, e)
		}
		sort.Slice(victims, func(i, j int) bool {
			return victims[i].at.Before(victims[j].at)
		})
		c.deleteLocked(victims[0].key)
	}
}

func (c *Client) deleteLocked(key string) {
	e := c.entries[key]
	if e == nil {
		return
	}
	delete(c.entries, key)
	c.used -= e.size
	_ = os.Remove(e.path)
	_ = os.Remove(strings.TrimSuffix(e.path, ".data") + ".key")
}
