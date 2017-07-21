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

package statescript

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/mendersoftware/log"
	"github.com/mendersoftware/mender/store"
	"github.com/pkg/errors"
)

const (
	exitRetryLater = -2 // TODO - what would be a good error code?

	// TODO - initial default value of 3 minutes total to every retry-later script. Find a good value!
	retryTotScriptTime time.Duration = 1 * 3 * time.Minute

	// TODO - this should be configureable. i.e. via menderconfig
	retryTimeoutInterval = 60 * time.Second
)

// ErrRetryLater returned when a statescript returns retryLater
var ErrRetryLater = errors.New("retry script later")

type Executor interface {
	ExecuteAll(state, action string, ignoreError bool) error
	CheckRootfsScriptsVersion() error
}

type Launcher struct {
	ArtScriptsPath          string
	RootfsScriptsPath       string
	SupportedScriptVersions []int
	Timeout                 int
}

//TODO: we can optimize for reading directories once and then creating
// a map with all the scripts that needs to be executed.

func (l Launcher) CheckRootfsScriptsVersion() error {
	ver, err := readVersion(filepath.Join(l.RootfsScriptsPath, "version"))
	if err != nil && os.IsNotExist(err) {
		// no scripts; no error
		return nil
	} else if err != nil {
		return errors.Wrap(err, "statescript: can not read rootfs scripts version")
	}

	for _, v := range l.SupportedScriptVersions {
		if v == ver {
			return nil
		}
	}
	return errors.Errorf("statescript: unsupported scripts version: %v", ver)
}

func matchVersion(actual int, supported []int, hasScripts bool) error {
	// if there are no scripts to execute we shold not care about the version
	if hasScripts == false {
		return nil
	}

	for _, v := range supported {
		if v == actual {
			return nil
		}
	}

	return errors.Errorf("statescript: supported versions does not match "+
		"(supported: %v; actual: %v)", supported, actual)
}

func (l Launcher) get(state, action string) ([]os.FileInfo, string, error) {

	sDir := l.ArtScriptsPath
	if state == "Idle" || state == "Sync" || state == "Download" {
		sDir = l.RootfsScriptsPath
	}

	// ReadDir reads the directory named by dirname and returns
	// a list of directory entries sorted by filename.
	// The list returned should be sorted which guarantees correct
	// order of scripts execution.
	files, err := ioutil.ReadDir(sDir)
	if err != nil && os.IsNotExist(err) {
		// no state scripts directory; just move on
		return nil, "", nil
	} else if err != nil {
		return nil, "", errors.Wrap(err, "statescript: can not read scripts directory")
	}

	scripts := make([]os.FileInfo, 0)
	var version int

	for _, file := range files {
		if file.Name() == "version" {
			version, err = readVersion(filepath.Join(sDir, file.Name()))
			if err != nil {
				return nil, "", errors.Wrapf(err, "statescript: can not read version file")
			}
		}

		if strings.Contains(file.Name(), state+"_") &&
			strings.Contains(file.Name(), action) {

			// all scripts must be formated like `ArtifactInstall_Enter_05(_wifi-driver)`(optional)
			re := regexp.MustCompile(`([A-Za-z]+)_(Enter|Leave|Error)_[0-9][0-9](_\S+)?`)
			if len(file.Name()) == len(re.FindString(file.Name())) {
				scripts = append(scripts, file)
			} else {
				log.Warningf("script format mismatch: '%s' will not be run ", file.Name())
			}
		}
	}

	if err := matchVersion(version, l.SupportedScriptVersions,
		len(scripts) != 0); err != nil {
		return nil, "", err
	}

	return scripts, sDir, nil
}

func retCode(err error) int {
	defaultFailedCode := -1

	if err != nil {
		// try to get the exit code
		if exitError, ok := err.(*exec.ExitError); ok {
			ws := exitError.Sys().(syscall.WaitStatus)
			return ws.ExitStatus()
		} else {
			return defaultFailedCode
		}
	}
	return 0
}

func (l Launcher) getTimeout() time.Duration {
	t := time.Duration(l.Timeout) * time.Second
	if t == 0 {
		log.Debug("statescript: timeout for executing scripts is not defined; " +
			"using default of 60 seconds")
		t = 60 * time.Second
	}
	return t
}

