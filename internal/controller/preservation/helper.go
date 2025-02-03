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

package preservation

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/yaml"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/utils/ptr"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
)

const (
	// InternalPreservationLabelKey is used to mark resources that are required internally
	// for ClusterInstance operations and should not be deleted during preservation or restoration.
	InternalPreservationLabelKey = v1alpha1.PreservationLabelKey + ".internal"

	// InternalPreservationLabelValue represents the label value used for identifying
	// internally required ClusterInstance resources.
	InternalPreservationLabelValue = "cluster-instance-required-resource"

	// PreservationModeInternal is a special preservation mode (for internal use only)
	// that indicates resources essential for ClusterInstance operations will be preserved.
	PreservationModeInternal v1alpha1.PreservationMode = "Internal"
)

const (
	// preservedDataLabelKey marks resources (ConfigMaps and Secrets) backed up by SiteConfig for restoration.
	// It enables identification of preserved resources during a restore operation.
	preservedDataLabelKey = v1alpha1.Group + "/preserved-data"

	// preservedInternalDataLabelKey marks internal resources (ConfigMaps and Secrets) backed up by SiteConfig for
	// restoration.
	// It enables identification of internally preserved ClusterInstance resources during a restore operation.
	preservedInternalDataLabelKey = v1alpha1.Group + "/preserved-internal-data"

	// ClusterIdentityDataAnnotationKey stores cluster identity data for preserved resources.
	ClusterIdentityDataAnnotationKey = preservedDataLabelKey + ".cluster-identity"

	// RestoredAtAnnotationKey indicates when a resource was restored.
	RestoredAtAnnotationKey = v1alpha1.PreservationLabelKey + ".restored-at"

	// reinstallGenerationAnnotationKey indicates the generation of resources requiring reinstallation.
	reinstallGenerationAnnotationKey = v1alpha1.Group + "/reinstall-generation"

	// resourceTypeAnnotationKey specifies the resource type (ConfigMap/Secret) being backed up.
	resourceTypeAnnotationKey = v1alpha1.PreservationLabelKey + ".resource-type"

	// preservedDataKey is the key used to store the original ConfigMap/Secret data in the Data field of the
	// corresponding preserved ConfigMap / Secret during backup.
	preservedDataKey = "original-resource"
)

type resourceType string

const (
	configMapResourceType resourceType = "ConfigMap"
	secretResourceType    resourceType = "Secret"
)

// resources encapsulates the names of ConfigMaps and Secrets that are part of preservation operations.
type resources struct {
	configMaps []string
	secrets    []string
}

type config struct {
	resourceKey         types.NamespacedName
	resourceType        resourceType
	ownerRef            string
	reinstallGeneration string
	preservationMode    v1alpha1.PreservationMode
}

// buildLabelSelector constructs a label selector based on the provided parameters.
func buildLabelSelector(labelKey string, operator selection.Operator, values []string) (labels.Selector, error) {
	requirement, err := labels.NewRequirement(labelKey, operator, values)
	if err != nil {
		return nil, fmt.Errorf("failed to create label selector: %w", err)
	}

	// Create and return the label selector with the constructed requirement.
	return labels.NewSelector().Add(*requirement), nil
}

// buildBackupLabelSelector constructs a label selector for backup operations based on the PreservationMode.
func buildBackupLabelSelector(mode v1alpha1.PreservationMode) (labels.Selector, error) {

	switch mode {
	case v1alpha1.PreservationModeNone:
		// No preservation required.
		return nil, nil
	case v1alpha1.PreservationModeAll:
		// Select all resources with the PreservationLabelKey.
		return buildLabelSelector(v1alpha1.PreservationLabelKey, selection.Exists, nil)
	case v1alpha1.PreservationModeClusterIdentity:
		// Select resources with PreservationLabelKey set to ClusterIdentityLabelValue.
		return buildLabelSelector(v1alpha1.PreservationLabelKey, selection.Equals,
			[]string{v1alpha1.ClusterIdentityLabelValue})
		// Select resources labeled with InternalPreservationLabelKey set to InternalPreservationLabelValue
	case PreservationModeInternal:
		return buildLabelSelector(InternalPreservationLabelKey, selection.Equals,
			[]string{InternalPreservationLabelValue})
	}
	// Handle unknown or unsupported PreservationMode.
	return nil, fmt.Errorf("unknown PreservationMode for backup: %v", mode)
}

