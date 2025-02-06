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

package reinstall

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
	"github.com/stolostron/siteconfig/internal/controller/conditions"
	"github.com/stolostron/siteconfig/internal/controller/configuration"
	"github.com/stolostron/siteconfig/internal/controller/deletion"
	cierrors "github.com/stolostron/siteconfig/internal/controller/errors"
	"github.com/stolostron/siteconfig/internal/controller/preservation"
)

const (
	requeueWithShortInterval          = 2 * time.Second
	deletionRequeueWithMediumInterval = 30 * time.Second
	deletionRequeueWithShortInterval  = 15 * time.Second
)

type ReinstallHandler struct {
	Client          client.Client
	Logger          *zap.Logger
	ConfigStore     *configuration.ConfigurationStore
	DeletionHandler *deletion.DeletionHandler
}

func (r *ReinstallHandler) ProcessRequest(
	ctx context.Context,
	clusterInstance *v1alpha1.ClusterInstance,
) (result reconcile.Result, err error) {

	reinstallSpec := clusterInstance.Spec.Reinstall
	if reinstallSpec == nil {
		r.Logger.Warn("Missing ReinstallSpec",
			zap.String("ClusterInstance", client.ObjectKeyFromObject(clusterInstance).String()))
		return ctrl.Result{}, errors.New("missing ReinstallSpec")
	}

	// Update logger with object context.
	log := r.Logger.Named("ProcessReinstallRequest").With(
		zap.String("ClusterInstance", client.ObjectKeyFromObject(clusterInstance).String()),
		zap.String("ResourceVersion", clusterInstance.ResourceVersion),
		zap.String("ReinstallGeneration", reinstallSpec.Generation),
		zap.String("PreservationMode", string(reinstallSpec.PreservationMode)),
	)

	// Nothing to do if the reinstall request is processed
	if isRequestProcessed(clusterInstance) {
		return reconcile.Result{}, nil
	}

	reinstallRequestProcessedCondition := findReinstallStatusCondition(clusterInstance,
		v1alpha1.ReinstallRequestProcessed)

	if reinstallRequestProcessedCondition == nil || isNewRequest(clusterInstance) {
		log.Info("New reinstall request detected")
		if err = initializeReinstallStatus(ctx, r.Client, log, clusterInstance); err != nil {
			log.Error("Failed to initialize Reinstall Status", zap.Error(err))
			return reconcile.Result{}, err
		}
		return reconcile.Result{Requeue: true}, nil
	}

	log.Info("Processing reinstall request")

	// Ensure that ReinstallRequestProcessed InProgress is set after initialization
	if reinstallRequestProcessedCondition.Reason == string(v1alpha1.Initialized) {
		patch := client.MergeFrom(clusterInstance.DeepCopy())
		clusterInstance.Status.Reinstall.RequestStartTime = metav1.Now()
		reinstallRequestProcessedCondition = reinstallRequestProcessedConditionStatus(
			metav1.ConditionFalse, v1alpha1.InProgress, fmt.Sprintf("Processing reinstall request for generation %s",
				reinstallSpec.Generation))

		setReinstallStatusCondition(clusterInstance, *reinstallRequestProcessedCondition)

		if updateErr := conditions.PatchCIStatus(ctx, r.Client, clusterInstance, patch); updateErr != nil {
			return reconcile.Result{}, logAndWrapUpdateFailure(log, nil, updateErr,
				fmt.Sprintf("failed to update reinstall condition '%s'", reinstallRequestProcessedCondition.Type))
		}
		return reconcile.Result{Requeue: true}, nil
	}

	result = reconcile.Result{}
	err = nil
	updateEndtime := false

	defer func() {
		patch := client.MergeFrom(clusterInstance.DeepCopy())
		if updateEndtime {
			clusterInstance.Status.Reinstall.RequestEndTime = metav1.Now()
			// Add hold annotation for manual intervention (CNF-15719)
		}

		changed := setReinstallStatusCondition(clusterInstance, *reinstallRequestProcessedCondition)
		if changed || updateEndtime {
			if updateErr := conditions.PatchCIStatus(ctx, r.Client, clusterInstance, patch); updateErr != nil {
				err = logAndWrapUpdateFailure(log, err, updateErr,
					fmt.Sprintf("failed to update reinstall condition '%s'", reinstallRequestProcessedCondition.Type))
			}
		}
	}()

	tasks := []struct {
		Description string
		Run         func(context.Context, *zap.Logger, *v1alpha1.ClusterInstance) (reconcile.Result, error)
	}{
		{"Validating reinstall request", r.ensureValidReinstallRequest},
		{"Setting reinstall request startTime", r.ensureStartTimeIsSet},
		{"Applying preservation labels to ClusterInstance secrets", r.applyPreservedLabelToClusterInstanceSecrets},
		{"Preserving data", r.ensureDataIsPreserved},
		{"Deleting rendered manifests", r.ensureRenderedManifestsAreDeleted},
		{"Restoring preserved data", r.ensurePreservedDataIsRestored},
	}
	for _, task := range tasks {
		log.Info("Executing task", zap.String("Task", task.Description))
		result, err = task.Run(ctx, log, clusterInstance)
		if !result.IsZero() {
			log.Info("ClusterInstance to be requeued")
			return
		}
		if err != nil {
			log.Error("Task failed", zap.String("Task", task.Description), zap.Error(err))
			reinstallRequestProcessedCondition = reinstallRequestProcessedConditionStatus(
				metav1.ConditionFalse, v1alpha1.Failed,
				fmt.Sprintf("Encountered error executing task: %s. Error: %v", task.Description, err))
			updateEndtime = true
			return
		}
		log.Info("Task completed", zap.String("Task", task.Description))
	}

	log.Info("Finalizing reinstall request")
	if err = r.finalizeReinstallRequest(ctx, log, clusterInstance); err != nil {
		return
	}
	reinstallRequestProcessedCondition = reinstallRequestProcessedConditionStatus(
		metav1.ConditionTrue,
		v1alpha1.Completed,
		"The reinstall process completed successfully and the cluster is ready to be reprovisioned.")

	return
}

