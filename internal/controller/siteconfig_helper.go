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

package controller

import (
	"encoding/json"
	"fmt"
	"html/template"
	"reflect"
	"strings"

	sprig "github.com/go-task/slim-sprig"
	"github.com/sakhoury/siteconfig/api/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8syaml "sigs.k8s.io/yaml"
)

const (
	cpuPartitioningKey = "cpuPartitioningMode"
)

type SpecialVars struct {
	CurrentNode                      v1alpha1.NodeSpec
	InstallConfigOverrides           string
	ControlPlaneAgents, WorkerAgents int
}

// SiteData is a special object that provides an interface to the SiteConfig spec fields for use in rendering templates
type SiteData struct {
	Site        v1alpha1.SiteConfigSpec
	SpecialVars SpecialVars
}

// getWorkloadPinningInstallConfigOverrides applies workload pinning to install config overrides if applicable
func getWorkloadPinningInstallConfigOverrides(siteConfig *v1alpha1.SiteConfig) (result string, err error) {

	scInstallConfigOverrides := siteConfig.Spec.InstallConfigOverrides
	if siteConfig.Spec.CPUPartitioning == v1alpha1.CPUPartitioningAllNodes {
		installOverrideValues := map[string]interface{}{}
		if scInstallConfigOverrides != "" {
			err := json.Unmarshal([]byte(scInstallConfigOverrides), &installOverrideValues)
			if err != nil {
				return scInstallConfigOverrides, err
			}
		}

		// Because the explicit value siteConfig.Spec.CPUPartitioning == CPUPartitioningAllNodes, we always overwrite
		// the installConfigOverrides value or add it if not present
		installOverrideValues[cpuPartitioningKey] = v1alpha1.CPUPartitioningAllNodes

		byteData, err := json.Marshal(installOverrideValues)
		if err != nil {
			return scInstallConfigOverrides, err
		}
		return string(byteData), nil
	}

	return scInstallConfigOverrides, nil
}

// getInstallConfigOverrides builds the InstallConfigOverrides and returns it as a json string
func getInstallConfigOverrides(siteConfig *v1alpha1.SiteConfig) (string, error) {

	// Get workload-pinning install config overrides
	installConfigOverrides, err := getWorkloadPinningInstallConfigOverrides(siteConfig)
	if err != nil {
		return installConfigOverrides, err
	}

	var commonKey = "networking"
	networkAnnotation := "{\"networking\":{\"networkType\":\"" + siteConfig.Spec.NetworkType + "\"}}"
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

// buildSiteData returns a Site object that is consumed for rendering templates
func buildSiteData(siteConfig *v1alpha1.SiteConfig, node *v1alpha1.NodeSpec) (data *SiteData, err error) {

	// Prepare specialVars
	var currentNode v1alpha1.NodeSpec
	if node != nil {
		currentNode = *node
	}

	installConfigOverrides, err := getInstallConfigOverrides(siteConfig)
	if err != nil {
		installConfigOverrides = ""
	}

	// Determine the number of control-plane and worker agents
	controlPlaneAgents := 0
	workerAgents := 0
	for _, node := range siteConfig.Spec.Nodes {
		switch node.Role {
		case "master":
			controlPlaneAgents++
		case "worker":
			workerAgents++
		}
	}

	data = &SiteData{
		Site: siteConfig.Spec,
		SpecialVars: SpecialVars{
			CurrentNode:            currentNode,
			InstallConfigOverrides: installConfigOverrides,
			ControlPlaneAgents:     controlPlaneAgents,
			WorkerAgents:           workerAgents,
		},
	}

	return
}

// suppressRenderingManifest function returns true if the manifest-rendering should be suppressed
func suppressRenderingManifest(kind string, suppressedManifests []string) bool {
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

func appendManifestAnnotations(extraAnnotations map[string]string, manifest map[string]interface{}) map[string]interface{} {
	if manifest["metadata"] == nil && len(extraAnnotations) > 0 {
		manifest["metadata"] = make(map[string]interface{})
	}
	metadata, _ := manifest["metadata"].(map[string]interface{})

	if metadata["annotations"] == nil && len(extraAnnotations) > 0 {
		metadata["annotations"] = make(map[string]interface{})
	}
	annotations, _ := metadata["annotations"].(map[string]interface{})

	for key, value := range extraAnnotations {
		if _, found := annotations[key]; !found {
			// It's a new annotation, adding
			if annotations == nil {
				annotations = make(map[string]interface{})
			}
			annotations[key] = value
		}
	}
	return manifest
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

// anyFieldDefined checks to see if an object has at least 1 field defined in a struct-like object
func anyFieldDefined(v interface{}) bool {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return false
	}
	for i := 0; i < rv.NumField(); i++ {
		field := rv.Field(i)
		zero := reflect.Zero(field.Type()).Interface()
		if !reflect.DeepEqual(field.Interface(), zero) {
			return true
		}
	}
	return false
}

// funcMap provides additional useful functions for template rendering
func funcMap() template.FuncMap {
	f := sprig.TxtFuncMap()
	f["toYaml"] = toYaml
	f["anyFieldDefined"] = anyFieldDefined
	return f
}

func toUnstructured(obj interface{}) (unstructured.Unstructured, error) {
	var uObj unstructured.Unstructured
	// Marshal the input object to JSON
	if content, err := json.Marshal(obj); err != nil {
		return uObj, err
	} else if err = json.Unmarshal(content, &uObj); err != nil {
		return uObj, err
	}
	return uObj, nil
}
