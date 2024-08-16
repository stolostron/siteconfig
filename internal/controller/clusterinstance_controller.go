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
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
	"github.com/stolostron/siteconfig/internal/controller/conditions"
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
	Log        logr.Logger
	TmplEngine *ci.TemplateEngine
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
	defer func() {
		r.Log.Info("Finished reconciling ClusterInstance", "name", req.NamespacedName)
	}()

	r.Log.Info("Start reconciling ClusterInstance", "name", req.NamespacedName)

	// Get the ClusterInstance CR
	clusterInstance := &v1alpha1.ClusterInstance{}
	if err := r.Get(ctx, req.NamespacedName, clusterInstance); err != nil {
		if errors.IsNotFound(err) {
			r.Log.Info("ClusterInstance not found", "name", req.NamespacedName)
			return doNotRequeue(), nil
		}
		r.Log.Error(err, "Failed to get ClusterInstance", "name", req.NamespacedName)
		// This is likely a case where the API is down, so requeue and try again shortly
		return requeueWithError(err)
	}

	r.Log.Info("Loaded ClusterInstance", "name", req.NamespacedName, "version", clusterInstance.GetResourceVersion())

	if res, stop, err := r.handleFinalizer(ctx, clusterInstance); !res.IsZero() || stop || err != nil {
		if err != nil {
			r.Log.Error(err, "Encountered error while handling finalizer", "ClusterInstance", req.NamespacedName)
		}
		return res, err
	}

	// Pre-empt the reconcile-loop when the ObservedGeneration is the same as the ObjectMeta.Generation
	if clusterInstance.Status.ObservedGeneration == clusterInstance.ObjectMeta.Generation {
		r.Log.Info("ObservedGeneration and ObjectMeta.Generation are the same, pre-empting reconcile",
			"ClusterInstance", req.NamespacedName)
		return doNotRequeue(), nil
	}

	// Validate ClusterInstance
	if err := r.handleValidate(ctx, clusterInstance); err != nil {
		return requeueWithError(err)
	}

	// Render, validate and apply templates
	if rendered, err := r.handleRenderTemplates(ctx, clusterInstance); err != nil {
		return requeueWithError(err)
	} else if rendered {
		r.Log.Info("ClusterInstance templates are rendered", "name", req.NamespacedName)
	} else {
		r.Log.Info("Failed to render templates for ClusterInstance", "name", req.NamespacedName)
	}

	// Update manifests' status that have been flagged for suppression
	if err := r.updateSuppressedManifestsStatus(ctx, clusterInstance); err != nil {
		return requeueWithError(err)
	}

	// Only update the ObservedGeneration when all the above processes have been successfully executed
	if clusterInstance.Status.ObservedGeneration != clusterInstance.ObjectMeta.Generation {
		r.Log.Info(
			fmt.Sprintf("Updating ObservedGeneration to %d", clusterInstance.ObjectMeta.Generation),
			"ClusterInstance", req.NamespacedName)
		patch := client.MergeFrom(clusterInstance.DeepCopy())
		clusterInstance.Status.ObservedGeneration = clusterInstance.ObjectMeta.Generation
		return ctrl.Result{}, conditions.PatchCIStatus(ctx, r.Client, clusterInstance, patch)
	}

	return doNotRequeue(), nil
}

func (r *ClusterInstanceReconciler) finalizeClusterInstance(
	ctx context.Context,
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
			if err := r.Client.Delete(ctx, obj); err == nil {
				r.Log.Info("Successfully deleted resource", manifest.Kind, manifest.Name)
			} else if !errors.IsNotFound(err) {
				r.Log.Info("Failed to delete resource", manifest.Kind, manifest.Name)
				return err
			}
		}
	}
	r.Log.Info("Successfully finalized ClusterInstance", "name", clusterInstance.Name)
	return nil
}

func (r *ClusterInstanceReconciler) handleFinalizer(
	ctx context.Context,
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
		if err := r.finalizeClusterInstance(ctx, clusterInstance); err != nil {
			return ctrl.Result{}, true, err
		}

		// Remove clusterInstanceFinalizer. Once all finalizers have been
		// removed, the object will be deleted.
		r.Log.Info("Removing ClusterInstance finalizer", "name", clusterInstance.Name)
		patch := client.MergeFrom(clusterInstance.DeepCopy())
		if controllerutil.RemoveFinalizer(clusterInstance, clusterInstanceFinalizer) {
			return ctrl.Result{}, true, r.Patch(ctx, clusterInstance, patch)
		}
	}
	return ctrl.Result{}, false, nil
}