// buildRestoreLabelSelector constructs a label selector for restore operations based on the PreservationMode.
func buildRestoreLabelSelector(mode v1alpha1.PreservationMode) (labels.Selector, error) {
	switch mode {
	case v1alpha1.PreservationModeNone:
		// No restoration required.
		return nil, nil
	case v1alpha1.PreservationModeAll, v1alpha1.PreservationModeClusterIdentity:
		// Use the preservedLabel for restoration (applies to both PreservationModeAll and
		// PreservationModeClusterIdentity).
		return buildLabelSelector(preservedDataLabelKey, selection.Exists, nil)
	case PreservationModeInternal:
		return buildLabelSelector(preservedInternalDataLabelKey, selection.Exists, nil)
	}

	// Handle unknown or unsupported PreservationMode.
	return nil, fmt.Errorf("unknown PreservationMode for restore: %v", mode)
}

// generateBackupName returns a backup name by appending the resource type and reinstall generation to the base name.
func generateBackupName(rType resourceType, name, generation string) string {
	return fmt.Sprintf("%s-%s-%s", strings.ToLower(string(rType)), name, generation)
}

// sanitizeResourceMetadata removes specific metadata fields from the given Kubernetes object.
func sanitizeResourceMetadata(obj client.Object) {
	obj.SetCreationTimestamp(metav1.Time{})
	obj.SetOwnerReferences(nil)
	obj.SetResourceVersion("")
	obj.SetUID("")
}

func backupResources(
	ctx context.Context, c client.Client, log *zap.Logger,
	labelSelector labels.Selector,
	namespace string,
	baseConfig config,
) error {

	if baseConfig.preservationMode == v1alpha1.PreservationModeNone {
		return nil
	}

	listOptions := &client.ListOptions{
		Namespace:     namespace,
		LabelSelector: labelSelector,
	}

	// Retrieve data for backup
	data, err := resourcesForPreservation(ctx, c, log, listOptions)
	if err != nil {
		return err
	}

	var errs []error

	backupFn := func(rType resourceType, resourceNames []string) {
		for _, name := range resourceNames {
			preservedName := generateBackupName(rType, name, baseConfig.reinstallGeneration)
			config := config{
				resourceKey:         types.NamespacedName{Namespace: namespace, Name: name},
				resourceType:        rType,
				ownerRef:            baseConfig.ownerRef,
				reinstallGeneration: baseConfig.reinstallGeneration,
				preservationMode:    baseConfig.preservationMode,
			}

			if err := backupResource(ctx, c, log, preservedName, config); err != nil {
				log.Error("Failed to backup resource",
					zap.String("kind", string(rType)),
					zap.String("namespace", namespace),
					zap.String("name", name),
					zap.Error(err))
				errs = append(errs, err)
			} else {
				log.Debug("Successfully backed-up resource",
					zap.String("kind", string(rType)),
					zap.String("namespace", namespace),
					zap.String("name", name))
			}
		}
	}

	// Backup each ConfigMap.
	backupFn(configMapResourceType, data.configMaps)

	// Backup each Secret.
	backupFn(secretResourceType, data.secrets)

	return utilerrors.NewAggregate(errs)
}

// backupResource backs up a Kubernetes resource (ConfigMap or Secret) by preserving its metadata and data.
// The backup resource includes a preservation label and is created with the specified backup name.
func backupResource(
	ctx context.Context, c client.Client, log *zap.Logger,
	backupName string,
	config config,
) error {

	var resource client.Object
	switch config.resourceType {
	case configMapResourceType:
		resource = &corev1.ConfigMap{}
	case secretResourceType:
		resource = &corev1.Secret{}
	default:
		err := fmt.Errorf("unsupported resource type for backup: %T", resource)
		log.Error("Unsupported resource type", zap.Error(err))
		return err
	}

	// Fetch the original resource.
	if err := c.Get(ctx, config.resourceKey, resource); err != nil {
		log.Error("Failed to retrieve resource for backup",
			zap.String("namespace", config.resourceKey.Namespace),
			zap.String("name", config.resourceKey.Name),
			zap.Error(err))
		return fmt.Errorf("failed to retrieve %s (%s/%s) for preservation: %w",
			config.resourceType, config.resourceKey.Namespace, config.resourceKey.Name, err)
	}

	// Update logger with object context.
	log = log.With(
		zap.String("kind", string(config.resourceType)),
		zap.String("name", resource.GetName()),
		zap.String("namespace", resource.GetNamespace()),
	)

	log.Debug("Starting resource backup")

	// Sanitize the resource metadata.
	sanitizeResourceMetadata(resource)

	// Marshal the resource data into YAML.
	data, err := yaml.Marshal(resource)
	if err != nil {
		log.Error("Failed to marshal resource", zap.Error(err))
		return fmt.Errorf("failed to marshal resource %s (%s/%s) for preservation: %w",
			config.resourceType, config.resourceKey.Namespace, config.resourceKey.Name, err)
	}

	// Determine the resource type and construct the preserved resource.
	objectMeta := metav1.ObjectMeta{
		Name:      backupName,
		Namespace: config.resourceKey.Namespace,
	}

	// Annotate the preserved resource if it is cluster-identity data
	if resource.GetLabels()[v1alpha1.PreservationLabelKey] == v1alpha1.ClusterIdentityLabelValue {
		metav1.SetMetaDataAnnotation(&objectMeta, ClusterIdentityDataAnnotationKey, "")
	}

	preservedResource := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Secret",
			APIVersion: corev1.SchemeGroupVersion.String(),
		},
		ObjectMeta: objectMeta,
		Immutable:  ptr.To(true),
		Type:       corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			preservedDataKey: data,
		},
	}

	if err := createResource(ctx, c, preservedResource, config); err != nil {
		log.Error("Failed to create preserved resource", zap.Error(err))
		return err
	}

	log.Debug("Successfully backed-up resource")
	return nil
}

