// Copyright 2017 Northern.tech AS
//
//    Licensed under the Apache License, Version 2.0 (the "License");
//    you may not use this file except in compliance with the License.
//    You may obtain a copy of the License at
//
//        http://www.apache.org/licenses/LICENSE-2.0
//
//    Unless required by applicable law or agreed to in writing, software
//    distributed under the License is distributed on an "AS IS" BASIS,
//    WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//    See the License for the specific language governing permissions and
//    limitations under the License.
package client

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"

	"github.com/mendersoftware/log"
	"github.com/pkg/errors"
)

type LogUploader interface {
	Upload(api ApiRequester, server string, logs LogData) error
}

type LogData struct {
	DeploymentID string `json:"-"`
	Messages     []byte `json:"messages"`
}

type LogUploadClient struct {
}

func NewLog() LogUploader {
	return &LogUploadClient{}
}

// Report status information to the backend
func (u *LogUploadClient) Upload(api ApiRequester, url string, logs LogData) error {
	req, err := makeLogUploadRequest(url, &logs)
	if err != nil {
		return errors.Wrapf(err, "failed to prepare log upload request")
	}

	r, err := api.Do(req)
	if err != nil {
		log.Error("failed to upload logs: ", err)
		return errors.Wrapf(err, "uploading logs failed")
	}

	defer r.Body.Close()

	// HTTP 204 No Content
	if r.StatusCode != http.StatusNoContent {
		log.Errorf("got unexpected HTTP status when uploading log: %v", r.StatusCode)
		return errors.Errorf("uploading logs failed, bad status %v", r.StatusCode)
	}
	log.Debugf("logs uploaded, response %v", r)

	return nil
}

func makeLogUploadRequest(server string, logs *LogData) (*http.Request, error) {
	path := fmt.Sprintf("/deployments/device/deployments/%s/log",
		logs.DeploymentID)
	url := buildApiURL(server, path)

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := io.Copy(zw, bytes.NewReader(logs.Messages)); err != nil {
		return nil, errors.Wrap(err, "failed to compress the request body")
	}
	if err := zw.Close(); err != nil {
		return nil, errors.Wrap(err, "failed to close gzip writer")
	}

	hreq, err := http.NewRequest(http.MethodPut, url, &buf)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create log sending HTTP request")
	}

	hreq.Header.Add("Content-Encoding", "gzip")
	hreq.Header.Add("Content-Type", "application/json")
	return hreq, nil
}