func (r *ClusterInstanceReconciler) handleValidate(
	ctx context.Context,
	clusterInstance *v1alpha1.ClusterInstance,
) error {

	patch := client.MergeFrom(clusterInstance.DeepCopy())

	newCond := metav1.Condition{Type: string(conditions.ClusterInstanceValidated)}
	r.Log.Info("Starting validation", "ClusterInstance", clusterInstance.Name)
	err := ci.Validate(ctx, r.Client, clusterInstance)
	if err != nil {
		r.Log.Error(err, "ClusterInstance validation failed due to error", "ClusterInstance", clusterInstance.Name)

		newCond.Reason = string(conditions.Failed)
		newCond.Status = metav1.ConditionFalse
		newCond.Message = fmt.Sprintf("Validation failed: %s", err.Error())

	} else {
		r.Log.Info("Validation succeeded", "ClusterInstance", clusterInstance.Name)

		newCond.Reason = string(conditions.Completed)
		newCond.Status = metav1.ConditionTrue
		newCond.Message = "Validation succeeded"
	}
	r.Log.Info("Finished validation", "ClusterInstance", clusterInstance.Name)

	conditions.SetStatusCondition(&clusterInstance.Status.Conditions, conditions.ConditionType(newCond.Type),
		conditions.ConditionReason(newCond.Reason), newCond.Status, newCond.Message)

	if updateErr := conditions.PatchCIStatus(ctx, r.Client, clusterInstance, patch); updateErr != nil {
		if err == nil {
			r.Log.Info(
				fmt.Sprintf("Failed to update ClusterInstance %s status after validating ClusterInstance, err: %s",
					clusterInstance.Name, updateErr.Error()))
			err = updateErr
		}
	}

	return err
}

func (r *ClusterInstanceReconciler) renderManifests(
	ctx context.Context,
	clusterInstance *v1alpha1.ClusterInstance,
) ([]interface{}, error) {
	r.Log.Info(fmt.Sprintf("Rendering templates for ClusterInstance %s", clusterInstance.Name))

	patch := client.MergeFrom(clusterInstance.DeepCopy())
	renderedManifests, err := r.TmplEngine.ProcessTemplates(ctx, r.Client, *clusterInstance)
	if err != nil {
		r.Log.Error(err, "Failed to render manifests", "ClusterInstance", clusterInstance.Name)
		conditions.SetStatusCondition(&clusterInstance.Status.Conditions,
			conditions.RenderedTemplates,
			conditions.Failed,
			metav1.ConditionFalse,
			fmt.Sprintf("Failed to render templates, err= %s", err))
	} else {
		conditions.SetStatusCondition(&clusterInstance.Status.Conditions,
			conditions.RenderedTemplates,
			conditions.Completed,
			metav1.ConditionTrue,
			"Rendered templates successfully")
	}

	if updateErr := conditions.PatchCIStatus(ctx, r.Client, clusterInstance, patch); updateErr != nil {
		if err == nil {
			r.Log.Info(
				fmt.Sprintf("Failed to update ClusterInstance %s status after rendering templates, err: %s",
					clusterInstance.Name, updateErr.Error()))
			err = updateErr
		}
	}

	return renderedManifests, err
}

// getSyncWave extracts the syncWave from the given object manifest
// if the syncWave cannot be parsed, a nil-int pointer is returned with the error
func getSyncWave(object interface{}) (*int, error) {
	var (
		manifest    map[string]interface{}
		kind        string
		ok          bool
		err         error
		syncWaveStr string
		syncWave    int
	)

	if manifest, ok = object.(map[string]interface{}); !ok {
		return nil, fmt.Errorf("manifest should be of type 'map[string]interface{}'")
	}

	if kind, ok = manifest["kind"].(string); !ok {
		return nil, fmt.Errorf("missing field `kind` from rendered manifest")
	}

	syncWaveStr = ci.DefaultWaveAnnotation
	if metadata, ok := manifest["metadata"].(map[string]interface{}); ok {
		if annotations, ok := metadata["annotations"].(map[string]interface{}); ok {
			if syncWaveStr, ok = annotations[ci.WaveAnnotation].(string); !ok {
				syncWaveStr = ci.DefaultWaveAnnotation
			}
		}
	}

	if syncWave, err = strconv.Atoi(syncWaveStr); err != nil {
		return nil, fmt.Errorf("failed to extract annotation %s in resource %s", ci.WaveAnnotation, kind)
	}

	return &syncWave, nil
}

