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
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/apache/cloudstack-go/v2/cloudstack"
	"github.com/blang/semver/v4"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCompareStringSlice(t *testing.T) {
	tests := []struct {
		name string
		x    []string
		y    []string
		want bool
	}{
		{
			name: "equal slices same order",
			x:    []string{"a", "b", "c"},
			y:    []string{"a", "b", "c"},
			want: true,
		},
		{
			name: "equal slices different order",
			x:    []string{"a", "b", "c"},
			y:    []string{"c", "a", "b"},
			want: true,
		},
		{
			name: "different lengths",
			x:    []string{"a", "b"},
			y:    []string{"a", "b", "c"},
			want: false,
		},
		{
			name: "same length different elements",
			x:    []string{"a", "b", "c"},
			y:    []string{"a", "b", "d"},
			want: false,
		},
		{
			name: "both empty",
			x:    []string{},
			y:    []string{},
			want: true,
		},
		{
			name: "both nil",
			x:    nil,
			y:    nil,
			want: true,
		},
		{
			name: "one nil one empty",
			x:    nil,
			y:    []string{},
			want: true,
		},
		{
			name: "one empty one non-empty",
			x:    []string{},
			y:    []string{"a"},
			want: false,
		},
		{
			name: "duplicate elements equal",
			x:    []string{"a", "a", "b"},
			y:    []string{"a", "b", "a"},
			want: true,
		},
		{
			name: "duplicate elements not equal - different counts",
			x:    []string{"a", "a", "b"},
			y:    []string{"a", "b", "b"},
			want: false,
		},
		{
			name: "single element equal",
			x:    []string{"a"},
			y:    []string{"a"},
			want: true,
		},
		{
			name: "single element not equal",
			x:    []string{"a"},
			y:    []string{"b"},
			want: false,
		},
		{
			name: "CIDR list comparison - typical use case",
			x:    []string{"10.0.0.0/8", "192.168.0.0/16"},
			y:    []string{"192.168.0.0/16", "10.0.0.0/8"},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := compareStringSlice(tt.x, tt.y); got != tt.want {
				t.Errorf("compareStringSlice(%v, %v) = %v, want %v", tt.x, tt.y, got, tt.want)
			}
		})
	}
}

func TestSymmetricDifference(t *testing.T) {
	tests := []struct {
		name        string
		hostIDs     []string
		lbInstances []*cloudstack.VirtualMachine
		wantAssign  []string
		wantRemove  []string
	}{
		{
			name:        "no hosts no instances",
			hostIDs:     []string{},
			lbInstances: []*cloudstack.VirtualMachine{},
			wantAssign:  nil,
			wantRemove:  nil,
		},
		{
			name:        "all new hosts",
			hostIDs:     []string{"host1", "host2", "host3"},
			lbInstances: []*cloudstack.VirtualMachine{},
			wantAssign:  []string{"host1", "host2", "host3"},
			wantRemove:  nil,
		},
		{
			name:    "all hosts to remove",
			hostIDs: []string{},
			lbInstances: []*cloudstack.VirtualMachine{
				{Id: "host1"},
				{Id: "host2"},
			},
			wantAssign: nil,
			wantRemove: []string{"host1", "host2"},
		},
		{
			name:    "exact match - nothing to do",
			hostIDs: []string{"host1", "host2"},
			lbInstances: []*cloudstack.VirtualMachine{
				{Id: "host1"},
				{Id: "host2"},
			},
			wantAssign: nil,
			wantRemove: nil,
		},
		{
			name:    "partial overlap - some to add some to remove",
			hostIDs: []string{"host1", "host3"},
			lbInstances: []*cloudstack.VirtualMachine{
				{Id: "host1"},
				{Id: "host2"},
			},
			wantAssign: []string{"host3"},
			wantRemove: []string{"host2"},
		},
		{
			name:    "add one host",
			hostIDs: []string{"host1", "host2", "host3"},
			lbInstances: []*cloudstack.VirtualMachine{
				{Id: "host1"},
				{Id: "host2"},
			},
			wantAssign: []string{"host3"},
			wantRemove: nil,
		},
		{
			name:    "remove one host",
			hostIDs: []string{"host1"},
			lbInstances: []*cloudstack.VirtualMachine{
				{Id: "host1"},
				{Id: "host2"},
			},
			wantAssign: nil,
			wantRemove: []string{"host2"},
		},
		{
			name:        "nil instances",
			hostIDs:     []string{"host1"},
			lbInstances: nil,
			wantAssign:  []string{"host1"},
			wantRemove:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAssign, gotRemove := symmetricDifference(tt.hostIDs, tt.lbInstances)

			// Sort slices for comparison since map iteration order is not guaranteed
			sort.Strings(gotAssign)
			sort.Strings(tt.wantAssign)
			sort.Strings(gotRemove)
			sort.Strings(tt.wantRemove)

			if !compareStringSlice(gotAssign, tt.wantAssign) {
				t.Errorf("symmetricDifference() assign = %v, want %v", gotAssign, tt.wantAssign)
			}
			if !compareStringSlice(gotRemove, tt.wantRemove) {
				t.Errorf("symmetricDifference() remove = %v, want %v", gotRemove, tt.wantRemove)
			}
		})
	}
}

func TestIsFirewallSupported(t *testing.T) {
	tests := []struct {
		name     string
		services []cloudstack.NetworkServiceInternal
		want     bool
	}{
		{
			name:     "empty services",
			services: []cloudstack.NetworkServiceInternal{},
			want:     false,
		},
		{
			name:     "nil services",
			services: nil,
			want:     false,
		},
		{
			name: "firewall present",
			services: []cloudstack.NetworkServiceInternal{
				{Name: "Dhcp"},
				{Name: "Firewall"},
				{Name: "Dns"},
			},
			want: true,
		},
		{
			name: "firewall not present",
			services: []cloudstack.NetworkServiceInternal{
				{Name: "Dhcp"},
				{Name: "Dns"},
				{Name: "Lb"},
			},
			want: false,
		},
		{
			name: "only firewall",
			services: []cloudstack.NetworkServiceInternal{
				{Name: "Firewall"},
			},
			want: true,
		},
		{
			name: "case sensitive - lowercase firewall",
			services: []cloudstack.NetworkServiceInternal{
				{Name: "firewall"},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isFirewallSupported(tt.services); got != tt.want {
				t.Errorf("isFirewallSupported() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsNetworkACLSupported(t *testing.T) {
	tests := []struct {
		name     string
		services []cloudstack.NetworkServiceInternal
		want     bool
	}{
		{
			name:     "empty services",
			services: []cloudstack.NetworkServiceInternal{},
			want:     false,
		},
		{
			name:     "nil services",
			services: nil,
			want:     false,
		},
		{
			name: "NetworkACL present",
			services: []cloudstack.NetworkServiceInternal{
				{Name: "Dhcp"},
				{Name: "NetworkACL"},
				{Name: "Dns"},
			},
			want: true,
		},
		{
			name: "NetworkACL not present",
			services: []cloudstack.NetworkServiceInternal{
				{Name: "Dhcp"},
				{Name: "Dns"},
				{Name: "Firewall"},
			},
			want: false,
		},
		{
			name: "only NetworkACL",
			services: []cloudstack.NetworkServiceInternal{
				{Name: "NetworkACL"},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNetworkACLSupported(tt.services); got != tt.want {
				t.Errorf("isNetworkACLSupported() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetStringFromServiceAnnotation(t *testing.T) {
	tests := []struct {
		name           string
		annotations    map[string]string
		annotationKey  string
		defaultSetting string
		want           string
	}{
		{
			name:           "annotation present",
			annotations:    map[string]string{"key1": "value1"},
			annotationKey:  "key1",
			defaultSetting: "default",
			want:           "value1",
		},
		{
			name:           "annotation not present - use default",
			annotations:    map[string]string{"other": "value"},
			annotationKey:  "key1",
			defaultSetting: "default",
			want:           "default",
		},
		{
			name:           "annotation present but empty - return empty",
			annotations:    map[string]string{"key1": ""},
			annotationKey:  "key1",
			defaultSetting: "default",
			want:           "",
		},
		{
			name:           "nil annotations - use default",
			annotations:    nil,
			annotationKey:  "key1",
			defaultSetting: "default",
			want:           "default",
		},
		{
			name:           "empty default when not found",
			annotations:    map[string]string{},
			annotationKey:  "key1",
			defaultSetting: "",
			want:           "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-service",
					Namespace:   "default",
					Annotations: tt.annotations,
				},
			}
			if got := getStringFromServiceAnnotation(service, tt.annotationKey, tt.defaultSetting); got != tt.want {
				t.Errorf("getStringFromServiceAnnotation() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetBoolFromServiceAnnotation(t *testing.T) {
	tests := []struct {
		name           string
		annotations    map[string]string
		annotationKey  string
		defaultSetting bool
		want           bool
	}{
		{
			name:           "annotation true",
			annotations:    map[string]string{"key1": "true"},
			annotationKey:  "key1",
			defaultSetting: false,
			want:           true,
		},
		{
			name:           "annotation false",
			annotations:    map[string]string{"key1": "false"},
			annotationKey:  "key1",
			defaultSetting: true,
			want:           false,
		},
		{
			name:           "annotation not present - use default true",
			annotations:    map[string]string{},
			annotationKey:  "key1",
			defaultSetting: true,
			want:           true,
		},
		{
			name:           "annotation not present - use default false",
			annotations:    map[string]string{},
			annotationKey:  "key1",
			defaultSetting: false,
			want:           false,
		},
		{
			name:           "invalid value - use default true",
			annotations:    map[string]string{"key1": "invalid"},
			annotationKey:  "key1",
			defaultSetting: true,
			want:           true,
		},
		{
			name:           "invalid value - use default false",
			annotations:    map[string]string{"key1": "yes"},
			annotationKey:  "key1",
			defaultSetting: false,
			want:           false,
		},
		{
			name:           "empty value - use default",
			annotations:    map[string]string{"key1": ""},
			annotationKey:  "key1",
			defaultSetting: true,
			want:           true,
		},
		{
			name:           "nil annotations - use default",
			annotations:    nil,
			annotationKey:  "key1",
			defaultSetting: true,
			want:           true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-service",
					Namespace:   "default",
					Annotations: tt.annotations,
				},
			}
			if got := getBoolFromServiceAnnotation(service, tt.annotationKey, tt.defaultSetting); got != tt.want {
				t.Errorf("getBoolFromServiceAnnotation() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetCIDRList(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		want        []string
		wantErr     bool
		errContains string
		expectEmpty bool
	}{
		{
			name:        "defaults to allow all when annotation missing",
			annotations: nil,
			want:        []string{defaultAllowedCIDR},
		},
		{
			name: "trims and splits cidrs",
			annotations: map[string]string{
				ServiceAnnotationLoadBalancerSourceCidrs: "10.0.0.0/8, 192.168.0.0/16",
			},
			want: []string{"10.0.0.0/8", "192.168.0.0/16"},
		},
		{
			name: "empty annotation returns empty list",
			annotations: map[string]string{
				ServiceAnnotationLoadBalancerSourceCidrs: "",
			},
			expectEmpty: true,
		},
		{
			name: "invalid cidr returns error",
			annotations: map[string]string{
				ServiceAnnotationLoadBalancerSourceCidrs: "invalid-cidr",
			},
			wantErr:     true,
			errContains: "invalid CIDR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lb := &loadBalancer{}
			svc := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "svc",
					Namespace:   "default",
					Annotations: tt.annotations,
				},
			}

			got, err := lb.getCIDRList(svc)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Fatalf("error = %v, expected to contain %q", err, tt.errContains)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.expectEmpty {
				if len(got) != 0 {
					t.Fatalf("expected empty CIDR list, got %v", got)
				}
				return
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("getCIDRList() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCheckLoadBalancerRule(t *testing.T) {
	t.Run("rule not present returns nil", func(t *testing.T) {
		lb := &loadBalancer{
			rules: map[string]*cloudstack.LoadBalancerRule{},
		}
		port := corev1.ServicePort{Port: 80, NodePort: 30000, Protocol: corev1.ProtocolTCP}
		service := &corev1.Service{}

		rule, needsUpdate, err := lb.checkLoadBalancerRule("missing", port, LoadBalancerProtocolTCP, service, semver.Version{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rule != nil {
			t.Fatalf("expected nil rule, got %v", rule)
		}
		if needsUpdate {
			t.Fatalf("expected needsUpdate to be false")
		}
	})

	t.Run("basic property mismatch deletes rule", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)
		deleteParams := &cloudstack.DeleteLoadBalancerRuleParams{}

		gomock.InOrder(
			mockLB.EXPECT().NewDeleteLoadBalancerRuleParams("rule-id").Return(deleteParams),
			mockLB.EXPECT().DeleteLoadBalancerRule(deleteParams).Return(&cloudstack.DeleteLoadBalancerRuleResponse{}, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				LoadBalancer: mockLB,
			},
			ipAddr: "1.1.1.1",
			rules: map[string]*cloudstack.LoadBalancerRule{
				"rule": {
					Id:          "rule-id",
					Name:        "rule",
					Publicip:    "2.2.2.2",
					Privateport: "30000",
					Publicport:  "80",
					Cidrlist:    defaultAllowedCIDR,
					Algorithm:   "roundrobin",
					Protocol:    LoadBalancerProtocolTCP.CSProtocol(),
				},
			},
		}
		port := corev1.ServicePort{Port: 80, NodePort: 30000, Protocol: corev1.ProtocolTCP}
		service := &corev1.Service{}

		rule, needsUpdate, err := lb.checkLoadBalancerRule("rule", port, LoadBalancerProtocolTCP, service, semver.Version{Major: 4, Minor: 21, Patch: 0})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rule != nil {
			t.Fatalf("expected nil rule after deletion, got %v", rule)
		}
		if needsUpdate {
			t.Fatalf("expected needsUpdate to be false")
		}
		if _, exists := lb.rules["rule"]; exists {
			t.Fatalf("expected rule entry to be removed from map")
		}
	})

	t.Run("cidr change triggers update on supported version", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		// No expectations on the mock; any delete call would fail the test.
		mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)

		lbRule := &cloudstack.LoadBalancerRule{
			Id:          "rule-id",
			Name:        "rule",
			Publicip:    "1.1.1.1",
			Privateport: "30000",
			Publicport:  "80",
			Cidrlist:    "10.0.0.0/8",
			Algorithm:   "roundrobin",
			Protocol:    LoadBalancerProtocolTCP.CSProtocol(),
		}

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				LoadBalancer: mockLB,
			},
			ipAddr:    "1.1.1.1",
			algorithm: "roundrobin",
			rules: map[string]*cloudstack.LoadBalancerRule{
				"rule": lbRule,
			},
		}
		port := corev1.ServicePort{Port: 80, NodePort: 30000, Protocol: corev1.ProtocolTCP}
		service := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					ServiceAnnotationLoadBalancerSourceCidrs: "10.0.0.0/8,192.168.0.0/16",
				},
			},
		}

		rule, needsUpdate, err := lb.checkLoadBalancerRule("rule", port, LoadBalancerProtocolTCP, service, semver.Version{Major: 4, Minor: 22, Patch: 0})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rule != lbRule {
			t.Fatalf("expected existing rule to be returned")
		}
		if !needsUpdate {
			t.Fatalf("expected needsUpdate to be true due to CIDR change")
		}
	})

	t.Run("cidr change triggers delete with older version", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		// No expectations on the mock; any delete or create call would fail the test.
		mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)

		deleteParams := &cloudstack.DeleteLoadBalancerRuleParams{}

		gomock.InOrder(
			mockLB.EXPECT().NewDeleteLoadBalancerRuleParams("rule-id").Return(deleteParams),
			mockLB.EXPECT().DeleteLoadBalancerRule(deleteParams).Return(&cloudstack.DeleteLoadBalancerRuleResponse{}, nil),
		)

		lbRule := &cloudstack.LoadBalancerRule{
			Id:          "rule-id",
			Name:        "rule",
			Publicip:    "1.1.1.1",
			Privateport: "30000",
			Publicport:  "80",
			Cidrlist:    "10.0.0.0/8",
			Algorithm:   "roundrobin",
			Protocol:    LoadBalancerProtocolTCP.CSProtocol(),
		}

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				LoadBalancer: mockLB,
			},
			ipAddr:    "1.1.1.1",
			algorithm: "roundrobin",
			rules: map[string]*cloudstack.LoadBalancerRule{
				"rule": lbRule,
			},
		}
		port := corev1.ServicePort{Port: 80, NodePort: 30000, Protocol: corev1.ProtocolTCP}
		service := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					ServiceAnnotationLoadBalancerSourceCidrs: "10.0.0.0/8,192.168.0.0/16",
				},
			},
		}

		rule, needsUpdate, err := lb.checkLoadBalancerRule("rule", port, LoadBalancerProtocolTCP, service, semver.Version{Major: 4, Minor: 12, Patch: 0})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rule != nil {
			t.Fatalf("expected nil rule after deletion, got %v", rule)
		}
		if needsUpdate {
			t.Fatalf("expected needsUpdate to be false due to CIDR change with older version")
		}
	})

	t.Run("invalid cidr returns error", func(t *testing.T) {
		lb := &loadBalancer{
			rules: map[string]*cloudstack.LoadBalancerRule{
				"rule": {
					Id:          "rule-id",
					Name:        "rule",
					Publicip:    "1.1.1.1",
					Privateport: "30000",
					Publicport:  "80",
					Cidrlist:    defaultAllowedCIDR,
					Algorithm:   "roundrobin",
					Protocol:    LoadBalancerProtocolTCP.CSProtocol(),
				},
			},
		}
		port := corev1.ServicePort{Port: 80, NodePort: 30000, Protocol: corev1.ProtocolTCP}
		service := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					ServiceAnnotationLoadBalancerSourceCidrs: "bad-cidr",
				},
			},
		}

		_, _, err := lb.checkLoadBalancerRule("rule", port, LoadBalancerProtocolTCP, service, semver.Version{Major: 4, Minor: 22, Patch: 0})
		if err == nil {
			t.Fatalf("expected error for invalid CIDR")
		}
	})
}

