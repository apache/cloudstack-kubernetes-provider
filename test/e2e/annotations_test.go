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
	"maps"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	annotationSourceCidrs      = "service.beta.kubernetes.io/cloudstack-load-balancer-source-cidrs"
	annotationHostname         = "service.beta.kubernetes.io/cloudstack-load-balancer-hostname"
	annotationIPAssociated     = "service.beta.kubernetes.io/cloudstack-load-balancer-ip-associated-by-controller" //nolint:gosec
	annotationProxyProtocol    = "service.beta.kubernetes.io/cloudstack-load-balancer-proxy-protocol"
	annotationStickinessMethod = "service.beta.kubernetes.io/cloudstack-load-balancer-stickiness-method-name"
	annotationStickinessParams = "service.beta.kubernetes.io/cloudstack-load-balancer-stickiness-method-param"
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

// LbCookie follows appProtocol: http, is kept by a sync that changes nothing, is replaced when a
// parameter changes and is removed with the annotations. The rules stay throughout.
func TestAnnot_Stickiness(t *testing.T) {
	f := NewFramework(t)
	http := "http"
	svc := f.CreateLBService(func(s *corev1.Service) {
		s.Annotations = map[string]string{
			annotationStickinessMethod: "LbCookie",
			annotationStickinessParams: "cookie-name=SERVERID,nocache=true",
		}
		s.Spec.Ports = []corev1.ServicePort{
			{Name: "http", Port: 80, Protocol: corev1.ProtocolTCP, AppProtocol: &http},
			{Name: "alt", Port: 8080, Protocol: corev1.ProtocolTCP},
		}
	})
	lbName := defaultLoadBalancerName(svc)
	f.WaitForIngressIP(svc)
	rules := ruleIDsByPort(f.WaitForLBRules(lbName, 2))
	cookie := map[string]string{"cookie-name": "SERVERID", "nocache": "true"}

	policy := f.WaitForStickinessPolicy(rules["80"], "LbCookie", cookie)
	if policy.Description != stickinessPolicyMarker {
		t.Errorf("policy description = %q, want the controller's marker", policy.Description)
	}
	f.WaitForNoStickinessPolicy(rules["8080"])

	// Recreating an unchanged policy on every sync would reset affinity each time.
	f.ResyncAndWait(svc)
	if again := f.WaitForStickinessPolicy(rules["80"], "LbCookie", cookie); again.Id != policy.Id {
		t.Errorf("policy replaced %s -> %s by a reconcile that changed nothing", policy.Id, again.Id)
	}

	f.UpdateService(svc, func(s *corev1.Service) {
		s.Spec.Ports[0].AppProtocol = nil
		s.Spec.Ports[1].AppProtocol = &http
	})
	moved := f.WaitForStickinessPolicy(rules["8080"], "LbCookie", cookie)
	f.WaitForNoStickinessPolicy(rules["80"])

	f.UpdateService(svc, func(s *corev1.Service) {
		s.Annotations[annotationStickinessParams] = "cookie-name=JSESSIONID"
	})
	if replaced := f.WaitForStickinessPolicy(rules["8080"], "LbCookie", map[string]string{"cookie-name": "JSESSIONID"}); replaced.Id == moved.Id {
		t.Errorf("policy %s was kept across a parameter change, want it replaced", moved.Id)
	}

	f.UpdateService(svc, func(s *corev1.Service) {
		delete(s.Annotations, annotationStickinessMethod)
		delete(s.Annotations, annotationStickinessParams)
	})
	f.WaitForNoStickinessPolicy(rules["8080"])
	if after := ruleIDsByPort(f.WaitForLBRules(lbName, 2)); !maps.Equal(after, rules) {
		t.Errorf("rules changed %v -> %v, want them kept", rules, after)
	}
}

// A policy added by hand stays on a Service that does not ask for stickiness.
func TestAnnot_StickinessForeignPolicy(t *testing.T) {
	f := NewFramework(t)
	svc := f.CreateLBService(nil)
	f.WaitForIngressIP(svc)
	rules := f.WaitForLBRules(defaultLoadBalancerName(svc), 1)
	manual := f.CreateStickinessPolicy(rules[0].Id, "manual", "SourceBased", map[string]string{"tablesize": "200k"})

	f.ResyncAndWait(svc)
	live, err := f.StickinessPolicies(rules[0].Id)
	if err != nil {
		t.Fatalf("listing stickiness policies: %v", err)
	}
	if len(live) != 1 || live[0].Id != manual {
		t.Errorf("live policies = %+v, want the hand-made %s kept", live, manual)
	}
}

