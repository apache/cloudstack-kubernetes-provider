<!--
  Licensed to the Apache Software Foundation (ASF) under one
  or more contributor license agreements.  See the NOTICE file
  distributed with this work for additional information
  regarding copyright ownership.  The ASF licenses this file
  to you under the Apache License, Version 2.0 (the
  "License"); you may not use this file except in compliance
  with the License.  You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

  Unless required by applicable law or agreed to in writing,
  software distributed under the License is distributed on an
  "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
  KIND, either express or implied.  See the License for the
  specific language governing permissions and limitations
  under the License.
-->

# Apache CloudStack Kubernetes Provider Security Threat Model — delta (draft)

> **Delta document.** Inherits §3, §4 B1, and §7 from
> `cloudstack-threat-model-draft.md`. Read the main model first.

## §1 Header

- **Project:** Apache CloudStack Kubernetes Provider
  (`apache/cloudstack-kubernetes-provider`) — Kubernetes Cloud
  Controller Manager (CCM) for CloudStack-managed clusters.
- **Commit:** `4740dbc` (HEAD of `main` at draft time).
- **Date:** 2026-05-29.
- **Authors:** ASF Security team draft.
- **Status:** Draft delta over `cloudstack-threat-model-draft.md`.
- **Version binding:** as of the commit above.
- **Reporting:** as in the main model.
- **Provenance legend:** as in the main model.
- **Draft confidence:** 11 documented / 0 maintainer / 12 inferred.

