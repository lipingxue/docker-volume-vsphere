#!/bin/bash
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

filename=$1

i=0
idx=0
while read -r line
do
    echo $i
    if [ $i == 0 ]
    then
        # NODE_COUNT is the total number of nodes that in the swarm cluster
        NODE_COUNT=$line
        echo "NODE_COUNT  $NODE_COUNT"
    fi
    if [ $i == 1 ]
    then
        # MGR_COUNT is the total number of manager nodes in the swarm cluster
        MGR_COUNT=$line
        echo "MGR_COUNT $MGR_COUNT"
    fi
    if [ $i > 1 ]
    then
        # Read IP address in to array
        # In the configuration file, the first $MGR_COUNT line of IP address
        # will be the IP address of swarm manager node
        IP_ADDRESS[idx]=$line
        idx=$((idx+1))
    fi
    i=$((i+1))
done < $filename

IP_COUNT=${#IP_ADDRESS[@]}

echo "IP_COUNT $IP_ADDRESS"

# if [ $MGR_COUNT \> $NODE_COUNT ]
# then
#     echo "Total number of nodes cannot be smaller than the total number of manager nodes"
#     exit 1
# fi

if [ $((MGR_COUNT%2)) -eq 0 ]
then
    echo "Total number of manager nodes in the swarm cluster cannot be a even number"
    exit 1
fi

if [ $MGR_COUNT > 7 ]
then
    echo "Total number of manager in the swarm cluster is too big"
    exit 1
fi

if [ $NODE_COUNT != $IP_COUNT]
then
    echo "Total number of nodes does not match the number of IP addresses"
    exit 1
fi