func createResource(ctx context.Context, c client.Client, object client.Object, config config) error {
	// Add labels to the preserved object.
	labels := object.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}

	if config.ownerRef != "" {
		labels[ci.OwnedByLabel] = config.ownerRef
	}

	// Add annotations to the preserved object.
	annotations := object.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}

	annotations[resourceTypeAnnotationKey] = string(config.resourceType)

	if config.reinstallGeneration != "" {
		annotations[reinstallGenerationAnnotationKey] = config.reinstallGeneration
	}

	// Set additional label and annotation for retrieving and identifying backed-up resources.
	preservedDataLabel := preservedDataLabelKey
	additionalAnnotation := v1alpha1.PreservationLabelKey
	if config.preservationMode == PreservationModeInternal {
		preservedDataLabel = preservedInternalDataLabelKey
		additionalAnnotation = InternalPreservationLabelKey
	}
	labels[preservedDataLabel] = fmt.Sprint(metav1.Now().Unix())
	annotations[additionalAnnotation] = string(config.preservationMode)

	object.SetLabels(labels)
	object.SetAnnotations(annotations)

	// Create the backup resource.
	if err := c.Create(ctx, object); err != nil {
		return fmt.Errorf("failed to create preserved %s (%s/%s): %w",
			object.GetObjectKind().GroupVersionKind().Kind,
			object.GetNamespace(), object.GetName(),
			err,
		)
	}

	return nil
}

func fetchAndValidateObjectToRestore(
	ctx context.Context, c client.Client, log *zap.Logger, object client.Object, config config,
) (bool, error) {

	// Fetch the object from the cluster.
	if err := c.Get(ctx, config.resourceKey, object); err != nil {
		return true, fmt.Errorf("failed to retrieve resource (%s/%s) for restoration: %w",
			config.resourceKey.Namespace, config.resourceKey.Name, err)
	}

	// Update logger with object context.
	log = log.With(
		zap.String("kind", string(config.resourceType)),
		zap.String("name", object.GetName()),
		zap.String("namespace", object.GetNamespace()),
	)

	// Check preservation mode.
	if !isInternalResource(object) {
		preservationMode, hasAnnotation := object.GetAnnotations()[v1alpha1.PreservationLabelKey]
		if !hasAnnotation && config.preservationMode != v1alpha1.PreservationModeNone {
			log.Warn("Missing preservation mode annotation", zap.String("annotation", v1alpha1.PreservationLabelKey))
			return true, fmt.Errorf("preserved resource (%s/%s) is missing the '%s' annotation",
				config.resourceKey.Namespace, config.resourceKey.Name, v1alpha1.PreservationLabelKey)
		}

		if hasAnnotation && config.preservationMode == v1alpha1.PreservationModeNone {
			log.Warn("Preservation mode mismatch",
				zap.String("annotation", v1alpha1.PreservationLabelKey),
				zap.String("expected", string(v1alpha1.PreservationModeNone)),
			)
			return true, nil // Skip restore as preservation mode is None.
		}

		// Strict check for ClusterIdentity preservation mode.
		if config.preservationMode == v1alpha1.PreservationModeClusterIdentity &&
			preservationMode != string(config.preservationMode) {
			log.Warn("Cluster identity preservation mode mismatch",
				zap.String("expected", string(config.preservationMode)),
				zap.String("found", preservationMode),
			)
			return true, nil // Skip restore due to mismatched preservation mode.
		}
	}

	// Verify ownership if specified.
	if config.ownerRef != "" {
		if err := ci.VerifyOwnership(object, config.ownerRef); err != nil {
			log.Warn("Ownership verification failed",
				zap.String("label", ci.OwnedByLabel),
				zap.String("expected", config.ownerRef),
				zap.String("found", object.GetLabels()[ci.OwnedByLabel]),
			)
			return true, fmt.Errorf("ownership verification failed for object with label %s: %w", ci.OwnedByLabel, err)
		}
	}

	// Validate reinstall generation if specified.
	if config.reinstallGeneration != "" {
		value, ok := object.GetAnnotations()[reinstallGenerationAnnotationKey]
		if !ok {
			log.Warn("Missing reinstall generation annotation",
				zap.String("annotation", reinstallGenerationAnnotationKey))
			return true, fmt.Errorf("preserved resource (%s/%s) is missing the '%s' annotation",
				config.resourceKey.Namespace, config.resourceKey.Name, reinstallGenerationAnnotationKey)
		}
		if value != config.reinstallGeneration {
			log.Warn("Mismatched reinstall generation annotation",
				zap.String("annotation", reinstallGenerationAnnotationKey),
				zap.String("expected", config.reinstallGeneration),
				zap.String("found", value),
			)
			return true, fmt.Errorf("preserved resource's (%s/%s) '%s' annotation (%s) does not match (%s)",
				config.resourceKey.Namespace, config.resourceKey.Name, reinstallGenerationAnnotationKey, value,
				config.reinstallGeneration)
		}
	}

	return false, nil // Proceed with restore.
}

