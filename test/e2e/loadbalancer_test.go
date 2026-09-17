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
	"net"
	"strconv"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestLB_CreateSingleTCPPort(t *testing.T) {
	f := NewFramework(t)
	svc := f.CreateLBService(nil)
	lbName := defaultLoadBalancerName(svc)

	ingress := f.WaitForIngressIP(svc)
	if ingress.IP == "" {
		t.Fatalf("expected an ingress IP, got %+v", ingress)
	}
	if ip := net.ParseIP(ingress.IP); ip == nil {
		t.Fatalf("ingress IP %q is not a valid IP", ingress.IP)
	}

	rules := f.WaitForLBRules(lbName, 1)
	rule := rules[0]
	wantName := fmt.Sprintf("%s-tcp-80", lbName)
	if rule.Name != wantName {
		t.Errorf("rule name = %q, want %q", rule.Name, wantName)
	}
	if rule.Algorithm != "roundrobin" {
		t.Errorf("rule algorithm = %q, want roundrobin", rule.Algorithm)
	}
	if rule.Publicport != "80" {
		t.Errorf("rule public port = %q, want 80", rule.Publicport)
	}
	current, err := f.K8s.CoreV1().Services(svc.Namespace).Get(context.Background(), svc.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("getting service: %v", err)
	}
	nodePort := strconv.Itoa(int(current.Spec.Ports[0].NodePort))
	if rule.Privateport != nodePort {
		t.Errorf("rule private port = %q, want NodePort %q", rule.Privateport, nodePort)
	}
	if rule.Publicip != ingress.IP {
		t.Errorf("rule public IP = %q, want ingress IP %q", rule.Publicip, ingress.IP)
	}
	if !strings.Contains(rule.Cidrlist, "0.0.0.0/0") {
		t.Errorf("rule cidrlist = %q, want it to contain 0.0.0.0/0", rule.Cidrlist)
	}

	// The isolated network offering includes the Firewall service.
	f.Eventually(lbSyncTimeout, lbSyncInterval, "firewall rule for port 80",
		func() (bool, error) {
			fwRules, err := f.FirewallRules(rule.Publicipid)
			if err != nil {
				return false, err
			}
			for _, fw := range fwRules {
				if fw.Startport == 80 && fw.Endport == 80 && strings.EqualFold(fw.Protocol, "tcp") {
					return true, nil
				}
			}
			return false, nil
		})
}

func TestLB_MultiPort(t *testing.T) {
	f := NewFramework(t)
	svc := f.CreateLBService(func(s *corev1.Service) {
		s.Spec.Ports = []corev1.ServicePort{
			{Name: "http", Port: 80, Protocol: corev1.ProtocolTCP},
			{Name: "https", Port: 443, Protocol: corev1.ProtocolTCP},
		}
	})
	lbName := defaultLoadBalancerName(svc)

	f.WaitForIngressIP(svc)
	rules := f.WaitForLBRules(lbName, 2)
	if rules[0].Publicipid != rules[1].Publicipid {
		t.Errorf("expected both rules to share a public IP, got %q and %q",
			rules[0].Publicipid, rules[1].Publicipid)
	}
	ports := map[string]bool{}
	for _, r := range rules {
		ports[r.Publicport] = true
	}
	if !ports["80"] || !ports["443"] {
		t.Errorf("expected rules for ports 80 and 443, got %v", ports)
	}
}

func TestLB_NodeMembership(t *testing.T) {
	f := NewFramework(t)
	svc := f.CreateLBService(nil)
	lbName := defaultLoadBalancerName(svc)

	f.WaitForIngressIP(svc)
	rules := f.WaitForLBRules(lbName, 1)

	// kubeadm labels the control plane exclude-from-external-load-balancers, so only workers join the rule.
	wantIDs := map[string]bool{}
	for _, node := range f.Nodes() {
		if _, excluded := node.Labels["node.kubernetes.io/exclude-from-external-load-balancers"]; excluded {
			continue
		}
		vm, err := f.VMByName(node.Name)
		if err != nil || vm == nil {
			t.Fatalf("looking up VM for node %s: %v", node.Name, err)
		}
		wantIDs[vm.Id] = true
	}
	if len(wantIDs) == 0 {
		t.Fatal("no candidate worker nodes found")
	}

	f.Eventually(lbSyncTimeout, lbSyncInterval, "load balancer rule instances to match worker VMs",
		func() (bool, error) {
			p := f.CS.LoadBalancer.NewListLoadBalancerRuleInstancesParams(rules[0].Id)
			resp, err := f.CS.LoadBalancer.ListLoadBalancerRuleInstances(p)
			if err != nil {
				return false, err
			}
			gotIDs := map[string]bool{}
			for _, inst := range resp.LoadBalancerRuleInstances {
				gotIDs[inst.Id] = true
			}
			if len(gotIDs) != len(wantIDs) {
				return false, fmt.Errorf("got %d instances, want %d", len(gotIDs), len(wantIDs))
			}
			for id := range wantIDs {
				if !gotIDs[id] {
					return false, fmt.Errorf("VM %s missing from rule instances", id)
				}
			}
			return true, nil
		})
}

func TestLB_PortChange(t *testing.T) {
	f := NewFramework(t)
	svc := f.CreateLBService(nil)
	lbName := defaultLoadBalancerName(svc)

	f.WaitForIngressIP(svc)
	f.WaitForLBRules(lbName, 1)

	f.UpdateService(svc, func(s *corev1.Service) {
		s.Spec.Ports[0].Port = 8080
	})

	f.Eventually(lbSyncTimeout, lbSyncInterval, "rule for port 8080 to replace port 80",
		func() (bool, error) {
			rules, err := f.LBRules(lbName)
			if err != nil {
				return false, err
			}
			return len(rules) == 1 && rules[0].Publicport == "8080", nil
		})
}

func TestLB_Delete(t *testing.T) {
	f := NewFramework(t)
	svc := f.CreateLBService(nil)
	lbName := defaultLoadBalancerName(svc)

	f.WaitForIngressIP(svc)
	rules := f.WaitForLBRules(lbName, 1)
	publicIPID := rules[0].Publicipid

	f.DeleteServiceAndWait(svc)

	if remaining, err := f.LBRules(lbName); err != nil || len(remaining) != 0 {
		t.Errorf("expected no remaining rules, got %d (err %v)", len(remaining), err)
	}
	f.Eventually(lbSyncTimeout, lbSyncInterval, "public IP to be released",
		func() (bool, error) {
			ip, err := f.PublicIP(publicIPID)
			if err != nil {
				return false, err
			}
			return ip == nil || ip.Allocated == "", nil
		})
}
