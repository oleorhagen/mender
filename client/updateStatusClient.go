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
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/mendersoftware/log"
	"github.com/pkg/errors"
)

type UpdateStatusSubmitter interface {
	Submit(api ApiRequester, server string, data interface{}) error
}

type UpdateStatusClient struct {
}

type UpdateStatus string

const (
	ScriptStarted  UpdateStatus = "started"
	ScriptFinished UpdateStatus = "finished"
	ScriptError    UpdateStatus = "error"
	StateEntered   UpdateStatus = "state-entered"
	StateFinished  UpdateStatus = "state-finished"
)

// UpdateStatusData holds the update-status data reported to the backend
// during an update process
type UpdateStatusData struct {
	State      string       `json:"client_state"`
	ScriptName string       `json:"script_name,omitempty"`
	Status     UpdateStatus `json:"script_status"`
}

func NewUpdateStatus() InventorySubmitter {
	return &UpdateStatusClient{}
}

// Submit reports status information to the backend
func (i *UpdateStatusClient) Submit(api ApiRequester, url string, data interface{}) error {
	req, err := makeUpdateStatusRequest(url, data)
	if err != nil {
		return errors.Wrapf(err, "failed to prepare update-status submit request")
	}

	r, err := api.Do(req)
	if err != nil {
		log.Error("failed to submit update-status data: ", err)
		return errors.Wrapf(err, "inventory submit failed")
	}

	defer r.Body.Close()

	if r.StatusCode != http.StatusOK {
		log.Errorf("got unexpected HTTP status when submitting to update-status: %v", r.StatusCode)
		return errors.Errorf("inventory submit failed, bad status %v", r.StatusCode)
	}
	log.Debugf("update-status sent, response %v", r)

	return nil
}

func makeUpdateStatusRequest(server string, data interface{}) (*http.Request, error) {
	url := buildApiURL(server, fmt.Sprintf("/device/deployments/{%s}/status", "10")) // TODO - need to add the id

	out := &bytes.Buffer{}
	if err := json.NewEncoder(out).Encode(&data); err != nil {
		return nil, err
	}
	fmt.Println(out)

	hreq, err := http.NewRequest(http.MethodPatch, url, out)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to create update-status HTTP request")
	}
	fmt.Println("hreq")
	fmt.Println(hreq)

	hreq.Header.Add("Content-Type", "application/json")
	return hreq, nil
}
