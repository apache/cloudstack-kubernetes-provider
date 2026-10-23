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
	"strings"
	"testing"

	"github.com/apache/cloudstack-go/v2/cloudstack"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	restclient "k8s.io/client-go/rest"
	cloudprovider "k8s.io/cloud-provider"
)

type fakeClientBuilder struct {
	client kubernetes.Interface
	err    error
}

func (b *fakeClientBuilder) Config(string) (*restclient.Config, error) { return nil, b.err }
func (b *fakeClientBuilder) ConfigOrDie(string) *restclient.Config     { return nil }
func (b *fakeClientBuilder) Client(string) (kubernetes.Interface, error) {
	return b.client, b.err
}
func (b *fakeClientBuilder) ClientOrDie(string) kubernetes.Interface { return b.client }

var _ cloudprovider.ControllerClientBuilder = &fakeClientBuilder{}

type zoneLookup struct {
	name string
	call func(cs *CSCloud) (cloudprovider.Zone, error)
	// expect registers the one CloudStack lookup the call makes.
	expect func(e *vmLookupEnv, t *testing.T, result *cloudstack.VirtualMachine, count int, err error)
	// notFound is the text of the error for an instance CloudStack does not know.
	notFound string
}

var zoneLookups = []zoneLookup{
	{
		name: "GetZoneByNodeName",
		call: func(cs *CSCloud) (cloudprovider.Zone, error) {
			return cs.GetZoneByNodeName(context.TODO(), "node-1")
		},
		expect: func(e *vmLookupEnv, t *testing.T, result *cloudstack.VirtualMachine, count int, err error) {
			e.expectByName(t, "node-1", result, count, err)
		},
		notFound: "could not find node: node-1",
	},
	{
		name: "GetZoneByProviderID",
		call: func(cs *CSCloud) (cloudprovider.Zone, error) {
			return cs.GetZoneByProviderID(context.TODO(), "external-cloudstack://vm-1")
		},
		expect: func(e *vmLookupEnv, t *testing.T, result *cloudstack.VirtualMachine, count int, err error) {
			e.expectByID(t, "vm-1", result, count, err)
		},
		notFound: "could not find node by ID: external-cloudstack://vm-1",
	},
}

// TestZoneLookups checks that a zone is resolved from the instance's zone name in the configured
// project, that the region is the configured one or else the zone name, and that failures read
// differently for a missing instance and an API error.
func TestZoneLookups(t *testing.T) {
	for _, lookup := range zoneLookups {
		t.Run(lookup.name+"/region falls back to the zone", func(t *testing.T) {
			env := newVMLookupEnv(t, "")
			lookup.expect(env, t, testInstance(), 1, nil)

			zone, err := lookup.call(env.cs)

			if err != nil || zone.FailureDomain != "zone-a" || zone.Region != "zone-a" {
				t.Errorf("%s() = %+v, %v, want zone-a in region zone-a", lookup.name, zone, err)
			}
		})

		t.Run(lookup.name+"/configured region in a project", func(t *testing.T) {
			env := newVMLookupEnv(t, testProjectID)
			env.cs.region = "region-1"
			lookup.expect(env, t, testInstance(), 1, nil)

			zone, err := lookup.call(env.cs)

			if err != nil || zone.FailureDomain != "zone-a" || zone.Region != "region-1" {
				t.Errorf("%s() = %+v, %v, want zone-a in region region-1", lookup.name, zone, err)
			}
		})

		t.Run(lookup.name+"/instance not found", func(t *testing.T) {
			env := newVMLookupEnv(t, "")
			lookup.expect(env, t, nil, 0, errors.New("no match"))

			_, err := lookup.call(env.cs)

			if err == nil || err.Error() != lookup.notFound {
				t.Errorf("%s() error = %v, want %q", lookup.name, err, lookup.notFound)
			}
		})

		t.Run(lookup.name+"/api error", func(t *testing.T) {
			env := newVMLookupEnv(t, "")
			lookup.expect(env, t, nil, 1, errors.New("boom"))

			_, err := lookup.call(env.cs)

			if err == nil || err.Error() != "error retrieving zone: boom" {
				t.Errorf("%s() error = %v, want %q", lookup.name, err, "error retrieving zone: boom")
			}
		})
	}
}

func podNamed(name, namespace, nodeName string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec:       corev1.PodSpec{NodeName: nodeName},
	}
}

func setPodEnvironment(t *testing.T, podName, namespace string) {
	t.Helper()
	t.Setenv("HOSTNAME", podName)
	t.Setenv("POD_NAMESPACE", namespace)
}

