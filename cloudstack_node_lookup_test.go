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
	"reflect"
	"strings"
	"testing"

	"github.com/apache/cloudstack-go/v2/cloudstack"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	cloudprovider "k8s.io/cloud-provider"
)

const testProjectID = "6a3c1b9e-4f3d-4b43-9a6a-0c3d1f2e7b11"

type projectRecorder struct{ projectID string }

func (r *projectRecorder) SetProjectid(id string) { r.projectID = id }

// appliedProjectID returns the project id the lookup options would put on a request.
func appliedProjectID(t *testing.T, opts []cloudstack.OptionFunc) string {
	t.Helper()
	recorder := &projectRecorder{}
	for _, opt := range opts {
		if err := opt(nil, recorder); err != nil {
			t.Fatalf("applying lookup option: %v", err)
		}
	}
	return recorder.projectID
}

type vmLookupEnv struct {
	cs *CSCloud
	vm *cloudstack.MockVirtualMachineServiceIface
}

func newVMLookupEnv(t *testing.T, projectID string) *vmLookupEnv {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	vm := cloudstack.NewMockVirtualMachineServiceIface(ctrl)
	return &vmLookupEnv{
		cs: &CSCloud{
			client:    &cloudstack.CloudStackClient{VirtualMachine: vm},
			projectID: projectID,
		},
		vm: vm,
	}
}

// expectByName expects one lookup of the instance by name, scoped to the CSCloud's project.
func (e *vmLookupEnv) expectByName(t *testing.T, name string, result *cloudstack.VirtualMachine, count int, err error) *gomock.Call {
	t.Helper()
	return e.vm.EXPECT().GetVirtualMachineByName(gomock.Any(), gomock.Any()).
		DoAndReturn(func(got string, opts ...cloudstack.OptionFunc) (*cloudstack.VirtualMachine, int, error) {
			if got != name {
				t.Errorf("GetVirtualMachineByName name = %q, want %q", got, name)
			}
			if gotProject := appliedProjectID(t, opts); gotProject != e.cs.projectID {
				t.Errorf("GetVirtualMachineByName project = %q, want %q", gotProject, e.cs.projectID)
			}
			return result, count, err
		})
}

// expectByID expects one lookup of the instance by ID, scoped to the CSCloud's project.
func (e *vmLookupEnv) expectByID(t *testing.T, id string, result *cloudstack.VirtualMachine, count int, err error) *gomock.Call {
	t.Helper()
	return e.vm.EXPECT().GetVirtualMachineByID(gomock.Any(), gomock.Any()).
		DoAndReturn(func(got string, opts ...cloudstack.OptionFunc) (*cloudstack.VirtualMachine, int, error) {
			if got != id {
				t.Errorf("GetVirtualMachineByID id = %q, want %q", got, id)
			}
			if gotProject := appliedProjectID(t, opts); gotProject != e.cs.projectID {
				t.Errorf("GetVirtualMachineByID project = %q, want %q", gotProject, e.cs.projectID)
			}
			return result, count, err
		})
}

func testInstance() *cloudstack.VirtualMachine {
	return &cloudstack.VirtualMachine{
		Id:                  "vm-1",
		Name:                "node-1",
		Zonename:            "zone-a",
		Serviceofferingname: "Small Instance",
		Nic:                 []cloudstack.Nic{{Ipaddress: "10.0.0.1"}},
	}
}

type instanceLookupOp struct {
	name        string
	call        func(cs *CSCloud, key string) (any, error)
	want        any
	notFound    any
	notFoundErr error
	errPrefix   string
}

