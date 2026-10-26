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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

const annotationKey = "service.beta.kubernetes.io/cloudstack-load-balancer-ip-associated-by-controller"

var servicesResource = schema.GroupResource{Resource: "services"}

func annotatedService(annotations map[string]string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default", Annotations: annotations},
	}
}

func patchActions(client *fake.Clientset) []k8stesting.PatchAction {
	var patches []k8stesting.PatchAction
	for _, action := range client.Actions() {
		if patch, ok := action.(k8stesting.PatchAction); ok {
			patches = append(patches, patch)
		}
	}
	return patches
}

func failPatches(client *fake.Clientset, results ...error) {
	attempt := 0
	client.PrependReactor("patch", "services", func(k8stesting.Action) (bool, runtime.Object, error) {
		err := results[min(attempt, len(results)-1)]
		attempt++
		return err != nil, nil, err
	})
}

// TestSetServiceAnnotation checks when an annotation is patched and that only the one key is sent.
func TestSetServiceAnnotation(t *testing.T) {
	t.Run("no client builder skips the update", func(t *testing.T) {
		cs := &CSCloud{}

		if err := cs.setServiceAnnotation(context.TODO(), annotatedService(nil), annotationKey, "true"); err != nil {
			t.Errorf("setServiceAnnotation() unexpected error: %v", err)
		}
	})

	t.Run("client cannot be built", func(t *testing.T) {
		cs := &CSCloud{clientBuilder: &fakeClientBuilder{err: errors.New("no config")}}

		err := cs.setServiceAnnotation(context.TODO(), annotatedService(nil), annotationKey, "true")

		if err == nil || !strings.Contains(err.Error(), "failed to get Kubernetes client: no config") {
			t.Errorf("setServiceAnnotation() error = %v, want the client error", err)
		}
	})

	t.Run("missing service is skipped", func(t *testing.T) {
		client := fake.NewSimpleClientset()
		cs := &CSCloud{clientBuilder: &fakeClientBuilder{client: client}}

		if err := cs.setServiceAnnotation(context.TODO(), annotatedService(nil), annotationKey, "true"); err != nil {
			t.Errorf("setServiceAnnotation() unexpected error: %v", err)
		}
		if got := len(patchActions(client)); got != 0 {
			t.Errorf("got %d patches, want none", got)
		}
	})

	t.Run("service lookup fails", func(t *testing.T) {
		client := fake.NewSimpleClientset()
		client.PrependReactor("get", "services", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("boom")
		})
		cs := &CSCloud{clientBuilder: &fakeClientBuilder{client: client}}

		err := cs.setServiceAnnotation(context.TODO(), annotatedService(nil), annotationKey, "true")

		if err == nil || !strings.Contains(err.Error(), "failed to get service: boom") {
			t.Errorf("setServiceAnnotation() error = %v, want the lookup error", err)
		}
	})

	t.Run("annotation already set is not patched", func(t *testing.T) {
		client := fake.NewSimpleClientset(annotatedService(map[string]string{annotationKey: "true"}))
		cs := &CSCloud{clientBuilder: &fakeClientBuilder{client: client}}

		if err := cs.setServiceAnnotation(context.TODO(), annotatedService(nil), annotationKey, "true"); err != nil {
			t.Errorf("setServiceAnnotation() unexpected error: %v", err)
		}
		if got := len(patchActions(client)); got != 0 {
			t.Errorf("got %d patches, want none", got)
		}
	})

	t.Run("different value is merge patched", func(t *testing.T) {
		client := fake.NewSimpleClientset(annotatedService(map[string]string{annotationKey: "false", "other": "kept"}))
		cs := &CSCloud{clientBuilder: &fakeClientBuilder{client: client}}

		if err := cs.setServiceAnnotation(context.TODO(), annotatedService(nil), annotationKey, "true"); err != nil {
			t.Fatalf("setServiceAnnotation() unexpected error: %v", err)
		}

		patches := patchActions(client)
		if len(patches) != 1 {
			t.Fatalf("got %d patches, want 1", len(patches))
		}
		wantPatch := `{"metadata":{"annotations":{"` + annotationKey + `":"true"}}}`
		if patches[0].GetName() != "web" || patches[0].GetNamespace() != "default" ||
			patches[0].GetPatchType() != types.MergePatchType || string(patches[0].GetPatch()) != wantPatch {
			t.Errorf("patch = %s %s/%s %s, want a merge patch of web %s", patches[0].GetPatchType(), patches[0].GetNamespace(), patches[0].GetName(), patches[0].GetPatch(), wantPatch)
		}
		svc, err := client.CoreV1().Services("default").Get(context.TODO(), "web", metav1.GetOptions{})
		if err != nil || svc.Annotations[annotationKey] != "true" || svc.Annotations["other"] != "kept" {
			t.Errorf("annotations after the patch = %v, %v, want the key updated and the other one kept", svc.Annotations, err)
		}
	})
}

// TestPatchServiceAnnotation checks how a failing patch is retried or given up on.
func TestPatchServiceAnnotation(t *testing.T) {
	conflict := apierrors.NewConflict(servicesResource, "web", errors.New("changed"))
	tests := []struct {
		name        string
		results     []error
		wantPatches int
		wantErr     string
	}{
		{name: "first attempt succeeds", results: []error{nil}, wantPatches: 1},
		{name: "conflict then success", results: []error{conflict, nil}, wantPatches: 2},
		{name: "conflicts exhaust the retries", results: []error{conflict}, wantPatches: 3, wantErr: "failed to update service annotation after 3 retries due to conflicts"},
		{name: "service deleted meanwhile", results: []error{apierrors.NewNotFound(servicesResource, "web")}, wantPatches: 1},
		{name: "other error is not retried", results: []error{errors.New("boom")}, wantPatches: 1, wantErr: "failed to patch service annotation: boom"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := fake.NewSimpleClientset(annotatedService(nil))
			failPatches(client, tt.results...)
			cs := &CSCloud{}

			err := cs.patchServiceAnnotation(context.TODO(), client, "default", "web", annotationKey, "true")

			if tt.wantErr == "" && err != nil {
				t.Errorf("patchServiceAnnotation() unexpected error: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Errorf("patchServiceAnnotation() error = %v, want it to contain %q", err, tt.wantErr)
			}
			if got := len(patchActions(client)); got != tt.wantPatches {
				t.Errorf("got %d patch attempts, want %d", got, tt.wantPatches)
			}
		})
	}
}
