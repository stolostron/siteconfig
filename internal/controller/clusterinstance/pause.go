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
	"context"
	"fmt"

	"go.uber.org/zap"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/stolostron/siteconfig/api/v1alpha1"
)

// ApplyPause sets the paused annotation and updates the ClusterInstance Paused status.
// It first patches the metadata (annotations), then updates the Paused status field.
func ApplyPause(
	ctx context.Context,
	c client.Client,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
	reason string,
) error {
	// Check if already paused
	if clusterInstance.IsPaused() {
		return nil
	}

	// Create a copy for patching metadata
	originalInstance := clusterInstance.DeepCopy()

	// Set the paused annotation
	metav1.SetMetaDataAnnotation(&clusterInstance.ObjectMeta, v1alpha1.PausedAnnotation, "")

	// Patch metadata changes
	if err := c.Patch(ctx, clusterInstance, client.MergeFrom(originalInstance)); err != nil {
		return fmt.Errorf("failed to apply paused annotation: %w", err)
	}
	log.Info("Applied paused annotation", zap.String("Annotation", v1alpha1.PausedAnnotation))

	// Fetch the latest object to get updated resourceVersion before modifying ClusterInstance status
	if err := c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), clusterInstance); err != nil {
		return fmt.Errorf("failed to refresh ClusterInstance after metadata patch: %w", err)
	}

	// Update the status with pause details
	originalStatus := clusterInstance.DeepCopy()

	clusterInstance.Status.Paused = &v1alpha1.PausedStatus{
		TimeSet: metav1.Now(),
		Reason:  reason,
	}

	// Patch status changes
	if err := c.Status().Patch(ctx, clusterInstance, client.MergeFrom(originalStatus)); err != nil {
		return fmt.Errorf("failed to update Pause status: %w", err)
	}
	log.Info("Updated Pause status", zap.String("Reason", reason))

	return nil
}

// RemovePause clears the paused annotation and Paused status.
// It first removes the annotation, then clears the status field.
func RemovePause(
	ctx context.Context,
	c client.Client,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
) error {
	// If not paused, no action needed
	if !clusterInstance.IsPaused() {
		return nil
	}

	// Create a copy for patching metadata
	originalInstance := clusterInstance.DeepCopy()

	// Remove the paused annotation
	delete(clusterInstance.Annotations, v1alpha1.PausedAnnotation)

	// Patch metadata changes
	if err := c.Patch(ctx, clusterInstance, client.MergeFrom(originalInstance)); err != nil {
		return fmt.Errorf("failed to remove paused annotation: %w", err)
	}
	log.Info("Removed paused annotation", zap.String("Annotation", v1alpha1.PausedAnnotation))

	// Fetch the latest object to get updated resourceVersion before modifying status
	if err := c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), clusterInstance); err != nil {
		return fmt.Errorf("failed to refresh ClusterInstance after metadata patch: %w", err)
	}

	// Clear the Paused status
	originalStatus := clusterInstance.DeepCopy()
	clusterInstance.Status.Paused = nil

	// Patch status changes
	if err := c.Status().Patch(ctx, clusterInstance, client.MergeFrom(originalStatus)); err != nil {
		return fmt.Errorf("failed to clear Paused status: %w", err)
	}
	log.Info("Cleared Paused status")

	return nil
}
