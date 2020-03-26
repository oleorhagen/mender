// Copyright 2020 Northern.tech AS
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

package system

import (
	"os"
	"os/exec"

	"github.com/pkg/errors"
)

type SystemRebootCmd struct {
	command Commander
}

func NewSystemRebootCmd(command Commander) *SystemRebootCmd {
	return &SystemRebootCmd{
		command: command,
	}
}

// Reboot has been changed to handle coverage analysis. This means that the
// function now panics, instead of rebooting. The panic is caught in the main
// function, which then generates the coverage analysis, and then reboots.
func (s *SystemRebootCmd) Reboot() error {
	// Exit the client cleanly prior to rebooting, so that coverage can be
	// recorded by the test framework
	panic("Client needs reboot!")
	return errors.New("Failed to exit. Wtf")
}

type Commander interface {
	Command(name string, arg ...string) *exec.Cmd
}

type StatCommander interface {
	Stat(string) (os.FileInfo, error)
	Commander
}

// we need real OS implementation
type OsCalls struct {
}

func (OsCalls) Command(name string, arg ...string) *exec.Cmd {
	return exec.Command(name, arg...)
}

func (OsCalls) Stat(name string) (os.FileInfo, error) {
	return os.Stat(name)
}