func TestRuleToString(t *testing.T) {
	tests := []struct {
		name string
		rule *cloudstack.FirewallRule
		want string
	}{
		{
			name: "TCP rule",
			rule: &cloudstack.FirewallRule{
				Protocol:  "tcp",
				Cidrlist:  "10.0.0.0/8",
				Ipaddress: "203.0.113.1",
				Startport: 80,
				Endport:   80,
			},
			want: "{[10.0.0.0/8] -> 203.0.113.1:[80-80] (tcp)}",
		},
		{
			name: "UDP rule",
			rule: &cloudstack.FirewallRule{
				Protocol:  "udp",
				Cidrlist:  "192.168.0.0/16",
				Ipaddress: "203.0.113.2",
				Startport: 53,
				Endport:   53,
			},
			want: "{[192.168.0.0/16] -> 203.0.113.2:[53-53] (udp)}",
		},
		{
			name: "TCP rule with port range",
			rule: &cloudstack.FirewallRule{
				Protocol:  "tcp",
				Cidrlist:  "0.0.0.0/0",
				Ipaddress: "203.0.113.3",
				Startport: 8000,
				Endport:   8999,
			},
			want: "{[0.0.0.0/0] -> 203.0.113.3:[8000-8999] (tcp)}",
		},
		{
			name: "ICMP rule",
			rule: &cloudstack.FirewallRule{
				Protocol:  "icmp",
				Cidrlist:  "10.0.0.0/8",
				Ipaddress: "203.0.113.4",
				Icmptype:  8,
				Icmpcode:  0,
			},
			want: "{[10.0.0.0/8] -> 203.0.113.4 [8,0] (icmp)}",
		},
		{
			name: "unknown protocol",
			rule: &cloudstack.FirewallRule{
				Protocol:  "gre",
				Cidrlist:  "10.0.0.0/8",
				Ipaddress: "203.0.113.6",
			},
			want: "{[10.0.0.0/8] -> 203.0.113.6 (gre)}",
		},
		{
			name: "nil rule",
			rule: nil,
			want: "nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ruleToString(tt.rule)
			if got != tt.want {
				t.Errorf("ruleToString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRulesToString(t *testing.T) {
	tests := []struct {
		name  string
		rules []*cloudstack.FirewallRule
		want  string
	}{
		{
			name:  "empty list",
			rules: []*cloudstack.FirewallRule{},
			want:  "",
		},
		{
			name: "single rule",
			rules: []*cloudstack.FirewallRule{
				{
					Protocol:  "tcp",
					Cidrlist:  "10.0.0.0/8",
					Ipaddress: "203.0.113.1",
					Startport: 80,
					Endport:   80,
				},
			},
			want: "{[10.0.0.0/8] -> 203.0.113.1:[80-80] (tcp)}",
		},
		{
			name: "multiple rules",
			rules: []*cloudstack.FirewallRule{
				{
					Protocol:  "tcp",
					Cidrlist:  "10.0.0.0/8",
					Ipaddress: "203.0.113.1",
					Startport: 80,
					Endport:   80,
				},
				{
					Protocol:  "udp",
					Cidrlist:  "192.168.0.0/16",
					Ipaddress: "203.0.113.2",
					Startport: 53,
					Endport:   53,
				},
				{
					Protocol:  "icmp",
					Cidrlist:  "0.0.0.0/0",
					Ipaddress: "203.0.113.3",
					Icmptype:  8,
					Icmpcode:  0,
				},
			},
			want: "{[10.0.0.0/8] -> 203.0.113.1:[80-80] (tcp)}, {[192.168.0.0/16] -> 203.0.113.2:[53-53] (udp)}, {[0.0.0.0/0] -> 203.0.113.3 [8,0] (icmp)}",
		},
		{
			name: "rules with nil rule",
			rules: []*cloudstack.FirewallRule{
				{
					Protocol:  "tcp",
					Cidrlist:  "10.0.0.0/8",
					Ipaddress: "203.0.113.1",
					Startport: 80,
					Endport:   80,
				},
				nil,
				{
					Protocol:  "udp",
					Cidrlist:  "192.168.0.0/16",
					Ipaddress: "203.0.113.2",
					Startport: 53,
					Endport:   53,
				},
			},
			want: "{[10.0.0.0/8] -> 203.0.113.1:[80-80] (tcp)}, nil, {[192.168.0.0/16] -> 203.0.113.2:[53-53] (udp)}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rulesToString(tt.rules)
			if got != tt.want {
				t.Errorf("rulesToString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRulesMapToString(t *testing.T) {
	tests := []struct {
		name  string
		rules map[*cloudstack.FirewallRule]bool
		want  string
	}{
		{
			name:  "empty map",
			rules: map[*cloudstack.FirewallRule]bool{},
			want:  "",
		},
		{
			name: "single rule",
			rules: map[*cloudstack.FirewallRule]bool{
				{
					Protocol:  "tcp",
					Cidrlist:  "10.0.0.0/8",
					Ipaddress: "203.0.113.1",
					Startport: 80,
					Endport:   80,
				}: true,
			},
			want: "{[10.0.0.0/8] -> 203.0.113.1:[80-80] (tcp)}",
		},
		{
			name: "multiple rules",
			rules: map[*cloudstack.FirewallRule]bool{
				{
					Protocol:  "tcp",
					Cidrlist:  "10.0.0.0/8",
					Ipaddress: "203.0.113.1",
					Startport: 80,
					Endport:   80,
				}: true,
				{
					Protocol:  "udp",
					Cidrlist:  "192.168.0.0/16",
					Ipaddress: "203.0.113.2",
					Startport: 53,
					Endport:   53,
				}: false,
				{
					Protocol:  "icmp",
					Cidrlist:  "0.0.0.0/0",
					Ipaddress: "203.0.113.3",
					Icmptype:  8,
					Icmpcode:  0,
				}: true,
			},
			// Note: Map iteration order is non-deterministic, so we need to check
			// that all rules are present, not the exact order
			want: "", // We'll check this differently
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rulesMapToString(tt.rules)

			if tt.want == "" {
				// For maps, we can't predict order, so check that all rules are present
				if len(tt.rules) == 0 {
					if got != "" {
						t.Errorf("rulesMapToString() = %q, want empty string", got)
					}
					return
				}

				// Check that all rules are present in the output
				expectedRules := make([]string, 0, len(tt.rules))
				for rule := range tt.rules {
					expectedRules = append(expectedRules, ruleToString(rule))
				}

				// Split the output and check each rule is present
				if got != "" {
					parts := strings.Split(got, ", ")
					if len(parts) != len(expectedRules) {
						t.Errorf("rulesMapToString() returned %d rules, want %d", len(parts), len(expectedRules))
						return
					}

					// Check that all expected rules are in the output
					for _, expectedRule := range expectedRules {
						found := false
						for _, part := range parts {
							if part == expectedRule {
								found = true
								break
							}
						}
						if !found {
							t.Errorf("rulesMapToString() missing rule %q in output %q", expectedRule, got)
						}
					}
				} else if len(expectedRules) > 0 {
					t.Errorf("rulesMapToString() = empty string, want rules to be present")
				}
			} else {
				if got != tt.want {
					t.Errorf("rulesMapToString() = %q, want %q", got, tt.want)
				}
			}
		})
	}
}

func TestGetPublicIPAddress(t *testing.T) {
	t.Run("IP found and allocated", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockAddress := cloudstack.NewMockAddressServiceIface(ctrl)
		listParams := &cloudstack.ListPublicIpAddressesParams{}
		resp := &cloudstack.ListPublicIpAddressesResponse{
			Count: 1,
			PublicIpAddresses: []*cloudstack.PublicIpAddress{
				{
					Id:        "ip-123",
					Ipaddress: "203.0.113.1",
					Allocated: "2023-01-01T00:00:00+0000",
				},
			},
		}

		gomock.InOrder(
			mockAddress.EXPECT().NewListPublicIpAddressesParams().Return(listParams),
			mockAddress.EXPECT().ListPublicIpAddresses(gomock.Any()).Return(resp, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Address: mockAddress,
			},
		}

		err := lb.getPublicIPAddress("203.0.113.1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if lb.ipAddr != "203.0.113.1" {
			t.Errorf("ipAddr = %q, want %q", lb.ipAddr, "203.0.113.1")
		}
		if lb.ipAddrID != "ip-123" {
			t.Errorf("ipAddrID = %q, want %q", lb.ipAddrID, "ip-123")
		}
	})

	t.Run("IP found but not allocated - associates", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockAddress := cloudstack.NewMockAddressServiceIface(ctrl)
		mockNetwork := cloudstack.NewMockNetworkServiceIface(ctrl)
		listParams := &cloudstack.ListPublicIpAddressesParams{}
		resp := &cloudstack.ListPublicIpAddressesResponse{
			Count: 1,
			PublicIpAddresses: []*cloudstack.PublicIpAddress{
				{
					Id:        "ip-123",
					Ipaddress: "203.0.113.1",
					Allocated: "",
				},
			},
		}

		networkResp := &cloudstack.Network{
			Id:      "net-123",
			Vpcid:   "",
			Service: []cloudstack.NetworkServiceInternal{},
		}

		associateParams := &cloudstack.AssociateIpAddressParams{}
		associateResp := &cloudstack.AssociateIpAddressResponse{
			Id:        "ip-123",
			Ipaddress: "203.0.113.1",
		}

		gomock.InOrder(
			mockAddress.EXPECT().NewListPublicIpAddressesParams().Return(listParams),
			mockAddress.EXPECT().ListPublicIpAddresses(gomock.Any()).Return(resp, nil),
			mockNetwork.EXPECT().GetNetworkByID("net-123", gomock.Any()).Return(networkResp, 1, nil),
			mockAddress.EXPECT().NewAssociateIpAddressParams().Return(associateParams),
			mockAddress.EXPECT().AssociateIpAddress(gomock.Any()).Return(associateResp, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Address: mockAddress,
				Network: mockNetwork,
			},
			networkID: "net-123",
			ipAddr:    "203.0.113.1",
		}

		err := lb.getPublicIPAddress("203.0.113.1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if lb.ipAddr != "203.0.113.1" {
			t.Errorf("ipAddr = %q, want %q", lb.ipAddr, "203.0.113.1")
		}
		if lb.ipAddrID != "ip-123" {
			t.Errorf("ipAddrID = %q, want %q", lb.ipAddrID, "ip-123")
		}
	})

	t.Run("IP not found", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockAddress := cloudstack.NewMockAddressServiceIface(ctrl)
		listParams := &cloudstack.ListPublicIpAddressesParams{}
		resp := &cloudstack.ListPublicIpAddressesResponse{
			Count:             0,
			PublicIpAddresses: []*cloudstack.PublicIpAddress{},
		}

		gomock.InOrder(
			mockAddress.EXPECT().NewListPublicIpAddressesParams().Return(listParams),
			mockAddress.EXPECT().ListPublicIpAddresses(gomock.Any()).Return(resp, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Address: mockAddress,
			},
		}

		err := lb.getPublicIPAddress("203.0.113.1")
		if err == nil {
			t.Fatalf("expected error for IP not found")
		}
		if !strings.Contains(err.Error(), "could not find IP address") {
			t.Errorf("error message = %q, want to contain 'could not find IP address'", err.Error())
		}
	})

	t.Run("multiple IPs found", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockAddress := cloudstack.NewMockAddressServiceIface(ctrl)
		listParams := &cloudstack.ListPublicIpAddressesParams{}
		resp := &cloudstack.ListPublicIpAddressesResponse{
			Count: 2,
			PublicIpAddresses: []*cloudstack.PublicIpAddress{
				{Id: "ip-1", Ipaddress: "203.0.113.1"},
				{Id: "ip-2", Ipaddress: "203.0.113.1"},
			},
		}

		gomock.InOrder(
			mockAddress.EXPECT().NewListPublicIpAddressesParams().Return(listParams),
			mockAddress.EXPECT().ListPublicIpAddresses(gomock.Any()).Return(resp, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Address: mockAddress,
			},
		}

		err := lb.getPublicIPAddress("203.0.113.1")
		if err == nil {
			t.Fatalf("expected error for multiple IPs found")
		}
		if !strings.Contains(err.Error(), "Found 2 addresses") {
			t.Errorf("error message = %q, want to contain 'Found 2 addresses'", err.Error())
		}
	})

	t.Run("error retrieving IP", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockAddress := cloudstack.NewMockAddressServiceIface(ctrl)
		listParams := &cloudstack.ListPublicIpAddressesParams{}
		apiErr := fmt.Errorf("API error")

		gomock.InOrder(
			mockAddress.EXPECT().NewListPublicIpAddressesParams().Return(listParams),
			mockAddress.EXPECT().ListPublicIpAddresses(gomock.Any()).Return(nil, apiErr),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Address: mockAddress,
			},
		}

		err := lb.getPublicIPAddress("203.0.113.1")
		if err == nil {
			t.Fatalf("expected error")
		}
		if !strings.Contains(err.Error(), "error retrieving IP address") {
			t.Errorf("error message = %q, want to contain 'error retrieving IP address'", err.Error())
		}
	})

	t.Run("project ID handling", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockAddress := cloudstack.NewMockAddressServiceIface(ctrl)
		listParams := &cloudstack.ListPublicIpAddressesParams{}
		resp := &cloudstack.ListPublicIpAddressesResponse{
			Count: 1,
			PublicIpAddresses: []*cloudstack.PublicIpAddress{
				{
					Id:        "ip-123",
					Ipaddress: "203.0.113.1",
					Allocated: "2023-01-01T00:00:00+0000",
				},
			},
		}

		gomock.InOrder(
			mockAddress.EXPECT().NewListPublicIpAddressesParams().Return(listParams),
			mockAddress.EXPECT().ListPublicIpAddresses(gomock.Any()).Return(resp, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Address: mockAddress,
			},
			projectID: "proj-123",
		}

		err := lb.getPublicIPAddress("203.0.113.1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if lb.ipAddr != "203.0.113.1" {
			t.Errorf("ipAddr = %q, want %q", lb.ipAddr, "203.0.113.1")
		}
		if lb.ipAddrID != "ip-123" {
			t.Errorf("ipAddrID = %q, want %q", lb.ipAddrID, "ip-123")
		}
	})
}

