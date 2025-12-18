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
	"fmt"
	"time"

	"go.uber.org/zap"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	clusterv1 "open-cluster-management.io/api/cluster/v1"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	"github.com/stolostron/siteconfig/internal/controller/conditions"
	cierrors "github.com/stolostron/siteconfig/internal/controller/errors"
)

const (
	// PostProvisioningGracePeriod is the time to wait after provisioning completes before triggering cluster reimport.
	// This allows the installer to auto-recover (if possible).
	PostProvisioningGracePeriod = 5 * time.Minute

	// GracePeriodRecheckInterval is how often to recheck cluster health during grace period
	GracePeriodRecheckInterval = 1 * time.Minute

	// ReimportRecheckInterval is how often to recheck cluster status while waiting for reimport completion
	ReimportRecheckInterval = 30 * time.Second

	// SpokeClientTimeout is the timeout for spoke cluster REST client operations
	SpokeClientTimeout = 30 * time.Second

	// ConnectivityCheckTimeout is the timeout for validating spoke cluster connectivity
	ConnectivityCheckTimeout = 15 * time.Second

	// KubeconfigSecretKey is the key used to store kubeconfig data in admin-kubeconfig secrets.
	// This is the standard key used by OpenShift Hive for admin-kubeconfig secrets.
	KubeconfigSecretKey = "kubeconfig"
)

// SpokeClientFactory creates Kubernetes clients for spoke clusters.
// This interface enables dependency injection for testing.
type SpokeClientFactory interface {
	// CreateClient creates a client for the spoke cluster using the provided kubeconfig data.
	CreateClient(ctx context.Context, kubeconfigData []byte) (client.Client, error)
}

// DefaultSpokeClientFactory is the production implementation of SpokeClientFactory.
type DefaultSpokeClientFactory struct{}

// CreateClient creates a Kubernetes client from kubeconfig data and validates connectivity.
func (f *DefaultSpokeClientFactory) CreateClient(ctx context.Context, kubeconfigData []byte) (client.Client, error) {
	// Create REST config from kubeconfig data
	restConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigData)
	if err != nil {
		return nil, fmt.Errorf("failed to create REST config from kubeconfig: %w", err)
	}

	// Configure timeout for the REST config
	restConfig.Timeout = SpokeClientTimeout

	// Create client
	spokeClient, err := client.New(restConfig, client.Options{})
	if err != nil {
		return nil, fmt.Errorf("failed to create spoke client: %w", err)
	}

	// Validate connectivity before returning
	if err := validateSpokeConnectivity(ctx, spokeClient); err != nil {
		return nil, err
	}

	return spokeClient, nil
}

