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
	"sort"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	"github.com/sakhoury/siteconfig/internal/controller/conditions"
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

	"github.com/sakhoury/siteconfig/api/v1alpha1"
)

const siteConfigFinalizer = "metaclusterinstall.openshift.io/finalizer"

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
func (r *SiteConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	defer func() {
		r.Log.Info("Finish reconciling SiteConfig", "name", req.NamespacedName)
	}()

	r.Log.Info("Start reconciling SiteConfig", "name", req.NamespacedName)

	// Get the SiteConfig CR
	siteConfig := &v1alpha1.SiteConfig{}
	if err := r.Get(ctx, req.NamespacedName, siteConfig); err != nil {
		if errors.IsNotFound(err) {
			r.Log.Info("SiteConfig not found", "name", req.NamespacedName)
			return doNotRequeue(), nil
		}
		r.Log.Error(err, "Failed to get SiteConfig")
		// This is likely a case where the API is down, so requeue and try again shortly
		return requeueWithError(err)
	}

	r.Log.Info("Loaded SiteConfig", "name", req.NamespacedName, "version", siteConfig.GetResourceVersion())

	if res, stop, err := r.handleFinalizer(ctx, siteConfig); !res.IsZero() || stop || err != nil {
		if err != nil {
			r.Log.Error(err, "Encountered error while handling finalizer", "SiteConfig", req.NamespacedName)
		}
		return res, err
	}

	// Validate SiteConfig
	if err := r.handleValidate(ctx, siteConfig); err != nil {
		return requeueWithError(err)
	}

	// Render, validate and apply templates
	if rendered, err := r.handleRenderTemplates(ctx, siteConfig); err != nil {
		return requeueWithError(err)
	} else if rendered {
		r.Log.Info("SiteConfig templates are rendered", "name", req.NamespacedName)
	} else {
		r.Log.Info("Failed to render templates for SiteConfig", "name", req.NamespacedName)
	}

	// Update manifests' status that have been flagged for suppression
	if err := r.updateSuppressedManifestsStatus(ctx, siteConfig); err != nil {
		return requeueWithError(err)
	}

	return doNotRequeue(), nil
}

func (r *SiteConfigReconciler) finalizeSiteConfig(ctx context.Context, siteConfig *v1alpha1.SiteConfig) error {

	// Group the manifests by the sync-wave
	// This is so that the manifests can be deleted in descending order of sync-wave
	manifestGroups := map[int][]v1alpha1.ManifestReference{}
	for _, manifest := range siteConfig.Status.ManifestsRendered {
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
		r.Log.Info("Removing SiteConfig finalizer", "name", siteConfig.Name)
		patch := client.MergeFrom(siteConfig.DeepCopy())
		if controllerutil.RemoveFinalizer(siteConfig, siteConfigFinalizer) {
			return ctrl.Result{}, true, r.Patch(ctx, siteConfig, patch)
		}
	}
	return ctrl.Result{}, false, nil
}

func (r *SiteConfigReconciler) handleValidate(ctx context.Context, siteConfig *v1alpha1.SiteConfig) error {

	patch := client.MergeFrom(siteConfig.DeepCopy())

	newCond := metav1.Condition{Type: string(conditions.SiteConfigValidated)}
	r.Log.Info("Starting validation", "SiteConfig", siteConfig.Name)
	err := validateSiteConfig(ctx, r.Client, siteConfig)
	if err != nil {
		r.Log.Error(err, "SiteConfig validation failed due to error", "SiteConfig", siteConfig.Name)

		newCond.Reason = string(conditions.Failed)
		newCond.Status = metav1.ConditionFalse
		newCond.Message = fmt.Sprintf("Validation failed: %s", err.Error())

	} else {
		r.Log.Info("Validation succeeded", "SiteConfig", siteConfig.Name)

		newCond.Reason = string(conditions.Completed)
		newCond.Status = metav1.ConditionTrue
		newCond.Message = "Validation succeeded"
	}
	r.Log.Info("Finished validation", "SiteConfig", siteConfig.Name)

	conditions.SetStatusCondition(&siteConfig.Status.Conditions, conditions.ConditionType(newCond.Type), conditions.ConditionReason(newCond.Reason), newCond.Status, newCond.Message)

	if updateErr := conditions.PatchStatus(ctx, r.Client, siteConfig, patch); updateErr != nil {
		if err == nil {
			r.Log.Info(fmt.Sprintf("Failed to update SiteConfig %s status after validating SiteConfig, err: %s", siteConfig.Name, updateErr.Error()))
			err = updateErr
		}
	}

	return err
}

