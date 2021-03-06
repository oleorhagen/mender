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
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/mendersoftware/log"
	"github.com/stretchr/testify/assert"
)

func TestStore(t *testing.T) {
	tmp, err := ioutil.TempDir("", "scripts")
	assert.NoError(t, err)

	defer os.RemoveAll(tmp)

	// create some content in scripts directory
	f, err := os.Create(filepath.Join(tmp, "SampleScript"))
	assert.NoError(t, err)
	err = f.Close()
	assert.NoError(t, err)

	s := NewStore(tmp)
	err = s.Clear()
	assert.NoError(t, err)

	// check if directory is empty
	content, err := ioutil.ReadDir(tmp)
	assert.NoError(t, err)
	assert.Empty(t, content)

	// check if having empty location is not returning an error
	s.location = ""
	err = s.Clear()
	assert.NoError(t, err)

	// check if having unsafe location is returning an error
	//below one better to be passed
	// check if trying removig / will fail
	s.location = "/"
	err = s.Clear()
	assert.Error(t, err)
	s.location = "my-relative-path/scripts"
	err = s.Clear()
	assert.Error(t, err)

	s.location = tmp
	buf := bytes.NewBufferString("execute me")
	err = s.StoreScript(buf, "my_script")
	assert.NoError(t, err)
	content, err = ioutil.ReadDir(tmp)
	assert.NoError(t, err)
	assert.Equal(t, "my_script", content[0].Name())

	// storing the same file should return an error
	err = s.StoreScript(buf, "my_script")
	assert.Error(t, err)

	err = s.Finalize(1)
	assert.NoError(t, err)

	content, err = ioutil.ReadDir(tmp)
	assert.NoError(t, err)
	assert.Len(t, content, 2)

	hasVersion := false
	for _, file := range content {
		if file.Name() == "version" {
			hasVersion = true
			v, vErr := ioutil.ReadFile(filepath.Join(tmp, file.Name()))
			assert.NoError(t, vErr)
			ver, vErr := strconv.Atoi(string(v))
			assert.NoError(t, vErr)
			assert.Equal(t, 1, ver)
		}
	}
	assert.True(t, hasVersion)
	ver, err := readVersion(filepath.Join(tmp, "version"))
	assert.NoError(t, err)
	assert.Equal(t, 1, ver)
}

func TestExecutor(t *testing.T) {
	tmpArt, err := ioutil.TempDir("", "art_scripts")
	assert.NoError(t, err)
	defer os.RemoveAll(tmpArt)

	// array for holding the created scripts, used for comparing to the returned scripts from exec get
	// all scripts must be formated like `ArtifactInstall_Enter_05(_wifi-driver)`(optional)
	// in order for them to be executed
	scriptArr := []string{
		"ArtifactInstall_Leave",
		"ArtifactInstall_Leave_02",
		// ArtifactInstall_Leave_100 should not be added
		"ArtifactInstall_Leave_10_wifi-driver",
	}

	// create some content in scripts directory
	_, err = createArtifactTestScript(tmpArt, "ArtifactInstall_Leave", "#!/bin/bash \ntrue")

	tmpRootfs, err := ioutil.TempDir("", "rootfs_scripts")
	assert.NoError(t, err)
	defer os.RemoveAll(tmpRootfs)

	// create some content in scripts directory
	rootfsF, err := os.Create(filepath.Join(tmpRootfs, "Download_Enter_00"))
	assert.NoError(t, err)
	err = rootfsF.Close()
	assert.NoError(t, err)

	e := Launcher{
		ArtScriptsPath:          tmpArt,
		RootfsScriptsPath:       tmpRootfs,
		SupportedScriptVersions: []int{2, 3},
	}

	s, dir, err := e.get("Download", "Enter")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "supported versions does not match")

	// store.Finalize() should store version file in the artifact directory
	store := NewStore(tmpRootfs)
	err = store.Finalize(2)
	assert.NoError(t, err)

	s, dir, err = e.get("Download", "Enter")
	assert.NoError(t, err)
	assert.Equal(t, tmpRootfs, dir)
	assert.Equal(t, "Download_Enter_00", s[0].Name())

	// now, let's try to execute some scripts
	err = e.ExecuteAll("Download", "Enter", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "is not executable")

	// now the same as above, but we are ignoring errors
	err = e.ExecuteAll("Download", "Enter", true)
	assert.NoError(t, err)

	// no version file, but we are ignoring errors
	err = e.ExecuteAll("ArtifactInstall", "Leave", true)
	assert.NoError(t, err)

	store = NewStore(tmpArt)
	err = store.Finalize(2)
	assert.NoError(t, err)
	err = e.ExecuteAll("ArtifactInstall", "Leave", false)
	assert.NoError(t, err)

	// add a script that will fail
	_, err = createArtifactTestScript(tmpArt, "ArtifactInstall_Leave_02", "#!/bin/bash \nfalse")

	err = e.ExecuteAll("ArtifactInstall", "Leave", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "error executing")

	// the same as above, but we are ignoring errors
	err = e.ExecuteAll("ArtifactInstall", "Leave", true)
	assert.NoError(t, err)

	// Add a script that does not satisfy the format required
	// Thus it should not be added to the script array
	_, err = createArtifactTestScript(tmpArt, "ArtifactInstall_Leave_100", "#!/bin/bash \ntrue")
	assert.NoError(t, err)

	sysInstallScripts, _, err := e.get("ArtifactInstall", "Leave")
	testArtifactArrayEquals(t, scriptArr[1:2], sysInstallScripts)

	assert.NoError(t, err)

	// Add a script that does satisfy the full format required
	_, err = createArtifactTestScript(tmpArt, "ArtifactInstall_Leave_10_wifi-driver", "#!/bin/bash \ntrue")
	sysInstallScripts, _, err = e.get("ArtifactInstall", "Leave")
	testArtifactArrayEquals(t, scriptArr[1:], sysInstallScripts)
	assert.NoError(t, err)

	// Test script logging
	var buf bytes.Buffer
	oldOut := log.Log.Out
	defer log.SetOutput(oldOut)
	log.SetOutput(&buf)
	fileP, err := createArtifactTestScript(tmpArt, "ArtifactInstall_Leave_00", "#!/bin/bash \necho 'error data' >&2")
	assert.NoError(t, err)
	err = execute(fileP.Name(), 100) // give the script plenty of time to run
	assert.NoError(t, err)
	assert.Contains(t, buf.String(), "error data")

	buf.Reset()

	// write more than 10KB to stderr
	fileP, err = createArtifactTestScript(tmpArt, "ArtifactInstall_Leave_11", "#!/bin/bash \nhead -c 89999 </dev/urandom >&2\n exit 1")
	assert.NoError(t, err)
	err = execute(fileP.Name(), 100)
	assert.EqualError(t, err, "exit status 1")
	assert.Contains(t, buf.String(), "Truncated to 10KB")

	// add a script that will time-out, and die
	filep, err := createArtifactTestScript(tmpArt, "ArtifactInstall_Leave_10_btoot", "!#/bin/bash \nsleep 2")
	assert.NoError(t, err)
	ret := retCode(execute(filep.Name(), 1))
	assert.Equal(t, ret, -1)

	// Test retry-later functionality
	l := Launcher{
		ArtScriptsPath:          tmpArt,
		RootfsScriptsPath:       tmpRootfs,
		SupportedScriptVersions: []int{2, 3},
		RetryInterval:           1,
		RetryTimeout:            2,
	}

	// add a script that will time out
	_, err = createArtifactTestScript(tmpArt, "ArtifactInstall_Enter_66", "#!/bin/bash \n sleep 1\n exit 21")
	assert.NoError(t, err)
	err = l.ExecuteAll("ArtifactInstall", "Enter", false)
	assert.Contains(t, err.Error(), "retry time-limit exceeded")

	// test with ignore-error=true
	err = l.ExecuteAll("ArtifactInstall", "Enter", true)
	assert.NoError(t, err)

	err = os.Remove(filepath.Join(tmpArt, "ArtifactInstall_Enter_66"))
	assert.NoError(t, err)

	// add a script that retries and then succeeds
	script := fmt.Sprintf("#!/bin/bash \n sleep 1 \n if [ ! -f %s/scriptflag ]; then\n echo f > %[1]s/scriptflag\n exit 21 \nfi \n rm -f %[1]s/scriptflag\n exit 0", tmpArt)
	_, err = createArtifactTestScript(tmpArt, "ArtifactInstall_Enter_67", script)
	err = l.ExecuteAll("ArtifactInstall", "Enter", false)
	assert.NoError(t, err)
}

