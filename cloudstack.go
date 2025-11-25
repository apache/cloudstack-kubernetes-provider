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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/apache/cloudstack-go/v2/cloudstack"
	"github.com/blang/semver/v4"
	"gopkg.in/gcfg.v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	cloudprovider "k8s.io/cloud-provider"
	"k8s.io/klog/v2"
)

// ProviderName is the name of this cloud provider.
const ProviderName = "external-cloudstack"

// CSConfig wraps the config for the CloudStack cloud provider.
type CSConfig struct {
	Global struct {
		APIURL      string `gcfg:"api-url"`
		APIKey      string `gcfg:"api-key"`
		SecretKey   string `gcfg:"secret-key"`
		SSLNoVerify bool   `gcfg:"ssl-no-verify"`
		ProjectID   string `gcfg:"project-id"`
		Zone        string `gcfg:"zone"`
	}
}

// CSCloud is an implementation of Interface for CloudStack.
type CSCloud struct {
	client        *cloudstack.CloudStackClient
	projectID     string // If non-"", all resources will be created within this project
	zone          string
	version       semver.Version
	clientBuilder cloudprovider.ControllerClientBuilder
}

func init() {
	cloudprovider.RegisterCloudProvider(ProviderName, func(config io.Reader) (cloudprovider.Interface, error) {
		cfg, err := readConfig(config)
		if err != nil {
			return nil, err
		}

		return newCSCloud(cfg)
	})
}

func readConfig(config io.Reader) (*CSConfig, error) {
	cfg := &CSConfig{}

	if config == nil {
		return cfg, nil
	}

	if err := gcfg.ReadInto(cfg, config); err != nil {
		return nil, fmt.Errorf("could not parse cloud provider config: %v", err)
	}

	return cfg, nil
}

// newCSCloud creates a new instance of CSCloud.
func newCSCloud(cfg *CSConfig) (*CSCloud, error) {
	cs := &CSCloud{
		projectID: cfg.Global.ProjectID,
		zone:      cfg.Global.Zone,
		version:   semver.Version{},
	}

	if cfg.Global.APIURL != "" && cfg.Global.APIKey != "" && cfg.Global.SecretKey != "" {
		cs.client = cloudstack.NewAsyncClient(cfg.Global.APIURL, cfg.Global.APIKey, cfg.Global.SecretKey, !cfg.Global.SSLNoVerify)
	}

	if cs.client == nil {
		return nil, errors.New("no cloud provider config given")
	}

	version, err := cs.getManagementServerVersion()
	if err != nil {
		return nil, err
	}
	cs.version = version

	return cs, nil
}

func (cs *CSCloud) getManagementServerVersion() (semver.Version, error) {
	msServersResp, err := cs.client.Management.ListManagementServersMetrics(cs.client.Management.NewListManagementServersMetricsParams())
	if err != nil {
		return semver.Version{}, err
	}
	if msServersResp.Count == 0 {
		return semver.Version{}, errors.New("no management servers found")
	}
	version := msServersResp.ManagementServersMetrics[0].Version
	v, err := semver.ParseTolerant(strings.Join(strings.Split(version, ".")[0:3], "."))
	if err != nil {
		klog.Errorf("failed to parse management server version: %v", err)
		return semver.Version{}, err
	}
	return v, nil
}

// Initialize passes a Kubernetes clientBuilder interface to the cloud provider
func (cs *CSCloud) Initialize(clientBuilder cloudprovider.ControllerClientBuilder, stop <-chan struct{}) {
	cs.clientBuilder = clientBuilder
}

// LoadBalancer returns an implementation of LoadBalancer for CloudStack.
func (cs *CSCloud) LoadBalancer() (cloudprovider.LoadBalancer, bool) {
	if cs.client == nil {
		return nil, false
	}

	return cs, true
}

// Instances returns an implementation of Instances for CloudStack.
func (cs *CSCloud) Instances() (cloudprovider.Instances, bool) {
	if cs.client == nil {
		return nil, false
	}

	return cs, true
}

func (cs *CSCloud) InstancesV2() (cloudprovider.InstancesV2, bool) {
	if cs.client == nil {
		return nil, false
	}

	return cs, true
}

// Zones returns an implementation of Zones for CloudStack.
func (cs *CSCloud) Zones() (cloudprovider.Zones, bool) {
	if cs.client == nil {
		return nil, false
	}

	return cs, true
}

