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

package cloudstack

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/apache/cloudstack-go/v2/cloudstack"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

type entryPointEnv struct {
	cs      *CSCloud
	lb      *cloudstack.MockLoadBalancerServiceIface
	vm      *cloudstack.MockVirtualMachineServiceIface
	network *cloudstack.MockNetworkServiceIface
	address *cloudstack.MockAddressServiceIface
	fw      *cloudstack.MockFirewallServiceIface
	service *corev1.Service
}

func newEntryPointEnv(t *testing.T, projectID string) *entryPointEnv {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	e := &entryPointEnv{
		lb:      cloudstack.NewMockLoadBalancerServiceIface(ctrl),
		vm:      cloudstack.NewMockVirtualMachineServiceIface(ctrl),
		network: cloudstack.NewMockNetworkServiceIface(ctrl),
		address: cloudstack.NewMockAddressServiceIface(ctrl),
		fw:      cloudstack.NewMockFirewallServiceIface(ctrl),
		service: &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default", UID: "test-uid"}},
	}
	e.cs = &CSCloud{
		projectID: projectID,
		client: &cloudstack.CloudStackClient{
			LoadBalancer: e.lb, VirtualMachine: e.vm, Network: e.network, Address: e.address, Firewall: e.fw,
		},
	}
	return e
}

func tcpRule(id, port, ip, ipID string) *cloudstack.LoadBalancerRule {
	return &cloudstack.LoadBalancerRule{
		Id: id, Name: "atestuid-tcp-" + port, Publicport: port, Protocol: "tcp",
		Publicip: ip, Publicipid: ipID, Networkid: "net-1",
	}
}

// expectRuleListing expects the lookup of the Service's rules and checks the filter it sends.
func (e *entryPointEnv) expectRuleListing(t *testing.T, rules []*cloudstack.LoadBalancerRule, err error) *gomock.Call {
	t.Helper()
	e.lb.EXPECT().NewListLoadBalancerRulesParams().Return(&cloudstack.ListLoadBalancerRulesParams{})
	return e.lb.EXPECT().ListLoadBalancerRules(gomock.Any()).
		DoAndReturn(func(p *cloudstack.ListLoadBalancerRulesParams) (*cloudstack.ListLoadBalancerRulesResponse, error) {
			if keyword, _ := p.GetKeyword(); keyword != "atestuid" {
				t.Errorf("rule listing keyword = %q, want atestuid", keyword)
			}
			if listAll, _ := p.GetListall(); !listAll {
				t.Errorf("rule listing does not set listall")
			}
			if project, _ := p.GetProjectid(); project != e.cs.projectID {
				t.Errorf("rule listing project = %q, want %q", project, e.cs.projectID)
			}
			if err != nil {
				return nil, err
			}
			return &cloudstack.ListLoadBalancerRulesResponse{Count: len(rules), LoadBalancerRules: rules}, nil
		})
}

// TestGetLoadBalancerStatus checks what Kubernetes is told about a Service's load balancer and that the
// rules are looked up by the Service's load balancer name in its project.
func TestGetLoadBalancerStatus(t *testing.T) {
	tests := []struct {
		name        string
		projectID   string
		annotations map[string]string
		rules       []*cloudstack.LoadBalancerRule
		err         error
		wantStatus  *corev1.LoadBalancerStatus
		wantExists  bool
		wantErr     string
	}{
		{name: "no rules means no load balancer"},
		{
			name:       "rules give the ingress IP",
			rules:      []*cloudstack.LoadBalancerRule{tcpRule("rule-80", "80", "203.0.113.1", "ip-1")},
			wantStatus: &corev1.LoadBalancerStatus{Ingress: []corev1.LoadBalancerIngress{{IP: "203.0.113.1"}}},
			wantExists: true,
		},
		{
			name:       "rules in a project",
			projectID:  testProjectID,
			rules:      []*cloudstack.LoadBalancerRule{tcpRule("rule-80", "80", "203.0.113.1", "ip-1")},
			wantStatus: &corev1.LoadBalancerStatus{Ingress: []corev1.LoadBalancerIngress{{IP: "203.0.113.1"}}},
			wantExists: true,
		},
		{
			name:        "proxy protocol reports the Proxy IP mode",
			annotations: map[string]string{ServiceAnnotationLoadBalancerProxyProtocol: "true"},
			rules:       []*cloudstack.LoadBalancerRule{tcpRule("rule-80", "80", "203.0.113.1", "ip-1")},
			wantStatus: &corev1.LoadBalancerStatus{Ingress: []corev1.LoadBalancerIngress{
				{IP: "203.0.113.1", IPMode: ptr.To(corev1.LoadBalancerIPModeProxy)},
			}},
			wantExists: true,
		},
		{name: "listing fails", err: errors.New("boom"), wantErr: "error retrieving load balancer rules: boom"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEntryPointEnv(t, tt.projectID)
			env.service.Annotations = tt.annotations
			env.expectRuleListing(t, tt.rules, tt.err)

			status, exists, err := env.cs.GetLoadBalancer(context.TODO(), testClusterName, env.service)

			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("GetLoadBalancer() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || exists != tt.wantExists {
				t.Fatalf("GetLoadBalancer() = %v, %v, want exists %v", exists, err, tt.wantExists)
			}
			if (status == nil) != (tt.wantStatus == nil) || (status != nil && !reflect.DeepEqual(status, tt.wantStatus)) {
				t.Errorf("GetLoadBalancer() status = %+v, want %+v", status, tt.wantStatus)
			}
		})
	}
}

