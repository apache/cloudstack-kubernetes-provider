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
	"net"
	"sort"
	"strconv"
	"strings"

	"github.com/apache/cloudstack-go/v2/cloudstack"
	"github.com/blang/semver/v4"
	"k8s.io/klog/v2"

	corev1 "k8s.io/api/core/v1"
	cloudprovider "k8s.io/cloud-provider"
)

const (
	// defaultAllowedCIDR is the network range that is allowed on the firewall
	// by default when no explicit CIDR list is given on a LoadBalancer.
	defaultAllowedCIDR = "0.0.0.0/0"

	// ServiceAnnotationLoadBalancerProxyProtocol is the annotation used on the
	// service to enable the proxy protocol on a CloudStack load balancer.
	// Note that this protocol only applies to TCP service ports and
	// CloudStack >= 4.6 is required for it to work.
	ServiceAnnotationLoadBalancerProxyProtocol        = "service.beta.kubernetes.io/cloudstack-load-balancer-proxy-protocol"
	ServiceAnnotationLoadBalancerLoadbalancerHostname = "service.beta.kubernetes.io/cloudstack-load-balancer-hostname"

	// ServiceAnnotationLoadBalancerSourceCidrs is the annotation used on the
	// service to specify the source CIDR list for a CloudStack load balancer.
	// The CIDR list is a comma-separated list of CIDR ranges (e.g., "10.0.0.0/8,192.168.1.0/24").
	// If not specified, the default is to allow all sources ("0.0.0.0/0").
	ServiceAnnotationLoadBalancerSourceCidrs = "service.beta.kubernetes.io/cloudstack-load-balancer-source-cidrs"

	// ServiceAnnotationLoadBalancerIPAssociatedByController indicates that the controller
	// associated the IP address. This annotation is set by the controller when it associates
	// an unallocated IP, and is used to determine if the IP should be disassociated on deletion.
	ServiceAnnotationLoadBalancerIPAssociatedByController = "service.beta.kubernetes.io/cloudstack-load-balancer-ip-associated-by-controller" //nolint:gosec
)

// cidrListUpdateVersion is the first CloudStack release whose updateLoadBalancerRule API
// accepts a cidrlist. Below it, a changed source CIDR list can only be applied by deleting
// the rule and creating it again.
var cidrListUpdateVersion = semver.Version{Major: 4, Minor: 22, Patch: 0}

type loadBalancer struct {
	*cloudstack.CloudStackClient

	name                     string
	algorithm                string
	hostIDs                  []string
	ipAddr                   string
	ipAddrID                 string
	networkID                string
	projectID                string
	rules                    map[string]*cloudstack.LoadBalancerRule
	ipAssociatedByController bool
}

// desiredLBRule describes the load balancer rule a service port should be represented by,
// together with the existing CloudStack rule it resolved to (if any).
type desiredLBRule struct {
	name     string
	port     corev1.ServicePort
	protocol LoadBalancerProtocol
	existing *cloudstack.LoadBalancerRule // nil means the rule must be created
	update   bool                         // the existing rule needs an update call
}

// GetLoadBalancer returns whether the specified load balancer exists, and if so, what its status is.
func (cs *CSCloud) GetLoadBalancer(ctx context.Context, clusterName string, service *corev1.Service) (*corev1.LoadBalancerStatus, bool, error) {
	klog.V(4).Infof("GetLoadBalancer(%v, %v, %v)", clusterName, service.Namespace, service.Name)

	// Get the load balancer details and existing rules.
	lb, err := cs.getLoadBalancer(service)
	if err != nil {
		return nil, false, err
	}

	// If we don't have any rules, the load balancer does not exist.
	if len(lb.rules) == 0 {
		return nil, false, nil
	}

	klog.V(4).Infof("Found a load balancer associated with IP %v", lb.ipAddr)

	status := &corev1.LoadBalancerStatus{}
	status.Ingress = append(status.Ingress, corev1.LoadBalancerIngress{IP: lb.ipAddr})

	return status, true, nil
}

// EnsureLoadBalancer creates a new load balancer, or updates the existing one. Returns the status of the balancer.
func (cs *CSCloud) EnsureLoadBalancer(ctx context.Context, clusterName string, service *corev1.Service, nodes []*corev1.Node) (status *corev1.LoadBalancerStatus, err error) {
	klog.V(4).Infof("EnsureLoadBalancer(%v, %v, %v, %v, %v, %v)", clusterName, service.Namespace, service.Name, service.Spec.LoadBalancerIP, service.Spec.Ports, nodes)

	if len(service.Spec.Ports) == 0 {
		return nil, fmt.Errorf("requested load balancer with no ports")
	}

	// Get the load balancer details and existing rules.
	lb, err := cs.getLoadBalancer(service)
	if err != nil {
		return nil, err
	}

	// Set the load balancer algorithm.
	switch service.Spec.SessionAffinity {
	case corev1.ServiceAffinityNone:
		lb.algorithm = "roundrobin"
	case corev1.ServiceAffinityClientIP:
		lb.algorithm = "source"
	default:
		return nil, fmt.Errorf("unsupported load balancer affinity: %v", service.Spec.SessionAffinity)
	}

	// Verify that all the hosts belong to the same network, and retrieve their ID's.
	lb.hostIDs, lb.networkID, err = cs.verifyHosts(nodes)
	if err != nil {
		return nil, err
	}

	if !lb.hasLoadBalancerIP() {
		// Create or retrieve the load balancer IP.
		if err := lb.getLoadBalancerIP(service.Spec.LoadBalancerIP); err != nil {
			return nil, err
		}

		if lb.ipAddr != "" && lb.ipAddr != service.Spec.LoadBalancerIP {
			defer func(lb *loadBalancer) {
				if err != nil {
					if err := lb.releaseLoadBalancerIP(); err != nil {
						klog.Errorf(err.Error())
					}
				}
			}(lb)
		}

		// If the controller associated the IP and matches the service spec, set the annotation to persist this information.
		if lb.ipAssociatedByController && lb.ipAddr == service.Spec.LoadBalancerIP {
			if err := cs.setServiceAnnotation(ctx, service, ServiceAnnotationLoadBalancerIPAssociatedByController, "true"); err != nil {
				// Log the error but don't fail - the annotation is helpful but not critical
				klog.Warningf("Failed to set annotation on service %s/%s: %v", service.Namespace, service.Name, err)
			}
		}
	}

	klog.V(4).Infof("Load balancer %v is associated with IP %v", lb.name, lb.ipAddr)

	// Resolve every service port to the rule that should represent it.
	desired, err := lb.resolveLoadBalancerRules(service, cs.version)
	if err != nil {
		return nil, err
	}

	network, _, err := lb.Network.GetNetworkByID(lb.networkID, cloudstack.WithProject(lb.projectID))
	if err != nil {
		return nil, err
	}

	blocking, rest := lb.partitionObsoleteRules(desired)

	// Obsolete rules holding a public port that a new rule needs have to go first, or
	// CloudStack rejects the create as a port conflict.
	if err := lb.pruneRules(blocking, desired, network); err != nil {
		return nil, err
	}

	if err := lb.applyLoadBalancerRules(desired, service, network, cs.version); err != nil {
		return nil, err
	}

	// Everything else is removed only once the desired rules are in place, so a failure here
	// can never leave the service without the rules it does need.
	if err := lb.pruneRules(rest, desired, network); err != nil {
		return nil, err
	}

	status = &corev1.LoadBalancerStatus{}
	// If hostname is explicitly set using service annotation
	// Workaround for https://github.com/kubernetes/kubernetes/issues/66607
	if hostname := getStringFromServiceAnnotation(service, ServiceAnnotationLoadBalancerLoadbalancerHostname, ""); hostname != "" {
		status.Ingress = []corev1.LoadBalancerIngress{{Hostname: hostname}}
		return status, nil
	}
	// Default to IP
	status.Ingress = []corev1.LoadBalancerIngress{{IP: lb.ipAddr}}

	return status, nil
}

