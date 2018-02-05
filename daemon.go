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
	"github.com/mendersoftware/log"
	"github.com/mendersoftware/mender/store"
	"github.com/pkg/errors"
)

// Config section

type menderDaemon struct {
	mender Controller
	stop   bool
	sctx   StateContext
	store  store.Store
}

func NewDaemon(mender Controller, store store.Store) *menderDaemon {

	daemon := menderDaemon{
		mender: mender,
		sctx: StateContext{
			store: store,
		},
		store: store,
	}
	return &daemon
}

func (d *menderDaemon) StopDaemon() {
	d.stop = true
}

func (d *menderDaemon) Cleanup() {
	if d.store != nil {
		if err := d.store.Close(); err != nil {
			log.Errorf("failed to close data store: %v", err)
		}
		d.store = nil
	}
}

func (d *menderDaemon) shouldStop() bool {
	return d.stop
}

func (d *menderDaemon) Run() error {
	// set the first state transition
	var toState State = d.mender.GetCurrentState()
	cancelled := false
	for {
		toState, cancelled = ExternalStateMachine(toState, &d.sctx, d.mender)
		log.Infof("State: %s", toState.Id())

		if toState.Id() == MenderStateError {
			es, ok := toState.(*ErrorState)
			if ok {
				if es.IsFatal() {
					return es.cause
				}
			} else {
				return errors.New("failed")
			}
		}
		if cancelled || toState.Id() == MenderStateDone {
			break
		}
		if d.shouldStop() {
			return nil
		}
	}
	return nil
}

// Two-layered state-machine should be factored out into a package of it's own

func ExternalStateMachine(s State, ctx *StateContext, c Controller) (State, bool) {
	var externalState = s.Transition()
	var internalState = NewInternalMenderState(externalState, s)
	for {
		if err := externalState.Enter(c.StateScriptExecutor(), nil); err != nil {
			return s.EnterError(err), false
		}
		rs, canc := internalState.Handle(ctx, c)
		if canc {
			return rs, canc
		}
		if err := externalState.Leave(c.StateScriptExecutor(), nil); err != nil {
			return s.LeaveError(err), false
		}
		externalState = rs.Transition()
		return rs, canc
	}
}

// This way, or as a mender-state?
func InternalStateMachine(s State, ctx *StateContext, c Controller) (State, bool) {
	var internalState State = s
	var externalState Transition = s.Transition()
	log.Infof("ExternalState: %s", externalState)
	for {
		log.Infof("InternalState prior handle: %s", internalState.Id())
		rs, canc := internalState.Handle(ctx, c)
		log.Infof("InternalState post handle: %s", rs.Id())
		c.SetNextState(rs)
		if canc {
			return rs, canc
		}
		// leaving external state
		if rs.Transition() != i.externalState && rs.Transition() != ToNone {
			log.Infof("Leaving internal state: %s", i.internalState.Id())
			return rs, canc
		}
		internalState = rs
	}
}
