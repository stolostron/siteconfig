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

type DeletionContext struct {
	Context         context.Context
	Client          client.Client
	Logger          *zap.Logger
	TimeoutDuration *time.Duration
}

// NewDeletionContext creates a new DeletionContext with default DeletionTimeout if unspecified.
func NewDeletionContext(
	ctx context.Context,
	client client.Client,
	logger *zap.Logger,
	timeoutDuration *time.Duration,
) *DeletionContext {
	// Use a default timeout if none is provided
	if timeoutDuration == nil {
		timeoutDuration = ptr.To(DefaultDeletionTimeout)
	}

	if ctx == nil || client == nil || logger == nil {
		return nil
	}

	return &DeletionContext{
		Context:         ctx,
		Client:          client,
		Logger:          logger,
		TimeoutDuration: timeoutDuration,
	}
}

// DeleteRenderedObjects deletes all rendered objects except those explicitly excluded.
func (ctx *DeletionContext) DeleteRenderedObjects(
	clusterInstance *v1alpha1.ClusterInstance,
	excludeObjects []ci.RenderedObject,
) (bool, error) {
	if len(clusterInstance.Status.ManifestsRendered) == 0 {
		ctx.Logger.Info("The manifestsRendered list is empty, nothing to delete")
		return true, nil
	}

	objects := ctx.buildDeletionObjects(clusterInstance)
	return ctx.DeleteObjects(clusterInstance, objects, excludeObjects)
}

// DeleteObjects deletes the ClusterInstance rendered manifests by syncWaveGroup.
// SyncWaves are processed in descending order, that is, highest syncWaveGroup objects are first
// deleted and the lowest syncWaveGroups are deleted last.
// Returns: bool, error
// bool: indicates whether the deletion of all the specified (and owned) objects are successful
// error: error encountered during deletion attempt
func (ctx *DeletionContext) DeleteObjects(
	clusterInstance *v1alpha1.ClusterInstance,
	objects, excludeObjects []ci.RenderedObject,
) (deletionCompleted bool, err error) {
	deletionCompleted = false
	err = nil

	patch := client.MergeFrom(clusterInstance.DeepCopy())
	manifestsRendered := append([]v1alpha1.ManifestReference{}, clusterInstance.Status.ManifestsRendered...)

	objectSyncWaveMap := ci.SyncWaveMap{}
	if err := objectSyncWaveMap.AddObjects(objects); err != nil {
		ctx.Logger.Warn("Incurred error(s) collecting objects for deletion", zap.Error(err))
	}

	var (
		deleted bool
		errs    []error
	)
	defer func() {
		// Update ManifestsRendered if there are changes
		if !equality.Semantic.DeepEqual(clusterInstance.Status.ManifestsRendered, manifestsRendered) {
			clusterInstance.Status.ManifestsRendered = manifestsRendered
			if updateErr := conditions.PatchCIStatus(ctx.Context, ctx.Client, clusterInstance, patch); updateErr != nil {
				ctx.Logger.Error("Failed to update ClusterInstance.Status.ManifestsRendered", zap.Error(updateErr))
				errs = append(errs, updateErr)
			}
		}
		err = utilerrors.NewAggregate(errs)
	}()

	ownerRefLabel := ci.GenerateOwnedByLabelValue(clusterInstance.Namespace, clusterInstance.Name)

	for _, syncWave := range objectSyncWaveMap.GetDescendingSyncWaves() {
		syncWaveGroupDeleted := true

		for _, object := range objectSyncWaveMap.GetSlice(syncWave) {
			resourceId := object.GetResourceId()

			if ci.IndexOfObjectByIdentity(&object, excludeObjects) >= 0 {
				ctx.Logger.Sugar().Debugf("Excluding object (%s) from deletion", resourceId)
				continue
			}

			manifestRefIndex := v1alpha1.IndexOfManifestByIdentity(object.ManifestReference(), manifestsRendered)
			if manifestRefIndex < 0 {
				ctx.Logger.Sugar().Debugf(
					"Skipping deletion of object (%s) not found in ClusterInstance.Status.ManifestsRendered",
					resourceId)
				continue
			}

			manifestRef := &manifestsRendered[manifestRefIndex]
			deleted, err = ctx.DeleteObject(object, ownerRefLabel)
			if deleted || (err != nil && cierrors.IsNotOwnedObject(err)) {
				ctx.Logger.Sugar().Debugf(
					"Removing object (%s) from ClusterInstance.Status.ManifestsRendered",
					resourceId)
				manifestsRendered = append(manifestsRendered[:manifestRefIndex], manifestsRendered[manifestRefIndex+1:]...)
				continue
			}

			if err != nil {
				ctx.Logger.Sugar().Errorf("unable to delete object (%s)", resourceId, zap.Error(err))
				errs = append(errs, err)
				if cierrors.IsDeletionTimeoutError(err) {
					manifestRef.UpdateStatus(v1alpha1.ManifestDeletionTimedOut, err.Error(), metav1.Now())
					return
				}
				manifestRef.UpdateStatus(v1alpha1.ManifestDeletionFailure, err.Error(), metav1.Now())
				continue
			}

			if manifestRef.Status != v1alpha1.ManifestDeletionInProgress {
				ctx.Logger.Sugar().Debugf(
					"Updating object (%s) Status from from (%s) to (%s)",
					resourceId, manifestRef.Status, v1alpha1.ManifestDeletionInProgress)
				manifestRef.UpdateStatus(v1alpha1.ManifestDeletionInProgress, "", metav1.Now())
			}

			syncWaveGroupDeleted = false
		}

		if len(errs) > 0 || !syncWaveGroupDeleted {
			return
		}
	}

	ctx.Logger.Info("Successfully deleted objects")
	deletionCompleted = true
	return
}