// UpdateLoadBalancer updates hosts under the specified load balancer.
func (cs *CSCloud) UpdateLoadBalancer(ctx context.Context, clusterName string, service *corev1.Service, nodes []*corev1.Node) error {
	klog.V(4).Infof("UpdateLoadBalancer(%v, %v, %v, %v)", clusterName, service.Namespace, service.Name, nodes)

	// Get the load balancer details and existing rules.
	lb, err := cs.getLoadBalancer(service)
	if err != nil {
		return err
	}

	// Verify that all the hosts belong to the same network, and retrieve their ID's.
	lb.hostIDs, _, err = cs.verifyHosts(nodes)
	if err != nil {
		return err
	}

	for _, lbRule := range lb.rules {
		p := lb.LoadBalancer.NewListLoadBalancerRuleInstancesParams(lbRule.Id)

		// Retrieve all VMs currently associated to this load balancer rule.
		l, err := lb.LoadBalancer.ListLoadBalancerRuleInstances(p)
		if err != nil {
			return fmt.Errorf("error retrieving associated instances: %v", err)
		}

		assign, remove := symmetricDifference(lb.hostIDs, l.LoadBalancerRuleInstances)

		if len(assign) > 0 {
			klog.V(4).Infof("Assigning new hosts (%v) to load balancer rule: %v", assign, lbRule.Name)
			if err := lb.assignHostsToRule(lbRule, assign); err != nil {
				return err
			}
		}

		if len(remove) > 0 {
			klog.V(4).Infof("Removing old hosts (%v) from load balancer rule: %v", assign, lbRule.Name)
			if err := lb.removeHostsFromRule(lbRule, remove); err != nil {
				return err
			}
		}
	}

	return nil
}

func isFirewallSupported(services []cloudstack.NetworkServiceInternal) bool {
	for _, svc := range services {
		if svc.Name == "Firewall" {
			return true
		}
	}
	return false
}

func isNetworkACLSupported(services []cloudstack.NetworkServiceInternal) bool {
	for _, svc := range services {
		if svc.Name == "NetworkACL" {
			return true
		}
	}
	return false
}

