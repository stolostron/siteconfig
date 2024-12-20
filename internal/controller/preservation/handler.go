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

package preservation

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/stolostron/siteconfig/api/v1alpha1"
)

// preservedLabel is used to mark ConfigMaps and Secrets that have been backed up
// by the PreservationHandler. It enables identification of preserved resources
// during a restore operation.
const preservedLabel = v1alpha1.Group + "/preserved-data"

// Data encapsulates the names of ConfigMaps and Secrets that are part of preservation operations.
type Data struct {
	ConfigMaps []string
	Secrets    []string
}

// PreservationHandler manages the backup and restore operations for ConfigMaps and Secrets.
// It uses the PreservationMode to determine which resources should be preserved.
type PreservationHandler struct {
	Namespace        string
	PreservationMode v1alpha1.PreservationMode
}

// NewPreservationHandler creates and returns a new PreservationHandler instance.
func NewPreservationHandler(namespace string, preservationMode v1alpha1.PreservationMode) *PreservationHandler {
	if namespace == "" {
		return nil
	}
	return &PreservationHandler{
		Namespace:        namespace,
		PreservationMode: preservationMode,
	}
}

// Backup preserves ConfigMaps and Secrets based on the specified preservation mode.
// The outcome of each backup is logged and any errors encountered are aggregated.
func (p *PreservationHandler) Backup(
	ctx context.Context,
	c client.Client,
	reinstallGeneration string,
	log *zap.Logger) (err error) {

	if reinstallGeneration == "" {
		return fmt.Errorf("reinstallGeneration cannot be an empty string")
	}

	// Define the label selector for filtering ConfigMaps and Secrets.
	labelSelector, err := p.buildBackupLabelSelector()
	if err != nil {
		return err
	}

	if labelSelector == nil {
		return nil
	}

	// labelSelector = labelSelector.Add(*labelRequirement)
	listOptions := &client.ListOptions{
		Namespace:     p.Namespace,
		LabelSelector: labelSelector,
	}

	// Retrieve data for backup
	data, err := retrievePreservationData(ctx, c, listOptions, log)
	if err != nil {
		return err
	}

	var errs []error

	// Back up each ConfigMap.
	for _, name := range data.ConfigMaps {
		preservedName := generateName(name, reinstallGeneration)
		if err := backupConfigMap(ctx, c, p.Namespace, name, preservedName); err != nil {
			log.Error("Failed to back up ConfigMap",
				zap.String("namespace", p.Namespace), zap.String("name", name), zap.Error(err))
			errs = append(errs, err)
		} else {
			log.Info("Successfully backed up ConfigMap",
				zap.String("namespace", p.Namespace), zap.String("name", name), zap.String("backupName", preservedName))
		}
	}

	// Back up each Secret.
	for _, name := range data.Secrets {
		preservedName := generateName(name, reinstallGeneration)
		if err := backupSecret(ctx, c, p.Namespace, name, preservedName); err != nil {
			log.Error("Failed to back up Secret",
				zap.String("namespace", p.Namespace), zap.String("name", name), zap.Error(err))
			errs = append(errs, err)
		} else {
			log.Info("Successfully backed up Secret",
				zap.String("namespace", p.Namespace), zap.String("name", name), zap.String("backupName", preservedName))
		}
	}

	return errors.NewAggregate(errs)
}

// Restore restores ConfigMaps and Secrets from preserved data.
// The outcome of each restore is logged and any errors encountered are aggregated.
func (p *PreservationHandler) Restore(
	ctx context.Context,
	c client.Client,
	log *zap.Logger,
) (err error) {

	// Define the label selector for filtering ConfigMaps and Secrets.
	labelSelector, err := p.buildRestoreLabelSelector()
	if err != nil {
		return err
	}

	if labelSelector == nil {
		return nil
	}

	listOptions := &client.ListOptions{
		Namespace:     p.Namespace,
		LabelSelector: labelSelector,
	}

	// Retrieve data for restoration
	data, err := retrievePreservationData(ctx, c, listOptions, log)
	if err != nil {
		return err
	}

	var errs []error

	// Restore each ConfigMap.
	for _, name := range data.ConfigMaps {
		if err := restoreConfigMap(ctx, c, p.Namespace, name); err != nil {
			log.Error("Failed to restore ConfigMap",
				zap.String("namespace", p.Namespace), zap.String("name", name), zap.Error(err))
			errs = append(errs, err)
		} else {
			log.Info("Successfully restored ConfigMap",
				zap.String("namespace", p.Namespace), zap.String("name", name))
		}
	}

	// Restore each Secret.
	for _, name := range data.Secrets {
		if err := restoreSecret(ctx, c, p.Namespace, name); err != nil {
			log.Error("Failed to restore Secret",
				zap.String("namespace", p.Namespace), zap.String("name", name), zap.Error(err))
			errs = append(errs, err)
		} else {
			log.Info("Successfully restored Secret",
				zap.String("namespace", p.Namespace), zap.String("name", name))
		}
	}

	return errors.NewAggregate(errs)
}

