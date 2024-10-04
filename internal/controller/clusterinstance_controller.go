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
	"reflect"
	"sort"
	"time"

	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
	"github.com/stolostron/siteconfig/internal/controller/conditions"
	"go.uber.org/zap"
	"golang.org/x/exp/maps"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/stolostron/siteconfig/api/v1alpha1"
)

const clusterInstanceFinalizer = "clusterinstance." + v1alpha1.Group + "/finalizer"

// ClusterInstanceReconciler reconciles a ClusterInstance object
type ClusterInstanceReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	Recorder   record.EventRecorder
	Log        *zap.Logger
	TmplEngine *ci.TemplateEngine
}

func doNotRequeue() ctrl.Result {
	return ctrl.Result{Requeue: false}
}

func requeueWithError(err error) (ctrl.Result, error) {
	// can not be fixed by user during reconcile
	return ctrl.Result{}, err
}

//+kubebuilder:rbac:groups=siteconfig.open-cluster-management.io,resources=clusterinstances,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=siteconfig.open-cluster-management.io,resources=clusterinstances/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=siteconfig.open-cluster-management.io,resources=clusterinstances/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;create;update;patch;delete
//+kubebuilder:rbac:groups=hive.openshift.io,resources=clusterimagesets,verbs=get;list;watch
//+kubebuilder:rbac:groups=agent-install.openshift.io,resources=infraenvs,verbs=get;create;update;patch;delete
//+kubebuilder:rbac:groups=agent-install.openshift.io,resources=nmstateconfigs,verbs=get;create;update;patch;delete
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=managedclusters,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=register.open-cluster-management.io,resources=managedclusters/accept,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=cluster.open-cluster-management.io,resources=managedclustersets/join,verbs=create
//+kubebuilder:rbac:groups=extensions.hive.openshift.io,resources=agentclusterinstalls,verbs=get;create;update;patch;delete
//+kubebuilder:rbac:groups=extensions.hive.openshift.io,resources=imageclusterinstalls,verbs=get;create;update;patch;delete
//+kubebuilder:rbac:groups=hive.openshift.io,resources=clusterdeployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=hive.openshift.io,resources=clusterdeployments/status,verbs=get;watch
//+kubebuilder:rbac:groups=metal3.io,resources=baremetalhosts,verbs=get;create;update;patch;delete
//+kubebuilder:rbac:groups=agent.open-cluster-management.io,resources=klusterletaddonconfigs,verbs=get;create;update;patch;delete
//+kubebuilder:rbac:groups=metal3.io,resources=hostfirmwaresettings,verbs=get;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ClusterInstanceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {

	log := r.Log.With(
		zap.String("name", req.Name),
		zap.String("namespace", req.Namespace),
	)

	defer func() {
		log.Info("Finished reconciling ClusterInstance")
	}()

	log.Info("Starting reconcile ClusterInstance")

	// Get the ClusterInstance CR
	clusterInstance := &v1alpha1.ClusterInstance{}
	if err := r.Get(ctx, req.NamespacedName, clusterInstance); err != nil {
		if errors.IsNotFound(err) {
			log.Error("ClusterInstance not found")
			return doNotRequeue(), nil
		}
		log.Error("Failed to get ClusterInstance", zap.Error(err))
		// This is likely a case where the API is down, so requeue and try again shortly
		return requeueWithError(err)
	}

	log.Info("Loaded ClusterInstance", zap.String("version", clusterInstance.GetResourceVersion()))

	if res, stop, err := r.handleFinalizer(ctx, log, clusterInstance); !res.IsZero() || stop || err != nil {
		if err != nil {
			log.Error("Encountered error while handling finalizer", zap.Error(err))
		}
		return res, err
	}

	// Pre-empt the reconcile-loop when the ObservedGeneration is the same as the ObjectMeta.Generation
	if clusterInstance.Status.ObservedGeneration == clusterInstance.ObjectMeta.Generation {
		log.Info("ObservedGeneration and ObjectMeta.Generation are the same, pre-empting reconcile")
		return doNotRequeue(), nil
	}

	// Validate ClusterInstance
	if err := r.handleValidate(ctx, log, clusterInstance); err != nil {
		return requeueWithError(err)
	}

	// Render, validate and apply templates
	ok, err := r.handleRenderTemplates(ctx, log, clusterInstance)
	if err != nil {
		return requeueWithError(err)
	}
	if ok {
		log.Info("Finished rendering templates")
	} else {
		log.Info("Failed to render templates")
	}

	// Only update the ObservedGeneration when all the above processes have been successfully executed
	if clusterInstance.Status.ObservedGeneration != clusterInstance.ObjectMeta.Generation {
		log.Sugar().Infof("Updating ObservedGeneration to %d", clusterInstance.ObjectMeta.Generation)
		patch := client.MergeFrom(clusterInstance.DeepCopy())
		clusterInstance.Status.ObservedGeneration = clusterInstance.ObjectMeta.Generation
		return ctrl.Result{}, conditions.PatchCIStatus(ctx, r.Client, clusterInstance, patch)
	}

	return doNotRequeue(), nil
}