func (e *entryPointEnv) expectNodeVMs(vms ...*cloudstack.VirtualMachine) {
	e.vm.EXPECT().NewListVirtualMachinesParams().Return(&cloudstack.ListVirtualMachinesParams{})
	e.vm.EXPECT().ListVirtualMachines(gomock.Any()).
		Return(&cloudstack.ListVirtualMachinesResponse{Count: len(vms), VirtualMachines: vms}, nil)
}

func nodeVM(id, name string) *cloudstack.VirtualMachine {
	return &cloudstack.VirtualMachine{Id: id, Name: name, Nic: []cloudstack.Nic{{Networkid: "net-1"}}}
}

// expectRuleMembers answers the member listing of each rule by the rule ID the listing was built for.
func (e *entryPointEnv) expectRuleMembers(members map[string][]string, failFor string) {
	e.lb.EXPECT().NewListLoadBalancerRuleInstancesParams(gomock.Any()).
		DoAndReturn(func(id string) *cloudstack.ListLoadBalancerRuleInstancesParams {
			p := &cloudstack.ListLoadBalancerRuleInstancesParams{}
			p.SetId(id)
			return p
		}).AnyTimes()
	e.lb.EXPECT().ListLoadBalancerRuleInstances(gomock.Any()).
		DoAndReturn(func(p *cloudstack.ListLoadBalancerRuleInstancesParams) (*cloudstack.ListLoadBalancerRuleInstancesResponse, error) {
			id, _ := p.GetId()
			if id == failFor {
				return nil, errors.New("boom")
			}
			var vms []*cloudstack.VirtualMachine
			for _, vmID := range members[id] {
				vms = append(vms, &cloudstack.VirtualMachine{Id: vmID})
			}
			return &cloudstack.ListLoadBalancerRuleInstancesResponse{Count: len(vms), LoadBalancerRuleInstances: vms}, nil
		}).AnyTimes()
}

// recordMembershipChanges records the hosts assigned to and removed from each rule.
func (e *entryPointEnv) recordMembershipChanges() (assigned, removed map[string][]string) {
	assigned, removed = map[string][]string{}, map[string][]string{}

	e.lb.EXPECT().NewAssignToLoadBalancerRuleParams(gomock.Any()).
		DoAndReturn(func(id string) *cloudstack.AssignToLoadBalancerRuleParams {
			p := &cloudstack.AssignToLoadBalancerRuleParams{}
			p.SetId(id)
			return p
		}).AnyTimes()
	e.lb.EXPECT().AssignToLoadBalancerRule(gomock.Any()).
		DoAndReturn(func(p *cloudstack.AssignToLoadBalancerRuleParams) (*cloudstack.AssignToLoadBalancerRuleResponse, error) {
			id, _ := p.GetId()
			ids, _ := p.GetVirtualmachineids()
			assigned[id] = ids
			return &cloudstack.AssignToLoadBalancerRuleResponse{}, nil
		}).AnyTimes()
	e.lb.EXPECT().NewRemoveFromLoadBalancerRuleParams(gomock.Any()).
		DoAndReturn(func(id string) *cloudstack.RemoveFromLoadBalancerRuleParams {
			p := &cloudstack.RemoveFromLoadBalancerRuleParams{}
			p.SetId(id)
			return p
		}).AnyTimes()
	e.lb.EXPECT().RemoveFromLoadBalancerRule(gomock.Any()).
		DoAndReturn(func(p *cloudstack.RemoveFromLoadBalancerRuleParams) (*cloudstack.RemoveFromLoadBalancerRuleResponse, error) {
			id, _ := p.GetId()
			ids, _ := p.GetVirtualmachineids()
			removed[id] = ids
			return &cloudstack.RemoveFromLoadBalancerRuleResponse{}, nil
		}).AnyTimes()

	return assigned, removed
}

