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
	"fmt"
	"regexp"
	"strings"
)

// If Instances.InstanceID or cloudprovider.GetInstanceProviderID is changed, the regexp should be changed too.
var providerIDRegexp = regexp.MustCompile(`^` + ProviderName + `://([^/]*)/([^/]+)$`)

// instanceIDFromProviderID splits a provider's id and return instanceID.
// A providerID is build out of '${ProviderName}:///${instance-id}' which contains ':///'.
// or '${ProviderName}://${region}/${instance-id}' which contains '://'.
// See cloudprovider.GetInstanceProviderID and Instances.InstanceID.
func instanceIDFromProviderID(providerID string) (instanceID string, region string, err error) {

	// https://github.com/kubernetes/kubernetes/issues/85731
	if providerID != "" && !strings.Contains(providerID, "://") {
		providerID = ProviderName + "://" + providerID
	}

	matches := providerIDRegexp.FindStringSubmatch(providerID)
	if len(matches) != 3 {
		return "", "", fmt.Errorf("ProviderID \"%s\" didn't match expected format \"cloudstack://region/InstanceID\"", providerID)
	}
	return matches[2], matches[1], nil
}

// Sanitize label value so it complies with https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#syntax-and-character-set
// Anything but [-A-Za-z0-9_.] will get converted to '_'
func sanitizeLabel(value string) string {
	fn := func(r rune) rune {
		if r >= 'a' && r <= 'z' ||
			r >= 'A' && r <= 'Z' ||
			r >= '0' && r <= '9' ||
			r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}
	value = strings.Map(fn, value)

	// Must start & end with alphanumeric char
	value = strings.Trim(value, "-_.")

	// Strip anything over 63 chars
	if len(value) > 63 {
		value = value[:63]
		value = strings.Trim(value, "-_.")
	}

	return value
}
