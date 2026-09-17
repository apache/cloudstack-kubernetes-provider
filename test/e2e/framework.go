//go:build e2e

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

// Package e2e contains end-to-end tests that run against a live Kubernetes
// cluster whose cloud-controller-manager talks to a CloudStack management
// server (normally the simulator brought up by hack/e2e/up.sh).
//
// Configuration comes from the environment:
//
//	KUBECONFIG     kubeconfig of the cluster under test
//	CS_API_URL     CloudStack API endpoint (as reachable from the test process)
//	CS_API_KEY     CloudStack API key
//	CS_SECRET_KEY  CloudStack secret key
//	CS_PROJECT_ID  optional project scoping (set for the VPC phase)
//
// When any required variable is missing, the tests skip.
package e2e

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/apache/cloudstack-go/v2/cloudstack"
	"github.com/blang/semver/v4"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	lbSyncTimeout  = 3 * time.Minute
	lbSyncInterval = 3 * time.Second
)

// Framework bundles the clients and helpers shared by all e2e tests.
type Framework struct {
	T         *testing.T
	K8s       kubernetes.Interface
	CS        *cloudstack.CloudStackClient
	Namespace string
	ProjectID string
	Version   semver.Version
}

// NewFramework builds clients from the environment, skipping the test when
// the environment is not configured. It creates a per-test namespace that is
// deleted on cleanup.
func NewFramework(t *testing.T) *Framework {
	t.Helper()

	apiURL := os.Getenv("CS_API_URL")
	apiKey := os.Getenv("CS_API_KEY")
	secretKey := os.Getenv("CS_SECRET_KEY")
	if apiURL == "" || apiKey == "" || secretKey == "" {
		t.Skip("CS_API_URL/CS_API_KEY/CS_SECRET_KEY not set; skipping e2e test")
	}

	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		t.Skip("KUBECONFIG not set; skipping e2e test")
	}
	restCfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		t.Fatalf("building kubeconfig: %v", err)
	}
	k8s, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		t.Fatalf("building kubernetes client: %v", err)
	}

	verifySSL := true
	if noVerify, err := strconv.ParseBool(os.Getenv("CS_SSL_NO_VERIFY")); err == nil {
		verifySSL = !noVerify
	}
	cs := cloudstack.NewAsyncClient(apiURL, apiKey, secretKey, verifySSL)

	f := &Framework{
		T:         t,
		K8s:       k8s,
		CS:        cs,
		ProjectID: os.Getenv("CS_PROJECT_ID"),
	}
	f.Version = f.managementServerVersion()
	f.Namespace = f.createNamespace()
	return f
}

func (f *Framework) managementServerVersion() semver.Version {
	f.T.Helper()
	resp, err := f.CS.Management.ListManagementServersMetrics(
		f.CS.Management.NewListManagementServersMetricsParams())
	if err != nil {
		f.T.Fatalf("listing management servers: %v", err)
	}
	if resp.Count == 0 {
		f.T.Fatal("no management servers found")
	}
	raw := majorMinorPatch(resp.ManagementServersMetrics[0].Version)
	v, err := semver.ParseTolerant(raw)
	if err != nil {
		f.T.Fatalf("parsing management server version %q: %v", raw, err)
	}
	return v
}

// majorMinorPatch trims a CloudStack version such as "4.22.1.0" to its first
// three components without panicking on a shorter string.
func majorMinorPatch(version string) string {
	parts := strings.Split(version, ".")
	if len(parts) > 3 {
		parts = parts[:3]
	}
	return strings.Join(parts, ".")
}

func (f *Framework) createNamespace() string {
	f.T.Helper()
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		f.T.Fatalf("generating namespace suffix: %v", err)
	}
	name := "ccm-e2e-" + hex.EncodeToString(buf)
	_, err := f.K8s.CoreV1().Namespaces().Create(context.Background(),
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}, metav1.CreateOptions{})
	if err != nil {
		f.T.Fatalf("creating namespace %s: %v", name, err)
	}
	f.T.Cleanup(func() {
		err := f.K8s.CoreV1().Namespaces().Delete(
			context.Background(), name, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			// Deletion is asynchronous and the namespace is reaped regardless, so this only warns.
			f.T.Logf("warning: deleting namespace %s: %v", name, err)
		}
	})
	return name
}