// EnsureClusterIsReimported ensures the ManagedCluster is reimported after reinstall provisioning completes.
// Returns:
// - result: non-zero if requeue is needed
// - reimportNeeded: true if reimport is still needed (provisioning not yet complete)
// - err: error if reimport operation failed
func (r *ReinstallHandler) EnsureClusterIsReimported(
	ctx context.Context,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
) (result reconcile.Result, reimportNeeded bool, err error) {

	log = log.Named("EnsureClusterIsReimported")

	result = reconcile.Result{}
	reimportNeeded = false
	err = nil

	// Check if there's an active or recently completed reinstall
	reinstallStatus := clusterInstance.Status.Reinstall
	if reinstallStatus == nil || reinstallStatus.ObservedGeneration == "" {
		// No reinstall has ever been processed, nothing to do
		return
	}

	// Guard: reinstalls must be enabled
	if !r.ConfigStore.GetAllowReinstalls() {
		log.Debug("Reinstalls not enabled, skipping reimport check")
		return
	}

	// If reinstall hasn't completed yet, skip reimport check
	reinstallCompleted := findReinstallStatusCondition(clusterInstance, v1alpha1.ReinstallRequestProcessed)
	if reinstallCompleted == nil || reinstallCompleted.Status != metav1.ConditionTrue {
		log.Debug("Reinstall still in progress, skipping reimport check")
		return
	}

	var clusterReimportedCondition *metav1.Condition

	defer func() {
		if clusterReimportedCondition != nil {
			patch := client.MergeFrom(clusterInstance.DeepCopy())
			if changed := setReinstallStatusCondition(clusterInstance, *clusterReimportedCondition); changed {
				if updateErr := conditions.PatchCIStatus(ctx, r.Client, clusterInstance, patch); updateErr != nil {
					err = logAndWrapUpdateFailure(log, err, updateErr,
						fmt.Sprintf("failed to update reinstall condition '%s'", clusterReimportedCondition.Type))
				}
			}
		}
	}()

	// Check if reimport already completed
	existingCondition := findReinstallStatusCondition(clusterInstance, v1alpha1.ReinstallClusterReimported)
	if existingCondition != nil && existingCondition.Status == metav1.ConditionTrue {
		log.Info("Cluster reimport already completed")
		return
	}

	// Check if provisioning has completed
	provisionedCondition := meta.FindStatusCondition(clusterInstance.Status.Conditions,
		string(v1alpha1.ClusterProvisioned))

	if provisionedCondition == nil || provisionedCondition.Status != metav1.ConditionTrue {
		log.Info("Provisioning not yet complete, waiting for Provisioned condition to trigger reconciliation")
		return
	}

	// Check grace period status
	inGracePeriod, gracePeriodRemaining := isWithinGracePeriod(clusterInstance)
	timeSinceProvisioning := PostProvisioningGracePeriod - gracePeriodRemaining

	log.Info("Checking cluster health after provisioning",
		zap.Duration("timeSinceProvisioning", timeSinceProvisioning),
		zap.Duration("gracePeriod", PostProvisioningGracePeriod),
		zap.Duration("gracePeriodRemaining", gracePeriodRemaining))

	// Get the ManagedCluster resource
	managedCluster, err := getManagedClusterResource(ctx, r.Client, clusterInstance)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// ManagedCluster not yet created, wait
			log.Info("ManagedCluster not yet created, waiting for provisioning")
			reimportNeeded = true
			clusterReimportedCondition = reinstallClusterReimportedConditionStatus(
				metav1.ConditionFalse, v1alpha1.InProgress,
				"Waiting for ManagedCluster resource to be created")
			err = nil
			return
		}
		// Check if manifest is not yet rendered
		if cierrors.IsManagedClusterNotInManifestError(err) {
			log.Info("ManagedCluster not yet rendered, waiting for provisioning to complete")
			reimportNeeded = true
			clusterReimportedCondition = reinstallClusterReimportedConditionStatus(
				metav1.ConditionFalse, v1alpha1.InProgress,
				"Waiting for ManagedCluster to be rendered")
			err = nil
			return
		}
		clusterReimportedCondition = reinstallClusterReimportedConditionStatus(
			metav1.ConditionFalse, v1alpha1.Failed, err.Error())
		return
	}

	// Evaluate	cluster's health
	healthStatus := assessClusterHealth(managedCluster)

	// If cluster is fully healthy, reimport complete
	if healthStatus.isHealthy {
		log.Info("ManagedCluster is fully healthy and imported, no reimport needed",
			zap.String("Available", string(healthStatus.availableStatus)),
			zap.String("ImportSucceeded", string(healthStatus.importStatus)),
			zap.String("Joined", string(healthStatus.joinedStatus)))
		clusterReimportedCondition = reinstallClusterReimportedConditionStatus(
			metav1.ConditionTrue, v1alpha1.Completed,
			"ManagedCluster is healthy and fully imported")
		return
	}

	// Cluster is unhealthy - check if within grace period
	if inGracePeriod {
		log.Info("Cluster unhealthy but still in grace period, waiting for auto-recovery",
			zap.Duration("gracePeriodRemaining", gracePeriodRemaining),
			zap.String("availableStatus", string(healthStatus.availableStatus)),
			zap.String("availableReason", healthStatus.availableReason),
			zap.String("importStatus", string(healthStatus.importStatus)),
			zap.String("importReason", healthStatus.importReason))

		reimportNeeded = true
		result = reconcile.Result{
			Requeue:      true,
			RequeueAfter: GracePeriodRecheckInterval,
		}
		clusterReimportedCondition = reinstallClusterReimportedConditionStatus(
			metav1.ConditionFalse, v1alpha1.InProgress,
			fmt.Sprintf("Cluster unhealthy, waiting for auto-recovery (%.0fs remaining)",
				gracePeriodRemaining.Seconds()))
		return
	}

	// Grace period has expired - check if import is already in progress
	if isImportInProgress(managedCluster) {
		log.Info("Import is already in progress, waiting for completion",
			zap.String("ImportReason", healthStatus.importReason))
		reimportNeeded = true
		result = reconcile.Result{Requeue: true, RequeueAfter: ReimportRecheckInterval}
		clusterReimportedCondition = reinstallClusterReimportedConditionStatus(
			metav1.ConditionFalse, v1alpha1.InProgress,
			fmt.Sprintf("Import already in progress: %s", healthStatus.importReason))
		return
	}

	if healthStatus.availableStatus == metav1.ConditionUnknown &&
		healthStatus.availableReason == "ManagedClusterLeaseUpdateStopped" {
		log.Info("Cluster lease update stopped after grace period - triggering reimport",
			zap.Duration("timeSinceProvisioning", timeSinceProvisioning),
			zap.String("AvailableReason", healthStatus.availableReason),
			zap.String("AvailableMessage", healthStatus.availableMessage))
	}

	if healthStatus.importStatus == metav1.ConditionFalse &&
		healthStatus.importReason == "ManagedClusterImportFailed" {
		log.Info("Previous import failed - triggering reimport",
			zap.String("FailureReason", healthStatus.importMessage))
	}

	// At this point, grace period has expired and reimport is needed
	log.Info("Grace period expired and cluster still unhealthy, triggering reimport",
		zap.String("cluster", managedCluster.Name),
		zap.Duration("timeSinceProvisioning", timeSinceProvisioning))

	// Check if we've already initiated reimport (manifests applied to spoke) - avoid redundant work
	// The ReimportInitiated reason indicates we've successfully applied klusterlet manifests
	if existingCondition != nil &&
		existingCondition.Status == metav1.ConditionFalse &&
		existingCondition.Reason == string(v1alpha1.ReimportInitiated) {
		log.Info("Reimport already initiated, waiting for cluster to become healthy",
			zap.String("conditionMessage", existingCondition.Message))
		reimportNeeded = true
		result = reconcile.Result{Requeue: true, RequeueAfter: ReimportRecheckInterval}
		// Don't update condition - keep existing one to preserve the original timestamp
		return
	}

	// Perform the reimport
	if err = r.performReimport(ctx, log, managedCluster); err != nil {
		log.Error("Failed to perform reimport", zap.Error(err))
		clusterReimportedCondition = reinstallClusterReimportedConditionStatus(
			metav1.ConditionFalse, v1alpha1.Failed,
			fmt.Sprintf("Failed to perform reimport: %v", err))
		return
	}

	// Reimport initiated successfully - use ReimportInitiated reason to track state
	log.Info("Reimport initiated successfully")
	reimportNeeded = true
	result = reconcile.Result{Requeue: true, RequeueAfter: ReimportRecheckInterval}
	clusterReimportedCondition = reinstallClusterReimportedConditionStatus(
		metav1.ConditionFalse, v1alpha1.ReimportInitiated,
		"Klusterlet manifests applied to spoke cluster, waiting for cluster to become healthy")

	return
}