// Clusters returns an implementation of Clusters for CloudStack.
func (cs *CSCloud) Clusters() (cloudprovider.Clusters, bool) {
	if cs.client == nil {
		return nil, false
	}

	klog.Warning("This cloud provider doesn't support clusters")
	return nil, false
}

// Routes returns an implementation of Routes for CloudStack.
func (cs *CSCloud) Routes() (cloudprovider.Routes, bool) {
	if cs.client == nil {
		return nil, false
	}

	klog.Warning("This cloud provider doesn't support routes")
	return nil, false
}

// ProviderName returns the cloud provider ID.
func (cs *CSCloud) ProviderName() string {
	return ProviderName
}

// HasClusterID returns true if the cluster has a clusterID
func (cs *CSCloud) HasClusterID() bool {
	return true
}

// GetZone returns the Zone containing the region that the program is running in.
func (cs *CSCloud) GetZone(ctx context.Context) (cloudprovider.Zone, error) {
	zone := cloudprovider.Zone{}

	if cs.zone == "" {
		// In Kubernetes pods, os.Hostname() returns the pod name, not the node hostname.
		// We need to get the node name from the pod's spec.nodeName using the Kubernetes API.
		nodeName, err := cs.getNodeNameFromPod(ctx)
		if err != nil {
			return zone, fmt.Errorf("failed to get node name for retrieving the zone: %v", err)
		}

		instance, count, err := cs.client.VirtualMachine.GetVirtualMachineByName(
			nodeName,
			cloudstack.WithProject(cs.projectID),
		)
		if err != nil {
			if count == 0 {
				return zone, fmt.Errorf("could not find CloudStack instance with name %s for retrieving the zone: %v", nodeName, err)
			}
			return zone, fmt.Errorf("error getting instance for retrieving the zone: %v", err)
		}

		cs.zone = instance.Zonename
	}

	klog.V(2).Infof("Current zone is %v", cs.zone)
	zone.FailureDomain = cs.zone
	zone.Region = cs.zone

	return zone, nil
}

// GetZoneByProviderID returns the Zone, found by using the provider ID.
func (cs *CSCloud) GetZoneByProviderID(ctx context.Context, providerID string) (cloudprovider.Zone, error) {
	zone := cloudprovider.Zone{}

	instance, count, err := cs.client.VirtualMachine.GetVirtualMachineByID(
		cs.getInstanceIDFromProviderID(providerID),
		cloudstack.WithProject(cs.projectID),
	)
	if err != nil {
		if count == 0 {
			return zone, fmt.Errorf("could not find node by ID: %v", providerID)
		}
		return zone, fmt.Errorf("error retrieving zone: %v", err)
	}

	klog.V(2).Infof("Current zone is %v", cs.zone)
	zone.FailureDomain = instance.Zonename
	zone.Region = instance.Zonename

	return zone, nil
}

// GetZoneByNodeName returns the Zone, found by using the node name.
func (cs *CSCloud) GetZoneByNodeName(ctx context.Context, nodeName types.NodeName) (cloudprovider.Zone, error) {
	zone := cloudprovider.Zone{}

	instance, count, err := cs.client.VirtualMachine.GetVirtualMachineByName(
		string(nodeName),
		cloudstack.WithProject(cs.projectID),
	)
	if err != nil {
		if count == 0 {
			return zone, fmt.Errorf("could not find node: %v", nodeName)
		}
		return zone, fmt.Errorf("error retrieving zone: %v", err)
	}

	klog.V(2).Infof("Current zone is %v", cs.zone)
	zone.FailureDomain = instance.Zonename
	zone.Region = instance.Zonename

	return zone, nil
}

