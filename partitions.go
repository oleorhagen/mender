// Copyright 2018 Northern.tech AS
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
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"syscall"

	"github.com/mendersoftware/log"
	"github.com/pkg/errors"
)

var (
	RootPartitionDoesNotMatchMount = errors.New("Can not match active partition and any of mounted devices.")
	ErrorNoMatchBootPartRootPart   = errors.New("No match between boot and root partitions.")
	ErrorPartitionNumberNotSet     = errors.New("RootfsPartA and RootfsPartB settings are not both set.")
	ErrorPartitionNumberSame       = errors.New("RootfsPartA and RootfsPartB cannot be set to the same value.")
	ErrorPartitionNoMatchActive    = errors.New("Active root partition matches neither RootfsPartA nor RootfsPartB.")
)

type Partition interface {
	io.WriteCloser // TODO - partition needs a reader.
	fmt.Stringer
}

type partition struct {
	BlockDevice // need the write method to write to partition. TODO WriteCloser(?)
}

func NewPartition(path string) (*partition, error) {
	if path == "" {
		return nil, errors.New("partition has no path")
	}
	b, err := NewBlockDevice(path)
	if err != nil {
		return nil, errors.Wrap(err, "NewPartition: failed to initialize blockdevice")
	}
	p := &partition{
		*b,
	}
	return p, nil
}

func (p *partition) String() string {
	if p == nil {
		return ""
	}
	return p.Path
}

func (p *partition) MarshalJSON() ([]byte, error) {
	return json.Marshal(p.Path)
}

func (p *partition) UnmarshalJSON(b []byte) error {
	var path string
	if err := json.Unmarshal(b, &path); err != nil {
		return errors.Wrap(err, "failed to unmarshal the partition name")
	}
	p.Path = path
	return nil
}

type partitions struct {
	StatCommander
	BootEnvReadWriter
	rootfsPartA *partition
	rootfsPartB *partition
	active      *partition
	inactive    *partition
}

func (p *partitions) GetInactive() (*partition, error) {
	if p.inactive != nil {
		log.Debug("Inactive partition: ", p.inactive)
		return p.inactive, nil
	}
	return p.getAndCacheInactivePartition()
}

func (p *partitions) GetActive() (*partition, error) {
	if p.active != nil {
		log.Debug("Active partition: ", p.active)
		return p.active, nil
	}
	return p.getAndCacheActivePartition(isMountedRoot, getAllMountedDevices)
}

func (p *partitions) getAndCacheInactivePartition() (*partition, error) {
	if p.rootfsPartA == nil || p.rootfsPartB == nil {
		return nil, ErrorPartitionNumberNotSet
	}
	if p.rootfsPartA.String() == p.rootfsPartB.String() {
		return nil, ErrorPartitionNumberSame
	}

	active, err := p.GetActive()
	if err != nil {
		return nil, err
	}

	if active.String() == p.rootfsPartA.String() {
		p.inactive = p.rootfsPartB
	} else if active.String() == p.rootfsPartB.String() {
		p.inactive = p.rootfsPartA
	} else {
		return nil, ErrorPartitionNoMatchActive
	}

	log.Debugf("Detected inactive partition %s, based on active partition %s", p.inactive, active)
	return p.inactive, nil
}

func getRootCandidateFromMount(data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(line, " ")
		if len(fields) >= 3 && fields[2] == "/" {
			// we just need the first one (in fact there should be ONLY one)
			return fields[0]
		}
	}
	return ""
}

func getRootDevice(sc StatCommander) *syscall.Stat_t {
	rootStat, err := sc.Stat("/")
	if err != nil {
		// Seriously??
		// Something is *very* wrong.
		log.Error("Can not stat root device.")
		return nil
	}
	return rootStat.Sys().(*syscall.Stat_t)
}

func getAllMountedDevices(devDir string) (names []string, err error) {
	devFd, err := os.Open(devDir)
	if err != nil {
		return nil, err
	}
	defer devFd.Close()

	names, err = devFd.Readdirnames(0)
	if err != nil {
		return nil, err
	}
	for i := 0; i < len(names); i++ {
		names[i] = path.Join(devDir, names[i])
	}

	return names, nil
}

// There is a lot of system calls here so will be rather hard to test
func isMountedRoot(sc StatCommander, dev string, root *syscall.Stat_t) bool {
	// Check if this is a device file and its device ID matches that of the
	// root directory.
	stat, err := sc.Stat(dev)
	if err != nil ||
		(stat.Mode()&os.ModeDevice) == 0 ||
		stat.Sys().(*syscall.Stat_t).Rdev != root.Dev {
		return false
	}

	return true
}

func getRootFromMountedDevices(sc StatCommander,
	rootChecker func(StatCommander, string, *syscall.Stat_t) bool,
	devices []string, root *syscall.Stat_t) (string, error) {

	for _, device := range devices {
		if rootChecker(sc, device, root) {
			return device, nil
		}
	}
	return "", RootPartitionDoesNotMatchMount
}

func (p *partitions) getAndCacheActivePartition(rootChecker func(StatCommander, string, *syscall.Stat_t) bool,
	getMountedDevices func(string) ([]string, error)) (*partition, error) {
	mountData, err := p.Command("mount").Output()
	if err != nil {
		return nil, err
	}

	mountCandidate := getRootCandidateFromMount(mountData)
	rootDevice := getRootDevice(p)
	if rootDevice == nil {
		return nil, errors.New("Can not find root device")
	}

	// Fetch active partition from ENV
	bootEnvBootPart, err := getBootEnvActivePartition(p.BootEnvReadWriter)
	if err != nil {
		return nil, err
	}

	// First check if mountCandidate matches rootDevice
	if mountCandidate != "" {
		if rootChecker(p, mountCandidate, rootDevice) {
			a, err := NewPartition(mountCandidate)
			if err != nil {
				return nil, err
			}
			p.active = a
			log.Debugf("Setting active partition from mount candidate: %s", p.active)
			return p.active, nil
		}
		// If mount candidate does not match root device check if we have a match in ENV
		if checkBootEnvAndRootPartitionMatch(bootEnvBootPart, mountCandidate) {
			a, err := NewPartition(mountCandidate)
			if err != nil {
				return nil, err
			}
			p.active = a
			log.Debug("Setting active partition: ", mountCandidate)
			return p.active, nil
		}
		// If not see if we are lucky somewhere else
	}

	const devDir string = "/dev"

	mountedDevices, err := getMountedDevices(devDir)
	if err != nil {
		return nil, err
	}

	activePartition, err := getRootFromMountedDevices(p, rootChecker, mountedDevices, rootDevice)
	if err != nil {
		return nil, err
	}
	if checkBootEnvAndRootPartitionMatch(bootEnvBootPart, activePartition) {
		a, err := NewPartition(activePartition)
		if err != nil {
			return nil, err
		}
		p.active = a
		log.Debug("Setting active partition: ", activePartition)
		return p.active, nil
	}

	log.Error("Mounted root '" + activePartition + "' does not match boot environment mender_boot_part: " + bootEnvBootPart)
	return nil, ErrorNoMatchBootPartRootPart
}

func getBootEnvActivePartition(env BootEnvReadWriter) (string, error) {
	bootEnv, err := env.ReadEnv("mender_boot_part")
	if err != nil {
		return "", errors.Wrapf(err, ErrorNoMatchBootPartRootPart.Error())
	}

	return bootEnv["mender_boot_part"], nil
}

func checkBootEnvAndRootPartitionMatch(bootPartNum string, rootPart string) bool {
	return strings.HasSuffix(rootPart, bootPartNum)
}
