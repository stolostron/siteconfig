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

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"os"

	"go.uber.org/zap"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	k8sretry "k8s.io/client-go/util/retry"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	ctrlruntimezap "sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	bmh_v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"

	"github.com/openshift/assisted-service/api/v1beta1"

	hivev1 "github.com/openshift/hive/apis/hive/v1"

	clusterv1 "open-cluster-management.io/api/cluster/v1"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	"github.com/stolostron/siteconfig/internal/controller"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
	"github.com/stolostron/siteconfig/internal/controller/configuration"
	"github.com/stolostron/siteconfig/internal/controller/deletion"
	"github.com/stolostron/siteconfig/internal/controller/reinstall"
	"github.com/stolostron/siteconfig/internal/controller/retry"
	ai_templates "github.com/stolostron/siteconfig/internal/templates/assisted-installer"
	ibi_templates "github.com/stolostron/siteconfig/internal/templates/image-based-installer"
	//+kubebuilder:scaffold:imports
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(v1alpha1.AddToScheme(scheme))
	utilruntime.Must(hivev1.AddToScheme(scheme))
	utilruntime.Must(v1beta1.AddToScheme(scheme))
	utilruntime.Must(clusterv1.AddToScheme(scheme))
	utilruntime.Must(bmh_v1alpha1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var metricsCertDir string
	var clusterInstanceWebhookCertDir string
	var enableLeaderElection bool
	var probeAddr string
	var enableHTTP2 bool

	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&metricsCertDir, "metrics-tls-cert-dir", "", "The directory containing the tls.crt and tls.key.")
	flag.StringVar(&clusterInstanceWebhookCertDir, "clusterinstance-webhook-tls-cert-dir", "",
		"The directory containing the ClusterInstance webhook certs: tls.crt and tls.key.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	opts := ctrlruntimezap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(ctrlruntimezap.New(ctrlruntimezap.UseFlagOptions(&opts)))

	siteconfigLogger := zap.Must(zap.NewDevelopment())
	defer siteconfigLogger.Sync() //nolint:errcheck

	setupLog := siteconfigLogger.Named("SiteConfigSetup")

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	tlsOpts := []func(*tls.Config){}
	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	cfg := ctrl.GetConfigOrDie()
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			SecureServing:  metricsCertDir != "",
			CertDir:        metricsCertDir,
			BindAddress:    metricsAddr,
			TLSOpts:        tlsOpts,
			FilterProvider: filters.WithAuthenticationAndAuthorization,
		},
		HealthProbeBindAddress: probeAddr,
		WebhookServer: webhook.NewServer(webhook.Options{
			Port:    9443,
			CertDir: clusterInstanceWebhookCertDir,
			TLSOpts: tlsOpts,
		}),
		LeaderElection:   enableLeaderElection,
		LeaderElectionID: "manager." + v1alpha1.Group,
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error("Unable to start manager", zap.Error(err))
		os.Exit(1) // nolint:gocritic
	}

	// Check that the SiteConfig namespace value is defined
	siteConfigNamespace := getSiteConfigNamespace(setupLog)
	if siteConfigNamespace == "" {
		setupLog.Error("Unable to retrieve the SiteConfig Operator namespace")
		os.Exit(1)
	}

	// Create an uncached client to initialize ConfigMaps
	tmpClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		setupLog.Error("Failed to create temporary client", zap.Error(err))
		os.Exit(1)
	}

	// Initialize the default install template ConfigMaps
	if err := initConfigMapTemplates(context.TODO(), tmpClient, siteConfigNamespace, setupLog); err != nil {
		setupLog.Error("Unable to initialize the default reference installation template ConfigMaps",
			zap.Error(err))
		os.Exit(1)
	}

	// Initialize the SiteConfig Operator configuration store
	sharedConfigStore, err := createConfigurationStore(context.TODO(), tmpClient, siteConfigNamespace, setupLog)
	if err != nil {
		setupLog.Error("Failed to initialize the ConfigurationStore for the SiteConfig Operator")
		os.Exit(1)
	}

	// Create configuration monitor controller to track SiteConfig Operator configuration change(s)
	if err := (&controller.ConfigurationMonitor{
		Client:      mgr.GetClient(),
		Log:         siteconfigLogger.Named("ConfigurationMonitor"),
		Scheme:      mgr.GetScheme(),
		Namespace:   siteConfigNamespace,
		ConfigStore: sharedConfigStore,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error("Unable to create controller",
			zap.String("controller", "ConfigurationMonitor"),
			zap.Error(err))
		os.Exit(1)
	}

	// Create DeletionHandler for graceful deletion of rendered objects
	deletionHandler := &deletion.DeletionHandler{
		Client: mgr.GetClient(),
		Logger: siteconfigLogger.Named("DeletionHandler"),
	}

	// Create ReinstallHandler
	reinstallHandler := &reinstall.ReinstallHandler{
		Client:          mgr.GetClient(),
		Logger:          siteconfigLogger.Named("ReinstallHandler"),
		ConfigStore:     sharedConfigStore,
		DeletionHandler: deletionHandler,
	}

	// Create ClusterInstance controller for reconciling ClusterInstance CRs
	clusterInstanceLogger := siteconfigLogger.Named("ClusterInstanceController")
	if err := (&controller.ClusterInstanceReconciler{
		Client:           mgr.GetClient(),
		Scheme:           mgr.GetScheme(),
		Recorder:         mgr.GetEventRecorderFor("ClusterInstanceController"),
		Log:              clusterInstanceLogger,
		TmplEngine:       ci.NewTemplateEngine(),
		ConfigStore:      sharedConfigStore,
		DeletionHandler:  deletionHandler,
		ReinstallHandler: reinstallHandler,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error("Unable to create controller",
			zap.String("controller", "ClusterInstance"),
			zap.Error(err),
		)
		os.Exit(1)
	}

	// Create ClusterDeployment controller for monitoring cluster provisioning progress
	if err := (&controller.ClusterDeploymentReconciler{
		Client: mgr.GetClient(),
		Log:    siteconfigLogger.Named("ClusterDeploymentReconciler"),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error("Unable to create controller",
			zap.String("controller", "ClusterDeploymentReconciler"),
			zap.Error(err))
		os.Exit(1)
	}

	// Create ClusterInstance validating admission webhook
	if err = (&v1alpha1.ClusterInstance{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error("Unable to create webhook", zap.String("webhook", "ClusterInstance"), zap.Error(err))
		os.Exit(1)
	}
	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error("Unable to set up health check", zap.Error(err))
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error("Unable to set up ready check", zap.Error(err))
		os.Exit(1)
	}

	setupLog.Info("Starting SiteConfig manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error("Encountered an error starting SiteConfig manager", zap.Error(err))
		os.Exit(1)
	}
}

// getSiteConfigNamespace retrieves the namespace where the SiteConfig Operator is running.
// It reads the namespace from the POD_NAMESPACE environment variable.
// If the environment variable is not set, the function logs a warning and returns an empty string.
func getSiteConfigNamespace(log *zap.Logger) string {
	namespace := os.Getenv("POD_NAMESPACE")
	if namespace == "" {
		log.Warn("POD_NAMESPACE environment variable is not defined")
	}
	return namespace
}

// initConfigMapTemplates initializes default ConfigMaps consisting of the Assisted Installer and Image-based Installer
// installation templates in the specified namespace.
func initConfigMapTemplates(ctx context.Context, c client.Client, namespace string, log *zap.Logger) error {
	templates := make(map[string]map[string]string, 4)
	templates[ai_templates.ClusterLevelInstallTemplates] = ai_templates.GetClusterTemplates()
	templates[ai_templates.NodeLevelInstallTemplates] = ai_templates.GetNodeTemplates()
	templates[ibi_templates.ClusterLevelInstallTemplates] = ibi_templates.GetClusterTemplates()
	templates[ibi_templates.NodeLevelInstallTemplates] = ibi_templates.GetNodeTemplates()

	for k, v := range templates {
		immutable := true
		configMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      k,
				Namespace: namespace,
			},
			Immutable: &immutable,
			Data:      v,
		}

		if err := retry.RetryOnConflictOrRetriable(k8sretry.DefaultBackoff, func() error {
			if err := client.IgnoreAlreadyExists(c.Create(ctx, configMap)); err != nil {
				return fmt.Errorf("failed to create ConfigMap: %w", err)
			}
			return nil
		}); err != nil {
			return fmt.Errorf(
				"failed to create default reference installation template ConfigMap %s/%s, error: %w",
				namespace, k, err)
		}

		log.Sugar().Infof("Created default reference installation template ConfigMap %s/%s", namespace, k)
	}

	return nil
}