func (r *SiteConfigReconciler) renderManifests(ctx context.Context, siteConfig *v1alpha1.SiteConfig) ([]interface{}, error) {
	r.Log.Info(fmt.Sprintf("Rendering templates for SiteConfig %s", siteConfig.Name))

	patch := client.MergeFrom(siteConfig.DeepCopy())
	renderedManifests, err := r.ScBuilder.ProcessTemplates(ctx, r.Client, *siteConfig)
	if err != nil {
		r.Log.Error(err, "Failed to render manifests", "SiteConfig", siteConfig.Name)
		conditions.SetStatusCondition(&siteConfig.Status.Conditions,
			conditions.RenderedTemplates,
			conditions.Failed,
			metav1.ConditionFalse,
			fmt.Sprintf("Failed to render templates, err= %s", err))
	} else {
		conditions.SetStatusCondition(&siteConfig.Status.Conditions,
			conditions.RenderedTemplates,
			conditions.Completed,
			metav1.ConditionTrue,
			"Rendered templates successfully")
	}

	if updateErr := conditions.PatchStatus(ctx, r.Client, siteConfig, patch); updateErr != nil {
		if err == nil {
			r.Log.Info(fmt.Sprintf("Failed to update SiteConfig %s status after rendering templates, err: %s", siteConfig.Name, updateErr.Error()))
			err = updateErr
		}
	}

	return renderedManifests, err
}

