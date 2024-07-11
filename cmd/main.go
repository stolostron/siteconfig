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
	"flag"
	"fmt"
	"os"

	"github.com/go-logr/logr"
	bmh_v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	"github.com/openshift/assisted-service/api/v1beta1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sretry "k8s.io/client-go/util/retry"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
	"github.com/stolostron/siteconfig/internal/controller/retry"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	"github.com/stolostron/siteconfig/internal/controller"
	assistedinstaller "github.com/stolostron/siteconfig/internal/templates/assisted-installer"
	imagebasedinstall "github.com/stolostron/siteconfig/internal/templates/image-based-install"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

const (
	AssistedInstallerClusterTemplates = "ai-cluster-templates-v1"
	AssistedInstallerNodeTemplates    = "ai-node-templates-v1"
	ImageBasedInstallClusterTemplates = "ibi-cluster-templates-v1"
	ImageBasedInstallNodeTemplates    = "ibi-node-templates-v1"
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
	var enableLeaderElection bool
	var probeAddr string
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: server.Options{
			BindAddress: metricsAddr,
		},
		//MetricsBindAddress:     metricsAddr,
		//Port:                   9443,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "manager." + v1alpha1.Group,
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
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Check that the SiteConfig namespace value is defined
	if getSiteConfigNamespace(setupLog) == "" {
		setupLog.Info("unable to retrieve SiteConfig namespace")
		os.Exit(1)
	}

	if err := initConfigMapTemplates(context.TODO(), mgr.GetClient(), setupLog); err != nil {
		setupLog.Error(err, "unable to initialize default reference ConfigMap templates")
		os.Exit(1)
	}

	log := ctrl.Log.WithName("controllers").WithName("ClusterInstance")
	if err = (&controller.ClusterInstanceReconciler{
		Client:     mgr.GetClient(),
		Scheme:     mgr.GetScheme(),
		Recorder:   mgr.GetEventRecorderFor("ClusterInstance-controller"),
		Log:        log,
		TmplEngine: ci.NewTemplateEngine(log.WithName("ClusterInstance.TemplateEngine")),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ClusterInstance")
		os.Exit(1)
	}

	if err = (&controller.ClusterDeploymentReconciler{
		Client: mgr.GetClient(),
		Log:    ctrl.Log.WithName("controllers").WithName("ClusterDeploymentReconciler"),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ClusterDeploymentReconciler")
		os.Exit(1)
	}
	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func getSiteConfigNamespace(log logr.Logger) string {
	namespace := os.Getenv("POD_NAMESPACE")
	if namespace == "" {
		log.Info("POD_NAMESPACE environment variable is not defined")
	}
	return namespace
}

func initConfigMapTemplates(ctx context.Context, c client.Client, log logr.Logger) error {
	templates := make(map[string]map[string]string, 4)
	templates[AssistedInstallerClusterTemplates] = assistedinstaller.GetClusterTemplates()
	templates[AssistedInstallerNodeTemplates] = assistedinstaller.GetNodeTemplates()
	templates[ImageBasedInstallClusterTemplates] = imagebasedinstall.GetClusterTemplates()
	templates[ImageBasedInstallNodeTemplates] = imagebasedinstall.GetNodeTemplates()

	siteConfigNamespace := getSiteConfigNamespace(log)

	for k, v := range templates {
		immutable := true
		configMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      k,
				Namespace: siteConfigNamespace,
			},
			Immutable: &immutable,
			Data:      v,
		}

		if err := retry.RetryOnConflictOrRetriable(k8sretry.DefaultBackoff, func() error {
			return client.IgnoreAlreadyExists(c.Create(ctx, configMap))
		}); err != nil {
			return fmt.Errorf("failed to create default reference template ConfigMap %s/%s during init, error: %w", siteConfigNamespace, k, err)
		}

		log.Info(fmt.Sprintf("created default reference template ConfigMap %s/%s", siteConfigNamespace, k))
	}

	return nil
}
