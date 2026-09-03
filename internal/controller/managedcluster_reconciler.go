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

	"github.com/stolostron/siteconfig/api/v1alpha1"
	"github.com/stolostron/siteconfig/internal/controller/conditions"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=managedclusters,verbs=get;list;watch
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=managedclusters/status,verbs=get

// ManagedClusterReconciler reconciles a ManagedCluster object to
// update the ClusterInstance provisioned status condition for HostedControlPlane clusters
type ManagedClusterReconciler struct {
	client.Client
	Log    *zap.Logger
	Scheme *runtime.Scheme
}

func (r *ManagedClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {

	log := r.Log.With(
		zap.String("name", req.Name),
	)

	// Get the ManagedCluster CR
	managedCluster := &clusterv1.ManagedCluster{}
	if err := r.Get(ctx, req.NamespacedName, managedCluster); err != nil {
		if errors.IsNotFound(err) {
			log.Info("ManagedCluster not found")
			return doNotRequeue(), nil
		}
		log.Error("Failed to get ManagedCluster", zap.Error(err))
		// This is likely a case where the API is down, so requeue and try again shortly
		return requeueWithError(err)
	}

	// Fetch ClusterInstance associated with ManagedCluster object
	clusterInstance, err := r.getClusterInstance(ctx, log, managedCluster)
	if err != nil {
		return requeueWithError(err)
	}
	if clusterInstance == nil {
		return doNotRequeue(), nil
	}

	oldStatus := clusterInstance.Status.DeepCopy()
	patch := client.MergeFrom(clusterInstance.DeepCopy())

	// Initialize ClusterInstance ManagedCluster reference if unset
	if clusterInstance.Status.ManagedClusterRef == nil || clusterInstance.Status.ManagedClusterRef.Name == "" {
		clusterInstance.Status.ManagedClusterRef = &corev1.LocalObjectReference{Name: managedCluster.Name}
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
			"Waiting for ManagedCluster to become available")
	}

	updateProvisionedStatus(managedCluster, clusterInstance, log)

	if !equality.Semantic.DeepEqual(oldStatus, &clusterInstance.Status) {
		if updateErr := conditions.PatchCIStatus(ctx, r.Client, clusterInstance, patch); updateErr != nil {
			return requeueWithError(updateErr)
		}
	}

	return doNotRequeue(), nil
}

// updateProvisionedStatus updates the ClusterInstance Provisioned condition based on ManagedCluster Available status.
func updateProvisionedStatus(mc *clusterv1.ManagedCluster, ci *v1alpha1.ClusterInstance, log *zap.Logger) {

	// Provisioning is a one-way transition: never downgrade a completed provision
	// because the ManagedCluster later becomes unavailable.
	if existing := meta.FindStatusCondition(
		ci.Status.Conditions, string(v1alpha1.ClusterProvisioned),
	); existing != nil && existing.Status == metav1.ConditionTrue {
		return
	}

	availableCondition := meta.FindStatusCondition(mc.Status.Conditions, clusterv1.ManagedClusterConditionAvailable)

	if availableCondition == nil {
		log.Debug("ManagedCluster Available condition not found, setting Provisioned to Unknown")
		conditions.SetStatusCondition(&ci.Status.Conditions,
			v1alpha1.ClusterProvisioned,
			v1alpha1.Unknown,
			metav1.ConditionUnknown,
			"ManagedCluster Available condition not found")
		return
	}

	switch availableCondition.Status {
	case metav1.ConditionTrue:
		conditions.SetStatusCondition(&ci.Status.Conditions,
			v1alpha1.ClusterProvisioned,
			v1alpha1.Completed,
			metav1.ConditionTrue,
			"ManagedCluster is available")
	case metav1.ConditionFalse:
		conditions.SetStatusCondition(&ci.Status.Conditions,
			v1alpha1.ClusterProvisioned,
			v1alpha1.InProgress,
			metav1.ConditionFalse,
			"ManagedCluster is not available")
	case metav1.ConditionUnknown:
		conditions.SetStatusCondition(&ci.Status.Conditions,
			v1alpha1.ClusterProvisioned,
			v1alpha1.Unknown,
			metav1.ConditionUnknown,
			"ManagedCluster availability is unknown")
	}
}

