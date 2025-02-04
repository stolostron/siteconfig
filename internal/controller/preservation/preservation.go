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

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
	"github.com/stolostron/siteconfig/internal/controller/deletion"
)

// Backup preserves ConfigMaps and Secrets based on the specified preservation mode.
// The outcome of each backup is logged and any errors encountered are aggregated.
func Backup(
	ctx context.Context,
	c client.Client,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
) (err error) {
	log = log.Named("Backup")

	if clusterInstance.Spec.Reinstall.PreservationMode == v1alpha1.PreservationModeNone {
		log.Info("Skipping backup since PreservationMode is None")
		return nil
	}

	namespace := clusterInstance.Namespace
	modes := []v1alpha1.PreservationMode{
		clusterInstance.Spec.Reinstall.PreservationMode,
		PreservationModeInternal,
	}

	for _, mode := range modes {

		// Define the label selector for filtering ConfigMaps and Secrets.
		labelSelector, err := buildBackupLabelSelector(mode)
		if err != nil {
			return err
		}
		if labelSelector == nil {
			continue
		}

		baseConfig := config{
			ownerRef:            ci.GenerateOwnedByLabelValue(namespace, clusterInstance.Name),
			reinstallGeneration: clusterInstance.Spec.Reinstall.Generation,
			preservationMode:    mode,
		}

		if err := backupResources(ctx, c, log, labelSelector, namespace, baseConfig); err != nil {
			return err
		}
	}

	return nil
}

// Restore restores ConfigMaps and Secrets from preserved data.
// The outcome of each restore is logged and any errors encountered are aggregated.
func Restore(
	ctx context.Context,
	c client.Client,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
) (err error) {

	log = log.Named("Restore")

	if clusterInstance.Spec.Reinstall.PreservationMode == v1alpha1.PreservationModeNone {
		log.Info("Skipping restore since PreservationMode is None")
		return nil
	}

	namespace := clusterInstance.Namespace
	modes := []v1alpha1.PreservationMode{
		clusterInstance.Spec.Reinstall.PreservationMode,
		PreservationModeInternal,
	}

	for _, mode := range modes {

		// Define the label selector for filtering ConfigMaps and Secrets.
		labelSelector, err := buildRestoreLabelSelector(mode)
		if err != nil {
			return err
		}
		if labelSelector == nil {
			continue
		}

		baseConfig := config{
			ownerRef:            ci.GenerateOwnedByLabelValue(namespace, clusterInstance.Name),
			reinstallGeneration: clusterInstance.Spec.Reinstall.Generation,
			preservationMode:    mode,
		}

		if err := restoreResources(ctx, c, log, labelSelector, namespace, baseConfig); err != nil {
			return err
		}
	}
	return nil
}

// Cleanup deletes preserved ConfigMaps and Secrets.
// The outcome of each deletion is logged and any errors encountered are aggregated.
func Cleanup(
	ctx context.Context,
	c client.Client,
	deletionHandler *deletion.DeletionHandler,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
) (completedCleanup bool, err error) {
	completedCleanup = true

	namespace := clusterInstance.Namespace
	modes := []v1alpha1.PreservationMode{
		clusterInstance.Spec.Reinstall.PreservationMode,
		PreservationModeInternal,
	}

	var data resources
	for _, mode := range modes {

		// Define the label selector for filtering ConfigMaps and Secrets.
		labelSelector, err := buildRestoreLabelSelector(mode)
		if err != nil {
			return false, err
		}
		if labelSelector == nil {
			continue
		}

		listOptions := &client.ListOptions{
			Namespace:     namespace,
			LabelSelector: labelSelector,
		}

		// Retrieve data for restoration
		dataForMode, err := resourcesForPreservation(ctx, c, log, listOptions)
		if err != nil {
			return false, err
		}
		data.configMaps = append(data.configMaps, dataForMode.configMaps...)
		data.secrets = append(data.secrets, dataForMode.secrets...)
	}

	deleteFn := func(rType resourceType, name string) error {

		rawObject := map[string]interface{}{
			"apiVersion": "v1",
			"kind":       string(rType),
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": clusterInstance.Namespace,
			},
		}

		object := ci.RenderedObject{}
		err := object.SetObject(rawObject)
		if err != nil {
			return fmt.Errorf("failed to set object from raw data: %w", err)
		}

		deleted, err := deletionHandler.DeleteObject(ctx, clusterInstance, object, nil)
		if client.IgnoreNotFound(err) != nil {
			log.Warn("Failed to delete", zap.String("object", object.GetResourceId()), zap.Error(err))
			return fmt.Errorf("failed to delete object %s: %w", object.GetResourceId(), err)
		}
		if !deleted {
			completedCleanup = false
		} else {
			log.Info("Successfully deleted", zap.String("object", object.GetResourceId()))
		}
		return nil
	}

	var errs []error

	// Delete ConfigMaps
	for _, name := range data.configMaps {
		if err := deleteFn(configMapResourceType, name); err != nil {
			errs = append(errs, err)
		}
	}

	// Delete Secrets
	for _, name := range data.secrets {
		if err := deleteFn(secretResourceType, name); err != nil {
			errs = append(errs, err)
		}
	}

	return completedCleanup, utilerrors.NewAggregate(errs)
}

func GetPreservedResourceCounts(
	ctx context.Context,
	c client.Client,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
) (clusterIDCount, otherCount int, err error) {

	if clusterInstance.Spec.Reinstall.PreservationMode == v1alpha1.PreservationModeNone {
		return 0, 0, nil
	}

	labelSelector, err := buildRestoreLabelSelector(clusterInstance.Spec.Reinstall.PreservationMode)
	if err != nil || labelSelector == nil {
		return 0, 0, err
	}

	return countMatchingResources(ctx, c, clusterInstance.Namespace, labelSelector, false)
}

func GetRestoredResourceCounts(
	ctx context.Context,
	c client.Client,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
) (clusterIDCount, otherCount int, err error) {

	if clusterInstance.Spec.Reinstall.PreservationMode == v1alpha1.PreservationModeNone {
		return 0, 0, nil
	}

	labelSelector, err := buildBackupLabelSelector(clusterInstance.Spec.Reinstall.PreservationMode)
	if err != nil || labelSelector == nil {
		return 0, 0, err
	}

	return countMatchingResources(ctx, c, clusterInstance.Namespace, labelSelector, true)
}
