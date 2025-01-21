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

package controller

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntime "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/stolostron/siteconfig/internal/controller/configuration"
)

const (
	// SiteConfigOperatorConfigMap defines the name of the ConfigMap used to manage
	// the SiteConfig Operator's runtime configuration.
	SiteConfigOperatorConfigMap = "siteconfig-operator-configuration"

	// RequeueDelay defines the default requeue delay on transient errors.
	RequeueDelay = 30 * time.Second
)

// ConfigurationMonitor watches for changes to a specific ConfigMap and updates
// the SiteConfig Operator's runtime configuration accordingly.
type ConfigurationMonitor struct {
	client.Client
	Log    *zap.Logger
	Scheme *runtime.Scheme

	// Namespace containing the ConfigMap
	Namespace string

	// Store for runtime configuration
	ConfigStore *configuration.ConfigurationStore
}

//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete

// Reconcile processes events for the SiteConfig Configuration ConfigMap, ensuring the runtime configuration
// remains synchronized.
// Creates a default Configuration ConfigMap if one does not exist.
func (r *ConfigurationMonitor) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {

	log := r.Log.With(
		zap.String("name", req.Name),
		zap.String("namespace", req.Namespace),
	)

	log.Info("Start reconcile")
	defer log.Info("Completed reconcile")

	// Ignore non-SiteConfig configuration ConfigMaps
	if req.Name != SiteConfigOperatorConfigMap {
		log.Sugar().Infof("Ignoring unrelated ConfigMap %s", req.NamespacedName)
		return ctrl.Result{}, nil
	}

	// Retrieve the Configuration data from the ConfigMap
	data, err := GetConfigurationData(ctx, r.Client, req.Namespace)
	if err != nil {
		if errors.IsNotFound(err) {
			// Create the default ConfigMap if it does not exist
			log.Info("SiteConfig configuration ConfigMap not found, creating default configuration")
			if err := CreateDefaultConfigurationConfigMap(ctx, r.Client, req.Namespace); err != nil {
				log.Error("Failed to create default SiteConfig configuration ConfigMap", zap.Error(err))
				return ctrl.Result{RequeueAfter: RequeueDelay}, err
			}
			return ctrl.Result{}, nil
		}
		log.Error("Failed to retrieve SiteConfig configuration ConfigMap", zap.Error(err))
		return ctrl.Result{RequeueAfter: RequeueDelay}, err
	}

	// Update the runtime configuration store
	if err := r.ConfigStore.UpdateConfiguration(data); err != nil {
		log.Error("Failed to update configuration store", zap.Error(err))
		return ctrl.Result{}, fmt.Errorf("failed to update runtime configuration store: %w", err)
	}
	log.Sugar().Infof("Successfully updated runtime configuration: %v", data)

	return ctrl.Result{}, nil
}

// CreateDefaultConfigurationConfigMap creates a ConfigMap with default configuration values in the given namespace.
func CreateDefaultConfigurationConfigMap(ctx context.Context, c client.Client, namespace string) error {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      SiteConfigOperatorConfigMap,
			Namespace: namespace,
		},
		Data: configuration.NewDefaultConfiguration().ToMap(),
	}

	if err := c.Create(ctx, cm); err != nil {
		return fmt.Errorf("failed to create default ConfigMap: %w", err)
	}

	return nil
}

// GetConfigurationData retrieves the data field of the specified ConfigMap.
func GetConfigurationData(ctx context.Context, c client.Client, namespace string) (map[string]string, error) {
	// Fetch the ConfigMap
	cm := &corev1.ConfigMap{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: SiteConfigOperatorConfigMap}, cm); err != nil {
		return nil, fmt.Errorf("failed to fetch ConfigMap %s/%s: %w", namespace, SiteConfigOperatorConfigMap, err)
	}

	return cm.Data, nil
}

// SetupWithManager configures the controller to watch the SiteConfig configuration
// ConfigMap and process relevant events.
func (r *ConfigurationMonitor) SetupWithManager(mgr ctrl.Manager) error {
	if r.Namespace == "" {
		return fmt.Errorf("namespace not defined")
	}

	// isTargetConfigMap checks whether the given name and namespace match the expected
	// SiteConfig configuration ConfigMap.
	isTargetConfigMap := func(name, namespace string) bool {
		if name != SiteConfigOperatorConfigMap || namespace != r.Namespace {
			return false
		}
		return true
	}

	// Predicate to filter events for the target ConfigMap.
	siteConfigCMPredicate := predicate.Funcs{
		GenericFunc: func(e event.GenericEvent) bool { return false },
		CreateFunc: func(e event.CreateEvent) bool {
			return isTargetConfigMap(e.Object.GetName(), e.Object.GetNamespace())
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			// Recreate the ConfigMap if it is deleted
			return isTargetConfigMap(e.Object.GetName(), e.Object.GetNamespace())
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return isTargetConfigMap(e.ObjectNew.GetName(), e.ObjectNew.GetNamespace())
		},
	}

	//nolint:wrapcheck
	return ctrl.NewControllerManagedBy(mgr).
		Named("ConfigurationMonitor").
		For(&corev1.ConfigMap{}).
		WithOptions(ctrlruntime.Options{MaxConcurrentReconciles: 1}).
		WithEventFilter(siteConfigCMPredicate).
		Complete(r)
}
