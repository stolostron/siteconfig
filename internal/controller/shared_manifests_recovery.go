/*
Copyright 2025.

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

	"go.uber.org/zap"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/stolostron/siteconfig/api/v1alpha1"
)

// ensureSharedSourceBackup includes referenced source inputs in ACM backup. Copies
// remain derived resources and must not inherit this label.
func ensureSharedSourceBackup(ctx context.Context, c client.Client, cm *corev1.ConfigMap) error {
	if value, exists := cm.Labels[acmBackupLabel]; exists && value == acmBackupLabelValue {
		return nil
	}
	patch := client.MergeFrom(cm.DeepCopy())
	if cm.Labels == nil {
		cm.Labels = map[string]string{}
	}
	cm.Labels[acmBackupLabel] = acmBackupLabelValue
	if err := c.Patch(ctx, cm, patch); err != nil {
		return fmt.Errorf("failed to label shared source %s/%s for backup: %w", cm.Namespace, cm.Name, err)
	}
	return nil
}

// prepareSharedRecovery runs recovery before the observed-generation and restore guards.
func (r *ClusterInstanceReconciler) prepareSharedRecovery(
	ctx context.Context, log *zap.Logger, ci *v1alpha1.ClusterInstance,
) (ctrl.Result, error) {
	if ci.Status.ObservedGeneration == ci.Generation || isBeingRestored(ci) {
		if err := r.ensureMissingSharedCopies(ctx, ci); err != nil {
			return ctrl.Result{}, err
		}
	}
	return r.ensureVeleroRestoreStatusAnnotation(ctx, log, ci)
}

// ensureMissingSharedCopies creates only absent copies. It runs on the initial
// ClusterInstance informer list at each controller startup as well as deletion
// events, before the observed-generation/restore guards. It never renders resources
// or updates status. Existing copies are exclusively refreshed by SharedManifestSync.
func (r *ClusterInstanceReconciler) ensureMissingSharedCopies(ctx context.Context, ci *v1alpha1.ClusterInstance) error {
	if !ci.DeletionTimestamp.IsZero() || ci.IsPaused() {
		return nil
	}
	for _, ref := range ci.Spec.ExtraManifestsRefs {
		if ref.Namespace != v1alpha1.SharedResourcesNamespace {
			continue
		}
		if ci.Spec.ClusterName != ci.Namespace {
			return fmt.Errorf("shared extra-manifest references require spec.clusterName to equal the ClusterInstance namespace")
		}
		copy := &corev1.ConfigMap{}
		key := client.ObjectKey{Namespace: ci.Namespace, Name: ref.Name}
		err := r.Get(ctx, key, copy)
		if err == nil {
			continue
		}
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to check shared copy %s: %w", key, err)
		}
		source := &corev1.ConfigMap{}
		if err := r.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, source); err != nil {
			return fmt.Errorf("failed to recover shared copy %s: %w", key, err)
		}
		if err := ensureSharedSourceBackup(ctx, r.Client, source); err != nil {
			return err
		}
		copy = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
			Name: ref.Name, Namespace: ci.Namespace,
			Labels: map[string]string{ManagedCopyLabel: ManagedCopyLabelValue},
			Annotations: map[string]string{
				SourceNamespaceAnnotation:        ref.Namespace,
				SourceResourceVersionAnnotation:  source.ResourceVersion,
				InitialResourceVersionAnnotation: source.ResourceVersion,
			},
		}, Data: source.Data, BinaryData: source.BinaryData}
		if err := controllerutil.SetControllerReference(ci, copy, r.Scheme); err != nil {
			return fmt.Errorf("failed to set recovered copy owner: %w", err)
		}
		// A concurrent creation must never be overwritten, including a user-owned object.
		if err := r.Create(ctx, copy); err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to recover shared copy %s: %w", key, err)
		}
	}
	return nil
}

// sharedCopyOwnerRequests routes copy deletion back to its owning ClusterInstance.
// Reconciliation rechecks the current spec; stale owners/references cannot recreate
// a copy after its ClusterInstance is deleted or the reference is removed.
func sharedCopyOwnerRequests(_ context.Context, cm *corev1.ConfigMap) []ctrl.Request {
	if cm.Labels[ManagedCopyLabel] != ManagedCopyLabelValue ||
		cm.Annotations[SourceNamespaceAnnotation] != v1alpha1.SharedResourcesNamespace {
		return nil
	}
	owner := metav1.GetControllerOf(cm)
	if owner == nil || owner.APIVersion != v1alpha1.GroupVersion.String() || owner.Kind != v1alpha1.ClusterInstanceKind {
		return nil
	}
	return []ctrl.Request{{NamespacedName: client.ObjectKey{Namespace: cm.Namespace, Name: owner.Name}}}
}

// sharedCopySourceRequests repairs content drift by enqueueing the source, rather
// than running the provisioning reconciler. Both old and new update objects are
// mapped by the event handler, including when a user removes tracking metadata.
func sharedCopySourceRequests(ctx context.Context, cm *corev1.ConfigMap) []ctrl.Request {
	if len(sharedCopyOwnerRequests(ctx, cm)) == 0 {
		return nil
	}
	return []ctrl.Request{{NamespacedName: client.ObjectKey{Namespace: v1alpha1.SharedResourcesNamespace, Name: cm.Name}}}
}
