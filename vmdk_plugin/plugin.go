// Copyright 2016 VMware, Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

//
// VMWare VMDK Docker Data Volume plugin.
//
// Provide support for --driver=vmdk in Docker, when Docker VM is running under ESX.
//
// Serves requests from Docker Engine related to VMDK volume operations.
// Depends on vmdk-opsd service to be running on hosting ESX
// (see ./esx_service)
///

import (
	"fmt"
	"path/filepath"
	"sync"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/docker/go-plugins-helpers/volume"
	"github.com/vmware/docker-volume-vsphere/vmdk_plugin/utils/fs"
	"github.com/vmware/docker-volume-vsphere/vmdk_plugin/vmdkops"
	"golang.org/x/exp/inotify"
)

const (
	devWaitTimeout   = 1 * time.Second
	mountRoot        = "/mnt/vmdk" // VMDK block devices are mounted here
	sleepBeforeMount = 1 * time.Second
	watchPath        = "/dev/disk/by-path"
)

type vmdkDriver struct {
	m          *sync.Mutex // create() serialization - for future use
	useMockEsx bool
	ops        vmdkops.VmdkOps
	refCounts  refCountsMap
}

// creates vmdkDriver which to real ESX (useMockEsx=False) or a mock
func newVmdkDriver(useMockEsx bool) *vmdkDriver {
	var d *vmdkDriver
	if useMockEsx {
		d = &vmdkDriver{
			m:          &sync.Mutex{},
			useMockEsx: true,
			ops:        vmdkops.VmdkOps{Cmd: vmdkops.MockVmdkCmd{}},
		}
	} else {
		d = &vmdkDriver{
			m:          &sync.Mutex{},
			useMockEsx: false,
			ops:        vmdkops.VmdkOps{Cmd: vmdkops.EsxVmdkCmd{}},
			refCounts:  make(refCountsMap),
		}
		d.refCounts.Init(d)
	}

	return d
}
func (d *vmdkDriver) getRefCount(vol string) uint           { return d.refCounts.getCount(vol) }
func (d *vmdkDriver) incrRefCount(vol string) uint          { return d.refCounts.incr(vol) }
func (d *vmdkDriver) decrRefCount(vol string) (uint, error) { return d.refCounts.decr(vol) }

func getMountPoint(volName string) string {
	return filepath.Join(mountRoot, volName)

}

// Get info about a single volume
func (d *vmdkDriver) Get(r volume.Request) volume.Response {
	status, err := d.ops.Get(r.Name)
	if err != nil {
		return volume.Response{Err: err.Error()}
	}
	mountpoint := getMountPoint(r.Name)
	return volume.Response{Volume: &volume.Volume{Name: r.Name,
		Mountpoint: mountpoint,
		Status:     status}}
}

// List volumes known to the driver
func (d *vmdkDriver) List(r volume.Request) volume.Response {
	volumes, err := d.ops.List()
	if err != nil {
		return volume.Response{Err: err.Error()}
	}
	responseVolumes := make([]*volume.Volume, 0, len(volumes))
	for _, vol := range volumes {
		mountpoint := getMountPoint(vol.Name)
		responseVol := volume.Volume{Name: vol.Name, Mountpoint: mountpoint}
		responseVolumes = append(responseVolumes, &responseVol)
	}
	return volume.Response{Volumes: responseVolumes}
}

// Request attach and them mounts the volume.
// Actual mount - send attach to ESX and do the in-guest magic
// Returns mount point and  error (or nil)
func (d *vmdkDriver) mountVolume(name string) (string, error) {
	mountpoint := getMountPoint(name)

	// First, make sure  that mountpoint exists.
	err := fs.Mkdir(mountpoint)
	if err != nil {
		log.WithFields(
			log.Fields{"name": name, "dir": mountpoint},
		).Error("Failed to make directory for volume mount ")
		return mountpoint, err
	}

	if d.useMockEsx {
		return mountpoint, fmt.Errorf("No device to mount.")
	}

	skipInotify := false

	watcher, err := inotify.NewWatcher()

	if err != nil {
		log.WithFields(
			log.Fields{"name": name, "dir": mountpoint},
		).Error("Failed to create watcher, skip inotify ")
		skipInotify = true
	} else {
		err = watcher.Watch(watchPath)
		if err != nil {
			log.WithFields(
				log.Fields{"name": name, "dir": mountpoint},
			).Error("Failed to watch /dev, skip inotify ")
			skipInotify = true
		}
	}

	// Have ESX attach the disk
	dev, err := d.ops.Attach(name, nil)
	if err != nil {
		return mountpoint, err
	}

	device, err := fs.GetDevicePath(dev)
	if err != nil {
		return mountpoint, err
	}

	if skipInotify {
		time.Sleep(sleepBeforeMount)
		return mountpoint, fs.Mount(mountpoint, "ext2", device)
	}
loop:
	for {
		select {
		case ev := <-watcher.Event:
			log.Debug("event: ", ev)
			if ev.Name == device {
				// Log when the device is discovered
				log.WithFields(
					log.Fields{"name": name, "event": ev},
				).Info("Attach complete ")
				break loop
			}
		case err := <-watcher.Error:
			log.WithFields(
				log.Fields{"name": name, "device": device, "error": err},
			).Error("Hit error during watch ")
			break loop
		case <-time.After(devWaitTimeout):
			log.WithFields(
				log.Fields{"name": name, "timeout": devWaitTimeout, "device": device},
			).Error("Reached timeout while waiting for device ")
			break loop
		}
	}

	return mountpoint, fs.Mount(mountpoint, "ext2", device)
}