// createConfigurationStore initializes and returns a ConfigurationStore instance for the SiteConfig Operator.
// This function ensures that the necessary configuration ConfigMap exists in the given namespace and
// retrieves its data. If the ConfigMap does not exist, a default configuration is created.
func createConfigurationStore(
	ctx context.Context,
	client client.Client,
	namespace string,
	log *zap.Logger,
) (*configuration.ConfigurationStore, error) {
	// Attempt to initialize the SiteConfig Operator configuration
	data, err := initializeConfiguration(ctx, client, namespace, log)
	if err != nil {
		log.Error("Failed to initialize the SiteConfig Operator configuration", zap.Error(err))
		return nil, fmt.Errorf("failed to initialize configuration: %w", err)
	}

	if len(data) == 0 {
		log.Error("SiteConfig Operator configuration data is empty")
		return nil, fmt.Errorf("configuration data is empty")
	}

	// Parse configuration from the retrieved data
	config := &configuration.Configuration{}
	if err := config.FromMap(data); err != nil {
		log.Error("Failed to parse SiteConfig Operator configuration", zap.Error(err))
		return nil, fmt.Errorf("failed to parse SiteConfig Operator configuration: %w", err)
	}

	// Return the new configuration store, wrap the error if it occurs
	store, err := configuration.NewConfigurationStore(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create configuration store: %w", err)
	}

	return store, nil
}

