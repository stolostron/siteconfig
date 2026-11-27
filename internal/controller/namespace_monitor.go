/*
Copyright 2025 Red Hat, Inc.

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
	stderrors "errors"
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

const sharedNamespaceEnforceLabel = "pod-security.kubernetes.io/enforce"

// ErrSharedResourcesNamespaceTerminating indicates the shared-resources namespace is being deleted.
// Callers should requeue rather than treat it as an API failure.
var ErrSharedResourcesNamespaceTerminating = stderrors.New("shared-resources namespace is terminating")

// ErrSharedResourcesNamespaceNotManaged indicates an existing namespace with the shared-resources
// name is not owned by the SiteConfig operator and will not be adopted.
var ErrSharedResourcesNamespaceNotManaged = stderrors.New("shared-resources namespace is not managed by the SiteConfig operator")

// errSharedResourcesNamespaceCreated signals that another actor created the namespace between our
// Get and Create calls. It is an internal retry signal, not a Kubernetes conflict.
var errSharedResourcesNamespaceCreated = stderrors.New("shared-resources namespace created concurrently")

// NamespaceMonitor keeps the shared-resources namespace present and labeled.
type NamespaceMonitor struct {
	client.Client
	Log    *zap.Logger
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch;delete

// Reconcile ensures the shared-resources namespace exists, reconciles its operator-owned labels,
// and requeues while it is terminating. EnsureSharedResourcesNamespace also handles an
// AlreadyExists creation race.
func (r *NamespaceMonitor) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.With(zap.String("name", req.Name))

	// Ignore unrelated namespaces.
	if req.Name != v1alpha1.SharedResourcesNamespace {
		return ctrl.Result{}, nil
	}

	log.Info("Start reconcile")
	defer log.Info("Completed reconcile")

	if err := EnsureSharedResourcesNamespace(ctx, r.Client); err != nil {
		switch {
		case stderrors.Is(err, ErrSharedResourcesNamespaceTerminating):
			log.Info("Shared-resources namespace is terminating, requeuing")
			return ctrl.Result{RequeueAfter: RequeueDelay}, nil
		case stderrors.Is(err, ErrSharedResourcesNamespaceNotManaged):
			log.Info("Shared-resources namespace exists but is not managed by the SiteConfig operator; leaving it unchanged")
			return ctrl.Result{}, nil
		default:
			log.Error("Failed to ensure shared-resources namespace", zap.Error(err))
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// sharedNamespaceEventPredicate admits create, update, and delete events for the shared-resources
// namespace. Create is required so a namespace that is already terminating when the manager starts
// is observed and requeued until deletion; update catches a later transition to terminating.
// Reconcile reconciles labels on a healthy namespace and re-creates it when missing.
func sharedNamespaceEventPredicate() predicate.Predicate {
	isSharedNamespace := func(name string) bool {
		return name == v1alpha1.SharedResourcesNamespace
	}

	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return isSharedNamespace(e.Object.GetName()) },
		UpdateFunc:  func(e event.UpdateEvent) bool { return isSharedNamespace(e.ObjectNew.GetName()) },
		GenericFunc: func(e event.GenericEvent) bool { return false },
		DeleteFunc:  func(e event.DeleteEvent) bool { return isSharedNamespace(e.Object.GetName()) },
	}
}

// SetupWithManager configures the controller to watch the shared-resources namespace.
func (r *NamespaceMonitor) SetupWithManager(mgr ctrl.Manager) error {
	//nolint:wrapcheck
	return ctrl.NewControllerManagedBy(mgr).
		Named("NamespaceMonitor").
		For(&corev1.Namespace{}).
		WithOptions(ctrlruntime.Options{MaxConcurrentReconciles: 1}).
		WithEventFilter(sharedNamespaceEventPredicate()).
		Complete(r)
}

// EnsureSharedResourcesNamespace creates the shared-resources namespace when missing and reconciles
// labels on a namespace already owned by the operator. An existing namespace without the operator's
// managed-by marker is refused rather than adopted.
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
						sharedNamespaceEnforceLabel:            "restricted",
					},
				},
			}
			if err := c.Create(ctx, ns); err != nil {
				if errors.IsAlreadyExists(err) {
					// Another actor won the creation race. Re-read it after the retry loop
					// instead of fabricating a Kubernetes conflict.
					return errSharedResourcesNamespaceCreated
				}
				return fmt.Errorf("failed to create namespace %s: %w", v1alpha1.SharedResourcesNamespace, err)
			}
			return nil
		}

		if !ns.DeletionTimestamp.IsZero() {
			return fmt.Errorf("%w: %s", ErrSharedResourcesNamespaceTerminating, v1alpha1.SharedResourcesNamespace)
		}
		if ns.Labels[SharedResourcesNamespaceManagedByLabel] != SharedResourcesNamespaceManagedByValue {
			return fmt.Errorf("%w: %s", ErrSharedResourcesNamespaceNotManaged, v1alpha1.SharedResourcesNamespace)
		}
		if ns.Labels[sharedNamespaceEnforceLabel] == "restricted" {
			return nil
		}

		patch := client.MergeFrom(ns.DeepCopy())
		ns.Labels[sharedNamespaceEnforceLabel] = "restricted"
		if err := c.Patch(ctx, ns, patch); err != nil {
			return fmt.Errorf("failed to patch labels on namespace %s: %w", v1alpha1.SharedResourcesNamespace, err)
		}
		return nil
	})
	if stderrors.Is(err, errSharedResourcesNamespaceCreated) {
		// Re-read the winning namespace so labels/termination are evaluated against it.
		return EnsureSharedResourcesNamespace(ctx, c)
	}
	if err != nil {
		return fmt.Errorf("failed to ensure shared-resources namespace: %w", err)
	}
	return nil
}
