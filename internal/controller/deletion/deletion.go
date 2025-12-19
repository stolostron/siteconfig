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

package deletion

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
	"github.com/stolostron/siteconfig/internal/controller/conditions"
	cierrors "github.com/stolostron/siteconfig/internal/controller/errors"
)

const DefaultDeletionTimeout = 30 * time.Minute

type DeletionHandler struct {
	Client client.Client
	Logger *zap.Logger
}

// DeleteRenderedObjects deletes all rendered objects except those explicitly excluded.
func (d *DeletionHandler) DeleteRenderedObjects(
	ctx context.Context,
	clusterInstance *v1alpha1.ClusterInstance,
	excludeObjects []ci.RenderedObject,
	deletionTimeout *time.Duration,
) (bool, error) {
	log := d.Logger.With(
		zap.String("ClusterInstance", fmt.Sprintf("%s/%s", clusterInstance.Namespace, clusterInstance.Name)),
	)

	if len(clusterInstance.Status.ManifestsRendered) == 0 {
		log.Info("The manifestsRendered list is empty, nothing to delete")
		return true, nil
	}

	objects := objectsForClusterInstance(clusterInstance)
	return d.DeleteObjects(ctx, clusterInstance, objects, excludeObjects, deletionTimeout)
}

// DeleteObjects deletes the ClusterInstance rendered manifests by syncWaveGroup.
// SyncWaves are processed in descending order, that is, highest syncWaveGroup objects are first
// deleted and the lowest syncWaveGroups are deleted last.
// Returns: bool, error
// bool: indicates whether the deletion of all the specified (and owned) objects are successful
// error: error encountered during deletion attempt
func (d *DeletionHandler) DeleteObjects(
	ctx context.Context,
	clusterInstance *v1alpha1.ClusterInstance,
	objects, excludeObjects []ci.RenderedObject,
	deletionTimeout *time.Duration,
) (deletionCompleted bool, err error) {
	log := d.Logger.With(
		zap.String("ClusterInstance", fmt.Sprintf("%s/%s", clusterInstance.Namespace, clusterInstance.Name)),
	)

	deletionCompleted = false
	err = nil

	manifestsRendered := append([]v1alpha1.ManifestReference{}, clusterInstance.Status.ManifestsRendered...)

	objectSyncWaveMap := ci.SyncWaveMap{}
	if err := objectSyncWaveMap.AddObjects(objects); err != nil {
		log.Warn("Incurred error(s) collecting objects for deletion", zap.Error(err))
	}

	var (
		manifestsDeleted []v1alpha1.ManifestReference
		errs             []error
		deleted          bool
	)

	defer func() {
		patch := client.MergeFrom(clusterInstance.DeepCopy())
		// Update ManifestsRendered if there are changes
		manifestsRendered = filterOutDeletedManifests(manifestsRendered, manifestsDeleted)
		if !equality.Semantic.DeepEqual(clusterInstance.Status.ManifestsRendered, manifestsRendered) {
			clusterInstance.Status.ManifestsRendered = manifestsRendered
			if updateErr := conditions.PatchCIStatus(ctx, d.Client, clusterInstance, patch); updateErr != nil {
				log.Error("Failed to update Status.ManifestsRendered", zap.Error(updateErr))
				errs = append(errs, updateErr)
			}
		}
		err = utilerrors.NewAggregate(errs)
	}()

	for _, syncWave := range objectSyncWaveMap.GetDescendingSyncWaves() {
		log.Sugar().Debugf("Processing syncwave '%d' for deletion", syncWave)
		syncWaveGroupDeleted := true

		for _, object := range objectSyncWaveMap.GetObjectsForSyncWave(syncWave) {
			resourceId := object.GetResourceId()
			objectCopy := object

			if _, err = ci.IndexOfObjectByIdentity(&objectCopy, excludeObjects); err == nil {
				log.Sugar().Debugf("Excluding object (%s) from deletion", resourceId)
				continue
			}

			index, err1 := v1alpha1.IndexOfManifestByIdentity(object.ManifestReference(), manifestsRendered)
			if err1 != nil {
				log.Sugar().Debugf(
					"Skipping deletion of object (%s) not found in ClusterInstance.Status.ManifestsRendered",
					resourceId)
				continue
			}

			manifest := &manifestsRendered[index]
			deleted, err = d.DeleteObject(ctx, clusterInstance, object, deletionTimeout)
			if deleted || (err != nil && cierrors.IsNotOwnedObject(err)) {
				log.Sugar().Debugf("Marking manifest (%s) for removal from Status.ManifestsRendered", manifest.String())
				manifestsDeleted = append(manifestsDeleted, *manifest)
				continue
			}

			if err != nil {
				log.Sugar().Errorf("unable to delete object (%s)", resourceId, zap.Error(err))
				errs = append(errs, err)
				if cierrors.IsDeletionTimeoutError(err) {
					manifest.UpdateStatus(v1alpha1.ManifestDeletionTimedOut, err.Error())
					return
				}
				manifest.UpdateStatus(v1alpha1.ManifestDeletionFailure, err.Error())
				continue
			}

			if manifest.Status != v1alpha1.ManifestDeletionInProgress {
				log.Sugar().Debugf(
					"Updating manifest (%s) status from (%s) to (%s)",
					manifest.String(), manifest.Status, v1alpha1.ManifestDeletionInProgress)
				manifest.UpdateStatus(v1alpha1.ManifestDeletionInProgress, "")
			}

			syncWaveGroupDeleted = false
		}

		if len(errs) > 0 || !syncWaveGroupDeleted {
			return
		}
	}

	log.Info("Successfully deleted objects")
	deletionCompleted = true
	return
}