// Eventually polls cond until it returns true or the timeout elapses.
func (f *Framework) Eventually(timeout, interval time.Duration, desc string, cond func() (bool, error)) {
	f.T.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		ok, err := cond()
		lastErr = err
		if ok {
			return
		}
		time.Sleep(interval)
	}
	if lastErr != nil {
		f.T.Fatalf("timed out after %s waiting for %s; last error: %v", timeout, desc, lastErr)
	}
	f.T.Fatalf("timed out after %s waiting for %s; the condition was evaluated "+
		"without error but never became true", timeout, desc)
}

// CreateLBService creates a LoadBalancer service in the test namespace and
// registers cleanup that deletes it and waits for the CloudStack rules to
// disappear, failing the test if they do not. Later tests share this
// simulator and its public IP pool, so a leaked rule has to be reported here
// rather than left to surface as an unrelated failure downstream.
func (f *Framework) CreateLBService(mutate func(*corev1.Service)) *corev1.Service {
	f.T.Helper()
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2e",
			Namespace: f.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeLoadBalancer,
			Selector: map[string]string{"app": "e2e"},
			Ports: []corev1.ServicePort{
				{Name: "http", Port: 80, Protocol: corev1.ProtocolTCP},
			},
		},
	}
	if mutate != nil {
		mutate(svc)
	}
	created, err := f.K8s.CoreV1().Services(f.Namespace).Create(
		context.Background(), svc, metav1.CreateOptions{})
	if err != nil {
		f.T.Fatalf("creating service: %v", err)
	}
	f.T.Cleanup(func() { f.DeleteServiceAndWait(created) })
	return created
}

// DeleteServiceAndWait deletes the service if it still exists, then waits for
// its CloudStack load balancer rules to be cleaned up and its public IP to be
// released. The wait runs even when the service was already gone, because the
// Kubernetes object and the CloudStack rules are torn down asynchronously.
func (f *Framework) DeleteServiceAndWait(svc *corev1.Service) {
	f.T.Helper()
	ingressIP := f.serviceIngressIP(svc)

	err := f.K8s.CoreV1().Services(svc.Namespace).Delete(
		context.Background(), svc.Name, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		f.T.Fatalf("deleting service %s/%s: %v", svc.Namespace, svc.Name, err)
	}

	if !f.waitForLBRulesGone(defaultLoadBalancerName(svc)) {
		return
	}
	f.settlePublicIP(ingressIP, svc)
}

// waitForLBRulesGone reports whether the rules for lbName disappeared within
// the sync timeout. It fails with Errorf rather than Fatalf because it usually
// runs from t.Cleanup and the remaining cleanups still need to run.
func (f *Framework) waitForLBRulesGone(lbName string) bool {
	f.T.Helper()
	deadline := time.Now().Add(lbSyncTimeout)
	var lastErr error
	for time.Now().Before(deadline) {
		rules, err := f.LBRules(lbName)
		lastErr = err
		if err == nil && len(rules) == 0 {
			return true
		}
		time.Sleep(lbSyncInterval)
	}
	f.T.Errorf("load balancer rules for %s were not cleaned up within %s "+
		"(last list error: %v)", lbName, lbSyncTimeout, lastErr)
	return false
}

// settlePublicIP waits for a deleted service's public IP to leave the allocated
// state, so the next test cannot recycle an IP whose previous owner is still
// tearing down. It is a courtesy to the following test rather than an assertion
// about this one, so a timeout only logs.
func (f *Framework) settlePublicIP(ingressIP string, svc *corev1.Service) {
	if ingressIP == "" {
		return
	}
	deadline := time.Now().Add(lbSyncTimeout)
	for time.Now().Before(deadline) {
		ip, err := f.PublicIPByAddress(ingressIP)
		if err == nil && (ip == nil || ip.Allocated == "") {
			return
		}
		time.Sleep(lbSyncInterval)
	}
	f.T.Logf("warning: public IP %s was not released within %s after deleting %s/%s",
		ingressIP, lbSyncTimeout, svc.Namespace, svc.Name)
}