var instanceLookupOps = []instanceLookupOp{
	{
		name: "NodeAddresses",
		call: func(cs *CSCloud, key string) (any, error) {
			return cs.NodeAddresses(context.TODO(), types.NodeName(key))
		},
		want:        []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "10.0.0.1"}},
		notFound:    []corev1.NodeAddress(nil),
		notFoundErr: cloudprovider.InstanceNotFound,
		errPrefix:   "error retrieving node addresses",
	},
	{
		name: "InstanceID",
		call: func(cs *CSCloud, key string) (any, error) {
			return cs.InstanceID(context.TODO(), types.NodeName(key))
		},
		want:        "vm-1",
		notFound:    "",
		notFoundErr: cloudprovider.InstanceNotFound,
		errPrefix:   "error retrieving instance ID",
	},
	{
		name: "InstanceType",
		call: func(cs *CSCloud, key string) (any, error) {
			return cs.InstanceType(context.TODO(), types.NodeName(key))
		},
		want:        "SmallInstance",
		notFound:    "",
		notFoundErr: cloudprovider.InstanceNotFound,
		errPrefix:   "error retrieving instance type",
	},
}

var providerIDLookupOps = []instanceLookupOp{
	{
		name: "NodeAddressesByProviderID",
		call: func(cs *CSCloud, key string) (any, error) {
			return cs.NodeAddressesByProviderID(context.TODO(), key)
		},
		want:        []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "10.0.0.1"}},
		notFound:    []corev1.NodeAddress(nil),
		notFoundErr: cloudprovider.InstanceNotFound,
		errPrefix:   "error retrieving node addresses",
	},
	{
		name: "InstanceTypeByProviderID",
		call: func(cs *CSCloud, key string) (any, error) {
			return cs.InstanceTypeByProviderID(context.TODO(), key)
		},
		want:        "SmallInstance",
		notFound:    "",
		notFoundErr: cloudprovider.InstanceNotFound,
		errPrefix:   "error retrieving instance type",
	},
	{
		name: "InstanceExistsByProviderID",
		call: func(cs *CSCloud, key string) (any, error) {
			return cs.InstanceExistsByProviderID(context.TODO(), key)
		},
		want:      true,
		notFound:  false,
		errPrefix: "error retrieving instance",
	},
}

type lookupScenario struct {
	name      string
	projectID string
	result    *cloudstack.VirtualMachine
	count     int
	err       error
}

func lookupScenarios() []lookupScenario {
	return []lookupScenario{
		{name: "found", result: testInstance(), count: 1},
		{name: "found in a project", projectID: testProjectID, result: testInstance(), count: 1},
		{name: "not found", count: 0, err: errors.New("no match")},
		{name: "api error", count: 1, err: errors.New("boom")},
	}
}

func assertLookupOutcome(t *testing.T, op instanceLookupOp, sc lookupScenario, got any, err error) {
	t.Helper()
	switch {
	case sc.err == nil:
		if err != nil {
			t.Fatalf("%s() unexpected error: %v", op.name, err)
		}
		if !reflect.DeepEqual(got, op.want) {
			t.Errorf("%s() = %v, want %v", op.name, got, op.want)
		}
	case sc.count == 0:
		if err != op.notFoundErr {
			t.Errorf("%s() error = %v, want %v", op.name, err, op.notFoundErr)
		}
		if !reflect.DeepEqual(got, op.notFound) {
			t.Errorf("%s() = %v, want %v", op.name, got, op.notFound)
		}
	default:
		if err == nil || !strings.HasPrefix(err.Error(), op.errPrefix) || !strings.Contains(err.Error(), sc.err.Error()) {
			t.Errorf("%s() error = %v, want prefix %q wrapping %q", op.name, err, op.errPrefix, sc.err)
		}
	}
}

// TestInstanceLookupsByName checks that every node-name lookup asks CloudStack for that name in
// the configured project and maps a miss and an API failure to the errors the node controller expects.
func TestInstanceLookupsByName(t *testing.T) {
	for _, op := range instanceLookupOps {
		for _, sc := range lookupScenarios() {
			t.Run(op.name+"/"+sc.name, func(t *testing.T) {
				env := newVMLookupEnv(t, sc.projectID)
				env.expectByName(t, "node-1", sc.result, sc.count, sc.err)

				got, err := op.call(env.cs, "node-1")

				assertLookupOutcome(t, op, sc, got, err)
			})
		}
	}
}