func TestVersion(t *testing.T) {
	tmpDir, err := ioutil.TempDir("", "statescripts")
	assert.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	l := Launcher{
		RootfsScriptsPath:       tmpDir,
		SupportedScriptVersions: []int{2, 3},
	}

	// no scripts
	err = l.CheckRootfsScriptsVersion()
	assert.NoError(t, err)

	// no scripts directory
	l.RootfsScriptsPath = "/path/not/existing"
	err = l.CheckRootfsScriptsVersion()
	assert.NoError(t, err)

	// have only version file
	l.RootfsScriptsPath = tmpDir
	store := NewStore(tmpDir)
	err = store.Finalize(2) // will create version file
	assert.NoError(t, err)
	err = l.CheckRootfsScriptsVersion()
	assert.NoError(t, err)

	// have unsupported version
	err = os.Remove(filepath.Join(tmpDir, "version"))
	assert.NoError(t, err)
	err = store.Finalize(1) // will create version file
	assert.NoError(t, err)
	err = l.CheckRootfsScriptsVersion()
	assert.Error(t, err)

	// have usupported version and some script
	_, err = createArtifactTestScript(tmpDir, "ArtifactInstall_Leave_100", "#!/bin/bash \ntrue")
	assert.NoError(t, err)
	err = l.CheckRootfsScriptsVersion()
	assert.Error(t, err)

	// have script and correct version
	err = os.Remove(filepath.Join(tmpDir, "version"))
	assert.NoError(t, err)
	err = store.Finalize(2) // will create version file
	assert.NoError(t, err)
	err = l.CheckRootfsScriptsVersion()
	assert.NoError(t, err)

	newTmpDir, err := ioutil.TempDir("", "statescripts")
	assert.NoError(t, err)
	defer os.RemoveAll(newTmpDir)
	l.RootfsScriptsPath = newTmpDir

	// have only script, no version file
	_, err = createArtifactTestScript(newTmpDir, "ArtifactInstall_Leave_100", "#!/bin/bash \ntrue")
	assert.NoError(t, err)
	err = l.CheckRootfsScriptsVersion()
	assert.Error(t, err)

}

func createArtifactTestScript(dir, name, code string) (fileP *os.File, err error) {
	fileP, err = os.OpenFile(filepath.Join(dir, name),
		os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0755)
	_, err = fileP.WriteString(code)
	err = fileP.Close()
	return
}

func testArtifactArrayEquals(t *testing.T, expected []string, actual []os.FileInfo) {
	for i, script := range actual {
		assert.EqualValues(t, expected[i], script.Name())
	}
}
