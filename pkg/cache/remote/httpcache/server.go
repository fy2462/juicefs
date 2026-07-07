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
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/juicedata/juicefs/pkg/cache/remote"
)

func NewHandler(cache remote.Client) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			if r.Method != http.MethodGet {
				w.Header().Set("Allow", "GET")
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		key, ok := strings.CutPrefix(r.URL.Path, "/cache/")
		if !ok || key == "" {
			http.NotFound(w, r)
			return
		}
		key, err := url.PathUnescape(key)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		switch r.Method {
		case http.MethodGet:
			get(w, r, cache, key)
		case http.MethodPut:
			put(w, r, cache, key)
		case http.MethodDelete:
			del(w, r, cache, key)
		default:
			w.Header().Set("Allow", "GET, PUT, DELETE")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
}

func get(w http.ResponseWriter, r *http.Request, cache remote.Client, key string) {
	off, err := intQuery(r, "off", 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	size, err := intQuery(r, "size", -1)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	reader, err := cache.Get(r.Context(), key, off, size)
	if err != nil {
		writeCacheError(w, err)
		return
	}
	defer reader.Close()
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, reader)
}

func put(w http.ResponseWriter, r *http.Request, cache remote.Client, key string) {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := cache.Put(r.Context(), key, data); err != nil {
		writeCacheError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func del(w http.ResponseWriter, r *http.Request, cache remote.Client, key string) {
	if err := cache.Delete(r.Context(), key); err != nil {
		writeCacheError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func intQuery(r *http.Request, name string, defaultValue int) (int, error) {
	value := r.URL.Query().Get(name)
	if value == "" {
		return defaultValue, nil
	}
	return strconv.Atoi(value)
}

func writeCacheError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, remote.ErrMiss):
		http.Error(w, err.Error(), http.StatusNotFound)
	default:
		http.Error(w, remote.ErrUnavailable.Error(), http.StatusServiceUnavailable)
	}
}
