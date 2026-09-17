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
	"context"
	"os"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// vpcFramework skips unless the harness is in the VPC phase.
//
// 50-topology-vpc.sh appends E2E_ACL_ID, E2E_VPC_ID and E2E_PROJECT_ID to
// hack/e2e/_out/ids.env. Note that the project is read from CS_PROJECT_ID, not
// E2E_PROJECT_ID, because it also configures the CloudStack client in
// NewFramework; the test runner maps one to the other. To run this phase by
// hand:
//
//	. hack/e2e/_out/ids.env
//	CS_PROJECT_ID="$E2E_PROJECT_ID" go test -tags e2e ./test/e2e/... -run TestVPC
func vpcFramework(t *testing.T) (*Framework, string, string) {
	t.Helper()
	aclID := os.Getenv("E2E_ACL_ID")
	vpcID := os.Getenv("E2E_VPC_ID")
	if aclID == "" || vpcID == "" || os.Getenv("CS_PROJECT_ID") == "" {
		t.Skip("E2E_ACL_ID/E2E_VPC_ID/CS_PROJECT_ID not set; skipping VPC phase test " +
			"(CS_PROJECT_ID is set from E2E_PROJECT_ID in ids.env)")
	}
	return NewFramework(t), aclID, vpcID
}

// TestVPC_LoadBalancer covers the VPC path end to end: the LB rule is
// created, the public IP is associated with the VPC, ingress traffic is
// allowed via a Network ACL rule on the custom ACL list (not a firewall
// rule), and everything is cleaned up on delete.
func TestVPC_LoadBalancer(t *testing.T) {
	f, aclID, vpcID := vpcFramework(t)

	svc := f.CreateLBService(nil)
	lbName := defaultLoadBalancerName(svc)

	f.WaitForIngressIP(svc)
	rules := f.WaitForLBRules(lbName, 1)
	rule := rules[0]

	ip, err := f.PublicIP(rule.Publicipid)
	if err != nil || ip == nil {
		t.Fatalf("fetching public IP %s: %v", rule.Publicipid, err)
	}
	if ip.Vpcid != vpcID {
		t.Errorf("public IP vpcid = %q, want %q", ip.Vpcid, vpcID)
	}

	f.Eventually(lbSyncTimeout, lbSyncInterval, "network ACL rule for port 80",
		func() (bool, error) {
			aclRules, err := f.ACLRules(aclID)
			if err != nil {
				return false, err
			}
			for _, r := range aclRules {
				if r.Startport == "80" && r.Endport == "80" &&
					strings.EqualFold(r.Protocol, "tcp") &&
					strings.EqualFold(r.Action, "Allow") &&
					strings.EqualFold(r.Traffictype, "Ingress") {
					return true, nil
				}
			}
			return false, nil
		})

	// The tier offering has no Firewall service, so no firewall rule belongs here.
	fwRules, err := f.FirewallRules(rule.Publicipid)
	if err != nil {
		t.Fatalf("listing firewall rules: %v", err)
	}
	for _, fw := range fwRules {
		if fw.Startport == 80 && fw.Endport == 80 {
			t.Errorf("unexpected firewall rule on VPC public IP: %+v", fw)
		}
	}

	f.DeleteServiceAndWait(svc)
	f.Eventually(lbSyncTimeout, lbSyncInterval, "network ACL rule to be removed",
		func() (bool, error) {
			aclRules, err := f.ACLRules(aclID)
			if err != nil {
				return false, err
			}
			for _, r := range aclRules {
				if r.Startport == "80" && r.Endport == "80" && strings.EqualFold(r.Protocol, "tcp") {
					return false, nil
				}
			}
			return true, nil
		})
}

// TestVPC_NodesReinitialized asserts the CCM re-initialized the nodes against
// the project VMs after the phase switch.
func TestVPC_NodesReinitialized(t *testing.T) {
	f, _, _ := vpcFramework(t)
	for _, node := range f.Nodes() {
		vm, err := f.VMByName(node.Name)
		if err != nil {
			t.Fatalf("looking up project VM for node %s: %v", node.Name, err)
		}
		if vm == nil {
			t.Errorf("no project VM named %s visible with CS_PROJECT_ID", node.Name)
		}
	}
	for _, node := range f.Nodes() {
		for _, taint := range node.Spec.Taints {
			if taint.Key == "node.cloudprovider.kubernetes.io/uninitialized" {
				t.Errorf("node %s still has the uninitialized taint", node.Name)
			}
		}
	}
}

