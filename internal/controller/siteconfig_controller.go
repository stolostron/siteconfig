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

	clusterv1 "open-cluster-management.io/api/cluster/v1"

	"github.com/go-logr/logr"
	"github.com/sakhoury/siteconfig/internal/controller/conditions"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/sakhoury/siteconfig/api/v1alpha1"
)

const (
	siteConfigFinalizer = "metaclusterinstall.openshift.io/finalizer"
	managedClusterKind  = "ManagedCluster"
)

// SiteConfigReconciler reconciles a SiteConfig object
type SiteConfigReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
	Log       logr.Logger
	ScBuilder *SiteConfigBuilder
}

//nolint:unused
func doNotRequeue() ctrl.Result {
	return ctrl.Result{Requeue: false}
}

//nolint:unused
func requeueWithError(err error) (ctrl.Result, error) {
	// can not be fixed by user during reconcile
	return ctrl.Result{}, err
}

//nolint:unused
func requeueImmediately() ctrl.Result {
	// Allow a brief pause in case there's a delay with a DB Update
	return ctrl.Result{Requeue: true}
}

//nolint:unused
func requeueWithShortInterval() ctrl.Result {
	return requeueWithCustomInterval(30 * time.Second)
}

//nolint:unused
func requeueWithMediumInterval() ctrl.Result {
	return requeueWithCustomInterval(1 * time.Minute)
}

//nolint:unused
func requeueWithLongInterval() ctrl.Result {
	return requeueWithCustomInterval(5 * time.Minute)
}

//nolint:unused
func requeueWithCustomInterval(interval time.Duration) ctrl.Result {
	return ctrl.Result{RequeueAfter: interval}
}

//+kubebuilder:rbac:groups=metaclusterinstall.openshift.io,resources=siteconfigs,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=metaclusterinstall.openshift.io,resources=siteconfigs/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=metaclusterinstall.openshift.io,resources=siteconfigs/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;update;patch
//+kubebuilder:rbac:groups=hive.openshift.io,resources=clusterimagesets,verbs=get;list;watch
//+kubebuilder:rbac:groups=agent-install.openshift.io,resources=infraenvs,verbs=get;create;update;patch
//+kubebuilder:rbac:groups=agent-install.openshift.io,resources=nmstateconfigs,verbs=get;create;update;patch
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=managedclusters,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=register.open-cluster-management.io,resources=managedclusters/accept,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=managedclustersets/join,verbs=create
//+kubebuilder:rbac:groups=extensions.hive.openshift.io,resources=agentclusterinstalls,verbs=get;create;update;patch
//+kubebuilder:rbac:groups=extensions.hive.openshift.io,resources=imageclusterinstalls,verbs=get;create;update;patch;delete
//+kubebuilder:rbac:groups=hive.openshift.io,resources=clusterdeployments,verbs=get;list;watch;create;update;patch
//+kubebuilder:rbac:groups=hive.openshift.io,resources=clusterdeployments/status,verbs=get;watch
//+kubebuilder:rbac:groups=metal3.io,resources=baremetalhosts,verbs=get;create;update;patch
//+kubebuilder:rbac:groups=agent.open-cluster-management.io,resources=klusterletaddonconfigs,verbs=get;create;update;patch
//+kubebuilder:rbac:groups=metal3.io,resources=hostfirmwaresettings,verbs=get;create;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *SiteConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	defer func() {
		r.Log.Info("Finish reconciling SiteConfig", "name", req.NamespacedName)
	}()

	r.Log.Info("Start reconciling SiteConfig", "name", req.NamespacedName)

	// Get the SiteConfig CR
	siteConfig := &v1alpha1.SiteConfig{}
	if err := r.Get(ctx, req.NamespacedName, siteConfig); err != nil {
		if errors.IsNotFound(err) {
			r.Log.Info("SiteConfig not found", "name", siteConfig.Name)
			return doNotRequeue(), nil
		}
		r.Log.Error(err, "Failed to get SiteConfig")
		// This is likely a case where the API is down, so requeue and try again shortly
		return requeueWithError(err)
	}

	r.Log.Info("Loaded SiteConfig", "name", req.NamespacedName, "version", siteConfig.GetResourceVersion())

	if res, stop, err := r.handleFinalizer(ctx, siteConfig); !res.IsZero() || stop || err != nil {
		if err != nil {
			r.Log.Error(err, "encountered error while handling finalizer", "SiteConfig", siteConfig.Name)
		}
		return res, err
	}

	if !r.isSiteConfigValidated(siteConfig) {
		if err := r.handleValidate(ctx, siteConfig); err != nil {
			return requeueWithError(err)
		}
	}
	r.Log.Info("SiteConfig is validated", "name", req.NamespacedName)

	return ctrl.Result{}, nil
}

