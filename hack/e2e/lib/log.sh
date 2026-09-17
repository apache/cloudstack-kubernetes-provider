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

log() {
    echo "[$(date -u +%H:%M:%S)] $*" >&2
}

die() {
    log "FATAL: $*"
    exit 1
}

# wait_for <timeout-seconds> <interval-seconds> <description> <command...>
# Polls <command...> until it succeeds or the timeout elapses.
wait_for() {
    local timeout=$1 interval=$2 desc=$3
    shift 3
    local start=$SECONDS
    log "waiting up to ${timeout}s for: ${desc}"
    while ((SECONDS - start < timeout)); do
        if "$@" >/dev/null 2>&1; then
            log "ready after $((SECONDS - start))s: ${desc}"
            return 0
        fi
        sleep "$interval"
    done
    die "timed out after ${timeout}s waiting for: ${desc}"
}
