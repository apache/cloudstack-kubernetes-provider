# Cloudstack Cloud Controller Manager

[![](https://img.shields.io/github/release/swisstxt/cloudstack-cloud-controller-manager.svg?style=flat-square "Release")](https://github.com/swisstxt/cloudstack-cloud-controller-manager/releases)
[![](https://img.shields.io/badge/license-Apache%202.0-blue.svg?style=flat-square "Apache 2.0 license")](/LICENSE-2.0)
[![](https://img.shields.io/badge/language-Go-%235adaff.svg?style=flat-square "Go language")](https://golang.org)
[![](https://img.shields.io/docker/build/swisstxt/cloudstack-cloud-controller-manager.svg?style=flat-square "Docker build status")](https://hub.docker.com/r/swisstxt/cloudstack-cloud-controller-manager/)

A Cloud Controller Manager to facilitate Kubernetes deployments on Cloudstack.

Based on the old Cloudstack provider in Kubernetes that will be removed soon.

## Deployment

### Kubernetes

Prebuilt containers are posted on [Docker Hub](https://hub.docker.com/r/swisstxt/cloudstack-cloud-controller-manager).

The cloud controller is intended to be deployed as a daemon set, with on instance running on each node.

Please see [deployment.yaml](/deployment.yaml) for an example deployment.

The comments explain how to configure Cloudstack API access.
You need an access token that is allowed to fetch VM information and deploy load balancers in the project or domain where the nodes reside.

### Protocols

This CCM supports TCP, UDP and [TCP-Proxy](https://www.haproxy.org/download/1.8/doc/proxy-protocol.txt) LoadBalancer deployments.

For UDP and Proxy Protocol support, CloudStack 4.6 or later is required.

Since kube-proxy does not support the Proxy Protocol or UDP, you should connect this directly to pods, for example by deploying a DaemonSet and setting `hostNetwork: true`.
The service running in the pod must support the protocol.

See [service.yaml](/service.yaml) for an example Service deployment and part of a suitable configuration for an ingress controller.

### Node Labels

:warning: **The node name must match the host name, so the controller can fetch and assign metadata from CloudStack.**

It is recommended to launch `kubelet` with the following parameter:

```
--register-with-taints=node.cloudprovider.kubernetes.io/uninitialized=true:NoSchedule
```

This will treat the node as 'uninitialized' and cause the CCM to apply metadata labels from CloudStack automatically.

Supported labels are:
* kubernetes.io/hostname (= the instance name)
* beta.kubernetes.io/instance-type (= the compute offering)
* failure-domain.beta.kubernetes.io/zone (= the zone)
* failure-domain.beta.kubernetes.io/region (also = the zone)

It is also possible to trigger this process manually by issuing the following command:

```
kubectl taint nodes <my-node-without-labels> node.cloudprovider.kubernetes.io/uninitialized=true:NoSchedule
```

## Migration Guide

There are several notable differences to the old Kubernetes CloudStack cloud provider that need to be taken into
account when migrating from the old cloud provider to the standalone controller.

### Load Balancer

Load balancer rule names now include the protocol in addition to the LB name and service port.
This was added to distinguish tcp, udp and tcp-proxy services operating on the same port.
Without this change, it would not be possible to map a service that runs on both TCP and UDP port 8000, for example.

:warning: **If you have existing rules, remove them before the migration, and add them back afterwards.**

If you don't do this, you will end up with duplicate rules for the same service, which won't work.

### Metadata

Since the controller is now intended to be run inside a pod and not on the node, it will not be able to fetch metadata from the Virtual Router's DHCP server.

Instead, it first obtains the name of the node from Kubernetes, then fetches information from the CloudStack API.

## Development

### Building

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

### Testing

You need a local instance of the CloudStack Management Server or a 'real' one to connect to.
The CCM supports the same cloudstack.ini configuration file format used by [the cs tool](https://github.com/exoscale/cs),
so you can simply point it to that.

```bash
./cloudstack-ccm --cloud-provider external-cloudstack --cloud-config ~/.cloudstack.ini --master k8s-apiserver
```

Replace k8s-apiserver with the host name of your Kubernetes development clusters's API server.

If you don't have a 'real' CloudStack installation, you can also launch a local [simulator instance](https://hub.docker.com/r/cloudstack/simulator) instead. This is very useful for dry-run testing.

## Copyright

© 2018 SWISS TXT AG and the Kubernetes authors.

See LICENSE-2.0 for permitted usage.