// performReimport performs the reimport of the ManagedCluster by directly applying klusterlet manifests to the spoke
// cluster. This bypasses the OCM import controller which may skip reimport due to ImportOnly strategy when
// ManagedClusterImportSucceeded is already True.
func (r *ReinstallHandler) performReimport(
	ctx context.Context,
	log *zap.Logger,
	managedCluster *clusterv1.ManagedCluster,
) error {

	log = log.Named("performReimport")
	clusterName := managedCluster.Name

	// Get spoke kubeconfig from admin-kubeconfig secret
	log.Info("Getting spoke kubeconfig", zap.String("cluster", clusterName))
	kubeconfigData, err := extractKubeconfigFromSecret(ctx, r.Client, clusterName)
	if err != nil {
		return fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	// Create spoke client using the factory
	spokeClient, err := r.SpokeClientFactory.CreateClient(ctx, kubeconfigData)
	if err != nil {
		return fmt.Errorf("failed to create spoke client: %w", err)
	}

	// Apply klusterlet CRDs from ManifestWork to spoke
	log.Info("Applying klusterlet CRDs to spoke", zap.String("cluster", clusterName))
	crdsManifestWork := fmt.Sprintf("%s-klusterlet-crds", clusterName)
	if err := applyManifestWorkToSpoke(ctx, r.Client, log, clusterName, crdsManifestWork, spokeClient); err != nil {
		return fmt.Errorf("failed to apply klusterlet CRDs: %w", err)
	}
	log.Info("Klusterlet CRDs applied successfully", zap.String("cluster", clusterName))

	// Apply klusterlet resources from ManifestWork to spoke
	log.Info("Applying klusterlet resources to spoke", zap.String("cluster", clusterName))
	klusterletManifestWork := fmt.Sprintf("%s-klusterlet", clusterName)
	if err := applyManifestWorkToSpoke(ctx, r.Client, log, clusterName, klusterletManifestWork, spokeClient); err != nil {
		return fmt.Errorf("failed to apply klusterlet resources: %w", err)
	}
	log.Info("Klusterlet resources applied successfully", zap.String("cluster", clusterName))

	return nil
}

// applyManifestWorkToSpoke extracts manifests from a ManifestWork and applies them to the spoke cluster
func applyManifestWorkToSpoke(
	ctx context.Context,
	c client.Client,
	log *zap.Logger,
	namespace string,
	manifestWorkName string,
	spokeClient client.Client,
) error {
	// Get the ManifestWork using unstructured client
	manifestWork := &unstructured.Unstructured{}
	manifestWork.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "work.open-cluster-management.io",
		Version: "v1",
		Kind:    "ManifestWork",
	})

	if err := c.Get(ctx, types.NamespacedName{
		Namespace: namespace,
		Name:      manifestWorkName,
	}, manifestWork); err != nil {
		return fmt.Errorf("failed to get ManifestWork %s/%s: %w", namespace, manifestWorkName, err)
	}

	// Extract manifests from spec.workload.manifests
	workload, found, err := unstructured.NestedFieldCopy(manifestWork.Object, "spec", "workload", "manifests")
	if err != nil {
		return fmt.Errorf("failed to extract workload.manifests from ManifestWork: %w", err)
	}
	if !found {
		return fmt.Errorf("manifestWork %s/%s does not contain spec.workload.manifests", namespace, manifestWorkName)
	}

	manifests, ok := workload.([]interface{})
	if !ok {
		return fmt.Errorf("spec.workload.manifests is not a list")
	}

	log.Info("Applying manifests from ManifestWork",
		zap.String("manifestWork", manifestWorkName),
		zap.Int("manifestCount", len(manifests)))

	// Apply each manifest to the spoke cluster
	for i, manifest := range manifests {
		manifestMap, ok := manifest.(map[string]interface{})
		if !ok {
			log.Warn("Skipping invalid manifest", zap.Int("index", i))
			continue
		}

		// Convert to unstructured for applying
		obj := &unstructured.Unstructured{Object: manifestMap}

		// Use server-side apply for idempotency
		if err := applyToSpoke(ctx, spokeClient, obj); err != nil {
			// Log warning but continue with other manifests
			log.Warn("Failed to apply manifest",
				zap.Int("index", i),
				zap.String("kind", obj.GetKind()),
				zap.String("name", obj.GetName()),
				zap.Error(err))
		} else {
			log.Info("Applied manifest",
				zap.String("kind", obj.GetKind()),
				zap.String("name", obj.GetName()),
				zap.String("namespace", obj.GetNamespace()))
		}
	}

	return nil
}

