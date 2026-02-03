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
	"os"
	"time"

	"go.uber.org/zap"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
	"github.com/stolostron/siteconfig/internal/controller/conditions"
	"github.com/stolostron/siteconfig/internal/controller/configuration"
	"github.com/stolostron/siteconfig/internal/controller/deletion"
	cierrors "github.com/stolostron/siteconfig/internal/controller/errors"
	"github.com/stolostron/siteconfig/internal/controller/reinstall"
	ai_templates "github.com/stolostron/siteconfig/internal/templates/assisted-installer"
	ibi_templates "github.com/stolostron/siteconfig/internal/templates/image-based-installer"
)

const clusterInstanceFinalizer = "clusterinstance." + v1alpha1.Group + "/finalizer"

// ClusterInstanceFieldManager is the field manager name used for Server-Side Apply operations
const ClusterInstanceFieldManager = "siteconfig-controller"

// Disaster recovery constants
const (
	acmBackupLabel      = "cluster.open-cluster-management.io/backup"
	acmBackupLabelValue = ""
)

// Default Requeue delays
const (
	DefaultDeletionRequeueDelay        = 1 * time.Minute
	DefaultValidationErrorDelay        = 30 * time.Second
	DefaultTemplateRenderingErrorDelay = 30 * time.Second
	DefaultReimportErrorDelay          = 30 * time.Second
)

// ClusterInstanceReconciler reconciles a ClusterInstance object
type ClusterInstanceReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	Recorder         record.EventRecorder
	Log              *zap.Logger
	TmplEngine       *ci.TemplateEngine
	ConfigStore      *configuration.ConfigurationStore
	DeletionHandler  *deletion.DeletionHandler
	ReinstallHandler *reinstall.ReinstallHandler
}

// doNotRequeue returns a ctrl.Result indicating that no further reconciliation is required.
// Use this when the reconciliation loop has completed successfully.
func doNotRequeue() ctrl.Result {
	return ctrl.Result{Requeue: false}
}

// requeueWithDelay returns a ctrl.Result that requeues the request after a specified delay.
// Use this when reconciliation should be retried after a non-immediate condition is resolved.
func requeueWithDelay(delay time.Duration) ctrl.Result {
	return ctrl.Result{Requeue: true, RequeueAfter: delay}
}

// requeueWithError returns a ctrl.Result and error, indicating that reconciliation cannot proceed due to an
// unrecoverable error.
// This result signals the controller-runtime manager to log the error and potentially retry depending on its settings.
func requeueWithError(err error) (ctrl.Result, error) {
	return ctrl.Result{}, err
}

// requeueWithErrorAfterDelay returns a ctrl.Result and error, requeueing the request after a specified delay.
// Use this when reconciliation encounters a recoverable error but requires a delay before retrying.
func requeueWithErrorAfterDelay(err error, delay time.Duration) (ctrl.Result, error) {
	return requeueWithDelay(delay), err
}

// requeueForDeletion returns a ctrl.Result to requeue the request after the default deletion requeue delay.
// This is used when the reconciliation loop is waiting for resource deletion processes to complete.
func requeueForDeletion() ctrl.Result {
	return requeueWithDelay(DefaultDeletionRequeueDelay)
}