// initializeConfiguration ensures that the SiteConfig Operator configuration ConfigMap exists.
// If the ConfigMap does not exist, it creates it with default values and returns those defaults.
// If the ConfigMap exists, it retrieves and returns its data.
func initializeConfiguration(
	ctx context.Context,
	client client.Client,
	namespace string,
	log *zap.Logger,
) (data map[string]string, err error) {
	// Retry logic for handling transient errors or resource conflicts
	if err = retry.RetryOnConflictOrRetriable(k8sretry.DefaultBackoff, func() error {
		// Attempt to retrieve the ConfigMap
		data, err = controller.GetConfigurationData(ctx, client, namespace)
		if err == nil {
			return nil // Successfully retrieved ConfigMap data
		}

		// If ConfigMap is missing, create it with default values
		if errors.IsNotFound(err) {
			log.Info("SiteConfig configuration ConfigMap not found; creating one with default values")
			if createErr := controller.CreateDefaultConfigurationConfigMap(ctx, client, namespace); createErr != nil {
				log.Error("Failed to create default configuration ConfigMap", zap.Error(createErr))
				return fmt.Errorf("failed to create default configuration ConfigMap: %w", createErr) // Retry if creation fails
			}
			// Use default configuration if creation succeeds
			data = configuration.NewDefaultConfiguration().ToMap()
			return nil
		}

		// Log and return other errors for retry
		log.Error("Failed to retrieve configuration ConfigMap", zap.Error(err))
		return fmt.Errorf("failed to retrieve configuration ConfigMap: %w", err)
	}); err != nil {
		log.Error("Error during configuration initialization", zap.Error(err))
	}
	return
}
