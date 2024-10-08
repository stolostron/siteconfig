/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package clusterinstance

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/stolostron/siteconfig/api/v1alpha1"
)

const (
	cpuPartitioningKey = "cpuPartitioningMode"
)

type SpecialVars struct {
	CurrentNode                      v1alpha1.NodeSpec
	InstallConfigOverrides           string
	ControlPlaneAgents, WorkerAgents int
}

// ClusterData is a special object that provides an interface to the ClusterInstance spec fields for use in rendering
// ClusterInstance-based install templates
type ClusterData struct {
	Spec        v1alpha1.ClusterInstanceSpec
	SpecialVars SpecialVars
}

// BuildClusterData returns a Cluster object that is consumed for rendering ClusterInstance-based install templates
func BuildClusterData(
	clusterInstance *v1alpha1.ClusterInstance,
	node *v1alpha1.NodeSpec,
) (data *ClusterData, err error) {

	// Prepare specialVars
	var currentNode v1alpha1.NodeSpec
	if node != nil {
		currentNode = *node
	}

	installConfigOverrides, err := getInstallConfigOverrides(clusterInstance)
	if err != nil {
		installConfigOverrides = ""
	}

	// Determine the number of control-plane and worker agents
	controlPlaneAgents := 0
	workerAgents := 0
	for _, node := range clusterInstance.Spec.Nodes {
		switch node.Role {
		case "master":
			controlPlaneAgents++
		case "worker":
			workerAgents++
		}
	}

	data = &ClusterData{
		Spec: clusterInstance.Spec,
		SpecialVars: SpecialVars{
			CurrentNode:            currentNode,
			InstallConfigOverrides: installConfigOverrides,
			ControlPlaneAgents:     controlPlaneAgents,
			WorkerAgents:           workerAgents,
		},
	}

	return
}

// getWorkloadPinningInstallConfigOverrides applies workload pinning to install config overrides if applicable
func getWorkloadPinningInstallConfigOverrides(clusterInstance *v1alpha1.ClusterInstance) (result string, err error) {

	scInstallConfigOverrides := clusterInstance.Spec.InstallConfigOverrides
	if clusterInstance.Spec.CPUPartitioning == v1alpha1.CPUPartitioningAllNodes {
		installOverrideValues := map[string]interface{}{}
		if scInstallConfigOverrides != "" {
			err := json.Unmarshal([]byte(scInstallConfigOverrides), &installOverrideValues)
			if err != nil {
				return scInstallConfigOverrides, err
			}
		}

		// Because the explicit value clusterInstance.Spec.CPUPartitioning == CPUPartitioningAllNodes, we always
		// overwrite the installConfigOverrides value or add it if not present
		installOverrideValues[cpuPartitioningKey] = v1alpha1.CPUPartitioningAllNodes

		byteData, err := json.Marshal(installOverrideValues)
		if err != nil {
			return scInstallConfigOverrides, err
		}
		return string(byteData), nil
	}

	return scInstallConfigOverrides, nil
}

// getInstallConfigOverrides builds the InstallConfigOverrides and returns it as a JSON string
func getInstallConfigOverrides(clusterInstance *v1alpha1.ClusterInstance) (string, error) {

	// Get workload-pinning install config overrides
	installConfigOverrides, err := getWorkloadPinningInstallConfigOverrides(clusterInstance)
	if err != nil {
		return installConfigOverrides, err
	}

	var commonKey = "networking"
	networkAnnotation := "{\"networking\":{\"networkType\":\"" + clusterInstance.Spec.NetworkType + "\"}}"
	if !json.Valid([]byte(networkAnnotation)) {
		return installConfigOverrides, fmt.Errorf("invalid json conversion of network type")
	}

	switch installConfigOverrides {
	case "":
		return networkAnnotation, nil

	default:
		if !json.Valid([]byte(installConfigOverrides)) {
			return "", fmt.Errorf("invalid json parameter set at installConfigOverride")
		}

		var installConfigOverridesMap map[string]interface{}
		err := json.Unmarshal([]byte(installConfigOverrides), &installConfigOverridesMap)
		if err != nil {
			return "", fmt.Errorf("failed to unmarshal installConfigOverrides data: %v", installConfigOverrides)
		}

		if _, found := installConfigOverridesMap[commonKey]; found {
			networkMergedJson, err := mergeJSONCommonKey(networkAnnotation, installConfigOverrides, commonKey)
			if err != nil {
				return "", fmt.Errorf("failed to merge installConfigOverrides objects, error: %v", err)
			}
			return networkMergedJson, nil
		}

		trimmedConfigOverrides := strings.TrimPrefix(installConfigOverrides, "{")
		trimmedNetworkType := strings.TrimSuffix(networkAnnotation, "}")
		finalJson := trimmedNetworkType + "," + trimmedConfigOverrides
		if !json.Valid([]byte(finalJson)) {
			return "", fmt.Errorf("failed to marshal annotation for installConfigOverrides, error: %v", err)
		}
		return finalJson, nil

	}
}

// mergeJSONCommonKey merge 2 json in common key and return string
func mergeJSONCommonKey(mergeWith, mergeTo, key string) (string, error) {
	var (
		mapMergeWith map[string]interface{}
		mapMergeTo   map[string]interface{}
	)

	// Unmarshal JSON strings into maps
	if err := json.Unmarshal([]byte(mergeWith), &mapMergeWith); err != nil {
		return "", err
	}

	if err := json.Unmarshal([]byte(mergeTo), &mapMergeTo); err != nil {
		return "", err
	}

	// Check if the key exists in both maps
	mergeWithValue, mergeToValue := mapMergeWith[key], mapMergeTo[key]
	if mergeWithValue == nil || mergeToValue == nil {
		return "", fmt.Errorf("key not found in one of the JSON strings")
	}

	// Convert values to map if not already
	mergeWithValueMap, ok := mergeWithValue.(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("value associated with the key is not a map")
	}

	mergeToValueMap, ok := mergeToValue.(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("value associated with the key is not a map")
	}

	// Merge maps
	for k, v := range mergeWithValueMap {
		mergeToValueMap[k] = v
	}

	// Update the merged map in mergeTo
	mapMergeTo[key] = mergeToValueMap

	// Marshal merged map to JSON string
	mergedJSON, err := json.Marshal(mapMergeTo)
	if err != nil {
		return "", err
	}

	return string(mergedJSON), nil
}