func isRequestProcessed(clusterInstance *v1alpha1.ClusterInstance) bool {

	reinstallStatus := clusterInstance.Status.Reinstall
	if reinstallStatus == nil {
		return false
	}

	if reinstallStatus.ObservedGeneration == clusterInstance.Spec.Reinstall.Generation {
		return true
	}

	cond := meta.FindStatusCondition(reinstallStatus.Conditions, string(v1alpha1.ReinstallRequestProcessed))
	if cond == nil {
		return false
	}

	isProcessed := cond.Reason == string(v1alpha1.Completed) ||
		cond.Reason == string(v1alpha1.Failed) ||
		cond.Reason == string(v1alpha1.TimedOut)

	return reinstallStatus.InProgressGeneration == clusterInstance.Spec.Reinstall.Generation &&
		!reinstallStatus.RequestEndTime.IsZero() &&
		isProcessed && cond.Status != metav1.ConditionUnknown
}

func isNewRequest(clusterInstance *v1alpha1.ClusterInstance) bool {

	reinstallStatus := clusterInstance.Status.Reinstall

	if reinstallStatus == nil || reinstallStatus.InProgressGeneration != clusterInstance.Spec.Reinstall.Generation {
		return true
	}

	return false
}

// ensureValidReinstallRequest checks if cluster reinstall request is valid
func (r *ReinstallHandler) ensureValidReinstallRequest(
	ctx context.Context,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
) (result reconcile.Result, err error) {

	result = reconcile.Result{}
	err = nil

	patch := client.MergeFrom(clusterInstance.DeepCopy())

	var reinstallRequestCondition *metav1.Condition

	if !r.ConfigStore.GetAllowReinstalls() {
		log.Warn("SiteConfig Operator is not configured for cluster reinstalls")

		err = fmt.Errorf("siteConfig operator is not configured for cluster reinstalls")
		reinstallRequestCondition = reinstallRequestValidatedConditionStatus(metav1.ConditionFalse, v1alpha1.Failed,
			"Cluster Reinstallation is not enabled")
	} else {
		reinstallRequestCondition = reinstallRequestValidatedConditionStatus(metav1.ConditionTrue, v1alpha1.Completed,
			"Valid reinstall request")
	}

	if updateRequired := setReinstallStatusCondition(clusterInstance, *reinstallRequestCondition); updateRequired {
		result = reconcile.Result{Requeue: true}
		if updateErr := conditions.PatchCIStatus(ctx, r.Client, clusterInstance, patch); updateErr != nil {
			err = logAndWrapUpdateFailure(log, err, updateErr,
				fmt.Sprintf("failed to update reinstall condition '%s'", reinstallRequestCondition.Type))
		}
	}

	return
}