// groupAndSortManifests categorizes manifests by the sync-wave and then
// sorts the groups alphabetically by manifest kind
func groupAndSortManifests(manifests []interface{}) (map[int][]interface{}, error) {
	manifestGroups := make(map[int][]interface{}, len(manifests))
	for _, object := range manifests {

		var (
			ok          bool
			err         error
			syncWavePtr *int
		)

		if syncWavePtr, err = getSyncWave(object); err != nil || syncWavePtr == nil {
			return nil, err
		}

		// check if the key exists in the map
		if _, ok = manifestGroups[*syncWavePtr]; !ok {
			// if key doesn't exist, initialize the slice
			manifestGroups[*syncWavePtr] = make([]interface{}, 0)
		}
		// append the value to the slice associated with the key
		manifestGroups[*syncWavePtr] = append(manifestGroups[*syncWavePtr], object)
	}

	// sort grouped manifests alphabetically (by "kind") to make rendering more deterministic
	for _, syncWaveGroup := range manifestGroups {
		sort.Slice(syncWaveGroup, func(x, y int) bool {
			manifestX := syncWaveGroup[x].(map[string]interface{})
			manifestY := syncWaveGroup[y].(map[string]interface{})
			kindX := manifestX["kind"].(string)
			kindY := manifestY["kind"].(string)
			return kindX < kindY
		})
	}

	return manifestGroups, nil
}

func createOrPatch(
	ctx context.Context,
	c client.Client,
	obj unstructured.Unstructured,
	f controllerutil.MutateFn,
) (controllerutil.OperationResult, error) {
	existingObj := &unstructured.Unstructured{}
	existingObj.SetGroupVersionKind(obj.GroupVersionKind())
	if err := c.Get(ctx, client.ObjectKeyFromObject(&obj), existingObj); err != nil {
		if !errors.IsNotFound(err) {
			return controllerutil.OperationResultNone, err
		}

		// Mutate the object
		if f != nil {
			if err := f(); err != nil {
				return controllerutil.OperationResultNone, err
			}
		}

		if err := c.Create(ctx, &obj); err != nil {
			return controllerutil.OperationResultNone, err
		}
		return controllerutil.OperationResultCreated, nil
	}

	// Object exists, update it
	obj.SetResourceVersion(existingObj.GetResourceVersion())
	obj.SetOwnerReferences(existingObj.GetOwnerReferences())
	patch := client.MergeFrom(existingObj)

	if err := c.Patch(ctx, &obj, patch); err != nil {
		return controllerutil.OperationResultNone, err
	}

	return controllerutil.OperationResultUpdated, nil
}

func toUnstructured(obj interface{}) (unstructured.Unstructured, error) {
	var uObj unstructured.Unstructured
	// Marshal the input object to JSON
	if content, err := json.Marshal(obj); err != nil {
		return uObj, err
	} else if err = json.Unmarshal(content, &uObj); err != nil {
		return uObj, err
	}
	return uObj, nil
}

// createManifestReference creates a ManifestReference object from a manifest item
func createManifestReference(manifestItem interface{}, syncWave int) (*v1alpha1.ManifestReference, error) {
	var (
		manifest, metadata  map[string]interface{}
		apiVersion, kind    string
		name, namespace     string
		ok, namespaceScoped bool
	)

	if manifest, ok = manifestItem.(map[string]interface{}); !ok {
		return nil, fmt.Errorf("failed to interpret manifest type")
	}

	if metadata, ok = manifest["metadata"].(map[string]interface{}); !ok {
		return nil, fmt.Errorf("failed to interpret metadata")
	}

	if name, ok = metadata["name"].(string); !ok {
		return nil, fmt.Errorf("failed to extract name in metadata %v", metadata)
	}

	if apiVersion, ok = manifest["apiVersion"].(string); !ok {
		return nil, fmt.Errorf("failed to extract apiVersion in metadata %v", metadata)
	}

	if kind, ok = manifest["kind"].(string); !ok {
		return nil, fmt.Errorf("failed to extract manifest kind in metadata %v", metadata)
	}

	manifestRef := &v1alpha1.ManifestReference{
		Name:            name,
		Kind:            kind,
		APIGroup:        &apiVersion,
		SyncWave:        syncWave,
		LastAppliedTime: metav1.NewTime(time.Now())}

	if namespace, namespaceScoped = metadata["namespace"].(string); namespaceScoped {
		manifestRef.Namespace = namespace
	}

	return manifestRef, nil
}

