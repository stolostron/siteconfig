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
	"html/template"
	"strings"

	sprig "github.com/go-task/slim-sprig"
	"github.com/stolostron/siteconfig/api/v1alpha1"
	"k8s.io/apimachinery/pkg/types"
	k8syaml "sigs.k8s.io/yaml"
)

const (
	cpuPartitioningKey = "cpuPartitioningMode"
	AnnotationsKey     = "annotations"
	LabelsKey          = "labels"
)

type SpecialVars struct {
	CurrentNode                      v1alpha1.NodeSpec
	InstallConfigOverrides           string
	ControlPlaneAgents, WorkerAgents int
}

// ClusterData is a special object that provides an interface to the ClusterInstance spec fields for use in rendering
// templates
type ClusterData struct {
	Spec        v1alpha1.ClusterInstanceSpec
	SpecialVars SpecialVars
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

// buildClusterData returns a Cluster object that is consumed for rendering templates
func buildClusterData(clusterInstance *v1alpha1.ClusterInstance, node *v1alpha1.NodeSpec) (data *ClusterData, err error) {

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

// suppressManifest function returns true if the manifest-rendering should be suppressed
func suppressManifest(kind string, suppressedManifests []string) bool {
	if kind == "" || len(suppressedManifests) == 0 {
		return false
	}

	for _, manifest := range suppressedManifests {
		if manifest == kind {
			return true
		}
	}
	return false
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

func appendToManifestMetadata(
	appendData map[string]string,
	field string,
	manifest map[string]interface{},
) map[string]interface{} {
	if manifest["metadata"] == nil && len(appendData) > 0 {
		manifest["metadata"] = make(map[string]interface{})
	}
	metadata, _ := manifest["metadata"].(map[string]interface{})

	if metadata[field] == nil && len(appendData) > 0 {
		metadata[field] = make(map[string]interface{})
	}
	data, _ := metadata[field].(map[string]interface{})

	for key, value := range appendData {
		if _, found := data[key]; !found {
			// It's a new data-item, adding
			if data == nil {
				data = make(map[string]interface{})
			}
			data[key] = value
		}
	}
	return manifest
}

func appendManifestAnnotations(extraAnnotations map[string]string, manifest map[string]interface{}) map[string]interface{} {
	return appendToManifestMetadata(extraAnnotations, AnnotationsKey, manifest)
}

func appendManifestLabels(extraLabels map[string]string, manifest map[string]interface{}) map[string]interface{} {
	return appendToManifestMetadata(extraLabels, LabelsKey, manifest)
}

// toYaml marshals a given field to Yaml
func toYaml(v interface{}) string {
	data, err := k8syaml.Marshal(v)
	if err != nil {
		// Swallow errors inside of a template.
		return ""
	}
	return strings.TrimSuffix(string(data), "\n")
}

// funcMap provides additional useful functions for template rendering
func funcMap() template.FuncMap {
	f := sprig.TxtFuncMap()
	f["toYaml"] = toYaml
	return f
}

// GenerateOwnedByLabelValue is a utility function that generates the ownedBy label value
// using the ClusterInstance namespace and name
func GenerateOwnedByLabelValue(namespace, name string) string {
	return fmt.Sprintf("%s_%s", namespace, name)
}

// GetNamespacedNameFromOwnedByLabel extracts the namespace and name from the ownedBy label value
func GetNamespacedNameFromOwnedByLabel(ownedByLabel string) (types.NamespacedName, error) {
	res := strings.Split(ownedByLabel, "_")
	if len(res) != 2 {
		return types.NamespacedName{}, fmt.Errorf("expecting single underscore delimiter in %s label value", ownedByLabel)
	}

	return types.NamespacedName{Namespace: res[0], Name: res[1]}, nil
}