// applyToSpoke applies a single unstructured object to the spoke cluster using server-side apply
func applyToSpoke(
	ctx context.Context,
	spokeClient client.Client,
	obj *unstructured.Unstructured,
) error {
	// Make a deep copy for patching
	objCopy := obj.DeepCopy()

	// Set managed fields for server-side apply
	objCopy.SetManagedFields(nil)

	// Use Patch with Apply strategy for idempotency
	patchOpts := []client.PatchOption{
		client.ForceOwnership,
		client.FieldOwner("siteconfig-controller"),
	}

	if err := spokeClient.Patch(ctx, objCopy, client.Apply, patchOpts...); err != nil {
		return fmt.Errorf("failed to apply object %s/%s: %w", obj.GetNamespace(), obj.GetName(), err)
	}
	return nil
}

// validateAdminKubeconfigSecret performs security validation on kubeconfig secrets
func validateAdminKubeconfigSecret(secret *corev1.Secret, expectedClusterName string) error {
	// Validate secret type is Opaque (standard type for kubeconfig secrets)
	if secret.Type != corev1.SecretTypeOpaque {
		return fmt.Errorf("invalid secret type: expected %s, got %s",
			corev1.SecretTypeOpaque, secret.Type)
	}

	// Validate secret is in the expected namespace (namespace == cluster name) to prevent cross-namespace
	// secret access attacks
	if secret.Namespace != expectedClusterName {
		return fmt.Errorf("secret namespace mismatch: expected %s, got %s",
			expectedClusterName, secret.Namespace)
	}

	// Validate expected name format
	expectedName := fmt.Sprintf("%s-admin-kubeconfig", expectedClusterName)
	if secret.Name != expectedName {
		return fmt.Errorf("secret name mismatch: expected %s, got %s",
			expectedName, secret.Name)
	}

	return nil
}