func TestAssociatePublicIPAddress(t *testing.T) {
	t.Run("associate IP for regular network", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockAddress := cloudstack.NewMockAddressServiceIface(ctrl)
		mockNetwork := cloudstack.NewMockNetworkServiceIface(ctrl)
		networkResp := &cloudstack.Network{
			Id:      "net-123",
			Vpcid:   "",
			Service: []cloudstack.NetworkServiceInternal{},
		}

		associateParams := &cloudstack.AssociateIpAddressParams{}
		associateResp := &cloudstack.AssociateIpAddressResponse{
			Id:        "ip-123",
			Ipaddress: "203.0.113.1",
		}

		gomock.InOrder(
			mockNetwork.EXPECT().GetNetworkByID("net-123", gomock.Any()).Return(networkResp, 1, nil),
			mockAddress.EXPECT().NewAssociateIpAddressParams().Return(associateParams),
			mockAddress.EXPECT().AssociateIpAddress(gomock.Any()).Return(associateResp, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Address: mockAddress,
				Network: mockNetwork,
			},
			networkID: "net-123",
		}

		err := lb.associatePublicIPAddress()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if lb.ipAddr != "203.0.113.1" {
			t.Errorf("ipAddr = %q, want %q", lb.ipAddr, "203.0.113.1")
		}
		if lb.ipAddrID != "ip-123" {
			t.Errorf("ipAddrID = %q, want %q", lb.ipAddrID, "ip-123")
		}
		if !lb.ipAssociatedByController {
			t.Errorf("ipAssociatedByController = false, want true")
		}
	})

	t.Run("associate IP for VPC network", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockAddress := cloudstack.NewMockAddressServiceIface(ctrl)
		mockNetwork := cloudstack.NewMockNetworkServiceIface(ctrl)
		networkResp := &cloudstack.Network{
			Id:      "net-123",
			Vpcid:   "vpc-456",
			Service: []cloudstack.NetworkServiceInternal{},
		}

		associateParams := &cloudstack.AssociateIpAddressParams{}
		associateResp := &cloudstack.AssociateIpAddressResponse{
			Id:        "ip-123",
			Ipaddress: "203.0.113.1",
		}

		gomock.InOrder(
			mockNetwork.EXPECT().GetNetworkByID("net-123", gomock.Any()).Return(networkResp, 1, nil),
			mockAddress.EXPECT().NewAssociateIpAddressParams().Return(associateParams),
			mockAddress.EXPECT().AssociateIpAddress(gomock.Any()).Return(associateResp, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Address: mockAddress,
				Network: mockNetwork,
			},
			networkID: "net-123",
		}

		err := lb.associatePublicIPAddress()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if lb.ipAddr != "203.0.113.1" {
			t.Errorf("ipAddr = %q, want %q", lb.ipAddr, "203.0.113.1")
		}
		if lb.ipAddrID != "ip-123" {
			t.Errorf("ipAddrID = %q, want %q", lb.ipAddrID, "ip-123")
		}
		if !lb.ipAssociatedByController {
			t.Errorf("ipAssociatedByController = false, want true")
		}
	})

	t.Run("error retrieving network", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockNetwork := cloudstack.NewMockNetworkServiceIface(ctrl)
		apiErr := fmt.Errorf("network API error")

		mockNetwork.EXPECT().GetNetworkByID("net-123", gomock.Any()).Return(nil, 1, apiErr)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Network: mockNetwork,
			},
			networkID: "net-123",
		}

		err := lb.associatePublicIPAddress()
		if err == nil {
			t.Fatalf("expected error")
		}
		if !strings.Contains(err.Error(), "error retrieving network") {
			t.Errorf("error message = %q, want to contain 'error retrieving network'", err.Error())
		}
	})

	t.Run("network not found", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockNetwork := cloudstack.NewMockNetworkServiceIface(ctrl)

		mockNetwork.EXPECT().GetNetworkByID("net-123", gomock.Any()).Return(nil, 0, fmt.Errorf("not found"))

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Network: mockNetwork,
			},
			networkID: "net-123",
		}

		err := lb.associatePublicIPAddress()
		if err == nil {
			t.Fatalf("expected error")
		}
		if !strings.Contains(err.Error(), "could not find network") {
			t.Errorf("error message = %q, want to contain 'could not find network'", err.Error())
		}
	})

	t.Run("error associating IP", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockAddress := cloudstack.NewMockAddressServiceIface(ctrl)
		mockNetwork := cloudstack.NewMockNetworkServiceIface(ctrl)
		networkResp := &cloudstack.Network{
			Id:      "net-123",
			Vpcid:   "",
			Service: []cloudstack.NetworkServiceInternal{},
		}

		associateParams := &cloudstack.AssociateIpAddressParams{}
		apiErr := fmt.Errorf("associate API error")

		gomock.InOrder(
			mockNetwork.EXPECT().GetNetworkByID("net-123", gomock.Any()).Return(networkResp, 1, nil),
			mockAddress.EXPECT().NewAssociateIpAddressParams().Return(associateParams),
			mockAddress.EXPECT().AssociateIpAddress(gomock.Any()).Return(nil, apiErr),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Address: mockAddress,
				Network: mockNetwork,
			},
			networkID: "net-123",
		}

		err := lb.associatePublicIPAddress()
		if err == nil {
			t.Fatalf("expected error")
		}
		if !strings.Contains(err.Error(), "error associating new IP address") {
			t.Errorf("error message = %q, want to contain 'error associating new IP address'", err.Error())
		}
	})

	t.Run("project ID handling", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockAddress := cloudstack.NewMockAddressServiceIface(ctrl)
		mockNetwork := cloudstack.NewMockNetworkServiceIface(ctrl)
		networkResp := &cloudstack.Network{
			Id:      "net-123",
			Vpcid:   "",
			Service: []cloudstack.NetworkServiceInternal{},
		}

		associateParams := &cloudstack.AssociateIpAddressParams{}
		associateResp := &cloudstack.AssociateIpAddressResponse{
			Id:        "ip-123",
			Ipaddress: "203.0.113.1",
		}

		gomock.InOrder(
			mockNetwork.EXPECT().GetNetworkByID("net-123", gomock.Any()).Return(networkResp, 1, nil),
			mockAddress.EXPECT().NewAssociateIpAddressParams().Return(associateParams),
			mockAddress.EXPECT().AssociateIpAddress(gomock.Any()).Return(associateResp, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Address: mockAddress,
				Network: mockNetwork,
			},
			networkID: "net-123",
			projectID: "proj-123",
		}

		err := lb.associatePublicIPAddress()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestReleaseLoadBalancerIP(t *testing.T) {
	t.Run("successful release", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockAddress := cloudstack.NewMockAddressServiceIface(ctrl)
		disassociateParams := &cloudstack.DisassociateIpAddressParams{}

		gomock.InOrder(
			mockAddress.EXPECT().NewDisassociateIpAddressParams("ip-123").Return(disassociateParams),
			mockAddress.EXPECT().DisassociateIpAddress(disassociateParams).Return(&cloudstack.DisassociateIpAddressResponse{}, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Address: mockAddress,
			},
			ipAddrID: "ip-123",
			ipAddr:   "203.0.113.1",
		}

		err := lb.releaseLoadBalancerIP()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("error releasing IP", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockAddress := cloudstack.NewMockAddressServiceIface(ctrl)
		disassociateParams := &cloudstack.DisassociateIpAddressParams{}
		apiErr := fmt.Errorf("disassociate API error")

		gomock.InOrder(
			mockAddress.EXPECT().NewDisassociateIpAddressParams("ip-123").Return(disassociateParams),
			mockAddress.EXPECT().DisassociateIpAddress(disassociateParams).Return(nil, apiErr),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Address: mockAddress,
			},
			ipAddrID: "ip-123",
			ipAddr:   "203.0.113.1",
		}

		err := lb.releaseLoadBalancerIP()
		if err == nil {
			t.Fatalf("expected error")
		}
		if !strings.Contains(err.Error(), "error releasing load balancer IP") {
			t.Errorf("error message = %q, want to contain 'error releasing load balancer IP'", err.Error())
		}
	})
}

func TestGetLoadBalancerIP(t *testing.T) {
	t.Run("IP specified - retrieve existing", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockAddress := cloudstack.NewMockAddressServiceIface(ctrl)
		listParams := &cloudstack.ListPublicIpAddressesParams{}
		resp := &cloudstack.ListPublicIpAddressesResponse{
			Count: 1,
			PublicIpAddresses: []*cloudstack.PublicIpAddress{
				{
					Id:        "ip-123",
					Ipaddress: "203.0.113.1",
					Allocated: "2023-01-01T00:00:00+0000",
				},
			},
		}

		gomock.InOrder(
			mockAddress.EXPECT().NewListPublicIpAddressesParams().Return(listParams),
			mockAddress.EXPECT().ListPublicIpAddresses(gomock.Any()).Return(resp, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Address: mockAddress,
			},
		}

		err := lb.getLoadBalancerIP("203.0.113.1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if lb.ipAddr != "203.0.113.1" {
			t.Errorf("ipAddr = %q, want %q", lb.ipAddr, "203.0.113.1")
		}
	})

	t.Run("IP specified - associate unallocated", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockAddress := cloudstack.NewMockAddressServiceIface(ctrl)
		mockNetwork := cloudstack.NewMockNetworkServiceIface(ctrl)
		listParams := &cloudstack.ListPublicIpAddressesParams{}
		resp := &cloudstack.ListPublicIpAddressesResponse{
			Count: 1,
			PublicIpAddresses: []*cloudstack.PublicIpAddress{
				{
					Id:        "ip-123",
					Ipaddress: "203.0.113.1",
					Allocated: "",
				},
			},
		}

		networkResp := &cloudstack.Network{
			Id:      "net-123",
			Vpcid:   "",
			Service: []cloudstack.NetworkServiceInternal{},
		}

		associateParams := &cloudstack.AssociateIpAddressParams{}
		associateResp := &cloudstack.AssociateIpAddressResponse{
			Id:        "ip-123",
			Ipaddress: "203.0.113.1",
		}

		gomock.InOrder(
			mockAddress.EXPECT().NewListPublicIpAddressesParams().Return(listParams),
			mockAddress.EXPECT().ListPublicIpAddresses(gomock.Any()).Return(resp, nil),
			mockNetwork.EXPECT().GetNetworkByID("net-123", gomock.Any()).Return(networkResp, 1, nil),
			mockAddress.EXPECT().NewAssociateIpAddressParams().Return(associateParams),
			mockAddress.EXPECT().AssociateIpAddress(gomock.Any()).Return(associateResp, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Address: mockAddress,
				Network: mockNetwork,
			},
			networkID: "net-123",
			ipAddr:    "203.0.113.1",
		}

		err := lb.getLoadBalancerIP("203.0.113.1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if lb.ipAddr != "203.0.113.1" {
			t.Errorf("ipAddr = %q, want %q", lb.ipAddr, "203.0.113.1")
		}
		if lb.ipAddrID != "ip-123" {
			t.Errorf("ipAddrID = %q, want %q", lb.ipAddrID, "ip-123")
		}
		if !lb.ipAssociatedByController {
			t.Errorf("ipAssociatedByController = false, want true")
		}
	})

	t.Run("no IP specified - associate new", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockAddress := cloudstack.NewMockAddressServiceIface(ctrl)
		mockNetwork := cloudstack.NewMockNetworkServiceIface(ctrl)
		networkResp := &cloudstack.Network{
			Id:      "net-123",
			Vpcid:   "",
			Service: []cloudstack.NetworkServiceInternal{},
		}

		associateParams := &cloudstack.AssociateIpAddressParams{}
		associateResp := &cloudstack.AssociateIpAddressResponse{
			Id:        "ip-123",
			Ipaddress: "203.0.113.1",
		}

		gomock.InOrder(
			mockNetwork.EXPECT().GetNetworkByID("net-123", gomock.Any()).Return(networkResp, 1, nil),
			mockAddress.EXPECT().NewAssociateIpAddressParams().Return(associateParams),
			mockAddress.EXPECT().AssociateIpAddress(gomock.Any()).Return(associateResp, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Address: mockAddress,
				Network: mockNetwork,
			},
			networkID: "net-123",
		}

		err := lb.getLoadBalancerIP("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if lb.ipAddr != "203.0.113.1" {
			t.Errorf("ipAddr = %q, want %q", lb.ipAddr, "203.0.113.1")
		}
		if lb.ipAddrID != "ip-123" {
			t.Errorf("ipAddrID = %q, want %q", lb.ipAddrID, "ip-123")
		}
		if !lb.ipAssociatedByController {
			t.Errorf("ipAssociatedByController = false, want true")
		}
	})
}

