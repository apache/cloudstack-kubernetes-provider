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
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestLoadBalancerProtocol_CSProtocol(t *testing.T) {
	tests := []struct {
		name     string
		protocol LoadBalancerProtocol
		want     string
	}{
		{
			name:     "TCP protocol",
			protocol: LoadBalancerProtocolTCP,
			want:     "tcp",
		},
		{
			name:     "UDP protocol",
			protocol: LoadBalancerProtocolUDP,
			want:     "udp",
		},
		{
			name:     "TCP Proxy protocol",
			protocol: LoadBalancerProtocolTCPProxy,
			want:     "tcp-proxy",
		},
		{
			name:     "Invalid protocol",
			protocol: LoadBalancerProtocolInvalid,
			want:     "",
		},
		{
			name:     "Unknown protocol value",
			protocol: LoadBalancerProtocol(999),
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.protocol.CSProtocol(); got != tt.want {
				t.Errorf("CSProtocol() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLoadBalancerProtocol_IPProtocol(t *testing.T) {
	tests := []struct {
		name     string
		protocol LoadBalancerProtocol
		want     string
	}{
		{
			name:     "TCP protocol maps to tcp",
			protocol: LoadBalancerProtocolTCP,
			want:     "tcp",
		},
		{
			name:     "TCP Proxy protocol also maps to tcp",
			protocol: LoadBalancerProtocolTCPProxy,
			want:     "tcp",
		},
		{
			name:     "UDP protocol maps to udp",
			protocol: LoadBalancerProtocolUDP,
			want:     "udp",
		},
		{
			name:     "Invalid protocol returns empty",
			protocol: LoadBalancerProtocolInvalid,
			want:     "",
		},
		{
			name:     "Unknown protocol value returns empty",
			protocol: LoadBalancerProtocol(999),
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.protocol.IPProtocol(); got != tt.want {
				t.Errorf("IPProtocol() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLoadBalancerProtocol_String(t *testing.T) {
	// String() should return the same as CSProtocol()
	protocols := []LoadBalancerProtocol{
		LoadBalancerProtocolTCP,
		LoadBalancerProtocolUDP,
		LoadBalancerProtocolTCPProxy,
		LoadBalancerProtocolInvalid,
	}

	for _, p := range protocols {
		if got, want := p.String(), p.CSProtocol(); got != want {
			t.Errorf("String() = %v, want %v (same as CSProtocol)", got, want)
		}
	}
}

func TestProtocolFromLoadBalancer(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
		want     LoadBalancerProtocol
	}{
		{
			name:     "tcp string",
			protocol: "tcp",
			want:     LoadBalancerProtocolTCP,
		},
		{
			name:     "udp string",
			protocol: "udp",
			want:     LoadBalancerProtocolUDP,
		},
		{
			name:     "tcp-proxy string",
			protocol: "tcp-proxy",
			want:     LoadBalancerProtocolTCPProxy,
		},
		{
			name:     "empty string returns invalid",
			protocol: "",
			want:     LoadBalancerProtocolInvalid,
		},
		{
			name:     "unknown protocol returns invalid",
			protocol: "icmp",
			want:     LoadBalancerProtocolInvalid,
		},
		{
			name:     "uppercase TCP returns invalid",
			protocol: "TCP",
			want:     LoadBalancerProtocolInvalid,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ProtocolFromLoadBalancer(tt.protocol); got != tt.want {
				t.Errorf("ProtocolFromLoadBalancer(%q) = %v, want %v", tt.protocol, got, tt.want)
			}
		})
	}
}

func TestProtocolFromServicePort(t *testing.T) {
	tests := []struct {
		name        string
		port        corev1.ServicePort
		annotations map[string]string
		want        LoadBalancerProtocol
	}{
		{
			name: "TCP port without proxy annotation",
			port: corev1.ServicePort{
				Protocol: corev1.ProtocolTCP,
				Port:     80,
			},
			annotations: nil,
			want:        LoadBalancerProtocolTCP,
		},
		{
			name: "TCP port with proxy annotation true",
			port: corev1.ServicePort{
				Protocol: corev1.ProtocolTCP,
				Port:     80,
			},
			annotations: map[string]string{
				ServiceAnnotationLoadBalancerProxyProtocol: "true",
			},
			want: LoadBalancerProtocolTCPProxy,
		},
		{
			name: "TCP port with proxy annotation false",
			port: corev1.ServicePort{
				Protocol: corev1.ProtocolTCP,
				Port:     80,
			},
			annotations: map[string]string{
				ServiceAnnotationLoadBalancerProxyProtocol: "false",
			},
			want: LoadBalancerProtocolTCP,
		},
		{
			name: "UDP port",
			port: corev1.ServicePort{
				Protocol: corev1.ProtocolUDP,
				Port:     53,
			},
			annotations: nil,
			want:        LoadBalancerProtocolUDP,
		},
		{
			name: "UDP port ignores proxy annotation",
			port: corev1.ServicePort{
				Protocol: corev1.ProtocolUDP,
				Port:     53,
			},
			annotations: map[string]string{
				ServiceAnnotationLoadBalancerProxyProtocol: "true",
			},
			want: LoadBalancerProtocolUDP,
		},
		{
			name: "SCTP port returns invalid",
			port: corev1.ServicePort{
				Protocol: corev1.ProtocolSCTP,
				Port:     80,
			},
			annotations: nil,
			want:        LoadBalancerProtocolInvalid,
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
			if got := ProtocolFromServicePort(tt.port, service); got != tt.want {
				t.Errorf("ProtocolFromServicePort() = %v, want %v", got, tt.want)
			}
		})
	}
}