// ensureStartTimeIsSet sets the reinstall request startTime if unset
func (r *ReinstallHandler) ensureStartTimeIsSet(
	ctx context.Context,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
) (reconcile.Result, error) {
	if clusterInstance.Status.Reinstall.RequestStartTime.IsZero() {
		patch := client.MergeFrom(clusterInstance.DeepCopy())
		clusterInstance.Status.Reinstall.RequestStartTime = metav1.Now()
		if updateErr := conditions.PatchCIStatus(ctx, r.Client, clusterInstance, patch); updateErr != nil {
			return reconcile.Result{Requeue: true},
				logAndWrapUpdateFailure(log, nil, updateErr, "failed to update Reinstall.RequestStartTime")
		}
		log.Info("Updated Reinstall.RequestStartTime")
		return reconcile.Result{Requeue: true}, nil
	}
	log.Info("Reinstall.RequestStartTime is already set")
	return reconcile.Result{}, nil
}

// ensureRenderedManifestsAreDeleted deletes rendered manifests except for ManagedCluster
func (r *ReinstallHandler) ensureRenderedManifestsAreDeleted(ctx context.Context, log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance) (result reconcile.Result, err error) {

	result = reconcile.Result{}
	err = nil

	patch := client.MergeFrom(clusterInstance.DeepCopy())

	renderedManifestsDeletedCondition := findReinstallStatusCondition(clusterInstance,
		v1alpha1.ReinstallRenderedManifestsDeleted)

	defer func() {
		if renderedManifestsDeletedCondition != nil {
			if changed := setReinstallStatusCondition(clusterInstance, *renderedManifestsDeletedCondition); changed {
				result.Requeue = true
				if updateErr := conditions.PatchCIStatus(ctx, r.Client, clusterInstance, patch); updateErr != nil {
					err = logAndWrapUpdateFailure(log, err, updateErr,
						fmt.Sprintf("failed to update reinstall condition '%s'", renderedManifestsDeletedCondition.Type))
				}
			}
		}
	}()

	excludeFromDeletion := []ci.RenderedObject(nil)
	mc, err := getManagedCluster(clusterInstance)
	if err != nil {
		log.Error("Failed to retrieve ManagedCluster", zap.Error(err))
		renderedManifestsDeletedCondition = reinstallRenderedManifestsDeletedConditionStatus(
			metav1.ConditionFalse, v1alpha1.Failed, err.Error())
		return
	}

	if mc != nil {
		excludeFromDeletion = []ci.RenderedObject{*mc}
	}

	if renderedManifestsDeletedCondition == nil ||
		renderedManifestsDeletedCondition.Reason == string(v1alpha1.Initialized) {
		renderedManifestsDeletedCondition = reinstallRenderedManifestsDeletedConditionStatus(
			metav1.ConditionFalse, v1alpha1.InProgress, "Rendered manifests are being deleted")
		return
	}

	switch renderedManifestsDeletedCondition.Reason {
	case string(v1alpha1.Failed), string(v1alpha1.Completed):
		return
	}

	deletionCompleted, err := r.DeletionHandler.DeleteRenderedObjects(
		ctx, clusterInstance, excludeFromDeletion, ptr.To(deletion.DefaultDeletionTimeout))

	if err != nil {
		if cierrors.IsDeletionTimeoutError(err) {
			log.Warn("Deletion timed out, deferring further attempts")
			// Add hold annotation for manual intervention (CNF-15719)
			renderedManifestsDeletedCondition = reinstallRenderedManifestsDeletedConditionStatus(
				metav1.ConditionFalse, v1alpha1.TimedOut,
				"Timed-out waiting for rendered manifests to be deleted.")
			return
		}

		log.Error("Deletion encountered an error", zap.Error(err))
		renderedManifestsDeletedCondition = reinstallRenderedManifestsDeletedConditionStatus(
			metav1.ConditionFalse, v1alpha1.Failed, err.Error())
		return
	}

	if !deletionCompleted {
		log.Info("Waiting for rendered manifests to be deleted")
		result = reconcile.Result{Requeue: true, RequeueAfter: deletionRequeueWithMediumInterval}
		return
	}

	log.Info("Deletion completed successfully")
	renderedManifestsDeletedCondition = reinstallRenderedManifestsDeletedConditionStatus(
		metav1.ConditionTrue, v1alpha1.Completed,
		"Successfully deleted rendered manifests")
	return
}