func (r *SiteConfigReconciler) finalizeSiteConfig(ctx context.Context, siteConfig *v1alpha1.SiteConfig) error {
	// check if managedcluster resource exists in rendered manifests
	for _, manifest := range siteConfig.Status.ManifestsRendered {
		if manifest.Kind == managedClusterKind {
			// delete ManagedCluster resource
			managedCluster := &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: manifest.Name,
				},
			}
			if err := r.Client.Delete(ctx, managedCluster); err != nil {
				r.Log.Info("Failed to delete ManagedCluster", "ManagedCluster", manifest.Name)
				return err
			}
			r.Log.Info("Successfully deleted ManagedCluster", "ManagedCluster", manifest.Name)
		}
	}
	r.Log.Info("Successfully finalized SiteConfig", "name", siteConfig.Name)
	return nil
}

func (r *SiteConfigReconciler) handleFinalizer(ctx context.Context, siteConfig *v1alpha1.SiteConfig) (ctrl.Result, bool, error) {
	// Check if the SiteConfig instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	if siteConfig.DeletionTimestamp.IsZero() {
		// Check and add finalizer for this CR.
		if !controllerutil.ContainsFinalizer(siteConfig, siteConfigFinalizer) {
			controllerutil.AddFinalizer(siteConfig, siteConfigFinalizer)
			// update and requeue since the finalizer is added
			return ctrl.Result{Requeue: true}, true, r.Update(ctx, siteConfig)
		}
		return ctrl.Result{}, false, nil
	} else if controllerutil.ContainsFinalizer(siteConfig, siteConfigFinalizer) {
		// Run finalization logic for siteConfigFinalizer. If the
		// finalization logic fails, don't remove the finalizer so
		// that we can retry during the next reconciliation.
		if err := r.finalizeSiteConfig(ctx, siteConfig); err != nil {
			return ctrl.Result{}, true, err
		}

		// Remove siteConfigFinalizer. Once all finalizers have been
		// removed, the object will be deleted.
		if controllerutil.RemoveFinalizer(siteConfig, siteConfigFinalizer) {
			return ctrl.Result{}, true, r.Update(ctx, siteConfig)
		}
	}
	return ctrl.Result{}, false, nil
}

func (r *SiteConfigReconciler) isSiteConfigValidated(siteConfig *v1alpha1.SiteConfig) bool {
	condition := meta.FindStatusCondition(siteConfig.Status.Conditions, string(conditions.SiteConfigValidated))
	if condition != nil && condition.Status == metav1.ConditionTrue {
		return true
	}
	return false
}

func (r *SiteConfigReconciler) handleValidate(ctx context.Context, siteConfig *v1alpha1.SiteConfig) error {
	r.Log.Info("Starting validation", "SiteConfig", siteConfig.Name)
	if err := validateSiteConfig(ctx, r.Client, siteConfig); err != nil {
		r.Log.Error(err, "SiteConfig validation failed due to error", "SiteConfig", siteConfig.Name)
		conditions.SetStatusCondition(&siteConfig.Status.Conditions,
			conditions.SiteConfigValidated,
			conditions.Failed,
			metav1.ConditionFalse,
			fmt.Sprintf("Validation failed: %s", err.Error()))
	} else {
		r.Log.Info("Validation succeeded", "SiteConfig", siteConfig.Name)
		conditions.SetStatusCondition(&siteConfig.Status.Conditions,
			conditions.SiteConfigValidated,
			conditions.Completed,
			metav1.ConditionTrue,
			"Validation succeeded")
	}
	r.Log.Info("Finished validation", "SiteConfig", siteConfig.Name)
	return conditions.UpdateSiteConfigStatus(ctx, r.Client, siteConfig)
}

// SetupWithManager sets up the controller with the Manager.
func (r *SiteConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("SiteConfig")

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.SiteConfig{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Complete(r)
}
