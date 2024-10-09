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

package engine

import (
	"strings"
	"text/template"

	sprig "github.com/go-task/slim-sprig"

	k8syaml "sigs.k8s.io/yaml"

	"github.com/stolostron/siteconfig/api/v1alpha1"
)

const (
	AnnotationsKey = "annotations"
	LabelsKey      = "labels"
)

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

// pruneManifest function returns true if the manifest should be pruned (i.e. deleted)
func pruneManifest(resource v1alpha1.ResourceRef, pruneList []v1alpha1.ResourceRef) bool {
	if resource.APIVersion == "" || resource.Kind == "" || len(pruneList) == 0 {
		return false
	}

	for _, p := range pruneList {
		if resource.APIVersion == p.APIVersion && resource.Kind == p.Kind {
			return true
		}
	}
	return false
}

// suppressManifest function returns true if the manifest should be suppressed (i.e. not rendered)
func suppressManifest(kind string, supressManifestsList []string) bool {
	if kind == "" || len(supressManifestsList) == 0 {
		return false
	}

	for _, manifest := range supressManifestsList {
		if manifest == kind {
			return true
		}
	}
	return false
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
