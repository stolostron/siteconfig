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
	stderrors "errors"
	"fmt"
	"maps"
	"reflect"

	"go.uber.org/zap"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntime "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	"github.com/stolostron/siteconfig/internal/controller/configuration"
)

// SharedExtraManifestRefIndexKey is the field-index key mapping a ClusterInstance to the names of the
// shared-namespace extra-manifest ConfigMaps it references. It lets SharedManifestSync find, from a
// changed shared ConfigMap, all ClusterInstances that need their managed copy refreshed.
const SharedExtraManifestRefIndexKey = ".spec.extraManifestsRefs.shared"

// SharedManifestSync watches ConfigMaps in the shared-resources namespace and propagates content
// changes to the managed copies in each referencing ClusterInstance's namespace, keeping day-2
// consumers (e.g. ACM policies) current with the source. It runs independently of the
// ClusterInstanceReconciler and does not trigger a ClusterInstance reconcile, re-render, or
// downstream CRD update: it only refreshes ConfigMap content and the source-resource-version
// provenance annotation.
type SharedManifestSync struct {
	client.Client
	Log         *zap.Logger
	Scheme      *runtime.Scheme
	Recorder    events.EventRecorder
	ConfigStore *configuration.ConfigurationStore
}

//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete

// Reconcile propagates a shared ConfigMap's content to every managed copy that references it.
func (r *SharedManifestSync) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.With(zap.String("name", req.Name), zap.String("namespace", req.Namespace))

	// Only shared-namespace ConfigMaps are of interest.
	if req.Namespace != v1alpha1.SharedResourcesNamespace {
		return ctrl.Result{}, nil
	}

	log.Info("Start reconcile")
	defer log.Info("Completed reconcile")

	// Find all ClusterInstances that reference this shared ConfigMap.
	ciList := &v1alpha1.ClusterInstanceList{}
	if err := r.List(ctx, ciList, client.MatchingFields{SharedExtraManifestRefIndexKey: req.Name}); err != nil {
		log.Error("Failed to list ClusterInstances referencing shared ConfigMap", zap.Error(err))
		return ctrl.Result{RequeueAfter: RequeueDelay}, fmt.Errorf("failed to list referencing ClusterInstances: %w", err)
	}

	source := &corev1.ConfigMap{}
	err := r.Get(ctx, req.NamespacedName, source)
	if errors.IsNotFound(err) {
		// The source was deleted. Copies are intentionally retained for day-2 use; emit a warning
		// against each referencing ClusterInstance so the deletion is visible.
		for i := range ciList.Items {
			ci := &ciList.Items[i]
			r.Recorder.Eventf(ci, nil, corev1.EventTypeWarning, "SharedExtraManifestDeleted", "Sync",
				"Shared extra-manifest ConfigMap %s/%s was deleted; "+
					"the managed copy in namespace %s is retained with its last-synced content",
				v1alpha1.SharedResourcesNamespace, req.Name, ci.Namespace)
		}
		return ctrl.Result{}, nil
	}
	if err != nil {
		log.Error("Failed to get shared ConfigMap", zap.Error(err))
		return ctrl.Result{RequeueAfter: RequeueDelay}, fmt.Errorf("failed to get shared ConfigMap %s/%s: %w",
			req.Namespace, req.Name, err)
	}

	if len(ciList.Items) > 0 {
		if err := ensureSharedSourceBackup(ctx, r.Client, source); err != nil {
			return ctrl.Result{}, err
		}
	}
	var propagationErrors []error
	for i := range ciList.Items {
		ci := &ciList.Items[i]
		if err := r.propagate(ctx, log, ci, source); err != nil {
			log.Sugar().Errorf("Failed to propagate shared ConfigMap %s to namespace %s: %v",
				req.Name, ci.Namespace, err)
			propagationErrors = append(propagationErrors, err)
		}
	}
	if err := stderrors.Join(propagationErrors...); err != nil {
		return ctrl.Result{}, fmt.Errorf("shared manifest propagation failed: %w", err)
	}

	return ctrl.Result{}, nil
}