// TestUpdateLoadBalancerMembership checks that each rule is brought in line with the nodes by
// assigning only the missing hosts and removing only the stale ones.
func TestUpdateLoadBalancerMembership(t *testing.T) {
	tests := []struct {
		name         string
		members      map[string][]string
		wantAssigned map[string][]string
		wantRemoved  map[string][]string
	}{
		{
			name:    "rules already match",
			members: map[string][]string{"rule-80": {"vm-1", "vm-2"}, "rule-8080": {"vm-2", "vm-1"}},
		},
		{
			name:         "a node was added",
			members:      map[string][]string{"rule-80": {"vm-1"}, "rule-8080": {"vm-1", "vm-2"}},
			wantAssigned: map[string][]string{"rule-80": {"vm-2"}},
		},
		{
			name:        "a node was removed",
			members:     map[string][]string{"rule-80": {"vm-1", "vm-2", "vm-3"}, "rule-8080": {"vm-1", "vm-2"}},
			wantRemoved: map[string][]string{"rule-80": {"vm-3"}},
		},
		{
			name:         "nodes were replaced on one rule only",
			members:      map[string][]string{"rule-80": {"vm-1", "vm-3"}, "rule-8080": {"vm-1", "vm-2"}},
			wantAssigned: map[string][]string{"rule-80": {"vm-2"}},
			wantRemoved:  map[string][]string{"rule-80": {"vm-3"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newEntryPointEnv(t, testProjectID)
			env.expectRuleListing(t, []*cloudstack.LoadBalancerRule{
				tcpRule("rule-80", "80", "203.0.113.1", "ip-1"),
				tcpRule("rule-8080", "8080", "203.0.113.1", "ip-1"),
			}, nil)
			env.expectNodeVMs(nodeVM("vm-1", "node-1"), nodeVM("vm-2", "node-2"), nodeVM("vm-9", "other"))
			env.expectRuleMembers(tt.members, "")
			assigned, removed := env.recordMembershipChanges()

			if err := env.cs.UpdateLoadBalancer(context.TODO(), testClusterName, env.service, nodesNamed("node-1", "node-2")); err != nil {
				t.Fatalf("UpdateLoadBalancer() unexpected error: %v", err)
			}

			assertMembershipChanges(t, "assigned", assigned, tt.wantAssigned)
			assertMembershipChanges(t, "removed", removed, tt.wantRemoved)
		})
	}
}

func assertMembershipChanges(t *testing.T, what string, got, want map[string][]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s hosts on rules %v, want %v", what, got, want)
		return
	}
	for rule, wantIDs := range want {
		gotIDs := slices.Clone(got[rule])
		slices.Sort(gotIDs)
		if !slices.Equal(gotIDs, wantIDs) {
			t.Errorf("%s hosts on %s = %v, want %v", what, rule, gotIDs, wantIDs)
		}
	}
}

