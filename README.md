# Cloudstack Cloud Controller Manager

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

To build the cloudstack-ccm container, please use the provided Docker file:

```bash
make docker
```

## Use

Make sure your apiserver is running locally and keep your cloudstack config ready:

```bash
./cloudstack-ccm --cloud-provider external-cloudstack --cloud-config cloud.config --master localhost
```

## Copyright

© 2018 SWISS TXT AG and the Kubernetes authors
See LICENSE-2.0 for permitted usage.
