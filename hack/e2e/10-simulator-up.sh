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

# Starts the CloudStack simulator, waits for it to be usable, deploys the
# advanced zone and mints admin API keys into _out/keys.env.

set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/env.sh"
source "${E2E_ROOT}/lib/log.sh"
source "${E2E_ROOT}/lib/cmk.sh"

# --- docker network shared with kind -----------------------------------------
if ! docker network inspect "$E2E_NET" >/dev/null 2>&1; then
    log "creating docker network ${E2E_NET} (${E2E_SUBNET})"
    docker network create --driver bridge --subnet "$E2E_SUBNET" --gateway "$E2E_GW" "$E2E_NET"
fi

# --- simulator container ------------------------------------------------------
if docker inspect "$SIM_NAME" >/dev/null 2>&1 &&
    [[ "$(docker inspect -f '{{.Config.Image}}' "$SIM_NAME")" != "$SIM_IMAGE" ]]; then
    log "simulator container ${SIM_NAME} was created from $(docker inspect -f '{{.Config.Image}}' "$SIM_NAME"), not ${SIM_IMAGE}; recreating it"
    docker rm -f "$SIM_NAME" >/dev/null
fi

# docker inspect succeeds for a stopped container too, so check the run state
# explicitly rather than "reusing" one that was never started.
if docker inspect "$SIM_NAME" >/dev/null 2>&1; then
    if [[ "$(docker inspect -f '{{.State.Running}}' "$SIM_NAME")" == "true" ]]; then
        log "simulator container ${SIM_NAME} already running, reusing it"
    else
        log "simulator container ${SIM_NAME} exists but is stopped, starting it"
        docker start "$SIM_NAME" >/dev/null
    fi
else
    log "starting simulator ${SIM_IMAGE} as ${SIM_NAME}"
    # 8080 is the management API; 5050 is only the UI dev server.
    docker run -d --name "$SIM_NAME" \
        --network "$E2E_NET" --network-alias cloudstack-simulator \
        -p "127.0.0.1:${SIM_HOST_PORT}:8080" \
        "$SIM_IMAGE"
fi

# --- staged readiness ---------------------------------------------------------
jetty_up() {
    local code
    code="$(curl -s -o /dev/null -w '%{http_code}' -m 5 \
        "${CS_API_URL}?command=listCapabilities&response=json")"
    [[ "$code" == "401" || "$code" == "200" ]]
}

mgmt_server_up() {
    # The CCM reads this same field at startup and needs it parseable.
    local version
    version="$(cmk -c "$CMK_CONFIG" listManagementServersMetrics 2>/dev/null |
        jq -r '.managementserver[0].version // empty')"
    [[ -n "$version" ]]
}

cmk_init

wait_for 600 5 "jetty answering ${CS_API_URL}" jetty_up
wait_for 300 5 "CloudStack API accepting ${CS_ADMIN_USER} credentials" cmk_ready
wait_for 300 5 "management server registered" mgmt_server_up

# --- zone ---------------------------------------------------------------------
zone_enabled() {
    local state
    state="$(cmk -c "$CMK_CONFIG" listZones "name=${ZONE_NAME}" | jq -r '.zone[0].allocationstate // empty')"
    [[ "$state" == "Enabled" ]]
}

host_up() {
    local id
    id="$(cmk -c "$CMK_CONFIG" listHosts type=Routing state=Up 2>/dev/null |
        jq -r '.host[0].id // empty')"
    [[ -n "$id" ]]
}

if zone_enabled; then
    log "zone ${ZONE_NAME} already deployed"
else
    log "deploying zone ${ZONE_NAME} (this takes a few minutes)"
    docker exec "$SIM_NAME" python3 /root/tools/marvin/marvin/deployDataCenter.py \
        -i /root/setup/dev/advanced.cfg
fi
wait_for 600 10 "zone ${ZONE_NAME} enabled" zone_enabled
wait_for 300 10 "at least one routing host up" host_up

# --- admin API keys -----------------------------------------------------------
admin_user_id="$(cmk -c "$CMK_CONFIG" listUsers "username=${CS_ADMIN_USER}" | jq -r '.user[0].id')"
[[ -n "$admin_user_id" && "$admin_user_id" != "null" ]] || die "could not find user ${CS_ADMIN_USER}"

# getUserKeys first: registerUserKeys would rotate (invalidate) an existing
# pair, which is unfriendly to a long-lived local simulator.
keys="$(cmk -c "$CMK_CONFIG" getUserKeys "id=${admin_user_id}")"
api_key="$(jq -r '.userkeys.apikey // empty' <<<"$keys")"
secret_key="$(jq -r '.userkeys.secretkey // empty' <<<"$keys")"
if [[ -z "$api_key" || -z "$secret_key" ]]; then
    log "no existing keys, registering new ones"
    keys="$(cmk -c "$CMK_CONFIG" registerUserKeys "id=${admin_user_id}")"
    api_key="$(jq -r '.userkeys.apikey' <<<"$keys")"
    secret_key="$(jq -r '.userkeys.secretkey' <<<"$keys")"
fi
[[ -n "$api_key" && -n "$secret_key" ]] || die "failed to obtain admin API keys"

cat >"${E2E_OUT}/keys.env" <<EOF
export CS_API_KEY='${api_key}'
export CS_SECRET_KEY='${secret_key}'
EOF
chmod 600 "${E2E_OUT}/keys.env"
log "simulator ready; admin API keys written to ${E2E_OUT}/keys.env"
