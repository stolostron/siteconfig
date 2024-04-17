package conditions

import (
	"context"
	"fmt"

	siteconfigv1 "github.com/sakhoury/siteconfig/api/v1alpha1"
	"github.com/sakhoury/siteconfig/internal/controller/retry"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ConditionType is a string representing the condition's type
type ConditionType string

// The following constants define the different types of conditions that will be set
const (
	SiteConfigValidated        ConditionType = "SiteConfigValidated"
	RenderedTemplates          ConditionType = "RenderedTemplates"
	RenderedTemplatesValidated ConditionType = "RenderedTemplatesValidated"
	Provisioned                ConditionType = "Provisioned"
)

// ConditionReason is a string representing the condition's reason
type ConditionReason string

// The following constants define the different reasons that conditions will be set for
const (
	Completed  ConditionReason = "Completed"
	Failed     ConditionReason = "Failed"
	TimedOut   ConditionReason = "TimedOut"
	InProgress ConditionReason = "InProgress"
	Unknown    ConditionReason = "Unknown"
)

// SetStatusCondition is a convenience wrapper for meta.SetStatusCondition that takes in the types defined here and converts them to strings
func SetStatusCondition(existingConditions *[]metav1.Condition, conditionType ConditionType, conditionReason ConditionReason, conditionStatus metav1.ConditionStatus, message string) {
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
			Type:    string(conditionType),
			Status:  conditionStatus,
			Reason:  string(conditionReason),
			Message: message,
		},
	)
}

func UpdateSiteConfigStatus(ctx context.Context, c client.Client, siteConfig *siteconfigv1.SiteConfig) error {
	if c == nil {
		// In UT code
		return nil
	}

	err := retry.RetryOnRetriable(retry.RetryBackoffTwoMinutes, func() error {
		return c.Status().Update(ctx, siteConfig) //nolint:wrapcheck
	})

	if err != nil {
		return fmt.Errorf("failed to update SiteConfig status: %w", err)
	}

	return nil
}
