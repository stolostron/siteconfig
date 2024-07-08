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
*/

package clusterinstance

import (
	"context"
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hivev1 "github.com/openshift/hive/apis/hive/v1"
	"github.com/stolostron/siteconfig/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

func isValidJsonString(input string) bool {
	var result interface{}
	err := json.Unmarshal([]byte(input), &result)
	return err == nil
}

// Validate checks the given ClusterInstance, returns an error if validation fails, returns nil if it succeeds
func Validate(ctx context.Context, c client.Client, clusterInstance *v1alpha1.ClusterInstance) error {

	if clusterInstance.Spec.ClusterName == "" {
		return fmt.Errorf("missing cluster name")
	}

	if clusterInstance.Spec.ClusterImageSetNameRef == "" {
		return fmt.Errorf("clusterImageSetNameRef cannot be empty")
	}
	// Verify that the ClusterImageSet resource exists
	clusterImageSet := hivev1.ClusterImageSet{}
	key := types.NamespacedName{Name: clusterInstance.Spec.ClusterImageSetNameRef, Namespace: ""}
	if err := c.Get(ctx, key, &clusterImageSet); err != nil {
		return fmt.Errorf("encountered error validating ClusterImageSetNameRef: %s, err: %w", clusterInstance.Spec.ClusterImageSetNameRef, err)
	}

	// Check the cluster-level template references are defined
	if (clusterInstance.Spec.TemplateRefs == nil) || (len(clusterInstance.Spec.TemplateRefs) < 1) {
		return fmt.Errorf("missing cluster-level TemplateRefs")
	}
	// Verify that the cluster-level TemplateRefs exist
	for _, templateRef := range clusterInstance.Spec.TemplateRefs {
		cm := &corev1.ConfigMap{}
		if err := c.Get(ctx, types.NamespacedName{
			Name:      templateRef.Name,
			Namespace: templateRef.Namespace,
		}, cm); err != nil {
			return fmt.Errorf("failed to validate cluster-level TemplateRef: [%s in namespace %s], err: %w", templateRef.Name, templateRef.Namespace, err)
		}
	}

	// Check that pull secret exists in cluster namespace
	if clusterInstance.Spec.PullSecretRef.Name != "" {
		// Get the secret
		pullSecret := &corev1.Secret{}
		if err := c.Get(ctx, types.NamespacedName{
			Name:      clusterInstance.Spec.PullSecretRef.Name,
			Namespace: clusterInstance.Spec.ClusterName},
			pullSecret); err != nil {
			return fmt.Errorf("failed to validate Pull Secret: [%s in namespace %s], err: %w", clusterInstance.Spec.PullSecretRef.Name, clusterInstance.Spec.ClusterName, err)
		}
	}

	// Check that InstallConfigOverrides is a valid json-formatted string
	if clusterInstance.Spec.InstallConfigOverrides != "" {
		if !isValidJsonString(clusterInstance.Spec.InstallConfigOverrides) {
			return fmt.Errorf("installConfigOverrides is not a valid JSON-formatted string")
		}
	}

	// Check that IgnitionConfigOverride is a valid json-formatted string
	if clusterInstance.Spec.IgnitionConfigOverride != "" {
		if !isValidJsonString(clusterInstance.Spec.IgnitionConfigOverride) {
			return fmt.Errorf("cluster-level ignitionConfigOverride is not a valid JSON-formatted string")
		}
	}

	// If extraManifests are defined - check that they exist
	if clusterInstance.Spec.ExtraManifestsRefs != nil && len(clusterInstance.Spec.ExtraManifestsRefs) > 0 {
		for _, extraManifestRef := range clusterInstance.Spec.ExtraManifestsRefs {
			cm := &corev1.ConfigMap{}
			if err := c.Get(ctx, types.NamespacedName{
				Name:      extraManifestRef.Name,
				Namespace: clusterInstance.Namespace,
			}, cm); err != nil {
				return fmt.Errorf("failed to retrieve ExtraManifest: %s in namespace %s, err: %w", extraManifestRef.Name, clusterInstance.Namespace, err)
			}
		}
	}

	numControlPlaneAgents := 0
	numWorkerAgents := 0
	for _, node := range clusterInstance.Spec.Nodes {

		if node.Role == "master" {
			numControlPlaneAgents++
		} else if node.Role == "worker" {
			numWorkerAgents++
		}

		// Check the ref templates are defined
		if (node.TemplateRefs == nil) || (len(node.TemplateRefs) < 1) {
			return fmt.Errorf("missing node-level template refs [Node: Hostname=%s]", node.HostName)
		}
		// Verify that the node-level TemplateRefs exist
		for _, templateRef := range node.TemplateRefs {
			cm := &corev1.ConfigMap{}
			if err := c.Get(ctx, types.NamespacedName{
				Name:      templateRef.Name,
				Namespace: templateRef.Namespace,
			}, cm); err != nil {
				return fmt.Errorf("failed to validate node-level TemplateRef: %s in namespace %s [Node: Hostname=%s], err: %w", templateRef.Name, templateRef.Namespace, node.HostName, err)
			}
		}

		// Check that node BMC secrets exist in namespace
		if node.BmcCredentialsName.Name != "" {
			// Get the secret
			bmcSecret := &corev1.Secret{}
			if err := c.Get(ctx, types.NamespacedName{
				Name:      node.BmcCredentialsName.Name,
				Namespace: clusterInstance.Spec.ClusterName},
				bmcSecret); err != nil {
				return fmt.Errorf("failed to validate BMC credentials: %s in namespace %s [Node: Hostname=%s], err: %w", node.BmcCredentialsName.Name, clusterInstance.Spec.ClusterName, node.HostName, err)
			}
		}

		// Check that InstallerArgs is a valid json-formatted string
		if node.InstallerArgs != "" {
			if !isValidJsonString(node.InstallerArgs) {
				return fmt.Errorf("installerArgs is not a valid JSON-formatted string [Node: Hostname=%s]", node.HostName)
			}
		}

		// Check that IgnitionConfigOverride is a valid json-formatted string
		if node.IgnitionConfigOverride != "" {
			if !isValidJsonString(node.IgnitionConfigOverride) {
				return fmt.Errorf("ignitionConfigOverride is not a valid JSON-formatted string [Node: Hostname=%s]", node.HostName)
			}
		}
	}

	if numControlPlaneAgents < 1 {
		return fmt.Errorf("at least 1 ControlPlane agent is required")
	}

	// Validate ClusterType based on the node counts and validate number of worker agents to 0 for SNO
	if numControlPlaneAgents == 1 && clusterInstance.Spec.ClusterType == v1alpha1.ClusterTypeSNO && numWorkerAgents != 0 {
		return fmt.Errorf("sno cluster-type requires 1 control-plane agent and no worker agents")
	}

	// validation succeeded
	return nil
}