//nolint:lll
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
//+kubebuilder:rbac:groups=hypershift.openshift.io,resources=hostedclusters,verbs=get;create;update;patch;delete
//+kubebuilder:rbac:groups=hypershift.openshift.io,resources=nodepools,verbs=get;create;update;patch;delete
//+kubebuilder:rbac:groups=work.open-cluster-management.io,resources=manifestworks,verbs=get;list;watch

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
		if apierrors.IsNotFound(err) {
			log.Error("ClusterInstance not found")
			return doNotRequeue(), nil
		}
		log.Error("Failed to get ClusterInstance", zap.Error(err))
		// This is likely a case where the API is down, so requeue and try again shortly
		return requeueWithError(err)
	}

	// Update logger with resource version
	log = r.Log.With(
		zap.String("name", req.Name),
		zap.String("namespace", req.Namespace),
		zap.String("version", clusterInstance.GetResourceVersion()),
	)

	log.Info("Loaded ClusterInstance")

	// Check if paused annotation is set
	if clusterInstance.IsPaused() {
		msg := fmt.Sprintf("Paused annotation '%s' is set, this requires a manual resolution. "+
			"Please resolve the error and remove the annotation to continue reconciling the ClusterInstance CR",
			v1alpha1.PausedAnnotation)
		log.Warn(msg)
		return ctrl.Result{}, nil
	}

	// Ensure ClusterInstance.Status.Paused is nil:
	// - cleanup of Paused status when the Paused annotation is cleared.
	if clusterInstance.Status.Paused != nil {
		patch := client.MergeFrom(clusterInstance.DeepCopy())
		clusterInstance.Status.Paused = nil
		if err := r.Client.Status().Patch(ctx, clusterInstance, patch); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to clear Paused status, error: %w", err)
		}
		return ctrl.Result{Requeue: true}, nil
	}

	if res, err := r.handleFinalizer(ctx, log, clusterInstance); !res.IsZero() || err != nil {
		if err != nil {
			log.Error("Encountered error while handling finalizer", zap.Error(err))
		}
		return res, err
	}

	// Check if ManagedCluster requires reimport (only during reinstalls)
	if reinstallStatus := clusterInstance.Status.Reinstall; reinstallStatus != nil {
		result, reimportNeeded, err := r.ReinstallHandler.EnsureClusterIsReimported(ctx, log, clusterInstance)
		if err != nil {
			return requeueWithErrorAfterDelay(err, DefaultReimportErrorDelay)
		}
		if reimportNeeded {
			log.Info("Cluster reimport still in progress, requeuing")
			return result, nil
		}
	}

	// Pre-empt the reconcile-loop when the ObservedGeneration is the same as the Generation
	if clusterInstance.Status.ObservedGeneration == clusterInstance.Generation {
		log.Info("ObservedGeneration and ObjectMeta.Generation are the same, pre-empting reconcile")
		return doNotRequeue(), nil
	}

	// Apply the ACM disaster recovery backup labels to the install template ConfigMaps
	if err := r.applyACMBackupLabelToInstallTemplates(ctx, log, clusterInstance); err != nil {
		return requeueWithError(err)
	}

	// Check if reinstall is triggered
	if reinstallSpec := clusterInstance.Spec.Reinstall; reinstallSpec != nil {
		reinstallStatus := clusterInstance.Status.Reinstall
		if reinstallStatus == nil || reinstallStatus.ObservedGeneration != reinstallSpec.Generation {

			res, err := r.ReinstallHandler.ProcessRequest(ctx, clusterInstance)
			if err != nil {
				log.Error("Failed to process reinstall request", zap.Error(err))
				return res, fmt.Errorf("encountered an error while processing reinstall request, err: %w", err)
			}
			if !res.IsZero() {
				return res, nil
			}
		}
	}

	// Validate ClusterInstance
	if err := r.handleValidate(ctx, log, clusterInstance); err != nil {
		return requeueWithErrorAfterDelay(err, DefaultValidationErrorDelay)
	}

	// Render, validate and apply templates
	if ok, err := r.handleRenderTemplates(ctx, log, clusterInstance); err != nil {
		// Encountered an error, requeue the request
		return requeueWithErrorAfterDelay(err, DefaultTemplateRenderingErrorDelay)
	} else if !ok {
		// no error, however requeue required (e.g. wait for manifests to be pruned)
		log.Info("Could not complete rendering templates, will try again",
			zap.String("after", DefaultDeletionRequeueDelay.String()))
		return requeueForDeletion(), nil
	}
	log.Info("Finished rendering templates")

	// Only update the ObservedGeneration when all the above processes have been successfully executed
	if clusterInstance.Status.ObservedGeneration != clusterInstance.Generation {
		if err := r.updateObservedStatus(ctx, log, clusterInstance); err != nil {
			return ctrl.Result{}, fmt.Errorf("encountered an error updating observed ClusterInstance status: %w", err)
		}
	}

	return doNotRequeue(), nil
}

