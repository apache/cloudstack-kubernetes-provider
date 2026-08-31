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

# One-shot bring-up of the full simulated environment:
# simulator + zone -> kind cluster -> CloudStack VMs -> CCM.

set -euo pipefail
here="$(dirname "${BASH_SOURCE[0]}")"

"${here}/10-simulator-up.sh"
"${here}/20-kind-up.sh"
"${here}/30-topology-isolated.sh"
"${here}/40-ccm-deploy.sh"

echo
echo "Environment is up. Try it:"
echo "  export KUBECONFIG=${here}/_out/kubeconfig"
echo "  kubectl create deployment web --image=nginx"
echo "  kubectl expose deployment web --port=80 --type=LoadBalancer"
echo "  kubectl get svc web -w   # EXTERNAL-IP appears from 192.168.2.0/24"
echo
echo "Run the e2e suite:  make test-e2e"
echo "Tear down:          ${here}/99-down.sh"
