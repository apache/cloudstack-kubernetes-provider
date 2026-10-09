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
	"maps"
	"reflect"
	"slices"
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
			// Paging over a changing result set can return the same instance on
			// two pages. A wanted host must not end up in remove because of it.
			name:    "duplicate instance of a wanted host",
			hostIDs: []string{"host1", "host2"},
			lbInstances: []*cloudstack.VirtualMachine{
				{Id: "host1"},
				{Id: "host2"},
				{Id: "host1"},
			},
			wantAssign: nil,
			wantRemove: nil,
		},
		{
			name:    "duplicate instance of an unwanted host",
			hostIDs: []string{"host1"},
			lbInstances: []*cloudstack.VirtualMachine{
				{Id: "host1"},
				{Id: "host2"},
				{Id: "host2"},
			},
			wantAssign: nil,
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
	port := corev1.ServicePort{Port: 80, NodePort: 30000, Protocol: corev1.ProtocolTCP}
	belowCIDRUpdate := semver.MustParse("4.21.0")
	withCIDRUpdate := semver.MustParse("4.22.0")

	existingRule := func(edit func(*cloudstack.LoadBalancerRule)) *cloudstack.LoadBalancerRule {
		lbRule := &cloudstack.LoadBalancerRule{
			Id:          "rule-id",
			Name:        "rule",
			Publicip:    "1.1.1.1",
			Privateport: "30000",
			Publicport:  "80",
			Cidrlist:    defaultAllowedCIDR,
			Algorithm:   "roundrobin",
			Protocol:    LoadBalancerProtocolTCP.CSProtocol(),
		}
		if edit != nil {
			edit(lbRule)
		}
		return lbRule
	}
	unrestricted := &corev1.Service{}
	restrictedTo := func(cidrs string) *corev1.Service {
		return &corev1.Service{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			ServiceAnnotationLoadBalancerSourceCidrs: cidrs,
		}}}
	}

	tests := []struct {
		name     string
		existing *cloudstack.LoadBalancerRule
		ruleName string
		protocol LoadBalancerProtocol
		service  *corev1.Service
		version  semver.Version
		want     ruleChange
	}{
		{
			name:    "no existing rule is created",
			service: unrestricted,
			version: withCIDRUpdate,
			want:    ruleMissing,
		},
		{
			name:     "matching rule is left alone",
			existing: existingRule(nil),
			service:  unrestricted,
			version:  withCIDRUpdate,
			want:     ruleUpToDate,
		},
		{
			name:     "rule on another public IP is recreated",
			existing: existingRule(func(r *cloudstack.LoadBalancerRule) { r.Publicip = "2.2.2.2" }),
			service:  unrestricted,
			version:  withCIDRUpdate,
			want:     ruleNeedsRecreate,
		},
		{
			name:     "changed node port is recreated",
			existing: existingRule(func(r *cloudstack.LoadBalancerRule) { r.Privateport = "30001" }),
			service:  unrestricted,
			version:  withCIDRUpdate,
			want:     ruleNeedsRecreate,
		},
		{
			name:     "changed algorithm is updated",
			existing: existingRule(func(r *cloudstack.LoadBalancerRule) { r.Algorithm = "source" }),
			service:  unrestricted,
			version:  withCIDRUpdate,
			want:     ruleNeedsUpdate,
		},
		{
			name:     "protocol change is updated in place",
			existing: existingRule(func(r *cloudstack.LoadBalancerRule) { r.Name = "rule-tcp-80" }),
			ruleName: "rule-tcp-proxy-80",
			protocol: LoadBalancerProtocolTCPProxy,
			service:  unrestricted,
			version:  withCIDRUpdate,
			want:     ruleNeedsUpdate,
		},
		{
			name:     "cidr change is updated where the API accepts a cidrlist",
			existing: existingRule(func(r *cloudstack.LoadBalancerRule) { r.Cidrlist = "10.0.0.0/8" }),
			service:  restrictedTo("10.0.0.0/8,192.168.0.0/16"),
			version:  withCIDRUpdate,
			want:     ruleNeedsUpdate,
		},
		// CloudStack moves from 4.x to 24.0 after 4.23, so the 4.22 feature gate has
		// to keep treating the new numbering as newer rather than older.
		{
			name:     "cidr change is updated on the 24.0 series",
			existing: existingRule(func(r *cloudstack.LoadBalancerRule) { r.Cidrlist = "10.0.0.0/8" }),
			service:  restrictedTo("10.0.0.0/8,192.168.0.0/16"),
			version:  semver.MustParse("24.0.0"),
			want:     ruleNeedsUpdate,
		},
		{
			name:     "cidr change is recreated below the cidrlist update release",
			existing: existingRule(func(r *cloudstack.LoadBalancerRule) { r.Cidrlist = "10.0.0.0/8" }),
			service:  restrictedTo("10.0.0.0/8,192.168.0.0/16"),
			version:  belowCIDRUpdate,
			want:     ruleNeedsRecreate,
		},
		{
			name:     "matching multi-CIDR list is left alone",
			existing: existingRule(func(r *cloudstack.LoadBalancerRule) { r.Cidrlist = "10.0.0.0/8,192.168.0.0/16" }),
			service:  restrictedTo("10.0.0.0/8,192.168.0.0/16"),
			version:  withCIDRUpdate,
			want:     ruleUpToDate,
		},
		{
			name:     "rule with no cidrlist matches an unrestricted service",
			existing: existingRule(func(r *cloudstack.LoadBalancerRule) { r.Cidrlist = "" }),
			service:  unrestricted,
			version:  belowCIDRUpdate,
			want:     ruleUpToDate,
		},
		{
			name:     "rule with no cidrlist is recreated for a restricted service below the update release",
			existing: existingRule(func(r *cloudstack.LoadBalancerRule) { r.Cidrlist = "" }),
			service:  restrictedTo("10.0.0.0/8"),
			version:  belowCIDRUpdate,
			want:     ruleNeedsRecreate,
		},
		{
			name:     "rule with no cidrlist is updated for a restricted service",
			existing: existingRule(func(r *cloudstack.LoadBalancerRule) { r.Cidrlist = "" }),
			service:  restrictedTo("10.0.0.0/8"),
			version:  withCIDRUpdate,
			want:     ruleNeedsUpdate,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			t.Cleanup(ctrl.Finish)

			// No expectations: deciding a change must not call CloudStack.
			lb := &loadBalancer{
				CloudStackClient: &cloudstack.CloudStackClient{LoadBalancer: cloudstack.NewMockLoadBalancerServiceIface(ctrl)},
				ipAddr:           "1.1.1.1",
				algorithm:        "roundrobin",
			}
			ruleName := tt.ruleName
			if ruleName == "" {
				ruleName = "rule"
			}

			got, err := lb.checkLoadBalancerRule(tt.existing, ruleName, port, tt.protocol, tt.service, tt.version)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("checkLoadBalancerRule = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("invalid cidr returns error", func(t *testing.T) {
		lb := &loadBalancer{ipAddr: "1.1.1.1", algorithm: "roundrobin"}

		if _, err := lb.checkLoadBalancerRule(existingRule(nil), "rule", port, LoadBalancerProtocolTCP, restrictedTo("bad-cidr"), withCIDRUpdate); err == nil {
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

	// The release after 4.23 is numbered 24.0, which must still reach the
	// in-place CIDR update rather than falling back to delete-and-recreate.
	t.Run("update CIDR list on the 24.0 series", func(t *testing.T) {
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

		if err := lb.updateLoadBalancerRule(lb.rules["test-rule-tcp-80"], "test-rule-tcp-80", LoadBalancerProtocolTCP, service, semver.MustParse("24.0.0")); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		cidrList, ok := updateParams.GetCidrlist()
		if !ok {
			t.Fatalf("expected the CIDR list to be set on the update params")
		}
		if len(cidrList) != 1 || cidrList[0] != "10.0.0.0/8" {
			t.Fatalf("cidrlist = %v, want [10.0.0.0/8]", cidrList)
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

func TestListFirewallRulesDeduplicates(t *testing.T) {
	// Each page decodes into its own structs, so a rule returned on two pages
	// arrives as two pointers to an equal rule. updateFirewallRule keys its
	// bookkeeping on the pointer: it would keep one copy as the CIDR match and
	// delete the other by ID, removing the very rule it had just matched and
	// creating nothing in its place.
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	rule := func() *cloudstack.FirewallRule {
		return &cloudstack.FirewallRule{
			Id: "fw-keep", Protocol: "tcp", Startport: 80, Endport: 80,
			Cidrlist: defaultAllowedCIDR,
		}
	}

	mockFirewall := cloudstack.NewMockFirewallServiceIface(ctrl)
	mockFirewall.EXPECT().NewListFirewallRulesParams().
		Return(&cloudstack.ListFirewallRulesParams{})

	// Count of 2 with one rule per page makes listAll page, and the same rule
	// comes back both times. It must be built per call: a real second page is
	// decoded into its own struct, so the repeat is a distinct pointer.
	mockFirewall.EXPECT().ListFirewallRules(gomock.Any()).Times(2).
		DoAndReturn(func(p *cloudstack.ListFirewallRulesParams) (*cloudstack.ListFirewallRulesResponse, error) {
			return &cloudstack.ListFirewallRulesResponse{
				Count:         2,
				FirewallRules: []*cloudstack.FirewallRule{rule()},
			}, nil
		})

	var deleted []string
	mockFirewall.EXPECT().NewDeleteFirewallRuleParams(gomock.Any()).AnyTimes().
		DoAndReturn(func(id string) *cloudstack.DeleteFirewallRuleParams {
			deleted = append(deleted, id)
			return &cloudstack.DeleteFirewallRuleParams{}
		})
	mockFirewall.EXPECT().DeleteFirewallRule(gomock.Any()).AnyTimes().
		Return(&cloudstack.DeleteFirewallRuleResponse{}, nil)

	created := 0
	mockFirewall.EXPECT().NewCreateFirewallRuleParams(gomock.Any(), gomock.Any()).AnyTimes().
		DoAndReturn(func(ip, proto string) *cloudstack.CreateFirewallRuleParams {
			created++
			return &cloudstack.CreateFirewallRuleParams{}
		})
	mockFirewall.EXPECT().CreateFirewallRule(gomock.Any()).AnyTimes().
		Return(&cloudstack.CreateFirewallRuleResponse{}, nil)

	lb := &loadBalancer{
		CloudStackClient: &cloudstack.CloudStackClient{Firewall: mockFirewall},
		ipAddr:           "203.0.113.1",
	}

	// The existing rule already allows exactly what is wanted, so nothing should
	// be deleted and nothing created.
	if _, err := lb.updateFirewallRule("ip-123", 80, LoadBalancerProtocolTCP, []string{defaultAllowedCIDR}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(deleted) > 0 {
		t.Errorf("deleted %v, but that rule already matched the wanted CIDR", deleted)
	}
	if created > 0 {
		t.Errorf("created %d replacement rules, want 0", created)
	}
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

// testACLReason marks a Network ACL rule as owned by the test load balancer "atestuid".
const testACLReason = networkACLReasonPrefix + "atestuid"

// legacyACLRule is an ingress tcp rule for port 80 shaped as earlier releases created it. edit
// turns it into the kind of rule a test needs.
func legacyACLRule(id string, edit func(*cloudstack.NetworkACL)) *cloudstack.NetworkACL {
	aclRule := &cloudstack.NetworkACL{
		Id:          id,
		Protocol:    "tcp",
		Startport:   "80",
		Endport:     "80",
		Traffictype: "Ingress",
		Action:      "Allow",
		Cidrlist:    defaultAllowedCIDR,
		State:       "Active",
	}
	if edit != nil {
		edit(aclRule)
	}
	return aclRule
}

// ownACLRule is the tcp/80 rule of the test load balancer "atestuid".
func ownACLRule(id string) *cloudstack.NetworkACL {
	return legacyACLRule(id, func(r *cloudstack.NetworkACL) { r.Reason = testACLReason })
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
			name: "atestuid",
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
		if reason, _ := createParams.GetReason(); reason != testACLReason {
			t.Errorf("reason = %q, want %q", reason, testACLReason)
		}
		if cidrs, _ := createParams.GetCidrlist(); !compareStringSlice(cidrs, []string{defaultAllowedCIDR}) {
			t.Errorf("cidrlist = %v, want [%v]", cidrs, defaultAllowedCIDR)
		}
		if trafficType, _ := createParams.GetTraffictype(); trafficType != "Ingress" {
			t.Errorf("traffictype = %q, want Ingress", trafficType)
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
			mockNetwork.EXPECT().GetNetworkByID("net-123", gomock.Any()).Return(networkResp, 1, nil),
			mockNetworkACL.EXPECT().GetNetworkACLListByID("acl-456", gomock.Any()).Return(aclListResp, 1, nil),
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

	t.Run("tcp-proxy matches this service's tcp ACL rule", func(t *testing.T) {
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
				ownACLRule("acl-rule-123"),
			},
		}

		// No create expectations: the tcp rule already satisfies the tcp-proxy port.
		gomock.InOrder(
			mockNetwork.EXPECT().GetNetworkByID("net-123", gomock.Any()).Return(networkResp, 1, nil),
			mockNetworkACL.EXPECT().GetNetworkACLListByID("acl-456", gomock.Any()).Return(aclListResp, 1, nil),
			mockNetworkACL.EXPECT().NewListNetworkACLsParams().Return(listParams),
			mockNetworkACL.EXPECT().ListNetworkACLs(gomock.Any()).Return(listResp, nil),
		)

		lb := &loadBalancer{
			name: "atestuid",
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

	t.Run("this service's rule already exists", func(t *testing.T) {
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
				ownACLRule("acl-rule-123"),
			},
		}

		gomock.InOrder(
			mockNetwork.EXPECT().GetNetworkByID("net-123", gomock.Any()).Return(networkResp, 1, nil),
			mockNetworkACL.EXPECT().GetNetworkACLListByID("acl-456", gomock.Any()).Return(aclListResp, 1, nil),
			mockNetworkACL.EXPECT().NewListNetworkACLsParams().Return(listParams),
			mockNetworkACL.EXPECT().ListNetworkACLs(gomock.Any()).Return(listResp, nil),
		)

		lb := &loadBalancer{
			name: "atestuid",
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
	t.Run("delete this service's rule", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockNetworkACL := cloudstack.NewMockNetworkACLServiceIface(ctrl)
		listParams := &cloudstack.ListNetworkACLsParams{}
		listResp := &cloudstack.ListNetworkACLsResponse{
			Count: 1,
			NetworkACLs: []*cloudstack.NetworkACL{
				ownACLRule("acl-rule-123"),
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
			name: "atestuid",
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
				ownACLRule("acl-rule-123"),
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
			name: "atestuid",
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

func TestClassifyNetworkACLRule(t *testing.T) {
	tests := []struct {
		name string
		edit func(*cloudstack.NetworkACL)
		want networkACLRuleKind
	}{
		{"this service's rule", func(r *cloudstack.NetworkACL) { r.Reason = testACLReason }, aclRuleOwn},
		{"this service's rule being deleted", func(r *cloudstack.NetworkACL) { r.Reason = testACLReason; r.State = "Deleting" }, aclRuleIgnored},
		{"this service's rule with a CIDR edited by hand", func(r *cloudstack.NetworkACL) { r.Reason = testACLReason; r.Cidrlist = "10.0.0.0/8" }, aclRuleOwn},
		{"another service's rule", func(r *cloudstack.NetworkACL) { r.Reason = networkACLReasonPrefix + "aotheruid" }, aclRuleIgnored},
		{"a rule from an earlier release", nil, aclRuleLegacy},
		{"a rule from an earlier release being deleted", func(r *cloudstack.NetworkACL) { r.State = "Deleting" }, aclRuleIgnored},
		{"a restricted CIDR", func(r *cloudstack.NetworkACL) { r.Cidrlist = "10.0.0.0/8" }, aclRuleOperator},
		{"an IPv6 range as well", func(r *cloudstack.NetworkACL) { r.Cidrlist = "0.0.0.0/0,::/0" }, aclRuleOperator},
		{"a deny rule", func(r *cloudstack.NetworkACL) { r.Action = "Deny" }, aclRuleOperator},
		{"a reason of its own", func(r *cloudstack.NetworkACL) { r.Reason = "allow https" }, aclRuleOperator},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyNetworkACLRule(legacyACLRule("acl-1", tt.edit), testACLReason); got != tt.want {
				t.Errorf("classifyNetworkACLRule() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestListNetworkACLRules(t *testing.T) {
	t.Run("keeps ingress rules for exactly the port and protocol", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockNetworkACL := cloudstack.NewMockNetworkACLServiceIface(ctrl)
		listParams := &cloudstack.ListNetworkACLsParams{}
		mockNetworkACL.EXPECT().NewListNetworkACLsParams().Return(listParams)
		mockNetworkACL.EXPECT().ListNetworkACLs(listParams).Return(&cloudstack.ListNetworkACLsResponse{
			Count: 8,
			NetworkACLs: []*cloudstack.NetworkACL{
				legacyACLRule("tcp-80", nil),
				legacyACLRule("upper-case-tcp-80", func(r *cloudstack.NetworkACL) { r.Protocol = "TCP" }),
				legacyACLRule("deleting-tcp-80", func(r *cloudstack.NetworkACL) { r.State = "Deleting" }),
				legacyACLRule("egress-tcp-80", func(r *cloudstack.NetworkACL) { r.Traffictype = "Egress" }),
				legacyACLRule("udp-80", func(r *cloudstack.NetworkACL) { r.Protocol = "udp" }),
				legacyACLRule("tcp-81", func(r *cloudstack.NetworkACL) { r.Startport, r.Endport = "81", "81" }),
				legacyACLRule("tcp-80-81", func(r *cloudstack.NetworkACL) { r.Endport = "81" }),
				legacyACLRule("all", func(r *cloudstack.NetworkACL) { r.Protocol, r.Startport, r.Endport = "all", "", "" }),
			},
		}, nil)

		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{NetworkACL: mockNetworkACL},
			projectID:        "proj-1",
		}

		aclRules, err := lb.listNetworkACLRules("net-1", "tcp", 80)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var ids []string
		for _, aclRule := range aclRules {
			ids = append(ids, aclRule.Id)
		}
		if want := []string{"tcp-80", "deleting-tcp-80"}; !compareStringSlice(ids, want) {
			t.Errorf("rules = %v, want %v", ids, want)
		}
		if networkID, _ := listParams.GetNetworkid(); networkID != "net-1" {
			t.Errorf("networkid = %q, want net-1", networkID)
		}
		if listAll, _ := listParams.GetListall(); !listAll {
			t.Errorf("listall not set")
		}
		if projectID, _ := listParams.GetProjectid(); projectID != "proj-1" {
			t.Errorf("projectid = %q, want proj-1", projectID)
		}
	})

	t.Run("a rule repeated across pages is returned once", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		var all []*cloudstack.NetworkACL
		for i := 0; i < 501; i++ {
			all = append(all, legacyACLRule(fmt.Sprintf("acl-%d", i), func(r *cloudstack.NetworkACL) { r.Startport, r.Endport = "81", "81" }))
		}
		all[499] = ownACLRule("acl-own")
		all[500] = ownACLRule("acl-own")

		mockNetworkACL := cloudstack.NewMockNetworkACLServiceIface(ctrl)
		listParams := &cloudstack.ListNetworkACLsParams{}
		mockNetworkACL.EXPECT().NewListNetworkACLsParams().Return(listParams)
		mockNetworkACL.EXPECT().ListNetworkACLs(listParams).DoAndReturn(func(p *cloudstack.ListNetworkACLsParams) (*cloudstack.ListNetworkACLsResponse, error) {
			return &cloudstack.ListNetworkACLsResponse{Count: len(all), NetworkACLs: pageOf(t, p, all, 500)}, nil
		}).Times(2)

		lb := &loadBalancer{CloudStackClient: &cloudstack.CloudStackClient{NetworkACL: mockNetworkACL}}

		aclRules, err := lb.listNetworkACLRules("net-1", "tcp", 80)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(aclRules) != 1 {
			t.Errorf("rules = %d, want acl-own once", len(aclRules))
		}
	})

	t.Run("pages through every rule of the tier once", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		var all []*cloudstack.NetworkACL
		for i := 0; i < 600; i++ {
			all = append(all, legacyACLRule(fmt.Sprintf("acl-%d", i), func(r *cloudstack.NetworkACL) { r.Startport, r.Endport = "81", "81" }))
		}
		all[550] = ownACLRule("acl-own")

		mockNetworkACL := cloudstack.NewMockNetworkACLServiceIface(ctrl)
		listParams := &cloudstack.ListNetworkACLsParams{}
		mockNetworkACL.EXPECT().NewListNetworkACLsParams().Return(listParams)
		mockNetworkACL.EXPECT().ListNetworkACLs(listParams).DoAndReturn(func(p *cloudstack.ListNetworkACLsParams) (*cloudstack.ListNetworkACLsResponse, error) {
			return &cloudstack.ListNetworkACLsResponse{Count: len(all), NetworkACLs: pageOf(t, p, all, 500)}, nil
		}).Times(2)

		lb := &loadBalancer{CloudStackClient: &cloudstack.CloudStackClient{NetworkACL: mockNetworkACL}}

		aclRules, err := lb.listNetworkACLRules("net-1", "tcp", 80)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(aclRules) != 1 || aclRules[0].Id != "acl-own" {
			t.Errorf("rules = %v, want only acl-own from the second page", aclRules)
		}
	})
}

// sentUpdateFields names the fields of an updateNetworkACLItem call that were set, apart from
// the id, so a test can assert that an adoption sends nothing but the reason.
func sentUpdateFields(p *cloudstack.UpdateNetworkACLItemParams) []string {
	var sent []string
	set := func(name string, ok bool) {
		if ok {
			sent = append(sent, name)
		}
	}
	_, ok := p.GetAction()
	set("action", ok)
	_, ok = p.GetCidrlist()
	set("cidrlist", ok)
	_, ok = p.GetCustomid()
	set("customid", ok)
	_, ok = p.GetEndport()
	set("endport", ok)
	_, ok = p.GetFordisplay()
	set("fordisplay", ok)
	_, ok = p.GetIcmpcode()
	set("icmpcode", ok)
	_, ok = p.GetIcmptype()
	set("icmptype", ok)
	_, ok = p.GetNumber()
	set("number", ok)
	_, ok = p.GetPartialupgrade()
	set("partialupgrade", ok)
	_, ok = p.GetProtocol()
	set("protocol", ok)
	_, ok = p.GetReason()
	set("reason", ok)
	_, ok = p.GetStartport()
	set("startport", ok)
	_, ok = p.GetTraffictype()
	set("traffictype", ok)
	return sent
}

// Regression test for issue #107: a Network ACL rule is owned by one Service, so a rule of
// another Service, or one an operator made, never stands in for this Service's own rule.
func TestUpdateNetworkACLOwnership(t *testing.T) {
	notAllowed := fmt.Errorf("CloudStack API error 432 (CSExceptionErrorCode: 9999): The API [updateNetworkACLItem] does not exist or is not available for the account")
	otherService := func(r *cloudstack.NetworkACL) { r.Reason = networkACLReasonPrefix + "aotheruid" }

	tests := []struct {
		name       string
		rules      []*cloudstack.NetworkACL
		protocol   LoadBalancerProtocol
		globalList bool
		adopt      string
		adoptErr   error
		wantCreate bool
		createErr  error
		wantErr    string
	}{
		{name: "creates its own rule next to another service's rule", rules: []*cloudstack.NetworkACL{legacyACLRule("acl-other", otherService)}, wantCreate: true},
		{name: "an egress rule does not open the port", rules: []*cloudstack.NetworkACL{legacyACLRule("acl-egress", func(r *cloudstack.NetworkACL) { r.Traffictype = "Egress" })}, wantCreate: true},
		{name: "its own rule being deleted does not open the port", rules: []*cloudstack.NetworkACL{legacyACLRule("acl-own", func(r *cloudstack.NetworkACL) { r.Reason = testACLReason; r.State = "Deleting" })}, wantCreate: true},
		{name: "adopts a rule from an earlier release", rules: []*cloudstack.NetworkACL{legacyACLRule("acl-legacy", nil)}, adopt: "acl-legacy"},
		{name: "adopts only one of several rules from an earlier release", rules: []*cloudstack.NetworkACL{legacyACLRule("acl-legacy-1", nil), legacyACLRule("acl-legacy-2", nil)}, adopt: "acl-legacy-1"},
		{name: "keeps its own rule rather than adopting another", rules: []*cloudstack.NetworkACL{legacyACLRule("acl-legacy", nil), ownACLRule("acl-own")}},
		{name: "adopts a rule from an earlier release before deferring to an operator", rules: []*cloudstack.NetworkACL{legacyACLRule("acl-operator", func(r *cloudstack.NetworkACL) { r.Cidrlist = "10.0.0.0/8" }), legacyACLRule("acl-legacy", nil)}, adopt: "acl-legacy"},
		{name: "creates its own rule when adopting is not allowed", rules: []*cloudstack.NetworkACL{legacyACLRule("acl-legacy", nil)}, adopt: "acl-legacy", adoptErr: notAllowed, wantCreate: true},
		{name: "keeps relying on the old rule when adopting and creating are both refused", rules: []*cloudstack.NetworkACL{legacyACLRule("acl-legacy", nil)}, adopt: "acl-legacy", adoptErr: notAllowed, wantCreate: true, createErr: fmt.Errorf("CloudStack API error 432 (CSExceptionErrorCode: 9999): The API [createNetworkACL] does not exist or is not available for the account")},
		{name: "adopts a udp rule from an earlier release", protocol: LoadBalancerProtocolUDP, rules: []*cloudstack.NetworkACL{legacyACLRule("acl-legacy-udp", func(r *cloudstack.NetworkACL) { r.Protocol = "udp" })}, adopt: "acl-legacy-udp"},
		{name: "creates a udp rule next to a tcp rule on the same port", protocol: LoadBalancerProtocolUDP, rules: []*cloudstack.NetworkACL{legacyACLRule("acl-legacy-tcp", nil)}, wantCreate: true},
		{name: "fails when adopting fails otherwise", rules: []*cloudstack.NetworkACL{legacyACLRule("acl-legacy", nil)}, adopt: "acl-legacy", adoptErr: fmt.Errorf("router unreachable"), wantErr: "error adopting Network ACL rule acl-legacy"},
		{name: "leaves the port to an operator rule with a restricted CIDR", rules: []*cloudstack.NetworkACL{legacyACLRule("acl-operator", func(r *cloudstack.NetworkACL) { r.Cidrlist = "10.0.0.0/8" })}},
		{name: "adds its own rule next to an upper case TCP rule, as earlier releases did", rules: []*cloudstack.NetworkACL{legacyACLRule("acl-cks", func(r *cloudstack.NetworkACL) { r.Protocol = "TCP"; r.Cidrlist = "0.0.0.0/0,::/0" })}, wantCreate: true},
		{name: "leaves the port to an operator deny rule", rules: []*cloudstack.NetworkACL{legacyACLRule("acl-deny", func(r *cloudstack.NetworkACL) { r.Action = "Deny" })}},
		{name: "never adopts in a global ACL list", rules: []*cloudstack.NetworkACL{legacyACLRule("acl-legacy", nil)}, globalList: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			t.Cleanup(ctrl.Finish)

			listVpcID := "vpc-1"
			if tt.globalList {
				listVpcID = ""
			}

			mockNetwork := cloudstack.NewMockNetworkServiceIface(ctrl)
			mockNetworkACL := cloudstack.NewMockNetworkACLServiceIface(ctrl)
			mockNetwork.EXPECT().GetNetworkByID("net-1", gomock.Any()).Return(&cloudstack.Network{Id: "net-1", Aclid: "acl-1"}, 1, nil)
			mockNetworkACL.EXPECT().GetNetworkACLListByID("acl-1", gomock.Any()).Return(&cloudstack.NetworkACLList{Id: "acl-1", Name: "tier-acl", Vpcid: listVpcID}, 1, nil)
			mockNetworkACL.EXPECT().NewListNetworkACLsParams().Return(&cloudstack.ListNetworkACLsParams{})
			mockNetworkACL.EXPECT().ListNetworkACLs(gomock.Any()).Return(&cloudstack.ListNetworkACLsResponse{Count: len(tt.rules), NetworkACLs: tt.rules}, nil)

			updateParams := &cloudstack.UpdateNetworkACLItemParams{}
			if tt.adopt != "" {
				mockNetworkACL.EXPECT().NewUpdateNetworkACLItemParams(tt.adopt).Return(updateParams)
				mockNetworkACL.EXPECT().UpdateNetworkACLItem(updateParams).Return(&cloudstack.UpdateNetworkACLItemResponse{}, tt.adoptErr)
			}
			createParams := &cloudstack.CreateNetworkACLParams{}
			if tt.wantCreate {
				mockNetworkACL.EXPECT().NewCreateNetworkACLParams(tt.protocol.IPProtocol()).Return(createParams)
				mockNetworkACL.EXPECT().CreateNetworkACL(createParams).Return(&cloudstack.CreateNetworkACLResponse{Id: "acl-new"}, tt.createErr)
			}

			lb := &loadBalancer{
				name:             "atestuid",
				CloudStackClient: &cloudstack.CloudStackClient{Network: mockNetwork, NetworkACL: mockNetworkACL},
			}

			updated, err := lb.updateNetworkACL(80, tt.protocol, "net-1")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !updated {
				t.Errorf("updated = false, want true")
			}
			if tt.adopt != "" {
				if sent := sentUpdateFields(updateParams); !compareStringSlice(sent, []string{"reason"}) {
					t.Errorf("adoption sent %v, want only the reason", sent)
				}
				if reason, _ := updateParams.GetReason(); reason != testACLReason {
					t.Errorf("adopted with reason %q, want %q", reason, testACLReason)
				}
			}
			if tt.wantCreate {
				if reason, _ := createParams.GetReason(); reason != testACLReason {
					t.Errorf("created with reason %q, want %q", reason, testACLReason)
				}
				if networkID, _ := createParams.GetNetworkid(); networkID != "net-1" {
					t.Errorf("created in network %q, want net-1", networkID)
				}
			}
		})
	}
}

func TestDeleteNetworkACLRuleOwnership(t *testing.T) {
	otherService := func(r *cloudstack.NetworkACL) { r.Reason = networkACLReasonPrefix + "aotheruid" }
	deleting := func(r *cloudstack.NetworkACL) { r.Reason = testACLReason; r.State = "Deleting" }
	firstErr := fmt.Errorf("first delete failed")

	tests := []struct {
		name        string
		protocol    LoadBalancerProtocol
		rules       []*cloudstack.NetworkACL
		deleteErrs  map[string]error
		wantDeleted []string
		wantErr     error
	}{
		{
			name: "deletes only this service's rules",
			rules: []*cloudstack.NetworkACL{
				legacyACLRule("acl-other", otherService),
				ownACLRule("acl-own"),
				legacyACLRule("acl-legacy", nil),
				legacyACLRule("acl-operator", func(r *cloudstack.NetworkACL) { r.Cidrlist = "10.0.0.0/8" }),
				legacyACLRule("acl-cks", func(r *cloudstack.NetworkACL) { r.Protocol = "TCP" }),
			},
			wantDeleted: []string{"acl-own"},
		},
		{
			name:        "deletes its own rule again while it is being deleted",
			rules:       []*cloudstack.NetworkACL{legacyACLRule("acl-own", deleting)},
			wantDeleted: []string{"acl-own"},
		},
		{
			name:        "only logs a failure on a rule already being deleted",
			rules:       []*cloudstack.NetworkACL{legacyACLRule("acl-own", deleting)},
			deleteErrs:  map[string]error{"acl-own": fmt.Errorf("router unreachable")},
			wantDeleted: []string{"acl-own"},
		},
		{
			name:        "tries every rule and reports the first error",
			rules:       []*cloudstack.NetworkACL{ownACLRule("acl-own-1"), ownACLRule("acl-own-2")},
			deleteErrs:  map[string]error{"acl-own-1": firstErr, "acl-own-2": fmt.Errorf("second delete failed")},
			wantDeleted: []string{"acl-own-1", "acl-own-2"},
			wantErr:     firstErr,
		},
		{
			name:     "deletes a udp rule but not the tcp rule on the same port",
			protocol: LoadBalancerProtocolUDP,
			rules: []*cloudstack.NetworkACL{
				ownACLRule("acl-own-tcp"),
				legacyACLRule("acl-own-udp", func(r *cloudstack.NetworkACL) { r.Reason = testACLReason; r.Protocol = "udp" }),
			},
			wantDeleted: []string{"acl-own-udp"},
		},
		{
			name:  "deletes nothing when no rule is this service's",
			rules: []*cloudstack.NetworkACL{legacyACLRule("acl-legacy", nil), legacyACLRule("acl-other", otherService)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			t.Cleanup(ctrl.Finish)

			mockNetworkACL := cloudstack.NewMockNetworkACLServiceIface(ctrl)
			mockNetworkACL.EXPECT().NewListNetworkACLsParams().Return(&cloudstack.ListNetworkACLsParams{})
			mockNetworkACL.EXPECT().ListNetworkACLs(gomock.Any()).Return(&cloudstack.ListNetworkACLsResponse{Count: len(tt.rules), NetworkACLs: tt.rules}, nil)

			var deleted []string
			for _, id := range tt.wantDeleted {
				deleteParams := &cloudstack.DeleteNetworkACLParams{}
				mockNetworkACL.EXPECT().NewDeleteNetworkACLParams(id).Return(deleteParams)
				mockNetworkACL.EXPECT().DeleteNetworkACL(deleteParams).DoAndReturn(func(*cloudstack.DeleteNetworkACLParams) (*cloudstack.DeleteNetworkACLResponse, error) {
					deleted = append(deleted, id)
					if err := tt.deleteErrs[id]; err != nil {
						return nil, err
					}
					return &cloudstack.DeleteNetworkACLResponse{}, nil
				})
			}

			lb := &loadBalancer{
				name:             "atestuid",
				CloudStackClient: &cloudstack.CloudStackClient{NetworkACL: mockNetworkACL},
			}

			ok, err := lb.deleteNetworkACLRule(80, tt.protocol, "net-1")
			if err != tt.wantErr {
				t.Errorf("error = %v, want %v", err, tt.wantErr)
			}
			if ok != (tt.wantErr == nil) {
				t.Errorf("ok = %v, want %v", ok, tt.wantErr == nil)
			}
			if !compareStringSlice(deleted, tt.wantDeleted) {
				t.Errorf("deleted %v, want %v", deleted, tt.wantDeleted)
			}
		})
	}
}

// Firewall rules are not shared like Network ACL rules (issue #107): Services can only share a
// public IP on different ports, so pruning touches the firewall rules of the rule's own IP only.
func TestPruneRulesScopesFirewallRulesToTheirIP(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)
	mockFirewall := cloudstack.NewMockFirewallServiceIface(ctrl)
	listParams := &cloudstack.ListFirewallRulesParams{}
	deleteParams := &cloudstack.DeleteFirewallRuleParams{}
	gomock.InOrder(
		mockFirewall.EXPECT().NewListFirewallRulesParams().Return(listParams),
		mockFirewall.EXPECT().ListFirewallRules(listParams).Return(&cloudstack.ListFirewallRulesResponse{
			Count:         1,
			FirewallRules: []*cloudstack.FirewallRule{{Id: "fw-old", Protocol: "tcp", Startport: 80, Endport: 80, Cidrlist: defaultAllowedCIDR}},
		}, nil),
		mockFirewall.EXPECT().NewDeleteFirewallRuleParams("fw-old").Return(deleteParams),
		mockFirewall.EXPECT().DeleteFirewallRule(deleteParams).Return(&cloudstack.DeleteFirewallRuleResponse{}, nil),
		mockLB.EXPECT().NewDeleteLoadBalancerRuleParams("rule-old").Return(&cloudstack.DeleteLoadBalancerRuleParams{}),
		mockLB.EXPECT().DeleteLoadBalancerRule(gomock.Any()).Return(&cloudstack.DeleteLoadBalancerRuleResponse{}, nil),
	)

	lb := &loadBalancer{
		CloudStackClient: &cloudstack.CloudStackClient{LoadBalancer: mockLB, Firewall: mockFirewall},
		name:             "atestuid",
		networkID:        "net-1",
		ipAddrID:         "ip-current",
	}
	network := &cloudstack.Network{Id: "net-1", Service: []cloudstack.NetworkServiceInternal{{Name: "Firewall"}}}
	obsolete := []obsoleteRule{{
		rule:     &cloudstack.LoadBalancerRule{Id: "rule-old", Name: "atestuid-tcp-80", Publicipid: "ip-old", Publicport: "80", Protocol: "tcp", Networkid: "net-1"},
		protocol: LoadBalancerProtocolTCP,
		tuple:    portProtocol{"tcp", 80},
	}}
	desired := []desiredLBRule{{
		name:     "atestuid-tcp-443",
		port:     corev1.ServicePort{Port: 443, NodePort: 30443, Protocol: corev1.ProtocolTCP},
		protocol: LoadBalancerProtocolTCP,
	}}

	if err := lb.pruneRules(obsolete, desired, network); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ipID, _ := listParams.GetIpaddressid(); ipID != "ip-old" {
		t.Errorf("firewall rules listed on IP %q, want the rule's own ip-old", ipID)
	}
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

	// Rules with distinct names are not duplicates, so what settles the IP the service is
	// reconciled towards is the preferred address, and without one the first-listed rule,
	// matching the duplicate sweep.
	onPort80 := &cloudstack.LoadBalancerRule{Id: "rule-80", Name: "test-service-tcp-80", Publicip: "203.0.113.9", Publicipid: "ip-requested"}
	onPort443 := &cloudstack.LoadBalancerRule{Id: "rule-443", Name: "test-service-tcp-443", Publicip: "203.0.113.1", Publicipid: "ip-auto"}

	assertResolvedIP := func(t *testing.T, lb *loadBalancer, wantIP, wantIPID string) {
		t.Helper()
		if lb.ipAddr != wantIP || lb.ipAddrID != wantIPID {
			t.Errorf("ipAddr/ipAddrID = %v/%v, want %v/%v", lb.ipAddr, lb.ipAddrID, wantIP, wantIPID)
		}
		if len(lb.rules) != 2 {
			t.Errorf("rules count = %d, want 2", len(lb.rules))
		}
	}

	t.Run("the requested IP outranks a stale IP listed after it", func(t *testing.T) {
		lb := listDuplicates(t, onPort80.Publicip, "", onPort80, onPort443)
		assertResolvedIP(t, lb, onPort80.Publicip, onPort80.Publicipid)
	})

	t.Run("the published ingress IP outranks a stale IP listed after it", func(t *testing.T) {
		lb := listDuplicates(t, "", onPort80.Publicip, onPort80, onPort443)
		assertResolvedIP(t, lb, onPort80.Publicip, onPort80.Publicipid)
	})

	t.Run("the requested IP outranks a stale IP listed before it", func(t *testing.T) {
		lb := listDuplicates(t, onPort80.Publicip, "", onPort443, onPort80)
		assertResolvedIP(t, lb, onPort80.Publicip, onPort80.Publicipid)
	})

	t.Run("the first-listed IP wins when none is preferred", func(t *testing.T) {
		lb := listDuplicates(t, "", "", onPort443, onPort80)
		assertResolvedIP(t, lb, onPort443.Publicip, onPort443.Publicipid)
	})

	t.Run("a preferred IP no rule is on leaves the first-listed IP", func(t *testing.T) {
		lb := listDuplicates(t, "198.51.100.7", "", onPort443, onPort80)
		assertResolvedIP(t, lb, onPort443.Publicip, onPort443.Publicipid)
	})
}

func TestGetLoadBalancerDeduplicatesPagedRules(t *testing.T) {
	// A rule returned on two pages arrives as two pointers to an equal rule.
	// getLoadBalancer would see the name already in lb.rules, call it a
	// duplicate, and hand it to the sweep in EnsureLoadBalancer - deleting the
	// only rule the Service is published on. The IP preference cannot save it,
	// because both copies carry the same address.
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)
	mockLB.EXPECT().NewListLoadBalancerRulesParams().
		Return(&cloudstack.ListLoadBalancerRulesParams{})
	mockLB.EXPECT().ListLoadBalancerRules(gomock.Any()).Times(2).
		DoAndReturn(func(p *cloudstack.ListLoadBalancerRulesParams) (*cloudstack.ListLoadBalancerRulesResponse, error) {
			return &cloudstack.ListLoadBalancerRulesResponse{
				Count: 2,
				LoadBalancerRules: []*cloudstack.LoadBalancerRule{
					{Id: "rule-1", Name: "a-svc-TCP-80", Publicip: "1.2.3.4", Publicipid: "ip-1"},
				},
			}, nil
		})

	cs := &CSCloud{client: &cloudstack.CloudStackClient{LoadBalancer: mockLB}}
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "svc", Namespace: "default", UID: "abc123"},
	}

	lb, err := cs.getLoadBalancer(service)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(lb.duplicateRules) != 0 {
		t.Errorf("rule %v was repeated across pages, not duplicated; it must not be swept", lb.duplicateRules)
	}
	if len(lb.rules) != 1 {
		t.Errorf("rules = %d, want 1", len(lb.rules))
	}
	if lb.ipAddr != "1.2.3.4" {
		t.Errorf("ipAddr = %q, want %q", lb.ipAddr, "1.2.3.4")
	}
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

// pagedRequest is the read side of the paging parameters that every
// cloudstack-go List*Params exposes.
type pagedRequest interface {
	GetPage() (int, bool)
	GetPagesize() (int, bool)
}

// pageOf returns the window of items a request asks for, mirroring how
// CloudStack serves a list: a request carrying no paging parameters comes back
// truncated at pageSize, and later pages are served by offset. It also asserts
// the paging contract CloudStack enforces.
func pageOf[T any](t *testing.T, p pagedRequest, items []T, pageSize int) []T {
	t.Helper()

	page, paged := p.GetPage()
	size, sized := p.GetPagesize()

	if paged != sized {
		t.Errorf("page and pagesize must be sent together, got page set = %v, pagesize set = %v", paged, sized)
	}

	switch {
	case !paged:
		page, size = 1, pageSize
	case page < 2:
		t.Errorf("page = %d, want >= 2 (CloudStack rejects page 0)", page)
	case size != pageSize:
		t.Errorf("pagesize = %d, want %d", size, pageSize)
	}

	start := (page - 1) * size
	if start >= len(items) {
		return nil
	}

	end := start + size
	if end > len(items) {
		end = len(items)
	}

	return items[start:end]
}

// nodesNamed builds the node list a cloudprovider call receives.
func nodesNamed(names ...string) []*corev1.Node {
	nodes := make([]*corev1.Node, 0, len(names))
	for _, name := range names {
		nodes = append(nodes, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}})
	}
	return nodes
}

func TestVerifyHostsPagination(t *testing.T) {
	// CloudStack truncates list responses at default.page.size while still
	// reporting the full total in count. This is the regression from issue #99:
	// with more VMs in the account than fit in one page, the nodes beyond the
	// first page were invisible and the load balancer was never created.
	const pageSize = 500

	// Only the last VM is a cluster node, so it lands on the final page.
	vms := make([]*cloudstack.VirtualMachine, 750)
	for i := range vms {
		vms[i] = &cloudstack.VirtualMachine{
			Id:   fmt.Sprintf("vm-%d", i),
			Name: fmt.Sprintf("other-%d", i),
			Nic:  []cloudstack.Nic{{Networkid: "net-123"}},
		}
	}
	vms[len(vms)-1].Id = "vm-node-1"
	vms[len(vms)-1].Name = "node-1"

	t.Run("collects hosts from every page", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockVM := cloudstack.NewMockVirtualMachineServiceIface(ctrl)
		// Params are built once and reused across pages.
		mockVM.EXPECT().NewListVirtualMachinesParams().
			Return(&cloudstack.ListVirtualMachinesParams{})
		mockVM.EXPECT().ListVirtualMachines(gomock.Any()).Times(2).
			DoAndReturn(func(p *cloudstack.ListVirtualMachinesParams) (*cloudstack.ListVirtualMachinesResponse, error) {
				return &cloudstack.ListVirtualMachinesResponse{
					Count:           len(vms),
					VirtualMachines: pageOf(t, p, vms, pageSize),
				}, nil
			})

		cs := &CSCloud{client: &cloudstack.CloudStackClient{VirtualMachine: mockVM}}

		hostIDs, networkID, err := cs.verifyHosts(nodesNamed("node-1"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(hostIDs, []string{"vm-node-1"}) {
			t.Errorf("hostIDs = %v, want %v", hostIDs, []string{"vm-node-1"})
		}
		if networkID != "net-123" {
			t.Errorf("networkID = %q, want %q", networkID, "net-123")
		}
	})

	t.Run("deduplicates hosts repeated across pages", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		node := &cloudstack.VirtualMachine{
			Id:   "vm-node-1",
			Name: "node-1",
			Nic:  []cloudstack.Nic{{Networkid: "net-123"}},
		}

		// A VM is removed between the requests, shifting the offset so the node
		// comes back on both pages.
		pages := [][]*cloudstack.VirtualMachine{{node, node}, {node}}

		mockVM := cloudstack.NewMockVirtualMachineServiceIface(ctrl)
		mockVM.EXPECT().NewListVirtualMachinesParams().
			Return(&cloudstack.ListVirtualMachinesParams{})
		mockVM.EXPECT().ListVirtualMachines(gomock.Any()).Times(len(pages)).
			DoAndReturn(func(p *cloudstack.ListVirtualMachinesParams) (*cloudstack.ListVirtualMachinesResponse, error) {
				page := pages[0]
				pages = pages[1:]
				return &cloudstack.ListVirtualMachinesResponse{Count: 3, VirtualMachines: page}, nil
			})

		cs := &CSCloud{client: &cloudstack.CloudStackClient{VirtualMachine: mockVM}}

		hostIDs, _, err := cs.verifyHosts(nodesNamed("node-1"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(hostIDs, []string{"vm-node-1"}) {
			t.Errorf("hostIDs = %v, want %v", hostIDs, []string{"vm-node-1"})
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
	address  *cloudstack.MockAddressServiceIface
	acl      *cloudstack.MockNetworkACLServiceIface
	service  *corev1.Service
	nodes    []*corev1.Node

	instances     []*cloudstack.VirtualMachine
	policies      map[string][]cloudstack.LBStickinessPolicyStickinesspolicy
	policyErr     error
	policyLookups []string
}

func newEnsureLBTestEnv(ctrl *gomock.Controller, annotations map[string]string, ports []corev1.ServicePort) *ensureLBTestEnv {
	e := &ensureLBTestEnv{
		lb:       cloudstack.NewMockLoadBalancerServiceIface(ctrl),
		vm:       cloudstack.NewMockVirtualMachineServiceIface(ctrl),
		network:  cloudstack.NewMockNetworkServiceIface(ctrl),
		firewall: cloudstack.NewMockFirewallServiceIface(ctrl),
		address:  cloudstack.NewMockAddressServiceIface(ctrl),
		acl:      cloudstack.NewMockNetworkACLServiceIface(ctrl),
	}

	e.cs = &CSCloud{
		client: &cloudstack.CloudStackClient{
			LoadBalancer:   e.lb,
			VirtualMachine: e.vm,
			Network:        e.network,
			Firewall:       e.firewall,
			Address:        e.address,
			NetworkACL:     e.acl,
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

	// A sync counts the hosts of each adopted rule and lists its stickiness policies. By default
	// the rule already has the node and no policies, so tests that change neither work as before.
	e.instances = []*cloudstack.VirtualMachine{{Id: "vm-1"}}
	e.policies = map[string][]cloudstack.LBStickinessPolicyStickinesspolicy{}
	e.lb.EXPECT().NewListLoadBalancerRuleInstancesParams(gomock.Any()).DoAndReturn(func(string) *cloudstack.ListLoadBalancerRuleInstancesParams {
		return &cloudstack.ListLoadBalancerRuleInstancesParams{}
	}).AnyTimes()
	e.lb.EXPECT().ListLoadBalancerRuleInstances(gomock.Any()).DoAndReturn(e.listRuleInstances).AnyTimes()
	e.lb.EXPECT().NewListLBStickinessPoliciesParams().DoAndReturn(func() *cloudstack.ListLBStickinessPoliciesParams {
		return &cloudstack.ListLBStickinessPoliciesParams{}
	}).AnyTimes()
	e.lb.EXPECT().ListLBStickinessPolicies(gomock.Any()).DoAndReturn(e.listStickinessPolicies).AnyTimes()

	return e
}

// listRuleInstances answers a rule instance lookup with e.instances.
func (e *ensureLBTestEnv) listRuleInstances(*cloudstack.ListLoadBalancerRuleInstancesParams) (*cloudstack.ListLoadBalancerRuleInstancesResponse, error) {
	return &cloudstack.ListLoadBalancerRuleInstancesResponse{Count: len(e.instances), LoadBalancerRuleInstances: e.instances}, nil
}

// listStickinessPolicies answers a policy lookup from e.policies, one entry per rule like
// CloudStack, and records which rule was looked up.
func (e *ensureLBTestEnv) listStickinessPolicies(p *cloudstack.ListLBStickinessPoliciesParams) (*cloudstack.ListLBStickinessPoliciesResponse, error) {
	ruleID, _ := p.GetLbruleid()
	e.policyLookups = append(e.policyLookups, ruleID)
	if e.policyErr != nil {
		return nil, e.policyErr
	}
	wrapper := &cloudstack.LBStickinessPolicy{Lbruleid: ruleID, Stickinesspolicy: e.policies[ruleID]}

	return &cloudstack.ListLBStickinessPoliciesResponse{Count: 1, LBStickinessPolicies: []*cloudstack.LBStickinessPolicy{wrapper}}, nil
}

// expectHosts registers the node lookup every run performs before resolving rules.
func (e *ensureLBTestEnv) expectHosts() {
	e.vm.EXPECT().NewListVirtualMachinesParams().Return(&cloudstack.ListVirtualMachinesParams{})
	e.vm.EXPECT().ListVirtualMachines(gomock.Any()).Return(&cloudstack.ListVirtualMachinesResponse{
		Count: 1,
		VirtualMachines: []*cloudstack.VirtualMachine{
			{Id: "vm-1", Name: "node-1", Nic: []cloudstack.Nic{{Networkid: "net-1"}}},
		},
	}, nil)
}

// expectHostsAndNetwork registers the host and network lookups every run performs.
func (e *ensureLBTestEnv) expectHostsAndNetwork() {
	e.expectHostsAndNetworkWith(&cloudstack.Network{
		Id:      "net-1",
		Service: []cloudstack.NetworkServiceInternal{{Name: "Firewall"}},
	})
}

// expectHostsAndNetworkWith registers the host lookup and answers the network lookup with network.
func (e *ensureLBTestEnv) expectHostsAndNetworkWith(network *cloudstack.Network) {
	e.expectHosts()
	e.network.EXPECT().GetNetworkByID("net-1", gomock.Any()).Return(network, 1, nil)
}

// expectRules answers the run's load balancer rule listing with rules.
func (e *ensureLBTestEnv) expectRules(rules ...*cloudstack.LoadBalancerRule) {
	e.lb.EXPECT().NewListLoadBalancerRulesParams().Return(&cloudstack.ListLoadBalancerRulesParams{})
	e.lb.EXPECT().ListLoadBalancerRules(gomock.Any()).Return(&cloudstack.ListLoadBalancerRulesResponse{
		Count:             len(rules),
		LoadBalancerRules: rules,
	}, nil)
}

// expectFirewallListings answers each of the run's firewall listings, one per port, with resp.
func (e *ensureLBTestEnv) expectFirewallListings(resp *cloudstack.ListFirewallRulesResponse, times int) {
	e.firewall.EXPECT().NewListFirewallRulesParams().Return(&cloudstack.ListFirewallRulesParams{}).Times(times)
	e.firewall.EXPECT().ListFirewallRules(gomock.Any()).Return(resp, nil).Times(times)
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

	t.Run("rule named by the old provider is adopted and renamed", func(t *testing.T) {
		// Migration from the in-tree provider, whose rule names carried no protocol and whose
		// protocol was sent in upper case, which CloudStack before 4.21 stored verbatim. The
		// rule must be adopted rather than recreated, so the README can stop asking operators
		// to delete their rules before migrating.
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		env := newEnsureLBTestEnv(ctrl, nil, []corev1.ServicePort{tcpPort80})
		env.expectHostsAndNetwork()

		legacyRule := existingTCPRule()
		legacyRule.Id = "rule-legacy"
		legacyRule.Name = "atestuid-80"
		legacyRule.Protocol = "TCP"

		updateParams := &cloudstack.UpdateLoadBalancerRuleParams{}

		// No create and no delete expectations: either call fails the test.
		gomock.InOrder(
			env.lb.EXPECT().NewListLoadBalancerRulesParams().Return(&cloudstack.ListLoadBalancerRulesParams{}),
			env.lb.EXPECT().ListLoadBalancerRules(gomock.Any()).Return(&cloudstack.ListLoadBalancerRulesResponse{
				Count:             1,
				LoadBalancerRules: []*cloudstack.LoadBalancerRule{legacyRule},
			}, nil),
			env.lb.EXPECT().NewUpdateLoadBalancerRuleParams("rule-legacy").Return(updateParams),
			env.lb.EXPECT().UpdateLoadBalancerRule(gomock.Any()).Return(&cloudstack.UpdateLoadBalancerRuleResponse{}, nil),
		)

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
		if name, _ := updateParams.GetName(); name != "atestuid-tcp-80" {
			t.Errorf("updated name = %q, want %q", name, "atestuid-tcp-80")
		}
		if proto, _ := updateParams.GetProtocol(); proto != "tcp" {
			t.Errorf("updated protocol = %q, want %q", proto, "tcp")
		}
	})

	t.Run("rule named by the old provider is renamed in place below the cidrlist update release", func(t *testing.T) {
		// In-tree rules carry no cidrlist. That allows every source, so on a release that can
		// only apply a CIDR change by recreating the rule it must still count as unchanged.
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		env := newEnsureLBTestEnv(ctrl, nil, []corev1.ServicePort{tcpPort80})
		env.cs.version = semver.MustParse("4.21.0")
		env.expectHostsAndNetwork()

		legacyRule := existingTCPRule()
		legacyRule.Id = "rule-legacy"
		legacyRule.Name = "atestuid-80"
		legacyRule.Protocol = "TCP"
		legacyRule.Cidrlist = ""

		updateParams := &cloudstack.UpdateLoadBalancerRuleParams{}

		// No create and no delete expectations: either call fails the test.
		gomock.InOrder(
			env.lb.EXPECT().NewListLoadBalancerRulesParams().Return(&cloudstack.ListLoadBalancerRulesParams{}),
			env.lb.EXPECT().ListLoadBalancerRules(gomock.Any()).Return(&cloudstack.ListLoadBalancerRulesResponse{
				Count:             1,
				LoadBalancerRules: []*cloudstack.LoadBalancerRule{legacyRule},
			}, nil),
			env.lb.EXPECT().NewUpdateLoadBalancerRuleParams("rule-legacy").Return(updateParams),
			env.lb.EXPECT().UpdateLoadBalancerRule(gomock.Any()).Return(&cloudstack.UpdateLoadBalancerRuleResponse{}, nil),
		)

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
		if name, _ := updateParams.GetName(); name != "atestuid-tcp-80" {
			t.Errorf("updated name = %q, want %q", name, "atestuid-tcp-80")
		}
	})

	t.Run("an unsupported port leaves a rule that needs recreating in place", func(t *testing.T) {
		// Port 80 needs recreating for its new nodePort, and the SCTP port after it fails
		// the reconcile. That fails every retry, so deleting port 80 before the error would
		// leave it without a rule for good.
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		movedPort80 := corev1.ServicePort{Port: 80, NodePort: 30001, Protocol: corev1.ProtocolTCP}
		sctpPort := corev1.ServicePort{Port: 9000, NodePort: 30900, Protocol: corev1.ProtocolSCTP}
		env := newEnsureLBTestEnv(ctrl, nil, []corev1.ServicePort{movedPort80, sctpPort})
		env.expectHostsAndNetwork()

		// No delete expectation: the existing port 80 rule must survive.
		gomock.InOrder(
			env.lb.EXPECT().NewListLoadBalancerRulesParams().Return(&cloudstack.ListLoadBalancerRulesParams{}),
			env.lb.EXPECT().ListLoadBalancerRules(gomock.Any()).Return(&cloudstack.ListLoadBalancerRulesResponse{
				Count:             1,
				LoadBalancerRules: []*cloudstack.LoadBalancerRule{existingTCPRule()},
			}, nil),
		)

		if _, err := env.cs.EnsureLoadBalancer(context.TODO(), "test-cluster", env.service, env.nodes); err == nil {
			t.Fatalf("expected the unsupported SCTP port to fail the reconcile")
		}
	})

	t.Run("a failure before the apply phase leaves a rule that needs recreating in place", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		movedPort80 := corev1.ServicePort{Port: 80, NodePort: 30001, Protocol: corev1.ProtocolTCP}
		env := newEnsureLBTestEnv(ctrl, nil, []corev1.ServicePort{movedPort80})
		env.expectHosts()
		env.network.EXPECT().GetNetworkByID("net-1", gomock.Any()).Return(nil, -1, fmt.Errorf("API unavailable"))

		// No delete expectation: the existing port 80 rule must survive.
		gomock.InOrder(
			env.lb.EXPECT().NewListLoadBalancerRulesParams().Return(&cloudstack.ListLoadBalancerRulesParams{}),
			env.lb.EXPECT().ListLoadBalancerRules(gomock.Any()).Return(&cloudstack.ListLoadBalancerRulesResponse{
				Count:             1,
				LoadBalancerRules: []*cloudstack.LoadBalancerRule{existingTCPRule()},
			}, nil),
		)

		if _, err := env.cs.EnsureLoadBalancer(context.TODO(), "test-cluster", env.service, env.nodes); err == nil {
			t.Fatalf("expected the network lookup failure to fail the reconcile")
		}
	})

	t.Run("a rule that needs recreating is replaced in the apply phase", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		movedPort80 := corev1.ServicePort{Port: 80, NodePort: 30001, Protocol: corev1.ProtocolTCP}
		env := newEnsureLBTestEnv(ctrl, nil, []corev1.ServicePort{movedPort80})
		env.expectHostsAndNetwork()

		// The kept firewall rule shows the recreate is not mistaken for an obsolete rule.
		gomock.InOrder(
			env.lb.EXPECT().NewListLoadBalancerRulesParams().Return(&cloudstack.ListLoadBalancerRulesParams{}),
			env.lb.EXPECT().ListLoadBalancerRules(gomock.Any()).Return(&cloudstack.ListLoadBalancerRulesResponse{
				Count:             1,
				LoadBalancerRules: []*cloudstack.LoadBalancerRule{existingTCPRule()},
			}, nil),
			env.lb.EXPECT().NewDeleteLoadBalancerRuleParams("rule-1").Return(&cloudstack.DeleteLoadBalancerRuleParams{}),
			env.lb.EXPECT().DeleteLoadBalancerRule(gomock.Any()).Return(&cloudstack.DeleteLoadBalancerRuleResponse{}, nil),
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
		// Two rules share (tcp, 80) and the nodePort changed, so the matched rule has to be
		// recreated. The other rule still holds public port 80, so it must be deleted before
		// the create or CloudStack rejects it with a port conflict.
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

			// The blocking rule is pruned first...
			env.lb.EXPECT().NewDeleteLoadBalancerRuleParams("rule-b").Return(&cloudstack.DeleteLoadBalancerRuleParams{}),
			env.lb.EXPECT().DeleteLoadBalancerRule(gomock.Any()).Return(&cloudstack.DeleteLoadBalancerRuleResponse{}, nil),

			// ...and the matched rule, which cannot take a new nodePort, is deleted only
			// immediately before its replacement is created.
			env.lb.EXPECT().NewDeleteLoadBalancerRuleParams("rule-a").Return(&cloudstack.DeleteLoadBalancerRuleParams{}),
			env.lb.EXPECT().DeleteLoadBalancerRule(gomock.Any()).Return(&cloudstack.DeleteLoadBalancerRuleResponse{}, nil),
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

	t.Run("unparseable rule holding a needed port is pruned first", func(t *testing.T) {
		// A rule the provider cannot interpret still occupies its public port, so one sitting
		// on a port a create needs has to go before that create, or CloudStack rejects it.
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		env := newEnsureLBTestEnv(ctrl, nil, []corev1.ServicePort{tcpPort80})
		env.expectHostsAndNetwork()

		junkRule := existingTCPRule()
		junkRule.Id = "rule-junk"
		junkRule.Name = "atestuid-http-80"
		junkRule.Protocol = "http"

		gomock.InOrder(
			env.lb.EXPECT().NewListLoadBalancerRulesParams().Return(&cloudstack.ListLoadBalancerRulesParams{}),
			env.lb.EXPECT().ListLoadBalancerRules(gomock.Any()).Return(&cloudstack.ListLoadBalancerRulesResponse{
				Count:             1,
				LoadBalancerRules: []*cloudstack.LoadBalancerRule{junkRule},
			}, nil),

			// No firewall expectations for the junk rule: its protocol cannot be resolved, so
			// only the load balancer rule itself is deleted, and before the create.
			env.lb.EXPECT().NewDeleteLoadBalancerRuleParams("rule-junk").Return(&cloudstack.DeleteLoadBalancerRuleParams{}),
			env.lb.EXPECT().DeleteLoadBalancerRule(gomock.Any()).Return(&cloudstack.DeleteLoadBalancerRuleResponse{}, nil),

			env.lb.EXPECT().NewCreateLoadBalancerRuleParams("roundrobin", "atestuid-tcp-80", 30000, 80).Return(&cloudstack.CreateLoadBalancerRuleParams{}),
			env.lb.EXPECT().CreateLoadBalancerRule(gomock.Any()).Return(&cloudstack.CreateLoadBalancerRuleResponse{
				Id:         "rule-2",
				Name:       "atestuid-tcp-80",
				Publicip:   "10.0.0.1",
				Publicipid: "ip-1",
				Protocol:   "tcp",
			}, nil),
			env.lb.EXPECT().NewAssignToLoadBalancerRuleParams("rule-2").Return(&cloudstack.AssignToLoadBalancerRuleParams{}),
			env.lb.EXPECT().AssignToLoadBalancerRule(gomock.Any()).Return(&cloudstack.AssignToLoadBalancerRuleResponse{}, nil),
		)

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
		env.service.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{IP: "10.0.0.1"}}
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
			// Listed first, yet the published ingress IP makes ip-1 the address reconciled towards.
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

func TestUpdateLoadBalancerPagination(t *testing.T) {
	// Instances of a load balancer rule are one per load balanced node, so on a
	// large cluster the un-paged response was truncated and the stale nodes on
	// later pages were never removed from the rule.
	const pageSize = 500

	instances := make([]*cloudstack.VirtualMachine, 600)
	for i := range instances {
		instances[i] = &cloudstack.VirtualMachine{Id: fmt.Sprintf("vm-%d", i)}
	}

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockVM := cloudstack.NewMockVirtualMachineServiceIface(ctrl)
	mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)

	// getLoadBalancer: one rule, fits in a single page.
	mockLB.EXPECT().NewListLoadBalancerRulesParams().
		Return(&cloudstack.ListLoadBalancerRulesParams{})
	mockLB.EXPECT().ListLoadBalancerRules(gomock.Any()).
		Return(&cloudstack.ListLoadBalancerRulesResponse{
			Count: 1,
			LoadBalancerRules: []*cloudstack.LoadBalancerRule{
				{Id: "rule-1", Name: "rule-1", Publicip: "1.2.3.4", Publicipid: "ip-1"},
			},
		}, nil)

	// verifyHosts: the cluster is down to a single node.
	mockVM.EXPECT().NewListVirtualMachinesParams().
		Return(&cloudstack.ListVirtualMachinesParams{})
	mockVM.EXPECT().ListVirtualMachines(gomock.Any()).
		Return(&cloudstack.ListVirtualMachinesResponse{
			Count: 1,
			VirtualMachines: []*cloudstack.VirtualMachine{
				{Id: "vm-0", Name: "node-0", Nic: []cloudstack.Nic{{Networkid: "net-123"}}},
			},
		}, nil)

	// The rule's members arrive a page at a time.
	mockLB.EXPECT().NewListLoadBalancerRuleInstancesParams("rule-1").
		Return(&cloudstack.ListLoadBalancerRuleInstancesParams{})
	mockLB.EXPECT().ListLoadBalancerRuleInstances(gomock.Any()).Times(2).
		DoAndReturn(func(p *cloudstack.ListLoadBalancerRuleInstancesParams) (*cloudstack.ListLoadBalancerRuleInstancesResponse, error) {
			return &cloudstack.ListLoadBalancerRuleInstancesResponse{
				Count:                     len(instances),
				LoadBalancerRuleInstances: pageOf(t, p, instances, pageSize),
			}, nil
		})

	var removed []string
	mockLB.EXPECT().NewRemoveFromLoadBalancerRuleParams("rule-1").
		Return(&cloudstack.RemoveFromLoadBalancerRuleParams{})
	mockLB.EXPECT().RemoveFromLoadBalancerRule(gomock.Any()).
		DoAndReturn(func(p *cloudstack.RemoveFromLoadBalancerRuleParams) (*cloudstack.RemoveFromLoadBalancerRuleResponse, error) {
			removed, _ = p.GetVirtualmachineids()
			return &cloudstack.RemoveFromLoadBalancerRuleResponse{}, nil
		})

	cs := &CSCloud{
		client: &cloudstack.CloudStackClient{VirtualMachine: mockVM, LoadBalancer: mockLB},
	}
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "test-svc", Namespace: "default", UID: "abc123"},
	}

	if err := cs.UpdateLoadBalancer(context.TODO(), "cluster", service, nodesNamed("node-0")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Every instance except vm-0 must be removed, including those that only
	// appeared on the second page.
	if len(removed) != len(instances)-1 {
		t.Fatalf("removed %d hosts, want %d", len(removed), len(instances)-1)
	}
	if slices.Contains(removed, "vm-0") {
		t.Errorf("vm-0 is still a node but was removed from the rule")
	}
	if !slices.Contains(removed, "vm-599") {
		t.Errorf("vm-599 is on the second page and should have been removed, got %v", removed)
	}
}

// Regression test for issue #107 through a whole sync of a Service on a VPC tier.
func TestEnsureLoadBalancerNetworkACLOwnership(t *testing.T) {
	vpcTier := &cloudstack.Network{
		Id:      "net-1",
		Vpcid:   "vpc-1",
		Aclid:   "acl-1",
		Service: []cloudstack.NetworkServiceInternal{{Name: "NetworkACL"}},
	}
	tierACLList := &cloudstack.NetworkACLList{Id: "acl-1", Name: "tier-acl", Vpcid: "vpc-1"}
	existingRule := func() *cloudstack.LoadBalancerRule {
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
	otherServiceRule := legacyACLRule("acl-other", func(r *cloudstack.NetworkACL) { r.Reason = networkACLReasonPrefix + "aotheruid" })

	t.Run("a service sharing a port with another service opens it with its own rule", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		env := newEnsureLBTestEnv(ctrl, nil, []corev1.ServicePort{{Port: 80, NodePort: 30000, Protocol: corev1.ProtocolTCP}})
		env.expectHosts()
		env.expectRules(existingRule())
		env.network.EXPECT().GetNetworkByID("net-1", gomock.Any()).Return(vpcTier, 1, nil).Times(2)
		env.acl.EXPECT().GetNetworkACLListByID("acl-1", gomock.Any()).Return(tierACLList, 1, nil)
		env.acl.EXPECT().NewListNetworkACLsParams().Return(&cloudstack.ListNetworkACLsParams{})
		env.acl.EXPECT().ListNetworkACLs(gomock.Any()).Return(&cloudstack.ListNetworkACLsResponse{Count: 1, NetworkACLs: []*cloudstack.NetworkACL{otherServiceRule}}, nil)
		createParams := &cloudstack.CreateNetworkACLParams{}
		env.acl.EXPECT().NewCreateNetworkACLParams("tcp").Return(createParams)
		env.acl.EXPECT().CreateNetworkACL(createParams).Return(&cloudstack.CreateNetworkACLResponse{Id: "acl-own"}, nil)

		if _, err := env.cs.EnsureLoadBalancer(context.TODO(), "test-cluster", env.service, env.nodes); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if reason, _ := createParams.GetReason(); reason != testACLReason {
			t.Errorf("created with reason %q, want %q", reason, testACLReason)
		}
	})

	t.Run("a port change deletes only its own rule on the old port", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		env := newEnsureLBTestEnv(ctrl, nil, []corev1.ServicePort{{Port: 8080, NodePort: 30000, Protocol: corev1.ProtocolTCP}})
		env.expectHosts()
		env.expectRules(existingRule())
		env.network.EXPECT().GetNetworkByID("net-1", gomock.Any()).Return(vpcTier, 1, nil).Times(2)

		env.lb.EXPECT().NewCreateLoadBalancerRuleParams("roundrobin", "atestuid-tcp-8080", 30000, 8080).Return(&cloudstack.CreateLoadBalancerRuleParams{})
		env.lb.EXPECT().CreateLoadBalancerRule(gomock.Any()).Return(&cloudstack.CreateLoadBalancerRuleResponse{
			Id: "rule-2", Name: "atestuid-tcp-8080", Publicip: "10.0.0.1", Publicipid: "ip-1", Publicport: "8080", Protocol: "tcp", Networkid: "net-1",
		}, nil)
		env.lb.EXPECT().NewAssignToLoadBalancerRuleParams("rule-2").Return(&cloudstack.AssignToLoadBalancerRuleParams{})
		env.lb.EXPECT().AssignToLoadBalancerRule(gomock.Any()).Return(&cloudstack.AssignToLoadBalancerRuleResponse{}, nil)

		tierRules := []*cloudstack.NetworkACL{otherServiceRule, ownACLRule("acl-own-80")}
		env.acl.EXPECT().GetNetworkACLListByID("acl-1", gomock.Any()).Return(tierACLList, 1, nil)
		env.acl.EXPECT().NewListNetworkACLsParams().Return(&cloudstack.ListNetworkACLsParams{}).Times(2)
		env.acl.EXPECT().ListNetworkACLs(gomock.Any()).Return(&cloudstack.ListNetworkACLsResponse{Count: len(tierRules), NetworkACLs: tierRules}, nil).Times(2)
		env.acl.EXPECT().NewCreateNetworkACLParams("tcp").Return(&cloudstack.CreateNetworkACLParams{})
		env.acl.EXPECT().CreateNetworkACL(gomock.Any()).Return(&cloudstack.CreateNetworkACLResponse{Id: "acl-own-8080"}, nil)
		deleteParams := &cloudstack.DeleteNetworkACLParams{}
		env.acl.EXPECT().NewDeleteNetworkACLParams("acl-own-80").Return(deleteParams)
		env.acl.EXPECT().DeleteNetworkACL(deleteParams).Return(&cloudstack.DeleteNetworkACLResponse{}, nil)

		env.lb.EXPECT().NewDeleteLoadBalancerRuleParams("rule-1").Return(&cloudstack.DeleteLoadBalancerRuleParams{})
		env.lb.EXPECT().DeleteLoadBalancerRule(gomock.Any()).Return(&cloudstack.DeleteLoadBalancerRuleResponse{}, nil)

		if _, err := env.cs.EnsureLoadBalancer(context.TODO(), "test-cluster", env.service, env.nodes); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestEnsureLoadBalancerDeletedNetworkACLOwnership(t *testing.T) {
	newEnv := func(t *testing.T) (*CSCloud, *cloudstack.MockLoadBalancerServiceIface, *cloudstack.MockNetworkACLServiceIface, *cloudstack.MockAddressServiceIface, *cloudstack.ListNetworkACLsParams) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)
		mockNetwork := cloudstack.NewMockNetworkServiceIface(ctrl)
		mockNetworkACL := cloudstack.NewMockNetworkACLServiceIface(ctrl)
		mockAddress := cloudstack.NewMockAddressServiceIface(ctrl)

		mockLB.EXPECT().NewListLoadBalancerRulesParams().Return(&cloudstack.ListLoadBalancerRulesParams{})
		mockLB.EXPECT().ListLoadBalancerRules(gomock.Any()).Return(&cloudstack.ListLoadBalancerRulesResponse{
			Count: 1,
			LoadBalancerRules: []*cloudstack.LoadBalancerRule{{
				Id: "rule-1", Name: "atestuid-tcp-80", Publicip: "10.0.0.1", Publicipid: "ip-1", Publicport: "80", Protocol: "tcp", Networkid: "net-tier",
			}},
		}, nil)
		mockNetwork.EXPECT().GetNetworkByID("net-tier", gomock.Any()).Return(&cloudstack.Network{Id: "net-tier", Vpcid: "vpc-1"}, 1, nil)

		listParams := &cloudstack.ListNetworkACLsParams{}
		mockNetworkACL.EXPECT().NewListNetworkACLsParams().Return(listParams)
		mockNetworkACL.EXPECT().ListNetworkACLs(gomock.Any()).Return(&cloudstack.ListNetworkACLsResponse{
			Count: 3,
			NetworkACLs: []*cloudstack.NetworkACL{
				legacyACLRule("acl-other", func(r *cloudstack.NetworkACL) { r.Reason = networkACLReasonPrefix + "aotheruid" }),
				ownACLRule("acl-own"),
				legacyACLRule("acl-operator", func(r *cloudstack.NetworkACL) { r.Cidrlist = "10.0.0.0/8" }),
			},
		}, nil)

		cs := &CSCloud{client: &cloudstack.CloudStackClient{
			LoadBalancer: mockLB, Network: mockNetwork, NetworkACL: mockNetworkACL, Address: mockAddress,
		}}
		return cs, mockLB, mockNetworkACL, mockAddress, listParams
	}
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "test-service", Namespace: "default", UID: "test-uid"}}

	t.Run("deletes only its own ACL rule, in the rule's own network, then the rule and the IP", func(t *testing.T) {
		cs, mockLB, mockNetworkACL, mockAddress, listParams := newEnv(t)
		deleteACL := &cloudstack.DeleteNetworkACLParams{}
		deleteRule := &cloudstack.DeleteLoadBalancerRuleParams{}
		release := &cloudstack.DisassociateIpAddressParams{}
		gomock.InOrder(
			mockNetworkACL.EXPECT().NewDeleteNetworkACLParams("acl-own").Return(deleteACL),
			mockNetworkACL.EXPECT().DeleteNetworkACL(deleteACL).Return(&cloudstack.DeleteNetworkACLResponse{}, nil),
			mockLB.EXPECT().NewDeleteLoadBalancerRuleParams("rule-1").Return(deleteRule),
			mockLB.EXPECT().DeleteLoadBalancerRule(deleteRule).Return(&cloudstack.DeleteLoadBalancerRuleResponse{}, nil),
			mockAddress.EXPECT().NewDisassociateIpAddressParams("ip-1").Return(release),
			mockAddress.EXPECT().DisassociateIpAddress(release).Return(&cloudstack.DisassociateIpAddressResponse{}, nil),
		)

		if err := cs.EnsureLoadBalancerDeleted(context.TODO(), "test-cluster", service); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if networkID, _ := listParams.GetNetworkid(); networkID != "net-tier" {
			t.Errorf("ACL rules listed on network %q, want the rule's own net-tier", networkID)
		}
	})

	t.Run("deletes the other rules, keeps the failing one and reports both failures", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)
		mockNetwork := cloudstack.NewMockNetworkServiceIface(ctrl)
		mockNetworkACL := cloudstack.NewMockNetworkACLServiceIface(ctrl)
		mockFirewall := cloudstack.NewMockFirewallServiceIface(ctrl)
		mockAddress := cloudstack.NewMockAddressServiceIface(ctrl)

		rule80 := &cloudstack.LoadBalancerRule{Id: "rule-80", Name: "atestuid-tcp-80", Publicip: "10.0.0.1", Publicipid: "ip-1", Publicport: "80", Protocol: "tcp", Networkid: "net-tier"}
		rule8080 := &cloudstack.LoadBalancerRule{Id: "rule-8080", Name: "atestuid-tcp-8080", Publicip: "10.0.0.1", Publicipid: "ip-1", Publicport: "8080", Protocol: "tcp", Networkid: "net-tier"}
		duplicate := &cloudstack.LoadBalancerRule{Id: "rule-dup", Name: "atestuid-tcp-80", Publicip: "10.0.0.2", Publicipid: "ip-dup", Publicport: "80", Protocol: "tcp", Networkid: "net-tier"}
		mockLB.EXPECT().NewListLoadBalancerRulesParams().Return(&cloudstack.ListLoadBalancerRulesParams{})
		mockLB.EXPECT().ListLoadBalancerRules(gomock.Any()).Return(&cloudstack.ListLoadBalancerRulesResponse{
			Count: 3, LoadBalancerRules: []*cloudstack.LoadBalancerRule{rule80, rule8080, duplicate},
		}, nil)
		mockFirewall.EXPECT().NewListFirewallRulesParams().Return(&cloudstack.ListFirewallRulesParams{})
		mockFirewall.EXPECT().ListFirewallRules(gomock.Any()).Return(nil, fmt.Errorf("firewall API down"))

		mockNetwork.EXPECT().GetNetworkByID("net-tier", gomock.Any()).Return(&cloudstack.Network{Id: "net-tier", Vpcid: "vpc-1"}, 1, nil).Times(2)
		tierRules := []*cloudstack.NetworkACL{
			ownACLRule("acl-own-80"),
			legacyACLRule("acl-own-8080", func(r *cloudstack.NetworkACL) { r.Reason = testACLReason; r.Startport, r.Endport = "8080", "8080" }),
		}
		mockNetworkACL.EXPECT().NewListNetworkACLsParams().DoAndReturn(func() *cloudstack.ListNetworkACLsParams {
			return &cloudstack.ListNetworkACLsParams{}
		}).Times(2)
		mockNetworkACL.EXPECT().ListNetworkACLs(gomock.Any()).Return(&cloudstack.ListNetworkACLsResponse{Count: len(tierRules), NetworkACLs: tierRules}, nil).Times(2)
		delete80 := &cloudstack.DeleteNetworkACLParams{}
		delete8080 := &cloudstack.DeleteNetworkACLParams{}
		mockNetworkACL.EXPECT().NewDeleteNetworkACLParams("acl-own-80").Return(delete80)
		mockNetworkACL.EXPECT().DeleteNetworkACL(delete80).Return(nil, fmt.Errorf("router unreachable"))
		mockNetworkACL.EXPECT().NewDeleteNetworkACLParams("acl-own-8080").Return(delete8080)
		mockNetworkACL.EXPECT().DeleteNetworkACL(delete8080).Return(&cloudstack.DeleteNetworkACLResponse{}, nil)
		deleteRule := &cloudstack.DeleteLoadBalancerRuleParams{}
		mockLB.EXPECT().NewDeleteLoadBalancerRuleParams("rule-8080").Return(deleteRule)
		mockLB.EXPECT().DeleteLoadBalancerRule(deleteRule).Return(&cloudstack.DeleteLoadBalancerRuleResponse{}, nil)

		cs := &CSCloud{client: &cloudstack.CloudStackClient{
			LoadBalancer: mockLB, Network: mockNetwork, NetworkACL: mockNetworkACL, Firewall: mockFirewall, Address: mockAddress,
		}}

		err := cs.EnsureLoadBalancerDeleted(context.TODO(), "test-cluster", service)
		if err == nil || !strings.Contains(err.Error(), "router unreachable") || !strings.Contains(err.Error(), "firewall API down") {
			t.Fatalf("error = %v, want both the ACL rule and the sweep failure", err)
		}
	})

	t.Run("keeps the rule and the IP when its ACL rule cannot be deleted", func(t *testing.T) {
		cs, _, mockNetworkACL, _, _ := newEnv(t)
		deleteACL := &cloudstack.DeleteNetworkACLParams{}
		mockNetworkACL.EXPECT().NewDeleteNetworkACLParams("acl-own").Return(deleteACL)
		mockNetworkACL.EXPECT().DeleteNetworkACL(deleteACL).Return(nil, fmt.Errorf("router unreachable"))

		err := cs.EnsureLoadBalancerDeleted(context.TODO(), "test-cluster", service)
		if err == nil || !strings.Contains(err.Error(), "router unreachable") {
			t.Fatalf("error = %v, want the ACL rule failure reported for a retry", err)
		}
	})
}

func TestEnsureLoadBalancerDeletedFirewallUsesRuleNetwork(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)
	mockNetwork := cloudstack.NewMockNetworkServiceIface(ctrl)
	mockFirewall := cloudstack.NewMockFirewallServiceIface(ctrl)
	mockAddress := cloudstack.NewMockAddressServiceIface(ctrl)

	mockLB.EXPECT().NewListLoadBalancerRulesParams().Return(&cloudstack.ListLoadBalancerRulesParams{})
	mockLB.EXPECT().ListLoadBalancerRules(gomock.Any()).Return(&cloudstack.ListLoadBalancerRulesResponse{
		Count: 1,
		LoadBalancerRules: []*cloudstack.LoadBalancerRule{{
			Id: "rule-1", Name: "atestuid-tcp-80", Publicip: "203.0.113.1", Publicipid: "ip-1", Publicport: "80", Protocol: "tcp", Networkid: "net-iso",
		}},
	}, nil)
	mockNetwork.EXPECT().GetNetworkByID("net-iso", gomock.Any()).Return(&cloudstack.Network{Id: "net-iso"}, 1, nil)
	listParams := &cloudstack.ListFirewallRulesParams{}
	deleteFirewall := &cloudstack.DeleteFirewallRuleParams{}
	deleteRule := &cloudstack.DeleteLoadBalancerRuleParams{}
	release := &cloudstack.DisassociateIpAddressParams{}
	gomock.InOrder(
		mockFirewall.EXPECT().NewListFirewallRulesParams().Return(listParams),
		mockFirewall.EXPECT().ListFirewallRules(listParams).Return(&cloudstack.ListFirewallRulesResponse{
			Count:         1,
			FirewallRules: []*cloudstack.FirewallRule{{Id: "fw-1", Protocol: "tcp", Startport: 80, Endport: 80, Cidrlist: defaultAllowedCIDR}},
		}, nil),
		mockFirewall.EXPECT().NewDeleteFirewallRuleParams("fw-1").Return(deleteFirewall),
		mockFirewall.EXPECT().DeleteFirewallRule(deleteFirewall).Return(&cloudstack.DeleteFirewallRuleResponse{}, nil),
		mockLB.EXPECT().NewDeleteLoadBalancerRuleParams("rule-1").Return(deleteRule),
		mockLB.EXPECT().DeleteLoadBalancerRule(deleteRule).Return(&cloudstack.DeleteLoadBalancerRuleResponse{}, nil),
		mockAddress.EXPECT().NewDisassociateIpAddressParams("ip-1").Return(release),
		mockAddress.EXPECT().DisassociateIpAddress(release).Return(&cloudstack.DisassociateIpAddressResponse{}, nil),
	)

	cs := &CSCloud{client: &cloudstack.CloudStackClient{
		LoadBalancer: mockLB, Network: mockNetwork, Firewall: mockFirewall, Address: mockAddress,
	}}
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "test-service", Namespace: "default", UID: "test-uid"}}

	if err := cs.EnsureLoadBalancerDeleted(context.TODO(), "test-cluster", service); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ipID, _ := listParams.GetIpaddressid(); ipID != "ip-1" {
		t.Errorf("firewall rules listed on IP %q, want ip-1", ipID)
	}
}

// A Network ACL rule is scoped to a network, so an obsolete rule left over in another network
// is not protected by a desired port that happens to share its protocol and port.
func TestPruneRulesScopesNetworkACLsToTheirNetwork(t *testing.T) {
	aclNetwork := &cloudstack.Network{
		Id:      "net-new",
		Service: []cloudstack.NetworkServiceInternal{{Name: "NetworkACL"}},
	}
	firewallNetwork := &cloudstack.Network{
		Id:      "net-new",
		Service: []cloudstack.NetworkServiceInternal{{Name: "Firewall"}},
	}
	obsoleteRuleTier := &cloudstack.Network{
		Id:      "net-old",
		Service: []cloudstack.NetworkServiceInternal{{Name: "NetworkACL"}},
	}

	desired := []desiredLBRule{{
		name:     "atestuid-tcp-80",
		port:     corev1.ServicePort{Port: 80, NodePort: 30000, Protocol: corev1.ProtocolTCP},
		protocol: LoadBalancerProtocolTCP,
	}}
	desiredOn443 := []desiredLBRule{{
		name:     "atestuid-tcp-443",
		port:     corev1.ServicePort{Port: 443, NodePort: 30443, Protocol: corev1.ProtocolTCP},
		protocol: LoadBalancerProtocolTCP,
	}}

	obsoleteIn := func(networkID string) []obsoleteRule {
		return []obsoleteRule{{
			rule: &cloudstack.LoadBalancerRule{
				Id:         "rule-old",
				Name:       "atestuid-tcp-80",
				Publicipid: "ip-2",
				Publicport: "80",
				Protocol:   "tcp",
				Networkid:  networkID,
			},
			protocol: LoadBalancerProtocolTCP,
			tuple:    portProtocol{"tcp", 80},
		}}
	}
	obsoleteWithoutNetworkOn := func(publicIPID string) []obsoleteRule {
		obsolete := obsoleteIn("")
		obsolete[0].rule.Publicipid = publicIPID
		return obsolete
	}

	type pruneMocks struct {
		acl      *cloudstack.MockNetworkACLServiceIface
		network  *cloudstack.MockNetworkServiceIface
		firewall *cloudstack.MockFirewallServiceIface
	}

	newLB := func(ctrl *gomock.Controller) (*loadBalancer, *pruneMocks) {
		m := &pruneMocks{
			acl:      cloudstack.NewMockNetworkACLServiceIface(ctrl),
			network:  cloudstack.NewMockNetworkServiceIface(ctrl),
			firewall: cloudstack.NewMockFirewallServiceIface(ctrl),
		}
		mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)
		mockLB.EXPECT().NewDeleteLoadBalancerRuleParams("rule-old").Return(&cloudstack.DeleteLoadBalancerRuleParams{})
		mockLB.EXPECT().DeleteLoadBalancerRule(gomock.Any()).Return(&cloudstack.DeleteLoadBalancerRuleResponse{}, nil)

		return &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				LoadBalancer: mockLB,
				NetworkACL:   m.acl,
				Network:      m.network,
				Firewall:     m.firewall,
			},
			name:      "atestuid",
			networkID: "net-new",
			ipAddrID:  "ip-current",
		}, m
	}

	expectACLRuleDeleted := func(m *pruneMocks) *cloudstack.ListNetworkACLsParams {
		listParams := &cloudstack.ListNetworkACLsParams{}
		gomock.InOrder(
			m.acl.EXPECT().NewListNetworkACLsParams().Return(listParams),
			m.acl.EXPECT().ListNetworkACLs(gomock.Any()).Return(&cloudstack.ListNetworkACLsResponse{
				Count: 4,
				NetworkACLs: []*cloudstack.NetworkACL{
					legacyACLRule("acl-other-service", func(r *cloudstack.NetworkACL) { r.Reason = networkACLReasonPrefix + "aotheruid" }),
					ownACLRule("acl-rule-old"),
					legacyACLRule("acl-legacy", nil),
					legacyACLRule("acl-operator", func(r *cloudstack.NetworkACL) { r.Cidrlist = "10.0.0.0/8" }),
				},
			}, nil),
			m.acl.EXPECT().NewDeleteNetworkACLParams("acl-rule-old").Return(&cloudstack.DeleteNetworkACLParams{}),
			m.acl.EXPECT().DeleteNetworkACL(gomock.Any()).Return(&cloudstack.DeleteNetworkACLResponse{}, nil),
		)
		return listParams
	}

	t.Run("a rule in another network has its ACL rule deleted there", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		lb, m := newLB(ctrl)
		m.network.EXPECT().GetNetworkByID("net-old", gomock.Any()).Return(obsoleteRuleTier, 1, nil)
		listParams := expectACLRuleDeleted(m)

		if err := lb.pruneRules(obsoleteIn("net-old"), desired, aclNetwork); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if networkID, _ := listParams.GetNetworkid(); networkID != "net-old" {
			t.Errorf("ACL rules listed on network %q, want the obsolete rule's own %q", networkID, "net-old")
		}
	})

	t.Run("a rule in the reconciled network keeps its claimed ACL rule", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		// No ACL expectations: the desired tcp/80 port still claims that opening. No network
		// lookup either: the reconciled network is already known.
		lb, _ := newLB(ctrl)

		if err := lb.pruneRules(obsoleteIn("net-new"), desired, aclNetwork); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("a rule in a VPC tier is cleaned there while a firewall network reconciles", func(t *testing.T) {
		// Choosing the mechanism from the reconciled network would delete a firewall rule
		// that does not exist and leave this ACL rule open.
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		lb, m := newLB(ctrl)
		m.network.EXPECT().GetNetworkByID("net-old", gomock.Any()).Return(obsoleteRuleTier, 1, nil)
		listParams := expectACLRuleDeleted(m)

		if err := lb.pruneRules(obsoleteIn("net-old"), desiredOn443, firewallNetwork); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if networkID, _ := listParams.GetNetworkid(); networkID != "net-old" {
			t.Errorf("ACL rules listed on network %q, want the obsolete rule's own %q", networkID, "net-old")
		}
	})

	t.Run("a rule in a deleted network keeps its ACL rule", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		lb, m := newLB(ctrl)
		// GetNetworkByID reports not-found as an error alongside a count of 0.
		m.network.EXPECT().GetNetworkByID("net-old", gomock.Any()).Return(nil, 0, fmt.Errorf("No match found for net-old"))
		// Only the IP-scoped firewall rule is attempted: no network remains to place an
		// ACL rule in.
		gomock.InOrder(
			m.firewall.EXPECT().NewListFirewallRulesParams().Return(&cloudstack.ListFirewallRulesParams{}),
			m.firewall.EXPECT().ListFirewallRules(gomock.Any()).Return(&cloudstack.ListFirewallRulesResponse{}, nil),
		)

		if err := lb.pruneRules(obsoleteIn("net-old"), desiredOn443, aclNetwork); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("a failed network lookup keeps the rule and reports the error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		// No delete expectations: the rule must survive until its network can be looked up.
		mockNetwork := cloudstack.NewMockNetworkServiceIface(ctrl)
		mockNetwork.EXPECT().GetNetworkByID("net-old", gomock.Any()).Return(nil, -1, fmt.Errorf("API unavailable"))
		lb := &loadBalancer{
			CloudStackClient: &cloudstack.CloudStackClient{
				LoadBalancer: cloudstack.NewMockLoadBalancerServiceIface(ctrl),
				Network:      mockNetwork,
			},
			networkID: "net-new",
			ipAddrID:  "ip-current",
		}

		if err := lb.pruneRules(obsoleteIn("net-old"), desiredOn443, aclNetwork); err == nil {
			t.Fatalf("expected the lookup failure to be reported")
		}
	})

	t.Run("a rule with no network has only its firewall rule deleted", func(t *testing.T) {
		// An ACL rule deleted in a guessed network could be the only opening another service
		// has, while a firewall rule is scoped to this rule's own public IP.
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		lb, m := newLB(ctrl)
		gomock.InOrder(
			m.firewall.EXPECT().NewListFirewallRulesParams().Return(&cloudstack.ListFirewallRulesParams{}),
			m.firewall.EXPECT().ListFirewallRules(gomock.Any()).Return(&cloudstack.ListFirewallRulesResponse{}, nil),
		)

		if err := lb.pruneRules(obsoleteWithoutNetworkOn("ip-2"), desiredOn443, aclNetwork); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("a rule with no network on the reconciled IP uses that network", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		lb, m := newLB(ctrl)
		listParams := expectACLRuleDeleted(m)

		if err := lb.pruneRules(obsoleteWithoutNetworkOn("ip-current"), desiredOn443, aclNetwork); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if networkID, _ := listParams.GetNetworkid(); networkID != "net-new" {
			t.Errorf("ACL rules listed on network %q, want the reconciled %q", networkID, "net-new")
		}
	})
}

// virtualRouterStickinessCapability is the SupportedStickinessMethods capability of a VirtualRouter
// network, copied from the 4.22.1 simulator. It has not changed since 4.16.
const virtualRouterStickinessCapability = `[{"methodname":"LbCookie","paramlist":[{"paramname":"cookie-name","required":false,"isflag":false,"description":" "},{"paramname":"mode","required":false,"isflag":false,"description":" "},{"paramname":"nocache","required":false,"isflag":true,"description":" "},{"paramname":"indirect","required":false,"isflag":true,"description":" "},{"paramname":"postonly","required":false,"isflag":true,"description":" "},{"paramname":"domain","required":false,"isflag":false,"description":" "}],"description":"This is loadbalancer cookie based stickiness method."},{"methodname":"AppCookie","paramlist":[{"paramname":"cookie-name","required":false,"isflag":false,"description":" "},{"paramname":"length","required":false,"isflag":false,"description":" "},{"paramname":"holdtime","required":false,"isflag":false,"description":" "},{"paramname":"request-learn","required":false,"isflag":true,"description":" "},{"paramname":"prefix","required":false,"isflag":true,"description":" "},{"paramname":"mode","required":false,"isflag":false,"description":" "}],"description":"This is App session based sticky method."},{"methodname":"SourceBased","paramlist":[{"paramname":"tablesize","required":false,"isflag":false,"description":" "},{"paramname":"expire","required":false,"isflag":false,"description":" "}],"description":"This is source based Stickiness method, it can be used for any type of protocol."}]`

// virtualRouterNetwork returns a network that lists the VirtualRouter's stickiness methods.
func virtualRouterNetwork() *cloudstack.Network {
	return &cloudstack.Network{
		Id: "net-1",
		Service: []cloudstack.NetworkServiceInternal{
			{Name: "Firewall"},
			{Name: "Lb", Capability: []cloudstack.NetworkServiceInternalCapability{
				{Name: "SupportedStickinessMethods", Value: virtualRouterStickinessCapability},
			}},
		},
	}
}

// stickinessService builds a Service with the given ports and stickiness annotations. An empty
// value leaves the annotation out.
func stickinessService(method, params string, servicePorts ...corev1.ServicePort) *corev1.Service {
	annotations := map[string]string{}
	for key, value := range map[string]string{
		ServiceAnnotationLoadBalancerStickinessMethodName:  method,
		ServiceAnnotationLoadBalancerStickinessMethodParam: params,
	} {
		if value != "" {
			annotations[key] = value
		}
	}

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "test-service", Namespace: "default", Annotations: annotations},
		Spec:       corev1.ServiceSpec{Ports: servicePorts},
	}
}

func TestParseStickinessSpec(t *testing.T) {
	http := corev1.ServicePort{Name: "http", Port: 80, Protocol: corev1.ProtocolTCP}
	https := corev1.ServicePort{Name: "https", Port: 443, Protocol: corev1.ProtocolTCP}
	dns := corev1.ServicePort{Name: "dns", Port: 53, Protocol: corev1.ProtocolUDP}
	declaredHTTP := withAppProtocol(http, "http")
	declaredHTTPS := withAppProtocol(https, "https")

	tests := []struct {
		name       string
		service    *corev1.Service
		wantErr    string
		wantNil    bool
		wantParams map[string]string
		wantPorts  []int32
	}{
		{name: "no method means no stickiness", service: stickinessService("", "cookie-name=X", http), wantNil: true},
		{name: "LbCookie goes only on ports declaring appProtocol http", service: stickinessService("LbCookie", "", declaredHTTP, declaredHTTPS, dns), wantParams: map[string]string{}, wantPorts: []int32{80}},
		{name: "appProtocol is matched ignoring case", service: stickinessService("LbCookie", "", withAppProtocol(http, "HTTP"), https), wantParams: map[string]string{}, wantPorts: []int32{80}},
		{name: "LbCookie needs appProtocol http even on a single TCP port", service: stickinessService("LbCookie", "", http, dns), wantErr: "appProtocol: http"},
		{name: "LbCookie on several undeclared TCP ports needs appProtocol", service: stickinessService("LbCookie", "", http, https), wantErr: "appProtocol: http"},
		{name: "LbCookie on a port declaring https is rejected", service: stickinessService("LbCookie", "", declaredHTTPS), wantErr: "appProtocol: http"},
		{name: "SourceBased goes on every TCP port whatever appProtocol says", service: stickinessService("SourceBased", "", declaredHTTP, declaredHTTPS, dns), wantParams: map[string]string{}, wantPorts: []int32{80, 443}},
		{name: "AppCookie is rejected", service: stickinessService("appcookie", "", http), wantErr: "appsession"},
		{name: "params are trimmed and empty entries dropped", service: stickinessService("LbCookie", ", cookie-name = SERVERID ,nocache=true,", declaredHTTP), wantParams: map[string]string{"cookie-name": "SERVERID", "nocache": "true"}, wantPorts: []int32{80}},
		{name: "an empty value is rejected with a hint for flags", service: stickinessService("LbCookie", "nocache=", declaredHTTP), wantErr: "nocache=true"},
		{name: "an entry without = is rejected", service: stickinessService("LbCookie", "nocache", declaredHTTP), wantErr: "expected key=value"},
		{name: "= in a value is rejected", service: stickinessService("LbCookie", "cookie-name=a=b", declaredHTTP), wantErr: "are not allowed"},
		{name: "& in a value is rejected", service: stickinessService("LbCookie", "domain=x&y", declaredHTTP), wantErr: "are not allowed"},
		{name: "a quote in a value is rejected", service: stickinessService("LbCookie", `cookie-name=SERVER"ID`, declaredHTTP), wantErr: "are not allowed"},
		{name: "# in a value is rejected", service: stickinessService("LbCookie", "domain=a#b", declaredHTTP), wantErr: "are not allowed"},
		{name: "a backslash in a value is rejected", service: stickinessService("LbCookie", `cookie-name=a\b`, declaredHTTP), wantErr: "are not allowed"},
		{name: "whitespace in a value is rejected", service: stickinessService("LbCookie", "cookie-name=my cookie", declaredHTTP), wantErr: "are not allowed"},
		{name: "whitespace in a key is rejected", service: stickinessService("LbCookie", "cookie name=x", declaredHTTP), wantErr: "are not allowed"},
		{name: "a key set twice is rejected whatever its case", service: stickinessService("LbCookie", "mode=insert,MODE=rewrite", declaredHTTP), wantErr: "set twice"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, err := parseStickinessSpec(tt.service)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) || !strings.Contains(err.Error(), "stickiness") {
					t.Fatalf("error = %v, want it to mention %q and stickiness", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantNil {
				if spec != nil {
					t.Fatalf("spec = %+v, want nil", spec)
				}
				return
			}
			if !reflect.DeepEqual(spec.params, tt.wantParams) {
				t.Errorf("params = %v, want %v", spec.params, tt.wantParams)
			}
			gotPorts := slices.Sorted(maps.Keys(spec.ports))
			if !slices.Equal(gotPorts, tt.wantPorts) {
				t.Errorf("ports = %v, want %v", gotPorts, tt.wantPorts)
			}
		})
	}
}

func TestStickinessSpecWants(t *testing.T) {
	spec := &stickinessSpec{method: "LbCookie", ports: map[int32]bool{80: true}}
	tests := []struct {
		name string
		spec *stickinessSpec
		port corev1.ServicePort
		want bool
	}{
		{name: "a selected TCP port", spec: spec, port: corev1.ServicePort{Port: 80, Protocol: corev1.ProtocolTCP}, want: true},
		{name: "an unselected TCP port", spec: spec, port: corev1.ServicePort{Port: 443, Protocol: corev1.ProtocolTCP}},
		{name: "a UDP port with a selected number", spec: spec, port: corev1.ServicePort{Port: 80, Protocol: corev1.ProtocolUDP}},
		{name: "no spec", port: corev1.ServicePort{Port: 80, Protocol: corev1.ProtocolTCP}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.spec.wants(tt.port); got != tt.want {
				t.Errorf("wants = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSupportedStickinessMethods(t *testing.T) {
	if got := supportedStickinessMethods(virtualRouterNetwork()); len(got) != 3 || got[0].Name != "LbCookie" || !got[0].Params[2].IsFlag {
		t.Errorf("methods = %+v, want the VirtualRouter's three with nocache as a flag", got)
	}
	if got := supportedStickinessMethods(&cloudstack.Network{Service: []cloudstack.NetworkServiceInternal{{Name: "Firewall"}}}); got != nil {
		t.Errorf("methods = %+v, want nil for a network without the Lb service", got)
	}
	unreadable := &cloudstack.Network{Service: []cloudstack.NetworkServiceInternal{{Name: "Lb", Capability: []cloudstack.NetworkServiceInternalCapability{
		{Name: "SupportedStickinessMethods", Value: "not json"},
	}}}}
	if got := supportedStickinessMethods(unreadable); got != nil {
		t.Errorf("methods = %+v, want nil for an unreadable capability", got)
	}
}

func TestStickinessSpecNormalise(t *testing.T) {
	methods := supportedStickinessMethods(virtualRouterNetwork())
	tests := []struct {
		name       string
		method     string
		params     map[string]string
		methods    []stickinessMethod
		wantMethod string
		wantParams map[string]string
		wantErr    string
	}{
		{name: "method and param names take the advertised spelling", method: "lbcookie", params: map[string]string{"Cookie-Name": "SERVERID"}, methods: methods, wantMethod: "LbCookie", wantParams: map[string]string{"cookie-name": "SERVERID"}},
		{name: "a true flag is sent as true", method: "LbCookie", params: map[string]string{"nocache": "1"}, methods: methods, wantMethod: "LbCookie", wantParams: map[string]string{"nocache": "true"}},
		{name: "a false flag is left out", method: "LbCookie", params: map[string]string{"nocache": "false", "indirect": "True"}, methods: methods, wantMethod: "LbCookie", wantParams: map[string]string{"indirect": "true"}},
		{name: "a flag that is not a boolean is rejected", method: "LbCookie", params: map[string]string{"nocache": "maybe"}, methods: methods, wantErr: "true or false"},
		{name: "the cookie mode is lower-cased", method: "LbCookie", params: map[string]string{"mode": "INSERT"}, methods: methods, wantMethod: "LbCookie", wantParams: map[string]string{"mode": "insert"}},
		{name: "an unknown cookie mode is rejected", method: "LbCookie", params: map[string]string{"mode": "bogus"}, methods: methods, wantErr: "insert, rewrite, prefix"},
		{name: "an unknown method lists the supported ones", method: "NoSuchMethod", methods: methods, wantErr: "supported methods: LbCookie, SourceBased"},
		{name: "an unknown param lists the method's", method: "SourceBased", params: map[string]string{"cookie-name": "x"}, methods: methods, wantErr: "tablesize, expire"},
		{name: "a missing required param is rejected", method: "Custom", methods: []stickinessMethod{{Name: "Custom", Params: []stickinessMethodParam{{Name: "key", Required: true}}}}, wantErr: "needs parameter key"},
		{name: "without advertised methods nothing changes", method: "lbcookie", params: map[string]string{"nocache": "false"}, wantMethod: "lbcookie", wantParams: map[string]string{"nocache": "false"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := &stickinessSpec{method: tt.method, params: tt.params}
			if spec.params == nil {
				spec.params = map[string]string{}
			}
			err := spec.normalise(tt.methods)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) || !strings.Contains(err.Error(), "stickiness") {
					t.Fatalf("error = %v, want it to mention %q and stickiness", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if spec.method != tt.wantMethod || !maps.Equal(spec.params, tt.wantParams) {
				t.Errorf("spec = %s %v, want %s %v", spec.method, spec.params, tt.wantMethod, tt.wantParams)
			}
		})
	}

	var none *stickinessSpec
	if err := none.normalise(methods); err != nil {
		t.Errorf("normalising no spec: %v", err)
	}
}

func TestStickinessChanges(t *testing.T) {
	spec := &stickinessSpec{method: "LbCookie", params: map[string]string{"cookie-name": "SERVERID"}}
	ours := func(id string, params map[string]string) cloudstack.LBStickinessPolicyStickinesspolicy {
		return cloudstack.LBStickinessPolicyStickinesspolicy{Id: id, Methodname: "LbCookie", Params: params, Description: stickinessPolicyDescription}
	}
	foreign := func(id string) cloudstack.LBStickinessPolicyStickinesspolicy {
		return cloudstack.LBStickinessPolicyStickinesspolicy{Id: id, Methodname: "LbCookie", Params: map[string]string{"cookie-name": "SERVERID"}}
	}
	matching := ours("ours", map[string]string{"cookie-name": "SERVERID"})

	tests := []struct {
		name        string
		live        []cloudstack.LBStickinessPolicyStickinesspolicy
		want        bool
		wantCreate  bool
		wantDeletes []string
		wantErr     string
	}{
		{name: "a selected rule without a policy gets one", want: true, wantCreate: true},
		{name: "a selected rule with our matching policy is left alone", live: append([]cloudstack.LBStickinessPolicyStickinesspolicy{}, matching), want: true},
		{name: "a changed policy of ours is replaced by one create", live: append([]cloudstack.LBStickinessPolicyStickinesspolicy{}, ours("ours", map[string]string{"cookie-name": "OLD"})), want: true, wantCreate: true},
		{name: "an identical hand-made policy on a selected rule is replaced", live: append([]cloudstack.LBStickinessPolicyStickinesspolicy{}, foreign("manual")), want: true, wantCreate: true},
		{name: "ours and a hand-made policy are reported and nothing is deleted", live: append([]cloudstack.LBStickinessPolicyStickinesspolicy{}, matching, foreign("manual")), want: true, wantErr: "remove the extra ones"},
		{name: "two hand-made policies are reported and nothing is deleted", live: append([]cloudstack.LBStickinessPolicyStickinesspolicy{}, foreign("a"), foreign("b")), want: true, wantErr: "remove the extra ones"},
		{name: "an unselected rule loses only our policies", live: append([]cloudstack.LBStickinessPolicyStickinesspolicy{}, matching, foreign("manual"))},
		{name: "an unselected rule without policies is left alone"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantSpec := spec
			if !tt.want {
				wantSpec = nil
			}
			create, deletes, err := stickinessChanges(tt.live, wantSpec, tt.want)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want it to mention %q", err, tt.wantErr)
				}
				if create || len(deletes) > 0 {
					t.Errorf("create, deletes = %v, %v; want no changes with the error", create, deletes)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			wantDeletes := tt.wantDeletes
			if !tt.want && len(tt.live) > 0 {
				wantDeletes = []string{"ours"}
			}
			if create != tt.wantCreate || !slices.Equal(deletes, wantDeletes) {
				t.Errorf("create, deletes = %v, %v; want %v, %v", create, deletes, tt.wantCreate, wantDeletes)
			}
		})
	}
}

func TestLiveStickinessPolicies(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mockLB := cloudstack.NewMockLoadBalancerServiceIface(ctrl)
	params := &cloudstack.ListLBStickinessPoliciesParams{}
	mockLB.EXPECT().NewListLBStickinessPoliciesParams().Return(params)
	mockLB.EXPECT().ListLBStickinessPolicies(params).Return(&cloudstack.ListLBStickinessPoliciesResponse{
		Count: 1,
		LBStickinessPolicies: []*cloudstack.LBStickinessPolicy{{Stickinesspolicy: []cloudstack.LBStickinessPolicyStickinesspolicy{
			{Id: "revoked", State: "Revoked"},
			{Id: "live"},
		}}},
	}, nil)

	lb := &loadBalancer{CloudStackClient: &cloudstack.CloudStackClient{LoadBalancer: mockLB}}
	live, err := lb.liveStickinessPolicies("rule-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(live) != 1 || live[0].Id != "live" {
		t.Errorf("live = %+v, want only the live policy", live)
	}
	if ruleID, _ := params.GetLbruleid(); ruleID != "rule-1" {
		t.Errorf("lbruleid = %q, want rule-1", ruleID)
	}
}

// stickinessTestRule returns an up-to-date TCP rule of the "atestuid" load balancer for a port.
func stickinessTestRule(id string, port corev1.ServicePort) *cloudstack.LoadBalancerRule {
	return &cloudstack.LoadBalancerRule{
		Id:          id,
		Name:        fmt.Sprintf("atestuid-tcp-%d", port.Port),
		Publicip:    "10.0.0.1",
		Publicipid:  "ip-1",
		Publicport:  fmt.Sprint(port.Port),
		Privateport: fmt.Sprint(port.NodePort),
		Cidrlist:    defaultAllowedCIDR,
		Algorithm:   "roundrobin",
		Protocol:    "tcp",
		Networkid:   "net-1",
	}
}

// firewallRulesFor returns firewall rules that already open the given ports to everyone.
func firewallRulesFor(ports ...corev1.ServicePort) *cloudstack.ListFirewallRulesResponse {
	resp := &cloudstack.ListFirewallRulesResponse{}
	for _, port := range ports {
		resp.FirewallRules = append(resp.FirewallRules, &cloudstack.FirewallRule{
			Id:        fmt.Sprintf("fw-%d", port.Port),
			Protocol:  "tcp",
			Startport: int(port.Port),
			Endport:   int(port.Port),
			Cidrlist:  defaultAllowedCIDR,
		})
	}
	resp.Count = len(resp.FirewallRules)

	return resp
}

// stickinessAnnotations returns the stickiness annotations that are not empty.
func stickinessAnnotations(method, params string) map[string]string {
	return stickinessService(method, params).Annotations
}

// withAppProtocol returns the port declaring the given application protocol.
func withAppProtocol(port corev1.ServicePort, appProtocol string) corev1.ServicePort {
	port.AppProtocol = &appProtocol
	return port
}

func TestEnsureLoadBalancerStickiness(t *testing.T) {
	http := withAppProtocol(corev1.ServicePort{Name: "http", Port: 80, NodePort: 30000, Protocol: corev1.ProtocolTCP}, "http")
	alt := corev1.ServicePort{Name: "alt", Port: 8080, NodePort: 30080, Protocol: corev1.ProtocolTCP}
	cookie := map[string]string{"cookie-name": "SERVERID"}
	ours := func(params map[string]string) cloudstack.LBStickinessPolicyStickinesspolicy {
		return cloudstack.LBStickinessPolicyStickinesspolicy{Id: "policy-ours", Name: "atestuid-tcp-80", Methodname: "LbCookie", Params: params, Description: stickinessPolicyDescription}
	}
	manual := func(id string) cloudstack.LBStickinessPolicyStickinesspolicy {
		return cloudstack.LBStickinessPolicyStickinesspolicy{Id: id, Name: "manual", Methodname: "LbCookie", Params: cookie}
	}
	newEnv := func(t *testing.T, annotations map[string]string, ports ...corev1.ServicePort) *ensureLBTestEnv {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		return newEnsureLBTestEnv(ctrl, annotations, ports)
	}
	ensure := func(env *ensureLBTestEnv) error {
		_, err := env.cs.EnsureLoadBalancer(context.TODO(), "test-cluster", env.service, env.nodes)
		return err
	}
	expectCreate := func(env *ensureLBTestEnv, ruleID, method, ruleName string, createErr error) *cloudstack.CreateLBStickinessPolicyParams {
		params := &cloudstack.CreateLBStickinessPolicyParams{}
		env.lb.EXPECT().NewCreateLBStickinessPolicyParams(ruleID, method, ruleName).Return(params)
		env.lb.EXPECT().CreateLBStickinessPolicy(params).Return(&cloudstack.CreateLBStickinessPolicyResponse{}, createErr)
		return params
	}
	expectDelete := func(env *ensureLBTestEnv, policyID string) {
		params := &cloudstack.DeleteLBStickinessPolicyParams{}
		env.lb.EXPECT().NewDeleteLBStickinessPolicyParams(policyID).Return(params)
		env.lb.EXPECT().DeleteLBStickinessPolicy(params).Return(&cloudstack.DeleteLBStickinessPolicyResponse{}, nil)
	}
	sentParams := func(t *testing.T, params *cloudstack.CreateLBStickinessPolicyParams) map[string]string {
		t.Helper()
		if description, _ := params.GetDescription(); description != stickinessPolicyDescription {
			t.Errorf("policy description = %q, want the controller's marker", description)
		}
		sent, _ := params.GetParam()
		return sent
	}

	t.Run("a policy is created once the rule and its firewall are in place", func(t *testing.T) {
		env := newEnv(t, stickinessAnnotations("lbcookie", "Cookie-Name=SERVERID, nocache=1"), http)
		env.expectHostsAndNetworkWith(virtualRouterNetwork())
		env.expectRules(stickinessTestRule("rule-1", http))
		create := &cloudstack.CreateLBStickinessPolicyParams{}
		gomock.InOrder(
			env.firewall.EXPECT().NewListFirewallRulesParams().Return(&cloudstack.ListFirewallRulesParams{}),
			env.firewall.EXPECT().ListFirewallRules(gomock.Any()).Return(firewallRulesFor(http), nil),
			env.lb.EXPECT().NewCreateLBStickinessPolicyParams("rule-1", "LbCookie", "atestuid-tcp-80").Return(create),
			env.lb.EXPECT().CreateLBStickinessPolicy(create).Return(&cloudstack.CreateLBStickinessPolicyResponse{}, nil),
		)

		if err := ensure(env); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sent := sentParams(t, create); !maps.Equal(sent, map[string]string{"cookie-name": "SERVERID", "nocache": "true"}) {
			t.Errorf("sent params = %v, want the normalised cookie-name and nocache=true", sent)
		}
		if !slices.Equal(env.policyLookups, []string{"rule-1"}) {
			t.Errorf("policy lookups = %v, want one for rule-1", env.policyLookups)
		}
	})

	t.Run("the controller's matching policy is left alone", func(t *testing.T) {
		env := newEnv(t, stickinessAnnotations("LbCookie", "cookie-name=SERVERID"), http)
		env.expectHostsAndNetworkWith(virtualRouterNetwork())
		env.expectRules(stickinessTestRule("rule-1", http))
		env.expectFirewallListings(firewallRulesFor(http), 1)
		env.policies["rule-1"] = []cloudstack.LBStickinessPolicyStickinesspolicy{ours(cookie)}

		if err := ensure(env); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("a changed parameter is swapped by one create, with no delete", func(t *testing.T) {
		env := newEnv(t, stickinessAnnotations("LbCookie", "cookie-name=SERVERID"), http)
		env.expectHostsAndNetworkWith(virtualRouterNetwork())
		env.expectRules(stickinessTestRule("rule-1", http))
		env.expectFirewallListings(firewallRulesFor(http), 1)
		env.policies["rule-1"] = []cloudstack.LBStickinessPolicyStickinesspolicy{ours(map[string]string{"cookie-name": "OLD"})}
		create := expectCreate(env, "rule-1", "LbCookie", "atestuid-tcp-80", nil)

		if err := ensure(env); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sent := sentParams(t, create); !maps.Equal(sent, cookie) {
			t.Errorf("sent params = %v, want %v", sent, cookie)
		}
	})

	t.Run("a hand-made policy on a selected port is replaced by the controller's", func(t *testing.T) {
		env := newEnv(t, stickinessAnnotations("LbCookie", "cookie-name=SERVERID"), http)
		env.expectHostsAndNetworkWith(virtualRouterNetwork())
		env.expectRules(stickinessTestRule("rule-1", http))
		env.expectFirewallListings(firewallRulesFor(http), 1)
		env.policies["rule-1"] = []cloudstack.LBStickinessPolicyStickinesspolicy{manual("policy-manual")}
		expectCreate(env, "rule-1", "LbCookie", "atestuid-tcp-80", nil)

		if err := ensure(env); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("a hand-made policy on an unannotated Service is kept", func(t *testing.T) {
		env := newEnv(t, nil, http)
		env.expectHostsAndNetwork()
		env.expectRules(stickinessTestRule("rule-1", http))
		env.expectFirewallListings(firewallRulesFor(http), 1)
		env.policies["rule-1"] = []cloudstack.LBStickinessPolicyStickinesspolicy{manual("policy-manual")}

		if err := ensure(env); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("removing the annotation deletes only the controller's policy", func(t *testing.T) {
		env := newEnv(t, nil, http)
		env.expectHostsAndNetwork()
		env.expectRules(stickinessTestRule("rule-1", http))
		env.expectFirewallListings(firewallRulesFor(http), 1)
		env.policies["rule-1"] = []cloudstack.LBStickinessPolicyStickinesspolicy{ours(cookie), manual("policy-manual")}
		expectDelete(env, "policy-ours")

		if err := ensure(env); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("LbCookie follows appProtocol http and leaves other ports", func(t *testing.T) {
		env := newEnv(t, stickinessAnnotations("LbCookie", "cookie-name=SERVERID"), withAppProtocol(http, "https"), withAppProtocol(alt, "http"))
		env.expectHostsAndNetworkWith(virtualRouterNetwork())
		env.expectRules(stickinessTestRule("rule-1", http), stickinessTestRule("rule-2", alt))
		env.expectFirewallListings(firewallRulesFor(http, alt), 2)
		env.policies["rule-1"] = []cloudstack.LBStickinessPolicyStickinesspolicy{ours(cookie)}
		expectDelete(env, "policy-ours")
		expectCreate(env, "rule-2", "LbCookie", "atestuid-tcp-8080", nil)

		if err := ensure(env); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("LbCookie on ports declaring no appProtocol fails before any CloudStack call", func(t *testing.T) {
		undeclared := http
		undeclared.AppProtocol = nil
		env := newEnv(t, stickinessAnnotations("LbCookie", ""), undeclared, alt)

		if err := ensure(env); err == nil || !strings.Contains(err.Error(), "appProtocol: http") {
			t.Fatalf("error = %v, want it to ask for appProtocol: http", err)
		}
	})

	t.Run("an invalid parameter fails before any CloudStack call", func(t *testing.T) {
		env := newEnv(t, stickinessAnnotations("LbCookie", "cookie-name="), http)

		if err := ensure(env); err == nil || !strings.Contains(err.Error(), "missing value") {
			t.Fatalf("error = %v, want the empty value rejected", err)
		}
	})

	t.Run("a method the network does not offer fails before any write", func(t *testing.T) {
		env := newEnv(t, stickinessAnnotations("NoSuchMethod", ""), http)
		env.expectHostsAndNetworkWith(virtualRouterNetwork())
		env.expectRules(stickinessTestRule("rule-1", http))

		if err := ensure(env); err == nil || !strings.Contains(err.Error(), "supported methods: LbCookie, SourceBased") {
			t.Fatalf("error = %v, want it to list the supported methods", err)
		}
	})

	t.Run("a false flag is not sent", func(t *testing.T) {
		env := newEnv(t, stickinessAnnotations("LbCookie", "cookie-name=SERVERID,nocache=false"), http)
		env.expectHostsAndNetworkWith(virtualRouterNetwork())
		env.expectRules(stickinessTestRule("rule-1", http))
		env.expectFirewallListings(firewallRulesFor(http), 1)
		create := expectCreate(env, "rule-1", "LbCookie", "atestuid-tcp-80", nil)

		if err := ensure(env); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sent := sentParams(t, create); !maps.Equal(sent, cookie) {
			t.Errorf("sent params = %v, want %v without nocache", sent, cookie)
		}
	})

	t.Run("without the capability parameters pass through unchanged", func(t *testing.T) {
		env := newEnv(t, stickinessAnnotations("LbCookie", "nocache=false"), http)
		env.expectHostsAndNetwork()
		env.expectRules(stickinessTestRule("rule-1", http))
		env.expectFirewallListings(firewallRulesFor(http), 1)
		create := expectCreate(env, "rule-1", "LbCookie", "atestuid-tcp-80", nil)

		if err := ensure(env); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if sent := sentParams(t, create); !maps.Equal(sent, map[string]string{"nocache": "false"}) {
			t.Errorf("sent params = %v, want nocache=false passed through", sent)
		}
	})

	t.Run("a rejected policy keeps the rule and its firewall opening", func(t *testing.T) {
		env := newEnv(t, stickinessAnnotations("SourceBased", "tablesize=abc"), http)
		env.expectHostsAndNetworkWith(virtualRouterNetwork())
		env.expectRules(stickinessTestRule("rule-1", http))
		create := &cloudstack.CreateLBStickinessPolicyParams{}
		gomock.InOrder(
			env.firewall.EXPECT().NewListFirewallRulesParams().Return(&cloudstack.ListFirewallRulesParams{}),
			env.firewall.EXPECT().ListFirewallRules(gomock.Any()).Return(firewallRulesFor(http), nil),
			env.lb.EXPECT().NewCreateLBStickinessPolicyParams("rule-1", "SourceBased", "atestuid-tcp-80").Return(create),
			env.lb.EXPECT().CreateLBStickinessPolicy(create).Return(nil, fmt.Errorf("CloudStack API error 431: tablesize is not in size format")),
		)

		err := ensure(env)
		if err == nil || !strings.Contains(err.Error(), "error creating stickiness policy") || !strings.Contains(err.Error(), "tablesize") {
			t.Fatalf("error = %v, want CloudStack's rejection of the policy", err)
		}
	})

	t.Run("every port's failure is reported together", func(t *testing.T) {
		env := newEnv(t, stickinessAnnotations("SourceBased", "tablesize=200k"), http, alt)
		env.expectHostsAndNetworkWith(virtualRouterNetwork())
		env.expectRules(stickinessTestRule("rule-1", http), stickinessTestRule("rule-2", alt))
		env.expectFirewallListings(firewallRulesFor(http, alt), 2)
		expectCreate(env, "rule-1", "SourceBased", "atestuid-tcp-80", fmt.Errorf("router A unavailable"))
		expectCreate(env, "rule-2", "SourceBased", "atestuid-tcp-8080", fmt.Errorf("router B unavailable"))

		err := ensure(env)
		if err == nil || !strings.Contains(err.Error(), "router A unavailable") || !strings.Contains(err.Error(), "router B unavailable") {
			t.Fatalf("error = %v, want both ports' failures", err)
		}
	})

	t.Run("a recreated rule gets its policy without a lookup", func(t *testing.T) {
		moved := http
		moved.NodePort = 30001
		env := newEnv(t, stickinessAnnotations("LbCookie", "cookie-name=SERVERID"), moved)
		env.expectHostsAndNetworkWith(virtualRouterNetwork())
		env.expectRules(stickinessTestRule("rule-1", http))
		create := &cloudstack.CreateLBStickinessPolicyParams{}
		gomock.InOrder(
			env.lb.EXPECT().NewDeleteLoadBalancerRuleParams("rule-1").Return(&cloudstack.DeleteLoadBalancerRuleParams{}),
			env.lb.EXPECT().DeleteLoadBalancerRule(gomock.Any()).Return(&cloudstack.DeleteLoadBalancerRuleResponse{}, nil),
			env.lb.EXPECT().NewCreateLoadBalancerRuleParams("roundrobin", "atestuid-tcp-80", 30001, 80).Return(&cloudstack.CreateLoadBalancerRuleParams{}),
			env.lb.EXPECT().CreateLoadBalancerRule(gomock.Any()).Return(&cloudstack.CreateLoadBalancerRuleResponse{
				Id: "rule-new", Name: "atestuid-tcp-80", Publicip: "10.0.0.1", Publicipid: "ip-1", Protocol: "tcp",
			}, nil),
			env.lb.EXPECT().NewAssignToLoadBalancerRuleParams("rule-new").Return(&cloudstack.AssignToLoadBalancerRuleParams{}),
			env.lb.EXPECT().AssignToLoadBalancerRule(gomock.Any()).Return(&cloudstack.AssignToLoadBalancerRuleResponse{}, nil),
			env.firewall.EXPECT().NewListFirewallRulesParams().Return(&cloudstack.ListFirewallRulesParams{}),
			env.firewall.EXPECT().ListFirewallRules(gomock.Any()).Return(firewallRulesFor(http), nil),
			env.lb.EXPECT().NewCreateLBStickinessPolicyParams("rule-new", "LbCookie", "atestuid-tcp-80").Return(create),
			env.lb.EXPECT().CreateLBStickinessPolicy(create).Return(&cloudstack.CreateLBStickinessPolicyResponse{}, nil),
		)

		if err := ensure(env); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(env.policyLookups) != 0 {
			t.Errorf("policy lookups = %v, want none for a rule created in this run", env.policyLookups)
		}
	})

	t.Run("a proxy protocol toggle keeps the renamed rule's policy", func(t *testing.T) {
		annotations := stickinessAnnotations("LbCookie", "cookie-name=SERVERID")
		annotations[ServiceAnnotationLoadBalancerProxyProtocol] = "true"
		env := newEnv(t, annotations, http)
		env.expectHostsAndNetworkWith(virtualRouterNetwork())
		env.expectRules(stickinessTestRule("rule-1", http))
		env.expectFirewallListings(firewallRulesFor(http), 1)
		env.policies["rule-1"] = []cloudstack.LBStickinessPolicyStickinesspolicy{ours(cookie)}
		update := &cloudstack.UpdateLoadBalancerRuleParams{}
		env.lb.EXPECT().NewUpdateLoadBalancerRuleParams("rule-1").Return(update)
		env.lb.EXPECT().UpdateLoadBalancerRule(update).Return(&cloudstack.UpdateLoadBalancerRuleResponse{}, nil)

		if err := ensure(env); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if name, _ := update.GetName(); name != "atestuid-tcp-proxy-80" {
			t.Errorf("updated name = %q, want atestuid-tcp-proxy-80", name)
		}
	})

	t.Run("a prune failure still runs the stickiness phase", func(t *testing.T) {
		env := newEnv(t, stickinessAnnotations("LbCookie", "cookie-name=SERVERID"), http)
		env.expectHostsAndNetworkWith(virtualRouterNetwork())
		obsolete := stickinessTestRule("rule-obsolete", alt)
		env.expectRules(obsolete, stickinessTestRule("rule-1", http))
		env.expectFirewallListings(firewallRulesFor(http), 2)
		env.lb.EXPECT().NewDeleteLoadBalancerRuleParams("rule-obsolete").Return(&cloudstack.DeleteLoadBalancerRuleParams{})
		env.lb.EXPECT().DeleteLoadBalancerRule(gomock.Any()).Return(nil, fmt.Errorf("delete rule API error"))
		expectCreate(env, "rule-1", "LbCookie", "atestuid-tcp-80", nil)

		err := ensure(env)
		if err == nil || !strings.Contains(err.Error(), "delete rule API error") {
			t.Fatalf("error = %v, want the prune failure reported", err)
		}
	})

	t.Run("a rule with two policies is reported and keeps both", func(t *testing.T) {
		for name, live := range map[string][]cloudstack.LBStickinessPolicyStickinesspolicy{
			"ours and a hand-made one": {ours(map[string]string{"cookie-name": "OLD"}), manual("b")},
			"two hand-made ones":       {manual("a"), manual("b")},
		} {
			t.Run(name, func(t *testing.T) {
				env := newEnv(t, stickinessAnnotations("LbCookie", "cookie-name=SERVERID"), http)
				env.expectHostsAndNetworkWith(virtualRouterNetwork())
				env.expectRules(stickinessTestRule("rule-1", http))
				env.expectFirewallListings(firewallRulesFor(http), 1)
				env.policies["rule-1"] = live

				if err := ensure(env); err == nil || !strings.Contains(err.Error(), "remove the extra ones") {
					t.Fatalf("error = %v, want the hint to remove the extra policies", err)
				}
			})
		}
	})

	notAllowed := fmt.Errorf("CloudStack API error 432 (CSExceptionErrorCode: 9999): The API [listLBStickinessPolicies] does not exist or is not available for the account")

	t.Run("an account without the stickiness APIs still syncs a Service without stickiness", func(t *testing.T) {
		env := newEnv(t, nil, http)
		env.expectHostsAndNetwork()
		env.expectRules(stickinessTestRule("rule-1", http))
		env.expectFirewallListings(firewallRulesFor(http), 1)
		env.policyErr = notAllowed

		if err := ensure(env); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("an account without the stickiness APIs gets a clear error when stickiness is set", func(t *testing.T) {
		env := newEnv(t, stickinessAnnotations("LbCookie", "cookie-name=SERVERID"), http)
		env.expectHostsAndNetworkWith(virtualRouterNetwork())
		env.expectRules(stickinessTestRule("rule-1", http))
		env.expectFirewallListings(firewallRulesFor(http), 1)
		env.policyErr = notAllowed

		if err := ensure(env); err == nil || !strings.Contains(err.Error(), "stickiness needs the listLBStickinessPolicies") {
			t.Fatalf("error = %v, want it to name the missing APIs", err)
		}
	})

	t.Run("other policy lookup errors still fail the sync", func(t *testing.T) {
		env := newEnv(t, nil, http)
		env.expectHostsAndNetwork()
		env.expectRules(stickinessTestRule("rule-1", http))
		env.expectFirewallListings(firewallRulesFor(http), 1)
		env.policyErr = fmt.Errorf("CloudStack API error 530: internal error")

		if err := ensure(env); err == nil || !strings.Contains(err.Error(), "internal error") {
			t.Fatalf("error = %v, want the lookup failure", err)
		}
	})

	t.Run("a revoked policy is ignored", func(t *testing.T) {
		env := newEnv(t, stickinessAnnotations("LbCookie", "cookie-name=SERVERID"), http)
		env.expectHostsAndNetworkWith(virtualRouterNetwork())
		env.expectRules(stickinessTestRule("rule-1", http))
		env.expectFirewallListings(firewallRulesFor(http), 1)
		revoked := ours(cookie)
		revoked.State = "Revoked"
		env.policies["rule-1"] = []cloudstack.LBStickinessPolicyStickinesspolicy{revoked}
		expectCreate(env, "rule-1", "LbCookie", "atestuid-tcp-80", nil)

		if err := ensure(env); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestEnsureLoadBalancerRepairsEmptyRules(t *testing.T) {
	http := corev1.ServicePort{Name: "http", Port: 80, NodePort: 30000, Protocol: corev1.ProtocolTCP}

	t.Run("an adopted rule with no hosts gets them", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		env := newEnsureLBTestEnv(ctrl, nil, []corev1.ServicePort{http})
		env.expectHostsAndNetwork()
		env.expectRules(stickinessTestRule("rule-1", http))
		env.expectFirewallListings(firewallRulesFor(http), 1)
		env.instances = nil
		assign := &cloudstack.AssignToLoadBalancerRuleParams{}
		env.lb.EXPECT().NewAssignToLoadBalancerRuleParams("rule-1").Return(assign)
		env.lb.EXPECT().AssignToLoadBalancerRule(assign).Return(&cloudstack.AssignToLoadBalancerRuleResponse{}, nil)

		if _, err := env.cs.EnsureLoadBalancer(context.TODO(), "test-cluster", env.service, env.nodes); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ids, _ := assign.GetVirtualmachineids(); !slices.Equal(ids, []string{"vm-1"}) {
			t.Errorf("assigned VMs = %v, want [vm-1]", ids)
		}
	})

	t.Run("an adopted rule with other hosts is left to node sync", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		env := newEnsureLBTestEnv(ctrl, nil, []corev1.ServicePort{http})
		env.expectHostsAndNetwork()
		env.expectRules(stickinessTestRule("rule-1", http))
		env.expectFirewallListings(firewallRulesFor(http), 1)
		env.instances = []*cloudstack.VirtualMachine{{Id: "vm-2"}}

		if _, err := env.cs.EnsureLoadBalancer(context.TODO(), "test-cluster", env.service, env.nodes); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestEnsureLoadBalancerFreshIP(t *testing.T) {
	http := corev1.ServicePort{Name: "http", Port: 80, NodePort: 30000, Protocol: corev1.ProtocolTCP}
	freshService := func(t *testing.T, annotations map[string]string, createRuleErr error) *ensureLBTestEnv {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		env := newEnsureLBTestEnv(ctrl, annotations, []corev1.ServicePort{http})
		env.expectHosts()
		env.expectRules()
		env.network.EXPECT().GetNetworkByID("net-1", gomock.Any()).Return(virtualRouterNetwork(), 1, nil).Times(2)
		env.address.EXPECT().NewAssociateIpAddressParams().Return(&cloudstack.AssociateIpAddressParams{})
		env.address.EXPECT().AssociateIpAddress(gomock.Any()).Return(&cloudstack.AssociateIpAddressResponse{Id: "ip-new", Ipaddress: "10.0.0.9"}, nil)
		env.lb.EXPECT().NewCreateLoadBalancerRuleParams("roundrobin", "atestuid-tcp-80", 30000, 80).Return(&cloudstack.CreateLoadBalancerRuleParams{})
		if createRuleErr != nil {
			env.lb.EXPECT().CreateLoadBalancerRule(gomock.Any()).Return(nil, createRuleErr)
			return env
		}
		env.lb.EXPECT().CreateLoadBalancerRule(gomock.Any()).Return(&cloudstack.CreateLoadBalancerRuleResponse{
			Id: "rule-new", Name: "atestuid-tcp-80", Publicip: "10.0.0.9", Publicipid: "ip-new", Protocol: "tcp",
		}, nil)
		env.lb.EXPECT().NewAssignToLoadBalancerRuleParams("rule-new").Return(&cloudstack.AssignToLoadBalancerRuleParams{})
		env.lb.EXPECT().AssignToLoadBalancerRule(gomock.Any()).Return(&cloudstack.AssignToLoadBalancerRuleResponse{}, nil)
		env.expectFirewallListings(&cloudstack.ListFirewallRulesResponse{}, 1)
		env.firewall.EXPECT().NewCreateFirewallRuleParams("ip-new", "tcp").Return(&cloudstack.CreateFirewallRuleParams{})
		env.firewall.EXPECT().CreateFirewallRule(gomock.Any()).Return(&cloudstack.CreateFirewallRuleResponse{}, nil)
		return env
	}

	t.Run("an unsupported method fails before an IP is associated", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		env := newEnsureLBTestEnv(ctrl, stickinessAnnotations("NoSuchMethod", ""), []corev1.ServicePort{http})
		env.expectHosts()
		env.expectRules()
		env.network.EXPECT().GetNetworkByID("net-1", gomock.Any()).Return(virtualRouterNetwork(), 1, nil)

		_, err := env.cs.EnsureLoadBalancer(context.TODO(), "test-cluster", env.service, env.nodes)
		if err == nil || !strings.Contains(err.Error(), "not supported on this network") {
			t.Fatalf("error = %v, want the unsupported method rejected", err)
		}
	})

	t.Run("a rejected policy keeps the new IP and its rules", func(t *testing.T) {
		env := freshService(t, stickinessAnnotations("SourceBased", "tablesize=abc"), nil)
		env.lb.EXPECT().NewCreateLBStickinessPolicyParams("rule-new", "SourceBased", "atestuid-tcp-80").Return(&cloudstack.CreateLBStickinessPolicyParams{})
		env.lb.EXPECT().CreateLBStickinessPolicy(gomock.Any()).Return(nil, fmt.Errorf("tablesize is not in size format"))

		_, err := env.cs.EnsureLoadBalancer(context.TODO(), "test-cluster", env.service, env.nodes)
		if err == nil || !strings.Contains(err.Error(), "stickiness") {
			t.Fatalf("error = %v, want the policy rejection", err)
		}
	})

	t.Run("a failure before the rules exist still releases the new IP", func(t *testing.T) {
		env := freshService(t, nil, fmt.Errorf("create rule API error"))
		release := &cloudstack.DisassociateIpAddressParams{}
		env.address.EXPECT().NewDisassociateIpAddressParams("ip-new").Return(release)
		env.address.EXPECT().DisassociateIpAddress(release).Return(&cloudstack.DisassociateIpAddressResponse{}, nil)

		_, err := env.cs.EnsureLoadBalancer(context.TODO(), "test-cluster", env.service, env.nodes)
		if err == nil || !strings.Contains(err.Error(), "create rule API error") {
			t.Fatalf("error = %v, want the rule creation failure", err)
		}
	})
}
