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

package clusterinstance

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hivev1 "github.com/openshift/hive/apis/hive/v1"
	"github.com/stolostron/siteconfig/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

func validateResources(
	ctx context.Context,
	c client.Client,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
	strictSecretPresenceValidation bool,
) error {
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

	// Check that pull secret exists in cluster namespace.
	// When soft-fail is enabled (strictSecretPresenceValidation=false), only NotFound
	// continues validation with a warn log; other Get errors still fail.
	pullSecret := &corev1.Secret{}
	key = types.NamespacedName{Name: clusterInstance.Spec.PullSecretRef.Name, Namespace: clusterInstance.Namespace}
	if err := c.Get(ctx, key, pullSecret); err != nil {
		if !strictSecretPresenceValidation && apierrors.IsNotFound(err) {
			log.Warn("Pull Secret not detected; continuing validation",
				zap.String("name", key.Name),
				zap.String("namespace", key.Namespace),
				zap.Error(err))
		} else {
			return fmt.Errorf("failed to validate Pull Secret: [%s in namespace %s], err: %w",
				key.Name, key.Namespace, err)
		}
	}

	// If extraManifests are defined - check that they exist
	if len(clusterInstance.Spec.ExtraManifestsRefs) > 0 {
		for _, extraManifestRef := range clusterInstance.Spec.ExtraManifestsRefs {
			key = types.NamespacedName{Name: extraManifestRef.Name, Namespace: clusterInstance.Namespace}
			cm := &corev1.ConfigMap{}
			if err := c.Get(ctx, key, cm); err != nil {
				return fmt.Errorf("failed to retrieve ExtraManifest: %s in namespace %s, err: %w",
					key.Name, key.Namespace, err)
			}
		}
	}

	// Check that node BMC secrets exist in namespace.
	// Soft-fail applies only to NotFound when strictSecretPresenceValidation is false.
	for _, node := range clusterInstance.Spec.Nodes {
		bmcCredentialNS := clusterInstance.Namespace
		if node.HostRef != nil {
			bmcCredentialNS = node.HostRef.Namespace
		}
		key = types.NamespacedName{Name: node.BmcCredentialsName.Name, Namespace: bmcCredentialNS}
		bmcSecret := &corev1.Secret{}
		if err := c.Get(ctx, key, bmcSecret); err != nil {
			if !strictSecretPresenceValidation && apierrors.IsNotFound(err) {
				log.Warn("BMC credentials Secret not detected; continuing validation",
					zap.String("name", node.BmcCredentialsName.Name),
					zap.String("namespace", bmcCredentialNS),
					zap.String("hostname", node.HostName),
					zap.Error(err))
			} else {
				return fmt.Errorf(
					"failed to validate BMC credentials: %s in namespace %s [Node: Hostname=%s], err: %w",
					node.BmcCredentialsName.Name, bmcCredentialNS, node.HostName, err)
			}
		}
	}

	// validation succeeded
	return nil
}

func validateTemplateRefs(ctx context.Context, c client.Client, clusterInstance *v1alpha1.ClusterInstance) error {

	// Check the cluster-level template references are defined
	if len(clusterInstance.Spec.TemplateRefs) < 1 {
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
		if len(node.TemplateRefs) < 1 {
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

// Validate checks the given ClusterInstance, returns an error if validation fails, returns nil if it succeeds.
// When strictSecretPresenceValidation is true (default), pull secret and BMC credential presence
// checks fail validation as on main today. When false, NotFound emits a warn log and continues;
// other Get errors still fail validation.
func Validate(
	ctx context.Context,
	c client.Client,
	log *zap.Logger,
	clusterInstance *v1alpha1.ClusterInstance,
	strictSecretPresenceValidation bool,
) error {

	if clusterInstance.Spec.ClusterName == "" {
		return fmt.Errorf("missing cluster name")
	}

	if err := validateResources(ctx, c, log, clusterInstance, strictSecretPresenceValidation); err != nil {
		return fmt.Errorf("resource validation failed: %w", err)
	}

	if err := validateTemplateRefs(ctx, c, clusterInstance); err != nil {
		return fmt.Errorf("template reference(s) validation failed: %w", err)
	}

	if err := v1alpha1.ValidateClusterInstance(clusterInstance); err != nil {
		return fmt.Errorf("clusterInstance validation failed: %w", err)
	}

	// validation succeeded
	return nil
}
