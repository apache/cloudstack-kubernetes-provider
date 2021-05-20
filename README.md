# CloudStack Kubernetes Provider

[![](https://img.shields.io/github/release/apache/cloudstack-kubernetes-provider.svg?logo=github&style=flat-square "Release")](https://github.com/apache/cloudstack-kubernetes-provider/releases)
[![](https://img.shields.io/badge/license-Apache%202.0-blue.svg?color=%23282661&logo=apache&style=flat-square "Apache 2.0 license")](/LICENSE-2.0)
[![](https://img.shields.io/badge/language-Go-%235adaff.svg?logo=go&style=flat-square "Go language")](https://golang.org)
[![](https://img.shields.io/docker/v/apache/cloudstack-kubernetes-provider?label=docker%20hub&logo=docker&style=flat-square "Docker Hub Image Version")](https://hub.docker.com/r/apache/cloudstack-kubernetes-provider/)

A Cloud Controller Manager to facilitate Kubernetes deployments on Cloudstack.

Based on the old Cloudstack provider in Kubernetes that will be removed soon.

Refer:
* https://github.com/kubernetes/kubernetes/tree/release-1.15/pkg/cloudprovider/providers/cloudstack
* https://github.com/kubernetes/enhancements/issues/672
* https://github.com/kubernetes/enhancements/issues/88

## Deployment

### Kubernetes

Prebuilt containers are posted on [Docker Hub](https://hub.docker.com/r/apache/cloudstack-kubernetes-provider).

To configure API access to your CloudStack management server, you need to create a secret containing a `cloud-config`
that is suitable for your environment.

`cloud-config` should look like this:
```ini
[Global]
api-url = <CloudStack API URL>
api-key = <CloudStack API Key>
secret-key = <CloudStack API Secret>
project-id = <CloudStack Project UUID (optional)>
zone = <CloudStack Zone Name (optional)>
ssl-no-verify = <Disable SSL certificate validation: true or false (optional)>
```

The access token needs to be able to fetch VM information and deploy load balancers in the project or domain where the nodes reside.

To create the secret, use the following command:
```bash
kubectl -n kube-system create secret generic cloudstack-secret --from-file=cloud-config
```

You can then use the provided example [deployment.yaml](/deployment.yaml) to deploy the controller:
```bash
kubectl apply -f deployment.yaml
```

### Protocols

This CCM supports TCP, UDP and [TCP-Proxy](https://www.haproxy.org/download/1.8/doc/proxy-protocol.txt) LoadBalancer deployments.

For UDP and Proxy Protocol support, CloudStack 4.6 or later is required.

Since kube-proxy does not support the Proxy Protocol or UDP, you should connect this directly to pods, for example by deploying a DaemonSet and setting `hostPort: <TCP port>` on the desired container port.
Important: The service running in the pod must support the chosen protocol. Do not try to enable TCP-Proxy when the service only supports regular TCP.

[traefik-ingress-controller.yml](/traefik-ingress-controller.yml) contains a basic deployment for the Træfik ingress controller that illustrates how to use it with the proxy protocol.

For the nginx ingress controller, please refer to the official documentation at [kubernetes.github.io/ingress-nginx/deploy](https://kubernetes.github.io/ingress-nginx/deploy/). After applying the deployment, patch it for proxy protocol support with the provided fragment:

```bash
kubectl apply -f nginx-ingress-controller-patch.yml
```

### Node Labels

:warning: **The node name must match the host name, so the controller can fetch and assign metadata from CloudStack.**

It is recommended to launch `kubelet` with the following parameter:

```
--register-with-taints=node.cloudprovider.kubernetes.io/uninitialized=true:NoSchedule
```

This will treat the node as 'uninitialized' and cause the CCM to apply metadata labels from CloudStack automatically.

Supported labels for Kubernetes versions up to 1.16 are:
* kubernetes.io/hostname (= the instance name)
* beta.kubernetes.io/instance-type (= the compute offering)
* failure-domain.beta.kubernetes.io/zone (= the zone)
* failure-domain.beta.kubernetes.io/region (also = the zone)

Supported labels for Kubernetes versions 1.17 and later are:
* kubernetes.io/hostname (= the instance name)
* node.kubernetes.io/instance-type (= the compute offering)
* topology.kubernetes.io/zone (= the zone)
* topology.kubernetes.io/region (also = the zone)

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

At least Go 1.13 is required to build cloudstack-ccm.

To build the controller with correct versioning, some build flags need to be passed.
A Makefile is provided that sets these build flags to automatically derived values.

```bash
go get github.com/apache/cloudstack-kubernetes-provider
cd ${GOPATH}/src/github.com/apache/cloudstack-kubernetes-provider
make
```

To build the cloudstack-cloud-controller-manager container, please use the provided Dockerfile.
The Makefile will also with that and properly tag the resulting container.

```bash
make docker
```

### Testing

You need a local instance of the CloudStack Management Server or a 'real' one to connect to.
The CCM supports the same cloud-config configuration file format used by [the cs tool](https://github.com/exoscale/cs),
so you can simply point it to that.

```bash
./cloudstack-ccm --cloud-provider external-cloudstack --cloud-config ~/.cloud-config --master k8s-apiserver
```

Replace k8s-apiserver with the host name of your Kubernetes development clusters's API server.

If you don't have a 'real' CloudStack installation, you can also launch a local [simulator instance](https://hub.docker.com/r/cloudstack/simulator) instead. This is very useful for dry-run testing.

## Copyright

Copyright 2019 The Apache Software Foundation

This product includes software developed at
The Apache Software Foundation (http://www.apache.org/).
