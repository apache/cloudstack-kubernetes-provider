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
	"k8s.io/apimachinery/pkg/types"
	cloudprovider "k8s.io/cloud-provider"
)

// NodeAddresses returns the addresses of the specified instance.
func (cs *CSCloud) NodeAddresses(ctx context.Context, name types.NodeName) ([]corev1.NodeAddress, error) {
	instance, count, err := cs.client.VirtualMachine.GetVirtualMachineByName(
		string(name),
		cloudstack.WithProject(cs.projectID),
	)
	if err != nil {
		if count == 0 {
			return nil, cloudprovider.InstanceNotFound
		}
		return nil, fmt.Errorf("error retrieving node addresses: %v", err)
	}

	return cs.nodeAddresses(instance)
}

// NodeAddressesByProviderID returns the addresses of the specified instance.
func (cs *CSCloud) NodeAddressesByProviderID(ctx context.Context, providerID string) ([]corev1.NodeAddress, error) {
	id, _, err := instanceIDFromProviderID(providerID)
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
		return nil, fmt.Errorf("error retrieving node addresses: %v", err)
	}

	return cs.nodeAddresses(instance)
}

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

// InstanceID returns the cloud provider ID of the specified instance.
func (cs *CSCloud) InstanceID(ctx context.Context, name types.NodeName) (string, error) {
	instance, count, err := cs.client.VirtualMachine.GetVirtualMachineByName(
		string(name),
		cloudstack.WithProject(cs.projectID),
	)
	if err != nil {
		if count == 0 {
			return "", cloudprovider.InstanceNotFound
		}
		return "", fmt.Errorf("error retrieving instance ID: %v", err)
	}

	// return instance ID with empty region
	// in the future, with region support, this should be <region>/<instanceid>
	return "/" + instance.Id, nil
}

// InstanceType returns the type of the specified instance.
func (cs *CSCloud) InstanceType(ctx context.Context, name types.NodeName) (string, error) {
	instance, count, err := cs.client.VirtualMachine.GetVirtualMachineByName(
		string(name),
		cloudstack.WithProject(cs.projectID),
	)
	if err != nil {
		if count == 0 {
			return "", cloudprovider.InstanceNotFound
		}
		return "", fmt.Errorf("error retrieving instance type: %v", err)
	}

	return sanitizeLabel(instance.Serviceofferingname), nil
}

// InstanceTypeByProviderID returns the type of the specified instance.
func (cs *CSCloud) InstanceTypeByProviderID(ctx context.Context, providerID string) (string, error) {
	id, _, err := instanceIDFromProviderID(providerID)
	if err != nil {
		return "", err
	}

	instance, count, err := cs.client.VirtualMachine.GetVirtualMachineByID(
		id,
		cloudstack.WithProject(cs.projectID),
	)
	if err != nil {
		if count == 0 {
			return "", cloudprovider.InstanceNotFound
		}
		return "", fmt.Errorf("error retrieving instance type: %v", err)
	}

	return sanitizeLabel(instance.Serviceofferingname), nil
}

// AddSSHKeyToAllInstances is currently not implemented.
func (cs *CSCloud) AddSSHKeyToAllInstances(ctx context.Context, user string, keyData []byte) error {
	return cloudprovider.NotImplemented
}

// CurrentNodeName returns the name of the node we are currently running on.
func (cs *CSCloud) CurrentNodeName(ctx context.Context, hostname string) (types.NodeName, error) {
	return types.NodeName(hostname), nil
}

// InstanceExistsByProviderID returns if the instance still exists.
func (cs *CSCloud) InstanceExistsByProviderID(ctx context.Context, providerID string) (bool, error) {
	id, _, err := instanceIDFromProviderID(providerID)
	if err != nil {
		return false, err
	}

	_, count, err := cs.client.VirtualMachine.GetVirtualMachineByID(
		id,
		cloudstack.WithProject(cs.projectID),
	)
	if err != nil {
		if count == 0 {
			return false, nil
		}
		return false, fmt.Errorf("error retrieving instance: %v", err)
	}

	return true, nil
}

// InstanceShutdownByProviderID returns true if the instance is in safe state to detach volumes
func (cs *CSCloud) InstanceShutdownByProviderID(ctx context.Context, providerID string) (bool, error) {
	id, _, err := instanceIDFromProviderID(providerID)
	if err != nil {
		return false, err
	}

	instance, count, err := cs.client.VirtualMachine.GetVirtualMachineByID(
		id,
		cloudstack.WithProject(cs.projectID),
	)
	if err != nil {
		if count == 0 {
			return false, cloudprovider.InstanceNotFound
		}
		return false, fmt.Errorf("error retrieving instance state: %v", err)
	}
	return instance != nil && instance.State == "Stopped", nil
}

func (cs *CSCloud) InstanceExists(ctx context.Context, node *corev1.Node) (bool, error) {
	nodeName := types.NodeName(node.Name)
	providerID, err := cs.InstanceID(ctx, nodeName)
	if err != nil {
		return false, err
	}

	return cs.InstanceExistsByProviderID(ctx, providerID)
}

func (cs *CSCloud) InstanceShutdown(ctx context.Context, node *corev1.Node) (bool, error) {
	return cs.InstanceShutdownByProviderID(ctx, node.Spec.ProviderID)
}

func (cs *CSCloud) InstanceMetadata(ctx context.Context, node *corev1.Node) (*cloudprovider.InstanceMetadata, error) {
	id, region, err := instanceIDFromProviderID(node.Spec.ProviderID)
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
		return nil, fmt.Errorf("error retrieving instance: %v", err)
	}

	addresses, err := cs.nodeAddresses(instance)
	if err != nil {
		return nil, err
	}

	return &cloudprovider.InstanceMetadata{
		ProviderID:    node.Spec.ProviderID,
		InstanceType:  sanitizeLabel(instance.Serviceofferingname),
		NodeAddresses: addresses,
		Zone:          sanitizeLabel(instance.Zonename),
		Region:        region,
	}, nil
}