func TestCreateLoadBalancerRule(t *testing.T) {
	t.Run("create rule with default CIDR", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)
		createParams := &cloudstack.CreateLoadBalancerRuleParams{}
		createResp := &cloudstack.CreateLoadBalancerRuleResponse{
			Id:          "rule-123",
			Algorithm:   "roundrobin",
			Cidrlist:    defaultAllowedCIDR,
			Name:        "test-rule-tcp-80",
			Networkid:   "net-123",
			Privateport: "30000",
			Publicport:  "80",
			Publicip:    "203.0.113.1",
			Publicipid:  "ip-123",
			Protocol:    "tcp",
		}

		gomock.InOrder(
			mockLB.EXPECT().NewCreateLoadBalancerRuleParams("roundrobin", "test-rule-tcp-80", 30000, 80).Return(createParams),
			mockLB.EXPECT().CreateLoadBalancerRule(gomock.Any()).Return(createResp, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				LoadBalancer: mockLB,
			},
			algorithm: "roundrobin",
			networkID: "net-123",
			ipAddrID:  "ip-123",
			ipAddr:    "203.0.113.1",
		}

		port := corev1.ServicePort{
			Port:     80,
			NodePort: 30000,
			Protocol: corev1.ProtocolTCP,
		}
		service := &corev1.Service{}

		rule, err := lb.createLoadBalancerRule("test-rule-tcp-80", port, LoadBalancerProtocolTCP, service)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rule.Id != "rule-123" {
			t.Errorf("rule.Id = %q, want %q", rule.Id, "rule-123")
		}
		if rule.Name != "test-rule-tcp-80" {
			t.Errorf("rule.Name = %q, want %q", rule.Name, "test-rule-tcp-80")
		}
	})

	t.Run("create rule with custom CIDR list", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)
		createParams := &cloudstack.CreateLoadBalancerRuleParams{}
		createResp := &cloudstack.CreateLoadBalancerRuleResponse{
			Id:          "rule-123",
			Algorithm:   "roundrobin",
			Cidrlist:    "10.0.0.0/8,192.168.0.0/16",
			Name:        "test-rule-tcp-80",
			Networkid:   "net-123",
			Privateport: "30000",
			Publicport:  "80",
			Publicip:    "203.0.113.1",
			Publicipid:  "ip-123",
			Protocol:    "tcp",
		}

		gomock.InOrder(
			mockLB.EXPECT().NewCreateLoadBalancerRuleParams("roundrobin", "test-rule-tcp-80", 30000, 80).Return(createParams),
			mockLB.EXPECT().CreateLoadBalancerRule(gomock.Any()).Return(createResp, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				LoadBalancer: mockLB,
			},
			algorithm: "roundrobin",
			networkID: "net-123",
			ipAddrID:  "ip-123",
			ipAddr:    "203.0.113.1",
		}

		port := corev1.ServicePort{
			Port:     80,
			NodePort: 30000,
			Protocol: corev1.ProtocolTCP,
		}
		service := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					ServiceAnnotationLoadBalancerSourceCidrs: "10.0.0.0/8,192.168.0.0/16",
				},
			},
		}

		rule, err := lb.createLoadBalancerRule("test-rule-tcp-80", port, LoadBalancerProtocolTCP, service)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rule.Cidrlist != "10.0.0.0/8,192.168.0.0/16" {
			t.Errorf("rule.Cidrlist = %q, want %q", rule.Cidrlist, "10.0.0.0/8,192.168.0.0/16")
		}
	})

	t.Run("create rule with proxy protocol", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)
		createParams := &cloudstack.CreateLoadBalancerRuleParams{}
		createResp := &cloudstack.CreateLoadBalancerRuleResponse{
			Id:          "rule-123",
			Algorithm:   "roundrobin",
			Cidrlist:    defaultAllowedCIDR,
			Name:        "test-rule-tcp-proxy-80",
			Networkid:   "net-123",
			Privateport: "30000",
			Publicport:  "80",
			Publicip:    "203.0.113.1",
			Publicipid:  "ip-123",
			Protocol:    "tcp-proxy",
		}

		gomock.InOrder(
			mockLB.EXPECT().NewCreateLoadBalancerRuleParams("roundrobin", "test-rule-tcp-proxy-80", 30000, 80).Return(createParams),
			mockLB.EXPECT().CreateLoadBalancerRule(gomock.Any()).Return(createResp, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				LoadBalancer: mockLB,
			},
			algorithm: "roundrobin",
			networkID: "net-123",
			ipAddrID:  "ip-123",
			ipAddr:    "203.0.113.1",
		}

		port := corev1.ServicePort{
			Port:     80,
			NodePort: 30000,
			Protocol: corev1.ProtocolTCP,
		}
		service := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					ServiceAnnotationLoadBalancerProxyProtocol: "true",
				},
			},
		}

		rule, err := lb.createLoadBalancerRule("test-rule-tcp-proxy-80", port, LoadBalancerProtocolTCPProxy, service)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rule.Protocol != "tcp-proxy" {
			t.Errorf("rule.Protocol = %q, want %q", rule.Protocol, "tcp-proxy")
		}
	})

	t.Run("error creating rule", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)
		createParams := &cloudstack.CreateLoadBalancerRuleParams{}
		apiErr := fmt.Errorf("create rule API error")

		gomock.InOrder(
			mockLB.EXPECT().NewCreateLoadBalancerRuleParams("roundrobin", "test-rule-tcp-80", 30000, 80).Return(createParams),
			mockLB.EXPECT().CreateLoadBalancerRule(gomock.Any()).Return(nil, apiErr),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				LoadBalancer: mockLB,
			},
			algorithm: "roundrobin",
			networkID: "net-123",
			ipAddrID:  "ip-123",
			ipAddr:    "203.0.113.1",
		}

		port := corev1.ServicePort{
			Port:     80,
			NodePort: 30000,
			Protocol: corev1.ProtocolTCP,
		}
		service := &corev1.Service{}

		_, err := lb.createLoadBalancerRule("test-rule-tcp-80", port, LoadBalancerProtocolTCP, service)
		if err == nil {
			t.Fatalf("expected error")
		}
		if !strings.Contains(err.Error(), "error creating load balancer rule") {
			t.Errorf("error message = %q, want to contain 'error creating load balancer rule'", err.Error())
		}
	})

	t.Run("invalid CIDR in annotation", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)
		createParams := &cloudstack.CreateLoadBalancerRuleParams{}

		gomock.InOrder(
			mockLB.EXPECT().NewCreateLoadBalancerRuleParams("roundrobin", "test-rule-tcp-80", 30000, 80).Return(createParams),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				LoadBalancer: mockLB,
			},
			algorithm: "roundrobin",
		}

		port := corev1.ServicePort{
			Port:     80,
			NodePort: 30000,
			Protocol: corev1.ProtocolTCP,
		}
		service := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					ServiceAnnotationLoadBalancerSourceCidrs: "invalid-cidr",
				},
			},
		}

		_, err := lb.createLoadBalancerRule("test-rule-tcp-80", port, LoadBalancerProtocolTCP, service)
		if err == nil {
			t.Fatalf("expected error for invalid CIDR")
		}
		if !strings.Contains(err.Error(), "invalid CIDR") {
			t.Errorf("error message = %q, want to contain 'invalid CIDR'", err.Error())
		}
	})
}

func TestUpdateLoadBalancerRule(t *testing.T) {
	t.Run("update algorithm", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)
		updateParams := &cloudstack.UpdateLoadBalancerRuleParams{}

		gomock.InOrder(
			mockLB.EXPECT().NewUpdateLoadBalancerRuleParams("rule-123").Return(updateParams),
			mockLB.EXPECT().UpdateLoadBalancerRule(gomock.Any()).Return(&cloudstack.UpdateLoadBalancerRuleResponse{}, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				LoadBalancer: mockLB,
			},
			algorithm: "source",
			rules: map[string]*cloudstack.LoadBalancerRule{
				"test-rule-tcp-80": {
					Id:        "rule-123",
					Algorithm: "roundrobin",
					Protocol:  "tcp",
				},
			},
		}

		service := &corev1.Service{}

		err := lb.updateLoadBalancerRule("test-rule-tcp-80", LoadBalancerProtocolTCP, service, semver.Version{Major: 4, Minor: 22, Patch: 0})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("update protocol", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)
		updateParams := &cloudstack.UpdateLoadBalancerRuleParams{}

		gomock.InOrder(
			mockLB.EXPECT().NewUpdateLoadBalancerRuleParams("rule-123").Return(updateParams),
			mockLB.EXPECT().UpdateLoadBalancerRule(gomock.Any()).Return(&cloudstack.UpdateLoadBalancerRuleResponse{}, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				LoadBalancer: mockLB,
			},
			algorithm: "roundrobin",
			rules: map[string]*cloudstack.LoadBalancerRule{
				"test-rule-tcp-80": {
					Id:        "rule-123",
					Algorithm: "roundrobin",
					Protocol:  "tcp",
				},
			},
		}

		service := &corev1.Service{}

		err := lb.updateLoadBalancerRule("test-rule-tcp-80", LoadBalancerProtocolTCPProxy, service, semver.Version{Major: 4, Minor: 22, Patch: 0})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("update CIDR list (CS >= 4.22)", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)
		updateParams := &cloudstack.UpdateLoadBalancerRuleParams{}

		gomock.InOrder(
			mockLB.EXPECT().NewUpdateLoadBalancerRuleParams("rule-123").Return(updateParams),
			mockLB.EXPECT().UpdateLoadBalancerRule(gomock.Any()).Return(&cloudstack.UpdateLoadBalancerRuleResponse{}, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				LoadBalancer: mockLB,
			},
			algorithm: "roundrobin",
			rules: map[string]*cloudstack.LoadBalancerRule{
				"test-rule-tcp-80": {
					Id:        "rule-123",
					Algorithm: "roundrobin",
					Protocol:  "tcp",
					Cidrlist:  defaultAllowedCIDR,
				},
			},
		}

		service := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					ServiceAnnotationLoadBalancerSourceCidrs: "10.0.0.0/8",
				},
			},
		}

		err := lb.updateLoadBalancerRule("test-rule-tcp-80", LoadBalancerProtocolTCP, service, semver.Version{Major: 4, Minor: 22, Patch: 0})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("error updating rule", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)
		updateParams := &cloudstack.UpdateLoadBalancerRuleParams{}
		apiErr := fmt.Errorf("update rule API error")

		gomock.InOrder(
			mockLB.EXPECT().NewUpdateLoadBalancerRuleParams("rule-123").Return(updateParams),
			mockLB.EXPECT().UpdateLoadBalancerRule(gomock.Any()).Return(nil, apiErr),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				LoadBalancer: mockLB,
			},
			algorithm: "roundrobin",
			rules: map[string]*cloudstack.LoadBalancerRule{
				"test-rule-tcp-80": {
					Id:        "rule-123",
					Algorithm: "roundrobin",
					Protocol:  "tcp",
				},
			},
		}

		service := &corev1.Service{}

		err := lb.updateLoadBalancerRule("test-rule-tcp-80", LoadBalancerProtocolTCP, service, semver.Version{Major: 4, Minor: 22, Patch: 0})
		if err == nil {
			t.Fatalf("expected error")
		}
		if err != apiErr {
			t.Errorf("error = %v, want %v", err, apiErr)
		}
	})
}

func TestDeleteLoadBalancerRule(t *testing.T) {
	t.Run("successful deletion", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)
		deleteParams := &cloudstack.DeleteLoadBalancerRuleParams{}

		gomock.InOrder(
			mockLB.EXPECT().NewDeleteLoadBalancerRuleParams("rule-123").Return(deleteParams),
			mockLB.EXPECT().DeleteLoadBalancerRule(deleteParams).Return(&cloudstack.DeleteLoadBalancerRuleResponse{}, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				LoadBalancer: mockLB,
			},
			rules: map[string]*cloudstack.LoadBalancerRule{
				"test-rule": {
					Id:   "rule-123",
					Name: "test-rule",
				},
			},
		}

		rule := &cloudstack.LoadBalancerRule{
			Id:   "rule-123",
			Name: "test-rule",
		}

		err := lb.deleteLoadBalancerRule(rule)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, exists := lb.rules["test-rule"]; exists {
			t.Errorf("expected rule to be removed from map")
		}
	})

	t.Run("error deleting rule", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)
		deleteParams := &cloudstack.DeleteLoadBalancerRuleParams{}
		apiErr := fmt.Errorf("delete rule API error")

		gomock.InOrder(
			mockLB.EXPECT().NewDeleteLoadBalancerRuleParams("rule-123").Return(deleteParams),
			mockLB.EXPECT().DeleteLoadBalancerRule(deleteParams).Return(nil, apiErr),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				LoadBalancer: mockLB,
			},
			rules: map[string]*cloudstack.LoadBalancerRule{
				"test-rule": {
					Id:   "rule-123",
					Name: "test-rule",
				},
			},
		}

		rule := &cloudstack.LoadBalancerRule{
			Id:   "rule-123",
			Name: "test-rule",
		}

		err := lb.deleteLoadBalancerRule(rule)
		if err == nil {
			t.Fatalf("expected error")
		}
		if !strings.Contains(err.Error(), "error deleting load balancer rule") {
			t.Errorf("error message = %q, want to contain 'error deleting load balancer rule'", err.Error())
		}
	})
}

