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
	"testing"

	"go.uber.org/zap"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stolostron/siteconfig/internal/controller"
	"github.com/stolostron/siteconfig/internal/controller/configuration"
	ai_templates "github.com/stolostron/siteconfig/internal/templates/assisted-installer"
	ibi_templates "github.com/stolostron/siteconfig/internal/templates/image-based-installer"
)

func TestMain(t *testing.T) {
	RegisterFailHandler(Fail)
	t.Setenv("POD_NAMESPACE", "siteconfig-operator")
	RunSpecs(t, "Main Suite")
}

var _ = Describe("initConfigMapTemplates", func() {
	var (
		c             client.Client
		ctx           = context.Background()
		testLogger    = zap.NewNop().Named("Test")
		testNamespace = getSiteConfigNamespace(testLogger)
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme).
			Build()

		siteConfigNS := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: testNamespace,
			},
		}
		Expect(c.Create(ctx, siteConfigNS)).To(Succeed())
	})

	It("should create default assisted install and image based install cluster templates on initialization", func() {
		err := initConfigMapTemplates(ctx, c, testNamespace, testLogger)
		Expect(err).ToNot(HaveOccurred())

		cm := &corev1.ConfigMap{}
		key := types.NamespacedName{
			Name:      ai_templates.ClusterLevelInstallTemplates,
			Namespace: testNamespace,
		}
		Expect(c.Get(ctx, key, cm)).To(Succeed())

		cm = &corev1.ConfigMap{}
		key = types.NamespacedName{
			Name:      ibi_templates.ClusterLevelInstallTemplates,
			Namespace: testNamespace,
		}
		Expect(c.Get(ctx, key, cm)).To(Succeed())
	})

	It("should create default assisted install and image based install node templates on initialization", func() {
		err := initConfigMapTemplates(ctx, c, testNamespace, testLogger)
		Expect(err).ToNot(HaveOccurred())

		cm := &corev1.ConfigMap{}
		key := types.NamespacedName{
			Name:      ai_templates.NodeLevelInstallTemplates,
			Namespace: testNamespace,
		}
		Expect(c.Get(ctx, key, cm)).To(Succeed())

		cm = &corev1.ConfigMap{}
		key = types.NamespacedName{
			Name:      ibi_templates.NodeLevelInstallTemplates,
			Namespace: testNamespace,
		}
		Expect(c.Get(ctx, key, cm)).To(Succeed())
	})

	It("should not throw an error if a ConfigMap template already exists", func() {
		data := map[string]string{"test": "foobar"}
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ai_templates.NodeLevelInstallTemplates,
				Namespace: testNamespace,
			},
			Data: data,
		}
		Expect(c.Create(ctx, cm)).To(Succeed())

		key := types.NamespacedName{
			Name:      ai_templates.NodeLevelInstallTemplates,
			Namespace: testNamespace,
		}

		err := initConfigMapTemplates(ctx, c, testNamespace, testLogger)
		Expect(err).ToNot(HaveOccurred())

		// Verify that the existing ConfigMap is over-written
		aiNodeCM := &corev1.ConfigMap{}
		Expect(c.Get(ctx, key, aiNodeCM)).To(Succeed())
		Expect(aiNodeCM.Data).ToNot(Equal(data))
	})

	It("creates a default SiteConfig Operator configuration ConfigMap on initialization if not created previously", func() {
		_, err := createConfigurationStore(ctx, c, testNamespace, testLogger)
		// Given that a configuration CM has not been created, the initConfig is expected to be the default configuration
		Expect(err).ToNot(HaveOccurred())

		// retrieve the configuration CM and verify that the data is correctly set to the default configuration
		data, err := controller.GetConfigurationData(ctx, c, testNamespace)
		Expect(err).ToNot(HaveOccurred())

		expected := configuration.NewDefaultConfiguration().ToMap()
		Expect(data).To(Equal(expected))
	})

	It("uses an existing SiteConfig Operator configuration ConfigMap on initialization", func() {
		data := map[string]string{
			"allowReinstalls":         "true",
			"maxConcurrentReconciles": "10",
		}
		// create the configuration CM
		key := types.NamespacedName{
			Name:      controller.SiteConfigOperatorConfigMap,
			Namespace: testNamespace,
		}
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      key.Name,
				Namespace: key.Namespace,
			},
			Data: data,
		}
		Expect(c.Create(ctx, cm)).To(Succeed())

		// retrieve the configuration CM and verify that the data is correctly set
		configStore, err := createConfigurationStore(ctx, c, testNamespace, testLogger)
		Expect(err).ToNot(HaveOccurred())

		Expect(configStore.GetAllowReinstalls()).To(BeTrue())
		Expect(configStore.GetMaxConcurrentReconciles()).To(Equal(10))
	})

	It("exits when encounters an error with the configuration object", func() {
		// create the configuration CM
		key := types.NamespacedName{
			Name:      controller.SiteConfigOperatorConfigMap,
			Namespace: testNamespace,
		}
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      key.Name,
				Namespace: key.Namespace,
			},
			Data: map[string]string{
				"allowReinstalls":         "foobar",
				"maxConcurrentReconciles": "1",
			},
		}
		Expect(c.Create(ctx, cm)).To(Succeed())

		// retrieve the configuration CM
		_, err := createConfigurationStore(ctx, c, testNamespace, testLogger)
		// Given that a configuration CM has not been created, the initConfig is expected to be the default configuration
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("deleteConfigMap", func() {
	var (
		c             client.Client
		ctx           = context.Background()
		testLogger    = zap.NewNop().Named("Test")
		testNamespace = getSiteConfigNamespace(testLogger)
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme).
			Build()

		siteConfigNS := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: testNamespace,
			},
		}
		Expect(c.Create(ctx, siteConfigNS)).To(Succeed())
	})

	It("should delete the ConfigMap if it exists", func() {

		// create test ConfigMap
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: testNamespace,
			},
			Data: map[string]string{
				"foo": "bar",
			},
		}
		key := client.ObjectKeyFromObject(cm)
		Expect(c.Create(ctx, cm)).To(Succeed())

		Expect(c.Get(ctx, key, cm)).To(Succeed())

		// Delete the ConfigMap
		Expect(deleteConfigMap(ctx, c, key.Namespace, key.Name, testLogger)).To(BeNil())

		// Verify that the ConfigMap is deleted
		Expect(c.Get(ctx, key, cm)).ToNot(Succeed())
	})

	It("should return nil if the ConfigMap does not exist", func() {
		Expect(deleteConfigMap(ctx, c, testNamespace, "foobar", testLogger)).To(BeNil())
	})
})
