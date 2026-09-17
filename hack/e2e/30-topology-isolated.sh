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

# Creates an isolated guest network matching the kind docker subnet and
# deploys one CloudStack VM per kind node, pinned to the node's docker IP.

set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/env.sh"
source "${E2E_ROOT}/lib/log.sh"
source "${E2E_ROOT}/lib/cmk.sh"

[[ -s "${E2E_OUT}/node-ips" ]] || die "missing ${E2E_OUT}/node-ips — run 20-kind-up.sh first"
cmk_init

zone_id="$(cmk -c "$CMK_CONFIG" listZones "name=${ZONE_NAME}" | jq -r '.zone[0].id')"
[[ -n "$zone_id" && "$zone_id" != "null" ]] || die "zone ${ZONE_NAME} not found"

# The stock zone guest CIDR is 10.1.1.0/24; align it with the docker subnet so
# createNetwork accepts our gateway/netmask.
cmk -c "$CMK_CONFIG" updateZone "id=${zone_id}" "guestcidraddress=${E2E_SUBNET}" >/dev/null

net_id="$(cmk -c "$CMK_CONFIG" listNetworks "keyword=${E2E_ISO_NETWORK}" listall=true |
    jq -r --arg n "$E2E_ISO_NETWORK" '.network[]? | select(.name == $n) | .id')"
if [[ -z "$net_id" ]]; then
    offering_id="$(cmk -c "$CMK_CONFIG" listNetworkOfferings name=DefaultIsolatedNetworkOfferingWithSourceNatService state=Enabled |
        jq -r '.networkoffering[0].id')"
    [[ -n "$offering_id" && "$offering_id" != "null" ]] || die "isolated network offering not found"
    log "creating isolated network ${E2E_ISO_NETWORK} (${E2E_SUBNET})"
    net_id="$(cmk -c "$CMK_CONFIG" createNetwork "name=${E2E_ISO_NETWORK}" "displaytext=${E2E_ISO_NETWORK}" \
        "zoneid=${zone_id}" "networkofferingid=${offering_id}" \
        "gateway=${E2E_GW}" "netmask=${E2E_NETMASK}" |
        jq -r '.network.id // .id')"
fi
[[ -n "$net_id" && "$net_id" != "null" ]] || die "failed to create network ${E2E_ISO_NETWORK}"

offering_id="$(cmk -c "$CMK_CONFIG" listServiceOfferings "name=${E2E_SERVICE_OFFERING}" |
    jq -r '.serviceoffering[0].id')"
# templatefilter=executable excludes the SYSTEM (router) template, which
# cannot be used to deploy user VMs.
template_id="$(cmk -c "$CMK_CONFIG" listTemplates templatefilter=executable "zoneid=${zone_id}" hypervisor=Simulator |
    jq -r '.template[]? | select(.isready == true) | .id' | head -1)"
[[ -n "$offering_id" && "$offering_id" != "null" ]] || die "service offering '${E2E_SERVICE_OFFERING}' not found"
[[ -n "$template_id" ]] || die "no ready simulator template found"

while read -r node ip; do
    existing="$(cmk -c "$CMK_CONFIG" listVirtualMachines "keyword=${node}" listall=true |
        jq -r --arg n "$node" '.virtualmachine[]? | select(.name == $n) | .id')"
    if [[ -n "$existing" ]]; then
        log "VM ${node} already exists"
        continue
    fi
    log "deploying VM ${node} with IP ${ip}"
    cmk -c "$CMK_CONFIG" deployVirtualMachine "name=${node}" "displayname=${node}" \
        "zoneid=${zone_id}" "serviceofferingid=${offering_id}" "templateid=${template_id}" \
        "networkids=${net_id}" "ipaddress=${ip}" "startvm=true" >/dev/null
done <"${E2E_OUT}/node-ips"

# Post-condition: every node must now have a matching VM on the right IP, or
# the CCM will never initialize that node.
while read -r node ip; do
    vm_ip="$(cmk -c "$CMK_CONFIG" listVirtualMachines "keyword=${node}" listall=true |
        jq -r --arg n "$node" '.virtualmachine[]? | select(.name == $n) | .nic[0].ipaddress')"
    [[ "$vm_ip" == "$ip" ]] || die "VM ${node} has IP '${vm_ip}', expected ${ip}"
done <"${E2E_OUT}/node-ips"

cat >"${E2E_OUT}/ids.env" <<EOF
export E2E_ZONE_ID='${zone_id}'
export E2E_ISO_NETWORK_ID='${net_id}'
export E2E_SERVICE_OFFERING_ID='${offering_id}'
export E2E_TEMPLATE_ID='${template_id}'
EOF
log "isolated topology ready (network ${net_id})"
