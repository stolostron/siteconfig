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
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	"golang.org/x/exp/maps"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// generateName returns a backup name by appending the reinstall generation to the base name.
func generateName(name, generation string) string {
	return fmt.Sprintf("%s-%s", name, generation)
}

// addPreservedLabel adds a timestamp backup label to the given labels map to indicate preservation.
func addPreservedLabel(labels map[string]string) map[string]string {
	if labels == nil {
		labels = make(map[string]string)
	}
	labels[preservedLabel] = metav1.NewTime(time.Now()).String()
	return labels
}

// removePreservedLabel removes the backup preservation label from the provided labels map.
// If the labels map is nil, it returns the map unchanged.
func removePreservedLabel(labels map[string]string) map[string]string {
	if labels == nil {
		return labels
	}
	delete(labels, preservedLabel)
	return labels
}

// backupConfigMap backs up a Kubernetes ConfigMap by preserving its metadata and data.
// The backup ConfigMap includes a preservation label and is created with the specified name.
func backupConfigMap(
	ctx context.Context,
	c client.Client,
	namespace, name, preservedName string,
) error {
	// Fetch the original ConfigMap.
	originalConfigMap := &corev1.ConfigMap{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, originalConfigMap); err != nil {
		return fmt.Errorf("failed to retrieve ConfigMap %s/%s for preservation: %w", namespace, name, err)
	}

	// Marshal the ConfigMap data into JSON.
	encodedData, err := json.Marshal(originalConfigMap.Data)
	if err != nil {
		return fmt.Errorf("failed to marshal ConfigMap.Data for preservation: %w", err)
	}

	// Create the backup ConfigMap with the specified preserved name.
	// - The ObjectMeta includes the original annotations and labels, along with an additional label indicating
	//   preservation.
	// - The Data field uses the original resource name as the key and stores the marshaled data as the value.
	//   This approach ensures that the original ConfigMap is not modified or deleted and can be restored later if
	//   needed.
	preservedConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:        preservedName,
			Namespace:   namespace,
			Annotations: originalConfigMap.Annotations,
			Labels:      addPreservedLabel(originalConfigMap.Labels),
		},
		Data: map[string]string{
			name: string(encodedData),
		},
	}

	// Create the backup ConfigMap.
	if err := c.Create(ctx, preservedConfigMap); err != nil {
		return fmt.Errorf("failed to create preserved ConfigMap %s/%s: %w", namespace, preservedName, err)
	}

	return nil
}

// backupSecret backs up a Kubernetes Secret by preserving its metadata and data.
// The backup Secret includes a preservation label and is created with the specified name.
func backupSecret(
	ctx context.Context,
	c client.Client,
	namespace, name, preservedName string,
) error {
	// Fetch the original Secret.
	originalSecret := &corev1.Secret{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, originalSecret); err != nil {
		return fmt.Errorf("failed to retrieve Secret %s/%s for preservation: %w", namespace, name, err)
	}

	// Marshal the Secret data into JSON.
	encodedData, err := json.Marshal(originalSecret.Data)
	if err != nil {
		return fmt.Errorf("failed to marshal Secret.Data for preservation: %w", err)
	}

	// Create the backup Secret with the specified preserved name.
	// - The ObjectMeta includes the original annotations and labels, along with an additional label indicating
	//   preservation.
	// - The original Secret type is preserved to maintain compatibility with future restores.
	// - The Data field uses the original resource name as the key and stores the marshaled data as the value.
	//   This ensures that the original Secret remains unaltered and can be restored later when required.
	preservedSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        preservedName,
			Namespace:   namespace,
			Annotations: originalSecret.Annotations,
			Labels:      addPreservedLabel(originalSecret.Labels),
		},
		Type: originalSecret.Type,
		Data: map[string][]byte{
			name: encodedData,
		},
	}

	// Create the backup Secret.
	if err := c.Create(ctx, preservedSecret); err != nil {
		return fmt.Errorf("failed to create preserved Secret %s/%s: %w", namespace, preservedName, err)
	}

	return nil
}

