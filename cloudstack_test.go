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
	if cfg.Global.Version != "" {
		t.Errorf("version should be empty when not configured: %s", cfg.Global.Version)
	}

	cfg, err = readConfig(strings.NewReader(`
 [Global]
 api-url				= https://cloudstack.url
 version				= 4.21.0.0
 `))
	if err != nil {
		t.Fatalf("Should succeed when a valid config is provided: %v", err)
	}
	if cfg.Global.Version != "4.21.0.0" {
		t.Errorf("incorrect version: %s", cfg.Global.Version)
	}
}

func TestNewCSCloudWithVersionFromConfig(t *testing.T) {
	cfg := &CSConfig{}
	cfg.Global.APIURL = "https://cloudstack.url/client/api"
	cfg.Global.APIKey = "a-valid-api-key"
	cfg.Global.SecretKey = "a-valid-secret-key"
	cfg.Global.Version = "4.21.0.0"

	// The version from the config is used as-is, so no API call is made.
	cs, err := newCSCloud(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := semver.MustParse("4.21.0")
	if !cs.version.Equals(expected) {
		t.Fatalf("version = %v, want %v", cs.version, expected)
	}

	cfg.Global.Version = "not-a-version"
	if _, err := newCSCloud(cfg); err == nil {
		t.Fatalf("expected error for an invalid version in the config")
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

// newCSCloudWithCapabilities returns a CSCloud whose Configuration service is mocked to
// answer a single listCapabilities call with the given response and error.
func newCSCloudWithCapabilities(t *testing.T, resp *cloudstack.ListCapabilitiesResponse, err error) *CSCloud {
	t.Helper()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockConfig := cloudstack.NewMockConfigurationServiceIface(ctrl)
	params := &cloudstack.ListCapabilitiesParams{}

	gomock.InOrder(
		mockConfig.EXPECT().NewListCapabilitiesParams().Return(params),
		mockConfig.EXPECT().ListCapabilities(params).Return(resp, err),
	)

	return &CSCloud{
		client: &cloudstack.CloudStackClient{
			Configuration: mockConfig,
		},
	}
}

func TestGetCloudStackVersion(t *testing.T) {
	t.Run("returns parsed version", func(t *testing.T) {
		cs := newCSCloudWithCapabilities(t, &cloudstack.ListCapabilitiesResponse{
			Capabilities: &cloudstack.Capability{Cloudstackversion: "4.17.1.0"},
		}, nil)

		version, err := cs.getCloudStackVersion()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		expected := semver.MustParse("4.17.1")
		if !version.Equals(expected) {
			t.Fatalf("version = %v, want %v", version, expected)
		}
	})

	t.Run("returns correct parsed version with development server", func(t *testing.T) {
		cs := newCSCloudWithCapabilities(t, &cloudstack.ListCapabilitiesResponse{
			Capabilities: &cloudstack.Capability{Cloudstackversion: "4.17.1.0-SNAPSHOT"},
		}, nil)

		version, err := cs.getCloudStackVersion()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		expected := semver.MustParse("4.17.1")
		if !version.Equals(expected) {
			t.Fatalf("version = %v, want %v", version, expected)
		}
	})

	t.Run("returns error when api call fails", func(t *testing.T) {
		cs := newCSCloudWithCapabilities(t, nil, errors.New("api failure"))

		if _, err := cs.getCloudStackVersion(); err == nil {
			t.Fatalf("expected error, got nil")
		}
	})

	t.Run("returns error when no capabilities returned", func(t *testing.T) {
		cs := newCSCloudWithCapabilities(t, &cloudstack.ListCapabilitiesResponse{}, nil)

		if _, err := cs.getCloudStackVersion(); err == nil {
			t.Fatalf("expected error for missing capabilities")
		}
	})

	t.Run("returns error when version is empty", func(t *testing.T) {
		cs := newCSCloudWithCapabilities(t, &cloudstack.ListCapabilitiesResponse{
			Capabilities: &cloudstack.Capability{},
		}, nil)

		if _, err := cs.getCloudStackVersion(); err == nil {
			t.Fatalf("expected error for empty version")
		}
	})

	t.Run("returns error when version cannot be parsed", func(t *testing.T) {
		cs := newCSCloudWithCapabilities(t, &cloudstack.ListCapabilitiesResponse{
			Capabilities: &cloudstack.Capability{Cloudstackversion: "invalid.version.string"},
		}, nil)

		if _, err := cs.getCloudStackVersion(); err == nil {
			t.Fatalf("expected parse error")
		}
	})
}

func TestParseCloudStackVersion(t *testing.T) {
	tests := []struct {
		version string
		want    string
		wantErr bool
	}{
		{version: "4.17.1.0", want: "4.17.1"},
		{version: "4.17.1.0-SNAPSHOT", want: "4.17.1"},
		{version: "4.21.0", want: "4.21.0"},
		{version: "4.21", want: "4.21.0"},
		{version: "4", want: "4.0.0"},
		{version: "invalid.version.string", wantErr: true},
		{version: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			got, err := parseCloudStackVersion(tt.version)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got %v", tt.version, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !got.Equals(semver.MustParse(tt.want)) {
				t.Fatalf("version = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetRegionFromZone(t *testing.T) {
	tests := []struct {
		name   string
		region string
		zone   string
		want   string
	}{
		{
			name:   "region configured in cloud config",
			region: "us-east-1",
			zone:   "zone-1",
			want:   "us-east-1",
		},
		{
			name:   "region not configured, returns zone",
			region: "",
			zone:   "zone-1",
			want:   "zone-1",
		},
		{
			name:   "region configured with empty zone",
			region: "eu-central-1",
			zone:   "",
			want:   "eu-central-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := &CSCloud{
				region: tt.region,
			}
			got := cs.getRegionFromZone(tt.zone)
			if got != tt.want {
				t.Errorf("getRegionFromZone(%q) with region=%q = %q, want %q", tt.zone, tt.region, got, tt.want)
			}
		})
	}
}
