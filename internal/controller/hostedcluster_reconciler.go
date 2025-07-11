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

package controller

import (
	"context"
	"fmt"
	"time"

	hivev1 "github.com/openshift/hive/apis/hive/v1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/stolostron/siteconfig/api/v1alpha1"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
	"github.com/stolostron/siteconfig/internal/controller/conditions"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

//+kubebuilder:rbac:groups=hypershift.openshift.io,resources=hostedclusters,verbs=get;list;watch
//+kubebuilder:rbac:groups=hypershift.openshift.io,resources=hostedclusters/status,verbs=get;watch

// HostedClusterReconciler reconciles a HostedCluster object to
// update the ClusterInstance status conditions
type HostedClusterReconciler struct {
	client.Client
	Log    *zap.Logger
	Scheme *runtime.Scheme
}

func (r *HostedClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {

	log := r.Log.With(
		zap.String("name", req.Name),
		zap.String("namespace", req.Namespace),
	)

	// Get the HostedCluster CR
	hostedCluster := &hypershiftv1beta1.HostedCluster{}
	if err := r.Get(ctx, req.NamespacedName, hostedCluster); err != nil {
		if errors.IsNotFound(err) {
			log.Info("HostedCluster not found")
			return doNotRequeue(), nil
		}
		log.Error("Failed to get HostedCluster", zap.Error(err))
		// This is likely a case where the API is down, so requeue and try again shortly
		return requeueWithError(err)
	}

	// Fetch ClusterInstance associated with HostedCluster object
	clusterInstance, err := r.getClusterInstance(ctx, log, hostedCluster)
	if clusterInstance == nil {
		return doNotRequeue(), nil
	} else if err != nil {
		return requeueWithError(err)
	}

	patch := client.MergeFrom(clusterInstance.DeepCopy())

	// Initialize ClusterInstance hostedcluster reference if unset
	if clusterInstance.Status.HostedClusterRef == nil || clusterInstance.Status.HostedClusterRef.Name == "" {
		clusterInstance.Status.HostedClusterRef = &corev1.LocalObjectReference{Name: hostedCluster.Name}
	}

	// Initialize ClusterInstance Provisioned status if not found
	if provisionedStatus := meta.FindStatusCondition(
		clusterInstance.Status.Conditions,
		string(v1alpha1.ClusterProvisioned),
	); provisionedStatus == nil {
		log.Info("Initializing Provisioned condition", zap.String("ClusterInstance", clusterInstance.Name))
		conditions.SetStatusCondition(&clusterInstance.Status.Conditions,
			v1alpha1.ClusterProvisioned,
			v1alpha1.Unknown,
			metav1.ConditionUnknown,
			"Waiting for provisioning to start")
	}

	updateCIProvisionedStatusFromHostedCluster(hostedCluster, clusterInstance, log)
	updateCIDeploymentConditionsFromHostedCluster(hostedCluster, clusterInstance)
	if updateErr := conditions.PatchCIStatus(ctx, r.Client, clusterInstance, patch); updateErr != nil {
		return requeueWithError(updateErr)
	}

	return doNotRequeue(), nil
}

func updateCIProvisionedStatusFromHostedCluster(
	hc *hypershiftv1beta1.HostedCluster,
	ci *v1alpha1.ClusterInstance,
	log *zap.Logger,
) {
	available := findHostedClusterCondition(
		hc.Status.Conditions, hypershiftv1beta1.HostedClusterAvailable)
	progressing := findHostedClusterCondition(
		hc.Status.Conditions, hypershiftv1beta1.HostedClusterProgressing)
	degraded := findHostedClusterCondition(
		hc.Status.Conditions, hypershiftv1beta1.HostedClusterDegraded)
	clusterVersionSucceeding := findHostedClusterCondition(
		hc.Status.Conditions, hypershiftv1beta1.ClusterVersionSucceeding)
	clusterVersionFailing := findHostedClusterCondition(
		hc.Status.Conditions, hypershiftv1beta1.ClusterVersionFailing)
	validReleaseInfo := findHostedClusterCondition(
		hc.Status.Conditions, hypershiftv1beta1.ValidReleaseInfo)

	if available == nil || progressing == nil || degraded == nil {
		log.Debug("Failed to extract HostedCluster condition(s) from HostedCluster object")
		return
	}

	// Check whether cluster has finished provisioning
	// HostedCluster is considered successfully provisioned when:
	// - Available and ClusterVersionSucceeding are True
	// - Progressing and Degraded are False
	if available.Status == metav1.ConditionTrue &&
		progressing.Status == metav1.ConditionFalse &&
		degraded.Status == metav1.ConditionFalse &&
		isConditionTrue(clusterVersionSucceeding) {
		conditions.SetStatusCondition(&ci.Status.Conditions,
			v1alpha1.ClusterProvisioned,
			v1alpha1.Completed,
			metav1.ConditionTrue,
			"Provisioning completed")
		return
	}

	// Check whether cluster provisioning has failed
	// HostedCluster is considered failed when:
	// - ValidReleaseInfo is True (past preflight/setup phase)
	// - ClusterVersionFailing = True (definitive failure signal)
	// - Progressing = False (not actively working to recover)
	//
	// Note: We don't use Degraded=True as a failure signal because during early
	// provisioning, the control plane is expected to be degraded (not available yet)
	// even though it's at the correct version and will eventually succeed.
	if isConditionTrue(validReleaseInfo) &&
		isConditionTrue(clusterVersionFailing) &&
		progressing.Status == metav1.ConditionFalse {
		conditions.SetStatusCondition(&ci.Status.Conditions,
			v1alpha1.ClusterProvisioned,
			v1alpha1.Failed,
			metav1.ConditionFalse,
			"Provisioning failed")
		return
	}

	// If any conditions are missing or in unknown state, keep provisioned as unknown/in-progress
	conditions.SetStatusCondition(&ci.Status.Conditions,
		v1alpha1.ClusterProvisioned,
		v1alpha1.InProgress,
		metav1.ConditionFalse,
		"Provisioning cluster")
}

// updateCIDeploymentConditionsFromHostedCluster copies Hive ClusterDeployment-compatible
// conditions from HostedCluster.Status.ClusterDeploymentConditions to ClusterInstance.Status.DeploymentConditions.
//
// HyperShift provides these conditions directly in the HostedCluster status, synthesized from
// the cluster state. We map from HyperShift's general ClusterDeployment condition types to the
// ClusterInstall-specific variants that Hive uses for installation tracking:
//   - RequirementsMet -> ClusterInstallRequirementsMet
//   - Provisioned -> ClusterInstallCompleted
//   - ProvisionFailed -> ClusterInstallFailed
//   - ProvisionStopped -> ClusterInstallStopped
func updateCIDeploymentConditionsFromHostedCluster(
	hc *hypershiftv1beta1.HostedCluster,
	ci *v1alpha1.ClusterInstance,
) {
	now := metav1.NewTime(time.Now())

	// If HyperShift doesn't provide ClusterDeploymentConditions, set all to Unknown
	if len(hc.Status.ClusterDeploymentConditions) == 0 {
		unknownConditions := []struct {
			condType hivev1.ClusterDeploymentConditionType
			reason   string
			message  string
		}{
			{
				condType: hivev1.ClusterInstallRequirementsMetClusterDeploymentCondition,
				reason:   "ConditionsUnavailable",
				message:  "ClusterDeploymentConditions not available from HostedCluster",
			},
			{
				condType: hivev1.ClusterInstallCompletedClusterDeploymentCondition,
				reason:   "ConditionsUnavailable",
				message:  "ClusterDeploymentConditions not available from HostedCluster",
			},
			{
				condType: hivev1.ClusterInstallFailedClusterDeploymentCondition,
				reason:   "ConditionsUnavailable",
				message:  "ClusterDeploymentConditions not available from HostedCluster",
			},
			{
				condType: hivev1.ClusterInstallStoppedClusterDeploymentCondition,
				reason:   "ConditionsUnavailable",
				message:  "ClusterDeploymentConditions not available from HostedCluster",
			},
		}

		for _, unknownCond := range unknownConditions {
			ciCond := conditions.FindCDConditionType(ci.Status.DeploymentConditions, unknownCond.condType)
			if ciCond == nil {
				// Condition doesn't exist, append it as Unknown
				ci.Status.DeploymentConditions = append(ci.Status.DeploymentConditions, hivev1.ClusterDeploymentCondition{
					Type:               unknownCond.condType,
					Status:             corev1.ConditionUnknown,
					Reason:             unknownCond.reason,
					Message:            unknownCond.message,
					LastProbeTime:      now,
					LastTransitionTime: now,
				})
			} else {
				// Update existing condition to Unknown
				ciCond.Reason = unknownCond.reason
				ciCond.Message = unknownCond.message
				ciCond.LastProbeTime = now

				if ciCond.Status != corev1.ConditionUnknown {
					ciCond.Status = corev1.ConditionUnknown
					ciCond.LastTransitionTime = now
				}
			}
		}
		return
	}

	// Mapping from HyperShift's ClusterDeployment condition types to ClusterInstall types
	// HyperShift provides general ClusterDeployment conditions; we need ClusterInstall variants
	conditionMapping := map[hypershiftv1beta1.ClusterDeploymentConditionType]hivev1.ClusterDeploymentConditionType{
		hypershiftv1beta1.ClusterRequirementsMetType:  hivev1.ClusterInstallRequirementsMetClusterDeploymentCondition,
		hypershiftv1beta1.ClusterProvisionedType:      hivev1.ClusterInstallCompletedClusterDeploymentCondition,
		hypershiftv1beta1.ClusterProvisionFailedType:  hivev1.ClusterInstallFailedClusterDeploymentCondition,
		hypershiftv1beta1.ClusterProvisionStoppedType: hivev1.ClusterInstallStoppedClusterDeploymentCondition,
	}

	// Process each condition from HyperShift
	for _, hcCond := range hc.Status.ClusterDeploymentConditions {
		// Map to the ClusterInstall variant
		ciCondType, mapped := conditionMapping[hcCond.Type]
		if !mapped {
			// Unknown condition type, skip it
			continue
		}

		// Find or create the condition in ClusterInstance
		ciCond := conditions.FindCDConditionType(ci.Status.DeploymentConditions, ciCondType)
		if ciCond == nil {
			// Condition doesn't exist, append it
			ci.Status.DeploymentConditions = append(ci.Status.DeploymentConditions, hivev1.ClusterDeploymentCondition{
				Type:               ciCondType,
				Status:             hcCond.Status,
				Reason:             hcCond.Reason,
				Message:            hcCond.Message,
				LastProbeTime:      now,
				LastTransitionTime: hcCond.LastTransitionTime,
			})
		} else {
			// Update existing condition
			ciCond.Reason = hcCond.Reason
			ciCond.Message = hcCond.Message
			ciCond.LastProbeTime = now

			// Only update LastTransitionTime if status changed
			if ciCond.Status != hcCond.Status {
				ciCond.Status = hcCond.Status
				ciCond.LastTransitionTime = hcCond.LastTransitionTime
			}
		}
	}
}

func ternary(condition bool, trueVal, falseVal string) string {
	if condition {
		return trueVal
	}
	return falseVal
}

func isConditionTrue(cond *metav1.Condition) bool {
	return cond != nil && cond.Status == metav1.ConditionTrue
}

func isConditionFalse(cond *metav1.Condition) bool {
	return cond != nil && cond.Status == metav1.ConditionFalse
}

func findHostedClusterCondition(
	conditions []metav1.Condition,
	condType hypershiftv1beta1.ConditionType,
) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == string(condType) {
			return &conditions[i]
		}
	}
	return nil
}

