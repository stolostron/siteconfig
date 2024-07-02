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
	"time"

	"github.com/go-logr/logr"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	"github.com/stolostron/siteconfig/api/v1alpha1"
	"github.com/stolostron/siteconfig/internal/controller/conditions"
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

// ClusterDeploymentReconciler reconciles a ClusterDeployment object to
// update the SiteConfig cluster deployment status conditions
type ClusterDeploymentReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

func (r *ClusterDeploymentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Get the ClusterDeployment CR
	clusterDeployment := &hivev1.ClusterDeployment{}
	if err := r.Get(ctx, req.NamespacedName, clusterDeployment); err != nil {
		if errors.IsNotFound(err) {
			r.Log.Info("ClusterDeployment not found", "name", clusterDeployment.Name)
			return doNotRequeue(), nil
		}
		r.Log.Error(err, "Failed to get ClusterDeployment")
		// This is likely a case where the API is down, so requeue and try again shortly
		return requeueWithError(err)
	}

	// Fetch SiteConfig associated with ClusterDeployment object
	siteConfig, err := r.getSiteConfig(ctx, clusterDeployment)
	if siteConfig == nil {
		return doNotRequeue(), nil
	} else if err != nil {
		return requeueWithError(err)
	}

	patch := client.MergeFrom(siteConfig.DeepCopy())

	// Initialize siteconfig clusterdeployment reference if unset
	if siteConfig.Status.ClusterDeploymentRef == nil || siteConfig.Status.ClusterDeploymentRef.Name == "" {
		siteConfig.Status.ClusterDeploymentRef = &corev1.LocalObjectReference{Name: clusterDeployment.Name}
	}

	updateSCProvisionedStatus(clusterDeployment, siteConfig)
	updateSCDeploymentConditions(clusterDeployment, siteConfig)
	if updateErr := conditions.PatchStatus(ctx, r.Client, siteConfig, patch); updateErr != nil {
		return requeueWithError(updateErr)
	}

	return doNotRequeue(), nil
}

func clusterInstallConditionTypes() []hivev1.ClusterDeploymentConditionType {
	return []hivev1.ClusterDeploymentConditionType{
		hivev1.ClusterInstallRequirementsMetClusterDeploymentCondition,
		hivev1.ClusterInstallCompletedClusterDeploymentCondition,
		hivev1.ClusterInstallFailedClusterDeploymentCondition,
		hivev1.ClusterInstallStoppedClusterDeploymentCondition,
	}
}

func updateSCProvisionedStatus(cd *hivev1.ClusterDeployment, sc *v1alpha1.SiteConfig) {
	// Check if cluster has finished installing, if it has -> update siteConfig.Status.Conditions(Provisioned -> Completed)
	if cd.Spec.Installed {
		conditions.SetStatusCondition(&sc.Status.Conditions,
			conditions.Provisioned,
			conditions.Completed,
			metav1.ConditionTrue,
			"Provision completed")
	} else if installStopped := conditions.FindConditionType(cd.Status.Conditions, hivev1.ClusterInstallStoppedClusterDeploymentCondition); installStopped != nil {
		// Check if siteconfig.Status Provisioned -> InProgress condition
		if found := meta.FindStatusCondition(sc.Status.Conditions, string(conditions.Provisioned)); found == nil {
			if !cd.Spec.Installed && installStopped.Status == corev1.ConditionStatus(metav1.ConditionFalse) {
				conditions.SetStatusCondition(&sc.Status.Conditions,
					conditions.Provisioned,
					conditions.InProgress,
					metav1.ConditionTrue,
					"Provisioning cluster")
			}
		}
	}
}

