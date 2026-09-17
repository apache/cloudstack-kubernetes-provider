#!/usr/bin/env bash
# Licensed to the Apache Software Foundation (ASF) under one
# or more contributor license agreements.  See the NOTICE file
# distributed with this work for additional information
# regarding copyright ownership.  The ASF licenses this file
# to you under the Apache License, Version 2.0 (the
# "License"); you may not use this file except in compliance
# with the License.  You may obtain a copy of the License at
#
#   http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing,
# software distributed under the License is distributed on an
# "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
# KIND, either express or implied.  See the License for the
# specific language governing permissions and limitations
# under the License.

# Creates the kind cluster on the shared docker network and records the
# node-name -> docker-IP map that the CloudStack VMs must reproduce.

set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/env.sh"
source "${E2E_ROOT}/lib/log.sh"

if kind get clusters 2>/dev/null | grep -qx "$KIND_CLUSTER"; then
    log "kind cluster ${KIND_CLUSTER} already exists, reusing it"
else
    log "creating kind cluster ${KIND_CLUSTER} (image ${KIND_NODE_IMAGE}) on network ${E2E_NET}"
    KIND_EXPERIMENTAL_DOCKER_NETWORK="$E2E_NET" kind create cluster \
        --name "$KIND_CLUSTER" \
        --config "${E2E_ROOT}/kind-config.yaml" \
        --image "$KIND_NODE_IMAGE" \
        --wait 180s
fi

kind get kubeconfig --name "$KIND_CLUSTER" >"${E2E_OUT}/kubeconfig"
chmod 600 "${E2E_OUT}/kubeconfig"

# The CCM only initializes a node when the CloudStack VM's NIC IP matches the
# IP kubelet registered with (kind passes --node-ip). Record each node's IP on
# the shared network so 30-topology-* can pin the VMs to them.
: >"${E2E_OUT}/node-ips"
for node in $(kind get nodes --name "$KIND_CLUSTER"); do
    ip="$(docker inspect -f "{{(index .NetworkSettings.Networks \"${E2E_NET}\").IPAddress}}" "$node")"
    [[ -n "$ip" ]] || die "could not determine IP of ${node} on ${E2E_NET}"
    echo "${node} ${ip}" >>"${E2E_OUT}/node-ips"
done
log "node IPs:"
cat "${E2E_OUT}/node-ips" >&2

log "kind cluster ready; kubeconfig at ${E2E_OUT}/kubeconfig"