// restoreConfigMap restores a preserved ConfigMap to its original state.
// It retrieves the preserved ConfigMap by name, decodes its data, and creates a new ConfigMap
// with the original name and metadata.
func restoreConfigMap(
	ctx context.Context,
	c client.Client,
	namespace, preservedName string,
) error {
	// Fetch the preserved ConfigMap from the cluster.
	preservedConfigMap := &corev1.ConfigMap{}
	key := types.NamespacedName{Namespace: namespace, Name: preservedName}
	if err := c.Get(ctx, key, preservedConfigMap); err != nil {
		return fmt.Errorf("failed to retrieve ConfigMap %s/%s for restoration: %w", namespace, preservedName, err)
	}

	// Validate that the preserved ConfigMap contains exactly one key.
	if len(preservedConfigMap.Data) != 1 {
		return fmt.Errorf("preserved ConfigMap should only have 1 key, received %d", len(preservedConfigMap.Data))
	}

	// Extract and decode the data from the preserved ConfigMap.
	originalName := maps.Keys(preservedConfigMap.Data)[0]
	var decodedData map[string]string
	if err := json.Unmarshal([]byte(preservedConfigMap.Data[originalName]), &decodedData); err != nil {
		return fmt.Errorf("failed to unmarshal ConfigMap.Data, err: %w", err)
	}

	// Construct the restored ConfigMap using the original resource name and metadata.
	// - The name is set to the original resource's name.
	// - The namespace, annotations, and labels are copied from the preserved ConfigMap,
	//   with the preservation label removed to avoid marking it as a backup.
	// - The data is decoded from the preserved ConfigMap, restoring the original structure.
	restoredConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:        originalName,
			Namespace:   namespace,
			Annotations: preservedConfigMap.Annotations,
			Labels:      removePreservedLabel(preservedConfigMap.Labels),
		},
		Data: decodedData,
	}

	// Attempt to create the restored ConfigMap. Do not error if the ConfigMap already exists.
	if err := c.Create(ctx, restoredConfigMap); err != nil {
		if k8serrors.IsAlreadyExists(err) {
			return nil
		}
		return fmt.Errorf("failed to restore preserved ConfigMap %s/%s: %w", namespace, preservedName, err)
	}

	return nil
}

// restoreSecret restores a preserved Secret to its original state.
// It retrieves the preserved Secret by name, decodes its data, and creates a new Secret
// with the original name and metadata.
func restoreSecret(
	ctx context.Context,
	c client.Client,
	namespace, preservedName string,
) error {
	// Fetch the preserved Secret from the cluster.
	preservedSecret := &corev1.Secret{}
	key := types.NamespacedName{Namespace: namespace, Name: preservedName}
	if err := c.Get(ctx, key, preservedSecret); err != nil {
		return fmt.Errorf("failed to retrieve Secret %s/%s for restoration: %w", namespace, preservedName, err)
	}

	// Validate that the preserved Secret contains exactly one key.
	if (len(preservedSecret.Data)) != 1 {
		return fmt.Errorf("preserved Secret should only have 1 key, received %d", len(preservedSecret.Data))
	}

	// Extract and decode the data from the preserved Secret.
	originalName := maps.Keys(preservedSecret.Data)[0]
	var decodedData map[string][]byte
	if err := json.Unmarshal([]byte(preservedSecret.Data[originalName]), &decodedData); err != nil {
		return fmt.Errorf("failed to unmarshal Secret.Data, err: %w", err)
	}

	// Construct the restored Secret using the original resource name and metadata.
	// - The name is set to the original resource's name.
	// - The namespace, annotations, and labels are copied from the preserved Secret,
	//   with the preservation label removed to indicate it is no longer a backup.
	// - The type is retained from the preserved Secret to maintain its intended usage.
	// - The data is decoded from the preserved Secret, restoring the original structure.
	restoredSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        originalName,
			Namespace:   namespace,
			Annotations: preservedSecret.Annotations,
			Labels:      removePreservedLabel(preservedSecret.Labels),
		},
		Type: preservedSecret.Type,
		Data: decodedData,
	}

	// Attempt to create the restored Secret. Do not error if the Secret already exists.
	if err := c.Create(ctx, restoredSecret); err != nil {
		if k8serrors.IsAlreadyExists(err) {
			return nil
		}
		return fmt.Errorf("failed to restore preserved Secret %s/%s: %w", namespace, preservedName, err)
	}

	return nil
}

// retrievePreservationData fetches the names of ConfigMaps and Secrets based on the provided ListOptions.
// Logs the count of retrieved resources and aggregates errors if any occur during the listing process.
func retrievePreservationData(
	ctx context.Context,
	c client.Client,
	listOptions *client.ListOptions,
	log *zap.Logger,
) (*Data, error) {
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
		return nil, errors.NewAggregate(errs)
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
	return &Data{
		ConfigMaps: configMaps,
		Secrets:    secrets,
	}, nil
}
