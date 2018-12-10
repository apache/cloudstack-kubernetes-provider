# Cloudstack Cloud Controller Manager

![](https://img.shields.io/docker/build/swisstxt/cloudstack-cloud-controller-manager.svg?style=flat-square "Docker build status")

A Cloud Controller Manager to facilitate Kubernetes deployments on Cloudstack.

Based on the old Cloudstack provider in kube-controller-manager.

## Build

All dependencies are vendored.
You need GNU make, git and Go 1.10 to build cloudstack-ccm.

```bash
go get github.com/swisstxt/cloudstack-cloud-controller-manager
cd ${GOPATH}/src/github.com/swisstxt/cloudstack-cloud-controller-manager
make
```

To build the cloudstack-cloud-controller-manager container, please use the provided Docker file:

```bash
docker build . -t swisstxt/cloudstack-cloud-controller-manager:latest
```

## Use

Prebuilt containers are posted on [Docker Hub](https://hub.docker.com/r/swisstxt/cloudstack-cloud-controller-manager).

**TODO** Add an example Kubernetes deployment.

Make sure your apiserver is running locally and keep your cloudstack config ready:

```bash
./cloudstack-ccm --cloud-provider external-cloudstack --cloud-config cloud.config --master localhost
```

## Copyright

© 2018 SWISS TXT AG and the Kubernetes authors.

See LICENSE-2.0 for permitted usage.
