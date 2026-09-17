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

# Generates cloud-config files, loads the CCM image into kind, deploys
# deployment.yaml and waits until all nodes are initialized.

set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/env.sh"
source "${E2E_ROOT}/lib/log.sh"

# shellcheck source=/dev/null
source "${E2E_OUT}/keys.env" 2>/dev/null || die "missing ${E2E_OUT}/keys.env — run 10-simulator-up.sh first"

# The CCM pod must reach the simulator via its IP on the shared docker
# network: pods cannot resolve docker's embedded DNS, and host.docker.internal
# does not exist on Linux.
sim_ip="$(docker inspect -f "{{(index .NetworkSettings.Networks \"${E2E_NET}\").IPAddress}}" "$SIM_NAME")"
[[ -n "$sim_ip" ]] || die "could not determine simulator IP on ${E2E_NET}"

# PROJECT_ID is optional; 50-topology-vpc.sh re-runs this script with it set.
project_line=""
if [[ -n "${E2E_PROJECT_ID:-}" ]]; then
    project_line="project-id = ${E2E_PROJECT_ID}"
fi

# In-cluster and host-process configs differ only in api-url.
cat >"${E2E_OUT}/cloud-config" <<EOF
[Global]
api-url    = http://${sim_ip}:8080/client/api
api-key    = ${CS_API_KEY}
secret-key = ${CS_SECRET_KEY}
zone       = ${ZONE_NAME}
region     = ${E2E_REGION}
${project_line}
EOF
sed "s|http://${sim_ip}:8080|http://localhost:${SIM_HOST_PORT}|" \
    "${E2E_OUT}/cloud-config" >"${E2E_OUT}/cloud-config-host"
chmod 600 "${E2E_OUT}/cloud-config" "${E2E_OUT}/cloud-config-host"

# Rebuild unless the image was supplied from outside. Reusing whatever happens to
# carry the tag would silently test a stale binary after the checkout changes.
if [[ "$CCM_IMAGE_PREBUILT" == "true" ]]; then
    docker image inspect "$CCM_IMAGE" >/dev/null 2>&1 ||
        die "CCM_IMAGE_PREBUILT=true but ${CCM_IMAGE} is not loaded"
    log "using prebuilt ${CCM_IMAGE}"
else
    log "building ${CCM_IMAGE} from ${REPO_ROOT}"
    docker build -t "$CCM_IMAGE" "$REPO_ROOT"
fi
kind load docker-image "$CCM_IMAGE" --name "$KIND_CLUSTER"

kubectl -n kube-system create secret generic cloudstack-secret \
    --from-file=cloud-config="${E2E_OUT}/cloud-config" \
    --dry-run=client -o yaml | kubectl apply -f -

kubectl apply -f "${REPO_ROOT}/deployment.yaml"
# Adjust the stock manifest for e2e: local image, no leader election (single
# replica, faster startup), verbose logs, and enough CPU that informer startup
# is not throttled on shared runners.
kubectl -n kube-system patch deployment cloud-controller-manager --type=json -p '[
  {"op":"replace","path":"/spec/template/spec/containers/0/image","value":"'"$CCM_IMAGE"'"},
  {"op":"replace","path":"/spec/template/spec/containers/0/imagePullPolicy","value":"Never"},
  {"op":"replace","path":"/spec/template/spec/containers/0/args","value":[
     "--cloud-provider=external-cloudstack","--cloud-config=/config/cloud-config",
     "--leader-elect=false","--v=4"]},
  {"op":"replace","path":"/spec/template/spec/containers/0/resources","value":{
     "requests":{"cpu":"100m","memory":"128Mi"},"limits":{"cpu":"1","memory":"512Mi"}}}
]'

kubectl -n kube-system rollout status deployment/cloud-controller-manager --timeout=180s

nodes_initialized() {
    local taints
    taints="$(kubectl get nodes -o jsonpath='{.items[*].spec.taints[?(@.key=="node.cloudprovider.kubernetes.io/uninitialized")].key}')"
    [[ -z "$taints" ]]
}
# If this times out, check the CCM log for
# 'provided node ip for node ... is not valid': it means the CloudStack VM's
# NIC IP does not match the kind node's docker IP.
wait_for 300 5 "all nodes initialized by the CCM" nodes_initialized

log "CCM deployed and all nodes initialized"
kubectl get nodes -o wide >&2
