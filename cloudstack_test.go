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
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/apache/cloudstack-go/v2/cloudstack"
	"github.com/blang/semver/v4"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const testClusterName = "testCluster"

func TestReadConfig(t *testing.T) {
	_, err := readConfig(nil)
	if err != nil {
		t.Fatalf("Should not return an error when no config is provided: %v", err)
	}

	cfg, err := readConfig(strings.NewReader(`
 [Global]
 api-url				= https://cloudstack.url
 api-key				= a-valid-api-key
 secret-key			= a-valid-secret-key
 ssl-no-verify	= true
 project-id			= a-valid-project-id
 `))
	if err != nil {
		t.Fatalf("Should succeed when a valid config is provided: %v", err)
	}

	if cfg.Global.APIURL != "https://cloudstack.url" {
		t.Errorf("incorrect api-url: %s", cfg.Global.APIURL)
	}
	if cfg.Global.APIKey != "a-valid-api-key" {
		t.Errorf("incorrect api-key: %s", cfg.Global.APIKey)
	}
	if cfg.Global.SecretKey != "a-valid-secret-key" {
		t.Errorf("incorrect secret-key: %s", cfg.Global.SecretKey)
	}
	if !cfg.Global.SSLNoVerify {
		t.Errorf("incorrect ssl-no-verify: %t", cfg.Global.SSLNoVerify)
	}
}

// This allows acceptance testing against an existing CloudStack environment.
func configFromEnv() (*CSConfig, bool) {
	cfg := &CSConfig{}

	cfg.Global.APIURL = os.Getenv("CS_API_URL")
	cfg.Global.APIKey = os.Getenv("CS_API_KEY")
	cfg.Global.SecretKey = os.Getenv("CS_SECRET_KEY")
	cfg.Global.ProjectID = os.Getenv("CS_PROJECT_ID")

	// It is save to ignore the error here. If the input cannot be parsed SSLNoVerify
	// will still be a bool with its zero value (false) which is the expected default.
	cfg.Global.SSLNoVerify, _ = strconv.ParseBool(os.Getenv("CS_SSL_NO_VERIFY"))

	// Check if we have the minimum required info to be able to connect to CloudStack.
	ok := cfg.Global.APIURL != "" && cfg.Global.APIKey != "" && cfg.Global.SecretKey != ""

	return cfg, ok
}

func TestNewCSCloud(t *testing.T) {
	cfg, ok := configFromEnv()
	if !ok {
		t.Skipf("No config found in environment")
	}

	_, err := newCSCloud(cfg)
	if err != nil {
		t.Fatalf("Failed to construct/authenticate CloudStack: %v", err)
	}
}

func TestLoadBalancer(t *testing.T) {
	cfg, ok := configFromEnv()
	if !ok {
		t.Skipf("No config found in environment")
	}

	cs, err := newCSCloud(cfg)
	if err != nil {
		t.Fatalf("Failed to construct/authenticate CloudStack: %v", err)
	}

	lb, ok := cs.LoadBalancer()
	if !ok {
		t.Fatalf("LoadBalancer() returned false")
	}

	_, exists, err := lb.GetLoadBalancer(context.TODO(), testClusterName, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "noexist"}})
	if err != nil {
		t.Fatalf("GetLoadBalancer(\"noexist\") returned error: %s", err)
	}
	if exists {
		t.Fatalf("GetLoadBalancer(\"noexist\") returned exists")
	}
}

