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
	"bytes"
	"context"
	"fmt"
	"text/template"
	"unicode"

	"github.com/go-logr/logr"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/stolostron/siteconfig/api/v1alpha1"
)

const (
	WaveAnnotation        = v1alpha1.Group + "/sync-wave"
	DefaultWaveAnnotation = "0"
)

type ClusterInstanceBuilder struct {
	Log logr.Logger
}

func NewClusterInstanceBuilder(pLog logr.Logger) *ClusterInstanceBuilder {
	scBuilder := ClusterInstanceBuilder{Log: pLog}

	return &scBuilder
}

func (cib *ClusterInstanceBuilder) ProcessTemplates(ctx context.Context, c client.Client, clusterInstance v1alpha1.ClusterInstance) ([]interface{}, error) {
	cib.Log.Info(fmt.Sprintf("Processing cluster-level templates for ClusterInstance %s", clusterInstance.Name))

	// Render cluster-level templates
	clusterManifests, err := cib.renderTemplates(ctx, c, &clusterInstance, nil)
	if err != nil {
		cib.Log.Info(fmt.Sprintf("encountered error while processing cluster-level templates for ClusterInstance %s, err: %s", clusterInstance.Name, err.Error()))
		return clusterManifests, err
	}
	cib.Log.Info(fmt.Sprintf("Processed cluster-level templates for ClusterInstance %s", clusterInstance.Name))

	// Process node-level templates
	numNodes := len(clusterInstance.Spec.Nodes)
	for nodeId, node := range clusterInstance.Spec.Nodes {
		cib.Log.Info(fmt.Sprintf("Processing node-level templates for ClusterInstance %s [node: %d of %d]", clusterInstance.Name, nodeId+1, numNodes))

		// Render node-level templates
		nodeManifests, err := cib.renderTemplates(ctx, c, &clusterInstance, &node)
		if err != nil {
			cib.Log.Info(fmt.Sprintf("encountered error while processing node-level templates for ClusterInstance %s [%d of %d], err: %s", clusterInstance.Name, nodeId+1, numNodes, err.Error()))
			return clusterManifests, err
		}
		cib.Log.Info(fmt.Sprintf("Processed node-level templates for ClusterInstance %s [node: %d of %d]", clusterInstance.Name, nodeId+1, numNodes))

		for _, nodeCR := range nodeManifests {
			if nodeCR != nil {
				clusterManifests = append(clusterManifests, nodeCR)
			}
		}
	}

	return clusterManifests, nil
}

func (cib *ClusterInstanceBuilder) renderTemplates(ctx context.Context, c client.Client, clusterInstance *v1alpha1.ClusterInstance, node *v1alpha1.NodeSpec) ([]interface{}, error) {
	var (
		manifests           []interface{}
		templateRefs        []v1alpha1.TemplateRef
		suppressedManifests []string
	)

	// Determine whether templateRefs and suppressedManifests values are cluster-based or node-based
	if node == nil {
		// use cluster-level values
		templateRefs = clusterInstance.Spec.TemplateRefs
		suppressedManifests = clusterInstance.Spec.SuppressedManifests
	} else {
		// use node-level values
		templateRefs = node.TemplateRefs
		suppressedManifests = node.SuppressedManifests
	}

	for tId, templateRef := range templateRefs {
		cib.Log.Info(fmt.Sprintf("renderTemplates: processing templateRef %d of %d", tId+1, len(templateRefs)))

		templatesConfigMap := &corev1.ConfigMap{}
		if err := c.Get(ctx, types.NamespacedName{
			Name:      templateRef.Name,
			Namespace: templateRef.Namespace,
		}, templatesConfigMap); err != nil {
			cib.Log.Info(fmt.Sprintf("renderTemplates: failed to get ConfigMap, err: %s", err.Error()))
			return manifests, err
		}

		// process Template ConfigMap
		for templateKey, template := range templatesConfigMap.Data {

			clusterData, err := buildClusterData(clusterInstance, node)
			if err != nil {
				cib.Log.Error(err, fmt.Sprintf("renderTemplates: failed to build ClusterInstance data for ClusterInstance %s", clusterInstance.Name))
				return nil, err
			}

			manifest, err := cib.render(templateKey, template, clusterData)
			if err != nil {
				cib.Log.Error(err, fmt.Sprintf("renderTemplates: failed to render templateRef %s for ClusterInstance %s", templateRef.Name, clusterInstance.Name))
				return manifests, err
			}

			if manifest == nil {
				continue
			}

			kind := manifest["kind"].(string)
			if suppressManifest(kind, suppressedManifests) {
				cib.Log.Info(fmt.Sprintf("renderTemplates: suppressing manifest %s for ClusterInstance %s", kind, clusterInstance.Name))
				continue
			}
			if node == nil {
				// Append cluster-level user provided extra annotations if exist
				if extraManifestAnnotations, ok := clusterInstance.Spec.ExtraAnnotationSearch(kind); ok {
					manifest = appendManifestAnnotations(extraManifestAnnotations, manifest)
				}
			} else {
				// Append node-level user provided extra annotations if exist
				if extraManifestAnnotations, ok := node.ExtraAnnotationSearch(kind, &clusterInstance.Spec); ok {
					manifest = appendManifestAnnotations(extraManifestAnnotations, manifest)
				}
			}
			manifests = append(manifests, manifest)
		}
	}
	return manifests, nil
}

func (cib *ClusterInstanceBuilder) render(templateKey, templateStr string, data *ClusterData) (map[string]interface{}, error) {
	renderedTemplate := make(map[string]interface{})
	fMap := funcMap()
	t, err := template.New(templateKey).Funcs(fMap).Parse(templateStr)
	if err != nil {
		return nil, err
	}

	var b bytes.Buffer
	err = t.Execute(&b, data)
	if err != nil {
		return nil, err
	}

	// Ensure there's non-whitespace content
	for _, r := range b.String() {
		if !unicode.IsSpace(r) {
			if err := yaml.Unmarshal(b.Bytes(), &renderedTemplate); err != nil {
				return renderedTemplate, err
			}
			return renderedTemplate, nil
		}
	}
	// Output is all whitespace; return nil instead
	return nil, nil
}
