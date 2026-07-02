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
	"encoding/json"

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
