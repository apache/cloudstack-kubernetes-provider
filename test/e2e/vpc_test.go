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
	"strconv"
	"strings"
	"testing"

	"github.com/apache/cloudstack-go/v2/cloudstack"
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
// allowed via a Network ACL rule of the Service's own on the custom ACL list
// (not a firewall rule), and everything is cleaned up on delete.
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
					strings.EqualFold(r.Traffictype, "Ingress") &&
					r.Reason == aclRuleMarker(lbName) {
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

	// Every ACL rule on the port is counted, so no other Service may hold one on
	// it while this test runs; the VPC tests run one at a time and clean up.
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

// TestVPC_ProxyProtocolACL covers the proxy protocol on a VPC tier, where
// ingress is opened with a Network ACL rule rather than a firewall rule.
// updateNetworkACL used to create the rule with the CloudStack protocol name
// tcp-proxy, which the API rejects, so a proxy protocol service on a tier never
// reconciled at all. The ACL rule is keyed on the IP protocol, so it must be
// created as tcp and be the same single rule before and after the toggle.
func TestVPC_ProxyProtocolACL(t *testing.T) {
	f, aclID, _ := vpcFramework(t)

	// Every ACL rule on the port is counted, so no other Service may hold one on
	// it while this test runs. Note that 8081 is the virtual router's HAProxy
	// stats port, which CloudStack refuses to load balance.
	const servicePort int32 = 8085
	port := strconv.Itoa(int(servicePort))
	svc := f.CreateLBService(func(s *corev1.Service) {
		s.Annotations = map[string]string{annotationProxyProtocol: "true"}
		s.Spec.Ports = []corev1.ServicePort{
			{Name: "http", Port: servicePort, Protocol: corev1.ProtocolTCP},
		}
	})
	lbName := defaultLoadBalancerName(svc)

	f.WaitForIngressIP(svc)
	rules := f.WaitForLBRules(lbName, 1)
	if rules[0].Protocol != "tcp-proxy" {
		t.Errorf("rule protocol = %q, want tcp-proxy", rules[0].Protocol)
	}

	f.Eventually(lbSyncTimeout, lbSyncInterval, "the tcp network ACL rule for port "+port,
		func() (bool, error) {
			n, err := countACLRules(f, aclID, port)
			return n >= 1, err
		})

	aclRules, err := f.ACLRules(aclID)
	if err != nil {
		t.Fatalf("listing ACL rules: %v", err)
	}
	for _, r := range aclRules {
		if r.Startport == port && !strings.EqualFold(r.Protocol, "tcp") {
			t.Errorf("ACL rule for port %s has protocol %q, want tcp", port, r.Protocol)
		}
	}

	// Turning the annotation off keeps the Service's one ACL rule: both protocols share it.
	f.UpdateService(svc, func(s *corev1.Service) {
		delete(s.Annotations, annotationProxyProtocol)
	})
	f.Eventually(lbSyncTimeout, lbSyncInterval, "the rule to settle back on tcp",
		func() (bool, error) {
			current, err := f.LBRules(lbName)
			if err != nil || len(current) != 1 {
				return false, err
			}
			return current[0].Protocol == "tcp", nil
		})

	n, err := countACLRules(f, aclID, port)
	if err != nil {
		t.Fatalf("counting ACL rules: %v", err)
	}
	if n != 1 {
		t.Errorf("ACL rules for port %s = %d, want exactly 1 across the toggle", port, n)
	}
}

// aclRulesOnPort returns the ingress tcp ACL rules on the list for one port.
func aclRulesOnPort(f *Framework, aclID, port string) ([]*cloudstack.NetworkACL, error) {
	rules, err := f.ACLRules(aclID)
	if err != nil {
		return nil, err
	}
	var onPort []*cloudstack.NetworkACL
	for _, r := range rules {
		if r.Startport == port && r.Endport == port && strings.EqualFold(r.Protocol, "tcp") &&
			strings.EqualFold(r.Traffictype, "Ingress") {
			onPort = append(onPort, r)
		}
	}
	return onPort, nil
}

// hasACLRuleWithReason reports whether one of the rules carries the reason.
func hasACLRuleWithReason(rules []*cloudstack.NetworkACL, reason string) bool {
	for _, r := range rules {
		if r.Reason == reason {
			return true
		}
	}
	return false
}

// requireNoACLRules stops the test when rules for the port are left over from an earlier run,
// since the tests below count every rule on their port.
func requireNoACLRules(f *Framework, aclID, port string) {
	f.T.Helper()
	rules, err := aclRulesOnPort(f, aclID, port)
	if err != nil {
		f.T.Fatalf("listing ACL rules for port %s: %v", port, err)
	}
	for _, r := range rules {
		f.T.Errorf("leftover ACL rule %s for port %s (reason %q); delete it before rerunning", r.Id, port, r.Reason)
	}
	if len(rules) > 0 {
		f.T.FailNow()
	}
}

// TestVPC_ACLRulePerService is a regression test for issue #107: two Services
// on one tier exposing the same port each get an ACL rule of their own, so
// deleting one Service leaves the port open for the other. Earlier releases
// shared one rule between them and deleted it with the first Service, which
// this test reports as the port no longer being open.
func TestVPC_ACLRulePerService(t *testing.T) {
	f, aclID, _ := vpcFramework(t)

	const port = "80"
	requireNoACLRules(f, aclID, port)

	first := f.CreateLBService(nil)
	second := f.CreateLBService(func(s *corev1.Service) { s.Name = "e2e-second" })
	f.WaitForIngressIP(first)
	f.WaitForIngressIP(second)
	firstReason := aclRuleMarker(defaultLoadBalancerName(first))
	secondReason := aclRuleMarker(defaultLoadBalancerName(second))

	f.Eventually(lbSyncTimeout, lbSyncInterval, "port "+port+" to be open on the tier",
		func() (bool, error) {
			rules, err := aclRulesOnPort(f, aclID, port)
			return opensPortToAll(rules), err
		})

	f.DeleteServiceAndWait(first)

	rules, err := aclRulesOnPort(f, aclID, port)
	if err != nil {
		t.Fatalf("listing ACL rules: %v", err)
	}
	if !opensPortToAll(rules) {
		t.Fatalf("port %s is no longer open on the tier after deleting one of the two Services using it", port)
	}
	if !hasACLRuleWithReason(rules, secondReason) {
		t.Errorf("the remaining Service has no ACL rule of its own for port %s", port)
	}
	f.Eventually(lbSyncTimeout, lbSyncInterval, "the deleted Service's ACL rule to be removed",
		func() (bool, error) {
			rules, err := aclRulesOnPort(f, aclID, port)
			return !hasACLRuleWithReason(rules, firstReason), err
		})
}

// opensPortToAll reports whether one of the rules allows the port from anywhere and is not being
// deleted.
func opensPortToAll(rules []*cloudstack.NetworkACL) bool {
	for _, r := range rules {
		if strings.EqualFold(r.Action, "Allow") && r.State != "Deleting" &&
			strings.Contains(r.Cidrlist, "0.0.0.0/0") {
			return true
		}
	}
	return false
}

// TestVPC_LegacyACLRuleAdopted covers the upgrade path of issue #107: a rule
// shaped like the ones earlier releases created is adopted by the Service that
// needs its port, not duplicated, and is removed with that Service.
func TestVPC_LegacyACLRuleAdopted(t *testing.T) {
	f, aclID, _ := vpcFramework(t)

	const servicePort int32 = 8080
	port := strconv.Itoa(int(servicePort))
	requireNoACLRules(f, aclID, port)

	legacyID := f.CreateACLRule(aclID, int(servicePort), "0.0.0.0/0")
	svc := f.CreateLBService(func(s *corev1.Service) {
		s.Spec.Ports = []corev1.ServicePort{
			{Name: "http", Port: servicePort, Protocol: corev1.ProtocolTCP},
		}
	})
	reason := aclRuleMarker(defaultLoadBalancerName(svc))
	f.WaitForIngressIP(svc)

	f.Eventually(lbSyncTimeout, lbSyncInterval, "the existing ACL rule to be adopted",
		func() (bool, error) {
			r, err := f.ACLRule(aclID, legacyID)
			return r != nil && r.Reason == reason, err
		})
	if n, err := countACLRules(f, aclID, port); err != nil || n != 1 {
		t.Errorf("ACL rules for port %s = %d (error %v), want exactly 1; the Service added a rule instead of adopting", port, n, err)
	}

	f.DeleteServiceAndWait(svc)
	f.Eventually(lbSyncTimeout, lbSyncInterval, "the adopted ACL rule to be removed with its Service",
		func() (bool, error) {
			r, err := f.ACLRule(aclID, legacyID)
			return r == nil, err
		})
}

// TestVPC_OperatorACLRuleKept checks that the controller leaves an ACL rule
// someone else made for its port alone (issue #107): it adds no rule of its
// own and keeps the other rule when the Service is deleted.
func TestVPC_OperatorACLRuleKept(t *testing.T) {
	f, aclID, _ := vpcFramework(t)

	const servicePort int32 = 8085
	const operatorCIDR = "10.0.0.0/8"
	port := strconv.Itoa(int(servicePort))
	requireNoACLRules(f, aclID, port)

	operatorID := f.CreateACLRule(aclID, int(servicePort), operatorCIDR)
	svc := f.CreateLBService(func(s *corev1.Service) {
		s.Spec.Ports = []corev1.ServicePort{
			{Name: "http", Port: servicePort, Protocol: corev1.ProtocolTCP},
		}
	})
	f.WaitForIngressIP(svc)

	rules, err := aclRulesOnPort(f, aclID, port)
	if err != nil {
		t.Fatalf("listing ACL rules: %v", err)
	}
	if len(rules) != 1 || rules[0].Id != operatorID {
		t.Errorf("ACL rules for port %s = %d, want only the hand-made rule %s", port, len(rules), operatorID)
	}

	f.DeleteServiceAndWait(svc)

	r, err := f.ACLRule(aclID, operatorID)
	if err != nil {
		t.Fatalf("looking up the hand-made ACL rule: %v", err)
	}
	if r == nil {
		t.Fatalf("the hand-made ACL rule for port %s was deleted with the Service", port)
	}
	if r.Cidrlist != operatorCIDR || r.Reason != "" {
		t.Errorf("the hand-made ACL rule changed: cidrlist %q, reason %q", r.Cidrlist, r.Reason)
	}
}