func TestAssignHostsToRule(t *testing.T) {
	t.Run("successful assignment", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)
		assignParams := &cloudstack.AssignToLoadBalancerRuleParams{}

		gomock.InOrder(
			mockLB.EXPECT().NewAssignToLoadBalancerRuleParams("rule-123").Return(assignParams),
			mockLB.EXPECT().AssignToLoadBalancerRule(gomock.Any()).Return(&cloudstack.AssignToLoadBalancerRuleResponse{}, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				LoadBalancer: mockLB,
			},
		}

		rule := &cloudstack.LoadBalancerRule{
			Id:   "rule-123",
			Name: "test-rule",
		}

		err := lb.assignHostsToRule(rule, []string{"vm-1", "vm-2"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("error assigning hosts", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)
		assignParams := &cloudstack.AssignToLoadBalancerRuleParams{}
		apiErr := fmt.Errorf("assign API error")

		gomock.InOrder(
			mockLB.EXPECT().NewAssignToLoadBalancerRuleParams("rule-123").Return(assignParams),
			mockLB.EXPECT().AssignToLoadBalancerRule(gomock.Any()).Return(nil, apiErr),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				LoadBalancer: mockLB,
			},
		}

		rule := &cloudstack.LoadBalancerRule{
			Id:   "rule-123",
			Name: "test-rule",
		}

		err := lb.assignHostsToRule(rule, []string{"vm-1"})
		if err == nil {
			t.Fatalf("expected error")
		}
		if !strings.Contains(err.Error(), "error assigning hosts") {
			t.Errorf("error message = %q, want to contain 'error assigning hosts'", err.Error())
		}
	})

	t.Run("empty host list", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)
		assignParams := &cloudstack.AssignToLoadBalancerRuleParams{}

		gomock.InOrder(
			mockLB.EXPECT().NewAssignToLoadBalancerRuleParams("rule-123").Return(assignParams),
			mockLB.EXPECT().AssignToLoadBalancerRule(gomock.Any()).Return(&cloudstack.AssignToLoadBalancerRuleResponse{}, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				LoadBalancer: mockLB,
			},
		}

		rule := &cloudstack.LoadBalancerRule{
			Id:   "rule-123",
			Name: "test-rule",
		}

		err := lb.assignHostsToRule(rule, []string{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestRemoveHostsFromRule(t *testing.T) {
	t.Run("successful removal", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)
		removeParams := &cloudstack.RemoveFromLoadBalancerRuleParams{}

		gomock.InOrder(
			mockLB.EXPECT().NewRemoveFromLoadBalancerRuleParams("rule-123").Return(removeParams),
			mockLB.EXPECT().RemoveFromLoadBalancerRule(gomock.Any()).Return(&cloudstack.RemoveFromLoadBalancerRuleResponse{}, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				LoadBalancer: mockLB,
			},
		}

		rule := &cloudstack.LoadBalancerRule{
			Id:   "rule-123",
			Name: "test-rule",
		}

		err := lb.removeHostsFromRule(rule, []string{"vm-1", "vm-2"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("error removing hosts", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)
		removeParams := &cloudstack.RemoveFromLoadBalancerRuleParams{}
		apiErr := fmt.Errorf("remove API error")

		gomock.InOrder(
			mockLB.EXPECT().NewRemoveFromLoadBalancerRuleParams("rule-123").Return(removeParams),
			mockLB.EXPECT().RemoveFromLoadBalancerRule(gomock.Any()).Return(nil, apiErr),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				LoadBalancer: mockLB,
			},
		}

		rule := &cloudstack.LoadBalancerRule{
			Id:   "rule-123",
			Name: "test-rule",
		}

		err := lb.removeHostsFromRule(rule, []string{"vm-1"})
		if err == nil {
			t.Fatalf("expected error")
		}
		if !strings.Contains(err.Error(), "error removing hosts") {
			t.Errorf("error message = %q, want to contain 'error removing hosts'", err.Error())
		}
	})

	t.Run("empty host list", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)
		removeParams := &cloudstack.RemoveFromLoadBalancerRuleParams{}

		gomock.InOrder(
			mockLB.EXPECT().NewRemoveFromLoadBalancerRuleParams("rule-123").Return(removeParams),
			mockLB.EXPECT().RemoveFromLoadBalancerRule(gomock.Any()).Return(&cloudstack.RemoveFromLoadBalancerRuleResponse{}, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				LoadBalancer: mockLB,
			},
		}

		rule := &cloudstack.LoadBalancerRule{
			Id:   "rule-123",
			Name: "test-rule",
		}

		err := lb.removeHostsFromRule(rule, []string{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestUpdateFirewallRule(t *testing.T) {
	t.Run("create new firewall rule", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockFirewall := cloudstack.NewMockFirewallServiceIface(ctrl)
		listParams := &cloudstack.ListFirewallRulesParams{}
		listResp := &cloudstack.ListFirewallRulesResponse{
			Count:         0,
			FirewallRules: []*cloudstack.FirewallRule{},
		}

		createParams := &cloudstack.CreateFirewallRuleParams{}
		createResp := &cloudstack.CreateFirewallRuleResponse{
			Id: "fw-123",
		}

		gomock.InOrder(
			mockFirewall.EXPECT().NewListFirewallRulesParams().Return(listParams),
			mockFirewall.EXPECT().ListFirewallRules(gomock.Any()).Return(listResp, nil),
			mockFirewall.EXPECT().NewCreateFirewallRuleParams("ip-123", "tcp").Return(createParams),
			mockFirewall.EXPECT().CreateFirewallRule(gomock.Any()).Return(createResp, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Firewall: mockFirewall,
			},
			ipAddr: "203.0.113.1",
		}

		updated, err := lb.updateFirewallRule("ip-123", 80, LoadBalancerProtocolTCP, []string{"10.0.0.0/8"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !updated {
			t.Errorf("updated = false, want true")
		}
	})

	t.Run("rule already exists - no change", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockFirewall := cloudstack.NewMockFirewallServiceIface(ctrl)
		listParams := &cloudstack.ListFirewallRulesParams{}
		listResp := &cloudstack.ListFirewallRulesResponse{
			Count: 1,
			FirewallRules: []*cloudstack.FirewallRule{
				{
					Id:          "fw-123",
					Protocol:    "tcp",
					Startport:   80,
					Endport:     80,
					Cidrlist:    "10.0.0.0/8",
					Ipaddress:   "203.0.113.1",
					Ipaddressid: "ip-123",
				},
			},
		}

		gomock.InOrder(
			mockFirewall.EXPECT().NewListFirewallRulesParams().Return(listParams),
			mockFirewall.EXPECT().ListFirewallRules(gomock.Any()).Return(listResp, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Firewall: mockFirewall,
			},
			ipAddr: "203.0.113.1",
		}

		updated, err := lb.updateFirewallRule("ip-123", 80, LoadBalancerProtocolTCP, []string{"10.0.0.0/8"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !updated {
			t.Errorf("updated = false, want true")
		}
	})

	t.Run("update existing rule - CIDR change", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockFirewall := cloudstack.NewMockFirewallServiceIface(ctrl)
		listParams := &cloudstack.ListFirewallRulesParams{}
		listResp := &cloudstack.ListFirewallRulesResponse{
			Count: 1,
			FirewallRules: []*cloudstack.FirewallRule{
				{
					Id:          "fw-123",
					Protocol:    "tcp",
					Startport:   80,
					Endport:     80,
					Cidrlist:    "192.168.0.0/16",
					Ipaddress:   "203.0.113.1",
					Ipaddressid: "ip-123",
				},
			},
		}

		deleteParams := &cloudstack.DeleteFirewallRuleParams{}
		createParams := &cloudstack.CreateFirewallRuleParams{}
		createResp := &cloudstack.CreateFirewallRuleResponse{
			Id: "fw-124",
		}

		gomock.InOrder(
			mockFirewall.EXPECT().NewListFirewallRulesParams().Return(listParams),
			mockFirewall.EXPECT().ListFirewallRules(gomock.Any()).Return(listResp, nil),
			mockFirewall.EXPECT().NewDeleteFirewallRuleParams("fw-123").Return(deleteParams),
			mockFirewall.EXPECT().DeleteFirewallRule(deleteParams).Return(&cloudstack.DeleteFirewallRuleResponse{}, nil),
			mockFirewall.EXPECT().NewCreateFirewallRuleParams("ip-123", "tcp").Return(createParams),
			mockFirewall.EXPECT().CreateFirewallRule(gomock.Any()).Return(createResp, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Firewall: mockFirewall,
			},
			ipAddr: "203.0.113.1",
		}

		updated, err := lb.updateFirewallRule("ip-123", 80, LoadBalancerProtocolTCP, []string{"10.0.0.0/8"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !updated {
			t.Errorf("updated = false, want true")
		}
	})

	t.Run("default CIDR when empty list", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockFirewall := cloudstack.NewMockFirewallServiceIface(ctrl)
		listParams := &cloudstack.ListFirewallRulesParams{}
		listResp := &cloudstack.ListFirewallRulesResponse{
			Count:         0,
			FirewallRules: []*cloudstack.FirewallRule{},
		}

		createParams := &cloudstack.CreateFirewallRuleParams{}
		createResp := &cloudstack.CreateFirewallRuleResponse{
			Id: "fw-123",
		}

		gomock.InOrder(
			mockFirewall.EXPECT().NewListFirewallRulesParams().Return(listParams),
			mockFirewall.EXPECT().ListFirewallRules(gomock.Any()).Return(listResp, nil),
			mockFirewall.EXPECT().NewCreateFirewallRuleParams("ip-123", "tcp").Return(createParams),
			mockFirewall.EXPECT().CreateFirewallRule(gomock.Any()).Return(createResp, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Firewall: mockFirewall,
			},
			ipAddr: "203.0.113.1",
		}

		updated, err := lb.updateFirewallRule("ip-123", 80, LoadBalancerProtocolTCP, []string{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !updated {
			t.Errorf("updated = false, want true")
		}
	})

	t.Run("error listing rules", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockFirewall := cloudstack.NewMockFirewallServiceIface(ctrl)
		listParams := &cloudstack.ListFirewallRulesParams{}
		apiErr := fmt.Errorf("list API error")

		gomock.InOrder(
			mockFirewall.EXPECT().NewListFirewallRulesParams().Return(listParams),
			mockFirewall.EXPECT().ListFirewallRules(gomock.Any()).Return(nil, apiErr),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Firewall: mockFirewall,
			},
			ipAddr: "203.0.113.1",
		}

		_, err := lb.updateFirewallRule("ip-123", 80, LoadBalancerProtocolTCP, []string{"10.0.0.0/8"})
		if err == nil {
			t.Fatalf("expected error")
		}
		if !strings.Contains(err.Error(), "error fetching firewall rules") {
			t.Errorf("error message = %q, want to contain 'error fetching firewall rules'", err.Error())
		}
	})

	t.Run("error creating rule", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockFirewall := cloudstack.NewMockFirewallServiceIface(ctrl)
		listParams := &cloudstack.ListFirewallRulesParams{}
		listResp := &cloudstack.ListFirewallRulesResponse{
			Count:         0,
			FirewallRules: []*cloudstack.FirewallRule{},
		}

		createParams := &cloudstack.CreateFirewallRuleParams{}
		apiErr := fmt.Errorf("create API error")

		gomock.InOrder(
			mockFirewall.EXPECT().NewListFirewallRulesParams().Return(listParams),
			mockFirewall.EXPECT().ListFirewallRules(gomock.Any()).Return(listResp, nil),
			mockFirewall.EXPECT().NewCreateFirewallRuleParams("ip-123", "tcp").Return(createParams),
			mockFirewall.EXPECT().CreateFirewallRule(gomock.Any()).Return(nil, apiErr),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Firewall: mockFirewall,
			},
			ipAddr: "203.0.113.1",
		}

		_, err := lb.updateFirewallRule("ip-123", 80, LoadBalancerProtocolTCP, []string{"10.0.0.0/8"})
		if err == nil {
			t.Fatalf("expected error")
		}
		if !strings.Contains(err.Error(), "error creating new firewall rule") {
			t.Errorf("error message = %q, want to contain 'error creating new firewall rule'", err.Error())
		}
	})

	t.Run("error deleting rule - continues", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockFirewall := cloudstack.NewMockFirewallServiceIface(ctrl)
		listParams := &cloudstack.ListFirewallRulesParams{}
		listResp := &cloudstack.ListFirewallRulesResponse{
			Count: 1,
			FirewallRules: []*cloudstack.FirewallRule{
				{
					Id:          "fw-123",
					Protocol:    "tcp",
					Startport:   80,
					Endport:     80,
					Cidrlist:    "192.168.0.0/16",
					Ipaddress:   "203.0.113.1",
					Ipaddressid: "ip-123",
				},
			},
		}

		deleteParams := &cloudstack.DeleteFirewallRuleParams{}
		deleteErr := fmt.Errorf("delete API error")
		createParams := &cloudstack.CreateFirewallRuleParams{}
		createResp := &cloudstack.CreateFirewallRuleResponse{
			Id: "fw-124",
		}

		gomock.InOrder(
			mockFirewall.EXPECT().NewListFirewallRulesParams().Return(listParams),
			mockFirewall.EXPECT().ListFirewallRules(gomock.Any()).Return(listResp, nil),
			mockFirewall.EXPECT().NewDeleteFirewallRuleParams("fw-123").Return(deleteParams),
			mockFirewall.EXPECT().DeleteFirewallRule(deleteParams).Return(nil, deleteErr),
			mockFirewall.EXPECT().NewCreateFirewallRuleParams("ip-123", "tcp").Return(createParams),
			mockFirewall.EXPECT().CreateFirewallRule(gomock.Any()).Return(createResp, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Firewall: mockFirewall,
			},
			ipAddr: "203.0.113.1",
		}

		updated, err := lb.updateFirewallRule("ip-123", 80, LoadBalancerProtocolTCP, []string{"10.0.0.0/8"})
		// Should still return true even if delete failed
		if err != nil && !strings.Contains(err.Error(), "error creating") {
			t.Fatalf("unexpected error: %v", err)
		}
		if !updated {
			t.Errorf("updated = false, want true")
		}
	})
}

func TestDeleteFirewallRule(t *testing.T) {
	t.Run("delete matching rule", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockFirewall := cloudstack.NewMockFirewallServiceIface(ctrl)
		listParams := &cloudstack.ListFirewallRulesParams{}
		listResp := &cloudstack.ListFirewallRulesResponse{
			Count: 1,
			FirewallRules: []*cloudstack.FirewallRule{
				{
					Id:          "fw-123",
					Protocol:    "tcp",
					Startport:   80,
					Endport:     80,
					Ipaddressid: "ip-123",
				},
			},
		}

		deleteParams := &cloudstack.DeleteFirewallRuleParams{}

		gomock.InOrder(
			mockFirewall.EXPECT().NewListFirewallRulesParams().Return(listParams),
			mockFirewall.EXPECT().ListFirewallRules(gomock.Any()).Return(listResp, nil),
			mockFirewall.EXPECT().NewDeleteFirewallRuleParams("fw-123").Return(deleteParams),
			mockFirewall.EXPECT().DeleteFirewallRule(deleteParams).Return(&cloudstack.DeleteFirewallRuleResponse{}, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Firewall: mockFirewall,
			},
		}

		deleted, err := lb.deleteFirewallRule("ip-123", 80, LoadBalancerProtocolTCP)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !deleted {
			t.Errorf("deleted = false, want true")
		}
	})

	t.Run("no matching rules", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockFirewall := cloudstack.NewMockFirewallServiceIface(ctrl)
		listParams := &cloudstack.ListFirewallRulesParams{}
		listResp := &cloudstack.ListFirewallRulesResponse{
			Count:         0,
			FirewallRules: []*cloudstack.FirewallRule{},
		}

		gomock.InOrder(
			mockFirewall.EXPECT().NewListFirewallRulesParams().Return(listParams),
			mockFirewall.EXPECT().ListFirewallRules(gomock.Any()).Return(listResp, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Firewall: mockFirewall,
			},
		}

		deleted, err := lb.deleteFirewallRule("ip-123", 80, LoadBalancerProtocolTCP)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if deleted {
			t.Errorf("deleted = true, want false")
		}
	})

	t.Run("error listing rules", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockFirewall := cloudstack.NewMockFirewallServiceIface(ctrl)
		listParams := &cloudstack.ListFirewallRulesParams{}
		apiErr := fmt.Errorf("list API error")

		gomock.InOrder(
			mockFirewall.EXPECT().NewListFirewallRulesParams().Return(listParams),
			mockFirewall.EXPECT().ListFirewallRules(gomock.Any()).Return(nil, apiErr),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Firewall: mockFirewall,
			},
		}

		_, err := lb.deleteFirewallRule("ip-123", 80, LoadBalancerProtocolTCP)
		if err == nil {
			t.Fatalf("expected error")
		}
		if !strings.Contains(err.Error(), "error fetching firewall rules") {
			t.Errorf("error message = %q, want to contain 'error fetching firewall rules'", err.Error())
		}
	})

	t.Run("error deleting rule", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockFirewall := cloudstack.NewMockFirewallServiceIface(ctrl)
		listParams := &cloudstack.ListFirewallRulesParams{}
		listResp := &cloudstack.ListFirewallRulesResponse{
			Count: 1,
			FirewallRules: []*cloudstack.FirewallRule{
				{
					Id:          "fw-123",
					Protocol:    "tcp",
					Startport:   80,
					Endport:     80,
					Ipaddressid: "ip-123",
				},
			},
		}

		deleteParams := &cloudstack.DeleteFirewallRuleParams{}
		deleteErr := fmt.Errorf("delete API error")

		gomock.InOrder(
			mockFirewall.EXPECT().NewListFirewallRulesParams().Return(listParams),
			mockFirewall.EXPECT().ListFirewallRules(gomock.Any()).Return(listResp, nil),
			mockFirewall.EXPECT().NewDeleteFirewallRuleParams("fw-123").Return(deleteParams),
			mockFirewall.EXPECT().DeleteFirewallRule(deleteParams).Return(nil, deleteErr),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Firewall: mockFirewall,
			},
		}

		deleted, err := lb.deleteFirewallRule("ip-123", 80, LoadBalancerProtocolTCP)
		// Should return false if deletion failed
		if deleted {
			t.Errorf("deleted = true, want false")
		}
		if err != deleteErr {
			t.Errorf("error = %v, want %v", err, deleteErr)
		}
	})
}