func (r *ClusterInstanceReconciler) updateObservedStatus(
	ctx context.Context,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
) error {
	// Capture and compare snapshot of ClusterInstance.Spec
	lastSpecJSON, exists := clusterInstance.Annotations[v1alpha1.LastClusterInstanceSpecAnnotation]
	lastAppliedSpec := &v1alpha1.ClusterInstanceSpec{}
	if exists {
		if err := json.Unmarshal([]byte(lastSpecJSON), lastAppliedSpec); err != nil {
			return fmt.Errorf("failed to unmarshal last-applied Spec: %w", err)
		}
	}

	if !exists || !equality.Semantic.DeepEqual(clusterInstance.Spec, lastAppliedSpec) {
		currentSpecJSON, err := json.Marshal(clusterInstance.Spec)
		if err != nil {
			return fmt.Errorf("failed to marshal current ClusterInstance spec: %w", err)
		}

		log.Sugar().Infof("Updating  %s annotation to %v", v1alpha1.LastClusterInstanceSpecAnnotation,
			string(currentSpecJSON))
		patch := client.MergeFrom(clusterInstance.DeepCopy())
		metav1.SetMetaDataAnnotation(&clusterInstance.ObjectMeta, v1alpha1.LastClusterInstanceSpecAnnotation,
			string(currentSpecJSON))
		if err := r.Patch(ctx, clusterInstance, patch); err != nil {
			return fmt.Errorf("failed to update %s annotation: %w",
				v1alpha1.LastClusterInstanceSpecAnnotation, err)
		}

		// Re-fetch updated ClusterInstance
		if err := r.Get(ctx, client.ObjectKeyFromObject(clusterInstance), clusterInstance); err != nil {
			log.Error("Failed to get ClusterInstance", zap.Error(err))
			return fmt.Errorf("failed to re-fetch ClusterInstance: %w", err)
		}
	}

	log.Sugar().Infof("Updating ObservedGeneration to %d", clusterInstance.Generation)
	patch := client.MergeFrom(clusterInstance.DeepCopy())
	clusterInstance.Status.ObservedGeneration = clusterInstance.Generation

	if err := conditions.PatchCIStatus(ctx, r.Client, clusterInstance, patch); err != nil {
		return fmt.Errorf("failed to patch ClusterInstance status for ObservedGeneration update to %d: %w",
			clusterInstance.Generation, err)
	}

	return nil
}

func (r *ClusterInstanceReconciler) applyACMBackupLabelToInstallTemplates(
	ctx context.Context,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
) error {

	log = log.Named("applyACMBackupLabelToInstallTemplates")

	// Only apply the ACM disaster recovery backup label to install templates other than those provided
	// by the SiteConfig Operator
	siteConfigNS := os.Getenv("POD_NAMESPACE")
	if siteConfigNS == "" {
		log.Warn("Could not determine the SiteConfig Operator Namespace")
	}

	defaultInstallTemplates := []string{
		ai_templates.ClusterLevelInstallTemplates, ai_templates.NodeLevelInstallTemplates,
		ibi_templates.ClusterLevelInstallTemplates, ibi_templates.NodeLevelInstallTemplates,
	}

	applyDRLabelFn := func(ref v1alpha1.TemplateRef) error {

		// check if the install template reference is one of the default provided templates
		if ref.Namespace == siteConfigNS {
			for _, defaultTempl := range defaultInstallTemplates {
				if ref.Name == defaultTempl {
					// ignore install template
					return nil
				}
			}
		}

		cm := &corev1.ConfigMap{}
		if err := r.Get(ctx, types.NamespacedName{Namespace: ref.Namespace, Name: ref.Name}, cm); err != nil {
			return fmt.Errorf("failed to get ConfigMap %s/%s: %w", ref.Namespace, ref.Name, err)
		}

		labels := cm.GetLabels()
		if labels == nil {
			labels = make(map[string]string)
		} else if labels[acmBackupLabel] == acmBackupLabelValue {
			// nothing to do
			return nil
		}

		patch := client.MergeFrom(cm.DeepCopy())
		labels[acmBackupLabel] = acmBackupLabelValue
		cm.SetLabels(labels)

		if err := r.Patch(ctx, cm, patch); err != nil {
			return fmt.Errorf("failed to patch ConfigMap %s/%s: %w", cm.GetNamespace(), cm.GetName(), err)
		}

		return nil
	}

	applyDRLabelToTemplatesFn := func(refs []v1alpha1.TemplateRef) error {
		for _, ref := range refs {
			if err := applyDRLabelFn(ref); err != nil {
				log.Sugar().Errorf(
					"Failed to apply disaster recovery label to install template ConfigMap %s/%s, err: %v",
					ref.Namespace, ref.Name, err,
				)
				return err
			}
		}
		return nil
	}

	// Process cluster-level install templates
	if err := applyDRLabelToTemplatesFn(clusterInstance.Spec.TemplateRefs); err != nil {
		return err
	}

	// Process node-level install templates
	for _, node := range clusterInstance.Spec.Nodes {
		if err := applyDRLabelToTemplatesFn(node.TemplateRefs); err != nil {
			return err
		}
	}

	return nil
}