func (r *HostedClusterReconciler) getClusterInstance(
	ctx context.Context,
	log *zap.Logger,
	hc *hypershiftv1beta1.HostedCluster,
) (*v1alpha1.ClusterInstance, error) {
	ownedBy := getClusterInstanceOwner(hc.Labels)
	if ownedBy == "" {
		log.Info("ClusterInstance owner reference not found for HostedCluster")
		return nil, nil
	}

	clusterInstanceRef, err := ci.GetNamespacedNameFromOwnedByLabel(ownedBy)
	if err != nil {
		return nil, fmt.Errorf("failed to get namespaced name from OwnedBy label (%s): %w", ownedBy, err)
	}

	if clusterInstanceRef.Namespace != hc.Namespace {
		return nil, fmt.Errorf("hostedCluster namespace [%s] does not match ClusterInstance namespace [%s]",
			hc.Namespace, clusterInstanceRef.Namespace)
	}

	clusterInstance := &v1alpha1.ClusterInstance{}
	if err := r.Get(ctx, clusterInstanceRef,
		clusterInstance); err != nil {
		if errors.IsNotFound(err) {
			log.Info("ClusterInstance not found", zap.String("name", clusterInstanceRef.String()))
			return nil, nil
		}
		log.Info("Failed to get ClusterInstance", zap.String("name", clusterInstanceRef.String()))
		return nil, fmt.Errorf("failed to get ClusterInstance %s: %w", clusterInstanceRef.String(), err)
	}
	return clusterInstance, nil
}