func (r *ClusterInstanceReconciler) finalizeClusterInstance(
	ctx context.Context,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
) error {

	// Group the manifests by the sync-wave
	// This is so that the manifests can be deleted in descending order of sync-wave
	manifestGroups := map[int][]v1alpha1.ManifestReference{}
	for _, manifest := range clusterInstance.Status.ManifestsRendered {
		// check if the key exists in the map
		if _, ok := manifestGroups[manifest.SyncWave]; !ok {
			// if key doesn't exist, initialize the slice
			manifestGroups[manifest.SyncWave] = make([]v1alpha1.ManifestReference, 0)
		}
		// append the value to the slice associated with the key
		manifestGroups[manifest.SyncWave] = append(manifestGroups[manifest.SyncWave], manifest)
	}

	syncWaves := maps.Keys(manifestGroups)
	// Sort the syncWaves in descending order
	sort.Sort(sort.Reverse(sort.IntSlice(syncWaves)))

	for _, syncWave := range syncWaves {
		for _, manifest := range manifestGroups[syncWave] {
			obj := &unstructured.Unstructured{}
			obj.SetName(manifest.Name)
			obj.SetNamespace(manifest.Namespace)
			obj.SetAPIVersion(*manifest.APIGroup)
			obj.SetKind(manifest.Kind)

			if err := r.deleteResource(
				ctx,
				log,
				ci.GenerateOwnedByLabelValue(clusterInstance.Namespace, clusterInstance.Name),
				obj,
			); err != nil {
				return err
			}
		}
	}
	log.Info("Successfully finalized ClusterInstance")
	return nil
}

func (r *ClusterInstanceReconciler) deleteResource(
	ctx context.Context,
	log *zap.Logger,
	owner string,
	obj client.Object,
) error {

	resourceId := ci.GetResourceId(
		obj.GetObjectKind().GroupVersionKind().Kind, obj.GetNamespace(), obj.GetName(),
	)
	// Check that the manifest is logically owned-by the ClusterInstance
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
		if errors.IsNotFound(err) {
			log.Sugar().Infof("Skipping deletion of resource %s not found", resourceId)
			return nil
		}
		log.Sugar().Infof("Unable to retrieve resource %s for deletion", resourceId)
		return err
	}
	labels := obj.GetLabels()
	ownedBy, ok := labels[ci.OwnedByLabel]
	if !ok || ownedBy != owner {
		log.Sugar().Infof("Skipping deletion of resource %s not owned-by ClusterInstance %s", resourceId, owner)
		return nil
	}

	// Delete resource
	if err := r.Client.Delete(ctx, obj); err == nil {
		log.Sugar().Infof("Successfully deleted resource %s", resourceId)
	} else if !errors.IsNotFound(err) {
		log.Sugar().Infof("Failed to delete resource %s", resourceId)
		return err
	}

	return nil
}

func (r *ClusterInstanceReconciler) handleFinalizer(
	ctx context.Context,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
) (ctrl.Result, bool, error) {
	// Check if the ClusterInstance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	if clusterInstance.DeletionTimestamp.IsZero() {
		// Check and add finalizer for this CR.
		if !controllerutil.ContainsFinalizer(clusterInstance, clusterInstanceFinalizer) {
			controllerutil.AddFinalizer(clusterInstance, clusterInstanceFinalizer)
			// update and requeue since the finalizer is added
			return ctrl.Result{Requeue: true}, true, r.Update(ctx, clusterInstance)
		}
		return ctrl.Result{}, false, nil
	} else if controllerutil.ContainsFinalizer(clusterInstance, clusterInstanceFinalizer) {
		// Run finalization logic for clusterInstanceFinalizer. If the
		// finalization logic fails, don't remove the finalizer so
		// that we can retry during the next reconciliation.
		if err := r.finalizeClusterInstance(ctx, log, clusterInstance); err != nil {
			return ctrl.Result{}, true, err
		}

		// Remove clusterInstanceFinalizer. Once all finalizers have been
		// removed, the object will be deleted.
		log.Info("Removing ClusterInstance finalizer")
		patch := client.MergeFrom(clusterInstance.DeepCopy())
		if controllerutil.RemoveFinalizer(clusterInstance, clusterInstanceFinalizer) {
			return ctrl.Result{}, true, r.Patch(ctx, clusterInstance, patch)
		}
	}
	return ctrl.Result{}, false, nil
}

