# CloudStack Kubernetes Provider

[![](https://img.shields.io/github/release/apache/cloudstack-kubernetes-provider.svg?logo=github&style=flat-square "Release")](https://github.com/apache/cloudstack-kubernetes-provider/releases)
[![](https://img.shields.io/badge/license-Apache%202.0-blue.svg?color=%23282661&logo=apache&style=flat-square "Apache 2.0 license")](/LICENSE-2.0)
[![](https://img.shields.io/badge/language-Go-%235adaff.svg?logo=go&style=flat-square "Go language")](https://golang.org)
[![](https://img.shields.io/docker/v/apache/cloudstack-kubernetes-provider?label=docker%20hub&logo=docker&style=flat-square "Docker Hub Image Version")](https://hub.docker.com/r/apache/cloudstack-kubernetes-provider/)

A Cloud Controller Manager to facilitate Kubernetes deployments on CloudStack.

It replaces the CloudStack cloud provider that used to be built into Kubernetes and has since
been removed from the Kubernetes tree.

Refer:
* https://github.com/kubernetes/kubernetes/tree/release-1.15/pkg/cloudprovider/providers/cloudstack
* https://github.com/kubernetes/enhancements/issues/672
* https://github.com/kubernetes/enhancements/issues/88

## Deployment

The CloudStack Kubernetes Provider is automatically deployed when a Kubernetes Cluster is created on CloudStack 4.16+

In order to communicate with CloudStack, a separate service user **kubeadmin** is created in the same account as the cluster owner.
The provider uses this user's API keys to get the details of the cluster as well as update the networking rules. It is imperative that this user
is not altered or have its keys regenerated.

The provider can also be manually deployed as follows :

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
region = <Region Name (optional)>
ssl-no-verify = <Disable SSL certificate validation: true or false (optional)>
version = <CloudStack version, e.g. 4.21.0.0 (optional)>
```

If `zone` is not set, it is auto-detected from the node the controller runs on.

`region` sets the value of the region node labels. If it is not set, the region labels use the zone
name. Some workloads (such as Rook/Ceph) require the zone and region labels to differ. You need to
explicitly set `region` in that case.

`version` is normally detected automatically using the `listCapabilities` API. Set it to pin the version manually,
for example when the API user is not allowed to call `listCapabilities`.

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

### Service Annotations

The CloudStack Kubernetes Provider supports several annotations on LoadBalancer services to customize load balancer behavior:

#### `service.beta.kubernetes.io/cloudstack-load-balancer-proxy-protocol`

**Type:** Boolean (`"true"` or `"false"`)

**Default:** `false`

**Description:** Enables the [HAProxy Proxy Protocol](https://www.haproxy.org/download/1.8/doc/proxy-protocol.txt) on a CloudStack load balancer. This annotation only applies to TCP service ports and requires CloudStack 4.6 or later.

**Use Case:** Use this annotation when you need to preserve the original client IP address through the load balancer. This is commonly required for ingress controllers like Traefik or Nginx that need to know the client's real IP address.

**Example:**
```yaml
apiVersion: v1
kind: Service
metadata:
  name: my-service
  annotations:
    service.beta.kubernetes.io/cloudstack-load-balancer-proxy-protocol: "true"
spec:
  type: LoadBalancer
  ports:
    - port: 80
      protocol: TCP
```

#### `service.beta.kubernetes.io/cloudstack-load-balancer-hostname`

**Type:** String

**Default:** Not set (uses IP address)

**Description:** Sets a hostname for the load balancer ingress instead of using the IP address. This is a workaround for [Kubernetes issue #66607](https://github.com/kubernetes/kubernetes/issues/66607).

**Use Case:** Use this annotation when you need the LoadBalancer status to return a hostname instead of an IP address. This is useful for DNS-based routing or when you want to expose a specific hostname.

**Example:**
```yaml
apiVersion: v1
kind: Service
metadata:
  name: my-service
  annotations:
    service.beta.kubernetes.io/cloudstack-load-balancer-hostname: "lb.example.com"
spec:
  type: LoadBalancer
```


#### `service.beta.kubernetes.io/cloudstack-load-balancer-source-cidrs`

**Type:** String (comma-separated CIDR list)

**Default:** `"0.0.0.0/0"` (allows all sources)

**Description:** Sets the source CIDR list on the CloudStack **load balancer rule**, restricting the source addresses that the load balancer rule accepts traffic from.

This annotation restricts traffic at the load balancer rule only. It does **not** configure the
firewall: the firewall rule created alongside the load balancer rule comes from
`spec.loadBalancerSourceRanges`, which defaults to `0.0.0.0/0`. So if you set only this annotation,
disallowed sources are still turned away, but by the load balancer instead of being blocked at the
firewall — see [Restricting Source Traffic](#restricting-source-traffic).

**Use Case:** Use this annotation, together with `spec.loadBalancerSourceRanges`, to restrict access to
your load balancer to specific IP ranges. This is particularly useful for internal services or when
you want to limit access to specific networks.

**Example:**
```yaml
apiVersion: v1
kind: Service
metadata:
  name: my-service
  annotations:
    service.beta.kubernetes.io/cloudstack-load-balancer-source-cidrs: "10.0.0.0/8,192.168.1.0/24"
spec:
  type: LoadBalancer
```

**Format:** Comma-separated list of CIDR ranges. Spaces around commas are automatically trimmed.
Every entry must parse as a valid CIDR, otherwise the service fails to sync with an `invalid CIDR` error.

**CloudStack Version:** Creating a rule with a CIDR list works on all supported versions.
*Changing* the CIDR list of an existing rule can only be done in place on CloudStack 4.22 or later.
On earlier versions the controller deletes the load balancer rule and recreates it with the new CIDR
list, which briefly interrupts traffic on that port.

**Note:** If the annotation is not set, the load balancer rule allows all sources (`0.0.0.0/0`).
Setting it to an empty value (`""`) sends an empty CIDR list to CloudStack — it does not block all
traffic.

#### `service.beta.kubernetes.io/cloudstack-load-balancer-ip-associated-by-controller`

**Type:** Boolean (`"true"` or `"false"`)

**Default:** Not set

**Description:** Set by the controller, not by you. When the controller associates a public IP that
was not already allocated, it records that fact on the service with this annotation. On deletion the
annotation determines whether the IP is disassociated again: an IP the controller allocated is
released, an IP that was already allocated before the service existed is left in place.

The controller also checks for other load balancer rules on the same IP before releasing it, so an
IP shared by several services is not disassociated while still in use. Do not set or remove this
annotation by hand — doing so can leak a public IP or release one that you allocated yourself.

### Restricting Source Traffic

Traffic is filtered at two independent layers, which are configured separately. The second layer is
either a firewall rule or a Network ACL rule, depending on what the network offers:

| Layer | Configured by | Default |
| --- | --- | --- |
| CloudStack load balancer rule | `service.beta.kubernetes.io/cloudstack-load-balancer-source-cidrs` annotation | `0.0.0.0/0` |
| Firewall rule — isolated networks, and VPC networks that offer the Firewall service | `spec.loadBalancerSourceRanges` | `0.0.0.0/0` |
| Network ACL rule — VPC networks without the Firewall service | Not configurable | `0.0.0.0/0` |

Traffic has to be allowed by both layers, so where firewall rules are used, either setting alone is
enough to block unwanted sources. Setting both keeps the two rules consistent in CloudStack.
Where Network ACL rules are used, `spec.loadBalancerSourceRanges` has no effect and the annotation
is the only way to restrict sources.

The two layers turn traffic away differently. The firewall discards the packets on the virtual
router, so a blocked client simply times out. The load balancer rule lets the connection be
established first and then closes it, so a blocked client can still tell that the port is open.
Use `spec.loadBalancerSourceRanges` if you would rather not expose that.

To restrict access at both layers, set both:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: my-service
  annotations:
    service.beta.kubernetes.io/cloudstack-load-balancer-source-cidrs: "10.0.0.0/8"
spec:
  type: LoadBalancer
  loadBalancerSourceRanges:
    - 10.0.0.0/8
  ports:
    - port: 80
      protocol: TCP
```

The controller never opens the firewall implicitly; it always creates explicit firewall rules for
the ports it manages, and it removes firewall rules whose CIDR list no longer matches.

### Assigning a Specific IP Address

Set `spec.loadBalancerIP` to pin the load balancer to a known public IP:

```yaml
spec:
  type: LoadBalancer
  loadBalancerIP: 10.1.1.218
```

The address must be an existing public IP address visible to the configured account, otherwise the
service fails to sync with `could not find IP address`. It does not need to be associated with the
network beforehand: if the address is free, the controller associates it (with the VPC instead of
the network if the network belongs to a VPC).

When the service is deleted, the IP is released only if the controller associated it — see
[`...-ip-associated-by-controller`](#servicebetakubernetesiocloudstack-load-balancer-ip-associated-by-controller)
above.

### Session Affinity

The load balancer algorithm is derived from the service's `spec.sessionAffinity`; there is no
annotation for it.

| `spec.sessionAffinity` | CloudStack algorithm |
| --- | --- |
| `None` (default) | `roundrobin` |
| `ClientIP` | `source` |

Any other value makes the service fail to sync with `unsupported load balancer affinity`. Other
CloudStack algorithms, such as `leastconn`, cannot currently be selected.

### VPC Networks

VPC networks are supported. VPC networks normally do not offer the Firewall service, so the
controller creates **Network ACL** rules instead of firewall rules for the managed ports, and
associates public IPs with the VPC rather than with the network.

CloudStack 4.23 adds support for firewall rules on public IPs in VPC networks. This requires a
network offering that includes the Firewall service and is not enabled by default. The controller
chooses the mechanism based on the services the network offers, so on such networks it manages
firewall rules instead of Network ACL rules, and `spec.loadBalancerSourceRanges` is applied to
them.

Two things to be aware of when the controller manages Network ACL rules:
* The ACL rules the controller creates always allow `0.0.0.0/0`; `spec.loadBalancerSourceRanges` is
  not applied. Use the `cloudstack-load-balancer-source-cidrs` annotation to restrict sources.
* If the network uses one of the default ACL lists (`default_allow` or `default_deny`), the
  controller does not add ACL rules to it. Use a custom ACL list if you want the controller to
  manage the rules.

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
* failure-domain.beta.kubernetes.io/region (= region from config if defined, otherwise the zone)

Supported labels for Kubernetes versions 1.17 and later are:
* kubernetes.io/hostname (= the instance name)
* node.kubernetes.io/instance-type (= the compute offering)
* topology.kubernetes.io/zone (= the zone)
* topology.kubernetes.io/region (= region from config if defined, otherwise the zone)

It is also possible to trigger this process manually by issuing the following command:

```
kubectl taint nodes <my-node-without-labels> node.cloudprovider.kubernetes.io/uninitialized=true:NoSchedule
```

Along with the labels, initialization also sets the node's provider ID, in the form
`external-cloudstack://<instance UUID>`.

## FAQ

### How do I stop the controller from managing my LoadBalancer services?

Some clusters run on CloudStack but use a different load balancer implementation, and only want the
node and node lifecycle controllers. There are two ways to do this.

**Per service**, set `spec.loadBalancerClass` to the class handled by your own implementation. The
upstream service controller ignores any service that has a load balancer class set, so this
controller never sees it:

```yaml
spec:
  type: LoadBalancer
  loadBalancerClass: example.com/my-own-lb
```

:warning: `spec.loadBalancerClass` is immutable. Existing services have to be deleted and recreated
to adopt it, so this is best suited to a cluster you are still building out.

**For the whole cluster**, drop the service controller from the controller manager:

```yaml
args:
- --leader-elect=true
- --cloud-provider=external-cloudstack
- --cloud-config=/config/cloud-config
- --controllers=*,-service
```

The `*` is required: `--controllers` replaces the default list instead of adding to it, so
`--controllers=-service` on its own disables *every* controller. With `--controllers=*,-service`
the log should show only `"service" is disabled` on startup.

### Does this provide persistent volumes?

No. This controller manages nodes and load balancers only. For volumes, use a CloudStack CSI driver.

### Does it support Cluster API?

Not directly. Cluster API support for CloudStack lives in
[cluster-api-provider-cloudstack](https://github.com/kubernetes-sigs/cluster-api-provider-cloudstack).

## Troubleshooting

### Services stay in `<pending>`, log shows `could not find network`

The controller works out which network to create the rules in from the nodes: it matches the node
names against the CloudStack instance names and takes the network of the instance's first NIC. This
error means that network could not be read back with the configured credentials. Check that
`project-id` in the `cloud-config` matches the project the nodes are in, and that the account owning
the API key can see that network.

### Services stay in `<pending>`, log shows `There are no available nodes for LoadBalancer`

No node passed the service controller's filter. A node is skipped if it is not `Ready`, if it
carries the `node.kubernetes.io/exclude-from-external-load-balancers` label, or if the cluster
autoscaler has marked it for deletion.

### `exec /app/cloudstack-ccm: exec format error`

The image architecture does not match the node. Releases up to v1.1.0 were published for amd64 only;
use a newer release, which ships multi-architecture images including arm64.

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

At least Go 1.23 is required to build cloudstack-ccm.

To build the controller with correct versioning, some build flags need to be passed.
A Makefile is provided that sets these build flags to automatically derived values.

```bash
go get github.com/apache/cloudstack-kubernetes-provider
cd ${GOPATH}/src/github.com/apache/cloudstack-kubernetes-provider
make
```

To build the cloudstack-cloud-controller-manager container, please use the provided Dockerfile.
The Makefile will also do that and properly tag the resulting container.

```bash
make docker
```

### Testing

You need a local instance of the CloudStack Management Server or a 'real' one to connect to.
The CCM supports the same cloud-config configuration file format used by [the cs tool](https://github.com/exoscale/cs),
so you can simply point it to that.

```bash
./cloudstack-ccm --cloud-provider external-cloudstack --cloud-config ./cloud-config --kubeconfig ~/.kube/config
```

Point `--kubeconfig` at a kubeconfig for your Kubernetes development cluster, and `--cloud-config` at
a `cloud-config` for the CloudStack installation you want to talk to.

If you don't have a 'real' CloudStack installation, you can also launch a local [simulator instance](https://hub.docker.com/r/cloudstack/simulator) instead. This is very useful for dry-run testing.

### Debugging

You can use the VSCode extension [Go](https://marketplace.visualstudio.com/items?itemName=golang.go) to debug the CCM.
Add the following configuration to the `.vscode/launch.json` file to launch the CCM and debug it.

```json
{
    "version": "0.2.0",
    "configurations": [
        {
            "name": "Launch CloudStack CCM",
            "type": "go",
            "request": "launch",
            "mode": "auto",
            "program": "${workspaceFolder}/cmd/cloudstack-ccm",
            "env": {},
            "args": [
                "--cloud-provider=external-cloudstack",
                "--cloud-config=${workspaceFolder}/cloud-config",
                "--kubeconfig=${env:HOME}/.kube/config",
                "--leader-elect=false",
                "--v=4"
            ],
            "showLog": true,
            "trace": "verbose"
        },
        {
            "name": "Attach to Process",
            "type": "go",
            "request": "attach",
            "mode": "local",
            "processId": 0
        }
    ]
}
```

## Copyright

Copyright 2019 The Apache Software Foundation

This product includes software developed at
The Apache Software Foundation (http://www.apache.org/).
