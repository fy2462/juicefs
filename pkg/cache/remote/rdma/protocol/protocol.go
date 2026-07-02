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

package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/juicedata/juicefs/pkg/cache/remote"
)

type Op string

const (
	OpGet    Op = "GET"
	OpPut    Op = "PUT"
	OpDelete Op = "DELETE"
)

type Status string

const (
	StatusOK          Status = "OK"
	StatusMiss        Status = "MISS"
	StatusUnavailable Status = "UNAVAILABLE"
	StatusBadRequest  Status = "BAD_REQUEST"
)

type Request struct {
	Op      Op     `json:"op"`
	Key     string `json:"key"`
	Off     int    `json:"off,omitempty"`
	Size    int    `json:"size,omitempty"`
	Payload []byte `json:"payload,omitempty"`
}

type Response struct {
	Status  Status `json:"status"`
	Payload []byte `json:"payload,omitempty"`
}

type Executor struct {
	Backend remote.Client
}

func EncodeRequest(req Request) ([]byte, error) {
	return json.Marshal(req)
}

func DecodeRequest(data []byte) (Request, error) {
	var req Request
	err := json.Unmarshal(data, &req)
	return req, err
}

func EncodeResponse(resp Response) ([]byte, error) {
	return json.Marshal(resp)
}

func DecodeResponse(data []byte) (Response, error) {
	var resp Response
	err := json.Unmarshal(data, &resp)
	return resp, err
}

func StatusToError(status Status) error {
	switch status {
	case StatusOK:
		return nil
	case StatusMiss:
		return remote.ErrMiss
	default:
		return remote.ErrUnavailable
	}
}

func (e Executor) Handle(ctx context.Context, req Request) Response {
	if e.Backend == nil {
		return Response{Status: StatusUnavailable}
	}
	switch req.Op {
	case OpGet:
		return e.get(ctx, req)
	case OpPut:
		if err := e.Backend.Put(ctx, req.Key, req.Payload); err != nil {
			return Response{Status: statusFromError(err)}
		}
		return Response{Status: StatusOK}
	case OpDelete:
		if err := e.Backend.Delete(ctx, req.Key); err != nil {
			return Response{Status: statusFromError(err)}
		}
		return Response{Status: StatusOK}
	default:
		return Response{Status: StatusBadRequest}
	}
}

func (e Executor) get(ctx context.Context, req Request) Response {
	reader, err := e.Backend.Get(ctx, req.Key, req.Off, req.Size)
	if err != nil {
		return Response{Status: statusFromError(err)}
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		return Response{Status: StatusUnavailable}
	}
	return Response{Status: StatusOK, Payload: data}
}

func statusFromError(err error) Status {
	if errors.Is(err, remote.ErrMiss) {
		return StatusMiss
	}
	return StatusUnavailable
}
