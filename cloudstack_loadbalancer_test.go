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