// TestUpdateLoadBalancerFailures checks that a failing lookup stops the update before any rule is changed.
func TestUpdateLoadBalancerFailures(t *testing.T) {
	t.Run("rule listing fails", func(t *testing.T) {
		env := newEntryPointEnv(t, "")
		env.expectRuleListing(t, nil, errors.New("boom"))

		err := env.cs.UpdateLoadBalancer(context.TODO(), testClusterName, env.service, nodesNamed("node-1"))

		if err == nil || !strings.Contains(err.Error(), "error retrieving load balancer rules: boom") {
			t.Errorf("UpdateLoadBalancer() error = %v, want the listing error", err)
		}
	})

	t.Run("no node is known to CloudStack", func(t *testing.T) {
		env := newEntryPointEnv(t, "")
		env.expectRuleListing(t, []*cloudstack.LoadBalancerRule{tcpRule("rule-80", "80", "203.0.113.1", "ip-1")}, nil)
		env.expectNodeVMs(nodeVM("vm-9", "other"))

		err := env.cs.UpdateLoadBalancer(context.TODO(), testClusterName, env.service, nodesNamed("node-1"))

		if err == nil || !strings.Contains(err.Error(), "none of the hosts matched") {
			t.Errorf("UpdateLoadBalancer() error = %v, want the host matching error", err)
		}
	})

	t.Run("member listing fails", func(t *testing.T) {
		env := newEntryPointEnv(t, "")
		env.expectRuleListing(t, []*cloudstack.LoadBalancerRule{tcpRule("rule-80", "80", "203.0.113.1", "ip-1")}, nil)
		env.expectNodeVMs(nodeVM("vm-1", "node-1"))
		env.expectRuleMembers(nil, "rule-80")

		err := env.cs.UpdateLoadBalancer(context.TODO(), testClusterName, env.service, nodesNamed("node-1"))

		if err == nil || !strings.Contains(err.Error(), "error retrieving associated instances: boom") {
			t.Errorf("UpdateLoadBalancer() error = %v, want the member listing error", err)
		}
	})

	t.Run("assigning hosts fails", func(t *testing.T) {
		env := newEntryPointEnv(t, "")
		env.expectRuleListing(t, []*cloudstack.LoadBalancerRule{tcpRule("rule-80", "80", "203.0.113.1", "ip-1")}, nil)
		env.expectNodeVMs(nodeVM("vm-1", "node-1"))
		env.expectRuleMembers(map[string][]string{"rule-80": {}}, "")
		env.lb.EXPECT().NewAssignToLoadBalancerRuleParams("rule-80").Return(&cloudstack.AssignToLoadBalancerRuleParams{})
		env.lb.EXPECT().AssignToLoadBalancerRule(gomock.Any()).Return(nil, errors.New("boom"))

		err := env.cs.UpdateLoadBalancer(context.TODO(), testClusterName, env.service, nodesNamed("node-1"))

		if err == nil || !strings.Contains(err.Error(), "error assigning hosts to load balancer rule atestuid-tcp-80: boom") {
			t.Errorf("UpdateLoadBalancer() error = %v, want the assign error", err)
		}
	})

	t.Run("removing hosts fails", func(t *testing.T) {
		env := newEntryPointEnv(t, "")
		env.expectRuleListing(t, []*cloudstack.LoadBalancerRule{tcpRule("rule-80", "80", "203.0.113.1", "ip-1")}, nil)
		env.expectNodeVMs(nodeVM("vm-1", "node-1"))
		env.expectRuleMembers(map[string][]string{"rule-80": {"vm-1", "vm-3"}}, "")
		env.lb.EXPECT().NewRemoveFromLoadBalancerRuleParams("rule-80").Return(&cloudstack.RemoveFromLoadBalancerRuleParams{})
		env.lb.EXPECT().RemoveFromLoadBalancerRule(gomock.Any()).Return(nil, errors.New("boom"))

		err := env.cs.UpdateLoadBalancer(context.TODO(), testClusterName, env.service, nodesNamed("node-1"))

		if err == nil || !strings.Contains(err.Error(), "error removing hosts from load balancer rule atestuid-tcp-80: boom") {
			t.Errorf("UpdateLoadBalancer() error = %v, want the remove error", err)
		}
	})
}

// expectRuleDeletion expects the firewall cleanup and the deletion of one rule on a Firewall network.
func (e *entryPointEnv) expectRuleDeletion(t *testing.T, ruleID, ipID string) {
	t.Helper()
	e.network.EXPECT().GetNetworkByID("net-1", gomock.Any()).Return(&cloudstack.Network{Id: "net-1"}, 1, nil)
	e.fw.EXPECT().NewListFirewallRulesParams().Return(&cloudstack.ListFirewallRulesParams{})
	e.fw.EXPECT().ListFirewallRules(gomock.Any()).
		DoAndReturn(func(p *cloudstack.ListFirewallRulesParams) (*cloudstack.ListFirewallRulesResponse, error) {
			if got, _ := p.GetIpaddressid(); got != ipID {
				t.Errorf("firewall rules listed for IP %q, want %q", got, ipID)
			}
			return &cloudstack.ListFirewallRulesResponse{}, nil
		})
	e.lb.EXPECT().NewDeleteLoadBalancerRuleParams(ruleID).Return(&cloudstack.DeleteLoadBalancerRuleParams{})
	e.lb.EXPECT().DeleteLoadBalancerRule(gomock.Any()).Return(&cloudstack.DeleteLoadBalancerRuleResponse{}, nil)
}

