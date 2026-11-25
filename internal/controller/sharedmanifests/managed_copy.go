/*
Copyright 2025 Red Hat, Inc.

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

// Package sharedmanifests contains helpers shared by the ClusterInstance controller and the
// ClusterInstance validator for deciding whether a ConfigMap is a SiteConfig-managed copy.
package sharedmanifests

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stolostron/siteconfig/api/v1alpha1"
)

// IsOwnedManagedCopy reports whether cm is a SiteConfig-managed copy controlled by clusterInstance.
func IsOwnedManagedCopy(cm *corev1.ConfigMap, clusterInstance *v1alpha1.ClusterInstance) bool {
	return cm.Labels[v1alpha1.ManagedCopyLabel] == v1alpha1.ManagedCopyLabelValue &&
		metav1.IsControlledBy(cm, clusterInstance)
}

// IsAdoptableManagedCopy reports whether cm is an ownerless SiteConfig-managed copy that the
// ClusterInstance reconciler may adopt. Global Hub cluster migration and ACM hub restore can copy
// an extra-manifest ConfigMap without its ownerReferences; the managed-copy label marks the object
// as ours. Never adopt a copy that carries any ownerReference. See EP #1056.
func IsAdoptableManagedCopy(cm *corev1.ConfigMap) bool {
	return cm.Labels[v1alpha1.ManagedCopyLabel] == v1alpha1.ManagedCopyLabelValue &&
		len(cm.OwnerReferences) == 0
}