func execute(name string, timeout time.Duration) int {

	cmd := exec.Command(name)

	// As child process gets the same PGID as the parent by default, in order
	// to avoid killing Mender when killing process group we are setting
	// new PGID for the executed script and its children.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		return retCode(err)
	}

	timer := time.AfterFunc(timeout, func() {
		// In addition to kill a single process we are sending SIGKILL to
		// process group making sure we are killing the hanging script and
		// all its children.
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	})
	defer timer.Stop()

	if err := cmd.Wait(); err != nil {
		return retCode(err)
	}
	return 0
}

// TODO - test all functionality related to this
// RetryLaterContext implements Binary(un)Marshaler
type RetryLaterContext struct {
	State          string        `json:"State"`
	LatestExecTime time.Time     `json:"LatestExecutionTime"`
	TotDuration    time.Duration `json:"TotalDuration"`
}

func (rtctx *RetryLaterContext) MarshalBinary() (data []byte, err error) {
	data, err = json.Marshal(rtctx)
	return
}

func (rtctx *RetryLaterContext) UnmarshalBinary(data []byte) error {

	if err := json.Unmarshal(data, rtctx); err != nil {
		return err
	}

	return nil

}

// TODO - pretty up, does not necessarily need to return a ctx
func getRetryLaterContext(db *store.DBStore, scriptName string, newctx *RetryLaterContext) (*RetryLaterContext, error) {

	ctx := &RetryLaterContext{}
	if err := db.Get(scriptName, ctx); os.IsNotExist(err) {
		err = db.Update(scriptName, newctx)
		if err != nil {
			return &RetryLaterContext{}, err
		}
		return newctx, nil
	} else if err != nil {
		return &RetryLaterContext{}, err
	}

	newctx.TotDuration += ctx.TotDuration

	return newctx, nil

}

func handleRetryLaterError(db *store.DBStore, scriptName string, execState string, execTime time.Time) error {

	elapsedTime := time.Since(execTime)

	ctx, err := getRetryLaterContext(db, scriptName, &RetryLaterContext{execState, execTime, elapsedTime})
	if err != nil {
		return err
	}

	if err = db.Update(scriptName, ctx); err != nil {
		return err
	}

	if ctx.TotDuration <= retryTotScriptTime {

		return ErrRetryLater
	}

	return errors.Errorf("statescript: error - retry time limit exceeded for %s", scriptName)

}

func (l Launcher) ExecuteAll(state, action string, ignoreError bool) error {
	scr, dir, err := l.get(state, action)

	log.Errorf("In state %s - action %s", state, action)
	// db for storing the retry later scripts
	db := store.NewDBStore("/data/mender")
	if db == nil {
		return errors.New("failed to open the database")
	}
	defer db.Close()

	if err != nil {
		if ignoreError {
			log.Errorf("statescript: ignoring error while executing [%s:%s] script: %v",
				state, action, err)
			return nil
		}
		return err
	}

	execBits := os.FileMode(syscall.S_IXUSR | syscall.S_IXGRP | syscall.S_IXOTH)
	timeout := l.getTimeout()

	for _, s := range scr {
		// check if script is executable
		if s.Mode()&execBits == 0 {
			if ignoreError {
				log.Errorf("statescript: ignoring script '%s' being not executable",
					filepath.Join(dir, s.Name()))
				continue
			} else {
				return errors.Errorf("statescript: script '%s' is not executable",
					filepath.Join(dir, s.Name()))
			}
		}

		execTime := time.Now()

		if ret := execute(filepath.Join(dir, s.Name()), timeout); ret != 0 {

			// In case of error scripts all should be executed.
			if ignoreError {
				log.Errorf("statescript: ignoring error executing '%s': %d", s.Name(), ret)
			} else if ret == exitRetryLater {
				return handleRetryLaterError(db, s.Name(), state, execTime)
			} else {
				return errors.Errorf("statescript: error executing '%s': %d",
					s.Name(), ret)
			}
		}
		// no error executing script -> remove the timer from the retrylater database entry.
		_, err := db.ReadAll(s.Name())
		if err == nil { // script has a retry entry in database.
			if err = db.Remove(s.Name()); err != nil {
				log.Error("failed to remove script %s from database", s.Name())
			}
		}
	}
	return nil
}