func (e *entryPointEnv) expectIPLookup(t *testing.T, ipID string, ip *cloudstack.PublicIpAddress, err error) {
	t.Helper()
	e.address.EXPECT().GetPublicIpAddressByID(gomock.Any(), gomock.Any()).
		DoAndReturn(func(id string, opts ...cloudstack.OptionFunc) (*cloudstack.PublicIpAddress, int, error) {
			if id != ipID || appliedProjectID(t, opts) != e.cs.projectID {
				t.Errorf("IP lookup of %q in project %q, want %q in %q", id, appliedProjectID(t, opts), ipID, e.cs.projectID)
			}
			if err != nil {
				return nil, 0, err
			}
			return ip, 1, nil
		})
}

func (e *entryPointEnv) expectRulesUsingIP(t *testing.T, ipID string, others []*cloudstack.LoadBalancerRule, err error) {
	t.Helper()
	e.lb.EXPECT().NewListLoadBalancerRulesParams().Return(&cloudstack.ListLoadBalancerRulesParams{})
	e.lb.EXPECT().ListLoadBalancerRules(gomock.Any()).
		DoAndReturn(func(p *cloudstack.ListLoadBalancerRulesParams) (*cloudstack.ListLoadBalancerRulesResponse, error) {
			if got, _ := p.GetPublicipid(); got != ipID {
				t.Errorf("rules listed for IP %q, want %q", got, ipID)
			}
			if listAll, _ := p.GetListall(); !listAll {
				t.Errorf("rules using the IP are listed without listall")
			}
			if project, _ := p.GetProjectid(); project != e.cs.projectID {
				t.Errorf("rules using the IP listed in project %q, want %q", project, e.cs.projectID)
			}
			if err != nil {
				return nil, err
			}
			return &cloudstack.ListLoadBalancerRulesResponse{Count: len(others), LoadBalancerRules: others}, nil
		})
}

func (e *entryPointEnv) expectIPRelease(t *testing.T, ipID string, err error) {
	t.Helper()
	e.address.EXPECT().NewDisassociateIpAddressParams(ipID).Return(&cloudstack.DisassociateIpAddressParams{})
	e.address.EXPECT().DisassociateIpAddress(gomock.Any()).Return(&cloudstack.DisassociateIpAddressResponse{}, err)
}

