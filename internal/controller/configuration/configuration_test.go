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

package configuration

import (
	"context"
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"go.uber.org/zap"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var _ = Describe("NewDefaultConfiguration", func() {
	It("creates a new Configuration object with default values for the SiteConfig Operator", func() {
		expected := &Configuration{
			AllowReinstalls:         DefaultAllowReinstalls,
			MaxConcurrentReconciles: DefaultMaxConcurrentReconciles,
		}
		gotConfig := NewDefaultConfiguration()
		Expect(reflect.DeepEqual(gotConfig, expected)).To(BeTrue())
	})
})

var _ = Describe("CreateDefaultConfigurationConfigMap", func() {
	var (
		c                   client.Client
		ctx                 = context.Background()
		testLogger          = zap.NewNop().Named("Test")
		siteConfigNamespace string
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			Build()

		siteConfigNamespace = GetPodNamespace(testLogger)
	})

	It("creates default configuration ConfigMap when it does not exist", func() {
		config, err := CreateDefaultConfigurationConfigMap(ctx, c, siteConfigNamespace)
		Expect(err).ToNot(HaveOccurred())
		Expect(reflect.DeepEqual(config, NewDefaultConfiguration()))

		// Check that the ConfigMap is created with correct data
		key := types.NamespacedName{
			Name:      SiteConfigOperatorConfigMap,
			Namespace: siteConfigNamespace,
		}
		cm := &corev1.ConfigMap{}
		Expect(c.Get(ctx, key, cm)).To(Succeed())

		cmConfig, err := FromMap(cm.Data)
		Expect(err).ToNot(HaveOccurred())
		Expect(reflect.DeepEqual(cmConfig, config)).To(BeTrue())
	})

	It("does not create the SiteConfig Operator configuration ConfigMap if it exists", func() {
		config := &Configuration{
			AllowReinstalls:         true,
			MaxConcurrentReconciles: 10,
		}

		// create the configuration CM
		key := types.NamespacedName{
			Name:      SiteConfigOperatorConfigMap,
			Namespace: siteConfigNamespace,
		}
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      key.Name,
				Namespace: key.Namespace,
			},
			Data: config.ToMap(),
		}
		Expect(c.Create(ctx, cm)).To(Succeed())

		got, err := CreateDefaultConfigurationConfigMap(ctx, c, siteConfigNamespace)
		Expect(err).ToNot(HaveOccurred())
		Expect(reflect.DeepEqual(got, config)).To(BeTrue())
	})
})

var _ = Describe("LoadFromConfigMap", func() {
	var (
		ctx                 = context.Background()
		c                   client.Client
		siteConfigNamespace string
		testLogger          = zap.NewNop().Named("Test")
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			Build()

		siteConfigNamespace = GetPodNamespace(testLogger)
	})

	It("returns a Configuration object from the SiteConfig Operator Configuration ConfigMap that is correctly defined", func() {
		config := &Configuration{
			AllowReinstalls:         true,
			MaxConcurrentReconciles: 10,
		}

		// create the configuration CM
		key := types.NamespacedName{
			Name:      SiteConfigOperatorConfigMap,
			Namespace: siteConfigNamespace,
		}
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      key.Name,
				Namespace: key.Namespace,
			},
			Data: config.ToMap(),
		}
		Expect(c.Create(ctx, cm)).To(Succeed())

		got, err := LoadFromConfigMap(ctx, c, siteConfigNamespace)
		Expect(err).ToNot(HaveOccurred())
		Expect(reflect.DeepEqual(got, config)).To(BeTrue())
	})

	It("errors when the SiteConfig Operator Configuration ConfigMap is not found", func() {
		got, err := LoadFromConfigMap(ctx, c, siteConfigNamespace)
		Expect(err).To(HaveOccurred())
		Expect(got).To(BeNil())
	})

	It("errors when the SiteConfig Operator Configuration ConfigMap is not defined properly", func() {
		// create the configuration CM
		key := types.NamespacedName{
			Name:      SiteConfigOperatorConfigMap,
			Namespace: siteConfigNamespace,
		}
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      key.Name,
				Namespace: key.Namespace,
			},
			Data: map[string]string{
				"allowReinstalls":         "foobar",
				"maxConcurrentReconciles": "10",
			},
		}
		Expect(c.Create(ctx, cm)).To(Succeed())

		got, err := LoadFromConfigMap(ctx, c, siteConfigNamespace)
		Expect(err).To(HaveOccurred())
		Expect(got).To(BeNil())
	})
})

var _ = Describe("Configuration.ToMap", func() {
	It("correctly converts Configuration objects to maps", func() {

		testConfigs := []Configuration{
			{
				AllowReinstalls:         false,
				MaxConcurrentReconciles: 5,
			},
			{
				AllowReinstalls:         true,
				MaxConcurrentReconciles: 1,
			},
		}
		expectedMaps := []map[string]string{
			{
				"allowReinstalls":         "false",
				"maxConcurrentReconciles": "5",
			},
			{
				"allowReinstalls":         "true",
				"maxConcurrentReconciles": "1",
			},
		}

		for index, config := range testConfigs {
			got := config.ToMap()
			Expect(got).To(Equal(expectedMaps[index]))
		}
	})
})

var _ = Describe("FromMap", func() {
	It("correctly converts maps to Configuration objects", func() {
		testMaps := []map[string]string{
			{
				"allowReinstalls":         "false",
				"maxConcurrentReconciles": "5",
			},
			{
				"allowReinstalls":         "true",
				"maxConcurrentReconciles": "1",
			},
		}

		expectedConfigs := []*Configuration{
			{
				AllowReinstalls:         false,
				MaxConcurrentReconciles: 5,
			},
			{
				AllowReinstalls:         true,
				MaxConcurrentReconciles: 1,
			},
		}

		for index, tMap := range testMaps {
			got, err := FromMap(tMap)
			Expect(err).ToNot(HaveOccurred())
			Expect(got).To(Equal(expectedConfigs[index]))
		}
	})

	It("errors when it fails to convert maps to Configuration objects", func() {
		testMaps := []map[string]string{
			{
				"allowReinstalls":         "foobar",
				"maxConcurrentReconciles": "5",
			},
			{
				"allowReinstalls":         "true",
				"maxConcurrentReconciles": "ONE",
			},
		}

		for _, tMap := range testMaps {
			got, err := FromMap(tMap)
			Expect(err).To(HaveOccurred())
			Expect(got).To(BeNil())
		}
	})

	It("errors when unknown fields are found while converting maps to Configuration objects", func() {
		testMaps := []map[string]string{
			{
				"allowReinstall$":         "true",
				"maxConcurrentReconciles": "1",
			},
			{
				"allowReinstalls":         "false",
				"maxConcurrentReconciles": "10",
				"foo":                     "bar",
			},
		}

		for _, tMap := range testMaps {
			got, err := FromMap(tMap)
			Expect(err).To(HaveOccurred())
			Expect(got).To(BeNil())
		}
	})
})
