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
	annotationSourceCidrs      = "service.beta.kubernetes.io/cloudstack-load-balancer-source-cidrs"
	annotationHostname         = "service.beta.kubernetes.io/cloudstack-load-balancer-hostname"
	annotationIPAssociated     = "service.beta.kubernetes.io/cloudstack-load-balancer-ip-associated-by-controller" //nolint:gosec
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

func TestAnnot_Stickiness(t *testing.T) {
	f := NewFramework(t)
	svc := f.CreateLBService(func(s *corev1.Service) {
		s.Annotations = map[string]string{
			annotationStickinessMethod: "LbCookie",
			annotationStickinessParams: "cookie-name=SERVERID",
		}
	})
	lbName := defaultLoadBalancerName(svc)

	f.WaitForIngressIP(svc)
	rules := f.WaitForLBRules(lbName, 1)
	ruleID := rules[0].Id
	policy := f.WaitForStickinessPolicy(ruleID, "LbCookie", map[string]string{"cookie-name": "SERVERID"})

	// A parameter change replaces the policy instead of editing it in place.
	f.UpdateService(svc, func(s *corev1.Service) {
		s.Annotations[annotationStickinessParams] = "cookie-name=JSESSIONID"
	})
	replaced := f.WaitForStickinessPolicy(ruleID, "LbCookie", map[string]string{"cookie-name": "JSESSIONID"})
	if replaced.Id == policy.Id {
		t.Errorf("policy %s was kept across a parameter change, want it recreated", policy.Id)
	}

	// Dropping the annotations removes the policy but keeps the rule.
	f.UpdateService(svc, func(s *corev1.Service) {
		delete(s.Annotations, annotationStickinessMethod)
		delete(s.Annotations, annotationStickinessParams)
	})
	f.Eventually(lbSyncTimeout, lbSyncInterval, "stickiness policy to be removed",
		func() (bool, error) {
			current, err := f.StickinessPolicy(ruleID)
			if err != nil {
				return false, err
			}
			return current == nil, nil
		})
	current := f.WaitForLBRules(lbName, 1)
	if current[0].Id != ruleID {
		t.Errorf("rule ID changed %s -> %s, want the rule to survive policy removal", ruleID, current[0].Id)
	}
}

// Replacing a policy has to delete the old one first, so a rejected replacement
// must leave the live rule with the stickiness it already had.
func TestAnnot_StickinessRejectedReplacement(t *testing.T) {
	f := NewFramework(t)
	svc := f.CreateLBService(func(s *corev1.Service) {
		s.Annotations = map[string]string{
			annotationStickinessMethod: "LbCookie",
			annotationStickinessParams: "cookie-name=SERVERID",
		}
	})
	lbName := defaultLoadBalancerName(svc)

	f.WaitForIngressIP(svc)
	rules := f.WaitForLBRules(lbName, 1)
	ruleID := rules[0].Id
	f.WaitForStickinessPolicy(ruleID, "LbCookie", map[string]string{"cookie-name": "SERVERID"})

	f.UpdateService(svc, func(s *corev1.Service) {
		s.Annotations[annotationStickinessMethod] = "NoSuchMethod"
	})
	f.Eventually(lbSyncTimeout, lbSyncInterval, "the sync to fail on the rejected replacement",
		func() (bool, error) {
			events, err := f.K8s.CoreV1().Events(svc.Namespace).List(context.Background(), metav1.ListOptions{
				FieldSelector: "involvedObject.name=" + svc.Name + ",reason=SyncLoadBalancerFailed",
			})
			if err != nil {
				return false, err
			}
			for _, event := range events.Items {
				if strings.Contains(event.Message, "stickiness") {
					return true, nil
				}
			}
			return false, nil
		})

	// The rollback put the original policy back, so the rule never goes unprotected.
	policy, err := f.StickinessPolicy(ruleID)
	if err != nil {
		t.Fatalf("reading the stickiness policy: %v", err)
	}
	if policy == nil {
		t.Fatal("rule lost its stickiness policy after a rejected replacement")
	}
	if !strings.EqualFold(policy.Methodname, "LbCookie") {
		t.Errorf("policy method = %q, want the original LbCookie", policy.Methodname)
	}

	f.UpdateService(svc, func(s *corev1.Service) {
		s.Annotations[annotationStickinessMethod] = "SourceBased"
		s.Annotations[annotationStickinessParams] = "tablesize=200k"
	})
	f.WaitForStickinessPolicy(ruleID, "SourceBased", map[string]string{"tablesize": "200k"})
}

// A rule whose stickiness policy CloudStack rejects must not survive as a
// half-configured rule: once the annotation is corrected, every rule has to end
// up with both its policy and its backend hosts.
func TestAnnot_StickinessInvalidMethod(t *testing.T) {
	f := NewFramework(t)
	svc := f.CreateLBService(nil)
	lbName := defaultLoadBalancerName(svc)

	f.WaitForIngressIP(svc)
	rules := f.WaitForLBRules(lbName, 1)
	var wantHosts int
	f.Eventually(lbSyncTimeout, lbSyncInterval, "hosts to be assigned to the first rule",
		func() (bool, error) {
			var err error
			wantHosts, err = f.RuleInstanceCount(rules[0].Id)
			return wantHosts > 0, err
		})

	// The new port is listed first so its rule is created, and its policy
	// rejected, before the existing rule is reconciled.
	f.UpdateService(svc, func(s *corev1.Service) {
		s.Annotations = map[string]string{annotationStickinessMethod: "NoSuchMethod"}
		s.Spec.Ports = []corev1.ServicePort{
			{Name: "alt", Port: 8080, Protocol: corev1.ProtocolTCP},
			{Name: "http", Port: 80, Protocol: corev1.ProtocolTCP},
		}
	})
	f.Eventually(lbSyncTimeout, lbSyncInterval, "the sync to fail on the rejected stickiness method",
		func() (bool, error) {
			events, err := f.K8s.CoreV1().Events(svc.Namespace).List(context.Background(), metav1.ListOptions{
				FieldSelector: "involvedObject.name=" + svc.Name + ",reason=SyncLoadBalancerFailed",
			})
			if err != nil {
				return false, err
			}
			for _, event := range events.Items {
				if strings.Contains(event.Message, "stickiness") {
					return true, nil
				}
			}
			return false, nil
		})

	f.UpdateService(svc, func(s *corev1.Service) {
		s.Annotations[annotationStickinessMethod] = "LbCookie"
		s.Annotations[annotationStickinessParams] = "cookie-name=SERVERID"
	})
	rules = f.WaitForLBRules(lbName, 2)
	for _, rule := range rules {
		f.WaitForStickinessPolicy(rule.Id, "LbCookie", map[string]string{"cookie-name": "SERVERID"})
		f.Eventually(lbSyncTimeout, lbSyncInterval, fmt.Sprintf("rule %s to have %d hosts", rule.Name, wantHosts),
			func() (bool, error) {
				got, err := f.RuleInstanceCount(rule.Id)
				if err != nil {
					return false, err
				}
				if got != wantHosts {
					return false, fmt.Errorf("rule %s has %d hosts", rule.Name, got)
				}
				return true, nil
			})
	}
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
