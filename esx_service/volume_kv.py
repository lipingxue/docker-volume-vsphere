# Copyright 2016 VMware, Inc. All Rights Reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#    http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

##
## This module handles creating and managing a kv store for volumes
## (vmdks) created by the docker volume plugin on an ESX host. The
## module exposes a set of functions that allow creat/delete/get/set
## on the kv store. Currently uses side cars to keep KV pairs for
## a given volume.

import json
import logging
import sys
import disk_ops

# Default kv side car alignment
KV_ALIGN = 4096

# Flag to track the version of Python on the platform
# All possible metadata keys for the volume. New keys should be added here as
# constants pointing to strings.

# The status of the volume, whether its attached or not to a VM.
STATUS = 'status'
# Timestamp of volume creation
CREATED = 'created'
# Name of the VM that created the volume
CREATED_BY = 'created-by'
# The name of the VM that the volume is attached to
ATTACHED_VM_NAME = 'attachedVMName'
# The UUID of the VM that the volume is attached to
ATTACHED_VM_UUID = 'attachedVMUuid'

# Dictionary of options passed in by the user
VOL_OPTS = 'volOpts'
# Options below this line are keys in the VOL_OPTS dict.

# The name of the VSAN policy applied to the VSAN volume. Invalid for Non-VSAN
# volumes.
VSAN_POLICY_NAME = 'vsan-policy-name'
# The size of the volume
SIZE = 'size'

# The disk allocation format for vmdk
DISK_ALLOCATION_FORMAT = 'diskformat'

VALID_ALLOCATION_FORMATS = ["zeroedthick", "thin", "eagerzeroedthick"]

DEFAULT_ALLOCATION_FORMAT = 'thin'

## Values for given keys

## Value for VSAN_POLICY_NAME
DEFAULT_VSAN_POLICY = '[VSAN default]'

## Values for STATUS
ATTACHED = 'attached'
DETACHED = 'detached'

# Create a kv store object for this volume identified by vol_path
# Create the side car or open if it exists.
def init():
   disk_ops.init()

def create(vol_path, vol_meta):
    """
    Create a side car KV store for given vol_path.
    Return true if successful, false otherwise
    """
    if disk_ops.create_kv(vol_path):
       return save(vol_path, vol_meta)
    return False


def delete(vol_path):
    """
    Delete a kv store object for this volume identified by vol_path.
    Return true if successful, false otherwise
    """
    return disk_ops.delete_kv(vol_path)


def getAll(vol_path):
    """
    Return the entire meta-data for the given vol_path.
    Return true if successful, false otherwise
    """
    return load(vol_path)


def setAll(vol_path, vol_meta):
    """
    Store the meta-data for a given vol-path
    Return true if successful, false otherwise
    """
    if vol_meta:
        return save(vol_path, vol_meta)
    # No data to save
    return True


# Set a string value for a given key(index)
def set_kv(vol_path, key, val):
    vol_meta = load(vol_path)

    if not vol_meta:
        return False

    vol_meta[key] = val

    return save(vol_path, vol_meta)


def get_kv(vol_path, key):
    """
    Return a string value for the given key, or None if the key is not present.
    """
    vol_meta = load(vol_path)

    if not vol_meta:
        return None

    if key in vol_meta:
        return vol_meta[key]
    else:
        return None


def remove(vol_path, key):
    """
    Remove a key/value pair from the store. Return true on success, false on
    error.
    """
    vol_meta = load(vol_path)

    if not vol_meta:
        return False

    if key in vol_meta:
        del vol_meta[key]

    return save(vol_path, vol_meta)


# Align a given string to the specified block boundary.
def align_str(kv_str, block):
   # Align string to the next block boundary. The -1 is to accommodate
   # a newline at the end of the string.
   aligned_len = int((len(kv_str) + block - 1) / block) * block - 1
   return '{:<{width}}\n'.format(kv_str, width=aligned_len)


# Load and return dictionary from the kv
def load(vol_path):
    meta_file = disk_ops.get_kv_name(vol_path)

    try:
       with open(meta_file, "r") as fh:
          kv_str = fh.read()
    except:
        logging.exception("Failed to access %s", meta_file)
        return None

    try:
       return json.loads(kv_str)
    except ValueError:
       logging.exception("Failed to decode meta-data for %s", vol_path);
       return None


# Save the dictionary to kv.
def save(vol_path, vol_meta):
    meta_file = disk_ops.get_kv_name(vol_path)

    kv_str = json.dumps(vol_meta)

    try:
       with open(meta_file, "w") as fh:
          fh.write(align_str(kv_str, KV_ALIGN))
    except:
        logging.exception("Failed to save meta-data for %s", vol_path);
        return False

    return True
