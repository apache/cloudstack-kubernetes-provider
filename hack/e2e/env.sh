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

# This file is sourced, not executed.
# shellcheck shell=bash

# All tunables for the simulator e2e harness in one place.
# Every value can be overridden from the environment (CI does this for the
# simulator tag and the kind node image).

E2E_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export E2E_ROOT
export REPO_ROOT="${E2E_ROOT}/../.."
export E2E_OUT="${E2E_ROOT}/_out"

# Docker network shared by the kind nodes and the simulator. The subnet must
# match the isolated network created in CloudStack: the CCM refuses to
# initialize a node whose kubelet-reported IP is missing from the CloudStack
# VM's NICs, so the VMs are deployed with the kind nodes' docker IPs.
export E2E_NET="${E2E_NET:-cs-ccm-e2e}"
export E2E_SUBNET="${E2E_SUBNET:-172.30.0.0/24}"
export E2E_GW="${E2E_GW:-172.30.0.1}"
export E2E_NETMASK="${E2E_NETMASK:-255.255.255.0}"

# CloudStack simulator
export SIM_NAME="${SIM_NAME:-cloudstack-simulator}"
export SIM_TAG="${SIM_TAG:-4.22.1.0}"
export SIM_IMAGE="${SIM_IMAGE:-apache/cloudstack-simulator:${SIM_TAG}}"
export SIM_HOST_PORT="${SIM_HOST_PORT:-8080}"
export CS_API_URL="${CS_API_URL:-http://localhost:${SIM_HOST_PORT}/client/api}"
export CS_ADMIN_USER="${CS_ADMIN_USER:-admin}"
export CS_ADMIN_PASS="${CS_ADMIN_PASS:-password}"
export ZONE_NAME="${ZONE_NAME:-Sandbox-simulator}"
export E2E_REGION="${E2E_REGION:-simulator-region}"

# kind
export KIND_CLUSTER="${KIND_CLUSTER:-cs-ccm-e2e}"
export KIND_NODE_IMAGE="${KIND_NODE_IMAGE:-kindest/node:v1.37.0}"

# CCM image built from this checkout. Set CCM_IMAGE_PREBUILT=true when the image
# was loaded from elsewhere (CI downloads it as an artifact) so 40-ccm-deploy.sh
# uses it as-is; otherwise it is rebuilt on every run to match the working tree.
export CCM_IMAGE="${CCM_IMAGE:-apache/cloudstack-kubernetes-provider:e2e}"
export CCM_IMAGE_PREBUILT="${CCM_IMAGE_PREBUILT:-false}"

# CloudStack names created by the harness
export E2E_ISO_NETWORK="${E2E_ISO_NETWORK:-ccm-e2e-iso}"
export E2E_PROJECT="${E2E_PROJECT:-ccm-e2e-vpc}"
export E2E_VPC="${E2E_VPC:-ccm-e2e-vpc}"
export E2E_VPC_CIDR="${E2E_VPC_CIDR:-172.30.0.0/22}"
export E2E_ACL_LIST="${E2E_ACL_LIST:-ccm-e2e-acl}"
export E2E_TIER="${E2E_TIER:-ccm-e2e-tier}"
export E2E_SERVICE_OFFERING="${E2E_SERVICE_OFFERING:-Small Instance}"

export KUBECONFIG="${KUBECONFIG:-${E2E_OUT}/kubeconfig}"

mkdir -p "${E2E_OUT}"