// Cleanup deletes preserved ConfigMaps and Secrets in the Background.
// The outcome of each deletion is logged and any errors encountered are aggregated.
func (p *PreservationHandler) Cleanup(
	ctx context.Context,
	c client.Client,
	log *zap.Logger,
) (err error) {

	// Define the label selector for filtering ConfigMaps and Secrets.
	labelSelector, err := p.buildCleanupLabelSelector()
	if err != nil {
		return err
	}

	if labelSelector == nil {
		return nil
	}

	listOptions := &client.ListOptions{
		Namespace:     p.Namespace,
		LabelSelector: labelSelector,
	}

	// Retrieve data for restoration
	data, err := retrievePreservationData(ctx, c, listOptions, log)
	if err != nil {
		return err
	}

	var (
		configMapType = "ConfigMap"
		secretType    = "Secret"
	)

	delete := func(name, resourceType string) error {
		var obj client.Object
		switch resourceType {
		case configMapType:
			obj = &corev1.ConfigMap{}
		case secretType:
			obj = &corev1.Secret{}
		}

		obj.SetName(name)
		obj.SetNamespace(p.Namespace)

		// Delete resource in the Background
		if err := c.Delete(ctx, obj, &client.DeleteOptions{
			PropagationPolicy: ptr.To(metav1.DeletePropagationBackground),
		}); err != nil {
			if k8serrors.IsNotFound(err) {
				log.Info("Did not find", zap.String("resource", resourceType),
					zap.String("namespace", p.Namespace), zap.String("name", name))
				return nil
			}
			log.Error("Failed to delete", zap.String("resource", resourceType),
				zap.String("namespace", p.Namespace), zap.String("name", name), zap.Error(err))
			return err
		}
		log.Info("Successfully deleted", zap.String("resource", resourceType),
			zap.String("namespace", p.Namespace), zap.String("name", name))
		return nil
	}

	var errs []error

	// Delete ConfigMaps
	for _, name := range data.ConfigMaps {
		if err := delete(name, configMapType); err != nil {
			errs = append(errs, err)
		}
	}

	// Delete Secrets
	for _, name := range data.Secrets {
		if err := delete(name, secretType); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.NewAggregate(errs)
}

// buildBackupLabelSelector constructs a label selector for backup operations based on the PreservationMode.
// Returns nil for PreservationModeNone as no preservation is required.
func (p *PreservationHandler) buildBackupLabelSelector() (labels.Selector, error) {
	var (
		requirement *labels.Requirement
		err         error
	)

	switch p.PreservationMode {
	case v1alpha1.PreservationModeNone:
		// No preservation required.
		return nil, nil
	case v1alpha1.PreservationModeAll:
		// Select all resources with the PreservationLabelKey.
		requirement, err = labels.NewRequirement(
			v1alpha1.PreservationLabelKey,
			selection.Exists,
			nil,
		)
	case v1alpha1.PreservationModeClusterIdentity:
		// Select resources with PreservationLabelKey set to ClusterIdentityLabelValue.
		requirement, err = labels.NewRequirement(
			v1alpha1.PreservationLabelKey,
			selection.Equals,
			[]string{v1alpha1.ClusterIdentityLabelValue},
		)
	default:
		// Handle unknown PreservationMode.
		return nil, fmt.Errorf("unknown PreservationMode: %v", p.PreservationMode)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create backup label selector: %w", err)
	}

	// Create and return the label selector with the constructed requirement.
	return labels.NewSelector().Add(*requirement), nil
}

// buildRestoreLabelSelector constructs a label selector for restore operations based on the PreservationMode.
// Returns nil for PreservationModeNone as no preservation is required.
func (p *PreservationHandler) buildRestoreLabelSelector() (labels.Selector, error) {
	var (
		requirement *labels.Requirement
		err         error
	)

	switch p.PreservationMode {
	case v1alpha1.PreservationModeNone:
		// No restoration required.
		return nil, nil
	case v1alpha1.PreservationModeAll, v1alpha1.PreservationModeClusterIdentity:
		// Select all resources with the backup preservation label.
		requirement, err = labels.NewRequirement(
			preservedLabel,
			selection.Exists,
			nil,
		)
	default:
		// Handle unknown PreservationMode.
		return nil, fmt.Errorf("unknown PreservationMode: %v", p.PreservationMode)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create restore label selector: %w", err)
	}

	// Create and return the label selector with the constructed requirement.
	return labels.NewSelector().Add(*requirement), nil
}

// buildCleanupLabelSelector constructs a label selector for cleanup operations.
func (p *PreservationHandler) buildCleanupLabelSelector() (labels.Selector, error) {
	requirement, err := labels.NewRequirement(
		preservedLabel,
		selection.Exists,
		nil,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to create cleanup label selector: %w", err)
	}

	// Create and return the label selector with the constructed requirement.
	return labels.NewSelector().Add(*requirement), nil
}