func (r *ReinstallHandler) finalizeReinstallRequest(
	ctx context.Context,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance) error {

	// Initiate deletion of preserved resources (limited to Secrets and ConfigMaps).
	// The function does not wait for their removal; instead, it marks them for deletion and proceeds.
	// If an error occurs during the deletion process, it is returned immediately,
	// causing finalizeReinstallRequest to return the same error.
	if _, err := preservation.Cleanup(ctx, r.Client, r.DeletionHandler, log, clusterInstance); err != nil {
		return fmt.Errorf("failed to cleanup preserved resources, error: %w", err)
	}

	patch := client.MergeFrom(clusterInstance.DeepCopy())
	updateRequired := false

	if clusterInstance.Status.Reinstall.ObservedGeneration != clusterInstance.Spec.Reinstall.Generation {
		clusterInstance.Status.Reinstall.ObservedGeneration = clusterInstance.Spec.Reinstall.Generation
		updateRequired = true
	}

	if clusterInstance.Status.Reinstall.InProgressGeneration != "" {
		clusterInstance.Status.Reinstall.InProgressGeneration = ""
		updateRequired = true
	}

	if clusterInstance.Status.Reinstall.RequestEndTime.IsZero() {
		clusterInstance.Status.Reinstall.RequestEndTime = metav1.Now()
		updateRequired = true
	}

	// Set clusterInstance.Status.Reinstall.History (CNF-15747)

	if updateRequired {
		if updateErr := conditions.PatchCIStatus(ctx, r.Client, clusterInstance, patch); updateErr != nil {
			return logAndWrapUpdateFailure(log, nil, updateErr, "failed to update reinstall status")
		}
	}
	return nil
}

// applyPreservedLabelToClusterInstanceSecrets ensures that secrets associated with the ClusterInstance
// are labeled for preservation before deleting rendered manifests.
// This is done on a best-effort basis. If a secret does not exist, it is ignored, and execution continues.
// For example, this can occur when a BareMetalHost object is deleted, causing its BMC credential secret
// to be removed as well.
//
// The function iterates over the relevant secrets and applies a preservation label to each.
// If an error occurs while retrieving or updating a secret, the function returns immediately
// with an error, preventing further execution.
func (r *ReinstallHandler) applyPreservedLabelToClusterInstanceSecrets(
	ctx context.Context,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
) (reconcile.Result, error) {
	// Apply label to ClusterInstance secrets.
	applyPreservationLabel := func(name string) error {
		secret := &corev1.Secret{}
		key := types.NamespacedName{Namespace: clusterInstance.Namespace, Name: name}
		if err := r.Client.Get(ctx, key, secret); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			log.Sugar().Errorf("failed to retrieve Secret for applying preservation label", zap.Error(err))
			return fmt.Errorf("failed to retrieve Secret for applying preservation label, err: %w", err)
		}
		patch := client.MergeFrom(secret.DeepCopy())
		updateRequired := setOrUpdateLabel(&secret.ObjectMeta, preservation.InternalPreservationLabelKey,
			preservation.InternalPreservationLabelValue)
		if updateRequired {
			if err := r.Client.Patch(ctx, secret, patch); err != nil {
				log.Sugar().Errorf("failed to update Secret %s with preservation label", key.String(), zap.Error(err))
				return fmt.Errorf("failed to update Secret %s with preservation label, err: %w", key.String(), err)
			}
			log.Sugar().Debugf("Updated Secret %s with preservation label", key.String())
		}
		return nil
	}

	clusterInstanceSecrets := []string{clusterInstance.Spec.PullSecretRef.Name}
	for _, node := range clusterInstance.Spec.Nodes {
		clusterInstanceSecrets = append(clusterInstanceSecrets, node.BmcCredentialsName.Name)
	}

	for _, secret := range clusterInstanceSecrets {
		err := applyPreservationLabel(secret)
		if err != nil {
			return reconcile.Result{},
				fmt.Errorf("encountered an error applying preservation label to ClusterInstance Secret, err: %w", err)
		}
	}

	return reconcile.Result{}, nil
}

