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

func validateResources(ctx context.Context, c client.Client, clusterInstance *v1alpha1.ClusterInstance) error {
	if clusterInstance.Spec.ClusterImageSetNameRef == "" {
		return fmt.Errorf("clusterImageSetNameRef cannot be empty")
	}
	// Verify that the ClusterImageSet resource exists
	clusterImageSet := hivev1.ClusterImageSet{}
	key := types.NamespacedName{Name: clusterInstance.Spec.ClusterImageSetNameRef, Namespace: ""}
	if err := c.Get(ctx, key, &clusterImageSet); err != nil {
		return fmt.Errorf("encountered error validating ClusterImageSetNameRef: %s, err: %w",
			clusterInstance.Spec.ClusterImageSetNameRef, err)
	}

	// Check that pull secret exists in cluster namespace
	pullSecret := &corev1.Secret{}
	key = types.NamespacedName{Name: clusterInstance.Spec.PullSecretRef.Name, Namespace: clusterInstance.Namespace}
	if err := c.Get(ctx, key, pullSecret); err != nil {
		return fmt.Errorf("failed to validate Pull Secret: [%s in namespace %s], err: %w",
			key.Name, key.Namespace, err)
	}

	// If extraManifests are defined - check that they exist
	if clusterInstance.Spec.ExtraManifestsRefs != nil && len(clusterInstance.Spec.ExtraManifestsRefs) > 0 {
		for _, extraManifestRef := range clusterInstance.Spec.ExtraManifestsRefs {
			key = types.NamespacedName{Name: extraManifestRef.Name, Namespace: clusterInstance.Namespace}
			cm := &corev1.ConfigMap{}
			if err := c.Get(ctx, key, cm); err != nil {
				return fmt.Errorf("failed to retrieve ExtraManifest: %s in namespace %s, err: %w",
					key.Name, key.Namespace, err)
			}
		}
	}

	// Check that node BMC secrets exist in namespace
	for _, node := range clusterInstance.Spec.Nodes {
		key = types.NamespacedName{Name: node.BmcCredentialsName.Name, Namespace: clusterInstance.Namespace}
		bmcSecret := &corev1.Secret{}
		if err := c.Get(ctx, key, bmcSecret); err != nil {
			return fmt.Errorf(
				"failed to validate BMC credentials: %s in namespace %s [Node: Hostname=%s], err: %w",
				node.BmcCredentialsName.Name, clusterInstance.Spec.ClusterName, node.HostName, err)
		}
	}

	// validation succeeded
	return nil
}

func validateTemplateRefs(ctx context.Context, c client.Client, clusterInstance *v1alpha1.ClusterInstance) error {

	// Check the cluster-level template references are defined
	if (clusterInstance.Spec.TemplateRefs == nil) || (len(clusterInstance.Spec.TemplateRefs) < 1) {
		return fmt.Errorf("missing cluster-level TemplateRefs")
	}

	// Verify that the cluster-level TemplateRefs exist
	for _, templateRef := range clusterInstance.Spec.TemplateRefs {
		key := types.NamespacedName{Name: templateRef.Name, Namespace: templateRef.Namespace}
		cm := &corev1.ConfigMap{}
		if err := c.Get(ctx, key, cm); err != nil {
			return fmt.Errorf("failed to validate cluster-level TemplateRef: [%s in namespace %s], err: %w",
				key.Name, key.Namespace, err)
		}
	}

	for _, node := range clusterInstance.Spec.Nodes {
		// Check the ref templates are defined
		if (node.TemplateRefs == nil) || (len(node.TemplateRefs) < 1) {
			return fmt.Errorf("missing node-level template refs [Node: Hostname=%s]", node.HostName)
		}
		// Verify that the node-level TemplateRefs exist
		for _, templateRef := range node.TemplateRefs {
			key := types.NamespacedName{Name: templateRef.Name, Namespace: templateRef.Namespace}
			cm := &corev1.ConfigMap{}
			if err := c.Get(ctx, key, cm); err != nil {
				return fmt.Errorf(
					"failed to validate node-level TemplateRef: %s in namespace %s [Node: Hostname=%s], err: %w",
					key.Name, key.Namespace, node.HostName, err)
			}
		}
	}

	// validation succeeded
	return nil
}

func isValidJSONString(input string) bool {
	if input == "" {
		return true
	}

	var result interface{}
	err := json.Unmarshal([]byte(input), &result)
	return err == nil
}

func validateJSONStrings(clusterInstance *v1alpha1.ClusterInstance) error {
	// Check that InstallConfigOverrides is a valid json-formatted string
	if !isValidJSONString(clusterInstance.Spec.InstallConfigOverrides) {
		return fmt.Errorf("installConfigOverrides is not a valid JSON-formatted string")
	}

	// Check that IgnitionConfigOverride is a valid json-formatted string
	if !isValidJSONString(clusterInstance.Spec.IgnitionConfigOverride) {
		return fmt.Errorf("cluster-level ignitionConfigOverride is not a valid JSON-formatted string")
	}

	for _, node := range clusterInstance.Spec.Nodes {
		// Check that InstallerArgs is a valid json-formatted string
		if !isValidJSONString(node.InstallerArgs) {
			return fmt.Errorf("installerArgs is not a valid JSON-formatted string [Node: Hostname=%s]", node.HostName)
		}

		// Check that IgnitionConfigOverride is a valid json-formatted string
		if !isValidJSONString(node.IgnitionConfigOverride) {
			return fmt.Errorf(
				"node-level ignitionConfigOverride is not a valid JSON-formatted string [Node: Hostname=%s]",
				node.HostName)
		}
	}

	// validation succeeded
	return nil
}

func validateControlPlaneAgents(clusterInstance *v1alpha1.ClusterInstance) error {
	numControlPlaneAgents := 0
	for _, node := range clusterInstance.Spec.Nodes {
		if node.Role == "master" {
			numControlPlaneAgents++
		}
	}

	if numControlPlaneAgents < 1 {
		return fmt.Errorf("at least 1 ControlPlane agent is required")
	}

	// Check that for SNO ClusterType, only 1 ControlPlane agent is specificed
	if clusterInstance.Spec.ClusterType == v1alpha1.ClusterTypeSNO && numControlPlaneAgents != 1 {
		// Single-node clusters must have a single control plane node and no workers.
		return fmt.Errorf("sno cluster-type can only have 1 control-plane agent")
	}

	// validation succeeded
	return nil
}

// Validate checks the given ClusterInstance, returns an error if validation fails, returns nil if it succeeds
func Validate(ctx context.Context, c client.Client, clusterInstance *v1alpha1.ClusterInstance) error {

	if clusterInstance.Spec.ClusterName == "" {
		return fmt.Errorf("missing cluster name")
	}

	if err := validateResources(ctx, c, clusterInstance); err != nil {
		return err
	}

	if err := validateTemplateRefs(ctx, c, clusterInstance); err != nil {
		return err
	}

	if err := validateJSONStrings(clusterInstance); err != nil {
		return err
	}

	if err := validateControlPlaneAgents(clusterInstance); err != nil {
		return err
	}

	// validation succeeded
	return nil
}
