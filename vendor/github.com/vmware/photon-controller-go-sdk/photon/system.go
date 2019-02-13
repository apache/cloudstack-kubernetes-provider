// Copyright (c) 2017 VMware, Inc. All Rights Reserved.
//
// This product is licensed to you under the Apache License, Version 2.0 (the "License").
// You may not use this product except in compliance with the License.
//
// This product may include a number of subcomponents with separate copyright notices and
// license terms. Your use of these subcomponents is subject to the terms and conditions
// of the subcomponent's license, as noted in the LICENSE file.

package photon

import (
	"bytes"
	"encoding/json"
)

// Contains functionality for system API.
type SystemAPI struct {
	client *Client
}

var systemUrl string = rootUrl + "/system"

// Get status of photon controller
func (api *SystemAPI) GetSystemStatus() (status *Status, err error) {
	res, err := api.client.restClient.Get(api.getEndpointUrl("status"), api.client.options.TokenOptions)
	if err != nil {
		return
	}
	defer res.Body.Close()
	res, err = getError(res)
	if err != nil {
		return
	}
	status = &Status{}
	err = json.NewDecoder(res.Body).Decode(status)
	return
}

// Gets the system info.
func (api *SystemAPI) GetSystemInfo() (deployment *Deployment, err error) {
	res, err := api.client.restClient.Get(api.getEndpointUrl("info"), api.client.options.TokenOptions)
	if err != nil {
		return
	}
	defer res.Body.Close()
	res, err = getError(res)
	if err != nil {
		return
	}
	var result Deployment
	err = json.NewDecoder(res.Body).Decode(&result)
	return &result, nil
}

// Pause system.
func (api *SystemAPI) PauseSystem() (task *Task, err error) {
	res, err := api.client.restClient.Post(
		api.getEndpointUrl("pause"),
		"application/json",
		bytes.NewReader([]byte("")),
		api.client.options.TokenOptions)
	if err != nil {
		return
	}
	defer res.Body.Close()

	task, err = getTask(getError(res))
	return
}

// Pause system background tasks.
func (api *SystemAPI) PauseBackgroundTasks() (task *Task, err error) {
	res, err := api.client.restClient.Post(
		api.getEndpointUrl("pause-background-tasks"),
		"application/json",
		bytes.NewReader([]byte("")),
		api.client.options.TokenOptions)
	if err != nil {
		return
	}
	defer res.Body.Close()

	task, err = getTask(getError(res))
	return
}

// Resume system.
func (api *SystemAPI) ResumeSystem() (task *Task, err error) {
	res, err := api.client.restClient.Post(
		api.getEndpointUrl("resume"),
		"application/json",
		bytes.NewReader([]byte("")),
		api.client.options.TokenOptions)
	if err != nil {
		return
	}
	defer res.Body.Close()

	task, err = getTask(getError(res))
	return
}

// Sets security groups for the system
func (api *SystemAPI) SetSecurityGroups(securityGroups *SecurityGroupsSpec) (task *Task, err error) {
	body, err := json.Marshal(securityGroups)
	if err != nil {
		return
	}
	url := api.getEndpointUrl("set-security-groups")
	res, err := api.client.restClient.Post(
		url,
		"application/json",
		bytes.NewReader(body),
		api.client.options.TokenOptions)
	if err != nil {
		return
	}
	defer res.Body.Close()
	task, err = getTask(getError(res))
	return
}

// Gets the system info.
func (api *SystemAPI) GetSystemSize() (deploymentSize *DeploymentSize, err error) {
	res, err := api.client.restClient.Get(api.getEndpointUrl("usage"), api.client.options.TokenOptions)
	if err != nil {
		return
	}
	defer res.Body.Close()
	res, err = getError(res)
	if err != nil {
		return
	}
	var result DeploymentSize
	err = json.NewDecoder(res.Body).Decode(&result)
	return &result, nil
}

// Gets authentication info.
func (api *SystemAPI) GetAuthInfo() (info *AuthInfo, err error) {
	res, err := api.client.restClient.Get(api.getEndpointUrl("auth"), nil)
	if err != nil {
		return
	}
	defer res.Body.Close()
	res, err = getError(res)
	if err != nil {
		return
	}
	info = &AuthInfo{}
	err = json.NewDecoder(res.Body).Decode(info)
	return
}

func (api *SystemAPI) getEndpointUrl(endpoint string) (url string) {
	return api.client.Endpoint + systemUrl + "/" + endpoint
}