// Unmounts the volume and then requests detach
func (d *vmdkDriver) unmountVolume(name string) error {
	mountpoint := getMountPoint(name)
	err := fs.Unmount(mountpoint)
	if err != nil {
		log.WithFields(
			log.Fields{"mountpoint": mountpoint, "error": err},
		).Error("Failed to unmount volume. Now trying to detach... ")
		// Do not return error. Continue with detach.
	}
	return d.ops.Detach(name, nil)
}

// The user wants to create a volume.
// No need to actually manifest the volume on the filesystem yet
// (until Mount is called).
// Name and driver specific options passed through to the ESX host
func (d *vmdkDriver) Create(r volume.Request) volume.Response {
	err := d.ops.Create(r.Name, r.Options)
	if err != nil {
		log.WithFields(log.Fields{"name": r.Name, "error": err}).Error("Create volume failed ")
		return volume.Response{Err: err.Error()}
	}
	log.WithFields(log.Fields{"name": r.Name}).Info("Volume created ")
	return volume.Response{Err: ""}
}

// removes individual volume. Docker would call it only if is not using it anymore
func (d *vmdkDriver) Remove(r volume.Request) volume.Response {
	log.WithFields(log.Fields{"name": r.Name}).Info("Removing volume ")

	// Docker is supposed to block 'remove' command if the volume is used. Verify.
	if d.getRefCount(r.Name) != 0 {
		msg := fmt.Sprintf("Remove failure - volume is still mounted. "+
			" volume=%s, refcount=%d", r.Name, d.getRefCount(r.Name))
		log.Error(msg)
		return volume.Response{Err: msg}
	}

	err := d.ops.Remove(r.Name, r.Options)
	if err != nil {
		log.WithFields(
			log.Fields{"name": r.Name, "error": err},
		).Error("Failed to remove volume ")
		return volume.Response{Err: err.Error()}
	}

	return volume.Response{Err: ""}
}

// give docker a reminder of the volume mount path
func (d *vmdkDriver) Path(r volume.Request) volume.Response {
	return volume.Response{Mountpoint: getMountPoint(r.Name)}
}

// Provide a volume to docker container - called once per container start.
// We need to keep refcount and unmount on refcount drop to 0
func (d *vmdkDriver) Mount(r volume.Request) volume.Response {
	log.WithFields(log.Fields{"name": r.Name}).Info("Mounting volume ")
	d.m.Lock()
	defer d.m.Unlock()

	// If the volume is already mounted , just increase the refcount.
	//
	// Note: We are deliberately incrementing refcount first, before trying
	// to do anything else. If Mount fails, Docker will send Unmount request,
	// and we will happily decrement the refcount there, and will fail the unmount
	// since the volume will have been never mounted.
	// Note: for new keys, GO maps return zero value, so no need for if_exists.

	refcnt := d.incrRefCount(r.Name) // save map traversal
	log.Debugf("volume name=%s refcnt=%d", r.Name, refcnt)
	if refcnt > 1 {
		log.WithFields(
			log.Fields{"name": r.Name, "refcount": refcnt},
		).Info("Already mounted, skipping mount. ")
		return volume.Response{Mountpoint: getMountPoint(r.Name)}
	}

	// This is the first time we are asked to mount the volume, so comply
	mountpoint, err := d.mountVolume(r.Name)
	if err != nil {
		log.WithFields(
			log.Fields{"name": r.Name, "error": err.Error()},
		).Error("Failed to mount ")
		return volume.Response{Err: err.Error()}
	}

	return volume.Response{Mountpoint: mountpoint}
}

// Unmount request from Docker. If mount refcount is drop to 0,
// unmount and detach from VM
func (d *vmdkDriver) Unmount(r volume.Request) volume.Response {
	log.WithFields(log.Fields{"name": r.Name}).Info("Unmounting Volume ")
	d.m.Lock()
	defer d.m.Unlock()

	// if the volume is still used by other containers, just return OK
	refcnt, err := d.decrRefCount(r.Name)
	if err != nil {
		// something went wrong - yell, but still try to unmount
		log.WithFields(
			log.Fields{"name": r.Name, "refcount": refcnt},
		).Error("Refcount error - still trying to unmount...")
	}
	log.Debugf("volume name=%s refcnt=%d", r.Name, refcnt)
	if refcnt >= 1 {
		log.WithFields(
			log.Fields{"name": r.Name, "refcount": refcnt},
		).Info("Still in use, skipping unmount request. ")
		return volume.Response{Err: ""}
	}

	// and if nobody needs it, unmount and detach
	err = d.unmountVolume(r.Name)
	if err != nil {
		log.WithFields(
			log.Fields{"name": r.Name, "error": err.Error()},
		).Error("Failed to unmount ")
		return volume.Response{Err: err.Error()}
	}
	return volume.Response{Err: ""}
}