// TestInstanceLookupsByProviderID checks that every provider ID lookup strips the scheme before
// asking CloudStack for the instance and accepts a bare instance ID as well.
func TestInstanceLookupsByProviderID(t *testing.T) {
	providerIDs := map[string]string{
		"with scheme": "external-cloudstack://vm-1",
		"bare id":     "vm-1",
	}

	for _, op := range providerIDLookupOps {
		for _, sc := range lookupScenarios() {
			for idName, providerID := range providerIDs {
				t.Run(op.name+"/"+sc.name+"/"+idName, func(t *testing.T) {
					env := newVMLookupEnv(t, sc.projectID)
					env.expectByID(t, "vm-1", sc.result, sc.count, sc.err)

					got, err := op.call(env.cs, providerID)

					assertLookupOutcome(t, op, sc, got, err)
				})
			}
		}
	}
}

// TestInstanceTypeStripsInvalidLabelCharacters checks that the service offering name is turned
// into a value that is valid in a node label.
func TestInstanceTypeStripsInvalidLabelCharacters(t *testing.T) {
	tests := map[string]string{
		"Small Instance": "SmallInstance",
		"Medium":         "Medium",
		"Big (x2)":       "Bigx2",
	}

	for offering, want := range tests {
		t.Run(offering, func(t *testing.T) {
			env := newVMLookupEnv(t, "")
			env.expectByName(t, "node-1", &cloudstack.VirtualMachine{Serviceofferingname: offering}, 1, nil)

			got, err := env.cs.InstanceType(context.TODO(), "node-1")

			if err != nil || got != want {
				t.Errorf("InstanceType() = %q, %v, want %q", got, err, want)
			}
		})
	}
}

// TestInstanceExists checks that the node is resolved to its ID by name before the existence check.
func TestInstanceExists(t *testing.T) {
	tests := []struct {
		name        string
		byName      func(e *vmLookupEnv, t *testing.T)
		byID        func(e *vmLookupEnv, t *testing.T)
		want        bool
		wantErr     error
		errContains string
	}{
		{
			name:   "instance exists",
			byName: func(e *vmLookupEnv, t *testing.T) { e.expectByName(t, "node-1", testInstance(), 1, nil) },
			byID:   func(e *vmLookupEnv, t *testing.T) { e.expectByID(t, "vm-1", testInstance(), 1, nil) },
			want:   true,
		},
		{
			name:    "node unknown to CloudStack",
			byName:  func(e *vmLookupEnv, t *testing.T) { e.expectByName(t, "node-1", nil, 0, errors.New("no match")) },
			wantErr: cloudprovider.InstanceNotFound,
		},
		{
			name:   "instance deleted after the name lookup",
			byName: func(e *vmLookupEnv, t *testing.T) { e.expectByName(t, "node-1", testInstance(), 1, nil) },
			byID:   func(e *vmLookupEnv, t *testing.T) { e.expectByID(t, "vm-1", nil, 0, errors.New("no match")) },
			want:   false,
		},
		{
			name:        "api error on the ID lookup",
			byName:      func(e *vmLookupEnv, t *testing.T) { e.expectByName(t, "node-1", testInstance(), 1, nil) },
			byID:        func(e *vmLookupEnv, t *testing.T) { e.expectByID(t, "vm-1", nil, 1, errors.New("boom")) },
			errContains: "error retrieving instance: boom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newVMLookupEnv(t, testProjectID)
			tt.byName(env, t)
			if tt.byID != nil {
				tt.byID(env, t)
			}

			got, err := env.cs.InstanceExists(context.TODO(), &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}})

			if got != tt.want {
				t.Errorf("InstanceExists() = %v, want %v", got, tt.want)
			}
			if tt.wantErr != nil && err != tt.wantErr {
				t.Errorf("InstanceExists() error = %v, want %v", err, tt.wantErr)
			}
			if tt.errContains != "" && (err == nil || !strings.Contains(err.Error(), tt.errContains)) {
				t.Errorf("InstanceExists() error = %v, want it to contain %q", err, tt.errContains)
			}
		})
	}
}

