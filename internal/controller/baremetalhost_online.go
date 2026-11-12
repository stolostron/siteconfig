/*
Copyright 2026.

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
	"strings"

	"go.uber.org/zap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
)

const (
	// This annotation is internal bookkeeping, not a desired power-state input.
	bmhOnlineInitializedAnnotation = v1alpha1.Group + "/online-initialized"
	// This manager holds the initial value only until another controller changes it.
	// It must never be reapplied once the normal manager has released online.
	bmhInitialPowerFieldManager = "siteconfig-initial-power"
)

func (r *ClusterInstanceReconciler) applyRenderedObject(
	ctx context.Context, c client.Client, log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance, object unstructured.Unstructured, dryRun bool,
) (controllerutil.OperationResult, error) {
	if object.GroupVersionKind().Group != "metal3.io" || object.GetKind() != "BareMetalHost" {
		return applyObject(ctx, c, log, object)
	}
	reader := r.APIReader
	if reader == nil {
		reader = c
	}
	return applyBareMetalHost(ctx, c, reader, log, clusterInstance, object, dryRun)
}

// applyBareMetalHost initializes online once per CI/installation/host association.
// Subsequent writes omit online after safely handing off SSA ownership. Reads use
// the API reader: a cache that strips managedFields cannot support this decision.
func applyBareMetalHost(
	ctx context.Context, c client.Client, reader client.Reader, log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance, object unstructured.Unstructured, dryRun bool,
) (controllerutil.OperationResult, error) {
	prepared, err := prepareBareMetalHostApply(clusterInstance, object)
	if err != nil {
		return controllerutil.OperationResultNone, err
	}

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(object.GroupVersionKind())
	if err := reader.Get(ctx, client.ObjectKeyFromObject(&object), existing); err != nil {
		if !apierrors.IsNotFound(err) {
			return controllerutil.OperationResultNone, fmt.Errorf("failed to read BMH before applying: %w", err)
		}
		// An SSA create with version zero succeeds only while the object is
		// absent. If another controller creates it after our GET, retry against
		// that object rather than blindly overwriting its power state.
		prepared.SetResourceVersion("0")
		if _, err := applyObject(ctx, c, log, *prepared); err != nil {
			return controllerutil.OperationResultNone, err
		}
		return controllerutil.OperationResultCreated, nil
	}
	if err := validateBareMetalHostApply(existing, prepared); err != nil {
		return controllerutil.OperationResultNone, err
	}
	prepared.SetUID(existing.GetUID())
	prepared.SetResourceVersion(existing.GetResourceVersion())
	if bareMetalHostOnlineInitialized(existing, prepared, clusterInstance) {
		if err := preserveBareMetalHostOnline(ctx, c, existing, prepared, dryRun); err != nil {
			return controllerutil.OperationResultNone, err
		}
	}
	return applyObject(ctx, c, log, *prepared)
}

func prepareBareMetalHostApply(
	clusterInstance *v1alpha1.ClusterInstance, object unstructured.Unstructured,
) (*unstructured.Unstructured, error) {
	if clusterInstance.UID == "" {
		return nil, fmt.Errorf("cannot initialize BMH power without a ClusterInstance UID")
	}
	lifecycle := string(clusterInstance.UID)
	if clusterInstance.Spec.Reinstall != nil {
		lifecycle += ":" + clusterInstance.Spec.Reinstall.Generation
	}
	owner := ci.GenerateOwnedByLabelValue(clusterInstance.Namespace, clusterInstance.Name)
	if ci.GetOwner(&object) != owner {
		return nil, fmt.Errorf("rendered BMH does not belong to ClusterInstance %s", owner)
	}

	prepared := object.DeepCopy()
	annotations := prepared.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[bmhOnlineInitializedAnnotation] = lifecycle
	prepared.SetAnnotations(annotations)

	return prepared, nil
}

func validateBareMetalHostApply(existing, prepared *unstructured.Unstructured) error {
	if !existing.GetDeletionTimestamp().IsZero() {
		return fmt.Errorf("cannot apply deleting BMH %s/%s", existing.GetNamespace(), existing.GetName())
	}
	existingOwner := ci.GetOwner(existing)
	if existingOwner != "" && existingOwner != ci.GetOwner(prepared) {
		return fmt.Errorf("baremetalhost belongs to another ClusterInstance: %s", existingOwner)
	}
	return nil
}

func bareMetalHostOnlineInitialized(
	existing, prepared *unstructured.Unstructured, clusterInstance *v1alpha1.ClusterInstance,
) bool {
	// Removing the ownership label is the deallocation boundary for inventory
	// hosts reused by the same CI. Existence alone does not imply initialization.
	if ci.GetOwner(existing) != ci.GetOwner(prepared) {
		return false
	}
	previousLifecycle := existing.GetAnnotations()[bmhOnlineInitializedAnnotation]
	lifecycle := prepared.GetAnnotations()[bmhOnlineInitializedAnnotation]
	// Adopt legacy SiteConfig-owned hosts without replaying an install default.
	if previousLifecycle == lifecycle || previousLifecycle == "" {
		return true
	}
	// A restored CI has a new UID but represents the same installed cluster.
	// Adopt its marker only for a handled restore with the same reinstall
	// generation; an explicit reinstall must still initialize power again.
	restoreName := clusterInstance.Labels[veleroRestoreNameLabel]
	if restoreName == "" || clusterInstance.Annotations[veleroRestoreHandledAnnotation] != restoreName {
		return false
	}
	_, previousReinstall, _ := strings.Cut(previousLifecycle, ":")
	_, currentReinstall, _ := strings.Cut(lifecycle, ":")
	return previousReinstall == currentReinstall
}

func preserveBareMetalHostOnline(
	ctx context.Context, c client.Client, existing, prepared *unstructured.Unstructured, dryRun bool,
) error {
	online, found, err := unstructured.NestedBool(existing.Object, "spec", "online")
	if err != nil || !found {
		return fmt.Errorf("existing BMH has no valid spec.online value")
	}
	if dryRun {
		// A dry-run handoff is not persisted, so a following dry-run omission
		// could delete a solely owned field. Validate other changes with its
		// current value instead; the real apply performs the ownership handoff.
		if err := unstructured.SetNestedField(prepared.Object, online, "spec", "online"); err != nil {
			return fmt.Errorf("failed to preserve BMH online during validation: %w", err)
		}
		return nil
	}

	ownsOnline, err := siteConfigOwnsOnline(existing)
	if err != nil {
		return err
	}
	if ownsOnline {
		handoff := &unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": existing.GetAPIVersion(),
			"kind":       existing.GetKind(),
			"metadata": map[string]interface{}{
				"name": existing.GetName(), "namespace": existing.GetNamespace(),
				"resourceVersion": existing.GetResourceVersion(), "uid": string(existing.GetUID()),
			},
			"spec": map[string]interface{}{"online": online},
		}}
		// Share the current value, without force, before removing it from the
		// normal manager. The resourceVersion prevents replaying a stale value
		// if an installer or power controller writes concurrently.
		//nolint:staticcheck // TODO: migrate to client.Client.Apply()
		if err := c.Patch(ctx, handoff, client.Apply, client.FieldOwner(bmhInitialPowerFieldManager)); err != nil {
			return fmt.Errorf("failed to hand off BMH online ownership: %w", err)
		}
		prepared.SetResourceVersion(handoff.GetResourceVersion())
	}
	unstructured.RemoveNestedField(prepared.Object, "spec", "online")
	return nil
}

func siteConfigOwnsOnline(object *unstructured.Unstructured) (bool, error) {
	for _, entry := range object.GetManagedFields() {
		// Historic Update entries are passive initializers: an SSA omission
		// does not remove their fields. Only an active Apply entry needs to
		// release online before a later Apply can safely omit it.
		if entry.Manager != ClusterInstanceFieldManager || entry.Operation != metav1.ManagedFieldsOperationApply ||
			entry.Subresource != "" || entry.FieldsV1 == nil {
			continue
		}
		var fields struct {
			Spec map[string]json.RawMessage `json:"f:spec"`
		}
		if err := json.Unmarshal(entry.FieldsV1.Raw, &fields); err != nil {
			return false, fmt.Errorf("failed to read BMH online field ownership: %w", err)
		}
		if _, found := fields.Spec["f:online"]; found {
			return true, nil
		}
	}
	return false, nil
}
