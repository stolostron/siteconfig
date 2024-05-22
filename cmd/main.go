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
	"github.com/sakhoury/siteconfig/internal/controller/retry"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sretry "k8s.io/client-go/util/retry"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	siteconfigv1alpha1 "github.com/sakhoury/siteconfig/api/v1alpha1"
	"github.com/sakhoury/siteconfig/internal/controller"
	assistedinstaller "github.com/sakhoury/siteconfig/internal/templates/assisted-installer"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

const (
	SiteConfigNamespace               = "siteconfig-system"
	AssistedInstallerClusterTemplates = "ai-cluster-templates-v1"
	AssistedInstallerNodeTemplates    = "ai-node-templates-v1"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(siteconfigv1alpha1.AddToScheme(scheme))
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
		LeaderElectionID:       "siteconfig.metaclusterinstall.openshift.io",
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

	log := ctrl.Log.WithName("controllers").WithName("SiteConfig")

	if err := initConfigMapTemplates(context.TODO(), mgr.GetClient(), setupLog); err != nil {
		setupLog.Error(err, "unable to initialize default reference ConfigMap templates")
		os.Exit(1)
	}

	if err = (&controller.SiteConfigReconciler{
		Client:    mgr.GetClient(),
		Scheme:    mgr.GetScheme(),
		Recorder:  mgr.GetEventRecorderFor("siteconfig-controller"),
		Log:       log,
		ScBuilder: controller.NewSiteConfigBuilder(log.WithName("SiteConfigBuilder")),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "SiteConfig")
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

func initConfigMapTemplates(ctx context.Context, c client.Client, log logr.Logger) error {
	templates := make(map[string]map[string]string, 2)
	templates[AssistedInstallerClusterTemplates] = assistedinstaller.GetClusterTemplates()
	templates[AssistedInstallerNodeTemplates] = assistedinstaller.GetNodeTemplates()

	for k, v := range templates {
		immutable := true
		configMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      k,
				Namespace: SiteConfigNamespace,
			},
			Immutable: &immutable,
			Data:      v,
		}

		if err := retry.RetryOnConflictOrRetriable(k8sretry.DefaultBackoff, func() error {
			return client.IgnoreAlreadyExists(c.Create(ctx, configMap))
		}); err != nil {
			return fmt.Errorf("failed to create default reference template ConfigMap %s/%s during init, error: %w", SiteConfigNamespace, k, err)
		}

		log.Info("created default reference template ConfigMap %s/%s", SiteConfigNamespace, k)
	}

	return nil
}