// propagate refreshes a single ClusterInstance's managed copy of the given source ConfigMap. It uses
// an explicit Get-then-Update. Missing copies require an eligible ClusterInstance reconciliation;
// a copy that is not a SiteConfig-managed copy is skipped with a warning Event, and a copy whose
// content already matches the source is skipped to avoid metadata-only churn.
func (r *SharedManifestSync) propagate(
	ctx context.Context,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
	source *corev1.ConfigMap,
) error {
	copy := &corev1.ConfigMap{}
	key := client.ObjectKey{Name: source.Name, Namespace: clusterInstance.Namespace}
	if err := r.Get(ctx, key, copy); err != nil {
		if errors.IsNotFound(err) {
			// The copy does not exist yet; the ClusterInstanceReconciler creates copies during
			// reconcile, so there is nothing to propagate here.
			log.Sugar().Infof("Managed copy %s/%s not found; skipping propagation", key.Namespace, key.Name)
			return nil
		}
		return fmt.Errorf("failed to get managed copy %s/%s: %w", key.Namespace, key.Name, err)
	}

	// Ownership guard: only refresh copies this operator owns for this ClusterInstance. Never
	// overwrite a user-created ConfigMap that happens to share the name.
	if copy.Labels[ManagedCopyLabel] != ManagedCopyLabelValue || !metav1.IsControlledBy(copy, clusterInstance) ||
		copy.Annotations[SourceNamespaceAnnotation] != v1alpha1.SharedResourcesNamespace {
		r.Recorder.Eventf(clusterInstance, nil, corev1.EventTypeWarning, "SharedExtraManifestNotManaged", "Sync",
			"ConfigMap %s/%s is not a SiteConfig-managed copy; skipping propagation of shared extra-manifest %s",
			key.Namespace, key.Name, source.Name)
		return nil
	}

	// Freshness is a content question: propagate only when data/binaryData differ. A source edit that
	// touched metadata alone deliberately does not write, so source-resource-version advances only on
	// an actual content propagation.
	if reflect.DeepEqual(copy.Data, source.Data) && reflect.DeepEqual(copy.BinaryData, source.BinaryData) {
		return nil
	}

	copy.Data = maps.Clone(source.Data)
	copy.BinaryData = source.DeepCopy().BinaryData
	if copy.Annotations == nil {
		copy.Annotations = map[string]string{}
	}
	// Advance the propagation provenance only; initial-resource-version records copy creation
	// and is intentionally left untouched.
	copy.Annotations[SourceResourceVersionAnnotation] = source.ResourceVersion

	if err := r.Update(ctx, copy); err != nil {
		return fmt.Errorf("failed to update managed copy %s/%s: %w", key.Namespace, key.Name, err)
	}

	log.Sugar().Infof("Propagated shared ConfigMap %s to managed copy in namespace %s", source.Name, key.Namespace)
	return nil
}

// SetupWithManager registers the shared-extra-manifest field index and configures the controller to
// watch ConfigMaps in the shared-resources namespace.
func (r *SharedManifestSync) SetupWithManager(mgr ctrl.Manager) error {
	if r.ConfigStore == nil {
		return fmt.Errorf("shared manifest sync configuration is required")
	}
	concurrency := r.ConfigStore.GetSharedManifestSyncMaxConcurrentReconciles()
	if concurrency < 1 {
		return fmt.Errorf("shared manifest sync concurrency must be positive")
	}
	r.Log.Info("Configuring shared manifest sync workers", zap.Int("maxConcurrentReconciles", concurrency))
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&v1alpha1.ClusterInstance{},
		SharedExtraManifestRefIndexKey,
		sharedManifestNames,
	); err != nil {
		return fmt.Errorf("failed to set up shared-extra-manifest field index: %w", err)
	}

	// Only process ConfigMaps in the shared-resources namespace.
	isShared := func(obj client.Object) bool {
		return obj.GetNamespace() == v1alpha1.SharedResourcesNamespace
	}
	sharedConfigMapPredicate := predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return isShared(e.Object) },
		UpdateFunc:  func(e event.UpdateEvent) bool { return isShared(e.ObjectNew) },
		DeleteFunc:  func(e event.DeleteEvent) bool { return isShared(e.Object) },
		GenericFunc: func(e event.GenericEvent) bool { return isShared(e.Object) },
	}

	//nolint:wrapcheck
	return ctrl.NewControllerManagedBy(mgr).
		Named("SharedManifestSync").
		For(&corev1.ConfigMap{}).
		WatchesRawSource(source.Kind(mgr.GetCache(), &corev1.ConfigMap{},
			handler.TypedEnqueueRequestsFromMapFunc(sharedCopySourceRequests))).
		WithOptions(ctrlruntime.Options{MaxConcurrentReconciles: concurrency}).
		WithEventFilter(sharedConfigMapPredicate).
		Complete(r)
}

func sharedManifestNames(obj client.Object) []string {
	ci, ok := obj.(*v1alpha1.ClusterInstance)
	if !ok {
		return nil
	}
	var names []string
	for _, ref := range ci.Spec.ExtraManifestsRefs {
		if ref.Namespace == v1alpha1.SharedResourcesNamespace {
			names = append(names, ref.Name)
		}
	}
	return names
}
