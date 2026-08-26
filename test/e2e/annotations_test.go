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
	"fmt"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	annotationSourceCidrs   = "service.beta.kubernetes.io/cloudstack-load-balancer-source-cidrs"
	annotationHostname      = "service.beta.kubernetes.io/cloudstack-load-balancer-hostname"
	annotationIPAssociated  = "service.beta.kubernetes.io/cloudstack-load-balancer-ip-associated-by-controller" //nolint:gosec
	annotationProxyProtocol = "service.beta.kubernetes.io/cloudstack-load-balancer-proxy-protocol"
)

func TestAnnot_SourceCIDRs(t *testing.T) {
	f := NewFramework(t)
	svc := f.CreateLBService(func(s *corev1.Service) {
		s.Annotations = map[string]string{
			annotationSourceCidrs: "10.0.0.0/8,192.168.100.0/24",
		}
	})
	lbName := defaultLoadBalancerName(svc)

	f.WaitForIngressIP(svc)
	rules := f.WaitForLBRules(lbName, 1)
	for _, cidr := range []string{"10.0.0.0/8", "192.168.100.0/24"} {
		if !strings.Contains(rules[0].Cidrlist, cidr) {
			t.Errorf("rule cidrlist = %q, want it to contain %s", rules[0].Cidrlist, cidr)
		}
	}
	originalRuleID := rules[0].Id

	f.UpdateService(svc, func(s *corev1.Service) {
		s.Annotations[annotationSourceCidrs] = "172.16.0.0/12"
	})
	// Assert on the settled rule after the poll, not inside it: a failed poll
	// would otherwise mask the in-place-versus-recreate check.
	var settledRuleID string
	f.Eventually(lbSyncTimeout, lbSyncInterval, "cidr list update to propagate",
		func() (bool, error) {
			current, err := f.LBRules(lbName)
			if err != nil {
				return false, err
			}
			if len(current) != 1 {
				return false, fmt.Errorf("saw %d rules, want 1", len(current))
			}
			if !strings.Contains(current[0].Cidrlist, "172.16.0.0/12") {
				return false, fmt.Errorf("cidrlist is %q, want it to contain 172.16.0.0/12",
					current[0].Cidrlist)
			}
			settledRuleID = current[0].Id
			return true, nil
		})

	// >= 4.22 updates the rule in place; older releases delete and recreate it.
	inPlace := f.Version.GTE(semver.Version{Major: 4, Minor: 22, Patch: 0})
	if inPlace && settledRuleID != originalRuleID {
		t.Errorf("expected in-place cidr update on %s (rule ID changed %s -> %s)",
			f.Version, originalRuleID, settledRuleID)
	}
	if !inPlace && settledRuleID == originalRuleID {
		t.Errorf("expected rule recreation on %s (rule ID unchanged)", f.Version)
	}
}

func TestAnnot_Hostname(t *testing.T) {
	f := NewFramework(t)
	svc := f.CreateLBService(func(s *corev1.Service) {
		s.Annotations = map[string]string{
			annotationHostname: "lb.example.com",
		}
	})

	ingress := f.WaitForIngressIP(svc)
	if ingress.Hostname != "lb.example.com" {
		t.Errorf("ingress hostname = %q, want lb.example.com", ingress.Hostname)
	}
	if ingress.IP != "" {
		t.Errorf("ingress IP = %q, want empty when hostname annotation is set", ingress.IP)
	}
}

func TestAnnot_SessionAffinity(t *testing.T) {
	f := NewFramework(t)
	svc := f.CreateLBService(func(s *corev1.Service) {
		s.Spec.SessionAffinity = corev1.ServiceAffinityClientIP
	})
	lbName := defaultLoadBalancerName(svc)

	f.WaitForIngressIP(svc)
	rules := f.WaitForLBRules(lbName, 1)
	if rules[0].Algorithm != "source" {
		t.Errorf("algorithm = %q, want source for sessionAffinity ClientIP", rules[0].Algorithm)
	}

	f.UpdateService(svc, func(s *corev1.Service) {
		s.Spec.SessionAffinity = corev1.ServiceAffinityNone
	})
	f.Eventually(lbSyncTimeout, lbSyncInterval, "algorithm to revert to roundrobin",
		func() (bool, error) {
			current, err := f.LBRules(lbName)
			if err != nil || len(current) != 1 {
				return false, err
			}
			return current[0].Algorithm == "roundrobin", nil
		})
}

