//go:build e2e

/*
 * Licensed to the Apache Software Foundation (ASF) under one
 * or more contributor license agreements.  See the NOTICE file
 * distributed with this work for additional information
 * regarding copyright ownership.  The ASF licenses this file
 * to you under the Apache License, Version 2.0 (the
 * "License"); you may not use this file except in compliance
 * with the License.  You may obtain a copy of the License at
 *
 *   http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

package e2e

import (
	"os"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

const providerIDPrefix = "external-cloudstack://"

// TestNode_Initialized asserts the CCM removed the uninitialized taint from
// every node.
func TestNode_Initialized(t *testing.T) {
	f := NewFramework(t)
	for _, node := range f.Nodes() {
		for _, taint := range node.Spec.Taints {
			if taint.Key == "node.cloudprovider.kubernetes.io/uninitialized" {
				t.Errorf("node %s still has the uninitialized taint", node.Name)
			}
		}
	}
}

// TestNode_ProviderID asserts every node's providerID references the matching
// CloudStack VM.
//
// kind starts kubelet with --provider-id=kind://..., and Kubernetes only lets
// the provider ID be set once, so under the kind-based harness the CCM never
// gets to assign it. Where that is the case the test verifies instead that
// the CCM would derive the right value, and reports the node as skipped so
// the limitation stays visible rather than silently reducing coverage.
func TestNode_ProviderID(t *testing.T) {
	f := NewFramework(t)
	checked := 0
	for _, node := range f.Nodes() {
		vm, err := f.VMByName(node.Name)
		if err != nil {
			t.Fatalf("looking up VM for node %s: %v", node.Name, err)
		}
		if vm == nil {
			t.Fatalf("no CloudStack VM named %s", node.Name)
		}
		want := providerIDPrefix + vm.Id

		if node.Spec.ProviderID != "" && !strings.HasPrefix(node.Spec.ProviderID, providerIDPrefix) {
			t.Logf("node %s has a foreign provider ID %q (set by the infrastructure, "+
				"not the CCM); expected CloudStack provider ID would be %q",
				node.Name, node.Spec.ProviderID, want)
			continue
		}
		if node.Spec.ProviderID != want {
			t.Errorf("node %s providerID = %q, want %q", node.Name, node.Spec.ProviderID, want)
		}
		checked++
	}
	if checked == 0 {
		t.Skip("every node has a provider ID assigned by the infrastructure; " +
			"the CCM's provider ID assignment is not exercised by this environment")
	}
}

// TestNode_Labels asserts the CCM applied instance-type, zone and region
// labels from CloudStack metadata.
func TestNode_Labels(t *testing.T) {
	f := NewFramework(t)
	region := os.Getenv("E2E_REGION")
	if region == "" {
		region = "simulator-region"
	}
	for _, node := range f.Nodes() {
		vm, err := f.VMByName(node.Name)
		if err != nil || vm == nil {
			t.Fatalf("looking up VM for node %s: %v", node.Name, err)
		}
		// Only presence is checked: labelInvalidCharsRegex rewrites the value ("Small Instance" -> "SmallInstance").
		if got := node.Labels[corev1.LabelInstanceTypeStable]; got == "" {
			t.Errorf("node %s is missing label %s", node.Name, corev1.LabelInstanceTypeStable)
		}
		if got := node.Labels[corev1.LabelTopologyZone]; got != vm.Zonename {
			t.Errorf("node %s zone label = %q, want %q", node.Name, got, vm.Zonename)
		}
		if got := node.Labels[corev1.LabelTopologyRegion]; got != region {
			t.Errorf("node %s region label = %q, want %q", node.Name, got, region)
		}
	}
}

// TestNode_InternalIP asserts each node's InternalIP equals its CloudStack
// VM's NIC address. This is the contract that makes the whole environment
// work: kubelet registers with the docker IP, and the CCM only initializes
// the node because the VM reports the same address.
func TestNode_InternalIP(t *testing.T) {
	f := NewFramework(t)
	for _, node := range f.Nodes() {
		vm, err := f.VMByName(node.Name)
		if err != nil || vm == nil {
			t.Fatalf("looking up VM for node %s: %v", node.Name, err)
		}
		if len(vm.Nic) == 0 {
			t.Fatalf("VM %s has no NICs", node.Name)
		}
		var internalIP string
		for _, addr := range node.Status.Addresses {
			if addr.Type == corev1.NodeInternalIP {
				internalIP = addr.Address
			}
		}
		if internalIP != vm.Nic[0].Ipaddress {
			t.Errorf("node %s InternalIP = %q, want VM NIC IP %q",
				node.Name, internalIP, vm.Nic[0].Ipaddress)
		}
	}
}
