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
		c                   client.Client
		ctx                 = context.Background()
		testLogger          = zap.NewNop().Named("Test")
		SiteConfigNamespace = getSiteConfigNamespace(testLogger)
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme).
			Build()

		siteConfigNS := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: SiteConfigNamespace,
			},
		}
		Expect(c.Create(ctx, siteConfigNS)).To(Succeed())
	})

	It("should create default assisted install and image based install cluster templates on initialization", func() {
		err := initConfigMapTemplates(ctx, c, testLogger)
		Expect(err).ToNot(HaveOccurred())

		cm := &corev1.ConfigMap{}
		key := types.NamespacedName{
			Name:      ai_templates.ClusterLevelInstallTemplates,
			Namespace: SiteConfigNamespace,
		}
		Expect(c.Get(ctx, key, cm)).To(Succeed())

		cm = &corev1.ConfigMap{}
		key = types.NamespacedName{
			Name:      ibi_templates.ClusterLevelInstallTemplates,
			Namespace: SiteConfigNamespace,
		}
		Expect(c.Get(ctx, key, cm)).To(Succeed())
	})

	It("should create default assisted install and image based install node templates on initialization", func() {
		err := initConfigMapTemplates(ctx, c, testLogger)
		Expect(err).ToNot(HaveOccurred())

		cm := &corev1.ConfigMap{}
		key := types.NamespacedName{
			Name:      ai_templates.NodeLevelInstallTemplates,
			Namespace: SiteConfigNamespace,
		}
		Expect(c.Get(ctx, key, cm)).To(Succeed())

		cm = &corev1.ConfigMap{}
		key = types.NamespacedName{
			Name:      ibi_templates.NodeLevelInstallTemplates,
			Namespace: SiteConfigNamespace,
		}
		Expect(c.Get(ctx, key, cm)).To(Succeed())
	})

	It("should not throw an error if a ConfigMap template already exists", func() {
		data := map[string]string{"test": "foobar"}
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ai_templates.NodeLevelInstallTemplates,
				Namespace: SiteConfigNamespace,
			},
			Data: data,
		}
		Expect(c.Create(ctx, cm)).To(Succeed())

		key := types.NamespacedName{
			Name:      ai_templates.NodeLevelInstallTemplates,
			Namespace: SiteConfigNamespace,
		}

		err := initConfigMapTemplates(ctx, c, testLogger)
		Expect(err).ToNot(HaveOccurred())

		// Verify that the existing ConfigMap is not over-written
		aiNodeCM := &corev1.ConfigMap{}
		Expect(c.Get(ctx, key, aiNodeCM)).To(Succeed())
		Expect(aiNodeCM.Data).To(Equal(data))
	})
})
