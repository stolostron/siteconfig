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

	"github.com/sakhoury/siteconfig/api/v1alpha1"
)

const (
	WaveAnnotation        = v1alpha1.Group + "/sync-wave"
	DefaultWaveAnnotation = "0"
)

type SiteConfigBuilder struct {
	Log logr.Logger
}

func NewSiteConfigBuilder(pLog logr.Logger) *SiteConfigBuilder {
	scBuilder := SiteConfigBuilder{Log: pLog}

	return &scBuilder
}

func (scbuilder *SiteConfigBuilder) ProcessTemplates(ctx context.Context, c client.Client, siteConfig v1alpha1.SiteConfig) ([]interface{}, error) {
	scbuilder.Log.Info(fmt.Sprintf("Processing cluster-level templates for SiteConfig %s", siteConfig.Name))

	// Render cluster-level templates
	siteManifests, err := scbuilder.renderTemplates(ctx, c, &siteConfig, nil)
	if err != nil {
		scbuilder.Log.Info(fmt.Sprintf("encountered error while processing cluster-level templates for SiteConfig %s, err: %s", siteConfig.Name, err.Error()))
		return siteManifests, err
	}
	scbuilder.Log.Info(fmt.Sprintf("Processed cluster-level templates for SiteConfig %s", siteConfig.Name))

	// Process node-level templates
	numNodes := len(siteConfig.Spec.Nodes)
	for nodeId, node := range siteConfig.Spec.Nodes {
		scbuilder.Log.Info(fmt.Sprintf("Processing node-level templates for SiteConfig %s [node: %d of %d]", siteConfig.Name, nodeId+1, numNodes))

		// Render node-level templates
		nodeManifests, err := scbuilder.renderTemplates(ctx, c, &siteConfig, &node)
		if err != nil {
			scbuilder.Log.Info(fmt.Sprintf("encountered error while processing node-level templates for SiteConfig %s [%d of %d], err: %s", siteConfig.Name, nodeId+1, numNodes, err.Error()))
			return siteManifests, err
		}
		scbuilder.Log.Info(fmt.Sprintf("Processed node-level templates for SiteConfig %s [node: %d of %d]", siteConfig.Name, nodeId+1, numNodes))

		for _, nodeCR := range nodeManifests {
			if nodeCR != nil {
				siteManifests = append(siteManifests, nodeCR)
			}
		}
	}

	return siteManifests, nil
}

func (scbuilder *SiteConfigBuilder) renderTemplates(ctx context.Context, c client.Client, siteConfig *v1alpha1.SiteConfig, node *v1alpha1.NodeSpec) ([]interface{}, error) {
	var (
		manifests           []interface{}
		templateRefs        []v1alpha1.TemplateRef
		suppressedManifests []string
	)

	// Determine whether templateRefs and suppressedManifests values are cluster-based or node-based
	if node == nil {
		// use cluster-level values
		templateRefs = siteConfig.Spec.TemplateRefs
		suppressedManifests = siteConfig.Spec.SuppressedManifests
	} else {
		// use node-level values
		templateRefs = node.TemplateRefs
		suppressedManifests = node.SuppressedManifests
	}

	for tId, templateRef := range templateRefs {
		scbuilder.Log.Info(fmt.Sprintf("renderTemplates: processing templateRef %d of %d", tId+1, len(templateRefs)))

		templatesConfigMap := &corev1.ConfigMap{}
		if err := c.Get(ctx, types.NamespacedName{
			Name:      templateRef.Name,
			Namespace: templateRef.Namespace,
		}, templatesConfigMap); err != nil {
			scbuilder.Log.Info(fmt.Sprintf("renderTemplates: failed to get ConfigMap, err: %s", err.Error()))
			return manifests, err
		}

		// process Template ConfigMap
		for templateKey, template := range templatesConfigMap.Data {

			siteData, err := buildSiteData(siteConfig, node)
			if err != nil {
				scbuilder.Log.Error(err, fmt.Sprintf("renderTemplates: failed to build SiteConfig data for SiteConfig %s", siteConfig.Name))
				return nil, err
			}

			manifest, err := scbuilder.render(templateKey, template, siteData)
			if err != nil {
				scbuilder.Log.Error(err, fmt.Sprintf("renderTemplates: failed to render templateRef %s for SiteConfig %s", templateRef.Name, siteConfig.Name))
				return manifests, err
			}

			if manifest == nil {
				continue
			}

			kind := manifest["kind"].(string)
			if suppressManifest(kind, suppressedManifests) {
				scbuilder.Log.Info(fmt.Sprintf("renderTemplates: suppressing manifest %s for SiteConfig %s", kind, siteConfig.Name))
				continue
			}
			if node == nil {
				// Append cluster-level user provided extra annotations if exist
				if extraManifestAnnotations, ok := siteConfig.Spec.ExtraAnnotationSearch(kind); ok {
					manifest = appendManifestAnnotations(extraManifestAnnotations, manifest)
				}
			} else {
				// Append node-level user provided extra annotations if exist
				if extraManifestAnnotations, ok := node.ExtraAnnotationSearch(kind, &siteConfig.Spec); ok {
					manifest = appendManifestAnnotations(extraManifestAnnotations, manifest)
				}
			}
			manifests = append(manifests, manifest)
		}
	}
	return manifests, nil
}

func (scbuilder *SiteConfigBuilder) render(templateKey, templateStr string, data *SiteData) (map[string]interface{}, error) {
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