func TestUpdateNetworkACL(t *testing.T) {
	t.Run("create new ACL rule", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockNetwork := cloudstack.NewMockNetworkServiceIface(ctrl)
		mockNetworkACL := cloudstack.NewMockNetworkACLServiceIface(ctrl)
		networkResp := &cloudstack.Network{
			Id:      "net-123",
			Aclid:   "acl-456",
			Service: []cloudstack.NetworkServiceInternal{},
		}

		aclListResp := &cloudstack.NetworkACLList{
			Id:   "acl-456",
			Name: "custom-acl",
		}

		listParams := &cloudstack.ListNetworkACLsParams{}
		listResp := &cloudstack.ListNetworkACLsResponse{
			Count:       0,
			NetworkACLs: []*cloudstack.NetworkACL{},
		}

		createParams := &cloudstack.CreateNetworkACLParams{}
		createResp := &cloudstack.CreateNetworkACLResponse{
			Id: "acl-rule-123",
		}

		gomock.InOrder(
			mockNetwork.EXPECT().GetNetworkByID("net-123", gomock.Any()).Return(networkResp, 1, nil),
			mockNetworkACL.EXPECT().GetNetworkACLListByID("acl-456", gomock.Any()).Return(aclListResp, 1, nil),
			mockNetworkACL.EXPECT().NewListNetworkACLsParams().Return(listParams),
			mockNetworkACL.EXPECT().ListNetworkACLs(gomock.Any()).Return(listResp, nil),
			mockNetworkACL.EXPECT().NewCreateNetworkACLParams("tcp").Return(createParams),
			mockNetworkACL.EXPECT().CreateNetworkACL(gomock.Any()).Return(createResp, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Network:    mockNetwork,
				NetworkACL: mockNetworkACL,
			},
		}

		updated, err := lb.updateNetworkACL(80, LoadBalancerProtocolTCP, "net-123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !updated {
			t.Errorf("updated = false, want true")
		}
	})

	t.Run("rule already exists", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockNetwork := cloudstack.NewMockNetworkServiceIface(ctrl)
		mockNetworkACL := cloudstack.NewMockNetworkACLServiceIface(ctrl)
		networkResp := &cloudstack.Network{
			Id:      "net-123",
			Aclid:   "acl-456",
			Service: []cloudstack.NetworkServiceInternal{},
		}

		aclListResp := &cloudstack.NetworkACLList{
			Id:   "acl-456",
			Name: "custom-acl",
		}

		listParams := &cloudstack.ListNetworkACLsParams{}
		listResp := &cloudstack.ListNetworkACLsResponse{
			Count: 1,
			NetworkACLs: []*cloudstack.NetworkACL{
				{
					Id:        "acl-rule-123",
					Protocol:  "tcp",
					Startport: "80",
					Endport:   "80",
				},
			},
		}

		gomock.InOrder(
			mockNetwork.EXPECT().GetNetworkByID("net-123", gomock.Any()).Return(networkResp, 1, nil),
			mockNetworkACL.EXPECT().GetNetworkACLListByID("acl-456", gomock.Any()).Return(aclListResp, 1, nil),
			mockNetworkACL.EXPECT().NewListNetworkACLsParams().Return(listParams),
			mockNetworkACL.EXPECT().ListNetworkACLs(gomock.Any()).Return(listResp, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Network:    mockNetwork,
				NetworkACL: mockNetworkACL,
			},
		}

		updated, err := lb.updateNetworkACL(80, LoadBalancerProtocolTCP, "net-123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !updated {
			t.Errorf("updated = false, want true")
		}
	})

	t.Run("default ACL - skip", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockNetwork := cloudstack.NewMockNetworkServiceIface(ctrl)
		mockNetworkACL := cloudstack.NewMockNetworkACLServiceIface(ctrl)
		networkResp := &cloudstack.Network{
			Id:      "net-123",
			Aclid:   "acl-456",
			Service: []cloudstack.NetworkServiceInternal{},
		}

		aclListResp := &cloudstack.NetworkACLList{
			Id:   "acl-456",
			Name: "default_allow",
		}

		gomock.InOrder(
			mockNetwork.EXPECT().GetNetworkByID("net-123", gomock.Any()).Return(networkResp, 1, nil),
			mockNetworkACL.EXPECT().GetNetworkACLListByID("acl-456", gomock.Any()).Return(aclListResp, 1, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Network:    mockNetwork,
				NetworkACL: mockNetworkACL,
			},
		}

		updated, err := lb.updateNetworkACL(80, LoadBalancerProtocolTCP, "net-123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !updated {
			t.Errorf("updated = false, want true")
		}
	})

	t.Run("error fetching network", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockNetwork := cloudstack.NewMockNetworkServiceIface(ctrl)
		apiErr := fmt.Errorf("network API error")

		mockNetwork.EXPECT().GetNetworkByID("net-123", gomock.Any()).Return(nil, 1, apiErr)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Network: mockNetwork,
			},
		}

		_, err := lb.updateNetworkACL(80, LoadBalancerProtocolTCP, "net-123")
		if err == nil {
			t.Fatalf("expected error")
		}
		if !strings.Contains(err.Error(), "error fetching Network") {
			t.Errorf("error message = %q, want to contain 'error fetching Network'", err.Error())
		}
	})

	t.Run("error fetching ACL list", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockNetwork := cloudstack.NewMockNetworkServiceIface(ctrl)
		mockNetworkACL := cloudstack.NewMockNetworkACLServiceIface(ctrl)
		networkResp := &cloudstack.Network{
			Id:      "net-123",
			Aclid:   "acl-456",
			Service: []cloudstack.NetworkServiceInternal{},
		}

		apiErr := fmt.Errorf("ACL list API error")

		gomock.InOrder(
			mockNetwork.EXPECT().GetNetworkByID("net-123", gomock.Any()).Return(networkResp, 1, nil),
			mockNetworkACL.EXPECT().GetNetworkACLListByID("acl-456", gomock.Any()).Return(nil, 0, apiErr),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Network:    mockNetwork,
				NetworkACL: mockNetworkACL,
			},
		}

		_, err := lb.updateNetworkACL(80, LoadBalancerProtocolTCP, "net-123")
		if err == nil {
			t.Fatalf("expected error")
		}
		if !strings.Contains(err.Error(), "error fetching Network ACL List") {
			t.Errorf("error message = %q, want to contain 'error fetching Network ACL List'", err.Error())
		}
	})

	t.Run("network not found", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockNetwork := cloudstack.NewMockNetworkServiceIface(ctrl)
		mockNetworkACL := cloudstack.NewMockNetworkACLServiceIface(ctrl)
		networkResp := &cloudstack.Network{
			Id:      "net-123",
			Aclid:   "acl-456",
			Service: []cloudstack.NetworkServiceInternal{},
		}

		aclListResp := &cloudstack.NetworkACLList{
			Id:   "acl-456",
			Name: "custom-acl",
		}

		listParams := &cloudstack.ListNetworkACLsParams{}
		apiErr := fmt.Errorf("list ACL API error")

		gomock.InOrder(
			mockNetwork.EXPECT().GetNetworkByID("net-123", gomock.Any()).Return(networkResp, 1, nil),
			mockNetworkACL.EXPECT().GetNetworkACLListByID("acl-456", gomock.Any()).Return(aclListResp, 1, nil),
			mockNetworkACL.EXPECT().NewListNetworkACLsParams().Return(listParams),
			mockNetworkACL.EXPECT().ListNetworkACLs(gomock.Any()).Return(nil, apiErr),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Network:    mockNetwork,
				NetworkACL: mockNetworkACL,
			},
		}

		_, err := lb.updateNetworkACL(80, LoadBalancerProtocolTCP, "net-123")
		if err == nil {
			t.Fatalf("expected error")
		}
		if !strings.Contains(err.Error(), "error fetching Network ACL") {
			t.Errorf("error message = %q, want to contain 'error fetching Network ACL'", err.Error())
		}
	})

	t.Run("error creating ACL rule", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockNetwork := cloudstack.NewMockNetworkServiceIface(ctrl)
		mockNetworkACL := cloudstack.NewMockNetworkACLServiceIface(ctrl)
		networkResp := &cloudstack.Network{
			Id:      "net-123",
			Aclid:   "acl-456",
			Service: []cloudstack.NetworkServiceInternal{},
		}

		aclListResp := &cloudstack.NetworkACLList{
			Id:   "acl-456",
			Name: "custom-acl",
		}

		listParams := &cloudstack.ListNetworkACLsParams{}
		listResp := &cloudstack.ListNetworkACLsResponse{
			Count:       0,
			NetworkACLs: []*cloudstack.NetworkACL{},
		}

		createParams := &cloudstack.CreateNetworkACLParams{}
		apiErr := fmt.Errorf("create ACL API error")

		gomock.InOrder(
			mockNetwork.EXPECT().GetNetworkByID("net-123", gomock.Any()).Return(networkResp, 1, nil),
			mockNetworkACL.EXPECT().GetNetworkACLListByID("acl-456", gomock.Any()).Return(aclListResp, 1, nil),
			mockNetworkACL.EXPECT().NewListNetworkACLsParams().Return(listParams),
			mockNetworkACL.EXPECT().ListNetworkACLs(gomock.Any()).Return(listResp, nil),
			mockNetworkACL.EXPECT().NewCreateNetworkACLParams("tcp").Return(createParams),
			mockNetworkACL.EXPECT().CreateNetworkACL(gomock.Any()).Return(nil, apiErr),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Network:    mockNetwork,
				NetworkACL: mockNetworkACL,
			},
		}

		_, err := lb.updateNetworkACL(80, LoadBalancerProtocolTCP, "net-123")
		if err == nil {
			t.Fatalf("expected error")
		}
		if !strings.Contains(err.Error(), "error creating Network ACL") {
			t.Errorf("error message = %q, want to contain 'error creating Network ACL'", err.Error())
		}
	})
}

