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
	"strconv"
	"strings"

	"github.com/apache/cloudstack-go/v2/cloudstack"
	"k8s.io/klog"

	v1 "k8s.io/api/core/v1"
	cloudprovider "k8s.io/cloud-provider"
)

// defaultAllowedCIDR is the network range that is allowed on the firewall
// by default when no explicit CIDR list is given on a LoadBalancer.
const defaultAllowedCIDR = "0.0.0.0/0"

type loadBalancer struct {
	*cloudstack.CloudStackClient

	name      string
	algorithm string
	hostIDs   []string
	ipAddr    string
	ipAddrID  string
	networkID string
	projectID string
	rules     map[string]*cloudstack.LoadBalancerRule
}

// GetLoadBalancer returns whether the specified load balancer exists, and if so, what its status is.
func (cs *CSCloud) GetLoadBalancer(ctx context.Context, clusterName string, service *v1.Service) (*v1.LoadBalancerStatus, bool, error) {
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

	status := &v1.LoadBalancerStatus{}
	status.Ingress = append(status.Ingress, v1.LoadBalancerIngress{IP: lb.ipAddr})

	return status, true, nil
}

// EnsureLoadBalancer creates a new load balancer, or updates the existing one. Returns the status of the balancer.
func (cs *CSCloud) EnsureLoadBalancer(ctx context.Context, clusterName string, service *v1.Service, nodes []*v1.Node) (status *v1.LoadBalancerStatus, err error) {
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
	case v1.ServiceAffinityNone:
		lb.algorithm = "roundrobin"
	case v1.ServiceAffinityClientIP:
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
	}

	klog.V(4).Infof("Load balancer %v is associated with IP %v", lb.name, lb.ipAddr)

	for _, port := range service.Spec.Ports {
		// Construct the protocol name first, we need it a few times
		protocol := ProtocolFromServicePort(port, service.Annotations)
		if protocol == LoadBalancerProtocolInvalid {
			return nil, fmt.Errorf("unsupported load balancer protocol: %v", port.Protocol)
		}

		// All ports have their own load balancer rule, so add the port to lbName to keep the names unique.
		lbRuleName := fmt.Sprintf("%s-%s-%d", lb.name, protocol, port.Port)

		// If the load balancer rule exists and is up-to-date, we move on to the next rule.
		lbRule, needsUpdate, err := lb.checkLoadBalancerRule(lbRuleName, port, protocol)
		if err != nil {
			return nil, err
		}

		if lbRule != nil {
			if needsUpdate {
				klog.V(4).Infof("Updating load balancer rule: %v", lbRuleName)
				if err := lb.updateLoadBalancerRule(lbRuleName, protocol); err != nil {
					return nil, err
				}
				// Delete the rule from the map, to prevent it being deleted.
				delete(lb.rules, lbRuleName)
			} else {
				klog.V(4).Infof("Load balancer rule %v is up-to-date", lbRuleName)
				// Delete the rule from the map, to prevent it being deleted.
				delete(lb.rules, lbRuleName)
			}
		} else {
			klog.V(4).Infof("Creating load balancer rule: %v", lbRuleName)
			lbRule, err = lb.createLoadBalancerRule(lbRuleName, port, protocol)
			if err != nil {
				return nil, err
			}

			klog.V(4).Infof("Assigning hosts (%v) to load balancer rule: %v", lb.hostIDs, lbRuleName)
			if err = lb.assignHostsToRule(lbRule, lb.hostIDs); err != nil {
				return nil, err
			}
		}

		if lbRule != nil {
			klog.V(4).Infof("Creating firewall rules for load balancer rule: %v (%v:%v:%v)", lbRuleName, protocol, lbRule.Publicip, port.Port)
			if _, err := lb.updateFirewallRule(lbRule.Publicipid, int(port.Port), protocol, service.Spec.LoadBalancerSourceRanges); err != nil {
				return nil, err
			}
		}
	}

	// Cleanup any rules that are now still in the rules map, as they are no longer needed.
	for _, lbRule := range lb.rules {
		protocol := ProtocolFromLoadBalancer(lbRule.Protocol)
		if protocol == LoadBalancerProtocolInvalid {
			return nil, fmt.Errorf("Error parsing protocol %v: %v", lbRule.Protocol, err)
		}
		port, err := strconv.ParseInt(lbRule.Publicport, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("Error parsing port %s: %v", lbRule.Publicport, err)
		}

		klog.V(4).Infof("Deleting firewall rules associated with load balancer rule: %v (%v:%v:%v)", lbRule.Name, protocol, lbRule.Publicip, port)
		if _, err := lb.deleteFirewallRule(lbRule.Publicipid, int(port), protocol); err != nil {
			return nil, err
		}

		klog.V(4).Infof("Deleting obsolete load balancer rule: %v", lbRule.Name)
		if err := lb.deleteLoadBalancerRule(lbRule); err != nil {
			return nil, err
		}
	}

	status = &v1.LoadBalancerStatus{}
	status.Ingress = []v1.LoadBalancerIngress{{IP: lb.ipAddr}}

	return status, nil
}

