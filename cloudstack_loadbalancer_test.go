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

		rule, needsUpdate, err := lb.checkLoadBalancerRule(lb.findLoadBalancerRule("missing", port, LoadBalancerProtocolTCP), "missing", port, LoadBalancerProtocolTCP, service, semver.Version{})
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

		rule, needsUpdate, err := lb.checkLoadBalancerRule(lb.rules["rule"], "rule", port, LoadBalancerProtocolTCP, service, semver.Version{Major: 4, Minor: 21, Patch: 0})
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

		rule, needsUpdate, err := lb.checkLoadBalancerRule(lb.rules["rule"], "rule", port, LoadBalancerProtocolTCP, service, semver.Version{Major: 4, Minor: 22, Patch: 0})
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

	t.Run("matching multi-CIDR list needs no update", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		// No expectations: an unchanged CIDR list must not trigger any API call.
		mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)

		lbRule := &cloudstack.LoadBalancerRule{
			Id:          "rule-id",
			Name:        "rule",
			Publicip:    "1.1.1.1",
			Privateport: "30000",
			Publicport:  "80",
			Cidrlist:    "10.0.0.0/8,192.168.0.0/16",
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

		rule, needsUpdate, err := lb.checkLoadBalancerRule(lb.rules["rule"], "rule", port, LoadBalancerProtocolTCP, service, semver.Version{Major: 4, Minor: 22, Patch: 0})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rule != lbRule {
			t.Fatalf("expected existing rule to be returned")
		}
		if needsUpdate {
			t.Fatalf("expected needsUpdate to be false for an unchanged CIDR list")
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

		rule, needsUpdate, err := lb.checkLoadBalancerRule(lb.rules["rule"], "rule", port, LoadBalancerProtocolTCP, service, semver.Version{Major: 4, Minor: 12, Patch: 0})
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

	t.Run("protocol change updates in place", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		// No expectations: a protocol change is an update, never a recreate.
		mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)

		lbRule := &cloudstack.LoadBalancerRule{
			Id:          "rule-id",
			Name:        "rule-tcp-80",
			Publicip:    "1.1.1.1",
			Privateport: "30000",
			Publicport:  "80",
			Cidrlist:    defaultAllowedCIDR,
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
				"rule-tcp-80": lbRule,
			},
		}
		port := corev1.ServicePort{Port: 80, NodePort: 30000, Protocol: corev1.ProtocolTCP}
		service := &corev1.Service{}

		rule, needsUpdate, err := lb.checkLoadBalancerRule(lbRule, "rule-tcp-proxy-80", port, LoadBalancerProtocolTCPProxy, service, semver.Version{Major: 4, Minor: 22, Patch: 0})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rule != lbRule {
			t.Fatalf("expected existing rule to be returned")
		}
		if !needsUpdate {
			t.Fatalf("expected needsUpdate to be true for a protocol and name change")
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

		_, _, err := lb.checkLoadBalancerRule(lb.rules["rule"], "rule", port, LoadBalancerProtocolTCP, service, semver.Version{Major: 4, Minor: 22, Patch: 0})
		if err == nil {
			t.Fatalf("expected error for invalid CIDR")
		}
	})
}