// extractKubeconfigFromSecret retrieves the admin kubeconfig for a cluster from the expected secret.
// It validates the secret type before extracting the kubeconfig data.
func extractKubeconfigFromSecret(ctx context.Context, c client.Client, clusterName string) ([]byte, error) {
	secretName := fmt.Sprintf("%s-admin-kubeconfig", clusterName)
	secret := &corev1.Secret{}

	if err := c.Get(ctx, types.NamespacedName{
		Namespace: clusterName,
		Name:      secretName,
	}, secret); err != nil {
		return nil, fmt.Errorf("failed to get admin kubeconfig secret %s/%s: %w",
			clusterName, secretName, err)
	}

	if err := validateAdminKubeconfigSecret(secret, clusterName); err != nil {
		return nil, fmt.Errorf("admin kubeconfig secret validation failed: %w", err)
	}

	return extractKubeconfig(secret)
}

// extractKubeconfig extracts kubeconfig data from a secret.
func extractKubeconfig(secret *corev1.Secret) ([]byte, error) {
	if data, exists := secret.Data[KubeconfigSecretKey]; exists && len(data) > 0 {
		return data, nil
	}

	return nil, fmt.Errorf("secret %s/%s missing kubeconfig data (expected key: %s)",
		secret.Namespace, secret.Name, KubeconfigSecretKey)
}

// validateSpokeConnectivity verifies the spoke cluster is reachable
func validateSpokeConnectivity(ctx context.Context, spokeClient client.Client) error {
	// Create timeout context for validation
	timeoutCtx, cancel := context.WithTimeout(ctx, ConnectivityCheckTimeout)
	defer cancel()

	// Try to get default namespace as connectivity check
	ns := &corev1.Namespace{}
	if err := spokeClient.Get(timeoutCtx, types.NamespacedName{Name: "default"}, ns); err != nil {
		return fmt.Errorf("spoke cluster connectivity validation failed: %w", err)
	}

	return nil
}