func (r *HostedClusterReconciler) mapClusterInstanceToHC(
	ctx context.Context,
	obj *v1alpha1.ClusterInstance,
) []reconcile.Request {
	clusterInstance := &v1alpha1.ClusterInstance{}
	if err := r.Get(ctx, types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()},
		clusterInstance); err != nil {
		return []reconcile.Request{}
	}

	if clusterInstance.Status.HostedClusterRef != nil &&
		clusterInstance.Status.HostedClusterRef.Name != "" {
		return []reconcile.Request{{
			NamespacedName: types.NamespacedName{
				Namespace: obj.GetNamespace(),
				Name:      clusterInstance.Status.HostedClusterRef.Name,
			},
		}}
	}

	return []reconcile.Request{}
}

// SetupWithManager sets up the controller with the Manager.
func (r *HostedClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {

	//nolint:wrapcheck
	return ctrl.NewControllerManagedBy(mgr).
		Named("hostedClusterReconciler").
		For(&hypershiftv1beta1.HostedCluster{},
			// watch for create and update event for HostedCluster
			builder.WithPredicates(predicate.Funcs{
				GenericFunc: func(e event.GenericEvent) bool { return false },
				CreateFunc: func(e event.CreateEvent) bool {
					return isOwnedByClusterInstance(e.Object.GetLabels())
				},
				DeleteFunc: func(e event.DeleteEvent) bool { return false },
				UpdateFunc: func(e event.UpdateEvent) bool {
					return isOwnedByClusterInstance(e.ObjectNew.GetLabels())
				},
			})).
		WatchesRawSource(source.TypedKind(mgr.GetCache(),
			&v1alpha1.ClusterInstance{},
			handler.TypedEnqueueRequestsFromMapFunc(r.mapClusterInstanceToHC),
		)).
		Complete(r)
}