func TestDeleteNetworkACLRule(t *testing.T) {
	t.Run("delete matching rule", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockNetworkACL := cloudstack.NewMockNetworkACLServiceIface(ctrl)
		listParams := &cloudstack.ListNetworkACLsParams{}
		listResp := &cloudstack.ListNetworkACLsResponse{
			Count: 1,
			NetworkACLs: []*cloudstack.NetworkACL{
				{
					Id:        "acl-rule-123",
					Protocol:  "tcp",
					Startport: "80",
					Endport:   "80",
				},
			},
		}

		deleteParams := &cloudstack.DeleteNetworkACLParams{}

		gomock.InOrder(
			mockNetworkACL.EXPECT().NewListNetworkACLsParams().Return(listParams),
			mockNetworkACL.EXPECT().ListNetworkACLs(gomock.Any()).Return(listResp, nil),
			mockNetworkACL.EXPECT().NewDeleteNetworkACLParams("acl-rule-123").Return(deleteParams),
			mockNetworkACL.EXPECT().DeleteNetworkACL(deleteParams).Return(&cloudstack.DeleteNetworkACLResponse{}, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				NetworkACL: mockNetworkACL,
			},
		}

		deleted, err := lb.deleteNetworkACLRule(80, LoadBalancerProtocolTCP, "net-123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !deleted {
			t.Errorf("deleted = false, want true")
		}
	})

	t.Run("no matching rules", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockNetworkACL := cloudstack.NewMockNetworkACLServiceIface(ctrl)
		listParams := &cloudstack.ListNetworkACLsParams{}
		listResp := &cloudstack.ListNetworkACLsResponse{
			Count:       0,
			NetworkACLs: []*cloudstack.NetworkACL{},
		}

		gomock.InOrder(
			mockNetworkACL.EXPECT().NewListNetworkACLsParams().Return(listParams),
			mockNetworkACL.EXPECT().ListNetworkACLs(gomock.Any()).Return(listResp, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				NetworkACL: mockNetworkACL,
			},
		}

		deleted, err := lb.deleteNetworkACLRule(80, LoadBalancerProtocolTCP, "net-123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !deleted {
			t.Errorf("deleted = false, want true")
		}
	})

	t.Run("error listing ACLs", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockNetworkACL := cloudstack.NewMockNetworkACLServiceIface(ctrl)
		listParams := &cloudstack.ListNetworkACLsParams{}
		apiErr := fmt.Errorf("list ACL API error")

		gomock.InOrder(
			mockNetworkACL.EXPECT().NewListNetworkACLsParams().Return(listParams),
			mockNetworkACL.EXPECT().ListNetworkACLs(gomock.Any()).Return(nil, apiErr),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				NetworkACL: mockNetworkACL,
			},
		}

		_, err := lb.deleteNetworkACLRule(80, LoadBalancerProtocolTCP, "net-123")
		if err == nil {
			t.Fatalf("expected error")
		}
		if !strings.Contains(err.Error(), "error fetching Network ACL rules") {
			t.Errorf("error message = %q, want to contain 'error fetching Network ACL rules'", err.Error())
		}
	})

	t.Run("error deleting ACL", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockNetworkACL := cloudstack.NewMockNetworkACLServiceIface(ctrl)
		listParams := &cloudstack.ListNetworkACLsParams{}
		listResp := &cloudstack.ListNetworkACLsResponse{
			Count: 1,
			NetworkACLs: []*cloudstack.NetworkACL{
				{
					Id:        "acl-rule-123",
					Protocol:  "tcp",
					Startport: "80",
					Endport:   "80",
				},
			},
		}

		deleteParams := &cloudstack.DeleteNetworkACLParams{}
		deleteErr := fmt.Errorf("delete ACL API error")

		gomock.InOrder(
			mockNetworkACL.EXPECT().NewListNetworkACLsParams().Return(listParams),
			mockNetworkACL.EXPECT().ListNetworkACLs(gomock.Any()).Return(listResp, nil),
			mockNetworkACL.EXPECT().NewDeleteNetworkACLParams("acl-rule-123").Return(deleteParams),
			mockNetworkACL.EXPECT().DeleteNetworkACL(deleteParams).Return(nil, deleteErr),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				NetworkACL: mockNetworkACL,
			},
		}

		deleted, err := lb.deleteNetworkACLRule(80, LoadBalancerProtocolTCP, "net-123")
		if deleted {
			t.Errorf("deleted = true, want false")
		}
		if err != deleteErr {
			t.Errorf("error = %v, want %v", err, deleteErr)
		}
	})
}

func TestGetLoadBalancer(t *testing.T) {
	t.Run("load balancer with existing rules", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)
		listParams := &cloudstack.ListLoadBalancerRulesParams{}
		listResp := &cloudstack.ListLoadBalancerRulesResponse{
			Count: 2,
			LoadBalancerRules: []*cloudstack.LoadBalancerRule{
				{
					Id:          "rule-1",
					Name:        "test-service-tcp-80",
					Publicip:    "203.0.113.1",
					Publicipid:  "ip-123",
					Algorithm:   "roundrobin",
					Protocol:    "tcp",
					Publicport:  "80",
					Privateport: "30000",
				},
				{
					Id:          "rule-2",
					Name:        "test-service-tcp-443",
					Publicip:    "203.0.113.1",
					Publicipid:  "ip-123",
					Algorithm:   "roundrobin",
					Protocol:    "tcp",
					Publicport:  "443",
					Privateport: "30443",
				},
			},
		}

		gomock.InOrder(
			mockLB.EXPECT().NewListLoadBalancerRulesParams().Return(listParams),
			mockLB.EXPECT().ListLoadBalancerRules(gomock.Any()).Return(listResp, nil),
		)

		cs := &CSCloud{
			client: &cloudstack.CloudStackClient{
				LoadBalancer: mockLB,
			},
		}

		service := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-service",
				Namespace: "default",
			},
		}

		lb, err := cs.getLoadBalancer(service)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if lb.ipAddr != "203.0.113.1" {
			t.Errorf("ipAddr = %q, want %q", lb.ipAddr, "203.0.113.1")
		}
		if lb.ipAddrID != "ip-123" {
			t.Errorf("ipAddrID = %q, want %q", lb.ipAddrID, "ip-123")
		}
		if len(lb.rules) != 2 {
			t.Errorf("rules count = %d, want %d", len(lb.rules), 2)
		}
	})

	t.Run("load balancer with no rules", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)
		listParams := &cloudstack.ListLoadBalancerRulesParams{}
		listResp := &cloudstack.ListLoadBalancerRulesResponse{
			Count:             0,
			LoadBalancerRules: []*cloudstack.LoadBalancerRule{},
		}

		gomock.InOrder(
			mockLB.EXPECT().NewListLoadBalancerRulesParams().Return(listParams),
			mockLB.EXPECT().ListLoadBalancerRules(gomock.Any()).Return(listResp, nil),
		)

		cs := &CSCloud{
			client: &cloudstack.CloudStackClient{
				LoadBalancer: mockLB,
			},
		}

		service := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-service",
				Namespace: "default",
			},
		}

		lb, err := cs.getLoadBalancer(service)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(lb.rules) != 0 {
			t.Errorf("rules count = %d, want %d", len(lb.rules), 0)
		}
		if lb.ipAddr != "" {
			t.Errorf("ipAddr = %q, want empty", lb.ipAddr)
		}
	})

	t.Run("error retrieving rules", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)
		listParams := &cloudstack.ListLoadBalancerRulesParams{}
		apiErr := fmt.Errorf("list rules API error")

		gomock.InOrder(
			mockLB.EXPECT().NewListLoadBalancerRulesParams().Return(listParams),
			mockLB.EXPECT().ListLoadBalancerRules(gomock.Any()).Return(nil, apiErr),
		)

		cs := &CSCloud{
			client: &cloudstack.CloudStackClient{
				LoadBalancer: mockLB,
			},
		}

		service := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-service",
				Namespace: "default",
			},
		}

		_, err := cs.getLoadBalancer(service)
		if err == nil {
			t.Fatalf("expected error")
		}
		if !strings.Contains(err.Error(), "error retrieving load balancer rules") {
			t.Errorf("error message = %q, want to contain 'error retrieving load balancer rules'", err.Error())
		}
	})

	listDuplicates := func(t *testing.T, requestedIP, publishedIP string, rules ...*cloudstack.LoadBalancerRule) *loadBalancer {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)
		mockLB.EXPECT().NewListLoadBalancerRulesParams().Return(&cloudstack.ListLoadBalancerRulesParams{})
		mockLB.EXPECT().ListLoadBalancerRules(gomock.Any()).
			Return(&cloudstack.ListLoadBalancerRulesResponse{Count: len(rules), LoadBalancerRules: rules}, nil)

		cs := &CSCloud{client: &cloudstack.CloudStackClient{LoadBalancer: mockLB}}
		service := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "test-service", Namespace: "default"},
			Spec:       corev1.ServiceSpec{LoadBalancerIP: requestedIP},
		}
		if publishedIP != "" {
			service.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{IP: publishedIP}}
		}

		lb, err := cs.getLoadBalancer(service)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return lb
	}
	onAutoIP := &cloudstack.LoadBalancerRule{Id: "rule-auto", Name: "test-service-tcp-80", Publicip: "203.0.113.1", Publicipid: "ip-auto"}
	onRequestedIP := &cloudstack.LoadBalancerRule{Id: "rule-requested", Name: "test-service-tcp-80", Publicip: "203.0.113.9", Publicipid: "ip-requested"}

	assertKept := func(t *testing.T, lb *loadBalancer, kept, discarded *cloudstack.LoadBalancerRule) {
		if got := lb.rules["test-service-tcp-80"]; got != kept {
			t.Errorf("kept rule = %v, want %v", got.Id, kept.Id)
		}
		if len(lb.duplicateRules) != 1 || lb.duplicateRules[0] != discarded {
			t.Errorf("duplicateRules = %+v, want only %v", lb.duplicateRules, discarded.Id)
		}
		if lb.ipAddr != kept.Publicip || lb.ipAddrID != kept.Publicipid {
			t.Errorf("ipAddr/ipAddrID = %v/%v, want the kept rule's %v/%v", lb.ipAddr, lb.ipAddrID, kept.Publicip, kept.Publicipid)
		}
	}

	t.Run("the first of two same-named rules is kept", func(t *testing.T) {
		lb := listDuplicates(t, "", "", onAutoIP, onRequestedIP)
		assertKept(t, lb, onAutoIP, onRequestedIP)
	})

	t.Run("a later rule on the requested IP is kept over an earlier one", func(t *testing.T) {
		lb := listDuplicates(t, onRequestedIP.Publicip, "", onAutoIP, onRequestedIP)
		assertKept(t, lb, onRequestedIP, onAutoIP)
	})

	t.Run("an earlier rule on the requested IP stays kept", func(t *testing.T) {
		lb := listDuplicates(t, onRequestedIP.Publicip, "", onRequestedIP, onAutoIP)
		assertKept(t, lb, onRequestedIP, onAutoIP)
	})

	t.Run("the rule on the published ingress IP is kept when no IP was requested", func(t *testing.T) {
		lb := listDuplicates(t, "", onRequestedIP.Publicip, onAutoIP, onRequestedIP)
		assertKept(t, lb, onRequestedIP, onAutoIP)
	})

	t.Run("a requested IP outranks the published ingress IP", func(t *testing.T) {
		lb := listDuplicates(t, onAutoIP.Publicip, onRequestedIP.Publicip, onRequestedIP, onAutoIP)
		assertKept(t, lb, onAutoIP, onRequestedIP)
	})
}

// A failed sweep is reported so the service controller retries, but only after
// this service's own rules and IP are gone, so the retry sees the leftover
// duplicate as an ordinary rule instead of blocking deletion for ever.
func TestEnsureLoadBalancerDeletedReportsASweepFailureLast(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)
	mockFW := cloudstack.NewMockFirewallServiceIface(ctrl)
	mockAddr := cloudstack.NewMockAddressServiceIface(ctrl)
	mockNet := cloudstack.NewMockNetworkServiceIface(ctrl)

	kept := &cloudstack.LoadBalancerRule{Id: "keep", Name: "test-service-tcp-80", Publicip: "203.0.113.1", Publicipid: "ip-keep", Publicport: "80", Protocol: "tcp"}
	duplicate := &cloudstack.LoadBalancerRule{Id: "dup", Name: "test-service-tcp-80", Publicip: "203.0.113.2", Publicipid: "ip-dup", Publicport: "80", Protocol: "tcp"}

	mockLB.EXPECT().NewListLoadBalancerRulesParams().Return(&cloudstack.ListLoadBalancerRulesParams{})
	mockLB.EXPECT().ListLoadBalancerRules(gomock.Any()).Return(&cloudstack.ListLoadBalancerRulesResponse{
		Count: 2, LoadBalancerRules: []*cloudstack.LoadBalancerRule{kept, duplicate},
	}, nil)

	// The sweep fails while listing the duplicate's firewall rules.
	mockFW.EXPECT().NewListFirewallRulesParams().Return(&cloudstack.ListFirewallRulesParams{})
	mockFW.EXPECT().ListFirewallRules(gomock.Any()).Return(nil, fmt.Errorf("firewall API down"))

	// Deletion of the kept rule must still happen.
	mockAddr.EXPECT().GetPublicIpAddressByID("ip-keep", gomock.Any()).
		Return(&cloudstack.PublicIpAddress{Id: "ip-keep", Associatednetworkid: "net-1"}, 1, nil)
	// Once inside getNetworkIDFromIPAddress, once in the delete loop itself.
	mockNet.EXPECT().GetNetworkByID("net-1", gomock.Any()).
		Return(&cloudstack.Network{Id: "net-1"}, 1, nil).Times(2)
	mockFW.EXPECT().NewListFirewallRulesParams().Return(&cloudstack.ListFirewallRulesParams{})
	mockFW.EXPECT().ListFirewallRules(gomock.Any()).Return(&cloudstack.ListFirewallRulesResponse{}, nil)
	deleteParams := &cloudstack.DeleteLoadBalancerRuleParams{}
	mockLB.EXPECT().NewDeleteLoadBalancerRuleParams("keep").Return(deleteParams)
	mockLB.EXPECT().DeleteLoadBalancerRule(deleteParams).Return(&cloudstack.DeleteLoadBalancerRuleResponse{}, nil)

	// And the load balancer IP must still be released.
	release := &cloudstack.DisassociateIpAddressParams{}
	mockAddr.EXPECT().NewDisassociateIpAddressParams("ip-keep").Return(release)
	mockAddr.EXPECT().DisassociateIpAddress(release).Return(&cloudstack.DisassociateIpAddressResponse{}, nil)

	cs := &CSCloud{client: &cloudstack.CloudStackClient{
		LoadBalancer: mockLB, Firewall: mockFW, Address: mockAddr, Network: mockNet,
	}}
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "test-service", Namespace: "default"}}

	err := cs.EnsureLoadBalancerDeleted(context.TODO(), "test", service)
	if err == nil {
		t.Fatal("expected the sweep failure to be reported so the delete is retried")
	}
	if !strings.Contains(err.Error(), "firewall API down") {
		t.Errorf("error = %q, want it to carry the sweep failure", err)
	}
	// gomock asserts on Cleanup that the kept rule was deleted and the IP released
	// despite the failure; without that the retry would have nothing to converge on.
}