// groupAndSortManifests categorizes manifests by the sync-wave and then
// sorts the groups alphabetically by manifest kind
func groupAndSortManifests(manifests []interface{}) (map[int][]interface{}, error) {
	var syncWaveStr string
	manifestGroups := make(map[int][]interface{}, len(manifests))
	for _, object := range manifests {
		manifest, ok := object.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("manifest should be of type 'map[string]interface{}'")
		}

		kind, ok := manifest["kind"].(string)
		if !ok {
			return nil, fmt.Errorf("missing field `kind` from rendered manifest")
		}

		syncWaveStr = DefaultWaveAnnotation
		if metadata, ok := manifest["metadata"].(map[string]interface{}); ok {
			if annotations, ok := metadata["annotations"].(map[string]interface{}); ok {
				if syncWaveStr, ok = annotations[WaveAnnotation].(string); !ok {
					syncWaveStr = DefaultWaveAnnotation
				}
			}
		}

		if syncWave, err := strconv.Atoi(syncWaveStr); err != nil {
			return nil, fmt.Errorf("failed to extract annotation %s in resource %s", WaveAnnotation, kind)
		} else {
			// check if the key exists in the map
			if _, ok := manifestGroups[syncWave]; !ok {
				// if key doesn't exist, initialize the slice
				manifestGroups[syncWave] = make([]interface{}, 0)
			}
			// append the value to the slice associated with the key
			manifestGroups[syncWave] = append(manifestGroups[syncWave], object)
		}
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

func createOrPatch(ctx context.Context, c client.Client, obj unstructured.Unstructured, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
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

func (r *SiteConfigReconciler) executeRenderedManifests(ctx context.Context, c client.Client, siteConfig *v1alpha1.SiteConfig, manifestGroups map[int][]interface{}, manifestStatus string) (bool, error) {

	successfulExecution := true
	patch := client.MergeFrom(siteConfig.DeepCopy())
	// Get the syncWaves of the map
	syncWaves := make([]int, 0, len(manifestGroups))
	for syncWave := range manifestGroups {
		syncWaves = append(syncWaves, syncWave)
	}
	// Sort the syncWaves in ascending order
	sort.Ints(syncWaves)

	for _, syncWave := range syncWaves {
		group := manifestGroups[syncWave]
		for _, item := range group {
			manifest := item.(map[string]interface{})
			metadata, _ := manifest["metadata"].(map[string]interface{})
			name := metadata["name"].(string)
			apiVersion := manifest["apiVersion"].(string)
			kind := manifest["kind"].(string)

			manifestRef := &v1alpha1.ManifestReference{Name: name, Kind: kind, APIGroup: &apiVersion, SyncWave: syncWave, LastAppliedTime: metav1.NewTime(time.Now())}

			namespace, namespaceOk := metadata["namespace"].(string)
			if namespaceOk {
				manifestRef.Namespace = namespace
			}

			if obj, err := toUnstructured(item); err != nil {
				successfulExecution = false
				manifestRef.Status = v1alpha1.ManifestRenderedFailure
				manifestRef.Message = err.Error()
			} else {
				setOwnerRef := func() error {
					if namespaceOk && namespace == siteConfig.Namespace {
						return ctrl.SetControllerReference(siteConfig, &obj, r.Scheme)
					}
					return nil
				}

				if result, err := createOrPatch(ctx, c, obj, setOwnerRef); err != nil {
					successfulExecution = false
					manifestRef.Status = v1alpha1.ManifestRenderedFailure
					manifestRef.Message = err.Error()
				} else if result != controllerutil.OperationResultNone {
					manifestRef.Status = manifestStatus
					manifestRef.Message = ""
				}
			}

			if found := findManifestRendered(manifestRef, siteConfig.Status.ManifestsRendered); found != nil {
				if found.Status != manifestRef.Status || found.Message != manifestRef.Message {
					found.LastAppliedTime = manifestRef.LastAppliedTime
					found.Status = manifestRef.Status
					found.Message = manifestRef.Message
				}
			} else {
				siteConfig.Status.ManifestsRendered = append(siteConfig.Status.ManifestsRendered, *manifestRef)
			}
		}
	}

	return successfulExecution, conditions.PatchStatus(ctx, r.Client, siteConfig, patch)
}

func findManifestRendered(manifest *v1alpha1.ManifestReference, manifestList []v1alpha1.ManifestReference) *v1alpha1.ManifestReference {
	for index, m := range manifestList {
		if *manifest.APIGroup == *m.APIGroup && manifest.Kind == m.Kind && manifest.Name == m.Name {
			return &manifestList[index]
		}
	}
	return nil
}

func (r *SiteConfigReconciler) handleRenderTemplates(ctx context.Context, siteConfig *v1alpha1.SiteConfig) (rendered bool, err error) {
	rendered = false

	var (
		unsortedManifests []interface{}
		manifestGroups    map[int][]interface{}
	)

	// Render templates manifests
	r.Log.Info(fmt.Sprintf("Rendering templates for SiteConfig %s", siteConfig.Name))
	unsortedManifests, err = r.renderManifests(ctx, siteConfig)
	if err != nil {
		r.Log.Info(fmt.Sprintf("encountered error while rendering templates for SiteConfig %s, err: %v", siteConfig.Name, err))
		return
	}

	// Organize rendered manifests by sync-wave and sort groups by manifest type
	manifestGroups, err = groupAndSortManifests(unsortedManifests)
	if err != nil {
		r.Log.Info(fmt.Sprintf("encountered error while rendering templates for SiteConfig %s, err: %v", siteConfig.Name, err))
		return
	}

	// Validate rendered manifests using kubernetes dry-run
	r.Log.Info(fmt.Sprintf("Validating rendered manifests for SiteConfig %s", siteConfig.Name))
	dryRunClient := client.NewDryRunClient(r.Client)
	patch := client.MergeFrom(siteConfig.DeepCopy())
	rendered, err = r.executeRenderedManifests(ctx, dryRunClient, siteConfig, manifestGroups, v1alpha1.ManifestRenderedValidated)
	if err != nil || !rendered {
		msg := fmt.Sprintf("failed to validate rendered manifests for SiteConfig %s using dry-run validation", siteConfig.Name)
		if err != nil {
			msg = fmt.Sprintf(", err: %v", err)
		}
		r.Log.Info(msg)

		conditions.SetStatusCondition(&siteConfig.Status.Conditions,
			conditions.RenderedTemplatesValidated,
			conditions.Failed,
			metav1.ConditionFalse,
			"Rendered manifests failed dry-run validation")
	} else {
		conditions.SetStatusCondition(&siteConfig.Status.Conditions,
			conditions.RenderedTemplatesValidated,
			conditions.Completed,
			metav1.ConditionTrue,
			"Rendered templates validation succeeded")
	}

	if updateErr := conditions.PatchStatus(ctx, r.Client, siteConfig, patch); updateErr != nil {
		if err == nil {
			r.Log.Info(fmt.Sprintf("failed to update SiteConfig %s status post validation of rendered templates, err: %s", siteConfig.Name, updateErr.Error()))
			err = updateErr
		}
	}
	if !rendered || err != nil {
		return
	}

	// Apply the rendered manifests
	r.Log.Info(fmt.Sprintf("Applying rendered manifests for SiteConfig %s", siteConfig.Name))
	patch = client.MergeFrom(siteConfig.DeepCopy())
	rendered, err = r.executeRenderedManifests(ctx, r.Client, siteConfig, manifestGroups, v1alpha1.ManifestRenderedSuccess)
	if err != nil || !rendered {
		msg := fmt.Sprintf("failed to apply rendered manifests for SiteConfig %s", siteConfig.Name)
		if err != nil {
			msg = fmt.Sprintf(", err: %v", err)
		}
		r.Log.Info(msg)

		conditions.SetStatusCondition(&siteConfig.Status.Conditions,
			conditions.RenderedTemplatesApplied,
			conditions.Failed,
			metav1.ConditionFalse,
			"Failed to apply site config manifests")
	} else {
		conditions.SetStatusCondition(&siteConfig.Status.Conditions,
			conditions.RenderedTemplatesApplied,
			conditions.Completed,
			metav1.ConditionTrue,
			"Applied site config manifests")
	}

	if updateErr := conditions.PatchStatus(ctx, r.Client, siteConfig, patch); updateErr != nil {
		if err == nil {
			r.Log.Info(fmt.Sprintf("Failed to update SiteConfig %s status post creation of rendered templates, err: %s", siteConfig.Name, updateErr.Error()))
			err = updateErr
		}
	}
	return
}

func (r *SiteConfigReconciler) updateSuppressedManifestsStatus(ctx context.Context, siteConfig *v1alpha1.SiteConfig) error {
	patch := client.MergeFrom(siteConfig.DeepCopy())

	suppressFn := func(suppressedManifests []string) {
		for _, kind := range suppressedManifests {
			for index, manifest := range siteConfig.Status.ManifestsRendered {
				if manifest.Kind == kind {
					siteConfig.Status.ManifestsRendered[index].Status = v1alpha1.ManifestSuppressed
					siteConfig.Status.ManifestsRendered[index].Message = ""
				}
			}
		}
	}

	// Suppress cluster-level manifests
	suppressFn(siteConfig.Spec.SuppressedManifests)

	// Suppress node-level manifests
	for _, node := range siteConfig.Spec.Nodes {
		suppressFn(node.SuppressedManifests)
	}

	return conditions.PatchStatus(ctx, r.Client, siteConfig, patch)
}

// SetupWithManager sets up the controller with the Manager.
func (r *SiteConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("SiteConfig")

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.SiteConfig{}).
		WithEventFilter(predicate.Or(predicate.GenerationChangedPredicate{}, predicate.LabelChangedPredicate{})).
		WithOptions(controller.Options{MaxConcurrentReconciles: 1}).
		Complete(r)
}