func (r *ClusterInstanceReconciler) handleValidate(
	ctx context.Context,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
) error {

	patch := client.MergeFrom(clusterInstance.DeepCopy())

	newCond := metav1.Condition{Type: string(v1alpha1.ClusterInstanceValidated)}
	log.Info("Starting validation")
	err := ci.Validate(ctx, r.Client, clusterInstance)
	if err != nil {
		log.Error("ClusterInstance validation failed due to error", zap.Error(err))

		newCond.Reason = string(v1alpha1.Failed)
		newCond.Status = metav1.ConditionFalse
		newCond.Message = fmt.Sprintf("Validation failed: %s", err.Error())

	} else {
		log.Info("Validation succeeded")

		newCond.Reason = string(v1alpha1.Completed)
		newCond.Status = metav1.ConditionTrue
		newCond.Message = "Validation succeeded"
	}
	log.Info("Finished validation")

	conditions.SetStatusCondition(&clusterInstance.Status.Conditions, v1alpha1.ClusterInstanceConditionType(newCond.Type),
		v1alpha1.ClusterInstanceConditionReason(newCond.Reason), newCond.Status, newCond.Message)

	if updateErr := conditions.PatchCIStatus(ctx, r.Client, clusterInstance, patch); updateErr != nil {
		if err == nil {
			log.Sugar().Errorf("Failed to update ClusterInstance status after validating ClusterInstance, err: %s",
				updateErr.Error)
			err = updateErr
		}
	}

	return err
}

func (r *ClusterInstanceReconciler) renderManifests(
	ctx context.Context,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
) (ci.RenderedObjectCollection, error) {
	log.Info("Starting to render templates")

	patch := client.MergeFrom(clusterInstance.DeepCopy())
	r.TmplEngine.SetLogger(log.Named("TemplateEngine"))
	renderedObjects, err := r.TmplEngine.ProcessTemplates(ctx, r.Client, *clusterInstance)
	if err != nil {
		log.Error("Failed to render templates", zap.Error(err))
		conditions.SetStatusCondition(&clusterInstance.Status.Conditions,
			v1alpha1.RenderedTemplates,
			v1alpha1.Failed,
			metav1.ConditionFalse,
			fmt.Sprintf("Failed to render templates, err= %s", err))
	} else {
		log.Info("Successfully rendered templates")
		conditions.SetStatusCondition(&clusterInstance.Status.Conditions,
			v1alpha1.RenderedTemplates,
			v1alpha1.Completed,
			metav1.ConditionTrue,
			"Rendered templates successfully")
	}

	if updateErr := conditions.PatchCIStatus(ctx, r.Client, clusterInstance, patch); updateErr != nil {
		if err == nil {
			log.Sugar().Errorf("Failed to update ClusterInstance status after rendering templates, err: %v", updateErr)
			err = updateErr
		}
	}

	log.Info("Finished rendering templates")
	return renderedObjects, err
}

// Copy copies all key/value pairs in src adding them to dst.
// When a key in src is already present in dst,
// the value in dst will be overwritten by the value associated
// with the key in src.
func mergeMaps(src, dst map[string]string) map[string]string {
	if len(src) == 0 {
		return dst
	}
	if len(dst) == 0 {
		return src
	}
	maps.Copy(dst, src)
	return dst
}