// restoreResource restores a preserved ConfigMap or Secret by fetching it, validating the resource type,
// unmarshaling the preserved data, and applying necessary metadata updates.
func restoreResource(ctx context.Context, c client.Client, log *zap.Logger, conf config) error {
	// Update logger with object context.
	log = log.With(
		zap.String("name", conf.resourceKey.Name),
		zap.String("namespace", conf.resourceKey.Namespace),
	)

	log.Debug("Starting resource restoration")

	// Fetch the preserved resource (always a Secret storing the backup)
	preservedResource := &corev1.Secret{}
	skip, err := fetchAndValidateObjectToRestore(ctx, c, log, preservedResource, conf)
	if err != nil {
		log.Error("Failed to fetch or validate preserved resource", zap.Error(err))
		return err
	}
	if skip {
		log.Debug("Skipping restoration of resource")
		return nil
	}

	// Determine the resource type from annotations
	resourceTypeValue, ok := preservedResource.GetAnnotations()[resourceTypeAnnotationKey]
	if !ok {
		return fmt.Errorf("missing '%s' annotation in preserved resource (%s/%s)",
			resourceTypeAnnotationKey, conf.resourceKey.Namespace, conf.resourceKey.Name)
	}

	data, ok := preservedResource.Data[preservedDataKey]
	if !ok {
		return fmt.Errorf("missing '%s' key in Secret.Data (%s/%s)",
			preservedDataKey, conf.resourceKey.Namespace, conf.resourceKey.Name)
	}

	var (
		restoredResource    client.Object
		preservedData       interface{}
		originalAnnotations map[string]string
		originalLabels      map[string]string
	)

	// Initialize the restored resource object based on the type
	switch resourceType(resourceTypeValue) {
	case secretResourceType:
		restoredResource = &corev1.Secret{}
	case configMapResourceType:
		restoredResource = &corev1.ConfigMap{}
	default:
		return fmt.Errorf("unknown resource type: %s", resourceTypeValue)
	}

	// Unmarshal the preserved data into the target object
	if err := yaml.Unmarshal(data, restoredResource); err != nil {
		return fmt.Errorf("failed to unmarshal preserved resource (%s/%s): %w",
			conf.resourceKey.Namespace, conf.resourceKey.Name, err)
	}

	// Capture annotations, labels, and data for later restoration
	originalAnnotations = restoredResource.GetAnnotations()
	originalLabels = restoredResource.GetLabels()

	switch v := restoredResource.(type) {
	case *corev1.ConfigMap:
		preservedData = v.Data
	case *corev1.Secret:
		preservedData = v.Data
	default:
		return fmt.Errorf("unsupported resource type: %T", restoredResource)
	}

	// Generate the mutate function to restore the resource
	mutateFn := func() error {
		switch v := restoredResource.(type) {
		case *corev1.ConfigMap:
			v.Data = preservedData.(map[string]string)
		case *corev1.Secret:
			v.Data = preservedData.(map[string][]byte)
		}

		// Apply the "restoredAt" annotation to indicate the time of restoration
		if originalAnnotations == nil {
			originalAnnotations = make(map[string]string)
		}
		originalAnnotations[RestoredAtAnnotationKey] = metav1.Now().Format(time.RFC3339)

		// Set the restored annotations and labels
		restoredResource.SetAnnotations(originalAnnotations)
		restoredResource.SetLabels(originalLabels)

		// Sanitize metadata by removing fields like UID, ResourceVersion, and OwnerReferences
		sanitizeResourceMetadata(restoredResource)

		return nil
	}

	// Attempt to create or update the restored resource
	result, err := controllerutil.CreateOrUpdate(ctx, c, restoredResource, mutateFn)
	if err != nil {
		log.Error("Failed to restore preserved resource", zap.String("kind", resourceTypeValue), zap.Error(err))
		return fmt.Errorf("failed to restore preserved %s (%s/%s): %w",
			resourceTypeValue, restoredResource.GetNamespace(), restoredResource.GetName(), err)
	}

	log.Info("Resource restoration completed successfully",
		zap.String("kind", resourceTypeValue),
		zap.String("namespace", restoredResource.GetNamespace()),
		zap.String("name", restoredResource.GetName()),
		zap.String("result", string(result)),
	)

	return nil
}