// TestInstanceMetadata checks the metadata the node controller sets on a new node and that a
// failure of any of the four lookups behind it is returned instead of a half-filled result.
func TestInstanceMetadata(t *testing.T) {
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}}
	lookups := []string{"InstanceID", "InstanceType", "NodeAddresses", "GetZoneByNodeName"}

	t.Run("all lookups succeed", func(t *testing.T) {
		env := newVMLookupEnv(t, testProjectID)
		env.cs.region = "region-1"
		env.vm.EXPECT().GetVirtualMachineByName(gomock.Any(), gomock.Any()).Times(len(lookups)).
			DoAndReturn(func(name string, opts ...cloudstack.OptionFunc) (*cloudstack.VirtualMachine, int, error) {
				if name != "node-1" || appliedProjectID(t, opts) != testProjectID {
					t.Errorf("lookup of %q in project %q, want node-1 in %q", name, appliedProjectID(t, opts), testProjectID)
				}
				return testInstance(), 1, nil
			})

		got, err := env.cs.InstanceMetadata(context.TODO(), node)
		if err != nil {
			t.Fatalf("InstanceMetadata() unexpected error: %v", err)
		}

		want := &cloudprovider.InstanceMetadata{
			ProviderID:    "external-cloudstack://vm-1",
			InstanceType:  "SmallInstance",
			NodeAddresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "10.0.0.1"}},
			Zone:          "zone-a",
			Region:        "region-1",
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("InstanceMetadata() = %+v, want %+v", got, want)
		}
	})

	for failAt, lookup := range lookups {
		t.Run("failure in "+lookup, func(t *testing.T) {
			env := newVMLookupEnv(t, "")
			calls := 0
			env.vm.EXPECT().GetVirtualMachineByName(gomock.Any(), gomock.Any()).Times(failAt + 1).
				DoAndReturn(func(string, ...cloudstack.OptionFunc) (*cloudstack.VirtualMachine, int, error) {
					calls++
					if calls == failAt+1 {
						return nil, 1, errors.New("boom")
					}
					return testInstance(), 1, nil
				})

			got, err := env.cs.InstanceMetadata(context.TODO(), node)

			if got != nil || err == nil || !strings.Contains(err.Error(), "boom") {
				t.Errorf("InstanceMetadata() = %+v, %v, want nil and an error containing boom", got, err)
			}
		})
	}
}

// TestUnsupportedInstanceOperations checks the operations the provider leaves to Kubernetes.
func TestUnsupportedInstanceOperations(t *testing.T) {
	cs := &CSCloud{}
	node := &corev1.Node{}

	if shutdown, err := cs.InstanceShutdownByProviderID(context.TODO(), "vm-1"); shutdown || err != cloudprovider.NotImplemented {
		t.Errorf("InstanceShutdownByProviderID() = %v, %v, want false, NotImplemented", shutdown, err)
	}
	if shutdown, err := cs.InstanceShutdown(context.TODO(), node); shutdown || err != cloudprovider.NotImplemented {
		t.Errorf("InstanceShutdown() = %v, %v, want false, NotImplemented", shutdown, err)
	}
	if err := cs.AddSSHKeyToAllInstances(context.TODO(), "user", nil); err != cloudprovider.NotImplemented {
		t.Errorf("AddSSHKeyToAllInstances() = %v, want NotImplemented", err)
	}
	if name, err := cs.CurrentNodeName(context.TODO(), "host-1"); err != nil || name != "host-1" {
		t.Errorf("CurrentNodeName() = %q, %v, want host-1", name, err)
	}
}