// DeleteObject ensures the deletion of an owned object.
func (d *DeletionHandler) DeleteObject(
	ctx context.Context,
	clusterInstance *v1alpha1.ClusterInstance,
	object ci.RenderedObject,
	deletionTimeout *time.Duration,
) (bool, error) {
	log := d.Logger.With(
		zap.String("ClusterInstance", fmt.Sprintf("%s/%s", clusterInstance.Namespace, clusterInstance.Name)),
	)

	obj := ptr.To(object.GetObject())
	resourceId := object.GetResourceId()

	if deleted, err := isObjectDeleted(ctx, d.Client, obj); deleted && err == nil {
		return true, nil
	} else if err != nil {
		return false, err
	}

	ownerRefLabel := ci.GenerateOwnedByLabelValue(clusterInstance.Namespace, clusterInstance.Name)
	if err := ci.VerifyOwnership(obj, ownerRefLabel); err != nil {
		log.Sugar().Warnf("Ownership verification failed for object (%s): %v", resourceId, err.Error())
		return false, fmt.Errorf("ownership verification failed for object %s: %w", resourceId, err)
	}

	if obj.GetDeletionTimestamp().IsZero() {
		log.Sugar().Debugf("Initiating object (%s) deletion", resourceId)
		if err := initiateObjectDeletion(ctx, d.Client, obj); err != nil {
			return false, fmt.Errorf("failed to initiate deletion for object (%s): %w", resourceId, err)
		}
	} else if timeoutExceeded(obj.GetDeletionTimestamp().Time, deletionTimeout) {
		err := cierrors.NewDeletionTimeoutError(resourceId)
		return false, fmt.Errorf("deletion timeout exceeded for object (%s): %w", resourceId, err)
	}

	log.Sugar().Debugf("Waiting for object (%s) to be deleted", resourceId)
	return false, nil
}

// Helper functions

func objectsForClusterInstance(clusterInstance *v1alpha1.ClusterInstance) []ci.RenderedObject {
	objects := make([]ci.RenderedObject, 0, len(clusterInstance.Status.ManifestsRendered))
	for _, manifest := range clusterInstance.Status.ManifestsRendered {
		object := ci.RenderedObject{}
		if err := object.SetObject(map[string]interface{}{
			"apiVersion": *manifest.APIGroup,
			"kind":       manifest.Kind,
			"metadata": map[string]interface{}{
				"name":      manifest.Name,
				"namespace": manifest.Namespace,
				"annotations": map[string]string{
					ci.WaveAnnotation: fmt.Sprintf("%d", manifest.SyncWave),
				},
			},
		}); err == nil {
			objects = append(objects, object)
		}
	}
	return objects
}

func isObjectDeleted(ctx context.Context, c client.Client, obj client.Object) (bool, error) {
	if err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, fmt.Errorf("failed to retrieve object: %w", err)
	}
	return false, nil
}

func initiateObjectDeletion(ctx context.Context, c client.Client, obj client.Object) error {
	// Use Background propagation for all resources to allow external controllers
	// (baremetal-operator, assisted-service, ACM/OCM, etc.) to manage their own
	// cleanup sequences for dependent resources. SiteConfig tracks deletion completion
	// independently by checking if objects still exist in the API server.
	if err := client.IgnoreNotFound(c.Delete(ctx, obj, &client.DeleteOptions{
		PropagationPolicy: ptr.To(metav1.DeletePropagationBackground),
	})); err != nil {
		return fmt.Errorf(
			"failed to delete object (%s/%s): %w", obj.GetNamespace(), obj.GetName(), err)
	}
	return nil
}

// timeoutExceeded checks whether the deletion has exceeded the timeout.
func timeoutExceeded(startTime time.Time, timeout *time.Duration) bool {
	if timeout == nil {
		return false
	}
	return time.Since(startTime) > *timeout
}

func filterOutDeletedManifests(manifestsRendered, manifestsDeleted []v1alpha1.ManifestReference,
) []v1alpha1.ManifestReference {

	filteredManifests := make([]v1alpha1.ManifestReference, 0, len(manifestsRendered))
	for _, manifest := range manifestsRendered {
		manifestCopy := manifest
		if _, err := v1alpha1.IndexOfManifestByIdentity(&manifestCopy, manifestsDeleted); err != nil {
			filteredManifests = append(filteredManifests, manifest)
		}
	}
	return filteredManifests
}
