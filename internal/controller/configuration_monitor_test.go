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
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stolostron/siteconfig/internal/controller/configuration"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var _ = Describe("Reconcile", func() {
	var (
		c                   client.Client
		r                   *ConfigurationMonitor
		ctx                 = context.Background()
		SiteConfigNamespace = "siteconfig-operator"
		testLogger          = zap.NewNop().Named("Test")
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&corev1.ConfigMap{}).
			Build()

		r = &ConfigurationMonitor{
			Client:      c,
			Scheme:      scheme.Scheme,
			Log:         testLogger,
			ConfigStore: configuration.NewConfigurationStore(configuration.NewDefaultConfiguration()),
		}

		namespace := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: SiteConfigNamespace,
			},
		}
		Expect(c.Create(ctx, namespace)).To(Succeed())
	})

	It("creates the default SiteConfig Configuration ConfigMap", func() {
		cm := &corev1.ConfigMap{}
		key := types.NamespacedName{
			Name:      configuration.SiteConfigOperatorConfigMap,
			Namespace: SiteConfigNamespace,
		}
		Expect(c.Get(ctx, key, cm)).ToNot(Succeed())

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))

		Expect(c.Get(ctx, key, cm)).To(Succeed())
		config, err := configuration.FromMap(cm.Data)
		Expect(err).ToNot(HaveOccurred())
		Expect(reflect.DeepEqual(config, configuration.NewDefaultConfiguration())).To(BeTrue())
	})

	It("updates the SiteConfig Configuration when the ConfigMap changes ", func() {

		key := types.NamespacedName{
			Name:      configuration.SiteConfigOperatorConfigMap,
			Namespace: SiteConfigNamespace,
		}

		// Kick-off a reconcile that should auto-create a default SiteConfig Configuration CM
		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))

		cm := &corev1.ConfigMap{}
		Expect(c.Get(ctx, key, cm)).To(Succeed())
		config, err := configuration.FromMap(cm.Data)
		Expect(err).ToNot(HaveOccurred())
		Expect(reflect.DeepEqual(config, configuration.NewDefaultConfiguration())).To(BeTrue())

		// Update config and verify changes
		updatedConfig := &configuration.Configuration{
			AllowReinstalls:         true,
			MaxConcurrentReconciles: 10,
		}

		// create the configuration CM
		cm.Data = updatedConfig.ToMap()
		Expect(c.Update(ctx, cm)).To(Succeed())

		// Kick-off a reconcile and verify that the configuration object is udpated
		res, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))

		cm1 := &corev1.ConfigMap{}
		Expect(c.Get(ctx, key, cm1)).To(Succeed())
		gotConfig, err := configuration.FromMap(cm1.Data)
		Expect(err).ToNot(HaveOccurred())
		Expect(reflect.DeepEqual(gotConfig, updatedConfig)).To(BeTrue())
	})

	It("recreates the SiteConfig Configuration ConfigMap with default config when deleted", func() {
		initConfig := &configuration.Configuration{
			AllowReinstalls:         true,
			MaxConcurrentReconciles: 10,
		}

		// create the configuration CM
		key := types.NamespacedName{
			Name:      configuration.SiteConfigOperatorConfigMap,
			Namespace: SiteConfigNamespace,
		}
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      key.Name,
				Namespace: key.Namespace,
			},
			Data: initConfig.ToMap(),
		}
		Expect(c.Create(ctx, cm)).To(Succeed())

		// Update the shared configuration store config
		r.ConfigStore.SetConfiguration(initConfig)

		// Kick-off a reconcile and verify that the SiteConfig Configuration CM has not changed
		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))

		Expect(c.Get(ctx, key, cm)).To(Succeed())
		gotConfig, err := configuration.FromMap(cm.Data)
		Expect(err).ToNot(HaveOccurred())
		Expect(reflect.DeepEqual(gotConfig, initConfig)).To(BeTrue())

		// Delete the CM
		Expect(c.Delete(ctx, cm)).To(Succeed())
		Expect(c.Get(ctx, key, cm)).ToNot(Succeed())

		// Verify default config CM is recreated on reconcile
		res, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))

		Expect(c.Get(ctx, key, cm)).To(Succeed())
		config, err := configuration.FromMap(cm.Data)
		Expect(err).ToNot(HaveOccurred())
		Expect(reflect.DeepEqual(config, configuration.NewDefaultConfiguration())).To(BeTrue())
	})
})
