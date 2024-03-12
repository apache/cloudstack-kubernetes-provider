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
	"errors"
	"fmt"

	"github.com/apache/cloudstack-go/v2/cloudstack"
	corev1 "k8s.io/api/core/v1"
	cloudprovider "k8s.io/cloud-provider"
	"k8s.io/klog/v2"
)

func (cs *CSCloud) nodeAddresses(instance *cloudstack.VirtualMachine) ([]corev1.NodeAddress, error) {
	if len(instance.Nic) == 0 {
		return nil, errors.New("instance does not have an internal IP")
	}

	addresses := []corev1.NodeAddress{
		{Type: corev1.NodeInternalIP, Address: instance.Nic[0].Ipaddress},
	}

	if instance.Hostname != "" {
		addresses = append(addresses, corev1.NodeAddress{Type: corev1.NodeHostName, Address: instance.Hostname})
	}

	if instance.Publicip != "" {
		addresses = append(addresses, corev1.NodeAddress{Type: corev1.NodeExternalIP, Address: instance.Publicip})
	}

	return addresses, nil
}

func (cs *CSCloud) InstanceExists(ctx context.Context, node *corev1.Node) (bool, error) {
	_, err := cs.getInstance(ctx, node)

	if err == cloudprovider.InstanceNotFound {
		klog.V(5).Infof("instance not found for node: %s", node.Name)
		return false, nil
	}

	if err != nil {
		return false, err
	}

	return true, nil
}

func (cs *CSCloud) InstanceShutdown(ctx context.Context, node *corev1.Node) (bool, error) {
	instance, err := cs.getInstance(ctx, node)
	if err != nil {
		return false, err
	}

	return instance != nil && instance.State == "Stopped", nil
}

func (cs *CSCloud) InstanceMetadata(ctx context.Context, node *corev1.Node) (*cloudprovider.InstanceMetadata, error) {
	instance, err := cs.getInstance(ctx, node)
	if err != nil {
		return nil, err
	}

	addresses, err := cs.nodeAddresses(instance)
	if err != nil {
		return nil, err
	}

	return &cloudprovider.InstanceMetadata{
		ProviderID:    getInstanceProviderID(instance),
		InstanceType:  sanitizeLabel(instance.Serviceofferingname),
		NodeAddresses: addresses,
		Zone:          sanitizeLabel(instance.Zonename),
		Region:        "",
	}, nil
}

func getInstanceProviderID(instance *cloudstack.VirtualMachine) string {
	// TODO: implement region
	return fmt.Sprintf("%s:///%s", ProviderName, instance.Id)
}

func (cs *CSCloud) getInstance(ctx context.Context, node *corev1.Node) (*cloudstack.VirtualMachine, error) {
	if node.Spec.ProviderID == "" {
		var err error
		klog.V(4).Infof("looking for node by node name %v", node.Name)
		instance, count, err := cs.client.VirtualMachine.GetVirtualMachineByName(
			node.Name,
			cloudstack.WithProject(cs.projectID),
		)
		if err != nil {
			if count == 0 {
				return nil, cloudprovider.InstanceNotFound
			}
			if count > 1 {
				return nil, fmt.Errorf("getInstance: multiple instances found")
			}
			return nil, fmt.Errorf("getInstance: error retrieving instance by name: %v", err)
		}
		return instance, nil
	}

	klog.V(4).Infof("looking for node by provider ID %v", node.Spec.ProviderID)
	id, _, err := instanceIDFromProviderID(node.Spec.ProviderID)
	if err != nil {
		return nil, err
	}

	instance, count, err := cs.client.VirtualMachine.GetVirtualMachineByID(
		id,
		cloudstack.WithProject(cs.projectID),
	)
	if err != nil {
		if count == 0 {
			return nil, cloudprovider.InstanceNotFound
		}
		if count > 1 {
			return nil, fmt.Errorf("getInstance: multiple instances found")
		}
		return nil, fmt.Errorf("error retrieving instance by provider ID: %v", err)
	}

	return instance, nil
}