// isHostedManagedCluster checks if a ManagedCluster is a hosted cluster based on labels and annotations
func isHostedManagedCluster(labels, annotations map[string]string) bool {
	return labels["ishosted"] == "true" &&
		annotations["open-cluster-management/created-via"] == "hypershift"
}

func (r *ManagedClusterReconciler) getClusterInstance(
	ctx context.Context,
	log *zap.Logger,
	mc *clusterv1.ManagedCluster,
) (*v1alpha1.ClusterInstance, error) {

	// List all ClusterInstances and find the one matching this ManagedCluster by name
	clusterInstanceList := &v1alpha1.ClusterInstanceList{}
	if err := r.List(ctx, clusterInstanceList); err != nil {
		log.Error("Failed to list ClusterInstances", zap.Error(err))
		return nil, fmt.Errorf("failed to list ClusterInstances: %w", err)
	}

	for i := range clusterInstanceList.Items {
		ci := &clusterInstanceList.Items[i]
		if ci.Spec.ClusterName == mc.Name && ci.Spec.ClusterType == v1alpha1.ClusterTypeHostedControlPlane {
			log.Info("Found matching ClusterInstance",
				zap.String("ClusterInstance", ci.Name),
				zap.String("namespace", ci.Namespace))
			return ci, nil
		}
	}

	log.Info("No matching HostedControlPlane ClusterInstance found for ManagedCluster")
	return nil, nil
}

func (r *ManagedClusterReconciler) mapClusterInstanceToManagedCluster(
	ctx context.Context,
	obj *v1alpha1.ClusterInstance,
) []reconcile.Request {

	// Only map HostedControlPlane clusters
	if obj.Spec.ClusterType != v1alpha1.ClusterTypeHostedControlPlane {
		return []reconcile.Request{}
	}

	// If ManagedClusterRef is set, use it
	if obj.Status.ManagedClusterRef != nil && obj.Status.ManagedClusterRef.Name != "" {
		return []reconcile.Request{{
			NamespacedName: types.NamespacedName{
				Name: obj.Status.ManagedClusterRef.Name,
			},
		}}
	}

	// Not yet reconciled: fall back to the expected ManagedCluster name
	if obj.Spec.ClusterName != "" {
		return []reconcile.Request{{
			NamespacedName: types.NamespacedName{Name: obj.Spec.ClusterName},
		}}
	}

	return []reconcile.Request{}
}

// SetupWithManager sets up the controller with the Manager.
func (r *ManagedClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {

	//nolint:wrapcheck
	return ctrl.NewControllerManagedBy(mgr).
		Named("managedClusterReconciler").
		For(&clusterv1.ManagedCluster{},
			// watch for create and update event for ManagedCluster
			builder.WithPredicates(predicate.Funcs{
				GenericFunc: func(e event.GenericEvent) bool { return false },
				CreateFunc: func(e event.CreateEvent) bool {
					return isHostedManagedCluster(e.Object.GetLabels(), e.Object.GetAnnotations())
				},
				DeleteFunc: func(e event.DeleteEvent) bool { return false },
				UpdateFunc: func(e event.UpdateEvent) bool {
					return isHostedManagedCluster(e.ObjectNew.GetLabels(), e.ObjectNew.GetAnnotations())
				},
			})).
		WatchesRawSource(source.TypedKind(mgr.GetCache(),
			&v1alpha1.ClusterInstance{},
			handler.TypedEnqueueRequestsFromMapFunc(r.mapClusterInstanceToManagedCluster),
		)).
		Complete(r)
}
