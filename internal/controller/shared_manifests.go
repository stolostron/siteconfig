/*
Copyright 2025 Red Hat, Inc.

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
	"context"
	stderrors "errors"
	"fmt"
	"maps"
	"reflect"

	"go.uber.org/zap"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	"github.com/stolostron/siteconfig/internal/controller/conditions"
	"github.com/stolostron/siteconfig/internal/controller/sharedmanifests"
)

// errManagedCopyNotOwned indicates an existing ConfigMap is not a SiteConfig-managed copy owned by
// this ClusterInstance. The install-time copy path rejects it; the sync skips it.
var errManagedCopyNotOwned = stderrors.New("name collision: managed copy is not owned by this ClusterInstance")

// errManagedCopyOwnerless indicates an ownerless managed copy is awaiting adoption. The install-time
// copy path adopts it; the sync skips it.
var errManagedCopyOwnerless = stderrors.New("managed copy is ownerless and awaiting adoption")

// handleCopySharedExtraManifests copies shared extra-manifest ConfigMaps into the ClusterInstance
// namespace while preserving the source references for validation and spec snapshots.
// On failure it records a validation failure condition on the ClusterInstance.
func (r *ClusterInstanceReconciler) handleCopySharedExtraManifests(
	ctx context.Context,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
) error {
	err := r.copySharedExtraManifests(ctx, log, clusterInstance)
	if err == nil {
		return nil
	}

	log.Error("Failed to copy shared extra-manifest ConfigMaps", zap.Error(err))

	patch := client.MergeFrom(clusterInstance.DeepCopy())
	conditions.SetStatusCondition(&clusterInstance.Status.Conditions,
		v1alpha1.ClusterInstanceValidated,
		v1alpha1.Failed,
		metav1.ConditionFalse,
		fmt.Sprintf("Failed to copy shared extra-manifests: %s", err.Error()))

	if updateErr := conditions.PatchCIStatus(ctx, r.Client, clusterInstance, patch); updateErr != nil {
		log.Sugar().Errorf("Failed to update ClusterInstance status after copying shared extra-manifests, err: %v",
			updateErr)
	}

	return err
}

// copySharedExtraManifests iterates the ClusterInstance's extra-manifest references. For each
// reference targeting the shared-resources namespace it copies the source ConfigMap into the
// ClusterInstance namespace (with an ownerReference for garbage collection, a managed-copy label,
// and provenance annotations). Source references are preserved; rendering uses a separate copy.
func (r *ClusterInstanceReconciler) copySharedExtraManifests(
	ctx context.Context,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
) error {
	for i := range clusterInstance.Spec.ExtraManifestsRefs {
		ref := &clusterInstance.Spec.ExtraManifestsRefs[i]

		// References to the ClusterInstance's own namespace (explicit or implicit) require no copy.
		// Rendering separately normalizes references for downstream consumers.
		if ref.Namespace != v1alpha1.SharedResourcesNamespace {
			continue
		}

		if err := r.copySharedExtraManifest(ctx, log, clusterInstance, ref.Name); err != nil {
			return err
		}

	}
	return nil
}

// copySharedExtraManifest copies a single shared ConfigMap into the ClusterInstance namespace.
func (r *ClusterInstanceReconciler) copySharedExtraManifest(
	ctx context.Context,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
	name string,
) error {
	source := &corev1.ConfigMap{}
	sourceKey := types.NamespacedName{Name: name, Namespace: v1alpha1.SharedResourcesNamespace}
	if err := r.Get(ctx, sourceKey, source); err != nil {
		return fmt.Errorf("failed to retrieve shared ExtraManifest %s/%s: %w",
			v1alpha1.SharedResourcesNamespace, name, err)
	}
	if err := ensureSharedSourceBackup(ctx, r.Client, source); err != nil {
		return err
	}

	copy := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: clusterInstance.Namespace,
		},
	}

	adopted := false
	var op controllerutil.OperationResult
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var createErr error
		op, createErr = controllerutil.CreateOrUpdate(ctx, r.Client, copy, func() error {
			// Re-read on every attempt, including conflict retries. The source read above can be
			// older than a synchronization that has already updated this copy. resourceVersion is
			// provenance only and is never used to order the two snapshots.
			fresh := &corev1.ConfigMap{}
			if err := r.Get(ctx, sourceKey, fresh); err != nil {
				return fmt.Errorf("failed to retrieve shared ExtraManifest %s/%s: %w",
					v1alpha1.SharedResourcesNamespace, name, err)
			}
			var applyErr error
			adopted, _, applyErr = applyManagedCopy(clusterInstance, fresh, copy, r.Scheme, true, true)
			return applyErr
		})
		return createErr
	})
	if err != nil {
		return fmt.Errorf("failed to copy shared ExtraManifest %s into namespace %s: %w",
			name, clusterInstance.Namespace, err)
	}
	if adopted {
		log.Sugar().Infof("Adopted ownerless managed copy of shared ExtraManifest %s in namespace %s",
			name, clusterInstance.Namespace)
	}

	if op != controllerutil.OperationResultNone {
		log.Sugar().Infof("Copied shared ExtraManifest %s into namespace %s (%s)",
			name, clusterInstance.Namespace, op)
	}

	return nil
}

// applyManagedCopy reconciles the managed copy in place from the latest source. allowAdoption
// controls whether an ownerless managed copy (migration/restore) may be adopted; the sync path
// deliberately never adopts. It returns whether the copy was adopted and whether content changed.
func applyManagedCopy(
	clusterInstance *v1alpha1.ClusterInstance,
	source *corev1.ConfigMap,
	copy *corev1.ConfigMap,
	scheme *runtime.Scheme,
	allowAdoption bool,
	setProvenance bool,
) (adopted, contentChanged bool, err error) {
	existing := copy.ResourceVersion != ""
	adoptable := existing && sharedmanifests.IsAdoptableManagedCopy(copy)
	adopting := adoptable && allowAdoption
	if existing && !sharedmanifests.IsOwnedManagedCopy(copy, clusterInstance) && !adopting {
		if adoptable {
			return false, false, errManagedCopyOwnerless
		}
		return false, false, errManagedCopyNotOwned
	}

	existingRV := copy.Annotations[v1alpha1.SourceResourceVersionAnnotation]
	contentChanged = !reflect.DeepEqual(copy.Data, source.Data) ||
		!reflect.DeepEqual(copy.BinaryData, source.BinaryData)
	copy.Data = maps.Clone(source.Data)
	copy.BinaryData = maps.Clone(source.BinaryData)

	if copy.Labels == nil {
		copy.Labels = map[string]string{}
	}
	copy.Labels[v1alpha1.ManagedCopyLabel] = v1alpha1.ManagedCopyLabelValue

	if copy.Annotations == nil {
		copy.Annotations = map[string]string{}
	}
	if setProvenance {
		copy.Annotations[v1alpha1.SourceNamespaceAnnotation] = v1alpha1.SharedResourcesNamespace
	}
	if adopting {
		// Adoption is treated like creation: rewrite all provenance from this hub's source.
		copy.Annotations[v1alpha1.SourceResourceVersionAnnotation] = source.ResourceVersion
		copy.Annotations[v1alpha1.InitialResourceVersionAnnotation] = source.ResourceVersion
	} else {
		if contentChanged || existingRV == "" {
			copy.Annotations[v1alpha1.SourceResourceVersionAnnotation] = source.ResourceVersion
		}
		// initial-resource-version is set once at creation and never updated, preserving a
		// copy-creation audit trail even as SharedManifestSync advances source-resource-version.
		if setProvenance {
			if _, ok := copy.Annotations[v1alpha1.InitialResourceVersionAnnotation]; !ok {
				copy.Annotations[v1alpha1.InitialResourceVersionAnnotation] = source.ResourceVersion
			}
		}
	}

	// ownerReference enables automatic garbage collection when the ClusterInstance is deleted.
	// Sync propagates into an already-owned copy and does not need a scheme for this.
	if !metav1.IsControlledBy(copy, clusterInstance) {
		if err := controllerutil.SetControllerReference(clusterInstance, copy, scheme); err != nil {
			return false, false, fmt.Errorf("failed to set ownerReference on copied ConfigMap %s/%s: %w",
				copy.Namespace, copy.Name, err)
		}
	}
	return adopting, contentChanged, nil
}

// cleanupSharedExtraManifests removes obsolete owned copies only after the new rendered resources
// have been applied successfully. Source and ownership checks protect unrelated ConfigMaps.
func (r *ClusterInstanceReconciler) cleanupSharedExtraManifests(ctx context.Context,
	clusterInstance *v1alpha1.ClusterInstance) error {
	copies := &corev1.ConfigMapList{}
	if err := r.List(ctx, copies, client.InNamespace(clusterInstance.Namespace),
		client.MatchingLabels{v1alpha1.ManagedCopyLabel: v1alpha1.ManagedCopyLabelValue}); err != nil {
		return fmt.Errorf("failed to list shared extra-manifest copies: %w", err)
	}
	referenced := map[string]bool{}
	for _, ref := range clusterInstance.Spec.ExtraManifestsRefs {
		if ref.Namespace == v1alpha1.SharedResourcesNamespace {
			referenced[ref.Name] = true
		}
	}
	for i := range copies.Items {
		cm := &copies.Items[i]
		if referenced[cm.Name] || !metav1.IsControlledBy(cm, clusterInstance) ||
			cm.Annotations[v1alpha1.SourceNamespaceAnnotation] != v1alpha1.SharedResourcesNamespace {
			continue
		}
		err := r.Delete(ctx, cm, client.Preconditions{UID: &cm.UID, ResourceVersion: &cm.ResourceVersion})
		if err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("failed to delete obsolete shared copy %s/%s: %w", cm.Namespace, cm.Name, err)
		}
	}
	return nil
}

// completeSharedManifestReconciliation runs only after successful render/apply and cleans using
// that applied reference set before updateObservedStatus can re-fetch the ClusterInstance.
func (r *ClusterInstanceReconciler) completeSharedManifestReconciliation(
	ctx context.Context, log *zap.Logger, clusterInstance *v1alpha1.ClusterInstance,
) error {
	if err := r.cleanupSharedExtraManifests(ctx, clusterInstance); err != nil {
		return err
	}
	return r.updateObservedStatus(ctx, log, clusterInstance)
}
