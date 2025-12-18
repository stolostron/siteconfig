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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"go.uber.org/zap"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/wI2L/jsondiff"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
	"github.com/stolostron/siteconfig/internal/controller/conditions"
	"github.com/stolostron/siteconfig/internal/controller/preservation"
)

// getManagedClusterManifest extracts the ManagedCluster manifest reference from ClusterInstance status.
// Only returns manifests with ManifestRenderedSuccess status.
func getManagedClusterManifest(clusterInstance *v1alpha1.ClusterInstance) *v1alpha1.ManifestReference {
	for i := range clusterInstance.Status.ManifestsRendered {
		manifest := &clusterInstance.Status.ManifestsRendered[i]
		if manifest.Kind == "ManagedCluster" && manifest.Status == v1alpha1.ManifestRenderedSuccess {
			return manifest
		}
	}
	return nil
}

func getManagedCluster(clusterInstance *v1alpha1.ClusterInstance) (*ci.RenderedObject, error) {
	manifest := getManagedClusterManifest(clusterInstance)
	if manifest == nil {
		return nil, nil
	}

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

// reinstallClusterReimportedConditionStatus creates a condition for cluster reimport status
func reinstallClusterReimportedConditionStatus(
	status metav1.ConditionStatus,
	reason v1alpha1.ClusterInstanceConditionReason,
	message string,
) *metav1.Condition {
	return conditionSetter(v1alpha1.ReinstallClusterReimported, status, reason, message)
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
		v1alpha1.ReinstallClusterReimported,
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
		// Clear existing conditions for new reinstall generation to ensure fresh timestamps
		clusterInstance.Status.Reinstall.Conditions = []metav1.Condition{}
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

func setOrUpdateLabel(objectMeta *metav1.ObjectMeta, key, value string) bool {
	if objectMeta.Labels == nil {
		objectMeta.Labels = make(map[string]string)
	}
	if currentValue, ok := objectMeta.Labels[key]; !ok || currentValue != value {
		objectMeta.Labels[key] = value
		return true
	}
	return false
}

func getDataPreservationSummary(
	ctx context.Context,
	c client.Client,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
) (*metav1.Condition, *metav1.Condition, error) {
	clusterIDCount, otherCount, err := preservation.GetPreservedResourceCounts(ctx, c, log, clusterInstance)
	if err != nil {
		log.Error("Failed to compute data preservation summary", zap.Error(err))
		return reinstallPreservationDataBackedupConditionStatus(metav1.ConditionFalse, v1alpha1.Failed, err.Error()),
			reinstallClusterIdentityDataDetectedConditionStatus(metav1.ConditionFalse, v1alpha1.Failed, err.Error()),
			err
	}

	totalPreservedResources := clusterIDCount + otherCount
	log.Info("Data preservation summary",
		zap.String("Total Resources", fmt.Sprintf("%d", totalPreservedResources)),
		zap.String("ClusterIdentity Resources", fmt.Sprintf("%d", clusterIDCount)),
		zap.String("Other Resources", fmt.Sprintf("%d", otherCount)),
	)

	preservationDataBackedupCondition := reinstallPreservationDataBackedupConditionStatus(
		metav1.ConditionTrue, v1alpha1.Completed,
		fmt.Sprintf("Number of resources preserved: %d ", totalPreservedResources))
	if totalPreservedResources == 0 {
		preservationDataBackedupCondition = reinstallPreservationDataBackedupConditionStatus(
			metav1.ConditionFalse, v1alpha1.DataUnavailable,
			"No resources were found to be preserved in the ClusterInstance namespace with the preservation data label")
	}

	clusterIdentityDetectedCondition := reinstallClusterIdentityDataDetectedConditionStatus(
		metav1.ConditionTrue, v1alpha1.DataAvailable,
		fmt.Sprintf("Number of cluster identity resources detected: %d ", clusterIDCount))
	if clusterIDCount == 0 {
		if clusterInstance.Spec.Reinstall.PreservationMode == v1alpha1.PreservationModeClusterIdentity {
			errorMsg := fmt.Sprintf("preservationMode set to '%s', found no cluster-identity resources",
				v1alpha1.PreservationModeClusterIdentity)
			log.Error(errorMsg)
			clusterIdentityDetectedCondition = reinstallClusterIdentityDataDetectedConditionStatus(
				metav1.ConditionFalse, v1alpha1.Failed, errorMsg)
			return preservationDataBackedupCondition, clusterIdentityDetectedCondition, errors.New(errorMsg)
		}
		clusterIdentityDetectedCondition = reinstallClusterIdentityDataDetectedConditionStatus(
			metav1.ConditionFalse,
			v1alpha1.DataUnavailable,
			"No cluster identity resources were detected for preservation")
	}

	return preservationDataBackedupCondition, clusterIdentityDetectedCondition, nil
}

func getDataRestorationSummary(
	ctx context.Context,
	c client.Client,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
) (*metav1.Condition, error) {

	clusterIDCount, otherCount, err := preservation.GetRestoredResourceCounts(ctx, c, log, clusterInstance)
	if err != nil {
		log.Error("Failed to compute restoration summary", zap.Error(err))
		return reinstallPreservationDataRestoredConditionStatus(
			metav1.ConditionFalse, v1alpha1.Failed, err.Error()), err
	}

	totalRestoredResources := clusterIDCount + otherCount
	log.Info("Data restoration summary",
		zap.String("Total Resources", fmt.Sprintf("%d", totalRestoredResources)),
		zap.String("ClusterIdentity Resources", fmt.Sprintf("%d", clusterIDCount)),
		zap.String("Other Resources", fmt.Sprintf("%d", otherCount)),
	)

	if totalRestoredResources == 0 {
		if clusterInstance.Spec.Reinstall.PreservationMode == v1alpha1.PreservationModeClusterIdentity {
			errorMsg := fmt.Sprintf("no restored resources found, PreservationMode '%s' expects data to be restored",
				v1alpha1.PreservationModeClusterIdentity)
			log.Error("failing reinstall request as no restoration data was found", zap.Error(err))
			return reinstallPreservationDataRestoredConditionStatus(
				metav1.ConditionFalse, v1alpha1.Failed, errorMsg), errors.New(errorMsg)
		}

		return reinstallPreservationDataRestoredConditionStatus(
			metav1.ConditionFalse, v1alpha1.DataUnavailable, "No restored resources found"), nil
	}

	return reinstallPreservationDataRestoredConditionStatus(
		metav1.ConditionTrue, v1alpha1.Completed,
		fmt.Sprintf("%d resources were successfully restored: %d cluster-identity resources, %d other resources",
			totalRestoredResources, clusterIDCount, otherCount)), nil
}

func computeClusterInstanceSpecDiff(clusterInstance *v1alpha1.ClusterInstance) (string, error) {

	lastObserved, ok := clusterInstance.Annotations[v1alpha1.LastClusterInstanceSpecAnnotation]
	if !ok {
		return "", nil
	}

	lastObservedSpec := &v1alpha1.ClusterInstanceSpec{}
	if err := json.Unmarshal([]byte(lastObserved), lastObservedSpec); err != nil {
		return "", fmt.Errorf("failed to unmarshal last-observed spec: %w", err)
	}

	lastObservedSpecJSON, err := json.Marshal(lastObservedSpec)
	if err != nil {
		return "", fmt.Errorf("failed to marshal last-observed ClusterInstance spec: %w", err)
	}

	currentSpecJSON, err := json.Marshal(clusterInstance.Spec)
	if err != nil {
		return "", fmt.Errorf("failed to marshal current ClusterInstance spec: %w", err)
	}

	// Generate JSON Patch using jsondiff
	diff, err := jsondiff.CompareJSON(lastObservedSpecJSON, currentSpecJSON)
	if err != nil {
		return "", fmt.Errorf(
			"failed to compute the difference between the last and the current ClusterInstance specs: %w", err)
	}

	// Return empty string for no differences
	if len(diff) == 0 {
		return "", nil
	}

	// Sort the Difference by Path (i.e. spec field), Value and then by Operation Type (add, replace, remove)
	sort.SliceStable(diff, func(i, j int) bool {
		if diff[i].Path != diff[j].Path {
			return diff[i].Path < diff[j].Path
		}
		vi, _ := json.Marshal(diff[i].Value)
		vj, _ := json.Marshal(diff[j].Value)
		if !bytes.Equal(vi, vj) {
			return string(vi) < string(vj)
		}
		return diff[i].Type < diff[j].Type
	})

	// Convert JSON Patch to string
	diffJSON, err := json.Marshal(diff)
	if err != nil {
		return "", fmt.Errorf(
			"failed to marshal the difference between the last and the current ClusterInstance specs as JSON: %w", err)
	}

	return string(diffJSON), nil
}