// handleFinalizer ensures proper finalizer management for the ClusterInstance resource.
// - Adds the finalizer if the object is not marked for deletion.
// - Executes cleanup logic and removes the finalizer when the object is marked for deletion.
// Returns a ctrl.Result to indicate whether the reconciler should requeue and an error if any operation fails.
func (r *ClusterInstanceReconciler) handleFinalizer(
	ctx context.Context,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
) (ctrl.Result, error) {
	log = log.Named("handleFinalizer")

	// Check if ClusterInstance is not being deleted
	if clusterInstance.DeletionTimestamp.IsZero() {
		return r.ensureFinalizer(ctx, log, clusterInstance)
	}

	// ClusterInstance is being deleted
	if !controllerutil.ContainsFinalizer(clusterInstance, clusterInstanceFinalizer) {
		// Finalizer already removed; no action needed
		return ctrl.Result{}, nil
	}

	log.Info("Running finalization logic for ClusterInstance")

	// Perform cleanup logic
	deletionCompleted, err := r.DeletionHandler.DeleteRenderedObjects(
		ctx, clusterInstance, nil, ptr.To(deletion.DefaultDeletionTimeout))

	if err != nil {
		if cierrors.IsDeletionTimeoutError(err) {
			log.Warn("Finalization timed out; pausing reconciliation")
			// Add paused annotation for manual intervention
			if err1 := ci.ApplyPause(ctx, r.Client, log, clusterInstance, err.Error()); err1 != nil {
				err = fmt.Errorf("encountered an error pausing ClusterInstance, error: %w", err1)
			}
			return ctrl.Result{}, err
		}
		log.Error("Finalization encountered an error", zap.Error(err))
		return ctrl.Result{}, fmt.Errorf("finalization encountered an error: %w", err)
	}

	if !deletionCompleted {
		log.Info("Waiting for rendered manifests to be deleted")
		return requeueForDeletion(), nil
	}

	// Finalization complete; remove the finalizer
	patch := client.MergeFrom(clusterInstance.DeepCopy())
	controllerutil.RemoveFinalizer(clusterInstance, clusterInstanceFinalizer)

	if err := r.Patch(ctx, clusterInstance, patch); err != nil {
		log.Error("Failed to remove finalizer", zap.Error(err))
		return ctrl.Result{}, fmt.Errorf("failed to remove finalizer for ClusterInstance %s/%s: %w",
			clusterInstance.Namespace, clusterInstance.Name, err)
	}

	log.Info("Finalizer removed successfully")
	return ctrl.Result{}, nil
}

// ensureFinalizer ensures the ClusterInstance has the required finalizer and requeues if it was added.
func (r *ClusterInstanceReconciler) ensureFinalizer(
	ctx context.Context,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
) (ctrl.Result, error) {
	if controllerutil.ContainsFinalizer(clusterInstance, clusterInstanceFinalizer) {
		// Finalizer already present; no action needed
		return ctrl.Result{}, nil
	}

	patch := client.MergeFrom(clusterInstance.DeepCopy())
	controllerutil.AddFinalizer(clusterInstance, clusterInstanceFinalizer)

	// Persist the finalizer addition
	if err := r.Patch(ctx, clusterInstance, patch); err != nil {
		log.Error("Failed to add finalizer", zap.Error(err))
		return ctrl.Result{}, fmt.Errorf("failed to add finalizer to ClusterInstance %s/%s: %w",
			clusterInstance.Namespace, clusterInstance.Name, err)
	}

	log.Info("Finalizer added successfully; requeuing reconciliation")
	return ctrl.Result{Requeue: true}, nil
}