func TestSplitCIDRList(t *testing.T) {
	tests := []struct {
		name     string
		cidrList string
		want     []string
	}{
		{name: "empty", cidrList: "", want: nil},
		{name: "single", cidrList: "10.0.0.0/8", want: []string{"10.0.0.0/8"}},
		{
			name:     "comma separated",
			cidrList: "10.0.0.0/8,192.168.0.0/16",
			want:     []string{"10.0.0.0/8", "192.168.0.0/16"},
		},
		{
			name:     "space separated",
			cidrList: "10.0.0.0/8 192.168.0.0/16",
			want:     []string{"10.0.0.0/8", "192.168.0.0/16"},
		},
		{
			name:     "comma and surrounding spaces",
			cidrList: "10.0.0.0/8, 192.168.0.0/16",
			want:     []string{"10.0.0.0/8", "192.168.0.0/16"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitCIDRList(tt.cidrList)
			if len(got) != len(tt.want) {
				t.Fatalf("splitCIDRList(%q) = %v, want %v", tt.cidrList, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("splitCIDRList(%q)[%d] = %q, want %q", tt.cidrList, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestFindLoadBalancerRule(t *testing.T) {
	port80 := corev1.ServicePort{Port: 80, NodePort: 30000, Protocol: corev1.ProtocolTCP}

	// newLB builds a load balancer reconciling towards ip-1, holding the given rules.
	newLB := func(rules ...*cloudstack.LoadBalancerRule) *loadBalancer {
		lb := &loadBalancer{
			ipAddr:   "10.0.0.1",
			ipAddrID: "ip-1",
			rules:    map[string]*cloudstack.LoadBalancerRule{},
		}
		for _, r := range rules {
			lb.rules[r.Name] = r
		}
		return lb
	}
	rule := func(name, protocol, publicPort string) *cloudstack.LoadBalancerRule {
		return &cloudstack.LoadBalancerRule{
			Name: name, Protocol: protocol, Publicport: publicPort,
			Publicip: "10.0.0.1", Publicipid: "ip-1",
		}
	}

	t.Run("exact name match", func(t *testing.T) {
		tcpRule := rule("lb-tcp-80", "tcp", "80")
		lb := newLB(tcpRule)

		if got := lb.findLoadBalancerRule("lb-tcp-80", port80, LoadBalancerProtocolTCP); got != tcpRule {
			t.Fatalf("findLoadBalancerRule = %v, want exact match %v", got, tcpRule)
		}
	})

	t.Run("protocol toggle falls back to IP protocol and port", func(t *testing.T) {
		tcpRule := rule("lb-tcp-80", "tcp", "80")
		lb := newLB(tcpRule)

		if got := lb.findLoadBalancerRule("lb-tcp-proxy-80", port80, LoadBalancerProtocolTCPProxy); got != tcpRule {
			t.Fatalf("findLoadBalancerRule = %v, want fallback match %v", got, tcpRule)
		}
	})

	t.Run("reverse protocol toggle", func(t *testing.T) {
		proxyRule := rule("lb-tcp-proxy-80", "tcp-proxy", "80")
		lb := newLB(proxyRule)

		if got := lb.findLoadBalancerRule("lb-tcp-80", port80, LoadBalancerProtocolTCP); got != proxyRule {
			t.Fatalf("findLoadBalancerRule = %v, want fallback match %v", got, proxyRule)
		}
	})

	t.Run("udp rule does not match tcp port", func(t *testing.T) {
		lb := newLB(rule("lb-udp-80", "udp", "80"))

		if got := lb.findLoadBalancerRule("lb-tcp-80", port80, LoadBalancerProtocolTCP); got != nil {
			t.Fatalf("findLoadBalancerRule = %v, want nil (udp must not satisfy tcp)", got)
		}
	})

	t.Run("tcp and udp on the same port stay distinct", func(t *testing.T) {
		tcpRule := rule("lb-tcp-8000", "tcp", "8000")
		udpRule := rule("lb-udp-8000", "udp", "8000")
		lb := newLB(tcpRule, udpRule)
		port := corev1.ServicePort{Port: 8000, NodePort: 30800, Protocol: corev1.ProtocolUDP}

		if got := lb.findLoadBalancerRule("lb-udp-8000", port, LoadBalancerProtocolUDP); got != udpRule {
			t.Fatalf("findLoadBalancerRule = %v, want %v", got, udpRule)
		}
		// A proxy-protocol toggle on the tcp port must resolve to the tcp rule, never the udp one.
		port.Protocol = corev1.ProtocolTCP
		if got := lb.findLoadBalancerRule("lb-tcp-proxy-8000", port, LoadBalancerProtocolTCPProxy); got != tcpRule {
			t.Fatalf("findLoadBalancerRule = %v, want %v", got, tcpRule)
		}
	})

	t.Run("port mismatch returns nil", func(t *testing.T) {
		lb := newLB(rule("lb-tcp-443", "tcp", "443"))

		if got := lb.findLoadBalancerRule("lb-tcp-80", port80, LoadBalancerProtocolTCP); got != nil {
			t.Fatalf("findLoadBalancerRule = %v, want nil", got)
		}
	})

	t.Run("rule on another IP is not reused", func(t *testing.T) {
		// Reusing a rule on a stale IP would delete it via checkLoadBalancerRule, stranding
		// its firewall rule. It must be left for the prune pass instead.
		staleName := rule("lb-tcp-80", "tcp", "80")
		staleName.Publicip, staleName.Publicipid = "10.0.0.2", "ip-2"
		staleFallback := rule("lb-tcp-proxy-80", "tcp-proxy", "80")
		staleFallback.Publicip, staleFallback.Publicipid = "10.0.0.2", "ip-2"
		lb := newLB(staleName, staleFallback)

		if got := lb.findLoadBalancerRule("lb-tcp-80", port80, LoadBalancerProtocolTCP); got != nil {
			t.Fatalf("findLoadBalancerRule = %v, want nil for a rule on another IP", got)
		}
	})

	t.Run("current IP preferred over exact name on another IP", func(t *testing.T) {
		staleName := rule("lb-tcp-80", "tcp", "80")
		staleName.Publicip, staleName.Publicipid = "10.0.0.2", "ip-2"
		current := rule("lb-tcp-proxy-80", "tcp-proxy", "80")
		lb := newLB(staleName, current)

		// The exact name lives on the stale IP; the fallback must find the current-IP rule.
		if got := lb.findLoadBalancerRule("lb-tcp-80", port80, LoadBalancerProtocolTCP); got != current {
			t.Fatalf("findLoadBalancerRule = %v, want current-IP rule %v", got, current)
		}
	})

	t.Run("multiple candidates picked deterministically", func(t *testing.T) {
		ruleA := rule("lb-tcp-80-a", "tcp", "80")
		ruleB := rule("lb-tcp-80-b", "tcp-proxy", "80")
		lb := newLB(ruleA, ruleB)

		// Both share (tcp, 80); the pick must follow name order, not map order.
		for i := 0; i < 10; i++ {
			if got := lb.findLoadBalancerRule("lb-tcp-80", port80, LoadBalancerProtocolTCP); got != ruleA {
				t.Fatalf("findLoadBalancerRule = %v, want deterministic first-by-name %v", got, ruleA)
			}
		}
	})

	t.Run("empty rules map returns nil", func(t *testing.T) {
		lb := newLB()

		if got := lb.findLoadBalancerRule("lb-tcp-80", port80, LoadBalancerProtocolTCP); got != nil {
			t.Fatalf("findLoadBalancerRule = %v, want nil", got)
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

		err := lb.updateLoadBalancerRule(lb.rules["test-rule-tcp-80"], "test-rule-tcp-80", LoadBalancerProtocolTCP, service, semver.Version{Major: 4, Minor: 22, Patch: 0})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if algo, _ := updateParams.GetAlgorithm(); algo != "source" {
			t.Errorf("algorithm = %q, want %q", algo, "source")
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

		// Matched under the old name, so the update must switch protocol and rename.
		err := lb.updateLoadBalancerRule(lb.rules["test-rule-tcp-80"], "test-rule-tcp-proxy-80", LoadBalancerProtocolTCPProxy, service, semver.Version{Major: 4, Minor: 22, Patch: 0})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if proto, _ := updateParams.GetProtocol(); proto != "tcp-proxy" {
			t.Errorf("protocol = %q, want %q", proto, "tcp-proxy")
		}
		if name, _ := updateParams.GetName(); name != "test-rule-tcp-proxy-80" {
			t.Errorf("name = %q, want %q", name, "test-rule-tcp-proxy-80")
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

		err := lb.updateLoadBalancerRule(lb.rules["test-rule-tcp-80"], "test-rule-tcp-80", LoadBalancerProtocolTCP, service, semver.Version{Major: 4, Minor: 22, Patch: 0})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cidrs, _ := updateParams.GetCidrlist(); len(cidrs) != 1 || cidrs[0] != "10.0.0.0/8" {
			t.Errorf("cidrlist = %v, want %v", cidrs, []string{"10.0.0.0/8"})
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

		err := lb.updateLoadBalancerRule(lb.rules["test-rule-tcp-80"], "test-rule-tcp-80", LoadBalancerProtocolTCP, service, semver.Version{Major: 4, Minor: 22, Patch: 0})
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
			mockNetwork.EXPECT().GetNetworkByID("net-123").Return(networkResp, 1, nil),
			mockNetworkACL.EXPECT().GetNetworkACLListByID("acl-456").Return(aclListResp, 1, nil),
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

	t.Run("tcp-proxy creates ACL rule with tcp protocol", func(t *testing.T) {
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
			mockNetwork.EXPECT().GetNetworkByID("net-123").Return(networkResp, 1, nil),
			mockNetworkACL.EXPECT().GetNetworkACLListByID("acl-456").Return(aclListResp, 1, nil),
			mockNetworkACL.EXPECT().NewListNetworkACLsParams().Return(listParams),
			mockNetworkACL.EXPECT().ListNetworkACLs(gomock.Any()).Return(listResp, nil),
			// tcp-proxy must be created as tcp.
			mockNetworkACL.EXPECT().NewCreateNetworkACLParams("tcp").Return(createParams),
			mockNetworkACL.EXPECT().CreateNetworkACL(gomock.Any()).Return(createResp, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Network:    mockNetwork,
				NetworkACL: mockNetworkACL,
			},
		}

		updated, err := lb.updateNetworkACL(80, LoadBalancerProtocolTCPProxy, "net-123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !updated {
			t.Errorf("updated = false, want true")
		}
	})

	t.Run("tcp-proxy matches existing tcp ACL rule", func(t *testing.T) {
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

		// No create expectations: the tcp rule already satisfies the tcp-proxy port.
		gomock.InOrder(
			mockNetwork.EXPECT().GetNetworkByID("net-123").Return(networkResp, 1, nil),
			mockNetworkACL.EXPECT().GetNetworkACLListByID("acl-456").Return(aclListResp, 1, nil),
			mockNetworkACL.EXPECT().NewListNetworkACLsParams().Return(listParams),
			mockNetworkACL.EXPECT().ListNetworkACLs(gomock.Any()).Return(listResp, nil),
		)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				Network:    mockNetwork,
				NetworkACL: mockNetworkACL,
			},
		}

		updated, err := lb.updateNetworkACL(80, LoadBalancerProtocolTCPProxy, "net-123")
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
			mockNetwork.EXPECT().GetNetworkByID("net-123").Return(networkResp, 1, nil),
			mockNetworkACL.EXPECT().GetNetworkACLListByID("acl-456").Return(aclListResp, 1, nil),
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
			mockNetwork.EXPECT().GetNetworkByID("net-123").Return(networkResp, 1, nil),
			mockNetworkACL.EXPECT().GetNetworkACLListByID("acl-456").Return(aclListResp, 1, nil),
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

		mockNetwork.EXPECT().GetNetworkByID("net-123").Return(nil, 1, apiErr)

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
			mockNetwork.EXPECT().GetNetworkByID("net-123").Return(networkResp, 1, nil),
			mockNetworkACL.EXPECT().GetNetworkACLListByID("acl-456").Return(nil, 0, apiErr),
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
			mockNetwork.EXPECT().GetNetworkByID("net-123").Return(networkResp, 1, nil),
			mockNetworkACL.EXPECT().GetNetworkACLListByID("acl-456").Return(aclListResp, 1, nil),
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
			mockNetwork.EXPECT().GetNetworkByID("net-123").Return(networkResp, 1, nil),
			mockNetworkACL.EXPECT().GetNetworkACLListByID("acl-456").Return(aclListResp, 1, nil),
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
			mockAddress.EXPECT().GetPublicIpAddressByID("ip-123").Return(ipResp, 1, nil),
			mockNetwork.EXPECT().GetNetworkByID("net-123").Return(networkResp, 1, nil),
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

		mockAddress.EXPECT().GetPublicIpAddressByID("ip-123").Return(nil, 0, apiErr)

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

// ensureLBTestEnv holds the fixtures shared by the TestEnsureLoadBalancer subtests.
// Each subtest sets its own mock expectations.
type ensureLBTestEnv struct {
	cs       *CSCloud
	lb       *cloudstack.MockLoadBalancerServiceIface
	vm       *cloudstack.MockVirtualMachineServiceIface
	network  *cloudstack.MockNetworkServiceIface
	firewall *cloudstack.MockFirewallServiceIface
	service  *corev1.Service
	nodes    []*corev1.Node
}

func newEnsureLBTestEnv(ctrl *gomock.Controller, annotations map[string]string, ports []corev1.ServicePort) *ensureLBTestEnv {
	e := &ensureLBTestEnv{
		lb:       cloudstack.NewMockLoadBalancerServiceIface(ctrl),
		vm:       cloudstack.NewMockVirtualMachineServiceIface(ctrl),
		network:  cloudstack.NewMockNetworkServiceIface(ctrl),
		firewall: cloudstack.NewMockFirewallServiceIface(ctrl),
	}

	e.cs = &CSCloud{
		client: &cloudstack.CloudStackClient{
			LoadBalancer:   e.lb,
			VirtualMachine: e.vm,
			Network:        e.network,
			Firewall:       e.firewall,
		},
		version: semver.Version{Major: 4, Minor: 22, Patch: 0},
	}

	// UID "test-uid" makes the load balancer name "atestuid", so rules are atestuid-<protocol>-<port>.
	e.service = &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test-service",
			Namespace:   "default",
			UID:         "test-uid",
			Annotations: annotations,
		},
		Spec: corev1.ServiceSpec{
			SessionAffinity: corev1.ServiceAffinityNone,
			Ports:           ports,
		},
	}

	e.nodes = []*corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}}}

	return e
}

// expectHostsAndNetwork registers the host and network lookups every run performs.
func (e *ensureLBTestEnv) expectHostsAndNetwork() {
	e.vm.EXPECT().NewListVirtualMachinesParams().Return(&cloudstack.ListVirtualMachinesParams{})
	e.vm.EXPECT().ListVirtualMachines(gomock.Any()).Return(&cloudstack.ListVirtualMachinesResponse{
		Count: 1,
		VirtualMachines: []*cloudstack.VirtualMachine{
			{Id: "vm-1", Name: "node-1", Nic: []cloudstack.Nic{{Networkid: "net-1"}}},
		},
	}, nil)
	e.network.EXPECT().GetNetworkByID("net-1", gomock.Any()).Return(&cloudstack.Network{
		Id:      "net-1",
		Service: []cloudstack.NetworkServiceInternal{{Name: "Firewall"}},
	}, 1, nil)
}

func TestEnsureLoadBalancer(t *testing.T) {
	tcpPort80 := corev1.ServicePort{Port: 80, NodePort: 30000, Protocol: corev1.ProtocolTCP}

	existingTCPRule := func() *cloudstack.LoadBalancerRule {
		return &cloudstack.LoadBalancerRule{
			Id:          "rule-1",
			Name:        "atestuid-tcp-80",
			Publicip:    "10.0.0.1",
			Publicipid:  "ip-1",
			Publicport:  "80",
			Privateport: "30000",
			Cidrlist:    defaultAllowedCIDR,
			Algorithm:   "roundrobin",
			Protocol:    "tcp",
			Networkid:   "net-1",
		}
	}

	matchingFirewallRule := &cloudstack.FirewallRule{
		Id:        "fw-1",
		Protocol:  "tcp",
		Startport: 80,
		Endport:   80,
		Cidrlist:  defaultAllowedCIDR,
	}

	t.Run("proxy protocol toggle updates rule in place", func(t *testing.T) {
		// Regression test for issue #2: enabling the annotation on a live service must update
		// the existing tcp rule, not create a conflicting tcp-proxy rule on the same port.
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		env := newEnsureLBTestEnv(ctrl, map[string]string{
			ServiceAnnotationLoadBalancerProxyProtocol: "true",
		}, []corev1.ServicePort{tcpPort80})
		env.expectHostsAndNetwork()

		updateParams := &cloudstack.UpdateLoadBalancerRuleParams{}

		// No create or delete expectations: either call fails the test.
		gomock.InOrder(
			env.lb.EXPECT().NewListLoadBalancerRulesParams().Return(&cloudstack.ListLoadBalancerRulesParams{}),
			env.lb.EXPECT().ListLoadBalancerRules(gomock.Any()).Return(&cloudstack.ListLoadBalancerRulesResponse{
				Count:             1,
				LoadBalancerRules: []*cloudstack.LoadBalancerRule{existingTCPRule()},
			}, nil),
			env.lb.EXPECT().NewUpdateLoadBalancerRuleParams("rule-1").Return(updateParams),
			env.lb.EXPECT().UpdateLoadBalancerRule(gomock.Any()).Return(&cloudstack.UpdateLoadBalancerRuleResponse{}, nil),
		)

		// The existing tcp/80 firewall rule also serves tcp-proxy.
		gomock.InOrder(
			env.firewall.EXPECT().NewListFirewallRulesParams().Return(&cloudstack.ListFirewallRulesParams{}),
			env.firewall.EXPECT().ListFirewallRules(gomock.Any()).Return(&cloudstack.ListFirewallRulesResponse{
				Count:         1,
				FirewallRules: []*cloudstack.FirewallRule{matchingFirewallRule},
			}, nil),
		)

		status, err := env.cs.EnsureLoadBalancer(context.TODO(), "test-cluster", env.service, env.nodes)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(status.Ingress) != 1 || status.Ingress[0].IP != "10.0.0.1" {
			t.Errorf("status.Ingress = %v, want IP 10.0.0.1", status.Ingress)
		}
		if proto, _ := updateParams.GetProtocol(); proto != "tcp-proxy" {
			t.Errorf("updated protocol = %q, want %q", proto, "tcp-proxy")
		}
		if name, _ := updateParams.GetName(); name != "atestuid-tcp-proxy-80" {
			t.Errorf("updated name = %q, want %q", name, "atestuid-tcp-proxy-80")
		}
	})

	t.Run("proxy protocol removal updates rule in place", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		env := newEnsureLBTestEnv(ctrl, nil, []corev1.ServicePort{tcpPort80})
		env.expectHostsAndNetwork()

		existingProxyRule := existingTCPRule()
		existingProxyRule.Name = "atestuid-tcp-proxy-80"
		existingProxyRule.Protocol = "tcp-proxy"

		updateParams := &cloudstack.UpdateLoadBalancerRuleParams{}

		gomock.InOrder(
			env.lb.EXPECT().NewListLoadBalancerRulesParams().Return(&cloudstack.ListLoadBalancerRulesParams{}),
			env.lb.EXPECT().ListLoadBalancerRules(gomock.Any()).Return(&cloudstack.ListLoadBalancerRulesResponse{
				Count:             1,
				LoadBalancerRules: []*cloudstack.LoadBalancerRule{existingProxyRule},
			}, nil),
			env.lb.EXPECT().NewUpdateLoadBalancerRuleParams("rule-1").Return(updateParams),
			env.lb.EXPECT().UpdateLoadBalancerRule(gomock.Any()).Return(&cloudstack.UpdateLoadBalancerRuleResponse{}, nil),
		)

		gomock.InOrder(
			env.firewall.EXPECT().NewListFirewallRulesParams().Return(&cloudstack.ListFirewallRulesParams{}),
			env.firewall.EXPECT().ListFirewallRules(gomock.Any()).Return(&cloudstack.ListFirewallRulesResponse{
				Count:         1,
				FirewallRules: []*cloudstack.FirewallRule{matchingFirewallRule},
			}, nil),
		)

		_, err := env.cs.EnsureLoadBalancer(context.TODO(), "test-cluster", env.service, env.nodes)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if proto, _ := updateParams.GetProtocol(); proto != "tcp" {
			t.Errorf("updated protocol = %q, want %q", proto, "tcp")
		}
		if name, _ := updateParams.GetName(); name != "atestuid-tcp-80" {
			t.Errorf("updated name = %q, want %q", name, "atestuid-tcp-80")
		}
	})

	t.Run("obsolete rule pruned after new rule created", func(t *testing.T) {
		// The service moved from port 80 to 443. The new rule is created first, so a failure
		// while pruning port 80 can never leave the service with no rule at all.
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		tcpPort443 := corev1.ServicePort{Port: 443, NodePort: 30443, Protocol: corev1.ProtocolTCP}
		env := newEnsureLBTestEnv(ctrl, nil, []corev1.ServicePort{tcpPort443})
		env.expectHostsAndNetwork()

		gomock.InOrder(
			env.lb.EXPECT().NewListLoadBalancerRulesParams().Return(&cloudstack.ListLoadBalancerRulesParams{}),
			env.lb.EXPECT().ListLoadBalancerRules(gomock.Any()).Return(&cloudstack.ListLoadBalancerRulesResponse{
				Count:             1,
				LoadBalancerRules: []*cloudstack.LoadBalancerRule{existingTCPRule()},
			}, nil),

			// The port 443 rule is created, then its hosts and firewall reconciled.
			env.lb.EXPECT().NewCreateLoadBalancerRuleParams("roundrobin", "atestuid-tcp-443", 30443, 443).Return(&cloudstack.CreateLoadBalancerRuleParams{}),
			env.lb.EXPECT().CreateLoadBalancerRule(gomock.Any()).Return(&cloudstack.CreateLoadBalancerRuleResponse{
				Id:         "rule-2",
				Name:       "atestuid-tcp-443",
				Publicip:   "10.0.0.1",
				Publicipid: "ip-1",
				Protocol:   "tcp",
			}, nil),
			env.lb.EXPECT().NewAssignToLoadBalancerRuleParams("rule-2").Return(&cloudstack.AssignToLoadBalancerRuleParams{}),
			env.lb.EXPECT().AssignToLoadBalancerRule(gomock.Any()).Return(&cloudstack.AssignToLoadBalancerRuleResponse{}, nil),
			env.firewall.EXPECT().NewListFirewallRulesParams().Return(&cloudstack.ListFirewallRulesParams{}),
			env.firewall.EXPECT().ListFirewallRules(gomock.Any()).Return(&cloudstack.ListFirewallRulesResponse{}, nil),
			env.firewall.EXPECT().NewCreateFirewallRuleParams("ip-1", "tcp").Return(&cloudstack.CreateFirewallRuleParams{}),
			env.firewall.EXPECT().CreateFirewallRule(gomock.Any()).Return(&cloudstack.CreateFirewallRuleResponse{}, nil),

			// Only then is the obsolete port 80 rule pruned: firewall rule, then the LB rule.
			env.firewall.EXPECT().NewListFirewallRulesParams().Return(&cloudstack.ListFirewallRulesParams{}),
			env.firewall.EXPECT().ListFirewallRules(gomock.Any()).Return(&cloudstack.ListFirewallRulesResponse{
				Count:         1,
				FirewallRules: []*cloudstack.FirewallRule{matchingFirewallRule},
			}, nil),
			env.firewall.EXPECT().NewDeleteFirewallRuleParams("fw-1").Return(&cloudstack.DeleteFirewallRuleParams{}),
			env.firewall.EXPECT().DeleteFirewallRule(gomock.Any()).Return(&cloudstack.DeleteFirewallRuleResponse{}, nil),
			env.lb.EXPECT().NewDeleteLoadBalancerRuleParams("rule-1").Return(&cloudstack.DeleteLoadBalancerRuleParams{}),
			env.lb.EXPECT().DeleteLoadBalancerRule(gomock.Any()).Return(&cloudstack.DeleteLoadBalancerRuleResponse{}, nil),
		)

		_, err := env.cs.EnsureLoadBalancer(context.TODO(), "test-cluster", env.service, env.nodes)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("duplicate rule on claimed port keeps firewall rules", func(t *testing.T) {
		// A leftover tcp rule shares (tcp, 80) with the desired tcp-proxy rule. The duplicate
		// is pruned, but its firewall rule is the one the kept rule needs, so it must stay.
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		env := newEnsureLBTestEnv(ctrl, map[string]string{
			ServiceAnnotationLoadBalancerProxyProtocol: "true",
		}, []corev1.ServicePort{tcpPort80})
		env.expectHostsAndNetwork()

		duplicateProxyRule := existingTCPRule()
		duplicateProxyRule.Id = "rule-2"
		duplicateProxyRule.Name = "atestuid-tcp-proxy-80"
		duplicateProxyRule.Protocol = "tcp-proxy"

		gomock.InOrder(
			env.lb.EXPECT().NewListLoadBalancerRulesParams().Return(&cloudstack.ListLoadBalancerRulesParams{}),
			env.lb.EXPECT().ListLoadBalancerRules(gomock.Any()).Return(&cloudstack.ListLoadBalancerRulesResponse{
				Count:             2,
				LoadBalancerRules: []*cloudstack.LoadBalancerRule{existingTCPRule(), duplicateProxyRule},
			}, nil),
			// The obsolete tcp rule is deleted, the tcp-proxy rule is kept as-is.
			env.lb.EXPECT().NewDeleteLoadBalancerRuleParams("rule-1").Return(&cloudstack.DeleteLoadBalancerRuleParams{}),
			env.lb.EXPECT().DeleteLoadBalancerRule(gomock.Any()).Return(&cloudstack.DeleteLoadBalancerRuleResponse{}, nil),
		)

		// One firewall listing (the apply pass) and no deletions.
		gomock.InOrder(
			env.firewall.EXPECT().NewListFirewallRulesParams().Return(&cloudstack.ListFirewallRulesParams{}),
			env.firewall.EXPECT().ListFirewallRules(gomock.Any()).Return(&cloudstack.ListFirewallRulesResponse{
				Count:         1,
				FirewallRules: []*cloudstack.FirewallRule{matchingFirewallRule},
			}, nil),
		)

		_, err := env.cs.EnsureLoadBalancer(context.TODO(), "test-cluster", env.service, env.nodes)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("rule blocking a create is pruned before the create", func(t *testing.T) {
		// Two rules share (tcp, 80) and the nodePort changed, so the matched rule is deleted
		// and has to be recreated. The surviving duplicate still holds public port 80, so it
		// must be deleted before the create or CloudStack rejects it with a port conflict.
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		movedPort80 := corev1.ServicePort{Port: 80, NodePort: 30001, Protocol: corev1.ProtocolTCP}
		env := newEnsureLBTestEnv(ctrl, nil, []corev1.ServicePort{movedPort80})
		env.expectHostsAndNetwork()

		matched := existingTCPRule()
		matched.Id = "rule-a"
		duplicate := existingTCPRule()
		duplicate.Id = "rule-b"
		duplicate.Name = "atestuid-tcp-proxy-80"
		duplicate.Protocol = "tcp-proxy"

		// No firewall delete expectation: the tcp/80 opening is still claimed by the desired
		// port, so pruning the duplicate must leave it alone.
		gomock.InOrder(
			env.lb.EXPECT().NewListLoadBalancerRulesParams().Return(&cloudstack.ListLoadBalancerRulesParams{}),
			env.lb.EXPECT().ListLoadBalancerRules(gomock.Any()).Return(&cloudstack.ListLoadBalancerRulesResponse{
				Count:             2,
				LoadBalancerRules: []*cloudstack.LoadBalancerRule{matched, duplicate},
			}, nil),

			// The matched rule cannot take a new nodePort, so it is dropped while resolving.
			env.lb.EXPECT().NewDeleteLoadBalancerRuleParams("rule-a").Return(&cloudstack.DeleteLoadBalancerRuleParams{}),
			env.lb.EXPECT().DeleteLoadBalancerRule(gomock.Any()).Return(&cloudstack.DeleteLoadBalancerRuleResponse{}, nil),

			// The duplicate is pruned next, freeing public port 80...
			env.lb.EXPECT().NewDeleteLoadBalancerRuleParams("rule-b").Return(&cloudstack.DeleteLoadBalancerRuleParams{}),
			env.lb.EXPECT().DeleteLoadBalancerRule(gomock.Any()).Return(&cloudstack.DeleteLoadBalancerRuleResponse{}, nil),

			// ...so that this create cannot conflict.
			env.lb.EXPECT().NewCreateLoadBalancerRuleParams("roundrobin", "atestuid-tcp-80", 30001, 80).Return(&cloudstack.CreateLoadBalancerRuleParams{}),
			env.lb.EXPECT().CreateLoadBalancerRule(gomock.Any()).Return(&cloudstack.CreateLoadBalancerRuleResponse{
				Id:         "rule-new",
				Name:       "atestuid-tcp-80",
				Publicip:   "10.0.0.1",
				Publicipid: "ip-1",
				Protocol:   "tcp",
			}, nil),
			env.lb.EXPECT().NewAssignToLoadBalancerRuleParams("rule-new").Return(&cloudstack.AssignToLoadBalancerRuleParams{}),
			env.lb.EXPECT().AssignToLoadBalancerRule(gomock.Any()).Return(&cloudstack.AssignToLoadBalancerRuleResponse{}, nil),
			env.firewall.EXPECT().NewListFirewallRulesParams().Return(&cloudstack.ListFirewallRulesParams{}),
			env.firewall.EXPECT().ListFirewallRules(gomock.Any()).Return(&cloudstack.ListFirewallRulesResponse{
				Count:         1,
				FirewallRules: []*cloudstack.FirewallRule{matchingFirewallRule},
			}, nil),
		)

		if _, err := env.cs.EnsureLoadBalancer(context.TODO(), "test-cluster", env.service, env.nodes); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("udp rule blocks a tcp create on the same port", func(t *testing.T) {
		// CloudStack rejects two load balancer rules with overlapping ports on one IP whatever
		// their protocols, so an obsolete udp/80 rule must be pruned before the tcp/80 create
		// even though the two never match each other. Its udp firewall rule is not claimed by
		// the desired tcp port, so that goes too.
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		env := newEnsureLBTestEnv(ctrl, nil, []corev1.ServicePort{tcpPort80})
		env.expectHostsAndNetwork()

		udpRule := existingTCPRule()
		udpRule.Id = "rule-udp"
		udpRule.Name = "atestuid-udp-80"
		udpRule.Protocol = "udp"

		udpFirewallRule := &cloudstack.FirewallRule{
			Id: "fw-udp", Protocol: "udp", Startport: 80, Endport: 80, Cidrlist: defaultAllowedCIDR,
		}

		gomock.InOrder(
			env.lb.EXPECT().NewListLoadBalancerRulesParams().Return(&cloudstack.ListLoadBalancerRulesParams{}),
			env.lb.EXPECT().ListLoadBalancerRules(gomock.Any()).Return(&cloudstack.ListLoadBalancerRulesResponse{
				Count:             1,
				LoadBalancerRules: []*cloudstack.LoadBalancerRule{udpRule},
			}, nil),

			// Prune first: the udp firewall rule, then the udp load balancer rule.
			env.firewall.EXPECT().NewListFirewallRulesParams().Return(&cloudstack.ListFirewallRulesParams{}),
			env.firewall.EXPECT().ListFirewallRules(gomock.Any()).Return(&cloudstack.ListFirewallRulesResponse{
				Count:         1,
				FirewallRules: []*cloudstack.FirewallRule{udpFirewallRule},
			}, nil),
			env.firewall.EXPECT().NewDeleteFirewallRuleParams("fw-udp").Return(&cloudstack.DeleteFirewallRuleParams{}),
			env.firewall.EXPECT().DeleteFirewallRule(gomock.Any()).Return(&cloudstack.DeleteFirewallRuleResponse{}, nil),
			env.lb.EXPECT().NewDeleteLoadBalancerRuleParams("rule-udp").Return(&cloudstack.DeleteLoadBalancerRuleParams{}),
			env.lb.EXPECT().DeleteLoadBalancerRule(gomock.Any()).Return(&cloudstack.DeleteLoadBalancerRuleResponse{}, nil),

			// Only then can the tcp rule be created on the freed port.
			env.lb.EXPECT().NewCreateLoadBalancerRuleParams("roundrobin", "atestuid-tcp-80", 30000, 80).Return(&cloudstack.CreateLoadBalancerRuleParams{}),
			env.lb.EXPECT().CreateLoadBalancerRule(gomock.Any()).Return(&cloudstack.CreateLoadBalancerRuleResponse{
				Id: "rule-tcp", Name: "atestuid-tcp-80", Publicip: "10.0.0.1", Publicipid: "ip-1", Protocol: "tcp",
			}, nil),
			env.lb.EXPECT().NewAssignToLoadBalancerRuleParams("rule-tcp").Return(&cloudstack.AssignToLoadBalancerRuleParams{}),
			env.lb.EXPECT().AssignToLoadBalancerRule(gomock.Any()).Return(&cloudstack.AssignToLoadBalancerRuleResponse{}, nil),
			env.firewall.EXPECT().NewListFirewallRulesParams().Return(&cloudstack.ListFirewallRulesParams{}),
			env.firewall.EXPECT().ListFirewallRules(gomock.Any()).Return(&cloudstack.ListFirewallRulesResponse{}, nil),
			env.firewall.EXPECT().NewCreateFirewallRuleParams("ip-1", "tcp").Return(&cloudstack.CreateFirewallRuleParams{}),
			env.firewall.EXPECT().CreateFirewallRule(gomock.Any()).Return(&cloudstack.CreateFirewallRuleResponse{}, nil),
		)

		if _, err := env.cs.EnsureLoadBalancer(context.TODO(), "test-cluster", env.service, env.nodes); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("unparseable leftover rule does not block reconciliation", func(t *testing.T) {
		// A rule the provider cannot interpret must be skipped, not abort the whole sync:
		// the desired ports still have to be reconciled.
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		env := newEnsureLBTestEnv(ctrl, nil, []corev1.ServicePort{tcpPort80})
		env.expectHostsAndNetwork()

		junkRule := existingTCPRule()
		junkRule.Id = "rule-junk"
		junkRule.Name = "atestuid-http-8080"
		junkRule.Protocol = "http"
		junkRule.Publicport = "8080"

		// No delete expectation for rule-junk: it is skipped, not deleted.
		gomock.InOrder(
			env.lb.EXPECT().NewListLoadBalancerRulesParams().Return(&cloudstack.ListLoadBalancerRulesParams{}),
			env.lb.EXPECT().ListLoadBalancerRules(gomock.Any()).Return(&cloudstack.ListLoadBalancerRulesResponse{
				Count:             2,
				LoadBalancerRules: []*cloudstack.LoadBalancerRule{junkRule, existingTCPRule()},
			}, nil),
		)

		// The desired tcp/80 rule is still reconciled.
		gomock.InOrder(
			env.firewall.EXPECT().NewListFirewallRulesParams().Return(&cloudstack.ListFirewallRulesParams{}),
			env.firewall.EXPECT().ListFirewallRules(gomock.Any()).Return(&cloudstack.ListFirewallRulesResponse{
				Count:         1,
				FirewallRules: []*cloudstack.FirewallRule{matchingFirewallRule},
			}, nil),
		)

		if _, err := env.cs.EnsureLoadBalancer(context.TODO(), "test-cluster", env.service, env.nodes); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("prune failure still reconciles desired rules", func(t *testing.T) {
		// A failed delete is still reported, even though the desired rules were applied fine.
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		env := newEnsureLBTestEnv(ctrl, nil, []corev1.ServicePort{tcpPort80})
		env.expectHostsAndNetwork()

		obsolete := existingTCPRule()
		obsolete.Id = "rule-obsolete"
		obsolete.Name = "atestuid-tcp-8080"
		obsolete.Publicport = "8080"

		deleteErr := fmt.Errorf("delete rule API error")

		gomock.InOrder(
			env.lb.EXPECT().NewListLoadBalancerRulesParams().Return(&cloudstack.ListLoadBalancerRulesParams{}),
			env.lb.EXPECT().ListLoadBalancerRules(gomock.Any()).Return(&cloudstack.ListLoadBalancerRulesResponse{
				Count:             2,
				LoadBalancerRules: []*cloudstack.LoadBalancerRule{obsolete, existingTCPRule()},
			}, nil),
			env.lb.EXPECT().NewDeleteLoadBalancerRuleParams("rule-obsolete").Return(&cloudstack.DeleteLoadBalancerRuleParams{}),
			env.lb.EXPECT().DeleteLoadBalancerRule(gomock.Any()).Return(nil, deleteErr),
		)

		gomock.InOrder(
			// Apply pass: the desired tcp/80 firewall rule already matches.
			env.firewall.EXPECT().NewListFirewallRulesParams().Return(&cloudstack.ListFirewallRulesParams{}),
			env.firewall.EXPECT().ListFirewallRules(gomock.Any()).Return(&cloudstack.ListFirewallRulesResponse{
				Count:         1,
				FirewallRules: []*cloudstack.FirewallRule{matchingFirewallRule},
			}, nil),
			// Prune pass: nothing matches the obsolete port 8080.
			env.firewall.EXPECT().NewListFirewallRulesParams().Return(&cloudstack.ListFirewallRulesParams{}),
			env.firewall.EXPECT().ListFirewallRules(gomock.Any()).Return(&cloudstack.ListFirewallRulesResponse{}, nil),
		)

		_, err := env.cs.EnsureLoadBalancer(context.TODO(), "test-cluster", env.service, env.nodes)
		if err == nil {
			t.Fatalf("expected the prune failure to be reported")
		}
		if !strings.Contains(err.Error(), "delete rule API error") {
			t.Errorf("error = %v, want it to mention the delete failure", err)
		}
	})

	t.Run("obsolete rule on another IP has firewall rules deleted", func(t *testing.T) {
		// An obsolete rule on another public IP shares (tcp, 80) with a desired port. Claims
		// are per IP, so the old IP's firewall rule must still be deleted.
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		env := newEnsureLBTestEnv(ctrl, nil, []corev1.ServicePort{tcpPort80})
		env.expectHostsAndNetwork()

		oldIPRule := existingTCPRule()
		oldIPRule.Id = "rule-9"
		oldIPRule.Name = "atestuid-tcp-80-old"
		oldIPRule.Publicip = "10.0.0.2"
		oldIPRule.Publicipid = "ip-2"

		oldIPFirewallRule := &cloudstack.FirewallRule{
			Id:        "fw-2",
			Protocol:  "tcp",
			Startport: 80,
			Endport:   80,
			Cidrlist:  defaultAllowedCIDR,
		}

		gomock.InOrder(
			env.lb.EXPECT().NewListLoadBalancerRulesParams().Return(&cloudstack.ListLoadBalancerRulesParams{}),
			// Listed last so the load balancer resolves to ip-1.
			env.lb.EXPECT().ListLoadBalancerRules(gomock.Any()).Return(&cloudstack.ListLoadBalancerRulesResponse{
				Count:             2,
				LoadBalancerRules: []*cloudstack.LoadBalancerRule{oldIPRule, existingTCPRule()},
			}, nil),
			env.lb.EXPECT().NewDeleteLoadBalancerRuleParams("rule-9").Return(&cloudstack.DeleteLoadBalancerRuleParams{}),
			env.lb.EXPECT().DeleteLoadBalancerRule(gomock.Any()).Return(&cloudstack.DeleteLoadBalancerRuleResponse{}, nil),
		)

		gomock.InOrder(
			// Apply pass: the kept rule's firewall rule already matches.
			env.firewall.EXPECT().NewListFirewallRulesParams().Return(&cloudstack.ListFirewallRulesParams{}),
			env.firewall.EXPECT().ListFirewallRules(gomock.Any()).Return(&cloudstack.ListFirewallRulesResponse{
				Count:         1,
				FirewallRules: []*cloudstack.FirewallRule{matchingFirewallRule},
			}, nil),
			// Prune pass: the old IP's rule is unclaimed, so it is deleted.
			env.firewall.EXPECT().NewListFirewallRulesParams().Return(&cloudstack.ListFirewallRulesParams{}),
			env.firewall.EXPECT().ListFirewallRules(gomock.Any()).Return(&cloudstack.ListFirewallRulesResponse{
				Count:         1,
				FirewallRules: []*cloudstack.FirewallRule{oldIPFirewallRule},
			}, nil),
			env.firewall.EXPECT().NewDeleteFirewallRuleParams("fw-2").Return(&cloudstack.DeleteFirewallRuleParams{}),
			env.firewall.EXPECT().DeleteFirewallRule(gomock.Any()).Return(&cloudstack.DeleteFirewallRuleResponse{}, nil),
		)

		_, err := env.cs.EnsureLoadBalancer(context.TODO(), "test-cluster", env.service, env.nodes)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
