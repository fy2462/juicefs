/*
 * JuiceFS, Copyright 2020 Juicedata, Inc.
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

//nolint:errcheck
package chunk

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/juicedata/juicefs/pkg/cache/remote"
	"github.com/juicedata/juicefs/pkg/cache/remote/httpcache"
	"github.com/juicedata/juicefs/pkg/cache/remote/mock"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func forgetSlice(store ChunkStore, sliceId uint64, size int) error {
	w := store.NewWriter(sliceId, 0)
	buf := bytes.Repeat([]byte{0x41}, size)
	if _, err := w.WriteAt(buf, 0); err != nil {
		return err
	}
	return w.Finish(size)
}

func testStore(t *testing.T, store ChunkStore) {
	writer := store.NewWriter(1, 0)
	data := []byte("hello world")
	if n, err := writer.WriteAt(data, 0); n != 11 || err != nil {
		t.Fatalf("write fail: %d %s", n, err)
	}
	offset := defaultConf.BlockSize - 3
	if n, err := writer.WriteAt(data, int64(offset)); err != nil || n != 11 {
		t.Fatalf("write fail: %d %s", n, err)
	}
	if err := writer.FlushTo(defaultConf.BlockSize + 3); err != nil {
		t.Fatalf("flush fail: %s", err)
	}
	size := offset + len(data)
	if err := writer.Finish(size); err != nil {
		t.Fatalf("finish fail: %s", err)
	}
	defer store.Remove(1, size)

	reader := store.NewReader(1, size)
	p := NewPage(make([]byte, 5))
	if n, err := reader.ReadAt(context.Background(), p, 6); n != 5 || err != nil {
		t.Fatalf("read failed: %d %s", n, err)
	} else if string(p.Data[:n]) != "world" {
		t.Fatalf("not expected: %s", string(p.Data[:n]))
	}
	p = NewPage(make([]byte, 5))
	if n, err := reader.ReadAt(context.Background(), p, 0); n != 5 || err != nil {
		t.Fatalf("read failed: %d %s", n, err)
	} else if string(p.Data[:n]) != "hello" {
		t.Fatalf("not expected: %s", string(p.Data[:n]))
	}
	p = NewPage(make([]byte, 20))
	if n, err := reader.ReadAt(context.Background(), p, offset); n != 11 || err != nil && err != io.EOF {
		t.Fatalf("read failed: %d %s", n, err)
	} else if string(p.Data[:n]) != "hello world" {
		t.Fatalf("not expected: %s", string(p.Data[:n]))
	}

	bsize := defaultConf.BlockSize / 2
	errs := make(chan error, 3)
	for i := 2; i < 5; i++ {
		go func(sliceId uint64) {
			if err := forgetSlice(store, sliceId, bsize); err != nil {
				errs <- err
				return
			}
			time.Sleep(time.Millisecond * 100) // waiting for flush
			errs <- store.Remove(sliceId, bsize)
		}(uint64(i))
	}
	for i := 0; i < 3; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("test concurrent write failed: %s", err)
		}
	}
}

var defaultConf = Config{
	BlockSize:         1 << 20,
	CacheDir:          filepath.Join(os.TempDir(), fmt.Sprintf("diskCache-%d", os.Getpid())),
	CacheMode:         0600,
	CacheSize:         10 << 20,
	CacheChecksum:     CsNone,
	CacheScanInterval: time.Second * 300,
	MaxUpload:         1,
	MaxDownload:       200,
	MaxRetries:        10,
	PutTimeout:        time.Second,
	GetTimeout:        time.Second * 2,
	AutoCreate:        true,
	BufferSize:        10 << 20,
}

var ctx = context.Background()

func TestStoreDefault(t *testing.T) {
	mem, _ := object.CreateStorage("mem", "", "", "", "")
	_ = os.RemoveAll(defaultConf.CacheDir)
	store := NewCachedStore(mem, defaultConf, nil)
	testStore(t, store)
	if used := store.UsedMemory(); used != 0 {
		t.Fatalf("used memory %d != expect 0", used)
	}
	if cnt, used := store.(*cachedStore).bcache.stats(); cnt != 0 || used != 0 {
		t.Fatalf("cache cnt %d used %d, expect both 0", cnt, used)
	}
}

func TestStoreMemCache(t *testing.T) {
	mem, _ := object.CreateStorage("mem", "", "", "", "")
	conf := defaultConf
	conf.CacheDir = "memory"
	store := NewCachedStore(mem, conf, nil)
	testStore(t, store)
	if used := store.UsedMemory(); used != 0 {
		t.Fatalf("used memory %d != expect 0", used)
	}
	if cnt, used := store.(*cachedStore).bcache.stats(); cnt != 0 || used != 0 {
		t.Fatalf("cache cnt %d used %d, expect both 0", cnt, used)
	}
}
func TestStoreCompressed(t *testing.T) {
	mem, _ := object.CreateStorage("mem", "", "", "", "")
	conf := defaultConf
	conf.Compress = "lz4"
	conf.AutoCreate = false
	store := NewCachedStore(mem, conf, nil)
	testStore(t, store)
}

func TestStoreLimited(t *testing.T) {
	mem, _ := object.CreateStorage("mem", "", "", "", "")
	conf := defaultConf
	conf.UploadLimit = 1e6
	conf.DownloadLimit = 1e6
	store := NewCachedStore(mem, conf, nil)
	testStore(t, store)
}

func TestStoreFull(t *testing.T) {
	mem, _ := object.CreateStorage("mem", "", "", "", "")
	conf := defaultConf
	conf.FreeSpace = 0.9999
	store := NewCachedStore(mem, conf, nil)
	testStore(t, store)
}

func TestStoreSmallBuffer(t *testing.T) {
	mem, _ := object.CreateStorage("mem", "", "", "", "")
	conf := defaultConf
	conf.BufferSize = 1 << 20
	store := NewCachedStore(mem, conf, nil)
	testStore(t, store)
}

func TestStoreAsync(t *testing.T) {
	mem, _ := object.CreateStorage("mem", "", "", "", "")
	conf := defaultConf
	conf.Writeback = true
	p := filepath.Join(conf.CacheDir, stagingDir, "chunks/0/0/123_0_4")
	os.MkdirAll(filepath.Dir(p), 0744)
	f, _ := os.Create(p)
	f.WriteString("good")
	f.Close()
	store := NewCachedStore(mem, conf, nil)
	time.Sleep(time.Millisecond * 50) // wait for scan to finish
	in, err := mem.Get(ctx, "chunks/0/0/123_0_4", 0, -1)
	if err != nil {
		t.Fatalf("staging object should be upload")
	}
	data, _ := io.ReadAll(in)
	if string(data) != "good" {
		t.Fatalf("data %s != expect good", data)
	}
	testStore(t, store)
}

func TestForceUpload(t *testing.T) {
	blob, _ := object.CreateStorage("mem", "", "", "", "")
	config := defaultConf
	_ = os.RemoveAll(config.CacheDir)
	config.Writeback = true
	config.WritebackThresholdSize = config.BlockSize + 1
	config.UploadDelay = time.Hour
	config.BlockSize = 4 << 20
	store := NewCachedStore(blob, config, nil)
	cleanCache := func() {
		rSlice := sliceForRead(1, 1024, store.(*cachedStore))
		keys := rSlice.keys()
		for _, k := range keys {
			store.(*cachedStore).bcache.remove(k, true)
		}
	}
	readSlice := func(id uint64, length int) error {
		p := NewPage(make([]byte, length))
		r := store.NewReader(id, length)
		_, err := r.ReadAt(context.Background(), p, 0)
		return err
	}

	// write to cache
	w := store.NewWriter(1, 0)
	if _, err := w.WriteAt(make([]byte, 1024), 0); err != nil {
		t.Fatalf("write fail: %s", err)
	}
	if err := w.Finish(1024); err != nil {
		t.Fatalf("write fail: %s", err)
	}
	cleanCache()
	if readSlice(1, 1024) == nil {
		t.Fatalf("read slice 1 should fail")
	}

	// write to os
	w = store.NewWriter(2, 0)
	w.SetWriteback(false)
	if _, err := w.WriteAt(make([]byte, 1024), 0); err != nil {
		t.Fatalf("write fail: %s", err)
	}
	if err := w.Finish(1024); err != nil {
		t.Fatalf("write fail: %s", err)
	}
	cleanCache()
	if readSlice(2, 1024) != nil {
		t.Fatalf("check slice 2 should success")
	}
}

func TestStoreDelayed(t *testing.T) {
	mem, _ := object.CreateStorage("mem", "", "", "", "")
	conf := defaultConf
	conf.Writeback = true
	conf.UploadDelay = time.Millisecond * 200
	store := NewCachedStore(mem, conf, nil)
	time.Sleep(time.Second) // waiting for cache scanned
	testStore(t, store)
	if err := forgetSlice(store, 10, 1024); err != nil {
		t.Fatalf("forge slice 10 1024: %s", err)
	}
	defer store.Remove(10, 1024)
	time.Sleep(time.Second) // waiting for upload
	if _, err := mem.Head(ctx, "chunks/0/0/10_0_1024"); err != nil {
		t.Fatalf("head object 10_0_1024: %s", err)
	}
}

func TestStoreMultiBuckets(t *testing.T) {
	mem, _ := object.CreateStorage("mem", "", "", "", "")
	conf := defaultConf
	conf.HashPrefix = true
	store := NewCachedStore(mem, conf, nil)
	testStore(t, store)
}

func TestFillCache(t *testing.T) {
	mem, _ := object.CreateStorage("mem", "", "", "", "")
	conf := defaultConf
	conf.CacheSize = 10 << 20
	conf.FreeSpace = 0.01
	_ = os.RemoveAll(conf.CacheDir)
	store := NewCachedStore(mem, conf, nil)
	if err := forgetSlice(store, 10, 1024); err != nil {
		t.Fatalf("forge slice 10 1024: %s", err)
	}
	defer store.Remove(10, 1024)
	bsize := conf.BlockSize
	if err := forgetSlice(store, 11, bsize); err != nil {
		t.Fatalf("forge slice 11 %d: %s", bsize, err)
	}
	defer store.Remove(11, bsize)

	time.Sleep(time.Millisecond * 100) // waiting for flush
	bcache := store.(*cachedStore).bcache
	if cnt, used := bcache.stats(); cnt != 1 || used != 1024+4096 { // only chunk 10 cached
		t.Fatalf("cache cnt %d used %d, expect cnt 1 used 5120", cnt, used)
	}
	if err := store.FillCache(10, 1024); err != nil {
		t.Fatalf("fill cache 10 1024: %s", err)
	}
	if err := store.FillCache(11, uint32(bsize)); err != nil {
		t.Fatalf("fill cache 11 %d: %s", bsize, err)
	}
	time.Sleep(time.Second)
	expect := int64(1024 + 4096 + bsize + 4096)
	if cnt, used := bcache.stats(); cnt != 2 || used != expect {
		t.Fatalf("cache cnt %d used %d, expect cnt 2 used %d", cnt, used, expect)
	}

	var missBytes uint64
	handler := func(exists bool, loc string, size int) {
		if !exists {
			missBytes += uint64(size)
		}
	}
	// check
	err := store.CheckCache(10, 1024, handler)
	assert.Nil(t, err)
	assert.Equal(t, uint64(0), missBytes)

	missBytes = 0
	err = store.CheckCache(11, uint32(bsize), handler)
	assert.Nil(t, err)
	assert.Equal(t, uint64(0), missBytes)

	// evict slice 11
	err = store.EvictCache(11, uint32(bsize))
	assert.Nil(t, err)

	// stat
	if cnt, used := bcache.stats(); cnt != 1 || used != 1024+4096 { // only chunk 10 cached
		t.Fatalf("cache cnt %d used %d, expect cnt 1 used 5120", cnt, used)
	}

	// check again
	missBytes = 0
	err = store.CheckCache(11, uint32(bsize), handler)
	assert.Nil(t, err)
	assert.Equal(t, uint64(bsize), missBytes)
}

func BenchmarkCachedRead(b *testing.B) {
	blob, _ := object.CreateStorage("mem", "", "", "", "")
	config := defaultConf
	config.BlockSize = 4 << 20
	store := NewCachedStore(blob, config, nil)
	w := store.NewWriter(1, 0)
	if _, err := w.WriteAt(make([]byte, 1024), 0); err != nil {
		b.Fatalf("write fail: %s", err)
	}
	if err := w.Finish(1024); err != nil {
		b.Fatalf("write fail: %s", err)
	}
	time.Sleep(time.Millisecond * 100)
	p := NewPage(make([]byte, 1024))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := store.NewReader(1, 1024)
		if n, err := r.ReadAt(context.Background(), p, 0); err != nil || n != 1024 {
			b.FailNow()
		}
	}
}

func BenchmarkUncachedRead(b *testing.B) {
	blob, _ := object.CreateStorage("mem", "", "", "", "")
	config := defaultConf
	config.BlockSize = 4 << 20
	config.CacheSize = 0
	store := NewCachedStore(blob, config, nil)
	w := store.NewWriter(2, 0)
	if _, err := w.WriteAt(make([]byte, 1024), 0); err != nil {
		b.Fatalf("write fail: %s", err)
	}
	if err := w.Finish(1024); err != nil {
		b.Fatalf("write fail: %s", err)
	}
	p := NewPage(make([]byte, 1024))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := store.NewReader(2, 1024)
		if n, err := r.ReadAt(context.Background(), p, 0); err != nil || n != 1024 {
			b.FailNow()
		}
	}
}

type dStore struct {
	object.ObjectStorage
	cnt int32
}

func (s *dStore) Get(ctx context.Context, key string, off, limit int64, getters ...object.AttrGetter) (io.ReadCloser, error) {
	atomic.AddInt32(&s.cnt, 1)
	return nil, errors.New("not found")
}

func TestStoreRetry(t *testing.T) {
	s := &dStore{}
	cs := NewCachedStore(s, defaultConf, nil)
	p := NewPage(nil)
	defer p.Release()
	cs.(*cachedStore).load(context.TODO(), "non", p, false, false) // wont retry
	require.Equal(t, int32(1), s.cnt)
}

func TestRemoteCacheConfigValidation(t *testing.T) {
	conf := defaultConf
	conf.RemoteCacheMode = "bad"
	conf.SelfCheck("test-uuid")

	require.Equal(t, "none", conf.RemoteCacheMode)
	require.Equal(t, 50*time.Millisecond, conf.RemoteCacheTimeout)
}

type countingStore struct {
	object.ObjectStorage
	gets atomic.Int32
}

func (s *countingStore) Get(ctx context.Context, key string, off, limit int64, getters ...object.AttrGetter) (io.ReadCloser, error) {
	s.gets.Add(1)
	return s.ObjectStorage.Get(ctx, key, off, limit, getters...)
}

type failingRemoteCache struct{}

func (f failingRemoteCache) Get(ctx context.Context, key string, off, size int) (io.ReadCloser, error) {
	return nil, errors.New("remote cache failed")
}

func (f failingRemoteCache) Put(ctx context.Context, key string, data []byte) error {
	return errors.New("remote cache failed")
}

func (f failingRemoteCache) Delete(ctx context.Context, key string) error {
	return nil
}

func (f failingRemoteCache) Close() error {
	return nil
}

type unavailableRemoteCache struct{}

func (u unavailableRemoteCache) Get(ctx context.Context, key string, off, size int) (io.ReadCloser, error) {
	return nil, remote.ErrUnavailable
}

func (u unavailableRemoteCache) Put(ctx context.Context, key string, data []byte) error {
	return remote.ErrUnavailable
}

func (u unavailableRemoteCache) Delete(ctx context.Context, key string) error {
	return remote.ErrUnavailable
}

func (u unavailableRemoteCache) Close() error {
	return nil
}

type missPutFailingRemoteCache struct{}

func (f missPutFailingRemoteCache) Get(ctx context.Context, key string, off, size int) (io.ReadCloser, error) {
	return nil, remote.ErrMiss
}

func (f missPutFailingRemoteCache) Put(ctx context.Context, key string, data []byte) error {
	return errors.New("remote cache put failed")
}

func (f missPutFailingRemoteCache) Delete(ctx context.Context, key string) error {
	return nil
}

func (f missPutFailingRemoteCache) Close() error {
	return nil
}

func TestRemoteCacheHitAvoidsObjectStorage(t *testing.T) {
	blob, _ := object.CreateStorage("mem", "", "", "", "")
	counting := &countingStore{ObjectStorage: blob}
	conf := defaultConf
	conf.CacheSize = 0
	conf.RemoteCacheMode = "mock"
	store := NewCachedStore(counting, conf, nil).(*cachedStore)

	key := "chunks/0/0/123_0_4"
	require.NoError(t, store.remoteCache.Put(context.Background(), key, []byte("good")))

	p := NewPage(make([]byte, 4))
	defer p.Release()
	require.NoError(t, store.load(context.Background(), key, p, false, false))
	require.Equal(t, []byte("good"), p.Data)
	require.Equal(t, int32(0), counting.gets.Load())
}

func TestRemoteCacheHitFillsLocalCacheWhenEnabled(t *testing.T) {
	blob, _ := object.CreateStorage("mem", "", "", "", "")
	counting := &countingStore{ObjectStorage: blob}
	conf := defaultConf
	conf.CacheDir = "memory"
	conf.RemoteCacheMode = "mock"
	conf.RemoteCacheFillLocal = true
	store := NewCachedStore(counting, conf, nil).(*cachedStore)

	key := "chunks/0/0/131_0_4"
	require.NoError(t, store.remoteCache.Put(context.Background(), key, []byte("warm")))

	p := NewPage(make([]byte, 4))
	defer p.Release()
	require.NoError(t, store.load(context.Background(), key, p, true, false))
	require.Equal(t, []byte("warm"), p.Data)
	require.Equal(t, int32(0), counting.gets.Load())

	cached, err := store.bcache.load(key)
	require.NoError(t, err)
	defer cached.Close()
	cachedData := make([]byte, 4)
	_, err = cached.ReadAt(cachedData, 0)
	require.NoError(t, err)
	require.Equal(t, []byte("warm"), cachedData)
}

func TestRemoteCacheHitDoesNotFillLocalCacheWhenDisabled(t *testing.T) {
	blob, _ := object.CreateStorage("mem", "", "", "", "")
	counting := &countingStore{ObjectStorage: blob}
	conf := defaultConf
	conf.CacheDir = "memory"
	conf.RemoteCacheMode = "mock"
	conf.RemoteCacheFillLocal = false
	store := NewCachedStore(counting, conf, nil).(*cachedStore)

	key := "chunks/0/0/132_0_4"
	require.NoError(t, store.remoteCache.Put(context.Background(), key, []byte("skip")))

	p := NewPage(make([]byte, 4))
	defer p.Release()
	require.NoError(t, store.load(context.Background(), key, p, true, false))
	require.Equal(t, []byte("skip"), p.Data)
	require.Equal(t, int32(0), counting.gets.Load())

	_, err := store.bcache.load(key)
	require.ErrorIs(t, err, errNotCached)
}

func TestRemoteCacheMissFallsBackToObjectStorageAndFillsRemote(t *testing.T) {
	blob, _ := object.CreateStorage("mem", "", "", "", "")
	key := "chunks/0/0/124_0_4"
	require.NoError(t, blob.Put(context.Background(), key, bytes.NewReader([]byte("cold"))))
	counting := &countingStore{ObjectStorage: blob}
	conf := defaultConf
	conf.CacheSize = 0
	conf.RemoteCacheMode = "mock"
	conf.RemoteCacheFillRemote = true
	store := NewCachedStore(counting, conf, nil).(*cachedStore)

	p := NewPage(make([]byte, 4))
	defer p.Release()
	require.NoError(t, store.load(context.Background(), key, p, false, false))
	require.Equal(t, []byte("cold"), p.Data)
	require.Equal(t, int32(1), counting.gets.Load())

	require.Eventually(t, func() bool {
		in, err := store.remoteCache.Get(context.Background(), key, 0, 4)
		if err != nil {
			return false
		}
		defer in.Close()
		data, err := io.ReadAll(in)
		return err == nil && bytes.Equal([]byte("cold"), data)
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, int32(1), counting.gets.Load())
}

func TestRemoteCacheMissDoesNotFillRemoteWhenDisabled(t *testing.T) {
	blob, _ := object.CreateStorage("mem", "", "", "", "")
	key := "chunks/0/0/133_0_4"
	require.NoError(t, blob.Put(context.Background(), key, bytes.NewReader([]byte("cold"))))
	counting := &countingStore{ObjectStorage: blob}
	conf := defaultConf
	conf.CacheSize = 0
	conf.RemoteCacheMode = "mock"
	conf.RemoteCacheFillRemote = false
	store := NewCachedStore(counting, conf, nil).(*cachedStore)

	p := NewPage(make([]byte, 4))
	defer p.Release()
	require.NoError(t, store.load(context.Background(), key, p, false, false))
	require.Equal(t, []byte("cold"), p.Data)
	require.Equal(t, int32(1), counting.gets.Load())

	p2 := NewPage(make([]byte, 4))
	defer p2.Release()
	require.NoError(t, store.load(context.Background(), key, p2, false, false))
	require.Equal(t, []byte("cold"), p2.Data)
	require.Equal(t, int32(2), counting.gets.Load())

	_, err := store.remoteCache.Get(context.Background(), key, 0, 4)
	require.ErrorIs(t, err, remote.ErrMiss)
}

func TestRemoteCacheErrorFallsBackToObjectStorage(t *testing.T) {
	blob, _ := object.CreateStorage("mem", "", "", "", "")
	require.NoError(t, blob.Put(context.Background(), "chunks/0/0/125_0_4", bytes.NewReader([]byte("safe"))))
	counting := &countingStore{ObjectStorage: blob}
	conf := defaultConf
	conf.CacheSize = 0
	conf.RemoteCacheMode = "mock"
	store := NewCachedStore(counting, conf, nil).(*cachedStore)
	store.remoteCache = failingRemoteCache{}

	p := NewPage(make([]byte, 4))
	defer p.Release()
	require.NoError(t, store.load(context.Background(), "chunks/0/0/125_0_4", p, false, false))
	require.Equal(t, []byte("safe"), p.Data)
	require.Equal(t, int32(1), counting.gets.Load())
}

func TestRemoteCacheUnavailableFallsBackToObjectStorage(t *testing.T) {
	blob, _ := object.CreateStorage("mem", "", "", "", "")
	key := "chunks/0/0/135_0_4"
	require.NoError(t, blob.Put(context.Background(), key, bytes.NewReader([]byte("safe"))))
	counting := &countingStore{ObjectStorage: blob}
	conf := defaultConf
	conf.CacheSize = 0
	conf.RemoteCacheMode = "mock"
	store := NewCachedStore(counting, conf, nil).(*cachedStore)
	store.remoteCache = unavailableRemoteCache{}

	p := NewPage(make([]byte, 4))
	defer p.Release()
	require.NoError(t, store.load(context.Background(), key, p, false, false))
	require.Equal(t, []byte("safe"), p.Data)
	require.Equal(t, int32(1), counting.gets.Load())
}

func TestRemoteCachePutErrorDoesNotFailObjectStorageRead(t *testing.T) {
	blob, _ := object.CreateStorage("mem", "", "", "", "")
	key := "chunks/0/0/134_0_4"
	require.NoError(t, blob.Put(context.Background(), key, bytes.NewReader([]byte("safe"))))
	counting := &countingStore{ObjectStorage: blob}
	conf := defaultConf
	conf.CacheSize = 0
	conf.RemoteCacheMode = "mock"
	conf.RemoteCacheFillRemote = true
	store := NewCachedStore(counting, conf, nil).(*cachedStore)
	store.remoteCache = missPutFailingRemoteCache{}

	p := NewPage(make([]byte, 4))
	defer p.Release()
	require.NoError(t, store.load(context.Background(), key, p, false, false))
	require.Equal(t, []byte("safe"), p.Data)
	require.Equal(t, int32(1), counting.gets.Load())
}

func TestRemoteCacheHTTPHitAvoidsObjectStorage(t *testing.T) {
	blob, _ := object.CreateStorage("mem", "", "", "", "")
	counting := &countingStore{ObjectStorage: blob}
	backend := mock.NewClient()
	server := httptest.NewServer(httpcache.NewHandler(backend))
	defer server.Close()

	key := "chunks/0/0/126_0_4"
	require.NoError(t, backend.Put(context.Background(), key, []byte("rdma")))

	conf := defaultConf
	conf.CacheSize = 0
	conf.RemoteCacheMode = "rdma"
	conf.RemoteCacheNodes = server.URL
	store := NewCachedStore(counting, conf, nil).(*cachedStore)

	p := NewPage(make([]byte, 4))
	defer p.Release()
	require.NoError(t, store.load(context.Background(), key, p, false, false))
	require.Equal(t, []byte("rdma"), p.Data)
	require.Equal(t, int32(0), counting.gets.Load())
}

func TestRemoteCacheRDMAModeWithoutNodesFallsBackToObjectStorage(t *testing.T) {
	blob, _ := object.CreateStorage("mem", "", "", "", "")
	key := "chunks/0/0/127_0_4"
	require.NoError(t, blob.Put(context.Background(), key, bytes.NewReader([]byte("cold"))))
	counting := &countingStore{ObjectStorage: blob}
	conf := defaultConf
	conf.CacheSize = 0
	conf.RemoteCacheMode = "rdma"
	store := NewCachedStore(counting, conf, nil).(*cachedStore)

	p := NewPage(make([]byte, 4))
	defer p.Release()
	require.NoError(t, store.load(context.Background(), key, p, false, false))
	require.Equal(t, []byte("cold"), p.Data)
	require.Equal(t, int32(1), counting.gets.Load())
}

func TestRemoteCacheHTTPTimeoutFallsBackToObjectStorage(t *testing.T) {
	blob, _ := object.CreateStorage("mem", "", "", "", "")
	key := "chunks/0/0/128_0_4"
	require.NoError(t, blob.Put(context.Background(), key, bytes.NewReader([]byte("safe"))))
	counting := &countingStore{ObjectStorage: blob}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("slow"))
	}))
	defer server.Close()

	conf := defaultConf
	conf.CacheSize = 0
	conf.RemoteCacheMode = "rdma"
	conf.RemoteCacheNodes = server.URL
	conf.RemoteCacheTimeout = 10 * time.Millisecond
	store := NewCachedStore(counting, conf, nil).(*cachedStore)

	p := NewPage(make([]byte, 4))
	defer p.Release()
	require.NoError(t, store.load(context.Background(), key, p, false, false))
	require.Equal(t, []byte("safe"), p.Data)
	require.Equal(t, int32(1), counting.gets.Load())
}

func TestRemoteCacheHTTPReplicasFallbackToHealthyNode(t *testing.T) {
	blob, _ := object.CreateStorage("mem", "", "", "", "")
	counting := &countingStore{ObjectStorage: blob}
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer failing.Close()
	backend := mock.NewClient()
	key := "chunks/0/0/129_0_4"
	require.NoError(t, backend.Put(context.Background(), key, []byte("copy")))
	healthy := httptest.NewServer(httpcache.NewHandler(backend))
	defer healthy.Close()

	conf := defaultConf
	conf.CacheSize = 0
	conf.RemoteCacheMode = "rdma"
	conf.RemoteCacheNodes = failing.URL + "," + healthy.URL
	conf.RemoteCacheReplicas = 2
	store := NewCachedStore(counting, conf, nil).(*cachedStore)

	p := NewPage(make([]byte, 4))
	defer p.Release()
	require.NoError(t, store.load(context.Background(), key, p, false, false))
	require.Equal(t, []byte("copy"), p.Data)
	require.Equal(t, int32(0), counting.gets.Load())
}

func TestRemoteCacheRDMATransportFallsBackToObjectStorage(t *testing.T) {
	blob, _ := object.CreateStorage("mem", "", "", "", "")
	key := "chunks/0/0/130_0_4"
	require.NoError(t, blob.Put(context.Background(), key, bytes.NewReader([]byte("safe"))))
	counting := &countingStore{ObjectStorage: blob}
	conf := defaultConf
	conf.CacheSize = 0
	conf.RemoteCacheMode = "rdma"
	conf.RemoteCacheTransport = "rdma"
	conf.RemoteCacheNodes = "127.0.0.1:9568"
	store := NewCachedStore(counting, conf, nil).(*cachedStore)

	p := NewPage(make([]byte, 4))
	defer p.Release()
	require.NoError(t, store.load(context.Background(), key, p, false, false))
	require.Equal(t, []byte("safe"), p.Data)
	require.Equal(t, int32(1), counting.gets.Load())
}