func (r *ClusterInstanceReconciler) handleValidate(
	ctx context.Context,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
) error {

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

	patch := client.MergeFrom(clusterInstance.DeepCopy())
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

	renderedObjects, err := r.TmplEngine.ProcessTemplates(ctx, r.Client, log.Named("TemplateEngine"), *clusterInstance)

	conditionReason := v1alpha1.Completed
	conditionStatus := metav1.ConditionTrue
	message := "Rendered templates successfully"
	if err != nil {
		log.Error("Failed to render templates", zap.Error(err))
		conditionReason = v1alpha1.Failed
		conditionStatus = metav1.ConditionFalse
		message = fmt.Sprintf("Failed to render templates, err= %s", err)
	} else {
		log.Info("Successfully rendered templates")
	}

	patch := client.MergeFrom(clusterInstance.DeepCopy())
	conditions.SetStatusCondition(&clusterInstance.Status.Conditions,
		v1alpha1.RenderedTemplates,
		conditionReason, conditionStatus, message)

	if updateErr := conditions.PatchCIStatus(ctx, r.Client, clusterInstance, patch); updateErr != nil {
		if err == nil {
			log.Sugar().Errorf("Failed to update ClusterInstance status after rendering templates, err: %v", updateErr)
			err = updateErr
		}
	}

	log.Info("Finished rendering templates")
	return renderedObjects, err
}

// applyObject uses Server-Side Apply to create or update objects with field management.
// This approach leverages Kubernetes' managedFields to track field ownership and enables automatic
// deletion of fields that are no longer present in the applied configuration.
func applyObject(
	ctx context.Context,
	c client.Client,
	log *zap.Logger,
	renderedObj unstructured.Unstructured,
) (controllerutil.OperationResult, error) {

	// Update logger with object context.
	log = log.Named("applyObject").With(
		zap.String("name", renderedObj.GetName()),
		zap.String("namespace", renderedObj.GetNamespace()),
		zap.String("kind", renderedObj.GetKind()),
	)

	// Create a safe copy of the rendered object for Server-Side Apply
	// The rendered object already contains the correct ExtraLabels/ExtraAnnotations from template rendering
	preparedObj := &unstructured.Unstructured{}
	preparedObj.SetGroupVersionKind(renderedObj.GroupVersionKind())
	preparedObj.SetName(renderedObj.GetName())
	preparedObj.SetNamespace(renderedObj.GetNamespace())

	// Perform a safe shallow copy of the object content
	preparedObj.Object = make(map[string]interface{})
	for k, v := range renderedObj.Object {
		preparedObj.Object[k] = v
	}

	// Apply using Server-Side Apply with field manager
	log.Debug("Applying object using Server-Side Apply")
	if err := c.Patch(ctx, preparedObj, client.Apply,
		client.FieldOwner(ClusterInstanceFieldManager), client.ForceOwnership); err != nil {
		return controllerutil.OperationResultNone, fmt.Errorf("failed to apply object using Server-Side Apply: %w", err)
	}

	// SSA handles create/update automatically, so we consider this an update operation
	// (SSA is idempotent and doesn't distinguish between create/update at the API level)
	log.Debug("Object applied using Server-Side Apply")
	return controllerutil.OperationResultUpdated, nil
}

// executeRenderedManifests applies objects and returns updated ManifestsRendered status
func (r *ClusterInstanceReconciler) executeRenderedManifests(
	ctx context.Context,
	c client.Client,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
	objects []ci.RenderedObject,
	manifestStatus string) (manifestsRendered []v1alpha1.ManifestReference, ok bool, err error) {

	ok = true
	manifestsRendered = append([]v1alpha1.ManifestReference{}, clusterInstance.Status.ManifestsRendered...)

	var errs []error
	for _, object := range objects {
		manifestRef := object.ManifestReference()

		status := v1alpha1.ManifestRenderedFailure
		message := ""
		if result, applyErr := applyObject(ctx, c, log, object.GetObject()); applyErr != nil {
			errs = append(errs, applyErr)
			ok = false
			message = applyErr.Error()
		} else if result != controllerutil.OperationResultNone {
			status = manifestStatus
		}
		manifestRef.UpdateStatus(status, message)

		// Update in manifestsRendered slice
		if index, findErr := v1alpha1.IndexOfManifestByIdentity(manifestRef, manifestsRendered); findErr != nil {
			manifestsRendered = append(manifestsRendered, *manifestRef)
		} else {
			manifestsRendered[index].UpdateStatus(manifestRef.Status, manifestRef.Message)
		}
	}

	return manifestsRendered, ok, utilerrors.NewAggregate(errs)

}