// TestEnsureLoadBalancerDeletedPublicIP checks which public IPs are released once the Service's
// rules are gone: one the controller allocated, and a user supplied one only when the controller
// associated it and no other Service still has rules on it.
func TestEnsureLoadBalancerDeletedPublicIP(t *testing.T) {
	const ipID = "ip-1"
	allocated := &cloudstack.PublicIpAddress{Id: ipID, Allocated: "2024-01-01T00:00:00+0000"}
	otherRule := tcpRule("other-rule", "8080", "203.0.113.1", ipID)

	tests := []struct {
		name         string
		userIP       string
		annotation   string
		expect       func(e *entryPointEnv, t *testing.T)
		wantErrorMsg string
	}{
		{
			name: "IP allocated by the controller is released",
			expect: func(e *entryPointEnv, t *testing.T) {
				e.expectIPRelease(t, ipID, nil)
			},
		},
		{
			name: "failure to release the IP is returned",
			expect: func(e *entryPointEnv, t *testing.T) {
				e.expectIPRelease(t, ipID, errors.New("boom"))
			},
			wantErrorMsg: "error releasing load balancer IP 203.0.113.1: boom",
		},
		{
			name:   "user IP the controller did not associate is kept",
			userIP: "203.0.113.1",
			expect: func(e *entryPointEnv, t *testing.T) {},
		},
		{
			name:       "user IP associated by the controller is released when no rule uses it",
			userIP:     "203.0.113.1",
			annotation: "true",
			expect: func(e *entryPointEnv, t *testing.T) {
				e.expectIPLookup(t, ipID, allocated, nil)
				e.expectRulesUsingIP(t, ipID, nil, nil)
				e.expectIPRelease(t, ipID, nil)
			},
		},
		{
			name:       "user IP associated by the controller is kept while another rule uses it",
			userIP:     "203.0.113.1",
			annotation: "true",
			expect: func(e *entryPointEnv, t *testing.T) {
				e.expectIPLookup(t, ipID, allocated, nil)
				e.expectRulesUsingIP(t, ipID, []*cloudstack.LoadBalancerRule{otherRule}, nil)
			},
		},
		{
			name:       "user IP is kept when it cannot be looked up",
			userIP:     "203.0.113.1",
			annotation: "true",
			expect: func(e *entryPointEnv, t *testing.T) {
				e.expectIPLookup(t, ipID, nil, errors.New("boom"))
			},
		},
		{
			name:       "user IP is kept when the rules using it cannot be listed",
			userIP:     "203.0.113.1",
			annotation: "true",
			expect: func(e *entryPointEnv, t *testing.T) {
				e.expectIPLookup(t, ipID, allocated, nil)
				e.expectRulesUsingIP(t, ipID, nil, errors.New("boom"))
			},
		},
	}

	for _, tt := range tests {
		for _, projectID := range []string{"", testProjectID} {
			t.Run(tt.name+"/project "+projectID, func(t *testing.T) {
				env := newEntryPointEnv(t, projectID)
				env.service.Spec.LoadBalancerIP = tt.userIP
				if tt.annotation != "" {
					env.service.Annotations = map[string]string{ServiceAnnotationLoadBalancerIPAssociatedByController: tt.annotation}
				}
				env.expectRuleListing(t, []*cloudstack.LoadBalancerRule{tcpRule("rule-80", "80", "203.0.113.1", ipID)}, nil)
				env.expectRuleDeletion(t, "rule-80", ipID)
				tt.expect(env, t)

				err := env.cs.EnsureLoadBalancerDeleted(context.TODO(), testClusterName, env.service)

				if tt.wantErrorMsg == "" && err != nil {
					t.Errorf("EnsureLoadBalancerDeleted() unexpected error: %v", err)
				}
				if tt.wantErrorMsg != "" && (err == nil || err.Error() != tt.wantErrorMsg) {
					t.Errorf("EnsureLoadBalancerDeleted() error = %v, want %q", err, tt.wantErrorMsg)
				}
			})
		}
	}
}

// TestEnsureLoadBalancerDeletedWithUnparsableRule checks that a rule whose protocol or port the
// provider cannot parse does not fail the deletion: an unknown protocol leaves the rule alone, and
// an unreadable port deletes the rule without touching the firewall.
func TestEnsureLoadBalancerDeletedWithUnparsableRule(t *testing.T) {
	t.Run("unknown protocol leaves the rule alone", func(t *testing.T) {
		env := newEntryPointEnv(t, "")
		env.service.Spec.LoadBalancerIP = "203.0.113.1"
		rule := tcpRule("rule-1", "80", "203.0.113.1", "ip-1")
		rule.Protocol = "icmp"
		env.expectRuleListing(t, []*cloudstack.LoadBalancerRule{rule}, nil)

		if err := env.cs.EnsureLoadBalancerDeleted(context.TODO(), testClusterName, env.service); err != nil {
			t.Errorf("EnsureLoadBalancerDeleted() unexpected error: %v", err)
		}
	})

	t.Run("unreadable port deletes the rule without firewall cleanup", func(t *testing.T) {
		env := newEntryPointEnv(t, "")
		env.service.Spec.LoadBalancerIP = "203.0.113.1"
		env.expectRuleListing(t, []*cloudstack.LoadBalancerRule{tcpRule("rule-1", "http", "203.0.113.1", "ip-1")}, nil)
		env.lb.EXPECT().NewDeleteLoadBalancerRuleParams("rule-1").Return(&cloudstack.DeleteLoadBalancerRuleParams{})
		env.lb.EXPECT().DeleteLoadBalancerRule(gomock.Any()).Return(&cloudstack.DeleteLoadBalancerRuleResponse{}, nil)

		if err := env.cs.EnsureLoadBalancerDeleted(context.TODO(), testClusterName, env.service); err != nil {
			t.Errorf("EnsureLoadBalancerDeleted() unexpected error: %v", err)
		}
	})
}

