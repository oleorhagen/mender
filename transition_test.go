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
package main

import (
	"errors"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mendersoftware/mender/client"
	"github.com/stretchr/testify/assert"
)

type testState struct {
	t                Transition
	shouldErrorEnter bool
	shouldErrorLeave bool
	shouldErrorError bool
	next             State
}

func (s *testState) Handle(ctx *StateContext, c Controller) (State, bool) {
	return s.next, false
}

func (s *testState) Cancel() bool { return true }

func (s *testState) Id() MenderState { return MenderStateInit }

func (s *testState) Transition() Transition        { return s.t }
func (s *testState) SetTransition(tran Transition) { s.t = tran }

type stateScript struct {
	state  string
	action string
}

type testExecutor struct {
	executed   []stateScript
	execErrors map[stateScript]bool
}

func (te *testExecutor) ExecuteAll(state, action string, ignoreError bool, req client.ApiRequester, serverURL string) error {
	te.executed = append(te.executed, stateScript{state, action})

	if _, ok := te.execErrors[stateScript{state, action}]; ok {
		if ignoreError {
			return nil
		}
		return errors.New("error executing script")
	}
	return nil
}

func (te *testExecutor) CheckRootfsScriptsVersion() error {
	return nil
}

func (te *testExecutor) setExecError(state *testState) {
	if state.shouldErrorEnter {
		te.execErrors[stateScript{state.Transition().String(), "Enter"}] = true
	}
	if state.shouldErrorLeave {
		te.execErrors[stateScript{state.Transition().String(), "Leave"}] = true
	}
	if state.shouldErrorError {
		te.execErrors[stateScript{state.Transition().String(), "Error"}] = true
	}
}

func (te *testExecutor) verifyExecuted(should []stateScript) bool {
	if len(should) != len(te.executed) {
		return false
	}
	for i, _ := range te.executed {
		if should[i] != te.executed[i] {
			return false
		}
	}
	return true
}

func TestTransitions(t *testing.T) {

	responder := &struct {
		httpStatus int
		reqdata    [][]byte
		path       string
	}{
		http.StatusOK,
		[][]byte{},
		"",
	}

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(responder.httpStatus)

		data, _ := ioutil.ReadAll(r.Body)
		responder.reqdata = append(responder.reqdata, data)
		responder.path = r.URL.Path
	}))
	defer ts.Close()

	mender, err := NewMender(menderConfig{ServerURL: ts.URL, ServerCertificate: "client/server.crt"}, MenderPieces{})
	assert.NoError(t, err)

	tc := []struct {
		from            *testState
		to              *testState
		expectedT       []stateScript
		expectedS       State
		expectedRequest [][]byte
	}{
		{from: &testState{t: ToIdle},
			to:        &testState{t: ToSync, next: initState},
			expectedT: []stateScript{{"Idle", "Leave"}, {"Sync", "Enter"}},
			expectedS: &InitState{},
			expectedRequest: [][]byte{
				[]byte(`{"client_state":"Sync","script_status":"state-entered"}`),
				[]byte(`{"client_state":"Sync","script_status":"state-finished"}`)},
		},
		// idle error should have no effect
		{from: &testState{t: ToIdle, shouldErrorLeave: true},
			to:        &testState{t: ToSync, next: initState},
			expectedT: []stateScript{{"Idle", "Leave"}, {"Sync", "Enter"}},
			expectedS: &InitState{},
			expectedRequest: [][]byte{
				[]byte(`{"client_state":"Sync","script_status":"state-entered"}`),
				[]byte(`{"client_state":"Sync","script_status":"state-finished"}`)},
		},
		{from: &testState{t: ToIdle},
			to:        &testState{t: ToSync, shouldErrorEnter: true, next: initState},
			expectedT: []stateScript{{"Idle", "Leave"}, {"Sync", "Enter"}},
			expectedS: &ErrorState{},
		},
		{from: &testState{t: ToSync, shouldErrorLeave: true},
			to:        &testState{t: ToDownload, next: initState},
			expectedT: []stateScript{{"Sync", "Leave"}},
			expectedS: &ErrorState{},
		},
		{from: &testState{t: ToError},
			to:        &testState{t: ToIdle, next: initState},
			expectedT: []stateScript{{"Error", "Leave"}, {"Idle", "Enter"}},
			expectedS: &InitState{},
			expectedRequest: [][]byte{
				[]byte(`{"client_state":"Idle","script_status":"state-entered"}`),
				[]byte(`{"client_state":"Idle","script_status":"state-finished"}`)},
		},
	}

	for _, tt := range tc {
		tt.from.next = tt.to

		te := &testExecutor{
			executed:   make([]stateScript, 0),
			execErrors: make(map[stateScript]bool),
		}
		te.setExecError(tt.from)
		te.setExecError(tt.to)

		mender.stateScriptExecutor = te
		mender.SetNextState(tt.from)

		s, c := mender.TransitionState(tt.to, nil)
		if tt.expectedRequest != nil {
			assert.JSONEq(t, string(tt.expectedRequest[0]), string(responder.reqdata[0]))
			assert.JSONEq(t, string(tt.expectedRequest[1]), string(responder.reqdata[1]))
			responder.reqdata = [][]byte{} // reset the server data
		}
		assert.IsType(t, tt.expectedS, s)
		assert.False(t, c)

		t.Logf("has: %v expect: %v\n", te.executed, tt.expectedT)
		assert.True(t, te.verifyExecuted(tt.expectedT))

	}
}

func TestGetName(t *testing.T) {
	assert.Equal(t, "Sync", getName(ToSync, "Enter"))
	assert.Equal(t, "",
		getName(ToArtifactRollbackReboot_Enter, "Leave"))
	assert.Equal(t, "ArtifactRollbackReboot",
		getName(ToArtifactRollbackReboot_Enter, "Error"))
	assert.Equal(t, "ArtifactRollbackReboot",
		getName(ToArtifactRollbackReboot_Enter, "Enter"))
	assert.Equal(t, "ArtifactRollbackReboot",
		getName(ToArtifactRollbackReboot_Leave, "Leave"))
	assert.Equal(t, "ArtifactRollbackReboot",
		getName(ToArtifactRollbackReboot_Leave, "Error"))
}

type checkIgnoreErrorsExecutor struct {
	shouldIgnore bool
}

func (e *checkIgnoreErrorsExecutor) ExecuteAll(state, action string, ignoreError bool, req client.ApiRequester, serverURL string) error {
	if e.shouldIgnore == ignoreError {
		return nil
	}
	return errors.New("should ignore errors, but is not")
}

func (e *checkIgnoreErrorsExecutor) CheckRootfsScriptsVersion() error {
	return nil
}

func TestIgnoreErrors(t *testing.T) {
	e := checkIgnoreErrorsExecutor{false}
	tr := ToArtifactReboot_Leave

	ac, err := client.NewApiClient(client.Config{})
	assert.NoError(t, err)
	err = tr.Leave(&e, ac.Request("authtoken"), "http://server.com") // dummy request and server
	assert.NoError(t, err)

	e = checkIgnoreErrorsExecutor{false}
	tr = ToArtifactCommit
	err = tr.Enter(&e, ac.Request("authtoken"), "http://server.com") // dummy request and server
	assert.NoError(t, err)

	e = checkIgnoreErrorsExecutor{true}
	tr = ToIdle
	err = tr.Enter(&e, ac.Request("authtoken"), "http://server.com") // dummy request and server
	assert.NoError(t, err)
}