// DeleteObject ensures the deletion of an owned object.
func (ctx *DeletionContext) DeleteObject(object ci.RenderedObject, ownerRefLabel string) (bool, error) {
	obj := ptr.To(object.GetObject())

	resourceId := object.GetResourceId()

	if exists, err := ctx.ensureObjectExists(obj); !exists && err == nil {
		return true, nil
	} else if err != nil {
		return false, err
	}

	if err := ci.VerifyOwnership(obj, ownerRefLabel); err != nil {
		ctx.Logger.Sugar().Errorf("Ownership verification failed for object (%s): %v", resourceId, err.Error())
		return false, err
	}

	if obj.GetDeletionTimestamp().IsZero() {
		if err := ctx.initiateObjectDeletion(obj); err != nil {
			return false, err
		}
	} else if timeoutExceeded(obj.GetDeletionTimestamp().Time, ctx.TimeoutDuration) {
		return false, cierrors.NewDeletionTimeoutError(resourceId)
	}

	ctx.Logger.Sugar().Infof("Waiting for object (%s) to be deleted", resourceId)
	return false, nil
}

// Utility functions

func (ctx *DeletionContext) buildDeletionObjects(clusterInstance *v1alpha1.ClusterInstance) []ci.RenderedObject {
	objects := make([]ci.RenderedObject, 0, len(clusterInstance.Status.ManifestsRendered))
	for _, manifest := range clusterInstance.Status.ManifestsRendered {
		object := ci.RenderedObject{}
		if err := object.SetObject(map[string]interface{}{
			"apiVersion": *manifest.APIGroup,
			"kind":       manifest.Kind,
			"metadata": map[string]interface{}{
				"name":      manifest.Name,
				"namespace": manifest.Namespace,
			},
		}); err == nil {
			objects = append(objects, object)
		}
	}
	return objects
}

func (ctx *DeletionContext) ensureObjectExists(obj client.Object) (bool, error) {
	if err := ctx.Client.Get(ctx.Context, client.ObjectKeyFromObject(obj), obj); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("failed to retrieve object: %w", err)
	}
	return true, nil
}

func (ctx *DeletionContext) initiateObjectDeletion(obj client.Object) error {
	return client.IgnoreNotFound(ctx.Client.Delete(ctx.Context, obj,
		&client.DeleteOptions{PropagationPolicy: ptr.To(metav1.DeletePropagationForeground)}))
}

// timeoutExceeded checks whether the deletion has exceeded the timeout.
func timeoutExceeded(startTime time.Time, timeout *time.Duration) bool {
	if timeout == nil {
		return false
	}
	return time.Since(startTime) > *timeout
}
