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

# Collects debugging artifacts from the simulator, the kind cluster and the
# CloudStack API into _out/artifacts. Never fails.

set -uo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/env.sh"
source "${E2E_ROOT}/lib/log.sh"
source "${E2E_ROOT}/lib/cmk.sh"

ART="${E2E_OUT}/artifacts"
mkdir -p "$ART"

log "collecting artifacts into ${ART}"

docker logs "$SIM_NAME" >"${ART}/simulator.log" 2>&1

# kubectl logs may stop working once the CCM rewrites node addresses, so fall
# back to reading container logs on the control-plane node directly.
if ! kubectl -n kube-system logs deployment/cloud-controller-manager --tail=-1 \
    >"${ART}/ccm.log" 2>&1; then
    docker exec "${KIND_CLUSTER}-control-plane" bash -c \
        'crictl ps -a --name cloud-controller-manager -q | head -1 | xargs -r crictl logs' \
        >"${ART}/ccm.log" 2>&1
fi

kubectl get nodes -o yaml >"${ART}/nodes.yaml" 2>&1
kubectl get svc -A -o yaml >"${ART}/services.yaml" 2>&1
kubectl describe svc -A >"${ART}/svc-describe.txt" 2>&1
kubectl get events -A --sort-by=.lastTimestamp >"${ART}/events.txt" 2>&1
kubectl -n kube-system get pods -o wide >"${ART}/kube-system-pods.txt" 2>&1

# cmk_init dies when cmk is missing, which would break the "never fails"
# contract above -- this script runs from an always() CI step, where exiting
# non-zero costs the CloudStack dumps and masks the original failure.
if ! command -v cmk >/dev/null 2>&1; then
    log "cmk is not installed; skipping CloudStack API dumps"
elif cmk_init && cmk_ready; then
    # projectid=-1 lets an admin list across all projects, so the VPC phase shows up too.
    for cmd in listLoadBalancerRules listPublicIpAddresses listFirewallRules \
        listNetworkACLs listVirtualMachines listNetworks; do
        name="cs-$(echo "$cmd" | tr '[:upper:]' '[:lower:]')"
        cmk -c "$CMK_CONFIG" "$cmd" listall=true | jq . >"${ART}/${name}.json" 2>&1
        cmk -c "$CMK_CONFIG" "$cmd" listall=true projectid=-1 | jq . >"${ART}/${name}-projects.json" 2>&1
    done
fi

kind export logs "${ART}/kind" --name "$KIND_CLUSTER" >/dev/null 2>&1

log "artifacts collected"
exit 0
