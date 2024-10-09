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

	"go.uber.org/zap"
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

type TemplateEngine struct{}

func NewTemplateEngine() *TemplateEngine {
	return &TemplateEngine{}
}

func (te *TemplateEngine) ProcessTemplates(
	ctx context.Context,
	c client.Client,
	log *zap.Logger,
	clusterInstance v1alpha1.ClusterInstance,
) (RenderedObjectCollection, error) {

	log = log.Named("ProcessTemplates")

	var renderedObjects RenderedObjectCollection
	log.Info("Started processing cluster-level install templates")

	// Render cluster-level install templates
	clusterObjects, err := te.renderTemplates(ctx, c, log, &clusterInstance, nil)
	if err != nil {
		log.Error("Encountered error while processing cluster-level install templates", zap.Error(err))
		return renderedObjects, err
	}

	if err := renderedObjects.AddObjects(clusterObjects); err != nil {
		return renderedObjects, err
	}
	log.Info("Finished processing cluster-level install templates")

	// Process node-level install templates
	numNodes := len(clusterInstance.Spec.Nodes)
	for nodeId, node := range clusterInstance.Spec.Nodes {
		log.Sugar().Infof("Started processing node-level install templates [node: %d of %d]", nodeId+1, numNodes)

		// Render node-level templates
		nodeObjects, err := te.renderTemplates(ctx, c, log, &clusterInstance, &node)
		if err != nil {
			log.Sugar().Errorf(
				"Encountered error while processing node-level install templates [node: %d of %d], err: %v",
				nodeId+1, numNodes, err)
			return renderedObjects, err
		}

		if err := renderedObjects.AddObjects(nodeObjects); err != nil {
			return renderedObjects, err
		}

		log.Sugar().Infof("Finished processing node-level install templates [node: %d of %d]", nodeId+1, numNodes)
	}

	return renderedObjects, nil
}

func (te *TemplateEngine) renderTemplates(
	ctx context.Context,
	c client.Client,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
	node *v1alpha1.NodeSpec,
) ([]RenderedObject, error) {

	var (
		renderedObjects []RenderedObject
		templateRefs    []v1alpha1.TemplateRef
	)

	// Determine whether templateRefs are cluster-based or node-based
	if node == nil {
		// use cluster-level values
		templateRefs = clusterInstance.Spec.TemplateRefs
	} else {
		// use node-level values
		templateRefs = node.TemplateRefs
	}

	log = log.Named("renderTemplates")

	for tId, templateRef := range templateRefs {
		log.Sugar().Infof("Processing templateRef %d of %d", tId+1, len(templateRefs))

		templatesConfigMap := &corev1.ConfigMap{}
		if err := c.Get(ctx, types.NamespacedName{
			Name:      templateRef.Name,
			Namespace: templateRef.Namespace,
		}, templatesConfigMap); err != nil {
			log.Error("Failed to get ConfigMap", zap.Error(err))
			return renderedObjects, err
		}

		// process Template ConfigMap
		for templateKey, template := range templatesConfigMap.Data {

			object, err := te.renderManifestFromTemplate(
				log,
				clusterInstance,
				node,
				templateRef.Name,
				templateKey,
				template)
			if err != nil {
				return renderedObjects, err
			}
			renderedObjects = append(renderedObjects, object)
		}
	}
	return renderedObjects, nil
}

func appendAnnotationsAndLabels(
	clusterInstance *v1alpha1.ClusterInstance,
	node *v1alpha1.NodeSpec,
	manifest map[string]interface{},
	kind string,
) map[string]interface{} {
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
	return manifest
}

func (te *TemplateEngine) renderManifestFromTemplate(
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
	node *v1alpha1.NodeSpec,
	templateRefName, templateKey, template string,
) (RenderedObject, error) {

	var object RenderedObject

	log = log.Named("renderManifestFromTemplate")

	clusterData, err := buildClusterData(clusterInstance, node)
	if err != nil {
		log.Error("Failed to build ClusterInstance data", zap.Error(err))
		return object, err
	}

	manifest, err := te.render(templateKey, template, clusterData)
	if err != nil {
		log.Error(fmt.Sprintf("Failed to render templateRef %s", templateRefName), zap.Error(err))
		return object, err
	}
	if manifest == nil {
		return object, nil
	}

	if err := object.SetObject(manifest); err != nil {
		log.Error(fmt.Sprintf("Failed to parse rendered template templateRef %s", templateRefName), zap.Error(err))
		return object, err
	}

	apiVersion := object.GetAPIVersion()
	kind := object.GetKind()
	name := object.GetName()
	namespace := object.GetNamespace()

	// Default action is to render the manifest
	object.action = actionRender

	// Determine if manifest should be pruned or suppressed
	suppressManifestLogMsg := fmt.Sprintf("Suppressed manifest %s", GetResourceId(name, namespace, kind))
	pruneList := clusterInstance.Spec.PruneManifests
	suppressManifestsList := clusterInstance.Spec.SuppressedManifests
	if node != nil {
		pruneList = append(pruneList, node.PruneManifests...)
		suppressManifestsList = append(suppressManifestsList, node.SuppressedManifests...)
	}
	if pruneManifest(v1alpha1.ResourceRef{APIVersion: apiVersion, Kind: kind}, pruneList) {
		object.action = actionPrune
		log.Debug(suppressManifestLogMsg)
		return object, nil
	}
	if suppressManifest(kind, suppressManifestsList) {
		object.action = actionSuppress
		log.Debug(suppressManifestLogMsg)
		return object, nil
	}

	// Append Annotations and Labels to rendered manifest
	updatedManifest := appendAnnotationsAndLabels(clusterInstance, node, object.GetObject().Object, kind)

	// Add owned-by label
	updatedManifest = appendManifestLabels(map[string]string{
		OwnedByLabel: GenerateOwnedByLabelValue(clusterInstance.Namespace, clusterInstance.Name),
	}, updatedManifest)

	// Update the rendered object with the labels and annotations applied above
	if err := object.SetObject(updatedManifest); err != nil {
		log.Error(fmt.Sprintf("Failed to parse rendered template templateRef %s", templateRefName), zap.Error(err))
		return object, err
	}

	return object, nil
}

func validateRenderedTemplate(manifest map[string]interface{}, templateKey string) error {
	if _, ok := manifest["apiVersion"].(string); !ok {
		return fmt.Errorf("missing apiVersion in template %s", templateKey)
	}

	if _, ok := manifest["kind"].(string); !ok {
		return fmt.Errorf("missing kind in template %s", templateKey)
	}

	if metadata, ok := manifest["metadata"].(map[string]interface{}); ok {
		if _, ok := metadata["name"].(string); !ok {
			return fmt.Errorf("missing metadata.name in template %s", templateKey)
		}
	} else {
		return fmt.Errorf("missing metadata in template %s", templateKey)
	}

	// all validations passed
	return nil
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
			return renderedTemplate, validateRenderedTemplate(renderedTemplate, templateKey)
		}
	}

	// Output is all whitespace; return nil instead
	return nil, nil
}