// getNodeNameFromPod gets the node name where this pod is running by querying the Kubernetes API.
// It uses the pod's name and namespace (from environment variables or hostname) to look up the pod
// and retrieve its spec.nodeName field.
func (cs *CSCloud) getNodeNameFromPod(ctx context.Context) (string, error) {
	if cs.clientBuilder == nil {
		return "", fmt.Errorf("clientBuilder not initialized, cannot query Kubernetes API")
	}

	client, err := cs.clientBuilder.Client("cloud-controller-manager")
	if err != nil {
		return "", fmt.Errorf("failed to get Kubernetes client: %v", err)
	}

	// Get pod name and namespace
	// In Kubernetes, the pod name is available as HOSTNAME environment variable
	// or we can use os.Hostname() which returns the pod name
	podName := os.Getenv("HOSTNAME")
	if podName == "" {
		var err error
		podName, err = os.Hostname()
		if err != nil {
			return "", fmt.Errorf("failed to get pod name: %v", err)
		}
	}

	// Get namespace from environment variable or default to kube-system for CCM
	namespace := os.Getenv("POD_NAMESPACE")
	if namespace == "" {
		// Try reading from service account namespace file (available in pods)
		if data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
			namespace = string(data)
		} else {
			// Default namespace for cloud controller manager
			namespace = "kube-system"
		}
	}

	// Get the pod object from Kubernetes API
	pod, err := client.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get pod %s/%s from Kubernetes API: %v", namespace, podName, err)
	}

	if pod.Spec.NodeName == "" {
		return "", fmt.Errorf("pod %s/%s does not have a nodeName assigned yet", namespace, podName)
	}

	klog.V(4).Infof("found node name %s for pod %s/%s", pod.Spec.NodeName, namespace, podName)
	return pod.Spec.NodeName, nil
}

// setServiceAnnotation updates a service annotation using the Kubernetes client.
// It uses a patch operation with retry logic to handle concurrent updates safely.
func (cs *CSCloud) setServiceAnnotation(ctx context.Context, service *corev1.Service, key, value string) error {
	if cs.clientBuilder == nil {
		klog.V(4).Infof("Client builder not available, skipping annotation update for service %s/%s", service.Namespace, service.Name)
		return nil
	}

	client, err := cs.clientBuilder.Client("cloud-controller-manager")
	if err != nil {
		return fmt.Errorf("failed to get Kubernetes client: %v", err)
	}

	// First, check if the annotation already has the correct value to avoid unnecessary updates
	svc, err := client.CoreV1().Services(service.Namespace).Get(ctx, service.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			klog.V(4).Infof("Service %s/%s not found, skipping annotation update", service.Namespace, service.Name)
			return nil
		}
		return fmt.Errorf("failed to get service: %v", err)
	}

	// Check if annotation already has the correct value
	if svc.Annotations != nil {
		if existingValue, exists := svc.Annotations[key]; exists && existingValue == value {
			klog.V(4).Infof("Annotation %s already set to %s for service %s/%s", key, value, service.Namespace, service.Name)
			return nil
		}
	}

	// Use patch operation with retry logic to handle concurrent updates
	return cs.patchServiceAnnotation(ctx, client, service.Namespace, service.Name, key, value)
}

// patchServiceAnnotation patches a service annotation using a JSON merge patch with retry logic.
// This method handles concurrent updates safely by retrying on conflicts.
func (cs *CSCloud) patchServiceAnnotation(ctx context.Context, client kubernetes.Interface, namespace, name, key, value string) error {
	const maxRetries = 3
	const retryDelay = 500 * time.Millisecond

	// Prepare the patch payload - merge patch that updates only the specific annotation
	// JSON merge patch will preserve other annotations while updating/adding this one
	patchData := map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": map[string]string{
				key: value,
			},
		},
	}

	patchBytes, err := json.Marshal(patchData)
	if err != nil {
		return fmt.Errorf("failed to marshal patch data: %v", err)
	}

	for attempt := 0; attempt < maxRetries; attempt++ {
		// Apply the patch using JSON merge patch type
		// This is atomic and avoids race conditions by merging with existing annotations
		_, err = client.CoreV1().Services(namespace).Patch(
			ctx,
			name,
			types.MergePatchType,
			patchBytes,
			metav1.PatchOptions{},
		)

		if err == nil {
			klog.V(4).Infof("Successfully set annotation %s=%s on service %s/%s", key, value, namespace, name)
			return nil
		}

		// Handle conflict errors with retry logic
		if apierrors.IsConflict(err) {
			if attempt < maxRetries-1 {
				klog.V(4).Infof("Conflict updating service %s/%s annotation, retrying (attempt %d/%d): %v", namespace, name, attempt+1, maxRetries, err)
				time.Sleep(retryDelay)
				continue
			}
			return fmt.Errorf("failed to update service annotation after %d retries due to conflicts: %v", maxRetries, err)
		}

		// Handle not found errors
		if apierrors.IsNotFound(err) {
			klog.V(4).Infof("Service %s/%s not found during patch, skipping annotation update", namespace, name)
			return nil
		}

		// For other errors, return immediately
		return fmt.Errorf("failed to patch service annotation: %v", err)
	}

	return fmt.Errorf("failed to update service annotation after %d attempts", maxRetries)
}
