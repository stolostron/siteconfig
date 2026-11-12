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
	"fmt"
	"reflect"
	"strconv"

	"go.uber.org/zap"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	"github.com/stolostron/siteconfig/internal/controller/conditions"
)

const (
	// ManagedCopyLabel marks a ConfigMap as a SiteConfig-managed copy of a shared extra-manifest
	// ConfigMap. It allows the controller to distinguish its own copies from user-created ConfigMaps
	// that happen to share the same name.
	ManagedCopyLabel = v1alpha1.Group + "/managed-copy"

	// ManagedCopyLabelValue is the value set on the ManagedCopyLabel.
	ManagedCopyLabelValue = "true"

	// SourceNamespaceAnnotation records the namespace a managed copy was copied from.
	SourceNamespaceAnnotation = v1alpha1.Group + "/source-namespace"

	// SourceResourceVersionAnnotation records the resourceVersion of the source ConfigMap as of the
	// most recent propagation. It is updated on every propagation (install-time copy and subsequent
	// SharedManifestSync updates) when content is propagated; metadata-only source edits need not advance it.
	SourceResourceVersionAnnotation = v1alpha1.Group + "/source-resource-version"

	// InitialResourceVersionAnnotation records the resourceVersion of the source ConfigMap at the
	// time the managed copy was first created. It is set once and never updated, providing a
	// copy-creation audit trail. For post-install reference changes this is not the installation version.
	InitialResourceVersionAnnotation = v1alpha1.Group + "/initial-resource-version"
)

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

// isSourceVersionAtLeast reports whether sourceRV is not older than the resourceVersion
// recorded on an existing managed copy. Both values come from the same API server, so a
// numeric comparison prevents a stale copier snapshot from overwriting a newer
// synchronization write (and vice versa).
func isSourceVersionAtLeast(sourceRV, copyRV string) bool {
	if copyRV == "" {
		return true
	}
	sourceVersion, sourceErr := strconv.ParseUint(sourceRV, 10, 64)
	copyVersion, copyErr := strconv.ParseUint(copyRV, 10, 64)
	if sourceErr != nil || copyErr != nil {
		return true
	}
	return sourceVersion >= copyVersion
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

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, copy, func() error {
		// Name-collision protection: if the ConfigMap already existed and is not a SiteConfig-managed
		// copy, refuse to overwrite a user-created ConfigMap.
		if copy.ResourceVersion != "" && (copy.Labels[ManagedCopyLabel] != ManagedCopyLabelValue ||
			!metav1.IsControlledBy(copy, clusterInstance)) {
			return fmt.Errorf(
				"name collision: ConfigMap %s/%s already exists and is not a SiteConfig-managed copy",
				clusterInstance.Namespace, name)
		}

		// A synchronization may have written a newer source snapshot after this copy
		// operation read its source. Do not overwrite the copy with that stale snapshot.
		existingRV := copy.Annotations[SourceResourceVersionAnnotation]
		if !isSourceVersionAtLeast(source.ResourceVersion, existingRV) {
			return nil
		}

		contentChanged := !reflect.DeepEqual(copy.Data, source.Data) ||
			!reflect.DeepEqual(copy.BinaryData, source.BinaryData)
		copy.Data = source.Data
		copy.BinaryData = source.BinaryData

		if copy.Labels == nil {
			copy.Labels = map[string]string{}
		}
		copy.Labels[ManagedCopyLabel] = ManagedCopyLabelValue

		if copy.Annotations == nil {
			copy.Annotations = map[string]string{}
		}
		copy.Annotations[SourceNamespaceAnnotation] = v1alpha1.SharedResourcesNamespace
		if contentChanged || existingRV == "" {
			copy.Annotations[SourceResourceVersionAnnotation] = source.ResourceVersion
		}

		// initial-resource-version is set once at creation and never updated, preserving a
		// copy-creation audit trail even as SharedManifestSync advances source-resource-version.
		if _, ok := copy.Annotations[InitialResourceVersionAnnotation]; !ok {
			copy.Annotations[InitialResourceVersionAnnotation] = source.ResourceVersion
		}

		// ownerReference enables automatic garbage collection when the ClusterInstance is deleted.
		if err := controllerutil.SetControllerReference(clusterInstance, copy, r.Scheme); err != nil {
			return fmt.Errorf("failed to set ownerReference on copied ConfigMap %s/%s: %w",
				clusterInstance.Namespace, name, err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to copy shared ExtraManifest %s into namespace %s: %w",
			name, clusterInstance.Namespace, err)
	}

	if op != controllerutil.OperationResultNone {
		log.Sugar().Infof("Copied shared ExtraManifest %s into namespace %s (%s)",
			name, clusterInstance.Namespace, op)
	}

	return nil
}

// cleanupSharedExtraManifests removes obsolete owned copies only after the new rendered resources
// have been applied successfully. Source and ownership checks protect unrelated ConfigMaps.
func (r *ClusterInstanceReconciler) cleanupSharedExtraManifests(ctx context.Context,
	clusterInstance *v1alpha1.ClusterInstance) error {
	copies := &corev1.ConfigMapList{}
	if err := r.List(ctx, copies, client.InNamespace(clusterInstance.Namespace),
		client.MatchingLabels{ManagedCopyLabel: ManagedCopyLabelValue}); err != nil {
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
			cm.Annotations[SourceNamespaceAnnotation] != v1alpha1.SharedResourcesNamespace {
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