func updateSCDeploymentConditions(cd *hivev1.ClusterDeployment, sc *v1alpha1.SiteConfig) {
	// Compare siteConfig.Status.installConditions to clusterDeployment.Conditions
	for _, cond := range clusterInstallConditionTypes() {
		installCond := conditions.FindConditionType(cd.Status.Conditions, cond)
		if installCond == nil {
			// not found, initialize with Unknown fields
			installCond = &hivev1.ClusterDeploymentCondition{
				Type:    cond,
				Status:  corev1.ConditionUnknown,
				Reason:  "Unknown",
				Message: "Unknown"}
		}

		now := metav1.NewTime(time.Now())

		// Search SiteConfig status DeploymentConditions for the installCond
		scCond := conditions.FindConditionType(sc.Status.DeploymentConditions, installCond.Type)
		if scCond == nil {
			installCond.LastTransitionTime = now
			installCond.LastProbeTime = now
			sc.Status.DeploymentConditions = append(sc.Status.DeploymentConditions, *installCond)
		} else {
			scCond.Status = installCond.Status
			scCond.Reason = installCond.Reason
			scCond.Message = installCond.Message
			scCond.LastProbeTime = now

			if scCond.Status != installCond.Status {
				scCond.LastTransitionTime = now
			}
		}
	}
}

func siteConfigOwner(ownerRefs []metav1.OwnerReference) string {
	for _, ownerRef := range ownerRefs {
		if ownerRef.Kind == v1alpha1.SiteConfigKind {
			return ownerRef.Name
		}
	}
	return ""
}
func isOwnedBySiteConfig(ownerRefs []metav1.OwnerReference) bool {
	return siteConfigOwner(ownerRefs) != ""
}

func (r *ClusterDeploymentReconciler) getSiteConfig(ctx context.Context, cd *hivev1.ClusterDeployment) (*v1alpha1.SiteConfig, error) {
	siteConfigRef := siteConfigOwner(cd.GetOwnerReferences())
	if siteConfigRef == "" {
		r.Log.Info("SiteConfig owner-reference not found for ClusterDeployment", "name", cd.Name)
		return nil, nil
	}

	siteConfig := &v1alpha1.SiteConfig{}
	if err := r.Get(ctx, types.NamespacedName{Name: siteConfigRef, Namespace: cd.Namespace}, siteConfig); err != nil {
		if errors.IsNotFound(err) {
			r.Log.Info("SiteConfig not found", "name", siteConfigRef)
			return nil, nil
		}
		r.Log.Info("Failed to get SiteConfig", "name", siteConfigRef, "ClusterDeployment", cd.Name)
		return nil, err
	}
	return siteConfig, nil
}

func (r *ClusterDeploymentReconciler) mapSiteConfigToCD(ctx context.Context, obj client.Object) []reconcile.Request {
	siteConfig := &v1alpha1.SiteConfig{}
	if err := r.Get(ctx, types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}, siteConfig); err != nil {
		return []reconcile.Request{}
	}

	if siteConfig.Status.ClusterDeploymentRef != nil &&
		siteConfig.Status.ClusterDeploymentRef.Name != "" {
		return []reconcile.Request{{
			NamespacedName: types.NamespacedName{
				Namespace: obj.GetNamespace(),
				Name:      siteConfig.Status.ClusterDeploymentRef.Name,
			},
		}}
	}

	return []reconcile.Request{}
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterDeploymentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("clusterDeploymentReconciler").
		For(&hivev1.ClusterDeployment{},
			// watch for create and update event for ClusterDeployment
			builder.WithPredicates(predicate.Funcs{
				GenericFunc: func(e event.GenericEvent) bool { return false },
				CreateFunc: func(e event.CreateEvent) bool {
					return isOwnedBySiteConfig(e.Object.GetOwnerReferences())
				},
				DeleteFunc: func(e event.DeleteEvent) bool { return false },
				UpdateFunc: func(e event.UpdateEvent) bool {
					return isOwnedBySiteConfig(e.ObjectNew.GetOwnerReferences())
				},
			})).
		WatchesRawSource(source.Kind(mgr.GetCache(), &v1alpha1.SiteConfig{}), handler.EnqueueRequestsFromMapFunc(r.mapSiteConfigToCD)).
		Complete(r)
}
