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

# Phase 2: creates a CloudStack project containing a VPC, a custom ACL list, a
# tier network and per-node VMs, then re-points the CCM at the project.
#
# A project is used so the VPC VMs and the phase-1 isolated-network VMs are
# mutually invisible: the CCM's verifyHosts matches VM names account-wide and
# fails when matched VMs are on different networks. With project-id set, only
# project resources are visible. The tier reuses the docker subnet, so the VMs
# get the same IPs as phase 1 and node initialization keeps working.

set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/env.sh"
source "${E2E_ROOT}/lib/log.sh"
source "${E2E_ROOT}/lib/cmk.sh"

[[ -s "${E2E_OUT}/node-ips" ]] || die "missing ${E2E_OUT}/node-ips — run 20-kind-up.sh first"
# shellcheck source=/dev/null
source "${E2E_OUT}/ids.env" 2>/dev/null || die "missing ${E2E_OUT}/ids.env — run 30-topology-isolated.sh first"
cmk_init

# Phase-1 LoadBalancer services must be gone before the CCM switches projects,
# or their CloudStack resources leak (the project-scoped CCM can't see them).
leftover="$(kubectl get svc -A -o jsonpath='{range .items[?(@.spec.type=="LoadBalancer")]}{.metadata.namespace}/{.metadata.name}{"\n"}{end}')"
if [[ -n "$leftover" ]]; then
    log "deleting leftover LoadBalancer services:"
    echo "$leftover" >&2
    while IFS=/ read -r ns name; do
        kubectl -n "$ns" delete svc "$name" --wait=true --timeout=120s
    done <<<"$leftover"
fi

project_id="$(cmk -c "$CMK_CONFIG" listProjects listall=true "name=${E2E_PROJECT}" |
    jq -r '.project[0].id // empty')"
if [[ -z "$project_id" ]]; then
    log "creating project ${E2E_PROJECT}"
    project_id="$(cmk -c "$CMK_CONFIG" createProject "name=${E2E_PROJECT}" "displaytext=${E2E_PROJECT}" |
        jq -r '.project.id // .id')"
fi
[[ -n "$project_id" && "$project_id" != "null" ]] || die "failed to create project"

vpc_id="$(cmk -c "$CMK_CONFIG" listVPCs listall=true "projectid=${project_id}" "name=${E2E_VPC}" |
    jq -r '.vpc[0].id // empty')"
if [[ -z "$vpc_id" ]]; then
    vpc_offering_id="$(cmk -c "$CMK_CONFIG" listVPCOfferings "name=Default VPC offering" |
        jq -r '.vpcoffering[0].id')"
    log "creating VPC ${E2E_VPC} (${E2E_VPC_CIDR})"
    vpc_id="$(cmk -c "$CMK_CONFIG" createVPC "name=${E2E_VPC}" "displaytext=${E2E_VPC}" \
        "zoneid=${E2E_ZONE_ID}" "cidr=${E2E_VPC_CIDR}" \
        "vpcofferingid=${vpc_offering_id}" "projectid=${project_id}" |
        jq -r '.vpc.id // .id')"
fi
[[ -n "$vpc_id" && "$vpc_id" != "null" ]] || die "failed to create VPC"

# A custom ACL list: the CCM refuses to manage rules on the built-in
# default_allow / default_deny lists.
acl_id="$(cmk -c "$CMK_CONFIG" listNetworkACLLists "vpcid=${vpc_id}" "name=${E2E_ACL_LIST}" |
    jq -r '.networkacllist[0].id // empty')"
if [[ -z "$acl_id" ]]; then
    log "creating ACL list ${E2E_ACL_LIST}"
    acl_id="$(cmk -c "$CMK_CONFIG" createNetworkACLList "name=${E2E_ACL_LIST}" \
        "description=${E2E_ACL_LIST}" "vpcid=${vpc_id}" |
        jq -r '.networkacllist.id // .id')"
fi
[[ -n "$acl_id" && "$acl_id" != "null" ]] || die "failed to create ACL list"

tier_id="$(cmk -c "$CMK_CONFIG" listNetworks listall=true "projectid=${project_id}" "keyword=${E2E_TIER}" |
    jq -r --arg n "$E2E_TIER" '.network[]? | select(.name == $n) | .id')"
if [[ -z "$tier_id" ]]; then
    tier_offering_id="$(cmk -c "$CMK_CONFIG" listNetworkOfferings name=DefaultIsolatedNetworkOfferingForVpcNetworks state=Enabled |
        jq -r '.networkoffering[0].id')"
    log "creating VPC tier ${E2E_TIER} (${E2E_SUBNET})"
    tier_id="$(cmk -c "$CMK_CONFIG" createNetwork "name=${E2E_TIER}" "displaytext=${E2E_TIER}" \
        "zoneid=${E2E_ZONE_ID}" "networkofferingid=${tier_offering_id}" \
        "vpcid=${vpc_id}" "aclid=${acl_id}" \
        "gateway=${E2E_GW}" "netmask=${E2E_NETMASK}" "projectid=${project_id}" |
        jq -r '.network.id // .id')"
fi
[[ -n "$tier_id" && "$tier_id" != "null" ]] || die "failed to create VPC tier"

while read -r node ip; do
    existing="$(cmk -c "$CMK_CONFIG" listVirtualMachines listall=true "projectid=${project_id}" "keyword=${node}" |
        jq -r --arg n "$node" '.virtualmachine[]? | select(.name == $n) | .id')"
    if [[ -n "$existing" ]]; then
        log "project VM ${node} already exists"
        continue
    fi
    log "deploying project VM ${node} with IP ${ip}"
    cmk -c "$CMK_CONFIG" deployVirtualMachine "name=${node}" "displayname=${node}" \
        "zoneid=${E2E_ZONE_ID}" "serviceofferingid=${E2E_SERVICE_OFFERING_ID}" \
        "templateid=${E2E_TEMPLATE_ID}" "networkids=${tier_id}" \
        "ipaddress=${ip}" "projectid=${project_id}" "startvm=true" >/dev/null
done <"${E2E_OUT}/node-ips"

{
    echo "export E2E_PROJECT_ID='${project_id}'"
    echo "export E2E_VPC_ID='${vpc_id}'"
    echo "export E2E_ACL_ID='${acl_id}'"
    echo "export E2E_TIER_ID='${tier_id}'"
} >>"${E2E_OUT}/ids.env"

# Re-point the CCM at the project and restart it.
E2E_PROJECT_ID="$project_id" "${E2E_ROOT}/40-ccm-deploy.sh"
kubectl -n kube-system rollout restart deployment/cloud-controller-manager
kubectl -n kube-system rollout status deployment/cloud-controller-manager --timeout=180s

log "VPC topology ready (project ${project_id}, tier ${tier_id}, acl ${acl_id})"
