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

BUILD_DATE=$(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
GIT_COMMIT=$(shell git rev-parse HEAD)
GIT_COMMIT_SHORT=$(shell git rev-parse --short HEAD)
GIT_TAG=$(shell git describe --abbrev=0 --tags 2>/dev/null || echo v0.0.0)
GIT_IS_TAG=$(shell git describe --exact-match --abbrev=0 --tags 2>/dev/null || echo NOT_A_TAG)
ifeq (${GIT_IS_TAG},NOT_A_TAG)
GIT_VERSION?=$(patsubst v%,%,${GIT_TAG})-main+${GIT_COMMIT}
else
GIT_VERSION?=$(patsubst v%,%,${GIT_TAG})
endif
LDFLAGS="-X k8s.io/kubernetes/pkg/version.gitVersion=${GIT_VERSION} -X k8s.io/kubernetes/pkg/version.gitCommit=${GIT_COMMIT} -X k8s.io/kubernetes/pkg/version.buildDate=${BUILD_DATE}"
export CGO_ENABLED=0
export GO111MODULE=on

# Keep these in step with hack/e2e/env.sh: both are overridable from the
# environment, so `make test-e2e` reaches the same simulator `make e2e-up`
# published rather than assuming the default port.
SIM_HOST_PORT ?= 8080
CS_API_URL ?= http://localhost:$(SIM_HOST_PORT)/client/api
# Exported so the harness scripts and the acceptance tests in `make test` --
# which read CS_API_URL from the environment -- agree with the targets here
# without every caller having to repeat the endpoint.
export SIM_HOST_PORT
export CS_API_URL

CMD_SRC=\
	cmd/cloudstack-ccm/main.go

.PHONY: all clean docker e2e-up e2e-down e2e-vpc test-e2e test-e2e-vpc

all: cloudstack-ccm

clean:
	rm -f cloudstack-ccm

cloudstack-ccm: ${CMD_SRC}
	go build -ldflags ${LDFLAGS} -o $@ $^

test: gofmt
	go test -v -coverprofile=coverage.txt -covermode=atomic
	go vet

docker: gofmt
	docker build . -t apache/cloudstack-kubernetes-provider:${GIT_COMMIT_SHORT}
	docker tag apache/cloudstack-kubernetes-provider:${GIT_COMMIT_SHORT} apache/cloudstack-kubernetes-provider:latest
ifneq (${GIT_IS_TAG},NOT_A_TAG)
	docker tag apache/cloudstack-kubernetes-provider:${GIT_COMMIT_SHORT} apache/cloudstack-kubernetes-provider:${GIT_TAG}
endif

# Simulator-based e2e environment; see docs/development.md
e2e-up:
	hack/e2e/up.sh

e2e-down:
	hack/e2e/99-down.sh

# go test runs with the package directory as its working directory, so
# KUBECONFIG must be absolute.
test-e2e:
	@test -f hack/e2e/_out/keys.env || (echo "environment not up; run 'make e2e-up' first" && exit 1)
	. hack/e2e/_out/keys.env && \
	KUBECONFIG=${CURDIR}/hack/e2e/_out/kubeconfig \
	CS_API_URL=$(CS_API_URL) \
	go test -tags e2e -v -timeout 30m ./test/e2e/... -run 'TestLB|TestNode|TestAnnot'

# Phase 2. Run hack/e2e/50-topology-vpc.sh first: it creates the project, VPC
# and tier, and re-points the CCM at the project. These tests only exercise
# anything with CS_PROJECT_ID set, and skip otherwise.
e2e-vpc:
	hack/e2e/50-topology-vpc.sh

test-e2e-vpc:
	@test -f hack/e2e/_out/ids.env || (echo "VPC topology not created; run 'make e2e-vpc' first" && exit 1)
	@grep -q E2E_PROJECT_ID hack/e2e/_out/ids.env || (echo "VPC topology not created; run 'make e2e-vpc' first" && exit 1)
	. hack/e2e/_out/keys.env && . hack/e2e/_out/ids.env && \
	KUBECONFIG=${CURDIR}/hack/e2e/_out/kubeconfig \
	CS_API_URL=$(CS_API_URL) \
	CS_PROJECT_ID="$$E2E_PROJECT_ID" \
	E2E_ACL_ID="$$E2E_ACL_ID" E2E_VPC_ID="$$E2E_VPC_ID" \
	go test -tags e2e -v -timeout 30m ./test/e2e/... -run 'TestVPC'

lint: gofmt
	@(echo "Running golangci-lint...")
	golangci-lint run

gofmt:
	@(echo "Running gofmt...")
	@(echo "gofmt -l"; FMTFILES="$$(gofmt -l .)"; if test -n "$${FMTFILES}"; then echo "Go files that need to be reformatted (use 'go fmt'):\n$${FMTFILES}"; exit 1; fi)
