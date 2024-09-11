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
	OwnedByLabel          = v1alpha1.Group + "/owned-by"
)

type TemplateEngine struct {
	Log logr.Logger
}

func NewTemplateEngine(pLog logr.Logger) *TemplateEngine {
	return &TemplateEngine{Log: pLog}
}

func (te *TemplateEngine) ProcessTemplates(
	ctx context.Context,
	c client.Client,
	clusterInstance v1alpha1.ClusterInstance,
) ([]interface{}, error) {

	te.Log.Info(fmt.Sprintf("Processing cluster-level templates for ClusterInstance %s", clusterInstance.Name))

	// Render cluster-level templates
	clusterManifests, err := te.renderTemplates(ctx, c, &clusterInstance, nil)
	if err != nil {
		te.Log.Info(
			fmt.Sprintf(
				"encountered error while processing cluster-level templates for ClusterInstance %s, err: %s",
				clusterInstance.Name, err.Error()))
		return clusterManifests, err
	}
	te.Log.Info(fmt.Sprintf("Processed cluster-level templates for ClusterInstance %s", clusterInstance.Name))

	// Process node-level templates
	numNodes := len(clusterInstance.Spec.Nodes)
	for nodeId, node := range clusterInstance.Spec.Nodes {
		te.Log.Info(
			fmt.Sprintf(
				"Processing node-level templates for ClusterInstance %s [node: %d of %d]",
				clusterInstance.Name, nodeId+1, numNodes))

		// Render node-level templates
		nodeManifests, err := te.renderTemplates(ctx, c, &clusterInstance, &node)
		if err != nil {
			te.Log.Info(
				fmt.Sprintf(
					"encountered error while processing node-level templates for ClusterInstance %s [%d of %d], err: %s",
					clusterInstance.Name, nodeId+1, numNodes, err.Error()))
			return clusterManifests, err
		}
		te.Log.Info(fmt.Sprintf(
			"Processed node-level templates for ClusterInstance %s [node: %d of %d]",
			clusterInstance.Name, nodeId+1, numNodes))

		for _, nodeCR := range nodeManifests {
			if nodeCR != nil {
				clusterManifests = append(clusterManifests, nodeCR)
			}
		}
	}

	return clusterManifests, nil
}

func (te *TemplateEngine) renderTemplates(
	ctx context.Context,
	c client.Client,
	clusterInstance *v1alpha1.ClusterInstance,
	node *v1alpha1.NodeSpec,
) ([]interface{}, error) {

	var (
		manifests    []interface{}
		templateRefs []v1alpha1.TemplateRef
	)

	// Determine whether templateRefs are cluster-based or node-based
	if node == nil {
		// use cluster-level values
		templateRefs = clusterInstance.Spec.TemplateRefs
	} else {
		// use node-level values
		templateRefs = node.TemplateRefs
	}

	for tId, templateRef := range templateRefs {
		te.Log.Info(fmt.Sprintf("renderTemplates: processing templateRef %d of %d", tId+1, len(templateRefs)))

		templatesConfigMap := &corev1.ConfigMap{}
		if err := c.Get(ctx, types.NamespacedName{
			Name:      templateRef.Name,
			Namespace: templateRef.Namespace,
		}, templatesConfigMap); err != nil {
			te.Log.Info(fmt.Sprintf("renderTemplates: failed to get ConfigMap, err: %s", err.Error()))
			return manifests, err
		}

		// process Template ConfigMap
		for templateKey, template := range templatesConfigMap.Data {

			manifest, err := te.renderManifestFromTemplate(
				clusterInstance,
				node,
				templateRef.Name,
				templateKey,
				template)
			if err != nil {
				return nil, err
			}
			if manifest != nil {
				manifests = append(manifests, manifest)
			}
		}
	}
	return manifests, nil
}

func (te *TemplateEngine) renderManifestFromTemplate(
	clusterInstance *v1alpha1.ClusterInstance,
	node *v1alpha1.NodeSpec,
	templateRefName, templateKey, template string,
) (map[string]interface{}, error) {

	clusterData, err := buildClusterData(clusterInstance, node)
	if err != nil {
		te.Log.Error(err,
			fmt.Sprintf("renderTemplates: failed to build ClusterInstance data for ClusterInstance %s",
				clusterInstance.Name))
		return nil, err
	}

	manifest, err := te.render(templateKey, template, clusterData)
	if err != nil {
		te.Log.Error(err,
			fmt.Sprintf("renderTemplates: failed to render templateRef %s for ClusterInstance %s",
				templateRefName, clusterInstance.Name))
		return nil, err
	}

	if manifest == nil {
		return nil, nil
	}

	var (
		kind string
		ok   bool
	)
	if kind, ok = manifest["kind"].(string); !ok {
		return nil, fmt.Errorf("missing kind in template %s", templateKey)
	}

	suppressedManifests := clusterInstance.Spec.SuppressedManifests
	if node != nil {
		suppressedManifests = append(suppressedManifests, node.SuppressedManifests...)
	}

	if suppressManifest(kind, suppressedManifests) {
		te.Log.Info(fmt.Sprintf("renderTemplates: suppressing manifest %s for ClusterInstance %s",
			kind, clusterInstance.Name))
		return nil, nil
	}

	// Add owned-by label
	manifest = appendManifestLabels(map[string]string{
		OwnedByLabel: GenerateOwnedByLabelValue(clusterInstance.Namespace, clusterInstance.Name),
	}, manifest)

	if node == nil {
		// Append cluster-level user provided extra annotations if exist
		if extraManifestAnnotations, ok := clusterInstance.Spec.ExtraAnnotationSearch(kind); ok {
			manifest = appendManifestAnnotations(extraManifestAnnotations, manifest)
		}

		// Append cluster-level user provided extra labels if exist
		if extraManifestLabels, ok := clusterInstance.Spec.ExtraLabelSearch(kind); ok {
			manifest = appendManifestLabels(extraManifestLabels, manifest)
		}
	} else {
		// Append node-level user provided extra annotations if exist
		if extraManifestAnnotations, ok := node.ExtraAnnotationSearch(kind, &clusterInstance.Spec); ok {
			manifest = appendManifestAnnotations(extraManifestAnnotations, manifest)
		}

		// Append node-level user provided extra labels if exist
		if extraManifestLabels, ok := node.ExtraLabelSearch(kind, &clusterInstance.Spec); ok {
			manifest = appendManifestLabels(extraManifestLabels, manifest)
		}
	}

	return manifest, nil
}

func (te *TemplateEngine) render(templateKey, templateStr string, data *ClusterData) (map[string]interface{}, error) {

	renderedTemplate := make(map[string]interface{})
	fMap := funcMap()
	t, err := template.New(templateKey).Funcs(fMap).Parse(templateStr)
	if err != nil {
		return nil, err
	}

	var buffer bytes.Buffer
	err = t.Execute(&buffer, data)
	if err != nil {
		return nil, err
	}

	// Ensure there's non-whitespace content
	for _, r := range buffer.String() {
		if !unicode.IsSpace(r) {
			if err := yaml.Unmarshal(buffer.Bytes(), &renderedTemplate); err != nil {
				return renderedTemplate, err
			}
			return renderedTemplate, nil
		}
	}

	// Output is all whitespace; return nil instead
	return nil, nil
}
