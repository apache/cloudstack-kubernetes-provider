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

# Tears down everything the harness created.

set -uo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/env.sh"
source "${E2E_ROOT}/lib/log.sh"

log "deleting kind cluster ${KIND_CLUSTER}"
kind delete cluster --name "$KIND_CLUSTER" 2>/dev/null

log "removing simulator container ${SIM_NAME}"
docker rm -f "$SIM_NAME" 2>/dev/null

log "removing docker network ${E2E_NET}"
docker network rm "$E2E_NET" 2>/dev/null

rm -f "${E2E_OUT}/keys.env" "${E2E_OUT}/cloud-config" "${E2E_OUT}/cloud-config-host" \
    "${E2E_OUT}/kubeconfig" "${E2E_OUT}/node-ips" "${E2E_OUT}/ids.env" "${E2E_OUT}/cmk.ini"

log "done"
exit 0