func createOrPatch(
	ctx context.Context,
	c client.Client,
	log *zap.Logger,
	renderedObj unstructured.Unstructured,
) (controllerutil.OperationResult, error) {
	liveObj := &unstructured.Unstructured{}
	liveObj.SetGroupVersionKind(renderedObj.GroupVersionKind())
	if err := c.Get(ctx, client.ObjectKeyFromObject(&renderedObj), liveObj); err != nil {
		if !errors.IsNotFound(err) {
			return controllerutil.OperationResultNone, err
		}

		if err := c.Create(ctx, &renderedObj); err != nil {
			return controllerutil.OperationResultNone, err
		}
		return controllerutil.OperationResultCreated, nil
	}

	// Object exists, update it
	patch := client.MergeFrom(liveObj.DeepCopy())
	updatedLiveObj := liveObj.DeepCopy()

	// If the resource contains a status then remove it from the unstructured
	// copy to avoid unnecessary patching later.
	unstructured.RemoveNestedField(updatedLiveObj.Object, "status")

	// Merge metadata.annotations and metadata.labels
	updatedLiveObj.SetAnnotations(mergeMaps(renderedObj.GetAnnotations(), liveObj.GetAnnotations()))
	updatedLiveObj.SetLabels(mergeMaps(renderedObj.GetLabels(), liveObj.GetLabels()))

	// Replace updatedLiveObj.spec fields with those in renderedObj.spec
	spec, ok := renderedObj.Object["spec"].(map[string]interface{})
	if !ok {
		log.Sugar().Errorf("missing spec field in rendered install template %s",
			renderedObj.GroupVersionKind().String())
		return controllerutil.OperationResultNone, fmt.Errorf("missing spec in rendered install template")
	}
	for _, key := range maps.Keys(spec) {
		nestedField := []string{"spec", key}
		nestedValue, ok, err := unstructured.NestedFieldCopy(renderedObj.Object, nestedField...)
		if err != nil {
			return controllerutil.OperationResultNone, err
		}
		if !ok {
			// This situation should never occur, as it implies that a field present in the rendered object spec keys
			// is somehow missing from the actual rendered object spec.
			log.Sugar().Warnf("missing field %s in rendered install template %s",
				key, renderedObj.GroupVersionKind().String())
			continue
		}
		if err := unstructured.SetNestedField(updatedLiveObj.Object, nestedValue, nestedField...); err != nil {
			return controllerutil.OperationResultNone, err
		}
	}

	resourceId := ci.GetResourceId(
		updatedLiveObj.GetName(),
		updatedLiveObj.GetNamespace(),
		updatedLiveObj.GetKind(),
	)
	// Compute difference between the liveObject and updatedLiveObject without "status" fields
	unstructured.RemoveNestedField(liveObj.Object, "status")
	if !reflect.DeepEqual(liveObj, updatedLiveObj) {
		log.Debug(fmt.Sprintf("Change detected in resource %s, updating it", resourceId))

		// Only issue a Patch if the before and after resources (minus status) differ
		if err := c.Patch(ctx, updatedLiveObj, patch); err != nil {
			log.Info(fmt.Sprintf("Failed to update resource %s", resourceId))
			return controllerutil.OperationResultNone, err
		}
		return controllerutil.OperationResultUpdated, nil
	}
	log.Debug(fmt.Sprintf("No change detected in resource %s, skipping update request", resourceId))

	return controllerutil.OperationResultNone, nil
}

// createManifestReference creates a ManifestReference object from a manifest item
func createManifestReference(object ci.RenderedObject) (*v1alpha1.ManifestReference, error) {
	apiVersion := object.GetAPIVersion()
	syncWave, err := object.GetSyncWave()
	if err != nil {
		return nil, err
	}

	return &v1alpha1.ManifestReference{
		Name:            object.GetName(),
		Namespace:       object.GetNamespace(),
		Kind:            object.GetKind(),
		APIGroup:        &apiVersion,
		SyncWave:        syncWave,
		LastAppliedTime: metav1.NewTime(time.Now())}, nil
}

func (r *ClusterInstanceReconciler) executeRenderedManifests(
	ctx context.Context,
	c client.Client,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
	objects []ci.RenderedObject,
	manifestStatus string) (bool, error) {

	ok := true
	patch := client.MergeFrom(clusterInstance.DeepCopy())

	for _, object := range objects {

		manifestRef, err := createManifestReference(object)
		if err != nil {
			return false, err
		}

		if result, err := createOrPatch(ctx, c, log, object.GetObject()); err != nil {
			ok = false
			setManifestFailure(manifestRef, err)
		} else if result != controllerutil.OperationResultNone {
			setManifestSuccess(manifestRef, manifestStatus)
		}

		updateClusterInstanceStatus(clusterInstance, manifestRef)
	}

	return ok, conditions.PatchCIStatus(ctx, r.Client, clusterInstance, patch)
}

