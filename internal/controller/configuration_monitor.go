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
	"time"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlruntime "sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/stolostron/siteconfig/internal/controller/configuration"
)

// ConfigurationMonitor reconciles a ConfigMap object to
// update the SiteConfig Operator controller parameters
type ConfigurationMonitor struct {
	client.Client
	Log         *zap.Logger
	Scheme      *runtime.Scheme
	ConfigStore *configuration.ConfigurationStore
}

//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete

func (r *ConfigurationMonitor) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {

	log := r.Log.With(
		zap.String("name", req.Name),
		zap.String("namespace", req.Namespace),
	)

	log.Info("Start reconcile")
	defer log.Info("Completed reconcile")

	if req.Name != configuration.SiteConfigOperatorConfigMap {
		log.Sugar().Infof("Ignoring ConfigMap %s", req.NamespacedName)
		return ctrl.Result{}, nil
	}

	// Fetch the configuration from ConfigMap
	config, err := configuration.LoadFromConfigMap(ctx, r.Client, req.Namespace)
	if err != nil {
		if errors.IsNotFound(err) {
			_, err = configuration.CreateDefaultConfigurationConfigMap(ctx, r.Client, req.Namespace)
			return requeueWithError(err)
		}
		log.Error("Failed to get SiteConfig Configuration", zap.Error(err))

		// This is likely a case where the API is down, so requeue and try again shortly
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}

	log.Sugar().Infof("Loaded SiteConfig Configuration %s", req.NamespacedName)

	// Update configuration
	r.ConfigStore.SetConfiguration(config)
	log.Sugar().Infof("Updated SiteConfig Configuration with %v", config)

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ConfigurationMonitor) SetupWithManager(mgr ctrl.Manager) error {
	filterFn := func(name, namespace string) bool {
		if name != configuration.SiteConfigOperatorConfigMap || namespace != configuration.GetPodNamespace(r.Log) {
			return false
		}
		return true
	}

	// Predicate that checks for the SiteConfig Configuration ConfigMap
	siteConfigCMPredicate := predicate.Funcs{
		GenericFunc: func(e event.GenericEvent) bool { return false },
		CreateFunc: func(e event.CreateEvent) bool {
			return filterFn(e.Object.GetName(), e.Object.GetNamespace())
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			// recreate the SiteConfig configuration ConfigMap with default values on deletion
			return filterFn(e.Object.GetName(), e.Object.GetNamespace())
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			return filterFn(e.ObjectNew.GetName(), e.ObjectNew.GetNamespace())
		},
	}

	return ctrl.NewControllerManagedBy(mgr).
		Named("ConfigurationMonitor").
		For(&corev1.ConfigMap{}).
		WithOptions(ctrlruntime.Options{MaxConcurrentReconciles: 1}).
		WithEventFilter(siteConfigCMPredicate).
		Complete(r)
}