// EnsureLoadBalancerDeleted deletes the specified load balancer if it exists, returning
// nil if the load balancer specified either didn't exist or was successfully deleted.
func (cs *CSCloud) EnsureLoadBalancerDeleted(ctx context.Context, clusterName string, service *corev1.Service) error {
	klog.V(4).Infof("EnsureLoadBalancerDeleted(%v, %v, %v)", clusterName, service.Namespace, service.Name)

	// Get the load balancer details and existing rules.
	lb, err := cs.getLoadBalancer(service)
	if err != nil {
		return err
	}

	for _, lbRule := range lb.rules {
		klog.V(4).Infof("Deleting firewall rules / Network ACLs for load balancer: %v", lbRule.Name)
		protocol := ProtocolFromLoadBalancer(lbRule.Protocol)
		if protocol == LoadBalancerProtocolInvalid {
			klog.Errorf("Error parsing protocol: %v", lbRule.Protocol)
		} else {
			port, err := strconv.ParseInt(lbRule.Publicport, 10, 32)
			if err != nil {
				klog.Errorf("Error parsing port: %v", err)
			} else {
				networkId, err := cs.getNetworkIDFromIPAddress(lb.ipAddrID)
				if err != nil {
					return err
				}
				network, count, err := lb.Network.GetNetworkByID(networkId, cloudstack.WithProject(lb.projectID))
				if err != nil {
					if count == 0 {
						klog.Errorf("No network found with ID: %v", networkId)
						return err
					}
					return err
				}
				if network.Vpcid == "" {
					_, err = lb.deleteFirewallRule(lbRule.Publicipid, int(port), protocol)
					if err != nil {
						klog.Errorf("Error deleting firewall rule: %v", err)
					}
				} else {
					klog.V(4).Infof("Deleting network ACLs for %v - %v", int(port), protocol)
					_, err = lb.deleteNetworkACLRule(int(port), protocol, networkId)
					if err != nil {
						klog.Errorf("Error deleting Network ACL rule: %v", err)
					}
				}
			}

			klog.V(4).Infof("Deleting load balancer rule: %v", lbRule.Name)
			if err := lb.deleteLoadBalancerRule(lbRule); err != nil {
				return err
			}
		}
	}

	if lb.ipAddr != "" {
		// If the IP was allocated by the controller (not specified in service spec), release it.
		if lb.ipAddr != service.Spec.LoadBalancerIP {
			klog.V(4).Infof("Releasing load balancer IP: %v", lb.ipAddr)
			if err := lb.releaseLoadBalancerIP(); err != nil {
				return err
			}
		} else {
			// If the IP was specified in service spec, check if it was associated by the controller.
			// First, check if there's an annotation indicating the controller associated it.
			// If not, check if there are any other load balancer rules using this IP.
			shouldDisassociate := getBoolFromServiceAnnotation(service, ServiceAnnotationLoadBalancerIPAssociatedByController, false)

			if shouldDisassociate {
				// Annotation is set, so check if there are any other load balancer rules using this IP.
				// Since we've already deleted all rules for this service, any remaining rules must belong
				// to other services. If no other rules exist, it's safe to disassociate the IP.
				ip, count, err := lb.Address.GetPublicIpAddressByID(lb.ipAddrID)
				if err != nil {
					klog.Errorf("Error retrieving IP address %v for disassociation check: %v", lb.ipAddr, err)
					shouldDisassociate = false
				} else if count > 0 && ip.Allocated != "" {
					p := lb.LoadBalancer.NewListLoadBalancerRulesParams()
					p.SetPublicipid(lb.ipAddrID)
					p.SetListall(true)
					if lb.projectID != "" {
						p.SetProjectid(lb.projectID)
					}
					otherRules, err := lb.LoadBalancer.ListLoadBalancerRules(p)
					if err != nil {
						klog.Errorf("Error checking for other load balancer rules using IP %v: %v", lb.ipAddr, err)
						shouldDisassociate = false
					} else if otherRules.Count > 0 {
						// Other load balancer rules are using this IP (other services are using it),
						// so don't disassociate.
						shouldDisassociate = false
					}
				}
			}

			if shouldDisassociate {
				klog.V(4).Infof("Disassociating IP %v that was associated by the controller", lb.ipAddr)
				if err := lb.releaseLoadBalancerIP(); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// GetLoadBalancerName retrieves the name of the LoadBalancer.
func (cs *CSCloud) GetLoadBalancerName(ctx context.Context, clusterName string, service *corev1.Service) string {
	return cloudprovider.DefaultLoadBalancerName(service)
}

// getLoadBalancer retrieves the IP address and ID and all the existing rules it can find.
func (cs *CSCloud) getLoadBalancer(service *corev1.Service) (*loadBalancer, error) {
	lb := &loadBalancer{
		CloudStackClient: cs.client,
		name:             cs.GetLoadBalancerName(context.TODO(), "", service),
		projectID:        cs.projectID,
		rules:            make(map[string]*cloudstack.LoadBalancerRule),
	}

	p := cs.client.LoadBalancer.NewListLoadBalancerRulesParams()
	p.SetKeyword(lb.name)
	p.SetListall(true)

	if cs.projectID != "" {
		p.SetProjectid(cs.projectID)
	}

	l, err := cs.client.LoadBalancer.ListLoadBalancerRules(p)
	if err != nil {
		return nil, fmt.Errorf("error retrieving load balancer rules: %v", err)
	}

	for _, lbRule := range l.LoadBalancerRules {
		lb.rules[lbRule.Name] = lbRule

		if lb.ipAddr != "" && lb.ipAddr != lbRule.Publicip {
			klog.Warningf("Load balancer for service %v/%v has rules associated with different IP's: %v, %v", service.Namespace, service.Name, lb.ipAddr, lbRule.Publicip)
		}

		lb.ipAddr = lbRule.Publicip
		lb.ipAddrID = lbRule.Publicipid
	}

	klog.V(4).Infof("Load balancer %v contains %d rule(s)", lb.name, len(lb.rules))

	return lb, nil
}

// Get network ID from Public IP Address
func (cs *CSCloud) getNetworkIDFromIPAddress(publicIpId string) (string, error) {
	ip, count, err := cs.client.Address.GetPublicIpAddressByID(publicIpId)
	if err != nil {
		klog.Errorf("Failed to fetch the public IP for id: %v", publicIpId)
		return "", err
	}
	if count == 0 {
		return "", err
	}
	if ip.Networkid != "" {
		network, _, netErr := cs.client.Network.GetNetworkByID(ip.Associatednetworkid)
		if netErr != nil {
			klog.Errorf("Failed to fetch the network for id: %v", ip.Associatednetworkid)
			return "", err
		}
		return network.Id, nil
	}
	return "", nil
}

// verifyHosts verifies if all hosts belong to the same network, and returns the host ID's and network ID.
func (cs *CSCloud) verifyHosts(nodes []*corev1.Node) ([]string, string, error) {
	hostNames := map[string]bool{}
	for _, node := range nodes {
		// node.Name can be an FQDN as well, and CloudStack VM names aren't
		// To match, we need to Split the domain part off here, if present
		hostNames[strings.Split(strings.ToLower(node.Name), ".")[0]] = true
	}

	p := cs.client.VirtualMachine.NewListVirtualMachinesParams()
	p.SetListall(true)
	p.SetDetails([]string{"min", "nics"})

	if cs.projectID != "" {
		p.SetProjectid(cs.projectID)
	}

	l, err := cs.client.VirtualMachine.ListVirtualMachines(p)
	if err != nil {
		return nil, "", fmt.Errorf("error retrieving list of hosts: %v", err)
	}

	var hostIDs []string
	var networkID string

	// Check if the virtual machine is in the hosts slice, then add the corresponding ID.
	for _, vm := range l.VirtualMachines {
		if hostNames[strings.ToLower(vm.Name)] {
			if networkID != "" && networkID != vm.Nic[0].Networkid {
				return nil, "", fmt.Errorf("found hosts that belong to different networks")
			}

			networkID = vm.Nic[0].Networkid
			hostIDs = append(hostIDs, vm.Id)
		}
	}

	if len(hostIDs) == 0 || len(networkID) == 0 {
		return nil, "", fmt.Errorf("none of the hosts matched the list of VMs retrieved from CS API")
	}

	return hostIDs, networkID, nil
}

// hasLoadBalancerIP returns true if we have a load balancer address and ID.
func (lb *loadBalancer) hasLoadBalancerIP() bool {
	return lb.ipAddr != "" && lb.ipAddrID != ""
}

// getLoadBalancerIP retrieves an existing IP or associates a new IP.
func (lb *loadBalancer) getLoadBalancerIP(loadBalancerIP string) error {
	if loadBalancerIP != "" {
		return lb.getPublicIPAddress(loadBalancerIP)
	}

	return lb.associatePublicIPAddress()
}

// getPublicIPAddressID retrieves the ID of the given IP, and sets the address and it's ID.
func (lb *loadBalancer) getPublicIPAddress(loadBalancerIP string) error {
	klog.V(4).Infof("Retrieve load balancer IP details: %v", loadBalancerIP)

	p := lb.Address.NewListPublicIpAddressesParams()
	p.SetIpaddress(loadBalancerIP)
	p.SetAllocatedonly(false)
	p.SetListall(true)

	if lb.projectID != "" {
		p.SetProjectid(lb.projectID)
	}

	l, err := lb.Address.ListPublicIpAddresses(p)
	if err != nil {
		return fmt.Errorf("error retrieving IP address: %v", err)
	}

	if l.Count != 1 {
		return fmt.Errorf("could not find IP address %v. Found %d addresses", loadBalancerIP, l.Count)
	}

	lb.ipAddr = l.PublicIpAddresses[0].Ipaddress
	lb.ipAddrID = l.PublicIpAddresses[0].Id

	// If the IP is not allocated, associate it.
	if l.PublicIpAddresses[0].Allocated == "" {
		return lb.associatePublicIPAddress()
	}
	return nil
}

// associatePublicIPAddress associates a new IP and sets the address and it's ID.
func (lb *loadBalancer) associatePublicIPAddress() error {
	klog.V(4).Infof("Allocate new IP for load balancer: %v", lb.name)
	// If a network belongs to a VPC, the IP address needs to be associated with
	// the VPC instead of with the network.
	network, count, err := lb.Network.GetNetworkByID(lb.networkID, cloudstack.WithProject(lb.projectID))
	if err != nil {
		if count == 0 {
			return fmt.Errorf("could not find network %v", lb.networkID)
		}
		return fmt.Errorf("error retrieving network: %v", err)
	}

	p := lb.Address.NewAssociateIpAddressParams()

	if network.Vpcid != "" {
		p.SetVpcid(network.Vpcid)
	} else {
		p.SetNetworkid(lb.networkID)
	}

	if lb.projectID != "" {
		p.SetProjectid(lb.projectID)
	}

	if lb.ipAddr != "" {
		p.SetIpaddress(lb.ipAddr)
	}

	// Associate a new IP address
	r, err := lb.Address.AssociateIpAddress(p)
	if err != nil {
		return fmt.Errorf("error associating new IP address: %v", err)
	}

	lb.ipAddr = r.Ipaddress
	lb.ipAddrID = r.Id
	lb.ipAssociatedByController = true

	return nil
}

// releasePublicIPAddress releases an associated IP.
func (lb *loadBalancer) releaseLoadBalancerIP() error {
	p := lb.Address.NewDisassociateIpAddressParams(lb.ipAddrID)

	if _, err := lb.Address.DisassociateIpAddress(p); err != nil {
		return fmt.Errorf("error releasing load balancer IP %v: %v", lb.ipAddr, err)
	}

	return nil
}

func (lb *loadBalancer) getCIDRList(service *corev1.Service) ([]string, error) {
	sourceCIDRs := getStringFromServiceAnnotation(service, ServiceAnnotationLoadBalancerSourceCidrs, defaultAllowedCIDR)
	var cidrList []string
	if sourceCIDRs != "" {
		cidrList = strings.Split(sourceCIDRs, ",")
		for i, cidr := range cidrList {
			cidr = strings.TrimSpace(cidr)
			if _, _, err := net.ParseCIDR(cidr); err != nil {
				return nil, fmt.Errorf("invalid CIDR %s in annotation %s: %w", cidr, ServiceAnnotationLoadBalancerSourceCidrs, err)
			}
			cidrList[i] = cidr
		}
	}
	return cidrList, nil
}

// splitCIDRList splits the CIDR list of an existing CloudStack rule into its entries.
// CloudStack has reported these both comma and space separated, and a CIDR can contain
// neither character, so treat both as separators.
func splitCIDRList(cidrList string) []string {
	return strings.FieldsFunc(cidrList, func(r rune) bool {
		return r == ',' || r == ' '
	})
}

// resolveLoadBalancerRules maps every service port to the load balancer rule that should
// represent it, claiming each match as it goes so that what remains in lb.rules is exactly
// the obsolete set and no rule can be claimed twice.
func (lb *loadBalancer) resolveLoadBalancerRules(service *corev1.Service, version semver.Version) ([]desiredLBRule, error) {
	desired := make([]desiredLBRule, 0, len(service.Spec.Ports))

	for _, port := range service.Spec.Ports {
		// Construct the protocol name first, we need it a few times
		protocol := ProtocolFromServicePort(port, service)
		if protocol == LoadBalancerProtocolInvalid {
			return nil, fmt.Errorf("unsupported load balancer protocol: %v", port.Protocol)
		}

		// All ports have their own load balancer rule, so add the port to lbName to keep the names unique.
		lbRuleName := fmt.Sprintf("%s-%s-%d", lb.name, protocol, port.Port)

		lbRule, needsUpdate, err := lb.checkLoadBalancerRule(lb.findLoadBalancerRule(lbRuleName, port, protocol), lbRuleName, port, protocol, service, version)
		if err != nil {
			return nil, err
		}

		if lbRule != nil {
			// Claim by the rule's actual name: after a protocol change it still carries the old one.
			delete(lb.rules, lbRule.Name)
		}

		desired = append(desired, desiredLBRule{
			name:     lbRuleName,
			port:     port,
			protocol: protocol,
			existing: lbRule,
			update:   needsUpdate,
		})
	}

	return desired, nil
}

// findLoadBalancerRule locates the existing CloudStack rule for a desired service port. It
// prefers an exact name match, then falls back to matching on the tuple. That fallback is what
// lets a protocol change (tcp <-> tcp-proxy) update the existing rule instead of creating a
// conflicting one.
//
// Only rules on the IP being reconciled towards are eligible; a rule on any other IP is left
// for the prune pass, which also cleans up the firewall rules it leaves behind.
func (lb *loadBalancer) findLoadBalancerRule(lbRuleName string, port corev1.ServicePort, protocol LoadBalancerProtocol) *cloudstack.LoadBalancerRule {
	if lbRule, ok := lb.rules[lbRuleName]; ok && lbRule.Publicipid == lb.ipAddrID {
		return lbRule
	}

	publicPort := strconv.Itoa(int(port.Port))
	var names []string
	for name, lbRule := range lb.rules {
		if lbRule.Publicipid == lb.ipAddrID &&
			ProtocolFromLoadBalancer(lbRule.Protocol).IPProtocol() == protocol.IPProtocol() &&
			lbRule.Publicport == publicPort {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil
	}

	// Map iteration order is randomized; sort so the pick is deterministic.
	sort.Strings(names)
	if len(names) > 1 {
		klog.Warningf("Multiple load balancer rules match %s port %s: %v; using %v", protocol.IPProtocol(), publicPort, names, names[0])
	}
	return lb.rules[names[0]]
}

// portProtocol is the tuple CloudStack refuses to place two load balancer rules on, and that
// firewall and network ACL rules are keyed on. IPProtocol maps both tcp and tcp-proxy to
// "tcp", so a tcp and a tcp-proxy rule on one port share a tuple, and one firewall/ACL rule.
type portProtocol struct {
	ipProtocol string
	publicPort int32
}

// obsoleteRule is a rule no desired service port claimed, with its tuple already parsed.
type obsoleteRule struct {
	rule     *cloudstack.LoadBalancerRule
	protocol LoadBalancerProtocol
	tuple    portProtocol
}

// partitionObsoleteRules splits the rules left in lb.rules — those no desired port claimed —
// into the ones holding a tuple that a rule still to be created needs, and the rest.
func (lb *loadBalancer) partitionObsoleteRules(desired []desiredLBRule) (blocking, rest []obsoleteRule) {
	// CloudStack refuses two load balancer rules with overlapping public port ranges on one
	// IP whatever their protocols, so the port alone decides what blocks a create. Note this
	// is deliberately coarser than the firewall/ACL claim, which is per protocol because
	// firewall rules are.
	neededPorts := make(map[int32]bool)
	for _, d := range desired {
		if d.existing == nil {
			neededPorts[d.port.Port] = true
		}
	}

	// Iterate in name order so the prune sequence is reproducible.
	names := make([]string, 0, len(lb.rules))
	for name := range lb.rules {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		lbRule := lb.rules[name]

		protocol := ProtocolFromLoadBalancer(lbRule.Protocol)
		if protocol == LoadBalancerProtocolInvalid {
			klog.Errorf("Skipping obsolete load balancer rule %v with unknown protocol %v", lbRule.Name, lbRule.Protocol)
			continue
		}
		port, err := strconv.ParseInt(lbRule.Publicport, 10, 32)
		if err != nil {
			klog.Errorf("Skipping obsolete load balancer rule %v with invalid public port %v: %v", lbRule.Name, lbRule.Publicport, err)
			continue
		}

		obsolete := obsoleteRule{
			rule:     lbRule,
			protocol: protocol,
			tuple:    portProtocol{protocol.IPProtocol(), int32(port)},
		}

		// Conflicts are per public IP, so only a rule on the IP being reconciled towards can
		// block a create.
		if lbRule.Publicipid == lb.ipAddrID && neededPorts[obsolete.tuple.publicPort] {
			blocking = append(blocking, obsolete)
		} else {
			rest = append(rest, obsolete)
		}
	}

	return blocking, rest
}

// pruneRules deletes the given obsolete rules along with their firewall or network ACL rules.
// A firewall/ACL rule is kept when a desired port still claims the same tuple, since the two
// load balancer rules share it and pruning would strip the survivor of its opening.
//
// A rule that fails to delete is reported but does not stop the others being pruned.
func (lb *loadBalancer) pruneRules(obsolete []obsoleteRule, desired []desiredLBRule, network *cloudstack.Network) error {
	// Tuples the service still needs, and whose firewall/ACL rules therefore have to survive.
	claimed := make(map[portProtocol]bool, len(desired))
	for _, d := range desired {
		claimed[portProtocol{d.protocol.IPProtocol(), d.port.Port}] = true
	}

	var firstErr error
	recordErr := func(err error) {
		klog.Errorf("Error pruning obsolete load balancer rule: %v", err)
		if firstErr == nil {
			firstErr = err
		}
	}

	for _, o := range obsolete {
		lbRule, protocol, port := o.rule, o.protocol, o.tuple.publicPort

		if isFirewallSupported(network.Service) {
			// Firewall rules belong to a single public IP, so a claim only covers a rule on
			// the IP the service is being reconciled towards.
			if claimed[o.tuple] && lbRule.Publicipid == lb.ipAddrID {
				klog.V(4).Infof("Keeping firewall rules of obsolete load balancer rule %v (%v:%v:%v): still claimed by a service port", lbRule.Name, protocol, lbRule.Publicip, port)
			} else {
				klog.V(4).Infof("Deleting firewall rules associated with load balancer rule: %v (%v:%v:%v)", lbRule.Name, protocol, lbRule.Publicip, port)
				if _, err := lb.deleteFirewallRule(lbRule.Publicipid, int(port), protocol); err != nil {
					recordErr(err)
					continue
				}
			}
		} else if isNetworkACLSupported(network.Service) {
			// ACL rules belong to the network rather than an IP, so the claim always applies.
			if claimed[o.tuple] {
				klog.V(4).Infof("Keeping Network ACL rules of obsolete load balancer rule %v (%v:%v): still claimed by a service port", lbRule.Name, protocol, port)
			} else {
				klog.V(4).Infof("Deleting Network ACL rules associated with load balancer rule: %v (%v:%v)", lbRule.Name, protocol, port)
				if _, err := lb.deleteNetworkACLRule(int(port), protocol, lb.networkID); err != nil {
					recordErr(err)
					continue
				}
			}
		}

		klog.V(4).Infof("Deleting obsolete load balancer rule: %v", lbRule.Name)
		if err := lb.deleteLoadBalancerRule(lbRule); err != nil {
			recordErr(err)
		}
	}

	return firstErr
}

// applyLoadBalancerRules creates or updates the load balancer rule of every desired service
// port and reconciles the firewall or network ACL rules it needs.
func (lb *loadBalancer) applyLoadBalancerRules(desired []desiredLBRule, service *corev1.Service, network *cloudstack.Network, version semver.Version) error {
	for _, d := range desired {
		lbRule := d.existing

		if lbRule != nil {
			if d.update {
				klog.V(4).Infof("Updating load balancer rule: %v", d.name)
				if err := lb.updateLoadBalancerRule(lbRule, d.name, d.protocol, service, version); err != nil {
					return err
				}
			} else {
				klog.V(4).Infof("Load balancer rule %v is up-to-date", d.name)
			}
		} else {
			klog.V(4).Infof("Creating load balancer rule: %v", d.name)
			newRule, err := lb.createLoadBalancerRule(d.name, d.port, d.protocol, service)
			if err != nil {
				return err
			}
			lbRule = newRule

			klog.V(4).Infof("Assigning hosts (%v) to load balancer rule: %v", lb.hostIDs, d.name)
			if err := lb.assignHostsToRule(lbRule, lb.hostIDs); err != nil {
				return err
			}
		}

		if isFirewallSupported(network.Service) {
			klog.V(4).Infof("Creating firewall rules for load balancer rule: %v (%v:%v:%v)", d.name, d.protocol, lbRule.Publicip, d.port.Port)
			if _, err := lb.updateFirewallRule(lbRule.Publicipid, int(d.port.Port), d.protocol, service.Spec.LoadBalancerSourceRanges); err != nil {
				return err
			}
		} else if isNetworkACLSupported(network.Service) {
			klog.V(4).Infof("Creating ACL rules for load balancer rule: %v (%v:%v:%v)", d.name, d.protocol, lbRule.Publicip, d.port.Port)
			if _, err := lb.updateNetworkACL(int(d.port.Port), d.protocol, network.Id); err != nil {
				return err
			}
		}
	}

	return nil
}

// checkLoadBalancerRule checks if the given existing rule (nil if none was found) is up to
// date, can be brought up to date with an update call, or must be recreated. If it must be
// recreated, the existing rule is deleted so it can be created again.
func (lb *loadBalancer) checkLoadBalancerRule(lbRule *cloudstack.LoadBalancerRule, lbRuleName string, port corev1.ServicePort, protocol LoadBalancerProtocol, service *corev1.Service, version semver.Version) (*cloudstack.LoadBalancerRule, bool, error) {
	if lbRule == nil {
		return nil, false, nil
	}

	cidrList, err := lb.getCIDRList(service)
	if err != nil {
		return nil, false, err
	}

	lbRuleCidrList := splitCIDRList(lbRule.Cidrlist)

	// Check if basic properties match (IP and ports). If not, we need to recreate the rule.
	basicPropsMatch := lbRule.Publicip == lb.ipAddr &&
		lbRule.Privateport == strconv.Itoa(int(port.NodePort)) &&
		lbRule.Publicport == strconv.Itoa(int(port.Port))

	cidrListChanged := len(cidrList) != len(lbRuleCidrList) || !compareStringSlice(cidrList, lbRuleCidrList)
	updateProto := lbRule.Protocol != protocol.CSProtocol()

	// A CIDR change on an older CloudStack can only be applied by recreating the rule.
	if !basicPropsMatch || (cidrListChanged && version.LT(cidrListUpdateVersion)) {
		// Delete the load balancer rule so we can create a new one using the new values.
		if err := lb.deleteLoadBalancerRule(lbRule); err != nil {
			return nil, false, err
		}
		return nil, false, nil
	}

	// Rule can be updated. Check what needs updating.
	updateAlgo := lbRule.Algorithm != lb.algorithm
	// The name encodes the protocol, so a rule matched across a protocol change needs renaming.
	updateName := lbRule.Name != lbRuleName

	return lbRule, updateAlgo || updateProto || updateName || cidrListChanged, nil
}

// updateLoadBalancerRule updates a load balancer rule.
func (lb *loadBalancer) updateLoadBalancerRule(lbRule *cloudstack.LoadBalancerRule, lbRuleName string, protocol LoadBalancerProtocol, service *corev1.Service, version semver.Version) error {
	p := lb.LoadBalancer.NewUpdateLoadBalancerRuleParams(lbRule.Id)
	p.SetAlgorithm(lb.algorithm)
	p.SetProtocol(protocol.CSProtocol())
	p.SetName(lbRuleName)

	// Only send the CIDR list where the API accepts it; checkLoadBalancerRule recreates the
	// rule instead on older releases, so a change can never be silently dropped here.
	if version.GTE(cidrListUpdateVersion) {
		cidrList, err := lb.getCIDRList(service)
		if err != nil {
			return err
		}
		p.SetCidrlist(cidrList)
	}

	_, err := lb.LoadBalancer.UpdateLoadBalancerRule(p)
	return err
}

// createLoadBalancerRule creates a new load balancer rule and returns it's ID.
func (lb *loadBalancer) createLoadBalancerRule(lbRuleName string, port corev1.ServicePort, protocol LoadBalancerProtocol, service *corev1.Service) (*cloudstack.LoadBalancerRule, error) {
	p := lb.LoadBalancer.NewCreateLoadBalancerRuleParams(
		lb.algorithm,
		lbRuleName,
		int(port.NodePort),
		int(port.Port),
	)

	p.SetNetworkid(lb.networkID)
	p.SetPublicipid(lb.ipAddrID)
	p.SetProtocol(protocol.CSProtocol())

	// Do not open the firewall implicitly, we always create explicit firewall rules
	p.SetOpenfirewall(false)

	// Read the source CIDR annotation
	cidrList, err := lb.getCIDRList(service)
	if err != nil {
		return nil, err
	}

	// Set the CIDR list in the parameters
	p.SetCidrlist(cidrList)

	// Create a new load balancer rule.
	r, err := lb.LoadBalancer.CreateLoadBalancerRule(p)
	if err != nil {
		return nil, fmt.Errorf("error creating load balancer rule %v: %v", lbRuleName, err)
	}

	lbRule := &cloudstack.LoadBalancerRule{
		Id:          r.Id,
		Algorithm:   r.Algorithm,
		Cidrlist:    r.Cidrlist,
		Name:        r.Name,
		Networkid:   r.Networkid,
		Privateport: r.Privateport,
		Publicport:  r.Publicport,
		Publicip:    r.Publicip,
		Publicipid:  r.Publicipid,
		Protocol:    r.Protocol,
	}

	return lbRule, nil
}

// deleteLoadBalancerRule deletes a load balancer rule.
func (lb *loadBalancer) deleteLoadBalancerRule(lbRule *cloudstack.LoadBalancerRule) error {
	p := lb.LoadBalancer.NewDeleteLoadBalancerRuleParams(lbRule.Id)

	if _, err := lb.LoadBalancer.DeleteLoadBalancerRule(p); err != nil {
		return fmt.Errorf("error deleting load balancer rule %v: %v", lbRule.Name, err)
	}

	// Delete the rule from the map as it no longer exists
	delete(lb.rules, lbRule.Name)

	return nil
}

// assignHostsToRule assigns hosts to a load balancer rule.
func (lb *loadBalancer) assignHostsToRule(lbRule *cloudstack.LoadBalancerRule, hostIDs []string) error {
	p := lb.LoadBalancer.NewAssignToLoadBalancerRuleParams(lbRule.Id)
	p.SetVirtualmachineids(hostIDs)

	if _, err := lb.LoadBalancer.AssignToLoadBalancerRule(p); err != nil {
		return fmt.Errorf("error assigning hosts to load balancer rule %v: %v", lbRule.Name, err)
	}

	return nil
}

// removeHostsFromRule removes hosts from a load balancer rule.
func (lb *loadBalancer) removeHostsFromRule(lbRule *cloudstack.LoadBalancerRule, hostIDs []string) error {
	p := lb.LoadBalancer.NewRemoveFromLoadBalancerRuleParams(lbRule.Id)
	p.SetVirtualmachineids(hostIDs)

	if _, err := lb.LoadBalancer.RemoveFromLoadBalancerRule(p); err != nil {
		return fmt.Errorf("error removing hosts from load balancer rule %v: %v", lbRule.Name, err)
	}

	return nil
}

// symmetricDifference returns the symmetric difference between the old (existing) and new (wanted) host ID's.
func symmetricDifference(hostIDs []string, lbInstances []*cloudstack.VirtualMachine) ([]string, []string) {
	new := make(map[string]bool)
	for _, hostID := range hostIDs {
		new[hostID] = true
	}

	var remove []string
	for _, instance := range lbInstances {
		if new[instance.Id] {
			delete(new, instance.Id)
			continue
		}

		remove = append(remove, instance.Id)
	}

	var assign []string
	for hostID := range new {
		assign = append(assign, hostID)
	}

	return assign, remove
}

// compareStringSlice compares two unsorted slices of strings without sorting them first.
//
// The slices are equal if and only if both contain the same number of every unique element.
//
// Thanks to: https://stackoverflow.com/a/36000696
func compareStringSlice(x, y []string) bool {
	if len(x) != len(y) {
		return false
	}
	// create a map of string -> int
	diff := make(map[string]int, len(x))
	for _, _x := range x {
		// 0 value for int is 0, so just increment a counter for the string
		diff[_x]++
	}
	for _, _y := range y {
		// If the string _y is not in diff bail out early
		if _, ok := diff[_y]; !ok {
			return false
		}
		diff[_y] -= 1
		if diff[_y] == 0 {
			delete(diff, _y)
		}
	}
	return len(diff) == 0
}

func ruleToString(rule *cloudstack.FirewallRule) string {
	ls := &strings.Builder{}
	if rule == nil {
		ls.WriteString("nil")
	} else {
		switch rule.Protocol {
		case "tcp":
			fallthrough
		case "udp":
			fmt.Fprintf(ls, "{[%s] -> %s:[%d-%d] (%s)}", rule.Cidrlist, rule.Ipaddress, rule.Startport, rule.Endport, rule.Protocol)
		case "icmp":
			fmt.Fprintf(ls, "{[%s] -> %s [%d,%d] (%s)}", rule.Cidrlist, rule.Ipaddress, rule.Icmptype, rule.Icmpcode, rule.Protocol)
		default:
			fmt.Fprintf(ls, "{[%s] -> %s (%s)}", rule.Cidrlist, rule.Ipaddress, rule.Protocol)
		}
	}
	return ls.String()
}

func rulesToString(rules []*cloudstack.FirewallRule) string {
	ls := &strings.Builder{}
	first := true
	for _, rule := range rules {
		if first {
			first = false
		} else {
			ls.WriteString(", ")
		}
		ls.WriteString(ruleToString(rule))
	}
	return ls.String()
}

func rulesMapToString(rules map[*cloudstack.FirewallRule]bool) string {
	ls := &strings.Builder{}
	first := true
	for rule := range rules {
		if first {
			first = false
		} else {
			ls.WriteString(", ")
		}
		ls.WriteString(ruleToString(rule))
	}
	return ls.String()
}

// updateFirewallRule creates a firewall rule for a load balancer rule
//
// If the rule list is empty, all internet (IPv4: 0.0.0.0/0) is opened for the
// load balancer's port+protocol implicitly.
//
// Returns true if the firewall rule was created or updated
func (lb *loadBalancer) updateFirewallRule(publicIpId string, publicPort int, protocol LoadBalancerProtocol, allowedIPs []string) (bool, error) {
	if len(allowedIPs) == 0 {
		allowedIPs = []string{defaultAllowedCIDR}
	}

	p := lb.Firewall.NewListFirewallRulesParams()
	p.SetIpaddressid(publicIpId)
	p.SetListall(true)
	if lb.projectID != "" {
		p.SetProjectid(lb.projectID)
	}
	klog.V(4).Infof("Listing firewall rules for %v", p)
	r, err := lb.Firewall.ListFirewallRules(p)
	if err != nil {
		return false, fmt.Errorf("error fetching firewall rules for public IP %v: %v", publicIpId, err)
	}
	klog.V(4).Infof("All firewall rules for %v: %v", lb.ipAddr, rulesToString(r.FirewallRules))

	// find all rules that have a matching proto+port
	// a map may or may not be faster, but is a bit easier to understand
	filtered := make(map[*cloudstack.FirewallRule]bool)
	for _, rule := range r.FirewallRules {
		if rule.Protocol == protocol.IPProtocol() && rule.Startport == publicPort && rule.Endport == publicPort {
			filtered[rule] = true
		}
	}
	klog.V(4).Infof("Matching rules for %v: %v", lb.ipAddr, rulesMapToString(filtered))

	// determine if we already have a rule with matching cidrs
	var match *cloudstack.FirewallRule
	for rule := range filtered {
		cidrlist := splitCIDRList(rule.Cidrlist)
		if compareStringSlice(cidrlist, allowedIPs) {
			klog.V(4).Infof("Found identical rule: %v", rule)
			match = rule
			break
		}
	}

	if match != nil {
		// no need to create a new rule - but prevent deletion of the matching rule
		delete(filtered, match)
	}

	// delete all other rules that didn't match the CIDR list
	// do this first to prevent CS rule conflict errors
	klog.V(4).Infof("Firewall rules to be deleted for %v: %v", lb.ipAddr, rulesMapToString(filtered))
	for rule := range filtered {
		p := lb.Firewall.NewDeleteFirewallRuleParams(rule.Id)
		_, err = lb.Firewall.DeleteFirewallRule(p)
		if err != nil {
			// report the error, but keep on deleting the other rules
			klog.Errorf("Error deleting old firewall rule %v: %v", rule.Id, err)
		}
	}

	// create new rule if necessary
	if match == nil {
		// no rule found, create a new one
		p := lb.Firewall.NewCreateFirewallRuleParams(publicIpId, protocol.IPProtocol())
		p.SetCidrlist(allowedIPs)
		p.SetStartport(publicPort)
		p.SetEndport(publicPort)
		_, err = lb.Firewall.CreateFirewallRule(p)
		if err != nil {
			// return immediately if we can't create the new rule
			return false, fmt.Errorf("error creating new firewall rule for public IP %v, proto %v, port %v, allowed %v: %v", publicIpId, protocol, publicPort, allowedIPs, err)
		}
	}

	// return true (because we changed something), but also the last error if deleting one old rule failed
	return true, err
}

func (lb *loadBalancer) updateNetworkACL(publicPort int, protocol LoadBalancerProtocol, networkId string) (bool, error) {
	network, _, err := lb.Network.GetNetworkByID(networkId)
	if err != nil {
		return false, fmt.Errorf("error fetching Network with ID: %v, due to: %s", networkId, err)
	}

	networkAclList, count, err := lb.NetworkACL.GetNetworkACLListByID(network.Aclid)
	if err != nil {
		return false, fmt.Errorf("error fetching Network ACL List with ID: %v, due to: %s", network.Aclid, err)
	}

	if count == 0 {
		return false, fmt.Errorf("failed to find network ACL List with id: %v", network.Aclid)
	}

	if networkAclList.Name == "default_allow" || networkAclList.Name == "default_deny" {
		klog.Infof("Network is using a default network ACL. Cannot add ACL rules to default ACLs")
		return true, err
	}

	networkAclParams := lb.NetworkACL.NewListNetworkACLsParams()
	networkAclParams.SetAclid(network.Aclid)
	networkAclParams.SetNetworkid(networkId)

	networkAclResponse, err := lb.NetworkACL.ListNetworkACLs(networkAclParams)

	if err != nil {
		return false, fmt.Errorf("error fetching Network ACL with ID: %v for network with id: %v, due to: %s", network.Aclid, networkId, err)
	}

	// find all network ACL rules that have a matching proto+port
	// a map may or may not be faster, but is a bit easier to understand
	filtered := make(map[*cloudstack.NetworkACL]bool)
	for _, netAclRule := range networkAclResponse.NetworkACLs {
		if netAclRule.Protocol == protocol.IPProtocol() && netAclRule.Startport == strconv.Itoa(publicPort) && netAclRule.Endport == strconv.Itoa(publicPort) {
			filtered[netAclRule] = true
		}
	}

	if len(filtered) > 0 {
		klog.V(4).Infof("Network ACL rule for port %v and protocol %v already exists. No need to added a duplicate rule", publicPort, protocol)
		return true, err
	}

	// create ACL rule
	// ACL rules only know tcp/udp/icmp, so tcp-proxy maps to tcp. This also matches the
	// filter above, which would otherwise never find the rule again.
	acl := lb.NetworkACL.NewCreateNetworkACLParams(protocol.IPProtocol())
	acl.SetAclid(network.Aclid)
	acl.SetAction("Allow")
	acl.SetCidrlist([]string{"0.0.0.0/0"})
	acl.SetStartport(publicPort)
	acl.SetEndport(publicPort)
	acl.SetNetworkid(networkId)
	acl.SetTraffictype("Ingress")

	_, err = lb.NetworkACL.CreateNetworkACL(acl)
	if err != nil {
		return false, fmt.Errorf("error creating Network ACL for port: %v, due to: %s", publicPort, err)
	}
	return true, err
}

// deleteFirewallRule deletes the firewall rule associated with the ip:port:protocol combo
//
// returns true when corresponding rules were deleted
func (lb *loadBalancer) deleteFirewallRule(publicIpId string, publicPort int, protocol LoadBalancerProtocol) (bool, error) {
	p := lb.Firewall.NewListFirewallRulesParams()
	p.SetIpaddressid(publicIpId)
	p.SetListall(true)
	if lb.projectID != "" {
		p.SetProjectid(lb.projectID)
	}
	r, err := lb.Firewall.ListFirewallRules(p)
	if err != nil {
		return false, fmt.Errorf("error fetching firewall rules for public IP %v: %v", publicIpId, err)
	}

	// filter by proto:port
	filtered := make([]*cloudstack.FirewallRule, 0, 1)
	for _, rule := range r.FirewallRules {
		if rule.Protocol == protocol.IPProtocol() && rule.Startport == publicPort && rule.Endport == publicPort {
			filtered = append(filtered, rule)
		}
	}

	// delete all rules
	deleted := false
	for _, rule := range filtered {
		p := lb.Firewall.NewDeleteFirewallRuleParams(rule.Id)
		_, err = lb.Firewall.DeleteFirewallRule(p)
		if err != nil {
			klog.Errorf("Error deleting old firewall rule %v: %v", rule.Id, err)
		} else {
			deleted = true
		}
	}

	return deleted, err
}

// Delete Network ACLs deletes the Network ACL rule associated with the ip:port:protocol combo
func (lb *loadBalancer) deleteNetworkACLRule(publicPort int, protocol LoadBalancerProtocol, networkID string) (bool, error) {
	p := lb.NetworkACL.NewListNetworkACLsParams()
	p.SetListall(true)
	p.SetNetworkid(networkID)
	if lb.projectID != "" {
		p.SetProjectid(lb.projectID)
	}

	r, err := lb.NetworkACL.ListNetworkACLs(p)
	if err != nil {
		return false, fmt.Errorf("error fetching Network ACL rules Network ID %v: %v", networkID, err)
	}

	// filter by proto:port
	filtered := make([]*cloudstack.NetworkACL, 0, 1)
	for _, rule := range r.NetworkACLs {
		if rule.Protocol == protocol.IPProtocol() && rule.Startport == strconv.Itoa(publicPort) && rule.Endport == strconv.Itoa(publicPort) {
			filtered = append(filtered, rule)
		}
	}

	// delete first filtered rules
	if len(filtered) == 0 {
		klog.V(4).Infof("No ACL rules found matching protocol: %v and port: %v", protocol, publicPort)
		return true, nil
	}
	deleted := false
	ruleToBeDeleted := filtered[0]
	deleteAclParams := lb.NetworkACL.NewDeleteNetworkACLParams(ruleToBeDeleted.Id)
	_, err = lb.NetworkACL.DeleteNetworkACL(deleteAclParams)
	if err != nil {
		klog.Errorf("Error deleting old Network ACL rule %v: %v", ruleToBeDeleted.Id, err)
	} else {
		deleted = true
	}

	return deleted, err
}

// getStringFromServiceAnnotation searches a given v1.Service for a specific annotationKey and either returns the annotation's value or a specified defaultSetting
func getStringFromServiceAnnotation(service *corev1.Service, annotationKey string, defaultSetting string) string {
	klog.V(4).Infof("getStringFromServiceAnnotation(%s/%s, %v, %v)", service.Namespace, service.Name, annotationKey, defaultSetting)
	if annotationValue, ok := service.Annotations[annotationKey]; ok {
		//if there is an annotation for this setting, set the "setting" var to it
		// annotationValue can be empty, it is working as designed
		// it makes possible for instance provisioning loadbalancer without floatingip
		klog.V(4).Infof("Found a Service Annotation: %v = %v", annotationKey, annotationValue)
		return annotationValue
	}
	//if there is no annotation, set "settings" var to the value from cloud config
	if defaultSetting != "" {
		klog.V(4).Infof("Could not find a Service Annotation; falling back on cloud-config setting: %v = %v", annotationKey, defaultSetting)
	}
	return defaultSetting
}

// getBoolFromServiceAnnotation searches a given v1.Service for a specific annotationKey and either returns the annotation's boolean value or a specified defaultSetting
func getBoolFromServiceAnnotation(service *corev1.Service, annotationKey string, defaultSetting bool) bool {
	klog.V(4).Infof("getBoolFromServiceAnnotation(%s/%s, %v, %v)", service.Namespace, service.Name, annotationKey, defaultSetting)
	if annotationValue, ok := service.Annotations[annotationKey]; ok {
		returnValue := false
		switch annotationValue {
		case "true":
			returnValue = true
		case "false":
			returnValue = false
		default:
			returnValue = defaultSetting
		}

		klog.V(4).Infof("Found a Service Annotation: %v = %v", annotationKey, returnValue)
		return returnValue
	}
	klog.V(4).Infof("Could not find a Service Annotation; falling back to default setting: %v = %v", annotationKey, defaultSetting)
	return defaultSetting
}