func (r *ClusterInstanceReconciler) executeRenderedManifests(
	ctx context.Context,
	c client.Client,
	clusterInstance *v1alpha1.ClusterInstance,
	manifestGroups map[int][]interface{},
	manifestStatus string) (bool, error) {

	successfulExecution := true
	patch := client.MergeFrom(clusterInstance.DeepCopy())

	// Get the syncWaves of the map
	syncWaves := getSortedSyncWaves(manifestGroups)

	for _, syncWave := range syncWaves {
		group := manifestGroups[syncWave]
		for _, item := range group {

			manifestRef, err := createManifestReference(item, syncWave)
			if err != nil {
				return false, err
			}

			if obj, err := toUnstructured(item); err != nil {
				successfulExecution = false
				setManifestFailure(manifestRef, err)
			} else {
				if result, err := createOrPatch(
					ctx, c, obj,
					setOwnerRefFunc(manifestRef.Namespace, clusterInstance, &obj, r.Scheme)); err != nil {
					successfulExecution = false
					setManifestFailure(manifestRef, err)
				} else if result != controllerutil.OperationResultNone {
					setManifestSuccess(manifestRef, manifestStatus)
				}
			}

			updateClusterInstanceStatus(clusterInstance, manifestRef)
		}
	}

	return successfulExecution, conditions.PatchCIStatus(ctx, r.Client, clusterInstance, patch)
}

func getSortedSyncWaves(manifestGroups map[int][]interface{}) []int {
	syncWaves := make([]int, 0, len(manifestGroups))
	for syncWave := range manifestGroups {
		syncWaves = append(syncWaves, syncWave)
	}
	sort.Ints(syncWaves)
	return syncWaves
}

func setOwnerRefFunc(manifestNamespace string, clusterInstance *v1alpha1.ClusterInstance,
	obj metav1.Object, scheme *runtime.Scheme) controllerutil.MutateFn {
	return func() error {
		if manifestNamespace == clusterInstance.Namespace {
			return ctrl.SetControllerReference(clusterInstance, obj, scheme)
		}
		return nil
	}
}

func setManifestFailure(manifestRef *v1alpha1.ManifestReference, err error) {
	manifestRef.Status = v1alpha1.ManifestRenderedFailure
	manifestRef.Message = err.Error()
}

func setManifestSuccess(manifestRef *v1alpha1.ManifestReference, manifestStatus string) {
	manifestRef.Status = manifestStatus
	manifestRef.Message = ""
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
	clusterInstance *v1alpha1.ClusterInstance,
	manifestGroups map[int][]interface{}) (rendered bool, err error) {

	r.Log.Info(fmt.Sprintf("Validating rendered manifests for ClusterInstance %s", clusterInstance.Name))
	dryRunClient := client.NewDryRunClient(r.Client)
	patch := client.MergeFrom(clusterInstance.DeepCopy())
	rendered, err = r.executeRenderedManifests(ctx, dryRunClient, clusterInstance, manifestGroups,
		v1alpha1.ManifestRenderedValidated)
	if err != nil || !rendered {
		msg := fmt.Sprintf("failed to validate rendered manifests for ClusterInstance %s using dry-run validation",
			clusterInstance.Name)
		if err != nil {
			msg = fmt.Sprintf(", err: %v", err)
		}
		r.Log.Info(msg)

		conditions.SetStatusCondition(&clusterInstance.Status.Conditions,
			conditions.RenderedTemplatesValidated,
			conditions.Failed,
			metav1.ConditionFalse,
			"Rendered manifests failed dry-run validation")
	} else {
		conditions.SetStatusCondition(&clusterInstance.Status.Conditions,
			conditions.RenderedTemplatesValidated,
			conditions.Completed,
			metav1.ConditionTrue,
			"Rendered templates validation succeeded")
	}

	if updateErr := conditions.PatchCIStatus(ctx, r.Client, clusterInstance, patch); updateErr != nil {
		if err == nil {
			r.Log.Info(
				fmt.Sprintf("failed to update ClusterInstance %s status post validation of rendered templates, err: %s",
					clusterInstance.Name, updateErr.Error()))
			err = updateErr
		}
	}

	return rendered, err
}

