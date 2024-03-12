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
	v1 "k8s.io/api/core/v1"
)

// LoadBalancerProtocol represents a specific network protocol supported by the CloudStack load balancer.
//
// It also allows easy mapping to standard protocol names.
type LoadBalancerProtocol int

const (
	LoadBalancerProtocolTCP LoadBalancerProtocol = iota
	LoadBalancerProtocolUDP
	LoadBalancerProtocolTCPProxy
	LoadBalancerProtocolInvalid
)

// String returns the same value as CSProtocol.
func (p LoadBalancerProtocol) String() string {
	return p.CSProtocol()
}

// CSProtocol returns the full CloudStack protocol name.
// Returns "" if the value is unknown.
func (p LoadBalancerProtocol) CSProtocol() string {
	switch p {
	case LoadBalancerProtocolTCP:
		return "tcp"
	case LoadBalancerProtocolUDP:
		return "udp"
	case LoadBalancerProtocolTCPProxy:
		return "tcp-proxy"
	default:
		return ""
	}
}

// IPProtocol returns the standard IP protocol name.
// Returns "" if the value is unknown.
func (p LoadBalancerProtocol) IPProtocol() string {
	switch p {
	case LoadBalancerProtocolTCP:
		fallthrough
	case LoadBalancerProtocolTCPProxy:
		return "tcp"
	case LoadBalancerProtocolUDP:
		return "udp"
	default:
		return ""
	}
}

// ProtocolFromServicePort selects a suitable CloudStack protocol type
// based on a ServicePort object and annotations from a LoadBalancer definition.
//
// Supported combinations include:
//
//	v1.ProtocolTCP="tcp" -> "tcp"
//	v1.ProtocolTCP="udp" -> "udp" (CloudStack 4.6 and later)
//	v1.ProtocolTCP="tcp" + annotation "service.beta.kubernetes.io/cloudstack-load-balancer-proxy-protocol"
//	                     -> "tcp-proxy" (CloudStack 4.6 and later)
//
// Other values return LoadBalancerProtocolInvalid.
func ProtocolFromServicePort(port v1.ServicePort, service *v1.Service) LoadBalancerProtocol {
	proxy := getBoolFromServiceAnnotation(service, ServiceAnnotationLoadBalancerProxyProtocol, false)
	switch port.Protocol {
	case v1.ProtocolTCP:
		if proxy {
			return LoadBalancerProtocolTCPProxy
		} else {
			return LoadBalancerProtocolTCP
		}
	case v1.ProtocolUDP:
		return LoadBalancerProtocolUDP
	default:
		return LoadBalancerProtocolInvalid
	}
}

// ProtocolFromLoadBalancer returns the protocol corresponding to the
// CloudStack load balancer protocol name.
func ProtocolFromLoadBalancer(protocol string) LoadBalancerProtocol {
	switch protocol {
	case "tcp":
		return LoadBalancerProtocolTCP
	case "udp":
		return LoadBalancerProtocolUDP
	case "tcp-proxy":
		return LoadBalancerProtocolTCPProxy
	default:
		return LoadBalancerProtocolInvalid
	}
}
