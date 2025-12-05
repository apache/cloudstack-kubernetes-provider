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
	"strings"
	"testing"

	"github.com/apache/cloudstack-go/v2/cloudstack"
	corev1 "k8s.io/api/core/v1"
)

func TestNodeAddresses(t *testing.T) {
	cs := &CSCloud{}

	tests := []struct {
		name        string
		instance    *cloudstack.VirtualMachine
		wantAddrs   []corev1.NodeAddress
		wantErr     bool
		errContains string
	}{
		{
			name: "instance with internal IP only",
			instance: &cloudstack.VirtualMachine{
				Id:   "vm-1",
				Name: "test-vm",
				Nic: []cloudstack.Nic{
					{Ipaddress: "10.0.0.1"},
				},
			},
			wantAddrs: []corev1.NodeAddress{
				{Type: corev1.NodeInternalIP, Address: "10.0.0.1"},
			},
			wantErr: false,
		},
		{
			name: "instance with internal IP and hostname",
			instance: &cloudstack.VirtualMachine{
				Id:       "vm-1",
				Name:     "test-vm",
				Hostname: "test-hostname",
				Nic: []cloudstack.Nic{
					{Ipaddress: "10.0.0.1"},
				},
			},
			wantAddrs: []corev1.NodeAddress{
				{Type: corev1.NodeInternalIP, Address: "10.0.0.1"},
				{Type: corev1.NodeHostName, Address: "test-hostname"},
			},
			wantErr: false,
		},
		{
			name: "instance with internal IP and public IP",
			instance: &cloudstack.VirtualMachine{
				Id:       "vm-1",
				Name:     "test-vm",
				Publicip: "203.0.113.1",
				Nic: []cloudstack.Nic{
					{Ipaddress: "10.0.0.1"},
				},
			},
			wantAddrs: []corev1.NodeAddress{
				{Type: corev1.NodeInternalIP, Address: "10.0.0.1"},
				{Type: corev1.NodeExternalIP, Address: "203.0.113.1"},
			},
			wantErr: false,
		},
		{
			name: "instance with all address types",
			instance: &cloudstack.VirtualMachine{
				Id:       "vm-1",
				Name:     "test-vm",
				Hostname: "test-hostname",
				Publicip: "203.0.113.1",
				Nic: []cloudstack.Nic{
					{Ipaddress: "10.0.0.1"},
				},
			},
			wantAddrs: []corev1.NodeAddress{
				{Type: corev1.NodeInternalIP, Address: "10.0.0.1"},
				{Type: corev1.NodeHostName, Address: "test-hostname"},
				{Type: corev1.NodeExternalIP, Address: "203.0.113.1"},
			},
			wantErr: false,
		},
		{
			name: "instance with no NICs returns error",
			instance: &cloudstack.VirtualMachine{
				Id:   "vm-1",
				Name: "test-vm",
				Nic:  []cloudstack.Nic{},
			},
			wantAddrs:   nil,
			wantErr:     true,
			errContains: "does not have an internal IP",
		},
		{
			name: "instance with nil NICs returns error",
			instance: &cloudstack.VirtualMachine{
				Id:   "vm-1",
				Name: "test-vm",
				Nic:  nil,
			},
			wantAddrs:   nil,
			wantErr:     true,
			errContains: "does not have an internal IP",
		},
		{
			name: "instance with multiple NICs uses first",
			instance: &cloudstack.VirtualMachine{
				Id:   "vm-1",
				Name: "test-vm",
				Nic: []cloudstack.Nic{
					{Ipaddress: "10.0.0.1"},
					{Ipaddress: "10.0.0.2"},
				},
			},
			wantAddrs: []corev1.NodeAddress{
				{Type: corev1.NodeInternalIP, Address: "10.0.0.1"},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAddrs, err := cs.nodeAddresses(tt.instance)

			if tt.wantErr {
				if err == nil {
					t.Errorf("nodeAddresses() expected error, got nil")
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("nodeAddresses() error = %v, want error containing %q", err, tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("nodeAddresses() unexpected error: %v", err)
				return
			}

			if len(gotAddrs) != len(tt.wantAddrs) {
				t.Errorf("nodeAddresses() returned %d addresses, want %d", len(gotAddrs), len(tt.wantAddrs))
				return
			}

			for i, want := range tt.wantAddrs {
				if gotAddrs[i].Type != want.Type || gotAddrs[i].Address != want.Address {
					t.Errorf("nodeAddresses()[%d] = {%v, %v}, want {%v, %v}",
						i, gotAddrs[i].Type, gotAddrs[i].Address, want.Type, want.Address)
				}
			}
		})
	}
}
