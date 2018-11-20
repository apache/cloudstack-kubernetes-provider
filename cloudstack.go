/*
 * Copyright 2018 SWISS TXT AG
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     https://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package cloudstack

import (
	"io"

	"k8s.io/klog"

	cloudprovider "k8s.io/cloud-provider"
)

type CloudstackProvider struct {
}

const (
	cloudstackProviderName string = "custom-cloudstack"
)

func init() {
	cloudprovider.RegisterCloudProvider(cloudstackProviderName, func(r io.Reader) (cloudprovider.Interface, error) {
		klog.Infof("Creating Cloudstack provider from config %v", r)
		return &CloudstackProvider{}, nil
	})
}

// Initialize provides the cloud with a kubernetes client builder and may spawn goroutines
// to perform housekeeping or run custom controllers specific to the cloud provider.
// Any tasks started here should be cleaned up when the stop channel closes.
func (p *CloudstackProvider) Initialize(clientBuilder cloudprovider.ControllerClientBuilder, stop <-chan struct{}) {
	klog.Infof("Initializing Cloudstack provider")
	// initialize

	go func() {
		<-stop
		// cleanup

	}()
}

// LoadBalancer returns a balancer interface. Also returns true if the interface is supported, false otherwise.
func (p *CloudstackProvider) LoadBalancer() (cloudprovider.LoadBalancer, bool) {
	klog.Infof("Getting LoadBalancers from Cloudstack provider")
	return nil, false
}

// Instances returns an instances interface. Also returns true if the interface is supported, false otherwise.
func (p *CloudstackProvider) Instances() (cloudprovider.Instances, bool) {
	klog.Infof("Getting Instances from Cloudstack provider")
	return nil, false
}

// Zones returns a zones interface. Also returns true if the interface is supported, false otherwise.
func (p *CloudstackProvider) Zones() (cloudprovider.Zones, bool) {
	klog.Infof("Getting Zones from Cloudstack provider")
	return nil, false
}

// Clusters returns a clusters interface.  Also returns true if the interface is supported, false otherwise.
func (p *CloudstackProvider) Clusters() (cloudprovider.Clusters, bool) {
	klog.Infof("Getting Clusters from Cloudstack provider")
	return nil, false
}

// Routes returns a routes interface along with whether the interface is supported.
func (p *CloudstackProvider) Routes() (cloudprovider.Routes, bool) {
	klog.Infof("Getting Routes from Cloudstack provider")
	return nil, false
}

// ProviderName returns the cloud provider ID.
func (p *CloudstackProvider) ProviderName() string {
	return cloudstackProviderName
}

// HasClusterID returns true if a ClusterID is required and set
func (p *CloudstackProvider) HasClusterID() bool {
	return false
}