// TestEnsureLoadBalancerDeletedFailures checks that a failing lookup is returned before any rule is deleted.
func TestEnsureLoadBalancerDeletedFailures(t *testing.T) {
	t.Run("rule listing fails", func(t *testing.T) {
		env := newEntryPointEnv(t, "")
		env.expectRuleListing(t, nil, errors.New("boom"))

		err := env.cs.EnsureLoadBalancerDeleted(context.TODO(), testClusterName, env.service)

		if err == nil || !strings.Contains(err.Error(), "error retrieving load balancer rules: boom") {
			t.Errorf("EnsureLoadBalancerDeleted() error = %v, want the listing error", err)
		}
	})

	t.Run("network of the rule cannot be looked up", func(t *testing.T) {
		env := newEntryPointEnv(t, "")
		env.expectRuleListing(t, []*cloudstack.LoadBalancerRule{tcpRule("rule-80", "80", "203.0.113.1", "ip-1")}, nil)
		env.network.EXPECT().GetNetworkByID("net-1", gomock.Any()).Return(nil, 0, errors.New("boom"))

		err := env.cs.EnsureLoadBalancerDeleted(context.TODO(), testClusterName, env.service)

		if err == nil || !strings.Contains(err.Error(), "boom") {
			t.Errorf("EnsureLoadBalancerDeleted() error = %v, want the network error", err)
		}
	})

	t.Run("deleting the rule fails", func(t *testing.T) {
		env := newEntryPointEnv(t, "")
		env.expectRuleListing(t, []*cloudstack.LoadBalancerRule{tcpRule("rule-80", "80", "203.0.113.1", "ip-1")}, nil)
		env.network.EXPECT().GetNetworkByID("net-1", gomock.Any()).Return(&cloudstack.Network{Id: "net-1"}, 1, nil)
		env.fw.EXPECT().NewListFirewallRulesParams().Return(&cloudstack.ListFirewallRulesParams{})
		env.fw.EXPECT().ListFirewallRules(gomock.Any()).Return(&cloudstack.ListFirewallRulesResponse{}, nil)
		env.lb.EXPECT().NewDeleteLoadBalancerRuleParams("rule-80").Return(&cloudstack.DeleteLoadBalancerRuleParams{})
		env.lb.EXPECT().DeleteLoadBalancerRule(gomock.Any()).Return(nil, errors.New("boom"))

		err := env.cs.EnsureLoadBalancerDeleted(context.TODO(), testClusterName, env.service)

		if err == nil || !strings.Contains(err.Error(), "error deleting load balancer rule atestuid-tcp-80: boom") {
			t.Errorf("EnsureLoadBalancerDeleted() error = %v, want the delete error", err)
		}
	})
}