func TestGetNetworkIDFromIPAddress(t *testing.T) {
	t.Run("successful retrieval", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockAddress := cloudstack.NewMockAddressServiceIface(ctrl)
		mockNetwork := cloudstack.NewMockNetworkServiceIface(ctrl)
		ipResp := &cloudstack.PublicIpAddress{
			Id:                  "ip-123",
			Ipaddress:           "203.0.113.1",
			Networkid:           "net-123",
			Associatednetworkid: "net-123",
		}

		networkResp := &cloudstack.Network{
			Id:      "net-123",
			Service: []cloudstack.NetworkServiceInternal{},
		}

		gomock.InOrder(
			mockAddress.EXPECT().GetPublicIpAddressByID("ip-123", gomock.Any()).Return(ipResp, 1, nil),
			mockNetwork.EXPECT().GetNetworkByID("net-123", gomock.Any()).Return(networkResp, 1, nil),
		)

		cs := &CSCloud{
			client: &cloudstack.CloudStackClient{
				Address: mockAddress,
				Network: mockNetwork,
			},
		}

		networkID, err := cs.getNetworkIDFromIPAddress("ip-123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if networkID != "net-123" {
			t.Errorf("networkID = %q, want %q", networkID, "net-123")
		}
	})

	t.Run("IP not found", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockAddress := cloudstack.NewMockAddressServiceIface(ctrl)
		apiErr := fmt.Errorf("IP not found")

		mockAddress.EXPECT().GetPublicIpAddressByID("ip-123", gomock.Any()).Return(nil, 0, apiErr)

		cs := &CSCloud{
			client: &cloudstack.CloudStackClient{
				Address: mockAddress,
			},
		}

		_, err := cs.getNetworkIDFromIPAddress("ip-123")
		if err == nil {
			t.Fatalf("expected error")
		}
		if err != apiErr {
			t.Errorf("error = %v, want %v", err, apiErr)
		}
	})

	// The following two cases used to return ("", nil). The caller passes the
	// result straight to GetNetworkByID, which does not reject an empty ID, so
	// a nil error there resolves to an arbitrary network instead of failing.
	t.Run("IP not associated with a network", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockAddress := cloudstack.NewMockAddressServiceIface(ctrl)
		mockAddress.EXPECT().GetPublicIpAddressByID("ip-123", gomock.Any()).
			Return(&cloudstack.PublicIpAddress{Id: "ip-123"}, 1, nil)

		cs := &CSCloud{
			client: &cloudstack.CloudStackClient{Address: mockAddress},
		}

		networkID, err := cs.getNetworkIDFromIPAddress("ip-123")
		if err == nil {
			t.Fatalf("expected an error for an IP with no associated network")
		}
		if networkID != "" {
			t.Errorf("networkID = %q, want empty", networkID)
		}
	})

	t.Run("network lookup fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockAddress := cloudstack.NewMockAddressServiceIface(ctrl)
		mockNetwork := cloudstack.NewMockNetworkServiceIface(ctrl)
		netErr := fmt.Errorf("network not found")

		gomock.InOrder(
			mockAddress.EXPECT().GetPublicIpAddressByID("ip-123", gomock.Any()).
				Return(&cloudstack.PublicIpAddress{Id: "ip-123", Associatednetworkid: "net-123"}, 1, nil),
			mockNetwork.EXPECT().GetNetworkByID("net-123", gomock.Any()).Return(nil, 0, netErr),
		)

		cs := &CSCloud{
			client: &cloudstack.CloudStackClient{
				Address: mockAddress,
				Network: mockNetwork,
			},
		}

		networkID, err := cs.getNetworkIDFromIPAddress("ip-123")
		if err != netErr {
			t.Errorf("error = %v, want %v", err, netErr)
		}
		if networkID != "" {
			t.Errorf("networkID = %q, want empty", networkID)
		}
	})
}

// CloudStack does not enforce unique load balancer rule names. Because
// loadBalancer.rules is keyed by name it can only manage one rule per name, so
// the rest are tracked separately and removed; before that they survived
// deletion and leaked with no service left to reference them.
// A duplicate always sits on a different public IP from the kept rule, because
// CloudStack rejects a second rule on the same IP and port. Removing only the
// load balancer rule would therefore leak the duplicate's firewall rule and
// its public IP.
func TestDuplicateLoadBalancerRules(t *testing.T) {
	type mocks struct {
		lb   *cloudstack.MockLoadBalancerServiceIface
		fw   *cloudstack.MockFirewallServiceIface
		addr *cloudstack.MockAddressServiceIface
	}
	newLB := func(t *testing.T, duplicate *cloudstack.LoadBalancerRule) (*loadBalancer, mocks) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		m := mocks{
			lb:   cloudstack.NewMockLoadBalancerServiceIface(ctrl),
			fw:   cloudstack.NewMockFirewallServiceIface(ctrl),
			addr: cloudstack.NewMockAddressServiceIface(ctrl),
		}
		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				LoadBalancer: m.lb, Firewall: m.fw, Address: m.addr,
			},
			name:     "a-lb",
			ipAddrID: "ip-keep",
			rules: map[string]*cloudstack.LoadBalancerRule{
				"a-lb-tcp-80": {Id: "keep", Name: "a-lb-tcp-80", Publicipid: "ip-keep"},
			},
			duplicateRules: []*cloudstack.LoadBalancerRule{duplicate},
		}
		return lb, m
	}
	onOwnIP := &cloudstack.LoadBalancerRule{
		Id: "dup-1", Name: "a-lb-tcp-80", Publicport: "80", Protocol: "tcp", Publicipid: "ip-dup",
	}
	expectFirewallRules := func(m mocks, rules ...*cloudstack.FirewallRule) {
		params := &cloudstack.ListFirewallRulesParams{}
		m.fw.EXPECT().NewListFirewallRulesParams().Return(params)
		m.fw.EXPECT().ListFirewallRules(params).
			Return(&cloudstack.ListFirewallRulesResponse{FirewallRules: rules}, nil)
	}
	expectRuleDeleted := func(m mocks, id string, err error) {
		params := &cloudstack.DeleteLoadBalancerRuleParams{}
		m.lb.EXPECT().NewDeleteLoadBalancerRuleParams(id).Return(params)
		m.lb.EXPECT().DeleteLoadBalancerRule(params).
			Return(&cloudstack.DeleteLoadBalancerRuleResponse{}, err)
	}
	expectRulesOnIP := func(m mocks, count int) {
		params := &cloudstack.ListLoadBalancerRulesParams{}
		m.lb.EXPECT().NewListLoadBalancerRulesParams().Return(params)
		m.lb.EXPECT().ListLoadBalancerRules(params).
			Return(&cloudstack.ListLoadBalancerRulesResponse{Count: count}, nil)
	}

	t.Run("a duplicate on its own IP takes its firewall rule and IP with it", func(t *testing.T) {
		lb, m := newLB(t, onOwnIP)
		expectFirewallRules(m, &cloudstack.FirewallRule{Id: "fw-dup", Protocol: "tcp", Startport: 80, Endport: 80})
		fwDelete := &cloudstack.DeleteFirewallRuleParams{}
		m.fw.EXPECT().NewDeleteFirewallRuleParams("fw-dup").Return(fwDelete)
		m.fw.EXPECT().DeleteFirewallRule(fwDelete).Return(&cloudstack.DeleteFirewallRuleResponse{}, nil)
		expectRuleDeleted(m, "dup-1", nil)
		expectRulesOnIP(m, 0)
		release := &cloudstack.DisassociateIpAddressParams{}
		m.addr.EXPECT().NewDisassociateIpAddressParams("ip-dup").Return(release)
		m.addr.EXPECT().DisassociateIpAddress(release).Return(&cloudstack.DisassociateIpAddressResponse{}, nil)

		if err := lb.deleteDuplicateRules(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(lb.duplicateRules) != 0 {
			t.Errorf("duplicateRules = %d, want 0", len(lb.duplicateRules))
		}
		if kept := lb.rules["a-lb-tcp-80"]; kept == nil || kept.Id != "keep" {
			t.Errorf("kept rule = %+v, want the original rule to survive", kept)
		}
	})

	t.Run("the IP stays while another rule still uses it", func(t *testing.T) {
		lb, m := newLB(t, onOwnIP)
		expectFirewallRules(m)
		expectRuleDeleted(m, "dup-1", nil)
		expectRulesOnIP(m, 1)

		if err := lb.deleteDuplicateRules(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("a duplicate sharing the kept IP never releases it", func(t *testing.T) {
		sharesIP := &cloudstack.LoadBalancerRule{
			Id: "dup-1", Name: "a-lb-tcp-80", Publicport: "80", Protocol: "tcp", Publicipid: "ip-keep",
		}
		lb, m := newLB(t, sharesIP)
		expectFirewallRules(m)
		expectRuleDeleted(m, "dup-1", nil)

		if err := lb.deleteDuplicateRules(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("a delete failure is surfaced", func(t *testing.T) {
		lb, m := newLB(t, onOwnIP)
		expectFirewallRules(m)
		expectRuleDeleted(m, "dup-1", fmt.Errorf("boom"))

		if err := lb.deleteDuplicateRules(); err == nil {
			t.Fatal("expected an error when deleting a duplicate fails")
		}
	})

	t.Run("a duplicate with an unparseable public port is left in place without API calls", func(t *testing.T) {
		lb, _ := newLB(t, &cloudstack.LoadBalancerRule{Id: "dup-1", Name: "a-lb-tcp-80", Protocol: "tcp"})

		if err := lb.deleteDuplicateRules(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("a duplicate with an unsupported protocol is left in place without API calls", func(t *testing.T) {
		lb, _ := newLB(t, &cloudstack.LoadBalancerRule{Id: "dup-1", Name: "a-lb-tcp-80", Publicport: "80", Protocol: "sctp"})

		if err := lb.deleteDuplicateRules(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestVerifyHosts(t *testing.T) {
	t.Run("all hosts in same network", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockVM := cloudstack.NewMockVirtualMachineServiceIface(ctrl)
		listParams := &cloudstack.ListVirtualMachinesParams{}
		listResp := &cloudstack.ListVirtualMachinesResponse{
			Count: 2,
			VirtualMachines: []*cloudstack.VirtualMachine{
				{
					Id:   "vm-1",
					Name: "node-1",
					Nic: []cloudstack.Nic{
						{Networkid: "net-123"},
					},
				},
				{
					Id:   "vm-2",
					Name: "node-2",
					Nic: []cloudstack.Nic{
						{Networkid: "net-123"},
					},
				},
			},
		}

		gomock.InOrder(
			mockVM.EXPECT().NewListVirtualMachinesParams().Return(listParams),
			mockVM.EXPECT().ListVirtualMachines(gomock.Any()).Return(listResp, nil),
		)

		cs := &CSCloud{
			client: &cloudstack.CloudStackClient{
				VirtualMachine: mockVM,
			},
		}

		nodes := []*corev1.Node{
			{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "node-2"}},
		}

		hostIDs, networkID, err := cs.verifyHosts(nodes)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(hostIDs) != 2 {
			t.Errorf("hostIDs count = %d, want %d", len(hostIDs), 2)
		}
		if networkID != "net-123" {
			t.Errorf("networkID = %q, want %q", networkID, "net-123")
		}
	})

	t.Run("hosts in different networks", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockVM := cloudstack.NewMockVirtualMachineServiceIface(ctrl)
		listParams := &cloudstack.ListVirtualMachinesParams{}
		listResp := &cloudstack.ListVirtualMachinesResponse{
			Count: 2,
			VirtualMachines: []*cloudstack.VirtualMachine{
				{
					Id:   "vm-1",
					Name: "node-1",
					Nic: []cloudstack.Nic{
						{Networkid: "net-123"},
					},
				},
				{
					Id:   "vm-2",
					Name: "node-2",
					Nic: []cloudstack.Nic{
						{Networkid: "net-456"},
					},
				},
			},
		}

		gomock.InOrder(
			mockVM.EXPECT().NewListVirtualMachinesParams().Return(listParams),
			mockVM.EXPECT().ListVirtualMachines(gomock.Any()).Return(listResp, nil),
		)

		cs := &CSCloud{
			client: &cloudstack.CloudStackClient{
				VirtualMachine: mockVM,
			},
		}

		nodes := []*corev1.Node{
			{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "node-2"}},
		}

		_, _, err := cs.verifyHosts(nodes)
		if err == nil {
			t.Fatalf("expected error")
		}
		if !strings.Contains(err.Error(), "different networks") {
			t.Errorf("error message = %q, want to contain 'different networks'", err.Error())
		}
	})

	t.Run("no matching hosts", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockVM := cloudstack.NewMockVirtualMachineServiceIface(ctrl)
		listParams := &cloudstack.ListVirtualMachinesParams{}
		listResp := &cloudstack.ListVirtualMachinesResponse{
			Count:           0,
			VirtualMachines: []*cloudstack.VirtualMachine{},
		}

		gomock.InOrder(
			mockVM.EXPECT().NewListVirtualMachinesParams().Return(listParams),
			mockVM.EXPECT().ListVirtualMachines(gomock.Any()).Return(listResp, nil),
		)

		cs := &CSCloud{
			client: &cloudstack.CloudStackClient{
				VirtualMachine: mockVM,
			},
		}

		nodes := []*corev1.Node{
			{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}},
		}

		_, _, err := cs.verifyHosts(nodes)
		if err == nil {
			t.Fatalf("expected error")
		}
		if !strings.Contains(err.Error(), "none of the hosts matched") {
			t.Errorf("error message = %q, want to contain 'none of the hosts matched'", err.Error())
		}
	})

	t.Run("FQDN node names", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockVM := cloudstack.NewMockVirtualMachineServiceIface(ctrl)
		listParams := &cloudstack.ListVirtualMachinesParams{}
		listResp := &cloudstack.ListVirtualMachinesResponse{
			Count: 1,
			VirtualMachines: []*cloudstack.VirtualMachine{
				{
					Id:   "vm-1",
					Name: "node-1",
					Nic: []cloudstack.Nic{
						{Networkid: "net-123"},
					},
				},
			},
		}

		gomock.InOrder(
			mockVM.EXPECT().NewListVirtualMachinesParams().Return(listParams),
			mockVM.EXPECT().ListVirtualMachines(gomock.Any()).Return(listResp, nil),
		)

		cs := &CSCloud{
			client: &cloudstack.CloudStackClient{
				VirtualMachine: mockVM,
			},
		}

		nodes := []*corev1.Node{
			{ObjectMeta: metav1.ObjectMeta{Name: "node-1.example.com"}},
		}

		hostIDs, networkID, err := cs.verifyHosts(nodes)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(hostIDs) != 1 {
			t.Errorf("hostIDs count = %d, want %d", len(hostIDs), 1)
		}
		if networkID != "net-123" {
			t.Errorf("networkID = %q, want %q", networkID, "net-123")
		}
	})

	t.Run("case-insensitive matching", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockVM := cloudstack.NewMockVirtualMachineServiceIface(ctrl)
		listParams := &cloudstack.ListVirtualMachinesParams{}
		listResp := &cloudstack.ListVirtualMachinesResponse{
			Count: 1,
			VirtualMachines: []*cloudstack.VirtualMachine{
				{
					Id:   "vm-1",
					Name: "NODE-1",
					Nic: []cloudstack.Nic{
						{Networkid: "net-123"},
					},
				},
			},
		}

		gomock.InOrder(
			mockVM.EXPECT().NewListVirtualMachinesParams().Return(listParams),
			mockVM.EXPECT().ListVirtualMachines(gomock.Any()).Return(listResp, nil),
		)

		cs := &CSCloud{
			client: &cloudstack.CloudStackClient{
				VirtualMachine: mockVM,
			},
		}

		nodes := []*corev1.Node{
			{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}},
		}

		hostIDs, networkID, err := cs.verifyHosts(nodes)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(hostIDs) != 1 {
			t.Errorf("hostIDs count = %d, want %d", len(hostIDs), 1)
		}
		if networkID != "net-123" {
			t.Errorf("networkID = %q, want %q", networkID, "net-123")
		}
	})
}