// countACLRules returns how many ingress ACL rules on the list target a port.
func countACLRules(f *Framework, aclID, port string) (int, error) {
	rules, err := f.ACLRules(aclID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range rules {
		if r.Startport == port && r.Endport == port && strings.EqualFold(r.Protocol, "tcp") {
			n++
		}
	}
	return n, nil
}

// TestVPC_ACLRuleNotDuplicatedOnResync is a regression test for a
// project-scoping bug in updateNetworkACL: it listed the existing ACL rules
// without the project, so with project-id set it never saw the rule it had
// just created and appended another one on every reconcile.
func TestVPC_ACLRuleNotDuplicatedOnResync(t *testing.T) {
	f, aclID, _ := vpcFramework(t)

	// An ACL rule belongs to the tier, not the service, so sharing port 80 with
	// TestVPC_LoadBalancer would blur the count.
	const port = "8080"
	svc := f.CreateLBService(func(s *corev1.Service) {
		s.Spec.Ports = []corev1.ServicePort{
			{Name: "http", Port: 8080, Protocol: corev1.ProtocolTCP},
		}
	})
	lbName := defaultLoadBalancerName(svc)
	f.WaitForIngressIP(svc)

	f.Eventually(lbSyncTimeout, lbSyncInterval, "the ACL rule for port "+port,
		func() (bool, error) {
			n, err := countACLRules(f, aclID, port)
			return n >= 1, err
		})

	forceReconcile(f, svc, lbName)

	n, err := countACLRules(f, aclID, port)
	if err != nil {
		t.Fatalf("counting ACL rules: %v", err)
	}
	if n != 1 {
		t.Errorf("ACL rules for port %s = %d, want exactly 1; the reconcile duplicated the rule", port, n)
	}
}

// forceReconcile makes the service controller run EnsureLoadBalancer again by
// flipping sessionAffinity, then waits for the resulting algorithm change so the
// caller observes a reconcile that has demonstrably completed.
func forceReconcile(f *Framework, svc *corev1.Service, lbName string) {
	f.T.Helper()
	f.UpdateService(svc, func(s *corev1.Service) {
		s.Spec.SessionAffinity = corev1.ServiceAffinityClientIP
	})
	f.Eventually(lbSyncTimeout, lbSyncInterval, "the reconcile to apply the new algorithm",
		func() (bool, error) {
			rules, err := f.LBRules(lbName)
			if err != nil || len(rules) != 1 {
				return false, err
			}
			return rules[0].Algorithm == "source", nil
		})
}

// TestVPC_ExplicitLoadBalancerIPReleased is a regression test for a
// project-scoping bug in EnsureLoadBalancerDeleted: the disassociation check
// looked the public IP up without the project, so with project-id set the
// lookup failed, the controller decided not to disassociate, and the IP leaked
// on every deletion.
//
// It only reproduces with spec.loadBalancerIP set: an auto-allocated IP is
// released unconditionally and never reaches that check.
func TestVPC_ExplicitLoadBalancerIPReleased(t *testing.T) {
	f, _, _ := vpcFramework(t)

	freeIP, err := f.FreePublicIP()
	if err != nil {
		t.Fatalf("finding a free public IP: %v", err)
	}

	// The VPC virtual router rejects most public ports with "LB service provider
	// cannot support this rule"; 80 is one it accepts.
	svc := f.CreateLBService(func(s *corev1.Service) {
		s.Spec.LoadBalancerIP = freeIP
		s.Spec.Ports = []corev1.ServicePort{
			{Name: "http", Port: 80, Protocol: corev1.ProtocolTCP},
		}
	})

	if ingress := f.WaitForIngressIP(svc); ingress.IP != freeIP {
		t.Fatalf("ingress IP = %q, want requested %q", ingress.IP, freeIP)
	}

	// The annotation is what routes deletion through the disassociation path under test.
	f.Eventually(lbSyncTimeout, lbSyncInterval, "the ip-associated-by-controller annotation",
		func() (bool, error) {
			current, err := f.K8s.CoreV1().Services(svc.Namespace).Get(
				context.Background(), svc.Name, metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			return current.Annotations[annotationIPAssociated] == "true", nil
		})

	f.DeleteServiceAndWait(svc)

	f.Eventually(lbSyncTimeout, lbSyncInterval, "the explicitly requested IP to be released",
		func() (bool, error) {
			ip, err := f.PublicIPByAddress(freeIP)
			if err != nil || ip == nil {
				return false, err
			}
			return ip.Allocated == "", nil
		})
}