func TestGetManagementServerVersion(t *testing.T) {
	t.Run("returns parsed version", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockMgmt := cloudstack.NewMockManagementServiceIface(ctrl)
		params := &cloudstack.ListManagementServersMetricsParams{}
		resp := &cloudstack.ListManagementServersMetricsResponse{
			Count: 1,
			ManagementServersMetrics: []*cloudstack.ManagementServersMetric{
				{Version: "4.17.1.0"},
			},
		}

		gomock.InOrder(
			mockMgmt.EXPECT().NewListManagementServersMetricsParams().Return(params),
			mockMgmt.EXPECT().ListManagementServersMetrics(params).Return(resp, nil),
		)

		cs := &CSCloud{
			client: &cloudstack.CloudStackClient{
				Management: mockMgmt,
			},
		}

		version, err := cs.getManagementServerVersion()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		expected := semver.MustParse("4.17.1")
		if !version.Equals(expected) {
			t.Fatalf("version = %v, want %v", version, expected)
		}
	})

	t.Run("returns correct parsed version with development server", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockMgmt := cloudstack.NewMockManagementServiceIface(ctrl)
		params := &cloudstack.ListManagementServersMetricsParams{}
		resp := &cloudstack.ListManagementServersMetricsResponse{
			Count: 1,
			ManagementServersMetrics: []*cloudstack.ManagementServersMetric{
				{Version: "4.17.1.0-SNAPSHOT"},
			},
		}

		gomock.InOrder(
			mockMgmt.EXPECT().NewListManagementServersMetricsParams().Return(params),
			mockMgmt.EXPECT().ListManagementServersMetrics(params).Return(resp, nil),
		)

		cs := &CSCloud{
			client: &cloudstack.CloudStackClient{
				Management: mockMgmt,
			},
		}

		version, err := cs.getManagementServerVersion()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		expected := semver.MustParse("4.17.1")
		if !version.Equals(expected) {
			t.Fatalf("version = %v, want %v", version, expected)
		}
	})

	t.Run("returns error when api call fails", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockMgmt := cloudstack.NewMockManagementServiceIface(ctrl)
		params := &cloudstack.ListManagementServersMetricsParams{}
		apiErr := errors.New("api failure")

		gomock.InOrder(
			mockMgmt.EXPECT().NewListManagementServersMetricsParams().Return(params),
			mockMgmt.EXPECT().ListManagementServersMetrics(params).Return(nil, apiErr),
		)

		cs := &CSCloud{
			client: &cloudstack.CloudStackClient{
				Management: mockMgmt,
			},
		}

		if _, err := cs.getManagementServerVersion(); err == nil {
			t.Fatalf("expected error, got nil")
		}
	})

	t.Run("returns error when no servers found", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockMgmt := cloudstack.NewMockManagementServiceIface(ctrl)
		params := &cloudstack.ListManagementServersMetricsParams{}
		resp := &cloudstack.ListManagementServersMetricsResponse{
			Count:                    0,
			ManagementServersMetrics: []*cloudstack.ManagementServersMetric{},
		}

		gomock.InOrder(
			mockMgmt.EXPECT().NewListManagementServersMetricsParams().Return(params),
			mockMgmt.EXPECT().ListManagementServersMetrics(params).Return(resp, nil),
		)

		cs := &CSCloud{
			client: &cloudstack.CloudStackClient{
				Management: mockMgmt,
			},
		}

		if _, err := cs.getManagementServerVersion(); err == nil {
			t.Fatalf("expected error for zero management servers")
		}
	})

	t.Run("returns error when version cannot be parsed", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		mockMgmt := cloudstack.NewMockManagementServiceIface(ctrl)
		params := &cloudstack.ListManagementServersMetricsParams{}
		resp := &cloudstack.ListManagementServersMetricsResponse{
			Count: 1,
			ManagementServersMetrics: []*cloudstack.ManagementServersMetric{
				{Version: "invalid.version.string"},
			},
		}

		gomock.InOrder(
			mockMgmt.EXPECT().NewListManagementServersMetricsParams().Return(params),
			mockMgmt.EXPECT().ListManagementServersMetrics(params).Return(resp, nil),
		)

		cs := &CSCloud{
			client: &cloudstack.CloudStackClient{
				Management: mockMgmt,
			},
		}

		if _, err := cs.getManagementServerVersion(); err == nil {
			t.Fatalf("expected parse error")
		}
	})
}
