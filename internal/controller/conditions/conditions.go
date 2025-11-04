package conditions

import (
	"context"
	"fmt"

	hivev1 "github.com/openshift/hive/apis/hive/v1"
	"github.com/stolostron/siteconfig/api/v1alpha1"
	"github.com/stolostron/siteconfig/internal/controller/retry"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// SetStatusCondition is a convenience wrapper for meta.SetStatusCondition that takes in the types defined here and
// converts them to strings
func SetStatusCondition(
	existingConditions *[]metav1.Condition,
	conditionType v1alpha1.ClusterInstanceConditionType,
	conditionReason v1alpha1.ClusterInstanceConditionReason,
	conditionStatus metav1.ConditionStatus,
	message string,
) {
	conditions := *existingConditions
	condition := meta.FindStatusCondition(*existingConditions, string(conditionType))
	if condition != nil &&
		condition.Status != conditionStatus &&
		conditions[len(conditions)-1].Type != string(conditionType) {
		meta.RemoveStatusCondition(existingConditions, string(conditionType))
	}
	meta.SetStatusCondition(
		existingConditions,
		metav1.Condition{
			Type:               string(conditionType),
			Status:             conditionStatus,
			Reason:             string(conditionReason),
			Message:            message,
			LastTransitionTime: metav1.Now(),
		},
	)
}

func UpdateCIStatus(ctx context.Context, c client.Client, clusterInstance *v1alpha1.ClusterInstance) error {
	if err := retry.RetryOnConflictOrRetriable(retry.RetryBackoff30Seconds, func() error {
		return c.Status().Update(ctx, clusterInstance) //nolint:wrapcheck
	}); err != nil {
		return fmt.Errorf("failed to update ClusterInstance status: %w", err)
	}

	return nil
}

func PatchCIStatus(
	ctx context.Context,
	c client.Client,
	clusterInstance *v1alpha1.ClusterInstance,
	patch client.Patch,
) error {
	// Retry only on transient errors (internal, service unavailable, connection refused)
	// Let conflicts fail and propagate - controller will requeue
	if err := retry.RetryOnRetriable(retry.RetryBackoff30Seconds, func() error {
		return c.Status().Patch(ctx, clusterInstance, patch) //nolint:wrapcheck
	}); err != nil {
		return fmt.Errorf("failed to update ClusterInstance status: %w", err)
	}

	return nil
}

// FindCDConditionType finds the conditionType in ClusterDeployment conditions.
func FindCDConditionType(
	conditions []hivev1.ClusterDeploymentCondition,
	condType hivev1.ClusterDeploymentConditionType,
) *hivev1.ClusterDeploymentCondition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}

// FindStatusCondition finds the conditionType in status conditions.
func FindStatusCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}
