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

// updateCIDeploymentConditionsFromHostedCluster synthesizes the 4 standard Hive ClusterInstall
// conditions from HostedCluster status and stores them in ClusterInstance.Status.DeploymentConditions.
//
// The mapping is based on the following condition relationships between HostedCluster and DeploymentConditions
//   - RequirementsMet: ValidConfiguration and SupportedHostedCluster and ValidReleaseImage
//   - Completed: Available and ClusterVersionSucceeding
//   - Failed: ValidReleaseInfo and ClusterVersionFailing
//   - Stopped: (Completed or Failed) and not Progressing
func updateCIDeploymentConditionsFromHostedCluster(
	hc *hypershiftv1beta1.HostedCluster,
	ci *v1alpha1.ClusterInstance,
) {
	now := metav1.NewTime(time.Now())

	// Extract required conditions for Completed
	available := findHostedClusterCondition(
		hc.Status.Conditions, hypershiftv1beta1.HostedClusterAvailable)
	clusterVersionSucceeding := findHostedClusterCondition(
		hc.Status.Conditions, hypershiftv1beta1.ClusterVersionSucceeding)

	// Extract required conditions for Failed
	clusterVersionFailing := findHostedClusterCondition(
		hc.Status.Conditions, hypershiftv1beta1.ClusterVersionFailing)
	validReleaseInfo := findHostedClusterCondition(
		hc.Status.Conditions, hypershiftv1beta1.ValidReleaseInfo)

	// Extract required conditions for Stopped
	progressing := findHostedClusterCondition(
		hc.Status.Conditions, hypershiftv1beta1.HostedClusterProgressing)

	// Extract required conditions for RequirementsMet
	validConfiguration := findHostedClusterCondition(
		hc.Status.Conditions, hypershiftv1beta1.ValidHostedClusterConfiguration)
	supportedHostedCluster := findHostedClusterCondition(
		hc.Status.Conditions, hypershiftv1beta1.SupportedHostedCluster)
	validReleaseImage := findHostedClusterCondition(
		hc.Status.Conditions, hypershiftv1beta1.ValidReleaseImage)

	// Calculate RequirementsMet: All required preflight checks must pass
	// If any preflight condition is missing or not True, requirements are not met
	requirementsMet := isConditionTrue(validConfiguration) &&
		isConditionTrue(supportedHostedCluster) &&
		isConditionTrue(validReleaseImage)

	// Calculate Completed: Available=True AND ClusterVersionSucceeding=True
	// Both conditions must exist and be True for completion
	completed := isConditionTrue(available) && isConditionTrue(clusterVersionSucceeding)

	// Calculate Failed: ValidReleaseInfo=True AND ClusterVersionFailing=True
	// Only fail if we're past preflight (ValidReleaseInfo=True) and CVO is failing
	// Note: We don't use Degraded=True because it's expected during early provisioning
	failed := isConditionTrue(validReleaseInfo) && isConditionTrue(clusterVersionFailing)

	// Calculate Stopped: Installation finished (success or failure) AND not progressing
	// Only stopped if we've reached a terminal state and are no longer progressing
	// If progressing condition is missing, we're not stopped yet
	stopped := (completed || failed) && isConditionFalse(progressing)

	// Define the 4 Hive conditions to synthesize
	type conditionDef struct {
		condType hivev1.ClusterDeploymentConditionType
		isTrue   bool
		reason   string
		message  string
	}

	hiveConds := []conditionDef{
		{
			condType: hivev1.ClusterInstallRequirementsMetClusterDeploymentCondition,
			isTrue:   requirementsMet,
			reason:   ternary(requirementsMet, "RequirementsMet", "RequirementsNotMet"),
			message: ternary(requirementsMet,
				"Cluster installation requirements are met",
				"Waiting for preflight checks to complete"),
		},
		{
			condType: hivev1.ClusterInstallCompletedClusterDeploymentCondition,
			isTrue:   completed,
			reason:   ternary(completed, "InstallationCompleted", "InstallationInProgress"),
			message: ternary(completed,
				"Cluster installation has completed successfully",
				"Cluster installation is in progress"),
		},
		{
			condType: hivev1.ClusterInstallFailedClusterDeploymentCondition,
			isTrue:   failed,
			reason:   ternary(failed, "InstallationFailed", "InstallationNotFailed"),
			message: ternary(failed,
				"Cluster installation has failed",
				"Cluster installation has not failed"),
		},
		{
			condType: hivev1.ClusterInstallStoppedClusterDeploymentCondition,
			isTrue:   stopped,
			reason:   ternary(stopped, "InstallationStopped", "InstallationInProgress"),
			message: ternary(stopped,
				ternary(completed,
					"Installation stopped - completed successfully",
					"Installation stopped - failed"),
				"Installation is in progress"),
		},
	}

	// Update or append each condition
	// (mirrors updateCIDeploymentConditions in clusterdeployment_reconciler.go)
	for _, def := range hiveConds {
		status := corev1.ConditionFalse
		if def.isTrue {
			status = corev1.ConditionTrue
		}

		ciCond := conditions.FindCDConditionType(ci.Status.DeploymentConditions, def.condType)
		if ciCond == nil {
			// Condition doesn't exist, append it
			ci.Status.DeploymentConditions = append(ci.Status.DeploymentConditions, hivev1.ClusterDeploymentCondition{
				Type:               def.condType,
				Status:             status,
				Reason:             def.reason,
				Message:            def.message,
				LastProbeTime:      now,
				LastTransitionTime: now,
			})
		} else {
			// Update existing condition
			ciCond.Reason = def.reason
			ciCond.Message = def.message
			ciCond.LastProbeTime = now

			if ciCond.Status != status {
				ciCond.Status = status
				ciCond.LastTransitionTime = now
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
