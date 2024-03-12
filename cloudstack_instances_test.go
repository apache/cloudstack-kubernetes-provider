package cloudstack

import (
	"context"
	"fmt"
	"testing"

	"github.com/apache/cloudstack-go/v2/cloudstack"
	"github.com/golang/mock/gomock"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	cloudprovider "k8s.io/cloud-provider"
)

func makeInstance(instanceID, privateIP, publicIP string, stateName string) *cloudstack.VirtualMachine {
	instance := cloudstack.VirtualMachine{
		Id:                  instanceID,
		Name:                "testDummyVM",
		Displayname:         "testDummyVM",
		State:               stateName,
		Zoneid:              "1d8d87d4-1425-459c-8d81-c6f57dca2bd2",
		Zonename:            "shouldwork",
		Hostname:            "SimulatedAgent.4234d24b-37fd-42bf-9184-874889001baf",
		Serviceofferingid:   "498ce071-a077-4237-9492-e55c42553327",
		Serviceofferingname: "Very small instance",
		Publicip:            publicIP,
		Nic: []cloudstack.Nic{
			{
				Id:        "47d79da1-2fe1-4a44-a503-523055714a72",
				Ipaddress: privateIP,
			},
		},
		Instancename: "i-2-683-QA",
	}

	return &instance
}

func makeNode(nodeName string) *corev1.Node {
	providerID := "cloudstack:///915653c4-298b-4d74-bdee-4ced282114f1"
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
		},
		Spec: corev1.NodeSpec{
			ProviderID: providerID,
		},
	}
}

func makeNodeWithoutProviderID(nodeName string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
		},
	}
}

func TestGetInstanceProviderID(t *testing.T) {
	testCases := []struct {
		name       string
		instance   *cloudstack.VirtualMachine
		providerID string
	}{
		{
			name:       "get instance (with public IP) provider ID",
			instance:   makeInstance("2fda7aaa-24df-11ee-be56-0242ac120002", "192.168.0.1", "1.2.3.4", "Running"),
			providerID: "cloudstack:///2fda7aaa-24df-11ee-be56-0242ac120002",
		},
		{
			name:       "get instance (without public IP) provider ID",
			instance:   makeInstance("89e5fdbc-24df-11ee-be56-0242ac120002", "192.168.0.2", "", "Running"),
			providerID: "cloudstack:///89e5fdbc-24df-11ee-be56-0242ac120002",
		},
		{
			name:       "get instance (without private IP) provider ID",
			instance:   makeInstance("92794b3c-24df-11ee-be56-0242ac120002", "", "1.2.3.4", "Running"),
			providerID: "cloudstack:///92794b3c-24df-11ee-be56-0242ac120002",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			providerID := getInstanceProviderID(testCase.instance)
			assert.Equal(t, testCase.providerID, providerID)
		})
	}
}

func TestInstanceExists(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	cs := cloudstack.NewMockClient(mockCtrl)
	ms := cs.VirtualMachine.(*cloudstack.MockVirtualMachineServiceIface)

	fakeInstances := &CSCloud{
		client: cs,
	}

	nodeName := "testDummyVM"

	tests := []struct {
		name           string
		node           *corev1.Node
		mockedCSOutput *cloudstack.VirtualMachine
		expectedResult bool
	}{
		{
			name:           "test InstanceExists with running instance",
			node:           makeNode(nodeName),
			mockedCSOutput: makeInstance("915653c4-298b-4d74-bdee-4ced282114f1", "192.168.0.1", "1.2.3.4", "Running"),
			expectedResult: true,
		},
		{
			name:           "test InstanceExists with stopped instance",
			node:           makeNode(nodeName),
			mockedCSOutput: makeInstance("915653c4-298b-4d74-bdee-4ced282114f1", "192.168.0.1", "1.2.3.4", "Stopped"),
			expectedResult: true,
		},
		{
			name:           "test InstanceExists with destroyed instance (node without providerID)",
			node:           makeNodeWithoutProviderID(nodeName),
			mockedCSOutput: makeInstance("915653c4-298b-4d74-bdee-4ced282114f1", "192.168.0.1", "1.2.3.4", "Destroyed"),
			expectedResult: true,
		},
		{
			name:           "test InstanceExists with non existent node without providerID",
			node:           makeNodeWithoutProviderID("nonExistingVM"),
			mockedCSOutput: nil,
			expectedResult: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// t.Logf("test node: %v", test.node)
			if test.node.Spec.ProviderID == "" {
				if test.node.Name == "testDummyVM" {
					ms.EXPECT().GetVirtualMachineByName("testDummyVM", gomock.Any()).Return(test.mockedCSOutput, 1, nil)
				} else {
					ms.EXPECT().GetVirtualMachineByName("nonExistingVM", gomock.Any()).Return(test.mockedCSOutput, 0, fmt.Errorf("No match found for ..."))
				}
			} else {
				ms.EXPECT().GetVirtualMachineByID("915653c4-298b-4d74-bdee-4ced282114f1", gomock.Any()).Return(test.mockedCSOutput, 1, nil)
			}

			exists, err := fakeInstances.InstanceExists(context.TODO(), test.node)

			if err != nil {
				t.Errorf("InstanceExists failed with node %v: %v", nodeName, err)
			}

			if exists != test.expectedResult {
				t.Errorf("unexpected result, InstanceExists should return %v", test.expectedResult)
			}
		})
	}
}

