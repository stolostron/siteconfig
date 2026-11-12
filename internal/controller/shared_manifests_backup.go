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

package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ensureSharedSourceBackup includes referenced source inputs in ACM backup. Copies
// remain derived resources and must not inherit this label.
func ensureSharedSourceBackup(ctx context.Context, c client.Client, cm *corev1.ConfigMap) error {
	if value, exists := cm.Labels[acmBackupLabel]; exists && value == acmBackupLabelValue {
		return nil
	}
	patch := client.MergeFrom(cm.DeepCopy())
	if cm.Labels == nil {
		cm.Labels = map[string]string{}
	}
	cm.Labels[acmBackupLabel] = acmBackupLabelValue
	if err := c.Patch(ctx, cm, patch); err != nil {
		return fmt.Errorf("failed to label shared source %s/%s for backup: %w", cm.Namespace, cm.Name, err)
	}
	return nil
}