**About the project.** A Kubernetes Cloud Controller Manager (CCM)
written in Go that lets a Kubernetes cluster running on CloudStack
discover its node instances, sync labels, and provision load
balancers via the CloudStack API *(documented: `README.md`)*. Replaces
the old in-tree `kubernetes/kubernetes` CloudStack provider that was
removed *(documented: `README.md` — references to
`kubernetes/enhancements` issues #672 and #88)*. Deployed automatically
when CloudStack 4.16+ creates a Kubernetes cluster; can also be
deployed manually. Runs as a Pod in the `kube-system` namespace.

**Crucially**, *(documented: `README.md` "Deployment")*: a separate
service user **`kubeadmin`** is created in the same CloudStack account
as the cluster owner, and the CCM uses **that user's API keys** to
talk to CloudStack. The CCM holds long-lived CloudStack credentials in
a Kubernetes Secret.

## §2 Scope and intended use

**Primary intended use.** Run as the Kubernetes Cloud Controller
Manager for a Kubernetes cluster whose nodes are CloudStack-managed
VMs. Discovers node identity, propagates zone / region labels,
and provisions CloudStack load balancers in response to `Service
type=LoadBalancer` declarations *(documented: `cloudstack.go`,
`cloudstack_loadbalancer.go`, `cloudstack_instances.go`)*.

**Deployment shape.** A long-running Pod inside `kube-system`. Has
network access to (a) the Kubernetes API server (in-cluster
ServiceAccount auth), and (b) the CloudStack management server
(`api-url` from the mounted `cloud-config` Secret).

**Caller expectations.** The Kubernetes cluster operator is trusted
to:

- create the `cloudstack-secret` containing `cloud-config` with
  `api-url`, `api-key`, `secret-key`, optional `project-id`, `zone`,
  `region`, and **`ssl-no-verify`** *(documented: `README.md`)*,
- not regenerate the `kubeadmin` user's API keys after deployment
  (the CCM relies on a stable identity) *(documented: `README.md`)*,
- restrict access to the `cloudstack-secret` per Kubernetes RBAC.

**Component-family table.**

| Family | Representative entry | Touches outside the process? | In this delta? |
| --- | --- | --- | --- |
| Provider config + CloudStack client (`cloudstack.go` `CSConfig`, `newCSCloud`) | reads `cloud-config` Secret, builds `cloudstack-go` client | **yes — network + creds** | yes |
| Instance discovery (`cloudstack_instances.go`) | maps node VM IDs to CloudStack VMs | inherited | yes |
| Load-balancer reconciler (`cloudstack_loadbalancer.go`) | provisions CloudStack LB rules in response to K8s Service changes | inherited | yes |
| Protocol helpers (`protocol.go`) | TCP / UDP / TCP-Proxy LoadBalancer support *(documented: `README.md`)* | n/a | yes |
| `cmd/cloudstack-ccm` | CCM entry point | binds in-cluster ServiceAccount | yes |
| `deployment.yaml`, `nginx-ingress-controller-patch.yml`, Dockerfile | packaging | n/a at runtime | yes — these define the *deployment posture* |
| Tests `*_test.go` | unit / integration tests | n/a | **out of model** *(§3)* |
| `get_kubernetes_deps.sh` | dev script | n/a | **out of model** *(§3)* |

## §3 Out of scope (explicit non-goals)

The main model's §3 applies. **Additional** out-of-scope items
specific to the K8s provider:

1. **Kubernetes Secret confidentiality.** The CCM expects `cloud-config`
   to be in a Kubernetes Secret. Whether that Secret is encrypted at
   rest (etcd encryption, KMS provider), bound to a specific Pod, or
   exposed via in-cluster reads is the Kubernetes cluster's own
   responsibility. *(inferred — Q1)*
2. **Server-side correctness of CloudStack responses.** Same as
   sibling deltas.
3. **TLS verification when `ssl-no-verify = true`.** When the cluster
   operator sets `ssl-no-verify = true` in `cloud-config`, the CCM
   passes `verifyssl=false` to `cloudstack-go` (via the equivalent
   constructor). That is operator choice.
4. **In-cluster RBAC for the CCM's ServiceAccount.** What Kubernetes
   API permissions the CCM holds via its ServiceAccount and Role
   bindings is the cluster operator's job.
5. **Kubernetes itself.** Bugs in `kube-apiserver`, `kubelet`, etcd,
   the CRI runtime are upstream.
6. **The `kubeadmin` user creation flow on the CloudStack side.** The
   `kubeadmin` user is created by CloudStack's `cloudstack-management`
   when the cluster is provisioned; that flow is in the main model.
   This delta covers only the CCM's *consumption* of those credentials.
7. **The four sibling repos.**

## §4 Trust boundaries and data flow

The CCM stitches together two main-model trust transitions:

- **K1: K8s controller-manager → Kubernetes API server.** In-cluster
  ServiceAccount token, mounted into the Pod. Authn / authz are
  Kubernetes-side.
- **K2: CCM → CloudStack management server.** Main-model B1 — HMAC-
  SHA1 signed JSON API calls. Credentials come from the
  `cloudstack-secret`'s `cloud-config`.

The boundary is **the Kubernetes Pod sandbox** plus the mounted
Secret. Bytes inside the Pod that come from either the K8s API server
or the CloudStack management server are trusted control-plane content.

**`ssl-no-verify` behaviour** *(documented: `cloudstack.go` `CSConfig`
gcfg tag `ssl-no-verify`)*: when `true`, TLS verification of the
CloudStack management-server cert is disabled.

## §5 Assumptions about the environment

- **Host**: Kubernetes 1.16+ (the in-tree provider was removed in
  1.15) *(documented: `README.md`)*.
- **Container**: `apache/cloudstack-kubernetes-provider` published on
  Docker Hub *(documented: `README.md`)*.
- **Filesystem**: reads `cloud-config` from a mounted Secret;
  otherwise no host filesystem writes *(inferred — Q2)*.
- **Network**: outbound HTTPS to `api-url` (CloudStack) and in-cluster
  HTTPS to `kube-apiserver`.
- **Auth**: stdlib TLS, opt-out via `ssl-no-verify=true`; in-cluster
  K8s auth via projected ServiceAccount token.
- **What the CCM does not do**: no host-network listener, no privileged
  Pod requirement *(inferred — Q3)*, no write to the host filesystem.

## §5a Build-time and configuration variants

| Knob | Default | Stance | Effect |
| --- | --- | --- | --- |
| `api-url` | none | operator config | endpoint |
| `api-key`, `secret-key` | none — provisioned automatically as `kubeadmin` | operator must not regenerate after deployment *(documented: `README.md`)* | identity |
| `ssl-no-verify` | `false` *(inferred — Q4)* | dev / self-signed-CA | flips TLS verification off |
| `project-id`, `zone`, `region` | unset | optional scoping | constrains LB / instance discovery to a project |
| LoadBalancer protocols | TCP, UDP, TCP-Proxy *(documented: `README.md`)* | supported set | not security-relevant |

## §6 Assumptions about inputs

| Entry point | Parameter | Attacker-controllable in the model? | Caller must enforce |
| --- | --- | --- | --- |
| `cloud-config` Secret | `api-url`, `api-key`, `secret-key`, `ssl-no-verify`, etc. | **no** — Kubernetes cluster operator config | restrict Secret read RBAC; consider etcd-at-rest encryption |
| Kubernetes Service / Node / Endpoint events | watch payloads | **trusted from kube-apiserver** | bytes are control-plane |
| CloudStack JSON responses | typed-decoded | trusted (B1) | bytes are control-plane |

## §7 Adversary model

Main-model §7 applies. **Adjustments specific to the K8s provider**:

- "Unauthenticated network peer reaching `:8080`" is upstream.
- An additional adversary worth naming: **any Kubernetes principal
  with `get`/`list` on Secrets in `kube-system`**. Such a principal
  recovers the CloudStack `kubeadmin` user's API key and secret in
  plaintext and can drive the CloudStack API as `kubeadmin` (with
  whatever CloudStack-side role that user holds). This is in scope:
  the CCM cannot prevent in-cluster Secret read, but the documented
  *(README)* deployment recommendation that `kubeadmin` keys must not
  be regenerated means a leaked `cloud-config` cannot be invalidated
  by rotation without breaking the CCM. *(inferred — Q5 — high-
  priority.)*
- **A passive observer on the in-cluster network** between the CCM
  Pod and the CloudStack management server when `ssl-no-verify=true`
  is the same shape as in the Terraform-provider delta: TLS verify is
  off, MitM is possible undetected.

## §8 Security properties the CCM provides

### K1 — HMAC-SHA1 signature via `cloudstack-go`

- As main-model §8 P1.

### K2 — In-cluster ServiceAccount-bound CCM identity

- **Property.** The CCM authenticates to the Kubernetes API server as
  its own ServiceAccount; no shared secret is sent to kube-apiserver.
- **Conditions.** Pod is deployed per `deployment.yaml` with a
  ServiceAccount.
- **Violation symptom.** CCM speaks to kube-apiserver as a different
  identity.
- **Severity.** Security-critical, `VALID`.

### K3 — TLS verification toggle (`ssl-no-verify`)

- As Go-SDK delta §8 S2 / CloudMonkey delta §8 C2.

## §9 Security properties the CCM does *not* provide

- **No protection of the `cloud-config` Secret.** A K8s principal with
  Secret read in `kube-system` recovers CloudStack credentials.
- **No key rotation.** The README *requires* operators **not** to
  rotate `kubeadmin` API keys after deployment — meaning a compromise
  cannot be remediated by rotation without breaking the CCM. *(See
  §14 Q6 for the proposed-rotation question.)*
- **No defence when `ssl-no-verify = true`.**
- **No least-privilege constraint** on the `kubeadmin` CloudStack user's
  scope beyond what CloudStack's RBAC + project / zone scoping gives.
  *(inferred — Q7)*
- **No defence against an attacker with read access to the CCM Pod's
  in-cluster network namespace** (e.g. a sidecar in the same Pod).
  *(inferred — Q8)*

### False-friend properties

- **`ssl-no-verify` is not "test mode."** Same wording as the
  Terraform delta.
- **A Kubernetes Secret is not a hardened secret store** — it is
  base64-encoded by default in etcd, encrypted only when etcd
  encryption is enabled.
- **HMAC-SHA1** — see main model §11a.

## §10 Downstream responsibilities

The Kubernetes cluster operator MUST:

1. Enable etcd at-rest encryption (or KMS-backed Secret encryption)
   for the `cloudstack-secret`.
2. Restrict Kubernetes RBAC so that only the CCM ServiceAccount can
   read `cloudstack-secret`.
3. Set `ssl-no-verify = false` (the recommended posture) unless the
   CloudStack management server is unreachable over TLS in the
   cluster's network.
4. Scope the CloudStack-side `kubeadmin` user to the smallest
   CloudStack role that can list VMs and create/update load
   balancers — not a root admin or domain admin.
5. Treat the CCM Pod as a credential-bearing workload: no co-tenant
   sidecars, no shared PID namespace with untrusted workloads.
6. Match CCM container image to the CloudStack API contract (the
   image embeds a specific `cloudstack-go` version).
7. Monitor CloudStack audit logs for `kubeadmin`-account anomalies.

## §11 Known misuse patterns

- Storing `cloud-config` in a Kubernetes Secret without etcd
  encryption.
- Granting `secret get` on `kube-system` to non-admin Kubernetes
  principals.
- Running the CCM with a `kubeadmin` user that is a root admin or
  domain admin of the CloudStack account (over-privileged).
- Setting `ssl-no-verify = true` in production.
- Sharing the `cloud-config` across clusters of different trust
  levels — a compromise of one cluster's Secret compromises the
  others.
- Regenerating the `kubeadmin` API key — breaks the CCM until the
  `cloud-config` Secret is updated *(documented: `README.md`)*.

## §11a Known non-findings (recurring false positives)

- **"Secret contains plaintext `api-key` / `secret-key`."** That is
  the documented deployment shape. → `BY-DESIGN: property-disclaimed`.
- **"Pod has access to a long-lived credential."** Same. → `BY-DESIGN:
  property-disclaimed`.
- **"HMAC-SHA1 — SHA1 is deprecated."** → `KNOWN-NON-FINDING` per
  main model.
- **"`InsecureSkipVerify` is reachable from `ssl-no-verify`."**
  Operator choice. → `OUT-OF-MODEL: trusted-input`.
- **"`kubeadmin` user has broad CloudStack privileges."** Depends on
  operator's CloudStack RBAC posture per §10 item 4. → `OUT-OF-MODEL:
  trusted-input` against the operator's CloudStack config.
- **"Tests in `*_test.go` have weak input handling."** Out of model.
  → `OUT-OF-MODEL: unsupported-component`.

## §12 Conditions that would change this delta

- A move from static `cloud-config` Secret to a secret-manager
  pattern (External Secrets Operator, CSI Secret Driver, projected
  short-lived token from the CloudStack side).
- Support for short-lived / rotating credentials at the CloudStack
  side (would change the §9 "no key rotation" bullet).
- Addition of a CCM-side TLS-verify default that overrides
  `ssl-no-verify=false` to `true`.
- Change in signing algorithm at the main-model layer.

## §13 Triage dispositions

Use the same table as the main model.

## §14 Open questions for the maintainers

**Q1.** Out-of-scope: Kubernetes-side Secret confidentiality (etcd
encryption, KMS, RBAC). Confirm. *(maps to §3, §9)*

**Q2.** Confirm the CCM has no host-filesystem writes and no
ephemeral credential cache that survives Pod restart.

**Q3.** Does the upstream `deployment.yaml` require any host
privileges (host network, host PID, privileged container)? Proposed:
**no**. Confirm.

**Q4.** `ssl-no-verify` default — proposed: **`false`**. Confirm.
*(maps to §5a, §10)*

**Q5.** What is the recommended Kubernetes Secret protection posture
for `cloudstack-secret`? Proposed: etcd-at-rest encryption + RBAC
restricting `get`/`list` on Secrets in `kube-system` to cluster
admins + the CCM ServiceAccount only. *(maps to §3, §10)*

**Q6.** **Highest-leverage question in this delta.** The README says
*"It is imperative that this user is not altered or have its keys
regenerated."* — meaning credential rotation is not supported.

- Is rotation actually impossible, or is it documented-not-supported
  because the operator-side workflow is non-trivial?
- Is there a path to rotate the `kubeadmin` API key with controlled
  CCM downtime?
- Should the threat model state explicitly that the CCM's
  CloudStack credential is *non-rotatable* and treat any leak as
  requiring redeployment of the cluster's CloudStack account?
  *(maps to §9, §10, §11)*

**Q7.** What CloudStack-side RBAC scope is `kubeadmin` *expected* to
have? Proposed: the smallest set of API commands that covers `listVMs`
+ LoadBalancer rule CRUD. Is there a published recommended role?
*(maps to §9, §10)*

**Q8.** What is the CCM's posture against a malicious sidecar in the
same Pod, or against a malicious DaemonSet sharing the same node? Is
that out of scope (delegated to Pod isolation / Pod Security
Standards)? Proposed: out of scope. *(maps to §7, §9)*

**Q9.** Meta — should this delta live at `docs/threat-model.md` in
`apache/cloudstack-kubernetes-provider`, or in the website tree?

**Q10.** When the main model's signing algorithm changes, what is the
release-cadence commitment for an updated CCM image?

**Q11.** Confirm the unsupported-component list (tests, dev scripts,
old in-tree references in the README).

**Q12.** TCP-Proxy LoadBalancer support pulls in HAProxy PROXY
protocol *(documented: `README.md`)*. Is the CCM responsible for any
authentication of the PROXY-protocol header, or is that downstream of
the actual workload Pod? Proposed: downstream of the workload Pod.
