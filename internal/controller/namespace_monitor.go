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

package controller

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	k8sretry "k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntime "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	"github.com/stolostron/siteconfig/internal/controller/retry"
)

// SharedResourcesNamespaceManagedByLabel identifies the shared-resources namespace as managed by the
// SiteConfig operator.
const SharedResourcesNamespaceManagedByLabel = "app.kubernetes.io/managed-by"

// SharedResourcesNamespaceManagedByValue is the value for the managed-by label on the shared-resources
// namespace.
const SharedResourcesNamespaceManagedByValue = "siteconfig-operator"

// NamespaceMonitor watches for deletion of the shared-resources namespace and re-creates it.
type NamespaceMonitor struct {
	client.Client
	Log    *zap.Logger
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch;delete

// Reconcile re-creates the shared-resources namespace if it has been deleted.
func (r *NamespaceMonitor) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.With(zap.String("name", req.Name))

	// Ignore unrelated namespaces.
	if req.Name != v1alpha1.SharedResourcesNamespace {
		return ctrl.Result{}, nil
	}

	log.Info("Start reconcile")
	defer log.Info("Completed reconcile")

	ns := &corev1.Namespace{}
	err := r.Get(ctx, types.NamespacedName{Name: req.Name}, ns)
	if err == nil {
		// Namespace exists (or is terminating); nothing to do.
		return ctrl.Result{}, nil
	}
	if !errors.IsNotFound(err) {
		log.Error("Failed to get shared-resources namespace", zap.Error(err))
		return ctrl.Result{RequeueAfter: RequeueDelay}, fmt.Errorf("failed to get shared-resources namespace: %w", err)
	}

	log.Info("Shared-resources namespace not found, re-creating it")
	if err := EnsureSharedResourcesNamespace(ctx, r.Client); err != nil {
		log.Error("Failed to re-create shared-resources namespace", zap.Error(err))
		return ctrl.Result{RequeueAfter: RequeueDelay}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager configures the controller to watch for deletion of the shared-resources namespace.
func (r *NamespaceMonitor) SetupWithManager(mgr ctrl.Manager) error {
	isSharedNamespace := func(name string) bool {
		return name == v1alpha1.SharedResourcesNamespace
	}

	// Only process delete events for the shared-resources namespace. The namespace carries no
	// configuration that needs syncing on create/update.
	sharedNamespacePredicate := predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return false },
		UpdateFunc:  func(e event.UpdateEvent) bool { return false },
		GenericFunc: func(e event.GenericEvent) bool { return false },
		DeleteFunc: func(e event.DeleteEvent) bool {
			return isSharedNamespace(e.Object.GetName())
		},
	}

	//nolint:wrapcheck
	return ctrl.NewControllerManagedBy(mgr).
		Named("NamespaceMonitor").
		For(&corev1.Namespace{}).
		WithOptions(ctrlruntime.Options{MaxConcurrentReconciles: 1}).
		WithEventFilter(sharedNamespacePredicate).
		Complete(r)
}

// EnsureSharedResourcesNamespace creates the shared-resources namespace if it does not exist and
// reconciles its managed-by label. It is safe to call concurrently across operator replicas.
func EnsureSharedResourcesNamespace(ctx context.Context, c client.Client) error {
	err := retry.RetryOnConflictOrRetriable(k8sretry.DefaultBackoff, func() error {
		ns := &corev1.Namespace{}
		err := c.Get(ctx, types.NamespacedName{Name: v1alpha1.SharedResourcesNamespace}, ns)
		if err != nil {
			if !errors.IsNotFound(err) {
				return fmt.Errorf("failed to get namespace %s: %w", v1alpha1.SharedResourcesNamespace, err)
			}

			ns = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: v1alpha1.SharedResourcesNamespace,
					Labels: map[string]string{
						SharedResourcesNamespaceManagedByLabel: SharedResourcesNamespaceManagedByValue,
					},
				},
			}
			if err := client.IgnoreAlreadyExists(c.Create(ctx, ns)); err != nil {
				return fmt.Errorf("failed to create namespace %s: %w", v1alpha1.SharedResourcesNamespace, err)
			}
			return nil
		}

		// Namespace exists; reconcile the managed-by label if missing.
		if ns.Labels[SharedResourcesNamespaceManagedByLabel] == SharedResourcesNamespaceManagedByValue {
			return nil
		}

		patch := client.MergeFrom(ns.DeepCopy())
		if ns.Labels == nil {
			ns.Labels = map[string]string{}
		}
		ns.Labels[SharedResourcesNamespaceManagedByLabel] = SharedResourcesNamespaceManagedByValue
		if err := c.Patch(ctx, ns, patch); err != nil {
			return fmt.Errorf("failed to patch labels on namespace %s: %w", v1alpha1.SharedResourcesNamespace, err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to ensure shared-resources namespace: %w", err)
	}
	return nil
}
