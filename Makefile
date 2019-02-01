VERSION=v0.0.2
BUILD_DATE=$(shell date -u +'%Y-%m-%dT%H:%M:%SZ')
GIT_COMMIT=$(shell git rev-parse HEAD)
GIT_COMMIT_SHORT=$(shell git rev-parse --short HEAD)
GIT_VERSION=${VERSION}-master+${GIT_COMMIT}
LDFLAGS="-X github.com/swisstxt/cloudstack-cloud-controller-manager/vendor/k8s.io/kubernetes/pkg/version.gitVersion=${GIT_VERSION} -X github.com/swisstxt/cloudstack-cloud-controller-manager/vendor/k8s.io/kubernetes/pkg/version.gitCommit=${GIT_COMMIT} -X github.com/swisstxt/cloudstack-cloud-controller-manager/vendor/k8s.io/kubernetes/pkg/version.buildDate=${BUILD_DATE}"
export CGO_ENABLED=0

CMD_SRC=\
	cmd/cloudstack-ccm/main.go

.PHONY: all clean docker

all: cloudstack-ccm

clean:
	rm -f cloudstack-ccm

cloudstack-ccm: ${CMD_SRC}
	go build -ldflags ${LDFLAGS} -o $@ $^

docker:
	docker build . -t cloudstack-cloud-controller-manager:${GIT_COMMIT_SHORT}