func setManifestFailure(manifestRef *v1alpha1.ManifestReference, err error) {
	manifestRef.Status = v1alpha1.ManifestRenderedFailure
	manifestRef.Message = err.Error()
}

func setManifestSuccess(manifestRef *v1alpha1.ManifestReference, manifestStatus string) {
	manifestRef.Status = manifestStatus
	manifestRef.Message = ""
}

func removeClusterInstanceStatus(clusterInstance *v1alpha1.ClusterInstance, manifestRef v1alpha1.ManifestReference) {
	for index, m := range clusterInstance.Status.ManifestsRendered {
		if *manifestRef.APIGroup == *m.APIGroup && manifestRef.Kind == m.Kind && manifestRef.Name == m.Name {
			clusterInstance.Status.ManifestsRendered = append(
				clusterInstance.Status.ManifestsRendered[:index],
				clusterInstance.Status.ManifestsRendered[index+1:]...,
			)
		}
	}
}

func updateClusterInstanceStatus(clusterInstance *v1alpha1.ClusterInstance, manifestRef *v1alpha1.ManifestReference) {
	if found := findManifestRendered(manifestRef, clusterInstance.Status.ManifestsRendered); found != nil {
		if found.Status != manifestRef.Status || found.Message != manifestRef.Message {
			found.LastAppliedTime = manifestRef.LastAppliedTime
			found.Status = manifestRef.Status
			found.Message = manifestRef.Message
		}
	} else {
		clusterInstance.Status.ManifestsRendered = append(clusterInstance.Status.ManifestsRendered, *manifestRef)
	}
}

func findManifestRendered(
	manifest *v1alpha1.ManifestReference,
	manifestList []v1alpha1.ManifestReference,
) *v1alpha1.ManifestReference {
	for index, m := range manifestList {
		if *manifest.APIGroup == *m.APIGroup && manifest.Kind == m.Kind && manifest.Name == m.Name {
			return &manifestList[index]
		}
	}
	return nil
}

func (r *ClusterInstanceReconciler) validateRenderedManifests(
	ctx context.Context,
	pLog *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
	objects []ci.RenderedObject) (rendered bool, err error) {

	log := pLog.Named("validateRenderedManifests")
	log.Info("Executing a dry-run validation on the rendered manifests")

	dryRunClient := client.NewDryRunClient(r.Client)
	patch := client.MergeFrom(clusterInstance.DeepCopy())
	rendered, err = r.executeRenderedManifests(ctx, dryRunClient, log, clusterInstance, objects,
		v1alpha1.ManifestRenderedValidated)
	if err != nil || !rendered {
		msg := "failed to validate rendered manifests using dry-run validation"
		if err != nil {
			msg = fmt.Sprintf(", err: %v", err)
		}
		log.Info(msg)

		conditions.SetStatusCondition(&clusterInstance.Status.Conditions,
			v1alpha1.RenderedTemplatesValidated,
			v1alpha1.Failed,
			metav1.ConditionFalse,
			"Rendered manifests failed dry-run validation")
	} else {
		conditions.SetStatusCondition(&clusterInstance.Status.Conditions,
			v1alpha1.RenderedTemplatesValidated,
			v1alpha1.Completed,
			metav1.ConditionTrue,
			"Rendered templates validation succeeded")
	}

	if updateErr := conditions.PatchCIStatus(ctx, r.Client, clusterInstance, patch); updateErr != nil {
		if err == nil {
			log.Error("failed to update ClusterInstance status post validation of rendered templates",
				zap.Error(updateErr))
			err = updateErr
		}
	}

	log.Info("Finished executing a dry-run validation on the rendered manifests")

	return rendered, err
}