func TestInstanceShutdown(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	cs := cloudstack.NewMockClient(mockCtrl)
	ms := cs.VirtualMachine.(*cloudstack.MockVirtualMachineServiceIface)

	fakeInstances := &CSCloud{
		client: cs,
	}

	nodeName := "testDummyVM"

	tests := []struct {
		name           string
		node           *corev1.Node
		mockedCSOutput *cloudstack.VirtualMachine
		expectedResult bool
	}{
		{
			name:           "test InstanceShutdown with running instance",
			node:           makeNode(nodeName),
			mockedCSOutput: makeInstance("915653c4-298b-4d74-bdee-4ced282114f1", "192.168.0.1", "1.2.3.6", "Running"),
			expectedResult: false,
		},
		{
			name:           "test InstanceShutdown with running instance (node without providerID)",
			node:           makeNodeWithoutProviderID(nodeName),
			mockedCSOutput: makeInstance("915653c4-298b-4d74-bdee-4ced282114f1", "192.168.0.1", "1.2.3.6", "Running"),
			expectedResult: false,
		},
		{
			name:           "test InstanceShutdown with stopped instance",
			node:           makeNode(nodeName),
			mockedCSOutput: makeInstance("915653c4-298b-4d74-bdee-4ced282114f1", "192.168.0.1", "1.2.3.6", "Stopped"),
			expectedResult: true,
		},
		{
			name:           "test InstanceShutdown with stopped instance (node without providerID)",
			node:           makeNodeWithoutProviderID(nodeName),
			mockedCSOutput: makeInstance("915653c4-298b-4d74-bdee-4ced282114f1", "192.168.0.1", "1.2.3.6", "Stopped"),
			expectedResult: true,
		},
		{
			name:           "test InstanceShutdown with destroyed instance",
			node:           makeNode(nodeName),
			mockedCSOutput: makeInstance("915653c4-298b-4d74-bdee-4ced282114f1", "192.168.0.1", "1.2.3.6", "Destroyed"),
			expectedResult: false,
		},
		{
			name:           "test InstanceShutdown with terminated instance (node without providerID)",
			node:           makeNodeWithoutProviderID(nodeName),
			mockedCSOutput: makeInstance("915653c4-298b-4d74-bdee-4ced282114f1", "192.168.0.1", "1.2.3.6", "Destroyed"),
			expectedResult: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.node.Spec.ProviderID == "" {
				ms.EXPECT().GetVirtualMachineByName("testDummyVM", gomock.Any()).Return(test.mockedCSOutput, 1, nil)
			} else {
				ms.EXPECT().GetVirtualMachineByID("915653c4-298b-4d74-bdee-4ced282114f1", gomock.Any()).Return(test.mockedCSOutput, 1, nil)
			}

			shutdown, err := fakeInstances.InstanceShutdown(context.TODO(), test.node)

			if err != nil {
				t.Logf("InstanceShutdown failed with node %v: %v", nodeName, err)
			}

			if shutdown != test.expectedResult {
				t.Errorf("unexpected result, InstanceShutdown should return %v", test.expectedResult)
			}
		})
	}
}

