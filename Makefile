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

CMD_SRC=\
	cmd/cloudstack-ccm/main.go

.PHONY: all clean docker

all: cloudstack-ccm

clean:
	rm -f cloudstack-ccm

cloudstack-ccm: ${CMD_SRC}
	go build -ldflags ${LDFLAGS} -o $@ $^

test:
	go test -v
	go vet
	@(echo "gofmt -l"; FMTFILES="$$(gofmt -l .)"; if test -n "$${FMTFILES}"; then echo "Go files that need to be reformatted (use 'go fmt'):\n$${FMTFILES}"; exit 1; fi)

docker:
	docker build . -t apache/cloudstack-kubernetes-provider:${GIT_COMMIT_SHORT}
	docker tag apache/cloudstack-kubernetes-provider:${GIT_COMMIT_SHORT} apache/cloudstack-kubernetes-provider:latest
ifneq (${GIT_IS_TAG},NOT_A_TAG)
	docker tag apache/cloudstack-kubernetes-provider:${GIT_COMMIT_SHORT} apache/cloudstack-kubernetes-provider:${GIT_TAG}
endif
