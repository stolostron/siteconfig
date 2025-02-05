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

package reinstall

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
	"github.com/stolostron/siteconfig/internal/controller/conditions"
)

func getManagedCluster(clusterInstance *v1alpha1.ClusterInstance) (*ci.RenderedObject, error) {
	manifests := append([]v1alpha1.ManifestReference(nil), clusterInstance.Status.ManifestsRendered...)
	for _, manifest := range manifests {
		if manifest.Kind == "ManagedCluster" {
			obj, err := ci.NewRenderedObject(map[string]interface{}{
				"apiVersion": *manifest.APIGroup,
				"kind":       manifest.Kind,
				"metadata": map[string]interface{}{
					"name": manifest.Name,
					"annotations": map[string]string{
						ci.WaveAnnotation: fmt.Sprintf("%d", manifest.SyncWave),
					},
				},
			})
			if err != nil {
				return nil, fmt.Errorf("encountered an error while converting ManagedCluster to RenderedObject, err: %w",
					err)
			}
			return obj, nil
		}
	}
	return nil, nil
}

func findReinstallStatusCondition(
	clusterInstance *v1alpha1.ClusterInstance,
	conditionType v1alpha1.ClusterInstanceConditionType,
) *metav1.Condition {
	if clusterInstance.Status.Reinstall == nil {
		return nil
	}

	return meta.FindStatusCondition(clusterInstance.Status.Reinstall.Conditions, string(conditionType))
}

func setReinstallStatusCondition(clusterInstance *v1alpha1.ClusterInstance, condition metav1.Condition) bool {
	if clusterInstance.Status.Reinstall == nil {
		return false
	}

	return meta.SetStatusCondition(&clusterInstance.Status.Reinstall.Conditions, condition)
}

func conditionSetter(
	conditionType v1alpha1.ClusterInstanceConditionType,
	conditionStatus metav1.ConditionStatus,
	conditionReason v1alpha1.ClusterInstanceConditionReason,
	message string,
) *metav1.Condition {
	return &metav1.Condition{
		Type:    string(conditionType),
		Status:  conditionStatus,
		Reason:  string(conditionReason),
		Message: message,
	}
}

func reinstallRequestProcessedConditionStatus(
	status metav1.ConditionStatus,
	reason v1alpha1.ClusterInstanceConditionReason,
	message string,
) *metav1.Condition {
	return conditionSetter(v1alpha1.ReinstallRequestProcessed, status, reason, message)
}

func reinstallRequestValidatedConditionStatus(
	status metav1.ConditionStatus,
	reason v1alpha1.ClusterInstanceConditionReason,
	message string,
) *metav1.Condition {
	return conditionSetter(v1alpha1.ReinstallRequestValidated, status, reason, message)
}

func reinstallPreservationDataBackedupConditionStatus(
	status metav1.ConditionStatus,
	reason v1alpha1.ClusterInstanceConditionReason,
	message string,
) *metav1.Condition {
	return conditionSetter(v1alpha1.ReinstallPreservationDataBackedup, status, reason, message)
}

func reinstallClusterIdentityDataDetectedConditionStatus(
	status metav1.ConditionStatus,
	reason v1alpha1.ClusterInstanceConditionReason,
	message string,
) *metav1.Condition {
	return conditionSetter(v1alpha1.ReinstallClusterIdentityDataDetected, status, reason, message)
}

func reinstallRenderedManifestsDeletedConditionStatus(
	status metav1.ConditionStatus,
	reason v1alpha1.ClusterInstanceConditionReason,
	message string,
) *metav1.Condition {
	return conditionSetter(v1alpha1.ReinstallRenderedManifestsDeleted, status, reason, message)
}

func reinstallPreservationDataRestoredConditionStatus(
	status metav1.ConditionStatus,
	reason v1alpha1.ClusterInstanceConditionReason,
	message string,
) *metav1.Condition {
	return conditionSetter(v1alpha1.ReinstallPreservationDataRestored, status, reason, message)
}

func initializeReinstallStatus(
	ctx context.Context,
	c client.Client,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
) error {
	reinstallConditionTypes := []v1alpha1.ClusterInstanceConditionType{
		v1alpha1.ReinstallRequestProcessed,
		v1alpha1.ReinstallRequestValidated,
		v1alpha1.ReinstallPreservationDataBackedup,
		v1alpha1.ReinstallClusterIdentityDataDetected,
		v1alpha1.ReinstallRenderedManifestsDeleted,
		v1alpha1.ReinstallPreservationDataRestored,
	}

	now := metav1.Now()
	reinstallConditions := make([]metav1.Condition, len(reinstallConditionTypes))
	for i, conditionType := range reinstallConditionTypes {
		reinstallConditions[i] = metav1.Condition{
			Type:               string(conditionType),
			Status:             metav1.ConditionUnknown,
			Reason:             string(v1alpha1.Initialized),
			Message:            "Condition Initialized",
			LastTransitionTime: now,
		}
	}

	patch := client.MergeFrom(clusterInstance.DeepCopy())
	updateRequired := false

	if clusterInstance.Status.Reinstall == nil {
		clusterInstance.Status.Reinstall = &v1alpha1.ReinstallStatus{
			Conditions:           []metav1.Condition(nil),
			ObservedGeneration:   "",
			InProgressGeneration: clusterInstance.Spec.Reinstall.Generation,
			RequestStartTime:     metav1.Time{},
			RequestEndTime:       metav1.Time{},
			History:              []v1alpha1.ReinstallHistory(nil),
		}
		updateRequired = true
	}

	if clusterInstance.Status.Reinstall.InProgressGeneration != clusterInstance.Spec.Reinstall.Generation {
		clusterInstance.Status.Reinstall.InProgressGeneration = clusterInstance.Spec.Reinstall.Generation
		updateRequired = true
	}

	if !clusterInstance.Status.Reinstall.RequestStartTime.IsZero() {
		clusterInstance.Status.Reinstall.RequestStartTime = metav1.Time{}
		updateRequired = true
	}

	if !clusterInstance.Status.Reinstall.RequestEndTime.IsZero() {
		clusterInstance.Status.Reinstall.RequestEndTime = metav1.Time{}
		updateRequired = true
	}

	for _, condition := range reinstallConditions {
		if changed := meta.SetStatusCondition(&clusterInstance.Status.Reinstall.Conditions, condition); changed {
			updateRequired = true
		}
	}

	if updateRequired {
		log.Info("Initializing Reinstall Status")

		if err := conditions.PatchCIStatus(ctx, c, clusterInstance, patch); err != nil {
			errorMsg := "failed to initialize reinstall status"
			log.Error(errorMsg, zap.Error(err))
			return fmt.Errorf("%s, error: %w", errorMsg, err)
		}
	}

	return nil
}

func logAndWrapUpdateFailure(log *zap.Logger, currentError, updateError error, errorMsg string) error {
	log.Error(errorMsg, zap.Error(updateError))
	if currentError == nil {
		return fmt.Errorf("%s, update error: %w", errorMsg, updateError)
	}
	return fmt.Errorf("%s, update error: %w, error: %w", errorMsg, updateError, currentError)
}