func TestAnnot_ExplicitLoadBalancerIP(t *testing.T) {
	f := NewFramework(t)

	freeIP, err := f.FreePublicIP()
	if err != nil {
		t.Fatalf("finding a free public IP: %v", err)
	}

	svc := f.CreateLBService(func(s *corev1.Service) {
		s.Spec.LoadBalancerIP = freeIP
	})

	ingress := f.WaitForIngressIP(svc)
	if ingress.IP != freeIP {
		t.Fatalf("ingress IP = %q, want requested %q", ingress.IP, freeIP)
	}

	// The annotation is what routes deletion through the disassociation path.
	f.Eventually(lbSyncTimeout, lbSyncInterval, "ip-associated-by-controller annotation",
		func() (bool, error) {
			current, err := f.K8s.CoreV1().Services(svc.Namespace).Get(
				context.Background(), svc.Name, metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			return current.Annotations[annotationIPAssociated] == "true", nil
		})

	f.DeleteServiceAndWait(svc)
	f.Eventually(lbSyncTimeout, lbSyncInterval, "explicitly requested IP to be released",
		func() (bool, error) {
			ip, err := f.PublicIPByAddress(freeIP)
			if err != nil || ip == nil {
				return false, err
			}
			return ip.Allocated == "", nil
		})
}

// TestAnnot_ProxyProtocolToggle is the end-to-end regression test for issue #2:
// toggling the proxy protocol annotation on a live service used to wedge
// reconciliation for good. The rule name embeds the protocol and rules were
// looked up by name, so the changed protocol missed the lookup and the
// controller tried to create a second rule on a public port the old rule still
// held, which CloudStack rejects as a port conflict.
func TestAnnot_ProxyProtocolToggle(t *testing.T) {
	f := NewFramework(t)
	svc := f.CreateLBService(nil)
	lbName := defaultLoadBalancerName(svc)

	f.WaitForIngressIP(svc)
	rules := f.WaitForLBRules(lbName, 1)
	originalRuleID := rules[0].Id
	if rules[0].Protocol != "tcp" {
		t.Fatalf("rule protocol = %q, want tcp before the toggle", rules[0].Protocol)
	}

	// settle waits for exactly one rule carrying the wanted protocol and name,
	// and returns its ID so the caller can tell an update from a recreate.
	settle := func(protocol string) string {
		t.Helper()
		wantName := fmt.Sprintf("%s-%s-80", lbName, protocol)
		var ruleID string
		f.Eventually(lbSyncTimeout, lbSyncInterval, "the rule to settle on "+protocol,
			func() (bool, error) {
				current, err := f.LBRules(lbName)
				if err != nil {
					return false, err
				}
				if len(current) != 1 {
					return false, fmt.Errorf("saw %d rules, want 1", len(current))
				}
				if current[0].Protocol != protocol {
					return false, fmt.Errorf("protocol is %q, want %q", current[0].Protocol, protocol)
				}
				if current[0].Name != wantName {
					return false, fmt.Errorf("name is %q, want %q", current[0].Name, wantName)
				}
				ruleID = current[0].Id
				return true, nil
			})
		return ruleID
	}

	f.UpdateService(svc, func(s *corev1.Service) {
		s.Annotations = map[string]string{annotationProxyProtocol: "true"}
	})
	proxyRuleID := settle("tcp-proxy")
	if proxyRuleID != originalRuleID {
		t.Errorf("enabling the proxy protocol recreated the rule (%s -> %s), want an in-place update",
			originalRuleID, proxyRuleID)
	}

	f.UpdateService(svc, func(s *corev1.Service) {
		delete(s.Annotations, annotationProxyProtocol)
	})
	revertedRuleID := settle("tcp")
	if revertedRuleID != proxyRuleID {
		t.Errorf("disabling the proxy protocol recreated the rule (%s -> %s), want an in-place update",
			proxyRuleID, revertedRuleID)
	}

	// The public port stayed open throughout: the firewall rule is keyed on the
	// IP protocol, which both tcp and tcp-proxy map to.
	fwRules, err := f.FirewallRules(rules[0].Publicipid)
	if err != nil {
		t.Fatalf("listing firewall rules: %v", err)
	}
	found := false
	for _, fw := range fwRules {
		if fw.Startport == 80 && fw.Endport == 80 && strings.EqualFold(fw.Protocol, "tcp") {
			found = true
		}
	}
	if !found {
		t.Errorf("no tcp firewall rule for port 80 after the toggle; got %+v", fwRules)
	}
}