// UpdateLoadBalancer updates hosts under the specified load balancer.
func (cs *CSCloud) UpdateLoadBalancer(ctx context.Context, clusterName string, service *v1.Service, nodes []*v1.Node) error {
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

// EnsureLoadBalancerDeleted deletes the specified load balancer if it exists, returning
// nil if the load balancer specified either didn't exist or was successfully deleted.
func (cs *CSCloud) EnsureLoadBalancerDeleted(ctx context.Context, clusterName string, service *v1.Service) error {
	klog.V(4).Infof("EnsureLoadBalancerDeleted(%v, %v, %v)", clusterName, service.Namespace, service.Name)

	// Get the load balancer details and existing rules.
	lb, err := cs.getLoadBalancer(service)
	if err != nil {
		return err
	}

	for _, lbRule := range lb.rules {
		klog.V(4).Infof("Deleting firewall rules for load balancer: %v", lbRule.Name)
		protocol := ProtocolFromLoadBalancer(lbRule.Protocol)
		if protocol == LoadBalancerProtocolInvalid {
			klog.Errorf("Error parsing protocol: %v", lbRule.Protocol)
		} else {
			port, err := strconv.ParseInt(lbRule.Publicport, 10, 32)
			if err != nil {
				klog.Errorf("Error parsing port: %v", err)
			} else {
				lb.deleteFirewallRule(lbRule.Publicipid, int(port), protocol)
			}

			klog.V(4).Infof("Deleting load balancer rule: %v", lbRule.Name)
			if err := lb.deleteLoadBalancerRule(lbRule); err != nil {
				return err
			}
		}
	}

	if lb.ipAddr != "" && lb.ipAddr != service.Spec.LoadBalancerIP {
		klog.V(4).Infof("Releasing load balancer IP: %v", lb.ipAddr)
		if err := lb.releaseLoadBalancerIP(); err != nil {
			return err
		}
	}

	return nil
}

// GetLoadBalancerName retrieves the name of the LoadBalancer.
func (cs *CSCloud) GetLoadBalancerName(ctx context.Context, clusterName string, service *v1.Service) string {
	return cloudprovider.DefaultLoadBalancerName(service)
}

// getLoadBalancer retrieves the IP address and ID and all the existing rules it can find.
func (cs *CSCloud) getLoadBalancer(service *v1.Service) (*loadBalancer, error) {
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

// verifyHosts verifies if all hosts belong to the same network, and returns the host ID's and network ID.
func (cs *CSCloud) verifyHosts(nodes []*v1.Node) ([]string, string, error) {
	hostNames := map[string]bool{}
	for _, node := range nodes {
		hostNames[strings.ToLower(node.Name)] = true
	}

	p := cs.client.VirtualMachine.NewListVirtualMachinesParams()
	p.SetListall(true)

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

	return hostIDs, networkID, nil
}

// hasLoadBalancerIP returns true if we have a load balancer address and ID.
func (lb *loadBalancer) hasLoadBalancerIP() bool {
	return lb.ipAddr != "" && lb.ipAddrID != ""
}

// getLoadBalancerIP retieves an existing IP or associates a new IP.
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
	p.SetListall(true)

	if lb.projectID != "" {
		p.SetProjectid(lb.projectID)
	}

	l, err := lb.Address.ListPublicIpAddresses(p)
	if err != nil {
		return fmt.Errorf("error retrieving IP address: %v", err)
	}

	if l.Count != 1 {
		return fmt.Errorf("could not find IP address %v", loadBalancerIP)
	}

	lb.ipAddr = l.PublicIpAddresses[0].Ipaddress
	lb.ipAddrID = l.PublicIpAddresses[0].Id

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

	// Associate a new IP address
	r, err := lb.Address.AssociateIpAddress(p)
	if err != nil {
		return fmt.Errorf("error associating new IP address: %v", err)
	}

	lb.ipAddr = r.Ipaddress
	lb.ipAddrID = r.Id

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

// checkLoadBalancerRule checks if the rule already exists and if it does, if it can be updated. If
// it does exist but cannot be updated, it will delete the existing rule so it can be created again.
func (lb *loadBalancer) checkLoadBalancerRule(lbRuleName string, port v1.ServicePort, protocol LoadBalancerProtocol) (*cloudstack.LoadBalancerRule, bool, error) {
	lbRule, ok := lb.rules[lbRuleName]
	if !ok {
		return nil, false, nil
	}

	// Check if any of the values we cannot update (those that require a new load balancer rule) are changed.
	if lbRule.Publicip == lb.ipAddr && lbRule.Privateport == strconv.Itoa(int(port.NodePort)) && lbRule.Publicport == strconv.Itoa(int(port.Port)) {
		updateAlgo := lbRule.Algorithm != lb.algorithm
		updateProto := lbRule.Protocol != protocol.CSProtocol()
		return lbRule, updateAlgo || updateProto, nil
	}

	// Delete the load balancer rule so we can create a new one using the new values.
	if err := lb.deleteLoadBalancerRule(lbRule); err != nil {
		return nil, false, err
	}

	return nil, false, nil
}

// updateLoadBalancerRule updates a load balancer rule.
func (lb *loadBalancer) updateLoadBalancerRule(lbRuleName string, protocol LoadBalancerProtocol) error {
	lbRule := lb.rules[lbRuleName]

	p := lb.LoadBalancer.NewUpdateLoadBalancerRuleParams(lbRule.Id)
	p.SetAlgorithm(lb.algorithm)
	p.SetProtocol(protocol.CSProtocol())

	_, err := lb.LoadBalancer.UpdateLoadBalancerRule(p)
	return err
}

// createLoadBalancerRule creates a new load balancer rule and returns it's ID.
func (lb *loadBalancer) createLoadBalancerRule(lbRuleName string, port v1.ServicePort, protocol LoadBalancerProtocol) (*cloudstack.LoadBalancerRule, error) {
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
	if len(diff) == 0 {
		return true
	}
	return false
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
	for rule, _ := range rules {
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
		} else {
		}
	}
	klog.V(4).Infof("Matching rules for %v: %v", lb.ipAddr, rulesMapToString(filtered))

	// determine if we already have a rule with matching cidrs
	var match *cloudstack.FirewallRule
	for rule := range filtered {
		cidrlist := strings.Split(rule.Cidrlist, ",")
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
