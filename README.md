# Cloudstack Cloud Controller Manager

[![](https://img.shields.io/github/release/swisstxt/cloudstack-cloud-controller-manager.svg?style=flat-square "Release")](https://github.com/swisstxt/cloudstack-cloud-controller-manager/releases)
[![](https://img.shields.io/badge/license-Apache%202.0-blue.svg?style=flat-square "Apache 2.0 license")](/LICENSE-2.0)
[![](https://img.shields.io/badge/language-Go-%235adaff.svg?style=flat-square "Go language")](https://golang.org)
[![](https://img.shields.io/docker/build/swisstxt/cloudstack-cloud-controller-manager.svg?style=flat-square "Docker build status")](https://hub.docker.com/r/swisstxt/cloudstack-cloud-controller-manager/)

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

### Kubernetes

To deploy the ccm in the cluster see [deployment.yaml](https://github.com/swisstxt/cloudstack-cloud-controller-manager/blob/master/deployment.yaml) and configure your cloudstack and api server connection. See the comments.

### Development

Make sure your apiserver is running locally and keep your cloudstack config ready:

```bash
./cloudstack-ccm --cloud-provider external-cloudstack --cloud-config cloud.config --master localhost
```

## Copyright

© 2018 SWISS TXT AG and the Kubernetes authors.

See LICENSE-2.0 for permitted usage.