// TestGetNodeNameFromPod checks that the node is read from the CCM's own pod and that every way
// that can fail is reported.
func TestGetNodeNameFromPod(t *testing.T) {
	setPodEnvironment(t, "ccm-pod", "ccm-ns")

	tests := []struct {
		name      string
		builder   *fakeClientBuilder
		want      string
		errPrefix string
	}{
		{
			name:      "no client builder",
			errPrefix: "clientBuilder not initialized",
		},
		{
			name:      "client cannot be built",
			builder:   &fakeClientBuilder{err: errors.New("no config")},
			errPrefix: "failed to get Kubernetes client: no config",
		},
		{
			name:      "pod does not exist",
			builder:   &fakeClientBuilder{client: fake.NewSimpleClientset()},
			errPrefix: "failed to get pod ccm-ns/ccm-pod from Kubernetes API",
		},
		{
			name:      "pod not scheduled yet",
			builder:   &fakeClientBuilder{client: fake.NewSimpleClientset(podNamed("ccm-pod", "ccm-ns", ""))},
			errPrefix: "pod ccm-ns/ccm-pod does not have a nodeName assigned yet",
		},
		{
			name:    "pod scheduled",
			builder: &fakeClientBuilder{client: fake.NewSimpleClientset(podNamed("ccm-pod", "ccm-ns", "node-1"))},
			want:    "node-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := &CSCloud{}
			if tt.builder != nil {
				cs.clientBuilder = tt.builder
			}

			got, err := cs.getNodeNameFromPod(context.TODO())

			if tt.errPrefix != "" {
				if err == nil || !strings.HasPrefix(err.Error(), tt.errPrefix) {
					t.Errorf("getNodeNameFromPod() error = %v, want prefix %q", err, tt.errPrefix)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("getNodeNameFromPod() = %q, %v, want %q", got, err, tt.want)
			}
		})
	}
}

// TestGetZone checks that the zone is looked up once through the pod's node and then served from the cache.
func TestGetZone(t *testing.T) {
	t.Run("cached zone makes no lookup", func(t *testing.T) {
		env := newVMLookupEnv(t, "")
		env.cs.zone = "zone-a"
		env.cs.region = "region-1"

		zone, err := env.cs.GetZone(context.TODO())

		if err != nil || zone.FailureDomain != "zone-a" || zone.Region != "region-1" {
			t.Errorf("GetZone() = %+v, %v, want zone-a in region region-1", zone, err)
		}
	})

	t.Run("zone of the pod's node is looked up once", func(t *testing.T) {
		setPodEnvironment(t, "ccm-pod", "ccm-ns")
		env := newVMLookupEnv(t, testProjectID)
		env.cs.clientBuilder = &fakeClientBuilder{client: fake.NewSimpleClientset(podNamed("ccm-pod", "ccm-ns", "node-1"))}
		env.expectByName(t, "node-1", testInstance(), 1, nil).Times(1)

		for i := 0; i < 2; i++ {
			zone, err := env.cs.GetZone(context.TODO())
			if err != nil || zone.FailureDomain != "zone-a" || zone.Region != "zone-a" {
				t.Fatalf("GetZone() call %d = %+v, %v, want zone-a in region zone-a", i+1, zone, err)
			}
		}
	})

	failures := []struct {
		name      string
		builder   cloudprovider.ControllerClientBuilder
		result    *cloudstack.VirtualMachine
		count     int
		err       error
		errPrefix string
	}{
		{
			name:      "node of the pod unknown",
			errPrefix: "failed to get node name for retrieving the zone",
		},
		{
			name:      "instance not found",
			builder:   &fakeClientBuilder{client: fake.NewSimpleClientset(podNamed("ccm-pod", "ccm-ns", "node-1"))},
			count:     0,
			err:       errors.New("no match"),
			errPrefix: "could not find CloudStack instance with name node-1 for retrieving the zone",
		},
		{
			name:      "api error",
			builder:   &fakeClientBuilder{client: fake.NewSimpleClientset(podNamed("ccm-pod", "ccm-ns", "node-1"))},
			count:     1,
			err:       errors.New("boom"),
			errPrefix: "error getting instance for retrieving the zone",
		},
	}

	for _, tt := range failures {
		t.Run(tt.name, func(t *testing.T) {
			setPodEnvironment(t, "ccm-pod", "ccm-ns")
			env := newVMLookupEnv(t, "")
			env.cs.clientBuilder = tt.builder
			if tt.err != nil {
				env.expectByName(t, "node-1", tt.result, tt.count, tt.err)
			}

			_, err := env.cs.GetZone(context.TODO())

			if err == nil || !strings.HasPrefix(err.Error(), tt.errPrefix) {
				t.Errorf("GetZone() error = %v, want prefix %q", err, tt.errPrefix)
			}
		})
	}
}