// getManagedClusterResource retrieves the ManagedCluster resource by finding its reference in the ClusterInstance
// status.
func getManagedClusterResource(
	ctx context.Context,
	c client.Client,
	ci *v1alpha1.ClusterInstance,
) (*clusterv1.ManagedCluster, error) {
	manifest := getManagedClusterManifest(ci)
	if manifest == nil {
		return nil, fmt.Errorf("managedCluster not found in status: %w", cierrors.NewManagedClusterNotInManifestError())
	}

	// Fetch the ManagedCluster
	managedCluster := &clusterv1.ManagedCluster{}
	if err := c.Get(ctx, types.NamespacedName{Name: manifest.Name}, managedCluster); err != nil {
		return nil, fmt.Errorf("failed to get ManagedCluster %s: %w", manifest.Name, err)
	}

	return managedCluster, nil
}

// clusterHealthStatus represents the health state of a ManagedCluster
type clusterHealthStatus struct {
	isHealthy        bool
	availableStatus  metav1.ConditionStatus
	availableReason  string
	availableMessage string
	importStatus     metav1.ConditionStatus
	importReason     string
	importMessage    string
	joinedStatus     metav1.ConditionStatus
}

// assessClusterHealth evaluates ManagedCluster health based on its conditions.
func assessClusterHealth(mc *clusterv1.ManagedCluster) *clusterHealthStatus {
	findCondition := func(condType string) *metav1.Condition {
		for i := range mc.Status.Conditions {
			if mc.Status.Conditions[i].Type == condType {
				return &mc.Status.Conditions[i]
			}
		}
		return nil
	}

	availableCondition := findCondition(clusterv1.ManagedClusterConditionAvailable)
	importSucceededCondition := findCondition("ManagedClusterImportSucceeded")
	joinedCondition := findCondition(clusterv1.ManagedClusterConditionJoined)

	status := &clusterHealthStatus{}

	if availableCondition != nil {
		status.availableStatus = availableCondition.Status
		status.availableReason = availableCondition.Reason
		status.availableMessage = availableCondition.Message
	}

	if importSucceededCondition != nil {
		status.importStatus = importSucceededCondition.Status
		status.importReason = importSucceededCondition.Reason
		status.importMessage = importSucceededCondition.Message
	}

	if joinedCondition != nil {
		status.joinedStatus = joinedCondition.Status
	}

	status.isHealthy = availableCondition != nil &&
		availableCondition.Status == metav1.ConditionTrue &&
		importSucceededCondition != nil &&
		importSucceededCondition.Status == metav1.ConditionTrue &&
		joinedCondition != nil &&
		joinedCondition.Status == metav1.ConditionTrue

	return status
}

// isWithinGracePeriod checks if the cluster is within the post-provisioning  grace period, returning the
// remaining duration.
func isWithinGracePeriod(ci *v1alpha1.ClusterInstance) (bool, time.Duration) {
	provisionedCondition := meta.FindStatusCondition(ci.Status.Conditions,
		string(v1alpha1.ClusterProvisioned))

	if provisionedCondition == nil || provisionedCondition.Status != metav1.ConditionTrue {
		// Not yet provisioned, consider still in grace period
		return true, 0
	}

	timeSinceProvisioning := time.Since(provisionedCondition.LastTransitionTime.Time)
	remaining := PostProvisioningGracePeriod - timeSinceProvisioning

	return remaining > 0, remaining
}

// isImportInProgress checks if cluster import is currently running.
func isImportInProgress(mc *clusterv1.ManagedCluster) bool {
	for i := range mc.Status.Conditions {
		cond := &mc.Status.Conditions[i]
		if cond.Type == "ManagedClusterImportSucceeded" {
			return cond.Status == metav1.ConditionFalse &&
				(cond.Reason == "ManagedClusterImporting" ||
					cond.Reason == "ManagedClusterWaitForImporting")
		}
	}
	return false
}
