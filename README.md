# Cloudstack Cloud Controller Manager

[![](https://img.shields.io/github/release/swisstxt/cloudstack-cloud-controller-manager.svg?style=flat-square "Release")](https://github.com/swisstxt/cloudstack-cloud-controller-manager/releases)
[![](https://img.shields.io/badge/license-Apache%202.0-blue.svg?style=flat-square "Apache 2.0 license")](/LICENSE-2.0)
[![](https://img.shields.io/badge/language-Go-%235adaff.svg?style=flat-square "Go language")](https://golang.org)
[![](https://img.shields.io/docker/build/swisstxt/cloudstack-cloud-controller-manager.svg?style=flat-square "Docker build status")](https://hub.docker.com/r/swisstxt/cloudstack-cloud-controller-manager/)

A Cloud Controller Manager to facilitate Kubernetes deployments on Cloudstack.

Based on the old Cloudstack provider in Kubernetes that will be removed soon.

## Migration

There are several notable differences from the old cloud provider that need to be taken into
account when migrating to the standalone provider.

### Load Balancer

Load balancer rule names now include the protocol as well as the LB name and service port.
This was added to distinguish tcp, udp and tcp-proxy service operating on the same port.
Without this change, it would not be possible to map, for example, a service that runs on both TCP and UDP port 8000.

:warning: **If you have existing rules, remove them before upgrading and them back afterwards.**

If you don't do this, you need to manually remove the rules in CloudStack later.

### Metadata

When kubelet still contained cloud provider code, node metadata was fetched from the DHCP
server on the instance's Virtual Router.

This is no longer possible with the standalone cloud controller, so all metadata now comes from
the Cloudstack API. Some metadata may be missing or wrong, please file bugs when this happens to you.

### Node Labels

When doing a seamless migration without installing new nodes, it may be possible that old nodes do have labels from the cloud provider set. To trigger reassignment, simply taint the old nodes with the "uninitialized" taint, and the cloud controller will assign the labels:

```
kubectl taint nodes my-old-node node.cloudprovider.kubernetes.io/uninitialized=true:NoSchedule
```

## Build

All dependencies are vendored.
You need GNU make, git and Go 1.11 to build cloudstack-ccm.

It's still possible to build with Go 1.10, but you need to remove the option `-mod vendor` from the
`cloudstack-ccm` compilation target in the `Makefile`.

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

To deploy the ccm in the cluster see [deployment.yaml](/deployment.yaml) and configure your cloudstack and api server connection. See the comments.

### Protocols

This CCM supports TCP, UDP and [TCP-Proxy](https://www.haproxy.org/download/1.8/doc/proxy-protocol.txt)
LoadBalancer deployments.

For UDP and Proxy Protocol support, CloudStack 4.6 or later is required.

Since kube-proxy does not support the Proxy Protocol or UDP, you should connect this
directly to containers, for example by deploying a DaemonSet and setting `hostNetwork: true`.

See [service.yaml](/service.yaml) for an example Service deployment and part
of a suitable configuration for an ingress controller.

### Development

Make sure your apiserver is running locally and keep your cloudstack config ready:

```bash
./cloudstack-ccm --cloud-provider external-cloudstack --cloud-config cloud.config --master localhost
```

## Copyright

© 2018 SWISS TXT AG and the Kubernetes authors.

See LICENSE-2.0 for permitted usage.
