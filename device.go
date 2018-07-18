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
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mendersoftware/log"
	"github.com/pkg/errors"
)

type deviceConfig struct {
	rootfsPartA string
	rootfsPartB string
}

type device struct {
	BootEnvReadWriter
	Commander
	*partitions
}

var (
	errorNoUpgradeMounted = errors.New("There is nothing to commit")
)

func NewDevice(env BootEnvReadWriter, sc StatCommander, config deviceConfig) (*device, error) {
	rtfsa, err := NewPartition(config.rootfsPartA)
	if err != nil {
		return nil, errors.Wrap(err, "failed to initialize rootfsPartA")
	}
	rtfsb, err := NewPartition(config.rootfsPartB)
	if err != nil {
		return nil, errors.Wrap(err, "failed to initialize rootfsPartB")
	}
	partitions := partitions{
		StatCommander:     sc,
		BootEnvReadWriter: env,
		// TODO - should this simply be a string?
		rootfsPartA: rtfsa,
		rootfsPartB: rtfsb,
		active:      rtfsa,
		inactive:    rtfsb,
	}
	device := device{env, sc, &partitions}
	return &device, nil
}

func (d *device) Reboot() error {
	log.Info("Mender rebooting from active partition: %s", d.active)
	return d.Command("reboot").Run()
}

func (d *device) SwapPartitions() error {
	// first get inactive partition
	inactivePartition, inactivePartitionHex, err := d.getInactivePartition()
	if err != nil {
		return err
	}
	log.Infof("setting partition for rollback: %s", inactivePartition)

	err = d.WriteEnv(BootVars{"mender_boot_part": inactivePartition, "mender_boot_part_hex": inactivePartitionHex, "upgrade_available": "0"})
	if err != nil {
		return err
	}
	log.Debug("Marking inactive partition as a boot candidate successful.")
	return nil
}

func (d *device) InstallUpdate(image io.ReadCloser, size int64) error {
	log.Debugf("Trying to install update of size: %d", size)
	if image == nil || size < 0 {
		return errors.New("Have invalid update. Aborting.")
	}
	inactivePartition, err := d.GetInactive()
	if err != nil {
		return errors.Wrap(err, "failed to get the inactive partition")
	}
	// TODO - check write length(?)
	var n int64
	if n, err = io.Copy(inactivePartition, image); err != nil {
		fmt.Println(n)
		return errors.Wrap(err, "failed to install the new image")
	}
	fmt.Println(n)
	return nil
}

func (d *device) getInactivePartition() (string, string, error) {
	inactivePartition, err := d.GetInactive()
	if err != nil {
		return "", "", errors.New("Error obtaining inactive partition: " + err.Error())
	}

	log.Debugf("Marking inactive partition (%s) as the new boot candidate.", inactivePartition)

	partitionNumberDecStr := inactivePartition.String()[len(strings.TrimRight(inactivePartition.String(), "0123456789")):]
	partitionNumberDec, err := strconv.Atoi(partitionNumberDecStr)
	if err != nil {
		return "", "", errors.New("Invalid inactive partition: " + inactivePartition.String())
	}

	partitionNumberHexStr := fmt.Sprintf("%X", partitionNumberDec)

	return partitionNumberDecStr, partitionNumberHexStr, nil
}

func (d *device) EnableUpdatedPartition() error {

	inactivePartition, inactivePartitionHex, err := d.getInactivePartition()
	if err != nil {
		return err
	}

	log.Info("Enabling partition with new image installed to be a boot candidate: ", string(inactivePartition))
	// For now we are only setting boot variables
	err = d.WriteEnv(BootVars{"upgrade_available": "1", "mender_boot_part": inactivePartition, "mender_boot_part_hex": inactivePartitionHex, "bootcount": "0"})
	if err != nil {
		return err
	}

	log.Debug("Marking inactive partition as a boot candidate successful.")

	return nil
}

func (d *device) CommitUpdate() error {
	// Check if the user has an upgrade to commit, if not, throw an error
	hasUpdate, err := d.HasUpdate()
	if err != nil {
		return err
	}
	if hasUpdate {
		log.Info("Commiting update")
		// For now set only appropriate boot flags
		return d.WriteEnv(BootVars{"upgrade_available": "0"})
	}
	return errorNoUpgradeMounted
}

func (d *device) HasUpdate() (bool, error) {
	env, err := d.ReadEnv("upgrade_available")
	if err != nil {
		return false, errors.Wrapf(err, "failed to read environment variable")
	}
	upgradeAvailable := env["upgrade_available"]

	if upgradeAvailable == "1" {
		return true, nil
	}
	return false, nil
}