func (r *ClusterInstanceReconciler) validateRenderedManifests(
	ctx context.Context,
	pLog *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
	objects []ci.RenderedObject) (rendered bool, err error) {

	log := pLog.Named("validateRenderedManifests")
	log.Info("Executing a dry-run validation on the rendered manifests")

	dryRunClient := client.NewDryRunClient(r.Client)

	manifestsRendered, rendered, err := r.executeRenderedManifests(ctx, dryRunClient, log,
		clusterInstance, objects, v1alpha1.ManifestRenderedValidated)

	conditionReason := v1alpha1.Completed
	conditionStatus := metav1.ConditionTrue
	message := "Rendered templates validation succeeded"
	if err != nil || !rendered {
		conditionReason = v1alpha1.Failed
		conditionStatus = metav1.ConditionFalse
		message = "Rendered manifests failed dry-run validation"
		if err != nil {
			message = fmt.Sprintf("%s, err: %v", message, err)
		}
		log.Info(message)
	}

	patch := client.MergeFrom(clusterInstance.DeepCopy())

	// Update in-memory status with both ManifestsRendered and Conditions
	clusterInstance.Status.ManifestsRendered = manifestsRendered
	conditions.SetStatusCondition(&clusterInstance.Status.Conditions,
		v1alpha1.RenderedTemplatesValidated,
		conditionReason, conditionStatus, message)

	if updateErr := conditions.PatchCIStatus(ctx, r.Client, clusterInstance, patch); updateErr != nil {
		if err == nil {
			log.Error("failed to update ClusterInstance status after validation",
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

	manifestsRendered, rendered, err := r.executeRenderedManifests(ctx, r.Client,
		log, clusterInstance, objects, v1alpha1.ManifestRenderedSuccess)

	conditionReason := v1alpha1.Completed
	conditionStatus := metav1.ConditionTrue
	message := "Applied site config manifests"
	if err != nil || !rendered {
		msg := "failed to apply rendered manifests"
		if err != nil {
			msg = fmt.Sprintf(", err: %v", err)
		}
		log.Info(msg)
		conditionReason = v1alpha1.Failed
		conditionStatus = metav1.ConditionFalse
		message = "Failed to apply site config manifests"
	}

	patch := client.MergeFrom(clusterInstance.DeepCopy())

	// Update in-memory status with both ManifestsRendered and Conditions
	clusterInstance.Status.ManifestsRendered = manifestsRendered
	conditions.SetStatusCondition(&clusterInstance.Status.Conditions,
		v1alpha1.RenderedTemplatesApplied,
		conditionReason, conditionStatus, message)

	if updateErr := conditions.PatchCIStatus(ctx, r.Client, clusterInstance, patch); updateErr != nil {
		if err == nil {
			log.Error("failed to update ClusterInstance status post creation of rendered templates",
				zap.Error(updateErr))
			err = updateErr
		}
	}

	log.Info("Finished applying the rendered manifests")
	return rendered, err
}

func (r *ClusterInstanceReconciler) pruneManifests(
	ctx context.Context,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
	objects []ci.RenderedObject,
) (bool, error) {
	log = log.Named("pruneManifests")

	if len(objects) == 0 {
		log.Info("No objects to prune; skipping pruning operation")
		return true, nil
	}

	// Perform the deletion of objects
	deletionCompleted, err := r.DeletionHandler.DeleteObjects(ctx, clusterInstance, objects, nil,
		ptr.To(deletion.DefaultDeletionTimeout))

	if err != nil {
		if cierrors.IsDeletionTimeoutError(err) {
			log.Warn("Pruning operation timed out; manual intervention may be required", zap.Error(err))
			// Add paused annotation for manual intervention
			if err1 := ci.ApplyPause(ctx, r.Client, log, clusterInstance, err.Error()); err1 != nil {
				err = fmt.Errorf("encountered an error pausing ClusterInstance, error: %w", err1)
			}
			return false, err
		}
		log.Error("Pruning operation encountered an error", zap.Error(err))
		return false, fmt.Errorf("pruning operation encountered an error: %w", err)
	}

	if deletionCompleted {
		log.Info("Pruning operation completed successfully.")
		return true, nil
	}

	log.Info("Pruning operation in progress; waiting for objects to be pruned")
	return false, nil
}

func (r *ClusterInstanceReconciler) updateSuppressedManifestsStatus(
	ctx context.Context,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
	objects []ci.RenderedObject,
) error {

	manifestsRendered := append([]v1alpha1.ManifestReference{}, clusterInstance.Status.ManifestsRendered...)

	for _, object := range objects {
		resourceId := object.GetResourceId()
		manifestRef := object.ManifestReference()
		manifestRef.Status = v1alpha1.ManifestSuppressed
		manifestRef.Message = ""

		// Check if the manifestRef needs to be added to manifestsRendered
		if index, err := v1alpha1.IndexOfManifestByIdentity(manifestRef, manifestsRendered); err != nil {
			manifestsRendered = append(manifestsRendered, *manifestRef)
		} else {
			manifestsRendered[index].UpdateStatus(manifestRef.Status, manifestRef.Message)
		}
		log.Sugar().Infof("Suppressed manifest %s", resourceId)
	}

	// Update ClusterInstance.Status.ManifestsRendered only if there are changes
	if !equality.Semantic.DeepEqual(clusterInstance.Status.ManifestsRendered, manifestsRendered) {
		patch := client.MergeFrom(clusterInstance.DeepCopy())
		clusterInstance.Status.ManifestsRendered = manifestsRendered

		if updateErr := conditions.PatchCIStatus(ctx, r.Client, clusterInstance, patch); updateErr != nil {
			log.Error("Failed to update ClusterInstance.Status.ManifestsRendered", zap.Error(updateErr))
			return fmt.Errorf("failed to update ClusterInstance.Status.ManifestsRendered for ClusterInstance %s/%s: %w",
				clusterInstance.Namespace, clusterInstance.Name, updateErr)
		}
	}
	return nil
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
		log.Error("encountered error while rendering templates", zap.Error(err))
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

	r.Log.Sugar().Infof("ClusterInstanceReconciler is configured to reconcile %d requests concurrently",
		r.ConfigStore.GetMaxConcurrentReconciles())

	holdAnnotationPredicate := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			// Allow reconcile on the addition or removal of the HoldAnnotation
			_, oldEvent := e.ObjectOld.GetAnnotations()[v1alpha1.PausedAnnotation]
			_, newEvent := e.ObjectNew.GetAnnotations()[v1alpha1.PausedAnnotation]
			return oldEvent != newEvent
		},
	}

	// Trigger reconciliation when Provisioned condition changes during reinstall
	provisionedChangedPredicate := predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldCI, oldOK := e.ObjectOld.(*v1alpha1.ClusterInstance)
			newCI, newOK := e.ObjectNew.(*v1alpha1.ClusterInstance)

			if !oldOK || !newOK {
				return false
			}

			// Only trigger if there's an active reinstall
			if newCI.Spec.Reinstall == nil || newCI.Status.Reinstall == nil {
				return false
			}

			// Trigger reconcile when Provisioned condition changes
			oldProvisioned := meta.FindStatusCondition(oldCI.Status.Conditions, string(v1alpha1.ClusterProvisioned))
			newProvisioned := meta.FindStatusCondition(newCI.Status.Conditions, string(v1alpha1.ClusterProvisioned))

			// Check if status or reason changed
			if oldProvisioned == nil && newProvisioned != nil {
				return true
			}
			if oldProvisioned != nil && newProvisioned != nil {
				return oldProvisioned.Status != newProvisioned.Status ||
					oldProvisioned.Reason != newProvisioned.Reason
			}

			return false
		},
	}

	//nolint:wrapcheck
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.ClusterInstance{}).
		WithEventFilter(predicate.Or(
			predicate.GenerationChangedPredicate{},
			predicate.LabelChangedPredicate{},
			holdAnnotationPredicate,
			provisionedChangedPredicate,
		)).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: r.ConfigStore.GetMaxConcurrentReconciles(),
		}).
		Complete(r)
}
