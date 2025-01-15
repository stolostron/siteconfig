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
		c             client.Client
		r             *ConfigurationMonitor
		ctx           = context.Background()
		testNamespace = "siteconfig-operator"
		testLogger    = zap.NewNop().Named("Test")
		key           = types.NamespacedName{
			Name:      SiteConfigOperatorConfigMap,
			Namespace: testNamespace,
		}
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&corev1.ConfigMap{}).
			Build()

		configStore, err := configuration.NewConfigurationStore(configuration.NewDefaultConfiguration())
		Expect(err).ToNot(HaveOccurred())

		r = &ConfigurationMonitor{
			Client:      c,
			Scheme:      scheme.Scheme,
			Log:         testLogger,
			ConfigStore: configStore,
			Namespace:   testNamespace,
		}
	})

	It("creates the default SiteConfig configuration ConfigMap when not found", func() {
		cm := &corev1.ConfigMap{}

		Expect(c.Get(ctx, key, cm)).ToNot(Succeed())

		// Trigger the creation of the default SiteConfig configuration CM via reconcile
		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))

		// Fetch the configmap and compare the data
		Expect(c.Get(ctx, key, cm)).To(Succeed())
		Expect(cm.Data).To(Equal(configuration.NewDefaultConfiguration().ToMap()))
	})

	It("updates the SiteConfig configuration when the ConfigMap changes", func() {
		// Kick-off a reconcile that should auto-create a default SiteConfig configuration CM
		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))

		cm := &corev1.ConfigMap{}
		// Fetch the SiteConfig configuration ConfigMap and verify the configuration
		Expect(c.Get(ctx, key, cm)).To(Succeed())
		Expect(cm.Data).To(Equal(configuration.NewDefaultConfiguration().ToMap()))

		// Update config and verify changes
		cm.Data["allowReinstalls"] = "true"
		cm.Data["maxConcurrentReconciles"] = "10"
		Expect(c.Update(ctx, cm)).To(Succeed())

		// Trigger a reconcile and verify that the configuration is updated
		res, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))
		cm1 := &corev1.ConfigMap{}
		Expect(c.Get(ctx, key, cm1)).To(Succeed())
		Expect(cm1.Data).To(Equal(cm.Data))
	})

	It("recreates the SiteConfig configuration ConfigMap with default config when deleted", func() {
		// create the configuration CM
		data := map[string]string{
			"allowReinstalls":         "true",
			"maxConcurrentReconciles": "10",
		}
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      key.Name,
				Namespace: key.Namespace,
			},
			Data: data,
		}
		Expect(c.Create(ctx, cm)).To(Succeed())

		// Update the shared configuration store config
		err := r.ConfigStore.UpdateConfiguration(data)
		Expect(err).ToNot(HaveOccurred())

		// Kick-off a reconcile and verify the SiteConfig configuration CM
		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))
		Expect(c.Get(ctx, key, cm)).To(Succeed())
		Expect(cm.Data).To(Equal(data))

		// Delete the CM
		Expect(c.Delete(ctx, cm)).To(Succeed())
		Expect(c.Get(ctx, key, cm)).ToNot(Succeed())

		// Verify default config CM is recreated on reconcile
		res, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))

		Expect(c.Get(ctx, key, cm)).To(Succeed())
		Expect(cm.Data).To(Equal(configuration.NewDefaultConfiguration().ToMap()))
	})
})

var _ = Describe("CreateDefaultConfigurationConfigMap", func() {
	var (
		c             client.Client
		ctx           = context.Background()
		testNamespace = "siteconfig-operator"
		key           = types.NamespacedName{
			Name:      SiteConfigOperatorConfigMap,
			Namespace: testNamespace,
		}
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			Build()
	})

	It("creates default configuration ConfigMap", func() {
		err := CreateDefaultConfigurationConfigMap(ctx, c, testNamespace)
		Expect(err).ToNot(HaveOccurred())

		// Check that the ConfigMap is created with correct data
		cm := &corev1.ConfigMap{}
		Expect(c.Get(ctx, key, cm)).To(Succeed())
		Expect(err).ToNot(HaveOccurred())
		Expect(cm.Data).To(Equal(configuration.NewDefaultConfiguration().ToMap()))
	})

	It("returns an error if SiteConfig Operator configuration ConfigMap cannot be created", func() {
		// Simulate an error by pre-creating the ConfigMap
		// create the configuration CM
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      key.Name,
				Namespace: key.Namespace,
			},
			Data: configuration.NewDefaultConfiguration().ToMap(),
		}
		Expect(c.Create(ctx, cm)).To(Succeed())

		err := CreateDefaultConfigurationConfigMap(ctx, c, testNamespace)
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("GetConfigurationData", func() {
	var (
		ctx           = context.Background()
		c             client.Client
		testNamespace = "siteconfig-operator"
		key           = types.NamespacedName{
			Name:      SiteConfigOperatorConfigMap,
			Namespace: testNamespace,
		}
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			Build()
	})

	It("returns the configuration data when the SiteConfig Operator Configuration ConfigMap exists", func() {
		// create the configuration CM
		data := map[string]string{
			"allowReinstalls":         "true",
			"maxConcurrentReconciles": "10",
		}
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      key.Name,
				Namespace: key.Namespace,
			},
			Data: data,
		}
		Expect(c.Create(ctx, cm)).To(Succeed())

		got, err := GetConfigurationData(ctx, c, testNamespace)
		Expect(err).ToNot(HaveOccurred())
		Expect(got).To(Equal(data))
	})

	It("errors when the SiteConfig Operator Configuration ConfigMap is not found", func() {
		_, err := GetConfigurationData(ctx, c, testNamespace)
		Expect(err).To(HaveOccurred())
	})
})