func (r *ClusterInstanceReconciler) applyRenderedManifests(
	ctx context.Context,
	clusterInstance *v1alpha1.ClusterInstance,
	manifestGroups map[int][]interface{}) (rendered bool, err error) {

	r.Log.Info(fmt.Sprintf("Applying rendered manifests for ClusterInstance %s", clusterInstance.Name))
	patch := client.MergeFrom(clusterInstance.DeepCopy())
	if rendered, err = r.executeRenderedManifests(
		ctx,
		r.Client,
		clusterInstance,
		manifestGroups,
		v1alpha1.ManifestRenderedSuccess,
	); err != nil || !rendered {
		msg := fmt.Sprintf("failed to apply rendered manifests for ClusterInstance %s", clusterInstance.Name)
		if err != nil {
			msg = fmt.Sprintf(", err: %v", err)
		}
		r.Log.Info(msg)

		conditions.SetStatusCondition(&clusterInstance.Status.Conditions,
			conditions.RenderedTemplatesApplied,
			conditions.Failed,
			metav1.ConditionFalse,
			"Failed to apply site config manifests")
	} else {
		conditions.SetStatusCondition(&clusterInstance.Status.Conditions,
			conditions.RenderedTemplatesApplied,
			conditions.Completed,
			metav1.ConditionTrue,
			"Applied site config manifests")
	}

	if updateErr := conditions.PatchCIStatus(ctx, r.Client, clusterInstance, patch); updateErr != nil {
		if err == nil {
			r.Log.Info(
				fmt.Sprintf("Failed to update ClusterInstance %s status post creation of rendered templates, err: %s",
					clusterInstance.Name, updateErr.Error()))
			err = updateErr
		}
	}
	return
}

func (r *ClusterInstanceReconciler) handleRenderTemplates(
	ctx context.Context,
	clusterInstance *v1alpha1.ClusterInstance,
) (rendered bool, err error) {

	rendered = false

	var (
		unsortedManifests []interface{}
		manifestGroups    map[int][]interface{}
	)

	// Render templates manifests
	r.Log.Info(fmt.Sprintf("Rendering templates for ClusterInstance %s", clusterInstance.Name))
	unsortedManifests, err = r.renderManifests(ctx, clusterInstance)
	if err != nil {
		r.Log.Info(
			fmt.Sprintf("encountered error while rendering templates for ClusterInstance %s, err: %v",
				clusterInstance.Name, err))
		return
	}

	// Organize rendered manifests by sync-wave and sort groups by manifest type
	manifestGroups, err = groupAndSortManifests(unsortedManifests)
	if err != nil {
		r.Log.Info(
			fmt.Sprintf("encountered error while rendering templates for ClusterInstance %s, err: %v",
				clusterInstance.Name, err))
		return
	}

	// Validate rendered manifests using kubernetes dry-run
	if rendered, err = r.validateRenderedManifests(ctx, clusterInstance, manifestGroups); !rendered || err != nil {
		return
	}

	// Apply the rendered manifests
	rendered, err = r.applyRenderedManifests(ctx, clusterInstance, manifestGroups)

	return
}

func (r *ClusterInstanceReconciler) updateSuppressedManifestsStatus(
	ctx context.Context,
	clusterInstance *v1alpha1.ClusterInstance,
) error {

	patch := client.MergeFrom(clusterInstance.DeepCopy())

	suppressFn := func(suppressedManifests []string) {
		for _, kind := range suppressedManifests {
			for index, manifest := range clusterInstance.Status.ManifestsRendered {
				if manifest.Kind == kind {
					clusterInstance.Status.ManifestsRendered[index].Status = v1alpha1.ManifestSuppressed
					clusterInstance.Status.ManifestsRendered[index].Message = ""
				}
			}
		}
	}

	// Suppress cluster-level manifests
	suppressFn(clusterInstance.Spec.SuppressedManifests)

	// Suppress node-level manifests
	for _, node := range clusterInstance.Spec.Nodes {
		suppressFn(node.SuppressedManifests)
	}

	return conditions.PatchCIStatus(ctx, r.Client, clusterInstance, patch)
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