func restoreResources(
	ctx context.Context, c client.Client, log *zap.Logger,
	labelSelector labels.Selector,
	namespace string,
	baseConfig config,
) error {
	listOptions := &client.ListOptions{
		Namespace:     namespace,
		LabelSelector: labelSelector,
	}

	// Retrieve data for restoration
	data, err := resourcesForPreservation(ctx, c, log, listOptions)
	if err != nil {
		return err
	}

	var errs []error

	restoreFn := func(resourceNames []string) {
		for _, name := range resourceNames {
			config := config{
				resourceKey:         types.NamespacedName{Namespace: namespace, Name: name},
				ownerRef:            baseConfig.ownerRef,
				reinstallGeneration: baseConfig.reinstallGeneration,
				preservationMode:    baseConfig.preservationMode,
			}
			if err := restoreResource(ctx, c, log, config); err != nil {
				log.Error("Failed to restore resource",
					zap.String("namespace", namespace),
					zap.String("name", name),
					zap.Error(err))
				errs = append(errs, err)
			} else {
				log.Debug("Successfully restored resource",
					zap.String("namespace", namespace),
					zap.String("name", name))
			}
		}
	}

	// Restore ConfigMaps.
	restoreFn(data.configMaps)

	// Restore Secrets.
	restoreFn(data.secrets)

	return utilerrors.NewAggregate(errs)
}

// resourcesForPreservation fetches the names of ConfigMaps and Secrets based on the provided ListOptions.
// Logs the count of retrieved resources and aggregates errors if any occur during the listing process.
func resourcesForPreservation(
	ctx context.Context,
	c client.Client,
	log *zap.Logger,
	listOptions *client.ListOptions,
) (*resources, error) {
	var (
		errs       []error
		cmList     corev1.ConfigMapList
		secretList corev1.SecretList
	)

	// Fetch ConfigMaps matching the label selector.
	if err := c.List(ctx, &cmList, listOptions); err != nil {
		errs = append(errs, fmt.Errorf("failed to list ConfigMaps: %w", err))
	}

	// Fetch Secrets matching the label selector.
	if err := c.List(ctx, &secretList, listOptions); err != nil {
		errs = append(errs, fmt.Errorf("failed to list Secrets: %w", err))
	}

	// Aggregate and return any errors encountered during the listing process.
	if len(errs) > 0 {
		return nil, utilerrors.NewAggregate(errs)
	}

	// Collect the names of the retrieved ConfigMaps and Secrets.
	var configMaps, secrets []string
	for _, cm := range cmList.Items {
		configMaps = append(configMaps, cm.Name)
	}
	for _, s := range secretList.Items {
		secrets = append(secrets, s.Name)
	}
	log.Sugar().Infof("Retrieved %d ConfigMaps, %d Secrets for preservation", len(configMaps), len(secrets))

	// Return the collected resource names as a Data object.
	return &resources{
		configMaps: configMaps,
		secrets:    secrets,
	}, nil
}

func isInternalResource(obj client.Object) bool {
	labels := obj.GetLabels()

	if label := labels[InternalPreservationLabelKey]; label == InternalPreservationLabelValue {
		return true
	}

	if label := labels[preservedInternalDataLabelKey]; label != "" {
		return true
	}

	return false
}