// ensureDataIsPreserved preserves resources
func (r *ReinstallHandler) ensureDataIsPreserved(
	ctx context.Context,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
) (result reconcile.Result, err error) {

	result = reconcile.Result{}
	err = nil

	var (
		preservationDataBackedupCondition, clusterIdentityDetectedCondition *metav1.Condition
	)

	defer func() {
		patch := client.MergeFrom(clusterInstance.DeepCopy())
		updateRequired := false

		if preservationDataBackedupCondition != nil {
			updateRequired = setReinstallStatusCondition(clusterInstance, *preservationDataBackedupCondition)
		}

		if clusterIdentityDetectedCondition != nil {
			if changed := setReinstallStatusCondition(clusterInstance, *clusterIdentityDetectedCondition); changed {
				updateRequired = true
			}
		}

		if updateRequired {
			result.Requeue = true
			updateErr := conditions.PatchCIStatus(ctx, r.Client, clusterInstance, patch)
			if updateErr != nil {
				result.RequeueAfter = requeueWithShortInterval
				err = logAndWrapUpdateFailure(log, err, updateErr,
					"failed to update reinstall preservation condition(s)")
			}
		}
	}()

	if clusterInstance.Spec.Reinstall.PreservationMode == v1alpha1.PreservationModeNone {
		preservationDataBackedupCondition = reinstallPreservationDataBackedupConditionStatus(
			metav1.ConditionFalse, v1alpha1.PreservationNotRequired, "PreservationMode is set to None.")
		clusterIdentityDetectedCondition = reinstallClusterIdentityDataDetectedConditionStatus(
			metav1.ConditionFalse, v1alpha1.PreservationNotRequired, "PreservationMode is set to None.")
		return
	}

	log.Info("Starting data preservation")

	err = preservation.Backup(ctx, r.Client, log, clusterInstance)
	if err != nil {
		log.Error("Encountered error during data preservation", zap.Error(err))

		preservationDataBackedupCondition = reinstallPreservationDataBackedupConditionStatus(
			metav1.ConditionFalse, v1alpha1.Failed, err.Error())

		clusterIdentityDetectedCondition = reinstallClusterIdentityDataDetectedConditionStatus(
			metav1.ConditionFalse, v1alpha1.Failed, "Data preservation failed")

		return
	}

	log.Info("Data preservation completed, computing backup summary...")

	preservationDataBackedupCondition, clusterIdentityDetectedCondition, err =
		getDataPreservationSummary(ctx, r.Client, log, clusterInstance)

	return
}

// ensurePreservedDataIsRestored restores preserved resources
func (r *ReinstallHandler) ensurePreservedDataIsRestored(
	ctx context.Context,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
) (result reconcile.Result, err error) {

	result = reconcile.Result{}
	err = nil

	var preservationDataRestoredCondition *metav1.Condition

	defer func() {
		if preservationDataRestoredCondition != nil {
			patch := client.MergeFrom(clusterInstance.DeepCopy())

			if changed := setReinstallStatusCondition(clusterInstance, *preservationDataRestoredCondition); changed {
				result.Requeue = true
				updateErr := conditions.PatchCIStatus(ctx, r.Client, clusterInstance, patch)
				if updateErr != nil {
					result.RequeueAfter = requeueWithShortInterval
					err = logAndWrapUpdateFailure(log, err, updateErr,
						fmt.Sprintf("failed to update reinstall restore data condition '%s'",
							preservationDataRestoredCondition.Type))
				}
			}
		}
	}()

	if clusterInstance.Spec.Reinstall.PreservationMode == v1alpha1.PreservationModeNone {

		preservationDataRestoredCondition = reinstallPreservationDataRestoredConditionStatus(
			metav1.ConditionFalse, v1alpha1.PreservationNotRequired, "PreservationMode is set to None.")

		return
	}

	log.Info("Starting data restoration")

	err = preservation.Restore(ctx, r.Client, log, clusterInstance)
	if err != nil {
		log.Error("Encountered error during data restoration", zap.Error(err))
		preservationDataRestoredCondition = reinstallPreservationDataRestoredConditionStatus(
			metav1.ConditionFalse, v1alpha1.Failed, err.Error())
		return
	}

	log.Info("Data restoration completed, computing restoration summary...")

	preservationDataRestoredCondition, err = getDataRestorationSummary(ctx, r.Client, log, clusterInstance)
	return
}
