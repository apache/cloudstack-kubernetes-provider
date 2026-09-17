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

# CloudStack API access for the harness, via cmk (CloudMonkey).
#
# The scripts call cmk directly, always as:
#
#     cmk -c "$CMK_CONFIG" <command> [key=value ...]
#
# cmk resolves its config from either the -c flag or $HOME/.cmk/config and has
# no environment variable for it, so -c is passed explicitly rather than
# hijacking HOME. This keeps the developer's own ~/.cmk/config untouched, and
# means every command in the scripts is one you can paste into a shell.
#
# The generated profile authenticates with username/password rather than API
# keys, because the harness has to talk to CloudStack before any keys exist —
# it is what mints them. Two cmk defaults matter:
#
#   asyncblock = true   cmk waits for async jobs and returns the job result,
#                       so nothing here polls queryAsyncJobResult.
#   output     = json   responses omit the <command>response envelope, e.g.
#                       {"count":1,"zone":[...]}. Note that an empty result is
#                       zero bytes rather than {"count":0}.

CMK_CONFIG="${E2E_OUT}/cmk.ini"

cmk_init() {
    command -v cmk >/dev/null 2>&1 || die "cmk (CloudMonkey) is not installed — see docs/development.md"

    cat >"$CMK_CONFIG" <<EOF
output       = json
asyncblock   = true
timeout      = 1800
verifycert   = false
profile      = e2e

[e2e]
url       = ${CS_API_URL}
username  = ${CS_ADMIN_USER}
password  = ${CS_ADMIN_PASS}
domain    = /
apikey    =
secretkey =
EOF
    chmod 600 "$CMK_CONFIG"
}

# cmk_ready succeeds once the API answers an authenticated call.
cmk_ready() {
    [[ "$(cmk -c "$CMK_CONFIG" listCapabilities 2>/dev/null |
        jq -r '.capability.cloudstackversion // empty')" != "" ]]
}