// serviceIngressIP returns the load balancer ingress IP currently on the
// service, or "" if the service is gone or has no ingress IP.
func (f *Framework) serviceIngressIP(svc *corev1.Service) string {
	current, err := f.K8s.CoreV1().Services(svc.Namespace).Get(
		context.Background(), svc.Name, metav1.GetOptions{})
	if err != nil || len(current.Status.LoadBalancer.Ingress) == 0 {
		return ""
	}
	return current.Status.LoadBalancer.Ingress[0].IP
}

// defaultLoadBalancerName mirrors cloudprovider.DefaultLoadBalancerName: "a"
// followed by the service UID with dashes stripped, truncated to 32 chars.
func defaultLoadBalancerName(svc *corev1.Service) string {
	name := "a" + strings.ReplaceAll(string(svc.UID), "-", "")
	if len(name) > 32 {
		name = name[:32]
	}
	return name
}

// LBRules returns the CloudStack load balancer rules whose names start with
// the given LB name.
func (f *Framework) LBRules(lbName string) ([]*cloudstack.LoadBalancerRule, error) {
	p := f.CS.LoadBalancer.NewListLoadBalancerRulesParams()
	p.SetKeyword(lbName)
	p.SetListall(true)
	if f.ProjectID != "" {
		p.SetProjectid(f.ProjectID)
	}
	resp, err := f.CS.LoadBalancer.ListLoadBalancerRules(p)
	if err != nil {
		return nil, err
	}
	var rules []*cloudstack.LoadBalancerRule
	for _, r := range resp.LoadBalancerRules {
		if strings.HasPrefix(r.Name, lbName) {
			rules = append(rules, r)
		}
	}
	return rules, nil
}

// WaitForIngressIP waits until the service has a load balancer ingress entry
// and returns it.
func (f *Framework) WaitForIngressIP(svc *corev1.Service) corev1.LoadBalancerIngress {
	f.T.Helper()
	var ingress corev1.LoadBalancerIngress
	f.Eventually(lbSyncTimeout, lbSyncInterval,
		fmt.Sprintf("service %s/%s to get an ingress address", svc.Namespace, svc.Name),
		func() (bool, error) {
			current, err := f.K8s.CoreV1().Services(svc.Namespace).Get(
				context.Background(), svc.Name, metav1.GetOptions{})
			if err != nil {
				return false, err
			}
			if len(current.Status.LoadBalancer.Ingress) == 0 {
				return false, nil
			}
			ingress = current.Status.LoadBalancer.Ingress[0]
			return true, nil
		})
	return ingress
}

// WaitForLBRules waits until exactly want rules exist for lbName and returns them.
func (f *Framework) WaitForLBRules(lbName string, want int) []*cloudstack.LoadBalancerRule {
	f.T.Helper()
	var rules []*cloudstack.LoadBalancerRule
	f.Eventually(lbSyncTimeout, lbSyncInterval,
		fmt.Sprintf("%d load balancer rule(s) named %s-*", want, lbName),
		func() (bool, error) {
			var err error
			rules, err = f.LBRules(lbName)
			if err != nil {
				return false, err
			}
			if len(rules) != want {
				names := make([]string, 0, len(rules))
				for _, r := range rules {
					names = append(names, r.Name)
				}
				return false, fmt.Errorf("saw %d rule(s) %v, want %d", len(rules), names, want)
			}
			return true, nil
		})
	return rules
}

// FirewallRules lists the firewall rules on a public IP.
func (f *Framework) FirewallRules(publicIPID string) ([]*cloudstack.FirewallRule, error) {
	p := f.CS.Firewall.NewListFirewallRulesParams()
	p.SetIpaddressid(publicIPID)
	p.SetListall(true)
	if f.ProjectID != "" {
		p.SetProjectid(f.ProjectID)
	}
	resp, err := f.CS.Firewall.ListFirewallRules(p)
	if err != nil {
		return nil, err
	}
	return resp.FirewallRules, nil
}

// ACLRules lists the network ACL rules on an ACL list.
func (f *Framework) ACLRules(aclListID string) ([]*cloudstack.NetworkACL, error) {
	p := f.CS.NetworkACL.NewListNetworkACLsParams()
	p.SetAclid(aclListID)
	p.SetListall(true)
	if f.ProjectID != "" {
		p.SetProjectid(f.ProjectID)
	}
	resp, err := f.CS.NetworkACL.ListNetworkACLs(p)
	if err != nil {
		return nil, err
	}
	return resp.NetworkACLs, nil
}