// TestEnsureLoadBalancerRejectsBeforeCreatingAnything checks that a Service or lookup that cannot
// be served fails before an IP, rule or firewall entry is created: the mocks have no expectation
// for any of those calls, so one would fail the test.
func TestEnsureLoadBalancerRejectsBeforeCreatingAnything(t *testing.T) {
	http := corev1.ServicePort{Name: "http", Port: 80, NodePort: 30000, Protocol: corev1.ProtocolTCP}

	tests := []struct {
		name    string
		ports   []corev1.ServicePort
		setup   func(e *ensureLBTestEnv)
		wantErr string
	}{
		{
			name:    "no ports",
			setup:   func(e *ensureLBTestEnv) {},
			wantErr: "requested load balancer with no ports",
		},
		{
			name:  "rule listing fails",
			ports: []corev1.ServicePort{http},
			setup: func(e *ensureLBTestEnv) {
				e.lb.EXPECT().NewListLoadBalancerRulesParams().Return(&cloudstack.ListLoadBalancerRulesParams{})
				e.lb.EXPECT().ListLoadBalancerRules(gomock.Any()).Return(nil, errors.New("boom"))
			},
			wantErr: "error retrieving load balancer rules: boom",
		},
		{
			name:  "unsupported session affinity",
			ports: []corev1.ServicePort{http},
			setup: func(e *ensureLBTestEnv) {
				e.service.Spec.SessionAffinity = "Bogus"
				e.expectRules()
			},
			wantErr: "unsupported load balancer affinity: Bogus",
		},
		{
			name:  "nodes cannot be listed",
			ports: []corev1.ServicePort{http},
			setup: func(e *ensureLBTestEnv) {
				e.expectRules()
				e.vm.EXPECT().NewListVirtualMachinesParams().Return(&cloudstack.ListVirtualMachinesParams{})
				e.vm.EXPECT().ListVirtualMachines(gomock.Any()).Return(nil, errors.New("boom"))
			},
			wantErr: "error retrieving list of hosts: boom",
		},
		{
			name:  "network cannot be looked up",
			ports: []corev1.ServicePort{http},
			setup: func(e *ensureLBTestEnv) {
				e.expectRules()
				e.expectHosts()
				e.network.EXPECT().GetNetworkByID("net-1", gomock.Any()).Return(nil, 0, errors.New("boom"))
			},
			wantErr: "boom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			t.Cleanup(ctrl.Finish)
			env := newEnsureLBTestEnv(ctrl, nil, tt.ports)
			tt.setup(env)

			status, err := env.cs.EnsureLoadBalancer(context.TODO(), testClusterName, env.service, env.nodes)

			if status != nil || err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("EnsureLoadBalancer() = %v, %v, want an error containing %q", status, err, tt.wantErr)
			}
		})
	}
}

// TestEnsureLoadBalancerReportsHostnameAnnotation checks that a hostname annotation replaces the
// IP in the status Kubernetes publishes, while the rules are still created on the IP.
func TestEnsureLoadBalancerReportsHostnameAnnotation(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	http := corev1.ServicePort{Name: "http", Port: 80, NodePort: 30000, Protocol: corev1.ProtocolTCP}
	env := newEnsureLBTestEnv(ctrl, map[string]string{ServiceAnnotationLoadBalancerLoadbalancerHostname: "lb.example.com"}, []corev1.ServicePort{http})
	env.expectHosts()
	env.expectRules()
	env.network.EXPECT().GetNetworkByID("net-1", gomock.Any()).Return(virtualRouterNetwork(), 1, nil).Times(2)
	env.address.EXPECT().NewAssociateIpAddressParams().Return(&cloudstack.AssociateIpAddressParams{})
	env.address.EXPECT().AssociateIpAddress(gomock.Any()).Return(&cloudstack.AssociateIpAddressResponse{Id: "ip-new", Ipaddress: "10.0.0.9"}, nil)
	env.lb.EXPECT().NewCreateLoadBalancerRuleParams("roundrobin", "atestuid-tcp-80", 30000, 80).Return(&cloudstack.CreateLoadBalancerRuleParams{})
	env.lb.EXPECT().CreateLoadBalancerRule(gomock.Any()).Return(&cloudstack.CreateLoadBalancerRuleResponse{
		Id: "rule-new", Name: "atestuid-tcp-80", Publicip: "10.0.0.9", Publicipid: "ip-new", Protocol: "tcp",
	}, nil)
	env.lb.EXPECT().NewAssignToLoadBalancerRuleParams("rule-new").Return(&cloudstack.AssignToLoadBalancerRuleParams{})
	env.lb.EXPECT().AssignToLoadBalancerRule(gomock.Any()).Return(&cloudstack.AssignToLoadBalancerRuleResponse{}, nil)
	env.expectFirewallListings(&cloudstack.ListFirewallRulesResponse{}, 1)
	env.firewall.EXPECT().NewCreateFirewallRuleParams("ip-new", "tcp").Return(&cloudstack.CreateFirewallRuleParams{})
	env.firewall.EXPECT().CreateFirewallRule(gomock.Any()).Return(&cloudstack.CreateFirewallRuleResponse{}, nil)

	status, err := env.cs.EnsureLoadBalancer(context.TODO(), testClusterName, env.service, env.nodes)

	want := &corev1.LoadBalancerStatus{Ingress: []corev1.LoadBalancerIngress{{Hostname: "lb.example.com"}}}
	if err != nil || !reflect.DeepEqual(status, want) {
		t.Errorf("EnsureLoadBalancer() = %v, %v, want %v", status, err, want)
	}
}
