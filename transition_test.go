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
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/mendersoftware/log"
	"github.com/mendersoftware/mender/client"
	cltest "github.com/mendersoftware/mender/client/test"
	"github.com/mendersoftware/mender/store"
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

type spontanaeousRebootExecutor struct {
	expectedActions []string // test colouring
}

var panicFlag = false

func (sre *spontanaeousRebootExecutor) ExecuteAll(state, action string, ignoreError bool) error {
	log.Info("Executing all in spont-reboot")
	sre.expectedActions = append(sre.expectedActions, action)
	panicFlag = !panicFlag // flip
	if panicFlag {
		panic(fmt.Sprintf("state: %v action: %v", state, action))
	}
	return nil
}

func (te *spontanaeousRebootExecutor) CheckRootfsScriptsVersion() error {
	return nil
}

type testExecutor struct {
	executed   []stateScript
	execErrors map[stateScript]bool
}

func (te *testExecutor) ExecuteAll(state, action string, ignoreError bool) error {
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

func TestSpontanaeousReboot(t *testing.T) {

	// create temp dir
	td, _ := ioutil.TempDir("", "mender-install-update-")
	defer os.RemoveAll(td)

	// needed for the artifactInstaller
	deviceType := path.Join(td, "device_type")

	ioutil.WriteFile(deviceType, []byte("device_type=vexpress-qemu\n"), 0644)

	// prepare fake artifactInfo file
	// artifactInfo := path.Join(td, "artifact_info")
	// prepare fake device type file
	// deviceType := path.Join(td, "device_type")

	atok := client.AuthToken("authorized")
	authMgr := &testAuthManager{
		authorized: true,
		authtoken:  atok,
	}

	srv := cltest.NewClientTestServer()
	defer srv.Close()

	mender := newTestMender(nil,
		menderConfig{
			ServerURL: srv.URL,
		},
		testMenderPieces{
			MenderPieces: MenderPieces{
				device:  &fakeDevice{consumeUpdate: true},
				authMgr: authMgr,
			},
		},
	)
	mender.deviceTypeFile = deviceType

	ctx := StateContext{store: store.NewMemStore()}

	// update-response
	updateResponse := client.UpdateResponse{
		Artifact: struct {
			Source struct {
				URI    string
				Expire string
			}
			CompatibleDevices []string `json:"device_types_compatible"`
			ArtifactName      string   `json:"artifact_name"`
		}{
			Source: struct {
				URI    string
				Expire string
			}{
				URI: strings.Join([]string{srv.URL, "download"}, "/"),
			},
			CompatibleDevices: []string{"vexpress"},
			ArtifactName:      "foo",
		},
		ID: "foo",
	}

	// needed as we cannot recreate the state all on its own, without having a reader initialised.
	updateReader, err := MakeRootfsImageArtifact(1, false)
	assert.NoError(t, err)
	assert.NotNil(t, updateReader)
	// var size int64

	transitions := [][]struct {
		from                  State
		to                    State
		expectedStateData     *StateData
		expectedFromStateData RebootStateData
		expectedToStateData   RebootStateData
		transitionStatus      TransitionStatus
		expectedActions       []string
		modifyServer          func()
	}{
		{ // The code will step through a transition stepwise as a panic in executeAll will flip
			// every time it is run
			{
				// init -> idle
				// Fail in transition enter
				from:             initState,
				to:               idleState,
				transitionStatus: NoStatus,
				expectedStateData: &StateData{
					Version:          1, // standard version atm // FIXME export the field(?)
					FromState:        MenderStateInit,
					ToState:          MenderStateIdle,
					TransitionStatus: LeaveDone,
				},
				expectedFromStateData: RebootStateData{},
				expectedToStateData:   RebootStateData{},
				expectedActions:       []string{"Enter"},
			},
			{
				// finish enter and state
				from:             initState,
				to:               idleState,
				transitionStatus: LeaveDone,
				expectedStateData: &StateData{
					Version:          1,
					FromState:        MenderStateIdle,
					ToState:          MenderStateCheckWait,
					TransitionStatus: NoStatus,
				},
				expectedFromStateData: RebootStateData{},
				expectedToStateData:   RebootStateData{},
				expectedActions:       []string{"Enter"},
			},
		},
		// idleState -> checkWaitState
		{
			{
				// no transition done here
				from:             idleState,
				to:               checkWaitState,
				transitionStatus: NoStatus,
				expectedStateData: &StateData{
					Version:          1,
					FromState:        MenderStateCheckWait,
					ToState:          MenderStateInventoryUpdate,
					TransitionStatus: NoStatus,
				},
				expectedFromStateData: RebootStateData{},
				expectedToStateData:   RebootStateData{},
				expectedActions:       nil,
			},
			{
				// fail in idle-leave
				from:             checkWaitState,
				to:               inventoryUpdateState,
				transitionStatus: NoStatus,
				expectedStateData: &StateData{
					Version:          1,
					FromState:        MenderStateCheckWait,
					ToState:          MenderStateInventoryUpdate,
					TransitionStatus: NoStatus,
				},
				expectedFromStateData: RebootStateData{},
				expectedToStateData:   RebootStateData{},
				expectedActions:       []string{"Leave"},
			},
			{
				// finish idle-leave, fail in sync-enter
				from:             checkWaitState,
				to:               inventoryUpdateState,
				transitionStatus: NoStatus,
				expectedStateData: &StateData{
					Version:          1,
					FromState:        MenderStateCheckWait,
					ToState:          MenderStateInventoryUpdate,
					TransitionStatus: LeaveDone,
				},
				expectedFromStateData: RebootStateData{},
				expectedToStateData:   RebootStateData{},
				expectedActions:       []string{"Leave", "Enter"},
			},
			{
				// finish the transition
				from:             checkWaitState,
				to:               inventoryUpdateState,
				transitionStatus: LeaveDone,
				expectedStateData: &StateData{
					Version:          1,
					FromState:        MenderStateInventoryUpdate,
					ToState:          MenderStateCheckWait,
					TransitionStatus: NoStatus,
				},
				expectedFromStateData: RebootStateData{},
				expectedToStateData:   RebootStateData{},
				expectedActions:       []string{"Enter"},
			},
		},
		{
			// inv-update -> checkWait
			{
				// from invupdate to checkwait, fail leave
				from:             inventoryUpdateState,
				to:               checkWaitState,
				transitionStatus: NoStatus,
				expectedStateData: &StateData{
					Version:          1,
					FromState:        MenderStateInventoryUpdate,
					ToState:          MenderStateCheckWait,
					TransitionStatus: NoStatus,
				},
				expectedFromStateData: RebootStateData{},
				expectedToStateData:   RebootStateData{},
				expectedActions:       []string{"Leave"},
			},
			{
				// from invupdate to checkwait, finish leave, fail enter
				from:             inventoryUpdateState,
				to:               checkWaitState,
				transitionStatus: NoStatus,
				expectedStateData: &StateData{
					Version:          1,
					FromState:        MenderStateInventoryUpdate,
					ToState:          MenderStateCheckWait,
					TransitionStatus: LeaveDone,
				},
				expectedFromStateData: RebootStateData{},
				expectedToStateData:   RebootStateData{},
				expectedActions:       []string{"Leave", "Enter"},
			},
			{
				// from invupdate to checkwait, finish enter and state
				from:             inventoryUpdateState,
				to:               checkWaitState,
				transitionStatus: LeaveDone,
				expectedStateData: &StateData{
					Version:          1,
					FromState:        MenderStateCheckWait,
					ToState:          MenderStateUpdateCheck,
					TransitionStatus: NoStatus,
				},
				expectedFromStateData: RebootStateData{},
				expectedToStateData:   RebootStateData{},
				expectedActions:       []string{"Enter"},
			},
		},
		// checkwait -> updatecheck
		{
			{
				// fail chekwait leave
				from:             checkWaitState,
				to:               updateCheckState,
				transitionStatus: NoStatus,
				expectedStateData: &StateData{
					Version:          1,
					FromState:        MenderStateCheckWait,
					ToState:          MenderStateUpdateCheck,
					TransitionStatus: NoStatus,
				},
				expectedFromStateData: RebootStateData{},
				expectedToStateData:   RebootStateData{},
				expectedActions:       []string{"Leave"},
			},
			{
				// finish checkwait leave, fail updatecheck enter
				from:             checkWaitState,
				to:               updateCheckState,
				transitionStatus: NoStatus,
				expectedStateData: &StateData{
					Version:          1,
					FromState:        MenderStateCheckWait,
					ToState:          MenderStateUpdateCheck,
					TransitionStatus: LeaveDone,
				},
				expectedFromStateData: RebootStateData{},
				expectedToStateData:   RebootStateData{},
				expectedActions:       []string{"Leave", "Enter"},
			},
			{
				// finish updatecheck enter and handle updatecheck state
				// use a fakeupdater to return an update
				modifyServer: func() {
					mender.updater = fakeUpdater{
						GetScheduledUpdateReturnIface: updateResponse, // use a premade response
					}
				},
				from:             checkWaitState,
				to:               updateCheckState,
				transitionStatus: LeaveDone,
				expectedStateData: &StateData{
					Version:          1,
					FromState:        MenderStateUpdateCheck,
					ToState:          MenderStateUpdateFetch,
					TransitionStatus: NoStatus,
				},
				expectedFromStateData: RebootStateData{},
				expectedToStateData: RebootStateData{
					UpdateInfo: updateResponse,
				},
				expectedActions: []string{"Enter"},
			},
		},
		// update-check -> update-fetch
		{
			{
				// fail updatecheck leave
				from:             updateCheckState,
				to:               NewUpdateFetchState(updateResponse),
				transitionStatus: NoStatus,
				expectedStateData: &StateData{
					Version:          1,
					FromState:        MenderStateUpdateCheck,
					ToState:          MenderStateUpdateFetch,
					TransitionStatus: NoStatus,
				},
				expectedFromStateData: RebootStateData{},
				expectedToStateData: RebootStateData{
					UpdateInfo: updateResponse,
				},
				expectedActions: []string{"Leave"},
			},
			{
				// finish updatecheck leave, fail enter fetch
				from:             updateCheckState,
				to:               NewUpdateFetchState(updateResponse),
				transitionStatus: NoStatus,
				expectedStateData: &StateData{
					Version:          1,
					FromState:        MenderStateUpdateCheck,
					ToState:          MenderStateUpdateFetch,
					TransitionStatus: LeaveDone,
				},
				expectedFromStateData: RebootStateData{},
				expectedToStateData: RebootStateData{
					UpdateInfo: updateResponse,
				},
				expectedActions: []string{"Leave", "Enter"},
			},
			{
				// finish updatefetch enter and main state
				from:             updateCheckState,
				to:               NewUpdateFetchState(updateResponse),
				transitionStatus: LeaveDone,
				expectedStateData: &StateData{
					Version:          1,
					FromState:        MenderStateUpdateFetch,
					ToState:          MenderStateUpdateStore,
					TransitionStatus: NoStatus,
				},
				expectedFromStateData: RebootStateData{
					UpdateInfo: updateResponse,
				},
				expectedToStateData: RebootStateData{
					UpdateInfo: updateResponse,
				},
				expectedActions: []string{"Enter"},
			},
		},
		// update-fetch -> update-store
		{
			{
				// fail updatecheck leave
				modifyServer: func() {
					// var err error
					// // updateReader, size, err = mender.FetchUpdate(updateResponse.URI())
					// updateReader, err = MakeRootfsImageArtifact(1, false)
					// assert.NoError(t, err)
					// assert.NotNil(t, updateReader)

					// // try with a legit device_type
					// upd, err := MakeRootfsImageArtifact(1, false)
					// assert.NoError(t, err)
					// assert.NotNil(t, upd)

					// ioutil.WriteFile(deviceType, []byte("device_type=vexpress-qemu\n"), 0644)
					// err = mender.InstallUpdate(upd, 0)
					// log.Debugf("error in install: %v", err)
				},
				from:             NewUpdateFetchState(updateResponse),
				to:               NewUpdateStoreState(updateReader, 0, updateResponse),
				transitionStatus: NoStatus,
				expectedStateData: &StateData{
					Version:          1,
					FromState:        MenderStateUpdateFetch,
					ToState:          MenderStateUpdateStore,
					TransitionStatus: NoStatus,
				},
				expectedFromStateData: RebootStateData{
					UpdateInfo: updateResponse,
				},
				expectedToStateData: RebootStateData{
					UpdateInfo: updateResponse,
				},
				expectedActions: nil,
			},
		},
	}

	log.SetLevel(log.DebugLevel)

	// create a directory for the deployment-logs
	tempDir, _ := ioutil.TempDir("", "logs")
	defer os.RemoveAll(tempDir)

	DeploymentLogger = NewDeploymentLogManager(tempDir)

	for _, transition := range transitions {
		for _, tc := range transition {
			if tc.modifyServer != nil {
				tc.modifyServer()
			}
			rebootExecutor := &spontanaeousRebootExecutor{}
			mender.stateScriptExecutor = rebootExecutor
			RunPanickingTransition(t, mender.TransitionState, tc.from, tc.to, &ctx, tc.transitionStatus)
			assert.Equal(t, tc.expectedActions, rebootExecutor.expectedActions)

			sData, err := LoadStateData(ctx.store)
			assert.NoError(t, err)
			// amend the expected data to the final struct for comparison
			tc.expectedStateData.FromStateRebootData = tc.expectedFromStateData
			tc.expectedStateData.ToStateRebootData = tc.expectedToStateData
			assert.Equal(t, *tc.expectedStateData, sData)

			//  recreate the states that have been aborted
			fromState, toState, _ := mender.GetCurrentState(&ctx)
			assert.Equal(t, tc.expectedStateData.FromState, fromState.Id())
			assert.Equal(t, tc.expectedStateData.ToState, toState.Id())

		}

	}
}

func RunPanickingTransition(t *testing.T, f func(from, to State, ctx *StateContext, status TransitionStatus) (State, State, bool), from, to State, ctx *StateContext, status TransitionStatus) {
	defer func() {
		if r := recover(); r == nil {
			t.Log("no panic")
		} else {
			t.Logf("Panicked! %v", r)
		}
	}()
	f(from, to, ctx, status)
}

func TestTransitions(t *testing.T) {
	mender, err := NewMender(menderConfig{}, MenderPieces{})
	assert.NoError(t, err)

	ctx := StateContext{store: store.NewMemStore()}

	tc := []struct {
		from      *testState
		to        *testState
		expectedT []stateScript
		expectedS State
	}{
		{from: &testState{t: ToIdle},
			to:        &testState{t: ToSync, next: initState},
			expectedT: []stateScript{{"Idle", "Leave"}, {"Sync", "Enter"}},
			expectedS: &InitState{},
		},
		// idle error should have no effect
		{from: &testState{t: ToIdle, shouldErrorLeave: true},
			to:        &testState{t: ToSync, next: initState},
			expectedT: []stateScript{{"Idle", "Leave"}, {"Sync", "Enter"}},
			expectedS: &InitState{},
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

		p, s, c := mender.TransitionState(tt.from, tt.to, &ctx, NoStatus) // TODO - this test needs to be rewritten for spontanaeous reboots
		assert.Equal(t, p, p)                                             // TODO - not a valid test!
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

func (e *checkIgnoreErrorsExecutor) ExecuteAll(state, action string, ignoreError bool) error {
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
	err := tr.Leave(&e)
	assert.NoError(t, err)

	e = checkIgnoreErrorsExecutor{false}
	tr = ToArtifactCommit
	err = tr.Enter(&e)
	assert.NoError(t, err)

	e = checkIgnoreErrorsExecutor{true}
	tr = ToIdle
	err = tr.Enter(&e)
	assert.NoError(t, err)
}
