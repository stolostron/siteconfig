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

	"go.uber.org/zap"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/errors"

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

	namespace := clusterInstance.Namespace
	ownerRef := ci.GenerateOwnedByLabelValue(namespace, clusterInstance.Name)
	reinstallGeneration := clusterInstance.Spec.Reinstall.Generation
	mode := clusterInstance.Spec.Reinstall.PreservationMode

	// Define the label selector for filtering ConfigMaps and Secrets.
	labelSelector, err := buildBackupLabelSelector(mode)
	if err != nil {
		return err
	}
	if labelSelector == nil {
		log.Info("Nothing to backup")
		return nil
	}

	listOptions := &client.ListOptions{
		Namespace:     namespace,
		LabelSelector: labelSelector,
	}

	// Retrieve data for backup
	data, err := resourcesForPreservation(ctx, c, log, listOptions)
	if err != nil {
		return err
	}

	var errs []error

	backupFn := func(rType resourceType, resourceNames []string) {
		for _, name := range resourceNames {
			preservedName := generateBackupName(rType, name, reinstallGeneration)
			config := config{
				resourceKey:         types.NamespacedName{Namespace: namespace, Name: name},
				resourceType:        rType,
				ownerRef:            ownerRef,
				reinstallGeneration: reinstallGeneration,
				preservationMode:    mode,
			}

			if err := backupResource(ctx, c, log, preservedName, config); err != nil {
				log.Error("Failed to backup resource",
					zap.String("kind", string(rType)),
					zap.String("namespace", namespace),
					zap.String("name", name),
					zap.Error(err))
				errs = append(errs, err)
			} else {
				log.Debug("Successfully backed-up resource",
					zap.String("kind", string(rType)),
					zap.String("namespace", namespace),
					zap.String("name", name))
			}
		}
	}

	// Backup each ConfigMap.
	backupFn(configMapResourceType, data.configMaps)

	// Backup each Secret.
	backupFn(secretResourceType, data.secrets)

	return errors.NewAggregate(errs)
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

	namespace := clusterInstance.Namespace
	ownerRef := ci.GenerateOwnedByLabelValue(namespace, clusterInstance.Name)
	reinstallGeneration := clusterInstance.Spec.Reinstall.Generation
	mode := clusterInstance.Spec.Reinstall.PreservationMode

	// Define the label selector for filtering ConfigMaps and Secrets.
	labelSelector, err := buildRestoreLabelSelector(mode)
	if err != nil {
		return err
	}
	if labelSelector == nil {
		log.Info("Nothing to restore")
		return nil
	}

	listOptions := &client.ListOptions{
		Namespace:     namespace,
		LabelSelector: labelSelector,
	}

	// Retrieve data for restoration
	data, err := resourcesForPreservation(ctx, c, log, listOptions)
	if err != nil {
		return err
	}

	var errs []error

	restoreFn := func(resourceNames []string) {
		for _, name := range resourceNames {
			config := config{
				resourceKey:         types.NamespacedName{Namespace: namespace, Name: name},
				ownerRef:            ownerRef,
				reinstallGeneration: reinstallGeneration,
				preservationMode:    mode,
			}
			if err := restoreResource(ctx, c, log, config); err != nil {
				log.Error("Failed to restore resource",
					zap.String("namespace", namespace),
					zap.String("name", name),
					zap.Error(err))
				errs = append(errs, err)
			} else {
				log.Debug("Successfully restored resource",
					zap.String("namespace", namespace),
					zap.String("name", name))
			}
		}
	}

	// Restore ConfigMaps.
	restoreFn(data.configMaps)

	// Restore Secrets.
	restoreFn(data.secrets)

	return errors.NewAggregate(errs)
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

	// Define the label selector for filtering ConfigMaps and Secrets.
	labelSelector, err := buildRestoreLabelSelector(clusterInstance.Spec.Reinstall.PreservationMode)
	if err != nil {
		return false, err
	}

	if labelSelector == nil {
		return true, nil
	}

	listOptions := &client.ListOptions{
		Namespace:     clusterInstance.Namespace,
		LabelSelector: labelSelector,
	}

	// Retrieve data for restoration
	data, err := resourcesForPreservation(ctx, c, log, listOptions)
	if err != nil {
		return false, err
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
			return err
		}

		deleted, err := deletionHandler.DeleteObject(ctx, clusterInstance, object, nil)
		if client.IgnoreNotFound(err) != nil {
			log.Warn("Failed to delete", zap.String("object", object.GetResourceId()), zap.Error(err))
			return err
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

	return completedCleanup, errors.NewAggregate(errs)
}