// A stickiness change that cannot be applied fails the sync and leaves the current policies alone,
// whether the controller or CloudStack rejects it. Each case uses its own Service, because failing
// syncs keep recording events and Kubernetes drops events for an object that records too many.
func TestAnnot_StickinessRejected(t *testing.T) {
	cases := []struct {
		name       string
		method     string
		params     string
		undeclared bool
		wantErr    string
	}{
		{name: "a value CloudStack rejects", method: "SourceBased", params: "tablesize=abc", wantErr: "tablesize"},
		{name: "a method the network does not offer", method: "NoSuchMethod", wantErr: "not supported on this network"},
		{name: "an LbCookie mode HAProxy does not know", method: "LbCookie", params: "mode=bogus", wantErr: "insert, rewrite, prefix"},
		{name: "an empty parameter value", method: "LbCookie", params: "cookie-name=", wantErr: "missing value"},
		{name: "a character HAProxy would misread", method: "LbCookie", params: "cookie-name=a#b", wantErr: "are not allowed"},
		{name: "LbCookie on a port without appProtocol http", method: "LbCookie", undeclared: true, wantErr: "appProtocol: http"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := NewFramework(t)
			http := "http"
			svc := f.CreateLBService(func(s *corev1.Service) {
				s.Annotations = map[string]string{
					annotationStickinessMethod: "SourceBased",
					annotationStickinessParams: "tablesize=200k",
				}
				if !tc.undeclared {
					s.Spec.Ports[0].AppProtocol = &http
				}
			})
			policies := map[string]string{}
			for _, rule := range f.WaitForLBRules(defaultLoadBalancerName(svc), 1) {
				policies[rule.Id] = f.WaitForStickinessPolicy(rule.Id, "SourceBased", map[string]string{"tablesize": "200k"}).Id
			}

			f.UpdateService(svc, func(s *corev1.Service) {
				s.Annotations[annotationStickinessMethod] = tc.method
				s.Annotations[annotationStickinessParams] = tc.params
			})
			f.WaitForSyncFailure(svc, tc.wantErr)

			for ruleID, policyID := range policies {
				live, err := f.StickinessPolicies(ruleID)
				if err != nil {
					t.Fatalf("listing stickiness policies of rule %s: %v", ruleID, err)
				}
				if len(live) != 1 || live[0].Id != policyID {
					t.Errorf("rule %s policies = %+v, want %s untouched", ruleID, live, policyID)
				}
			}
		})
	}
}

// A new Service whose policy CloudStack rejects keeps its IP and rule and stays pending. Once the
// annotation is fixed, it publishes the same IP.
func TestAnnot_StickinessNewServiceRejected(t *testing.T) {
	f := NewFramework(t)
	svc := f.CreateLBService(func(s *corev1.Service) {
		s.Annotations = map[string]string{
			annotationStickinessMethod: "SourceBased",
			annotationStickinessParams: "tablesize=abc",
		}
	})
	lbName := defaultLoadBalancerName(svc)
	f.WaitForSyncFailure(svc, "tablesize")
	rules := f.WaitForLBRules(lbName, 1)

	current, err := f.K8s.CoreV1().Services(svc.Namespace).Get(context.Background(), svc.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting service: %v", err)
	}
	if len(current.Status.LoadBalancer.Ingress) != 0 {
		t.Errorf("ingress = %v, want none while the sync fails", current.Status.LoadBalancer.Ingress)
	}

	f.UpdateService(svc, func(s *corev1.Service) {
		s.Annotations[annotationStickinessParams] = "tablesize=200k"
	})
	if ingress := f.WaitForIngressIP(svc); ingress.IP != rules[0].Publicip {
		t.Errorf("ingress IP = %q, want the IP the failed sync kept, %q", ingress.IP, rules[0].Publicip)
	}
	f.WaitForStickinessPolicy(rules[0].Id, "SourceBased", map[string]string{"tablesize": "200k"})
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
