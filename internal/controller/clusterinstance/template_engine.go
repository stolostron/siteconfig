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
	"errors"
	"fmt"
	"io"
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
		nodeCopy := node

		// Render node-level templates
		nodeObjects, err := te.renderTemplates(ctx, c, log, &clusterInstance, &nodeCopy)
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
			return renderedObjects, fmt.Errorf("failed to get ConfigMap %s/%s: %w", templateRef.Namespace, templateRef.Name, err)
		}

		// process Template ConfigMap
		for templateKey, template := range templatesConfigMap.Data {

			objects, err := te.renderManifestFromTemplate(
				ctx,
				c,
				log,
				clusterInstance,
				node,
				templateRef.Name,
				templateKey,
				template)
			if err != nil {
				return renderedObjects, err
			}
			renderedObjects = append(renderedObjects, objects...)
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
	ctx context.Context,
	c client.Client,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
	node *v1alpha1.NodeSpec,
	templateRefName, templateKey, template string,
) ([]RenderedObject, error) {

	var objects []RenderedObject

	log = log.Named("renderManifestFromTemplate")

	clusterData, err := buildClusterData(ctx, c, clusterInstance, node)
	if err != nil {
		log.Error("Failed to build ClusterInstance data", zap.Error(err))
		return objects, err
	}

	manifests, err := te.render(templateKey, template, clusterData)
	if err != nil {
		log.Error(fmt.Sprintf("Failed to render templateRef %s", templateRefName), zap.Error(err))
		return objects, err
	}
	if len(manifests) == 0 {
		return objects, nil
	}

	pruneList := clusterInstance.Spec.PruneManifests
	suppressManifestsList := clusterInstance.Spec.SuppressedManifests
	if node != nil {
		pruneList = append(pruneList, node.PruneManifests...)
		suppressManifestsList = append(suppressManifestsList, node.SuppressedManifests...)
	}

	// Process each manifest (templates can render multiple YAML documents)
	for _, manifest := range manifests {
		var object RenderedObject

		if err := object.SetObject(manifest); err != nil {
			// Object decoder errors can contain the entire manifest, including secrets.
			log.Error("Failed to parse rendered template",
				zap.String("templateRef", templateRefName), zap.String("templateKey", templateKey))
			return objects, fmt.Errorf("failed to parse rendered template %s", templateKey)
		}

		apiVersion := object.GetAPIVersion()
		kind := object.GetKind()
		name := object.GetName()
		namespace := object.GetNamespace()

		// Default action is to render the manifest
		object.action = actionRender

		// Determine if manifest should be pruned or suppressed
		resourceId := GetResourceId(name, namespace, kind)
		if pruneManifest(v1alpha1.ResourceRef{APIVersion: apiVersion, Kind: kind}, pruneList) {
			object.action = actionPrune
			log.Debug("Pruned manifest", zap.String("resource", resourceId))
			objects = append(objects, object)
			continue
		}
		if suppressManifest(kind, suppressManifestsList) {
			object.action = actionSuppress
			log.Debug("Suppressed manifest", zap.String("resource", resourceId))
			objects = append(objects, object)
			continue
		}

		// Append Annotations and Labels to rendered manifest
		updatedManifest := appendAnnotationsAndLabels(clusterInstance, node, object.GetObject().Object, kind)

		// Add owned-by label
		updatedManifest = appendManifestLabels(map[string]string{
			OwnedByLabel: GenerateOwnedByLabelValue(clusterInstance.Namespace, clusterInstance.Name),
		}, updatedManifest)

		// Update the rendered object with the labels and annotations applied above
		if err := object.SetObject(updatedManifest); err != nil {
			log.Error("Failed to parse rendered template",
				zap.String("templateRef", templateRefName), zap.String("templateKey", templateKey))
			return objects, fmt.Errorf("failed to parse rendered template %s", templateKey)
		}

		objects = append(objects, object)
	}

	return objects, nil
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

func ParseTemplate(templateKey, templateStr string) (*template.Template, error) {
	fMap := funcMap()
	t, err := template.New(templateKey).Funcs(fMap).Parse(templateStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse template: %w", err)
	}
	return t, nil
}

func (te *TemplateEngine) render(templateKey, templateStr string, data *ClusterData) ([]map[string]interface{}, error) {

	var renderedTemplates []map[string]interface{}
	t, err := ParseTemplate(templateKey, templateStr)
	if err != nil {
		return nil, fmt.Errorf("encountered an error parsing template %s: %w", templateKey, err)
	}

	var buffer bytes.Buffer
	err = t.Execute(&buffer, data)
	if err != nil {
		return nil, fmt.Errorf("failed to execute template %s: %w", templateKey, err)
	}

	// Check if there's non-whitespace content
	hasContent := false
	for _, r := range buffer.String() {
		if !unicode.IsSpace(r) {
			hasContent = true
			break
		}
	}
	if !hasContent {
		// Output is all whitespace; return empty slice
		return renderedTemplates, nil
	}

	// Use yaml.Decoder to handle multiple YAML documents separated by ---
	decoder := yaml.NewDecoder(&buffer)
	// Count every Decode call, including empty and null documents, using a one-based index.
	for documentIndex := 1; ; documentIndex++ {
		var doc map[string]interface{}
		err := decoder.Decode(&doc)
		if err != nil {
			// io.EOF is expected when we reach the end of the document stream
			if errors.Is(err, io.EOF) {
				break
			}
			// Decoder errors can include rendered values, including secrets. Return
			// only the document location so callers can safely log the error.
			return renderedTemplates, fmt.Errorf("failed to unmarshal YAML document %d in template %s",
				documentIndex, templateKey)
		}

		// Consecutive or trailing separators can produce empty documents; they are not manifests.
		if doc == nil {
			continue
		}

		// Validate the document
		if err := validateRenderedTemplate(doc, templateKey); err != nil {
			return renderedTemplates, fmt.Errorf("failed to validate YAML document %d: %w", documentIndex, err)
		}

		renderedTemplates = append(renderedTemplates, doc)
	}

	return renderedTemplates, nil
}