func TestInstanceMetadata(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	cs := cloudstack.NewMockClient(mockCtrl)
	ms := cs.VirtualMachine.(*cloudstack.MockVirtualMachineServiceIface)

	fakeInstances := &CSCloud{
		client: cs,
	}

	nodeName := "testDummyVM"

	tests := []struct {
		name           string
		node           *corev1.Node
		expectedResult *cloudprovider.InstanceMetadata
		mockedCSOutput *cloudstack.VirtualMachine
	}{
		{
			name:           "test InstanceMetadata with running instance",
			node:           makeNode(nodeName),
			mockedCSOutput: makeInstance("915653c4-298b-4d74-bdee-4ced282114f1", "192.168.0.1", "1.2.3.6", "Running"),
			expectedResult: &cloudprovider.InstanceMetadata{
				ProviderID:   "cloudstack:///915653c4-298b-4d74-bdee-4ced282114f1",
				InstanceType: "Very_small_instance",
				NodeAddresses: []corev1.NodeAddress{
					{
						Type:    "InternalIP",
						Address: "192.168.0.1",
					},
					{
						Type:    "Hostname",
						Address: "SimulatedAgent.4234d24b-37fd-42bf-9184-874889001baf",
					},
					{
						Type:    "ExternalIP",
						Address: "1.2.3.6",
					},
				},
				Zone: "shouldwork",
			},
		},
		{
			name:           "test InstanceMetadata with running instance (node without providerID)",
			node:           makeNodeWithoutProviderID(nodeName),
			mockedCSOutput: makeInstance("915653c4-298b-4d74-bdee-4ced282114f1", "192.168.0.1", "1.2.3.6", "Running"),
			expectedResult: &cloudprovider.InstanceMetadata{
				ProviderID:   "cloudstack:///915653c4-298b-4d74-bdee-4ced282114f1",
				InstanceType: "Very_small_instance",
				NodeAddresses: []corev1.NodeAddress{
					{
						Type:    "InternalIP",
						Address: "192.168.0.1",
					},
					{
						Type:    "Hostname",
						Address: "SimulatedAgent.4234d24b-37fd-42bf-9184-874889001baf",
					},
					{
						Type:    "ExternalIP",
						Address: "1.2.3.6",
					},
				},
				Zone: "shouldwork",
			},
		},
		{
			name:           "test InstanceMetadata with stopped instance",
			node:           makeNode(nodeName),
			mockedCSOutput: makeInstance("915653c4-298b-4d74-bdee-4ced282114f1", "192.168.0.1", "", "Stopped"),
			expectedResult: &cloudprovider.InstanceMetadata{
				ProviderID:   "cloudstack:///915653c4-298b-4d74-bdee-4ced282114f1",
				InstanceType: "Very_small_instance",
				NodeAddresses: []corev1.NodeAddress{
					{
						Type:    "InternalIP",
						Address: "192.168.0.1",
					},
					{
						Type:    "Hostname",
						Address: "SimulatedAgent.4234d24b-37fd-42bf-9184-874889001baf",
					},
				},
				Zone: "shouldwork",
			},
		},
		{
			name:           "test InstanceMetadata with destroyed instance",
			node:           makeNode(nodeName),
			mockedCSOutput: makeInstance("915653c4-298b-4d74-bdee-4ced282114f1", "192.168.0.1", "", "Destroyed"),
			expectedResult: &cloudprovider.InstanceMetadata{
				ProviderID:   "cloudstack:///915653c4-298b-4d74-bdee-4ced282114f1",
				InstanceType: "Very_small_instance",
				NodeAddresses: []corev1.NodeAddress{
					{
						Type:    "InternalIP",
						Address: "192.168.0.1",
					},
					{
						Type:    "Hostname",
						Address: "SimulatedAgent.4234d24b-37fd-42bf-9184-874889001baf",
					},
				},
				Zone: "shouldwork",
			},
		},
		{
			name:           "test InstanceMetadata with destroyed instance (node without providerID)",
			node:           makeNodeWithoutProviderID(nodeName),
			mockedCSOutput: makeInstance("915653c4-298b-4d74-bdee-4ced282114f1", "192.168.0.1", "", "Destroyed"),
			expectedResult: &cloudprovider.InstanceMetadata{
				ProviderID:   "cloudstack:///915653c4-298b-4d74-bdee-4ced282114f1",
				InstanceType: "Very_small_instance",
				NodeAddresses: []corev1.NodeAddress{
					{
						Type:    "InternalIP",
						Address: "192.168.0.1",
					},
					{
						Type:    "Hostname",
						Address: "SimulatedAgent.4234d24b-37fd-42bf-9184-874889001baf",
					},
				},
				Zone: "shouldwork",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.node.Spec.ProviderID == "" {
				ms.EXPECT().GetVirtualMachineByName("testDummyVM", gomock.Any()).Return(test.mockedCSOutput, 1, nil)
			} else {
				ms.EXPECT().GetVirtualMachineByID("915653c4-298b-4d74-bdee-4ced282114f1", gomock.Any()).Return(test.mockedCSOutput, 1, nil)
			}

			metadata, err := fakeInstances.InstanceMetadata(context.TODO(), test.node)

			if err != nil {
				t.Logf("InstanceMetadata failed with node %v: %v", nodeName, err)
			}

			if !cmp.Equal(metadata, test.expectedResult) {
				t.Errorf("unexpected metadata %v, InstanceMetadata should return %v", metadata, test.expectedResult)
			}
		})
	}
}