func (r *ClusterInstanceReconciler) applyRenderedManifests(
	ctx context.Context,
	pLog *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
	objects []ci.RenderedObject) (rendered bool, err error) {

	log := pLog.Named("applyRenderedManifests")
	log.Info("Applying the rendered manifests")

	patch := client.MergeFrom(clusterInstance.DeepCopy())
	if rendered, err = r.executeRenderedManifests(ctx,
		r.Client,
		log,
		clusterInstance,
		objects,
		v1alpha1.ManifestRenderedSuccess,
	); err != nil || !rendered {
		msg := "failed to apply rendered manifests"
		if err != nil {
			msg = fmt.Sprintf(", err: %v", err)
		}
		log.Info(msg)

		conditions.SetStatusCondition(&clusterInstance.Status.Conditions,
			v1alpha1.RenderedTemplatesApplied,
			v1alpha1.Failed,
			metav1.ConditionFalse,
			"Failed to apply site config manifests")
	} else {
		conditions.SetStatusCondition(&clusterInstance.Status.Conditions,
			v1alpha1.RenderedTemplatesApplied,
			v1alpha1.Completed,
			metav1.ConditionTrue,
			"Applied site config manifests")
	}

	if updateErr := conditions.PatchCIStatus(ctx, r.Client, clusterInstance, patch); updateErr != nil {
		if err == nil {
			log.Error("failed to update ClusterInstance status post creation of rendered templates",
				zap.Error(updateErr))
			err = updateErr
		}
	}

	log.Info("Finished applying the rendered manifests")
	return
}

func (r *ClusterInstanceReconciler) pruneManifests(
	ctx context.Context,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
	objects []ci.RenderedObject,
) (bool, error) {
	ok := true
	patch := client.MergeFrom(clusterInstance.DeepCopy())

	for _, object := range objects {

		apiGroup := object.GetAPIVersion()
		manifestRef := v1alpha1.ManifestReference{
			APIGroup:  &apiGroup,
			Kind:      object.GetKind(),
			Name:      object.GetName(),
			Namespace: object.GetNamespace(),
		}
		obj := object.GetObject()
		if err := r.deleteResource(
			ctx,
			log,
			ci.GenerateOwnedByLabelValue(clusterInstance.Namespace, clusterInstance.Name),
			&obj,
		); err == nil {
			// Remove rendered manifest information from status.RenderedManifests
			removeClusterInstanceStatus(clusterInstance, manifestRef)
		} else {
			ok = false
			manifestRef.Status = v1alpha1.ManifestPruneFailure
			manifestRef.Message = err.Error()
			updateClusterInstanceStatus(clusterInstance, &manifestRef)
		}
	}

	return ok, conditions.PatchCIStatus(ctx, r.Client, clusterInstance, patch)
}

func (r *ClusterInstanceReconciler) updateSuppressedManifestsStatus(
	ctx context.Context,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
	objects []ci.RenderedObject,
) error {

	patch := client.MergeFrom(clusterInstance.DeepCopy())

	for _, object := range objects {
		resourceId := ci.GetResourceId(object.GetKind(), object.GetNamespace(), object.GetName())
		manifestRef, err := createManifestReference(object)
		if err != nil {
			return err
		}
		manifestRef.Status = v1alpha1.ManifestSuppressed
		manifestRef.Message = ""
		updateClusterInstanceStatus(clusterInstance, manifestRef)
		log.Sugar().Infof("Suppressed manifest %s", resourceId)
	}

	return conditions.PatchCIStatus(ctx, r.Client, clusterInstance, patch)
}

func (r *ClusterInstanceReconciler) handleRenderTemplates(
	ctx context.Context,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
) (ok bool, err error) {

	ok = false

	// Render templates manifests
	log.Info("Starting to render templates")
	objects, err := r.renderManifests(ctx, log, clusterInstance)
	if err != nil {
		r.Log.Info(
			fmt.Sprintf("encountered error while rendering templates for ClusterInstance %s, err: %v",
				clusterInstance.Name, err))
		return
	}

	// Prune resources in descending order of sync-wave
	if ok, err = r.pruneManifests(ctx, log, clusterInstance, objects.GetPruneObjects()); !ok || err != nil {
		return
	}

	// Update status for manifests previously rendered, but  have been listed for suppression
	if err = r.updateSuppressedManifestsStatus(ctx, log, clusterInstance, objects.GetSuppressObjects()); err != nil {
		return
	}

	// Validate rendered manifests using kubernetes dry-run
	renderList := objects.GetRenderObjects()
	if ok, err = r.validateRenderedManifests(ctx, log, clusterInstance, renderList); !ok || err != nil {
		return
	}

	// Apply the rendered manifests
	ok, err = r.applyRenderedManifests(ctx, log, clusterInstance, renderList)

	return
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterInstanceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("ClusterInstance")

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ClusterInstance{}).
		WithEventFilter(predicate.Or(predicate.GenerationChangedPredicate{}, predicate.LabelChangedPredicate{})).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Complete(r)
}
