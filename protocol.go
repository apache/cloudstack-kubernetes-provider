package cloudstack

import (
	"k8s.io/api/core/v1"
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

// ServiceAnnotationLoadBalancerProxyProtocol is the annotation used on the
// service to enable the proxy protocol on a CloudStack load balancer.
// The value of this annotation is ignored, even if it is seemingly boolean.
// Simple presence of the annotation will enable it.
// Note that this protocol only applies to TCP service ports and
// CloudStack 4.6 is required for it to work.
const ServiceAnnotationLoadBalancerProxyProtocol = "service.beta.kubernetes.io/cloudstack-load-balancer-proxy-protocol"

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
//   v1.ProtocolTCP="tcp" -> "tcp"
//   v1.ProtocolTCP="udp" -> "udp" (CloudStack 4.6 and later)
//   v1.ProtocolTCP="tcp" + annotation "service.beta.kubernetes.io/cloudstack-load-balancer-proxy-protocol"
//                        -> "tcp-proxy" (CloudStack 4.6 and later)
//
// Other values return LoadBalancerProtocolInvalid.
func ProtocolFromServicePort(port v1.ServicePort, annotations map[string]string) LoadBalancerProtocol {
	proxy := false
	// FIXME this accepts any value as true, even "false", 0 or other falsey stuff
	if _, ok := annotations[ServiceAnnotationLoadBalancerProxyProtocol]; ok {
		proxy = true
	}
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
