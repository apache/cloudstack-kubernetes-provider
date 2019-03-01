# Cloudstack Cloud Controller Manager

[![](https://img.shields.io/github/release/swisstxt/cloudstack-cloud-controller-manager.svg?style=flat-square "Release")](https://github.com/swisstxt/cloudstack-cloud-controller-manager/releases)
[![](https://img.shields.io/badge/license-Apache%202.0-blue.svg?style=flat-square "Apache 2.0 license")](/LICENSE-2.0)
[![](https://img.shields.io/badge/language-Go-%235adaff.svg?style=flat-square "Go language")](https://golang.org)
[![](https://img.shields.io/docker/build/swisstxt/cloudstack-cloud-controller-manager.svg?style=flat-square "Docker build status")](https://hub.docker.com/r/swisstxt/cloudstack-cloud-controller-manager/)

A Cloud Controller Manager to facilitate Kubernetes deployments on Cloudstack.

Based on the old Cloudstack provider in Kubernetes that will be removed soon.

## Migration

There are several notable differences to the old Kubernetes CloudStack cloud provider that need to be taken into
account when migrating to the standalone controller.

### Load Balancer

Load balancer rule names now include the protocol in addition to the LB name and service port.
This was added to distinguish tcp, udp and tcp-proxy services operating on the same port.
Without this change, it would not be possible to map a service that runs on both TCP and UDP port 8000, for example.

:warning: **If you have existing rules, remove them before the migration, and add them back afterwards.**

If you don't do this, you will end up with duplicate rules for the same service, which won't work.

### Metadata

When kubelet still contained cloud provider code, node metadata was fetched from the DHCP
server running on the Virtual Router.

This is no longer possible with the standalone cloud controller, so all metadata now comes from
the Cloudstack API. Some metadata may be missing or wrong, please file bugs when this happens to you.

### Node Labels

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

If you intend to deploy the CCM in a Kubernetes cluster, please see [deployment.yaml](/deployment.yaml) for an example configuration. The comments explain how to configure Cloudstack API access.

### Protocols

This CCM supports TCP, UDP and [TCP-Proxy](https://www.haproxy.org/download/1.8/doc/proxy-protocol.txt)
LoadBalancer deployments.

For UDP and Proxy Protocol support, CloudStack 4.6 or later is required.

Since kube-proxy does not support the Proxy Protocol or UDP, you should connect this
directly to containers, for example by deploying a DaemonSet and setting `hostNetwork: true`.

See [service.yaml](/service.yaml) for an example Service deployment and part
of a suitable configuration for an ingress controller.

### Development

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