// PublicIP fetches a public IP address record by its ID.
func (f *Framework) PublicIP(id string) (*cloudstack.PublicIpAddress, error) {
	p := f.CS.Address.NewListPublicIpAddressesParams()
	p.SetId(id)
	p.SetListall(true)
	p.SetAllocatedonly(false)
	if f.ProjectID != "" {
		p.SetProjectid(f.ProjectID)
	}
	resp, err := f.CS.Address.ListPublicIpAddresses(p)
	if err != nil {
		return nil, err
	}
	if len(resp.PublicIpAddresses) == 0 {
		return nil, nil
	}
	return resp.PublicIpAddresses[0], nil
}

// FreePublicIP returns an unallocated public IP address from the zone's range.
//
// Unlike PublicIP, this is deliberately not project-scoped: a free IP belongs
// to the zone's public range and has no owner yet, so filtering by project
// would exclude every candidate.
func (f *Framework) FreePublicIP() (string, error) {
	p := f.CS.Address.NewListPublicIpAddressesParams()
	p.SetAllocatedonly(false)
	p.SetListall(true)
	p.SetState("Free")
	resp, err := f.CS.Address.ListPublicIpAddresses(p)
	if err != nil {
		return "", err
	}
	if len(resp.PublicIpAddresses) == 0 {
		return "", fmt.Errorf("no free public IP addresses available")
	}
	return resp.PublicIpAddresses[0].Ipaddress, nil
}

// PublicIPByAddress fetches a public IP address record by its address, or nil.
//
// Also not project-scoped: this is used to assert that an IP was released, and
// a released IP is no longer a project resource. Scoping it would hide exactly
// the state the assertion is looking for.
func (f *Framework) PublicIPByAddress(addr string) (*cloudstack.PublicIpAddress, error) {
	p := f.CS.Address.NewListPublicIpAddressesParams()
	p.SetIpaddress(addr)
	p.SetAllocatedonly(false)
	p.SetListall(true)
	resp, err := f.CS.Address.ListPublicIpAddresses(p)
	if err != nil {
		return nil, err
	}
	if len(resp.PublicIpAddresses) == 0 {
		return nil, nil
	}
	return resp.PublicIpAddresses[0], nil
}

// VMByName returns the CloudStack VM with the given name, or nil.
func (f *Framework) VMByName(name string) (*cloudstack.VirtualMachine, error) {
	vm, count, err := f.CS.VirtualMachine.GetVirtualMachineByName(
		name, cloudstack.WithProject(f.ProjectID))
	if err != nil {
		if count == 0 {
			return nil, nil
		}
		return nil, err
	}
	return vm, nil
}

// Nodes returns all nodes of the cluster under test.
func (f *Framework) Nodes() []corev1.Node {
	f.T.Helper()
	nodes, err := f.K8s.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		f.T.Fatalf("listing nodes: %v", err)
	}
	return nodes.Items
}

// UpdateService applies mutate to the latest version of the service and
// updates it. A conflict means another writer (usually the CCM) won the race,
// so the service is re-read and the update retried. Any other error fails the
// test immediately: retrying it until the timeout would just bury the real
// cause under a generic "timed out" message.
func (f *Framework) UpdateService(svc *corev1.Service, mutate func(*corev1.Service)) *corev1.Service {
	f.T.Helper()
	var updated *corev1.Service
	f.Eventually(30*time.Second, time.Second, "service update to apply without conflicting",
		func() (bool, error) {
			current, err := f.K8s.CoreV1().Services(svc.Namespace).Get(
				context.Background(), svc.Name, metav1.GetOptions{})
			if err != nil {
				f.T.Fatalf("getting service %s/%s: %v", svc.Namespace, svc.Name, err)
			}
			mutate(current)
			updated, err = f.K8s.CoreV1().Services(svc.Namespace).Update(
				context.Background(), current, metav1.UpdateOptions{})
			if apierrors.IsConflict(err) {
				return false, err
			}
			if err != nil {
				f.T.Fatalf("updating service %s/%s: %v", svc.Namespace, svc.Name, err)
			}
			return true, nil
		})
	return updated
}
