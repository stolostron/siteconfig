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
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	"github.com/sakhoury/siteconfig/api/v1alpha1"
	"github.com/sakhoury/siteconfig/internal/controller/conditions"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

var _ = Describe("Reconcile", func() {
	var (
		c                 client.Client
		r                 *SiteConfigReconciler
		ctx               = context.Background()
		clusterName       = "test-cluster"
		clusterNamespace  = "test-namespace"
		siteConfig        *v1alpha1.SiteConfig
		pullSecret        *corev1.Secret
		testPullSecretVal = `{"auths":{"cloud.openshift.com":{"auth":"dXNlcjpwYXNzd29yZAo=","email":"r@r.com"}}}`
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.SiteConfig{}).
			Build()
		testLogger := ctrl.Log.WithName("SiteConfigBuilder")
		scBuilder := NewSiteConfigBuilder(testLogger)
		r = &SiteConfigReconciler{
			Client:    c,
			Scheme:    scheme.Scheme,
			Log:       testLogger,
			ScBuilder: scBuilder,
		}

		pullSecret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pull-secret",
				Namespace: clusterName,
			},
			Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte(testPullSecretVal)},
		}
		Expect(c.Create(ctx, pullSecret)).To(Succeed())

		siteConfig = &v1alpha1.SiteConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:       clusterName,
				Namespace:  clusterNamespace,
				Finalizers: []string{siteConfigFinalizer},
			},
			Spec: v1alpha1.SiteConfigSpec{
				ClusterName:            clusterName,
				PullSecretRef:          &corev1.LocalObjectReference{Name: pullSecret.Name},
				ClusterImageSetNameRef: "testimage:foobar",
				SSHPublicKey:           "test-ssh",
				BaseDomain:             "abcd",
				ClusterType:            v1alpha1.ClusterTypeSNO,
				TemplateRefs: []v1alpha1.TemplateRef{
					{Name: "test-cluster-template", Namespace: "default"}},
				Nodes: []v1alpha1.NodeSpec{{
					BmcAddress:         "1:2:3:4",
					BmcCredentialsName: v1alpha1.BmcCredentialsName{Name: "bmc"},
					TemplateRefs: []v1alpha1.TemplateRef{
						{Name: "test-node-template", Namespace: "default"}}}}},
		}
	})

	It("creates the correct SiteConfig manifest", func() {
		Expect(c.Create(ctx, siteConfig)).To(Succeed())

		key := types.NamespacedName{
			Namespace: clusterName,
			Name:      clusterNamespace,
		}
		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))
	})

	It("doesn't error for a missing SiteConfig", func() {
		key := types.NamespacedName{
			Namespace: clusterName,
			Name:      clusterNamespace,
		}
		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))
	})
})

var _ = Describe("handleFinalizer", func() {
	var (
		c                client.Client
		r                *SiteConfigReconciler
		ctx              = context.Background()
		clusterName      = "test-cluster"
		clusterNamespace = "test-namespace"
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.SiteConfig{}).
			Build()
		testLogger := ctrl.Log.WithName("SiteConfigBuilder")
		scBuilder := NewSiteConfigBuilder(testLogger)
		r = &SiteConfigReconciler{
			Client:    c,
			Scheme:    scheme.Scheme,
			Log:       testLogger,
			ScBuilder: scBuilder,
		}
	})

	It("adds the finalizer if the SiteConfig is not being deleted", func() {
		siteConfig := &v1alpha1.SiteConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: clusterNamespace,
			},
		}
		Expect(c.Create(ctx, siteConfig)).To(Succeed())

		res, stop, err := r.handleFinalizer(ctx, siteConfig)
		Expect(res).To(Equal(ctrl.Result{Requeue: true}))
		Expect(stop).To(BeTrue())
		Expect(err).ToNot(HaveOccurred())

		key := types.NamespacedName{
			Name:      clusterName,
			Namespace: clusterNamespace,
		}
		Expect(c.Get(ctx, key, siteConfig)).To(Succeed())
		Expect(siteConfig.GetFinalizers()).To(ContainElement(siteConfigFinalizer))
	})

	It("does nothing if the finalizer is already present", func() {
		siteConfig := &v1alpha1.SiteConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:       clusterName,
				Namespace:  clusterNamespace,
				Finalizers: []string{siteConfigFinalizer},
			},
		}
		Expect(c.Create(ctx, siteConfig)).To(Succeed())

		res, stop, err := r.handleFinalizer(ctx, siteConfig)
		Expect(res).To(Equal(ctrl.Result{}))
		Expect(stop).To(BeFalse())
		Expect(err).ToNot(HaveOccurred())
	})
})

var _ = Describe("handleValidate", func() {
	var (
		c                                            client.Client
		r                                            *SiteConfigReconciler
		ctx                                          = context.Background()
		bmcCredentialsName                           = "bmh-secret"
		bmc, pullSecret                              *corev1.Secret
		clusterName                                  = "test-cluster"
		clusterNamespace                             = "test-cluster"
		clusterImageSetName                          = "testimage:foobar"
		clusterImageSet                              *hivev1.ClusterImageSet
		clusterTemplateRef                           = "cluster-template-ref"
		clusterTemplate, nodeTemplate, extraManifest *corev1.ConfigMap
		extraManifestName                            = "extra-manifest"
		nodeTemplateRef                              = "node-template-ref"
		siteConfig                                   *v1alpha1.SiteConfig
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.SiteConfig{}).
			Build()
		testLogger := ctrl.Log.WithName("SiteConfigBuilder")
		scBuilder := NewSiteConfigBuilder(testLogger)
		r = &SiteConfigReconciler{
			Client:    c,
			Scheme:    scheme.Scheme,
			Log:       testLogger,
			ScBuilder: scBuilder,
		}

		bmc = getMockBmcSecret(bmcCredentialsName, clusterNamespace)
		clusterImageSet = getMockClusterImageSet(clusterImageSetName)
		pullSecret = getMockPullSecret("pull-secret", clusterNamespace)
		clusterTemplate = getMockClusterTemplate(clusterTemplateRef, clusterNamespace)
		nodeTemplate = getMockNodeTemplate(nodeTemplateRef, clusterNamespace)
		extraManifest = getMockExtraManifest(extraManifestName, clusterNamespace)

		SetupTestPrereqs(ctx, c, bmc, pullSecret, clusterImageSet, clusterTemplate, nodeTemplate, extraManifest)
		siteConfig = getMockSNOSiteConfig(clusterName, clusterNamespace, pullSecret.Name, bmcCredentialsName, clusterImageSetName, extraManifestName, clusterTemplateRef, nodeTemplateRef)
	})

	It("successfully sets the SiteConfigValidated condition to true for a valid SiteConfig", func() {
		Expect(c.Create(ctx, siteConfig)).To(Succeed())

		err := r.handleValidate(ctx, siteConfig)
		Expect(err).ToNot(HaveOccurred())

		key := types.NamespacedName{
			Name:      clusterName,
			Namespace: clusterNamespace,
		}
		Expect(c.Get(ctx, key, siteConfig)).To(Succeed())
		matched := false
		for _, cond := range siteConfig.Status.Conditions {
			if cond.Type == string(conditions.SiteConfigValidated) && cond.Status == metav1.ConditionTrue {
				matched = true
			}
		}
		Expect(matched).To(BeTrue())
	})

	It("successfully sets the SiteConfigValidated condition to false for an invalid SiteConfig", func() {
		siteConfig.Spec.ClusterName = ""
		Expect(c.Create(ctx, siteConfig)).To(Succeed())

		err := r.handleValidate(ctx, siteConfig)
		Expect(err).To(HaveOccurred())

		key := types.NamespacedName{
			Name:      clusterName,
			Namespace: clusterNamespace,
		}
		Expect(c.Get(ctx, key, siteConfig)).To(Succeed())
		matched := false
		for _, cond := range siteConfig.Status.Conditions {
			if cond.Type == string(conditions.SiteConfigValidated) && cond.Status == metav1.ConditionFalse {
				matched = true
			}
		}
		Expect(matched).To(BeTrue())
	})

	It("does not require a reconcile when the SiteConfigValidated condition remains unchanged", func() {
		siteConfig.Status.Conditions = []metav1.Condition{
			{
				Type:    string(conditions.SiteConfigValidated),
				Reason:  string(conditions.Completed),
				Status:  metav1.ConditionTrue,
				Message: "Validation succeeded",
			},
		}
		Expect(c.Create(ctx, siteConfig)).To(Succeed())

		err := r.handleValidate(ctx, siteConfig)
		Expect(err).ToNot(HaveOccurred())

		key := types.NamespacedName{
			Name:      clusterName,
			Namespace: clusterNamespace,
		}
		Expect(c.Get(ctx, key, siteConfig)).To(Succeed())
		matched := false
		for _, cond := range siteConfig.Status.Conditions {
			if cond.Type == string(conditions.SiteConfigValidated) && cond.Status == metav1.ConditionTrue {
				matched = true
			}
		}
		Expect(matched).To(BeTrue())
	})

	It("requires a reconcile when the SiteConfigValidated condition has changed", func() {
		siteConfig.Status.Conditions = []metav1.Condition{
			{
				Type:    string(conditions.SiteConfigValidated),
				Reason:  string(conditions.Failed),
				Status:  metav1.ConditionFalse,
				Message: "Validation failed",
			},
		}
		Expect(c.Create(ctx, siteConfig)).To(Succeed())

		err := r.handleValidate(ctx, siteConfig)
		Expect(err).ToNot(HaveOccurred())

		key := types.NamespacedName{
			Name:      clusterName,
			Namespace: clusterNamespace,
		}
		Expect(c.Get(ctx, key, siteConfig)).To(Succeed())
		matched := false
		for _, cond := range siteConfig.Status.Conditions {
			if cond.Type == string(conditions.SiteConfigValidated) && cond.Status == metav1.ConditionTrue {
				matched = true
			}
		}
		Expect(matched).To(BeTrue())
	})

})

var _ = Describe("handleRenderTemplates", func() {
	var (
		c          client.Client
		r          *SiteConfigReconciler
		ctx        = context.Background()
		siteConfig *v1alpha1.SiteConfig
	)

	BeforeEach(func() {

		var (
			bmcCredentialsName                           = "bmh-secret"
			bmc, pullSecret                              *corev1.Secret
			clusterName                                  = "test-cluster"
			clusterNamespace                             = "test-cluster"
			clusterImageSetName                          = "testimage:foobar"
			clusterTemplateRef                           = "cluster-template-ref"
			clusterTemplate, nodeTemplate, extraManifest *corev1.ConfigMap
			extraManifestName                            = "extra-manifest"
			nodeTemplateRef                              = "node-template-ref"
		)

		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.SiteConfig{}).
			Build()
		testLogger := ctrl.Log.WithName("SiteConfigBuilder")
		scBuilder := NewSiteConfigBuilder(testLogger)
		r = &SiteConfigReconciler{
			Client:    c,
			Scheme:    scheme.Scheme,
			Log:       testLogger,
			ScBuilder: scBuilder,
		}

		bmc = getMockBmcSecret(bmcCredentialsName, clusterNamespace)
		clusterImageSet := getMockClusterImageSet(clusterImageSetName)
		pullSecret = getMockPullSecret("pull-secret", clusterNamespace)
		clusterTemplate = getMockClusterTemplate(clusterTemplateRef, clusterNamespace)
		nodeTemplate = getMockNodeTemplate(nodeTemplateRef, clusterNamespace)
		extraManifest = getMockExtraManifest(extraManifestName, clusterNamespace)

		SetupTestPrereqs(ctx, c, bmc, pullSecret, clusterImageSet, clusterTemplate, nodeTemplate, extraManifest)
		siteConfig = getMockSNOSiteConfig(clusterName, clusterNamespace, pullSecret.Name, bmcCredentialsName, clusterImageSetName, extraManifestName, clusterTemplateRef, nodeTemplateRef)
	})

	It("fails to render templates and updates the status correctly", func() {
		siteConfig.Spec.Nodes[0].TemplateRefs = []v1alpha1.TemplateRef{
			{
				Name:      "test",
				Namespace: "default",
			},
		}

		siteConfig.Spec.TemplateRefs = []v1alpha1.TemplateRef{
			{
				Name:      "test",
				Namespace: "default",
			},
		}

		templateStr := `apiVersion: test.io/v1
metadata:
  name: "{{ .Site.ClusterName }}"
  namespace: "{{ .Site.ClusterName }}"
  annotations:
    metaclusterinstall.openshift.io/sync-wave: "1"
kind: Test
spec:
  name: "{{ .Site.ClusterNamee }}"`

		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "default",
			},
			Data: map[string]string{"Test": templateStr},
		}
		Expect(c.Create(ctx, cm)).To(Succeed())
		Expect(c.Create(ctx, siteConfig)).To(Succeed())

		err := r.handleValidate(ctx, siteConfig)
		Expect(err).ToNot(HaveOccurred())

		rendered, err := r.handleRenderTemplates(ctx, siteConfig)
		Expect(err).To(HaveOccurred())
		Expect(rendered).To(Equal(false))

		// Verify correct status conditions are set
		key := types.NamespacedName{
			Name:      siteConfig.Name,
			Namespace: siteConfig.Namespace,
		}
		Expect(c.Get(ctx, key, siteConfig)).To(Succeed())

		matched := false
		for _, cond := range siteConfig.Status.Conditions {
			if cond.Type == string(conditions.RenderedTemplates) && cond.Status == metav1.ConditionFalse {
				matched = true
			}
		}
		Expect(matched).To(Equal(true), "Condition %s was not found", conditions.RenderedTemplates)
	})

	It("successfully renders templates and updates the status correctly", func() {
		siteConfig.Spec.Nodes[0].TemplateRefs = []v1alpha1.TemplateRef{
			{
				Name:      "test",
				Namespace: "default",
			},
		}

		siteConfig.Spec.TemplateRefs = []v1alpha1.TemplateRef{
			{
				Name:      "test",
				Namespace: "default",
			},
		}

		templateStr := `apiVersion: test.io/v1
metadata:
  name: "{{ .Site.ClusterName }}"
  namespace: "{{ .Site.ClusterName }}"
  annotations:
    metaclusterinstall.openshift.io/sync-wave: "1"
kind: Test
spec:
  name: "{{ .Site.ClusterName }}"`

		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "default",
			},
			Data: map[string]string{"Test": templateStr},
		}
		Expect(c.Create(ctx, cm)).To(Succeed())
		Expect(c.Create(ctx, siteConfig)).To(Succeed())

		err := r.handleValidate(ctx, siteConfig)
		Expect(err).ToNot(HaveOccurred())

		rendered, err := r.handleRenderTemplates(ctx, siteConfig)
		Expect(err).ToNot(HaveOccurred())
		Expect(rendered).To(Equal(true))

		// Verify correct status conditions are set
		key := types.NamespacedName{
			Name:      siteConfig.Name,
			Namespace: siteConfig.Namespace,
		}
		Expect(c.Get(ctx, key, siteConfig)).To(Succeed())

		expectedConditions := []metav1.Condition{
			{
				Type:   string(conditions.SiteConfigValidated),
				Reason: string(conditions.Completed),
				Status: metav1.ConditionTrue,
			},
			{
				Type:   string(conditions.RenderedTemplates),
				Reason: string(conditions.Completed),
				Status: metav1.ConditionTrue,
			},
			{
				Type:   string(conditions.RenderedTemplatesValidated),
				Reason: string(conditions.Completed),
				Status: metav1.ConditionTrue,
			},
			{
				Type:   string(conditions.RenderedTemplatesApplied),
				Reason: string(conditions.Completed),
				Status: metav1.ConditionTrue,
			},
		}

		for _, expCond := range expectedConditions {
			matched := false
			for _, cond := range siteConfig.Status.Conditions {
				if cond.Type == expCond.Type &&
					cond.Reason == expCond.Reason &&
					cond.Status == expCond.Status {
					matched = true
				}
			}
			Expect(matched).To(Equal(true), "Condition %s was not found", expCond.Type)
		}
	})
})

var _ = Describe("updateSuppressedManifestsStatus", func() {
	var (
		c            client.Client
		r            *SiteConfigReconciler
		ctx          = context.Background()
		siteConfig   *v1alpha1.SiteConfig
		Manifests    []v1alpha1.ManifestReference
		aciApiGroup  = "extensions.hive.openshift.io/v1beta1"
		cdApiGroup   = "hive.openshift.io/v1"
		bmhApilGroup = "metal3.io/v1alpha1"
		nmscApiGroup = "agent-install.openshift.io/v1beta1"
	)

	BeforeEach(func() {

		var (
			clusterName      = "test-cluster"
			clusterNamespace = "test-cluster"
		)

		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.SiteConfig{}).
			Build()
		testLogger := ctrl.Log.WithName("SiteConfigBuilder")
		scBuilder := NewSiteConfigBuilder(testLogger)
		r = &SiteConfigReconciler{
			Client:    c,
			Scheme:    scheme.Scheme,
			Log:       testLogger,
			ScBuilder: scBuilder,
		}

		siteConfig = &v1alpha1.SiteConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: clusterNamespace,
			},
			Spec: v1alpha1.SiteConfigSpec{
				ClusterName: clusterName,
				Nodes: []v1alpha1.NodeSpec{
					{
						Role:       "master",
						BmcAddress: "1:2:3:4",
					},
				}},
		}

		Manifests = []v1alpha1.ManifestReference{
			{
				APIGroup: &cdApiGroup,
				Kind:     "ClusterDeployment",
				Name:     "test-cd",
				Status:   v1alpha1.ManifestRenderedSuccess,
			},
			{
				APIGroup: &aciApiGroup,
				Kind:     "AgentClusterInstall",
				Name:     "test-aci",
				Status:   v1alpha1.ManifestRenderedSuccess,
			},
			{
				APIGroup: &bmhApilGroup,
				Kind:     "BareMetalHost",
				Name:     "test-bmh",
				Status:   v1alpha1.ManifestRenderedSuccess,
			},
			{
				APIGroup: &nmscApiGroup,
				Kind:     "NMStateConfig",
				Name:     "test-aci",
				Status:   v1alpha1.ManifestRenderedSuccess,
			},
		}
	})

	It("does not suppress manifests if nothing is specified", func() {

		siteConfig.Spec.SuppressedManifests = []string{}
		siteConfig.Status = v1alpha1.SiteConfigStatus{
			ManifestsRendered: Manifests,
		}
		Expect(c.Create(ctx, siteConfig)).To(Succeed())

		err := r.updateSuppressedManifestsStatus(ctx, siteConfig)
		Expect(err).ToNot(HaveOccurred())

		// Verify status.ManifestRendered is unchanged
		sc := &v1alpha1.SiteConfig{}
		key := types.NamespacedName{
			Name:      siteConfig.Name,
			Namespace: siteConfig.Namespace,
		}
		Expect(c.Get(ctx, key, sc)).To(Succeed())
		for _, expManifest := range Manifests {
			manifest := findManifestRendered(&expManifest, sc.Status.ManifestsRendered)
			Expect(manifest).ToNot(Equal(nil))
			Expect(manifest.Status).To(Equal(expManifest.Status))
		}
	})

	It("correctly suppresses cluster-level manifests when specified", func() {

		siteConfig.Spec.SuppressedManifests = []string{"ClusterDeployment"}
		siteConfig.Status = v1alpha1.SiteConfigStatus{
			ManifestsRendered: Manifests,
		}
		Expect(c.Create(ctx, siteConfig)).To(Succeed())

		err := r.updateSuppressedManifestsStatus(ctx, siteConfig)
		Expect(err).ToNot(HaveOccurred())

		// Verify handling of suppression
		expectedManifests := []v1alpha1.ManifestReference{
			{
				APIGroup: &cdApiGroup,
				Kind:     "ClusterDeployment",
				Name:     "test-cd",
				Status:   v1alpha1.ManifestSuppressed,
			},
			{
				APIGroup: &aciApiGroup,
				Kind:     "AgentClusterInstall",
				Name:     "test-aci",
				Status:   v1alpha1.ManifestRenderedSuccess,
			},
			{
				APIGroup: &bmhApilGroup,
				Kind:     "BareMetalHost",
				Name:     "test-bmh",
				Status:   v1alpha1.ManifestRenderedSuccess,
			},
			{
				APIGroup: &nmscApiGroup,
				Kind:     "NMStateConfig",
				Name:     "test-aci",
				Status:   v1alpha1.ManifestRenderedSuccess,
			},
		}
		sc := &v1alpha1.SiteConfig{}
		key := types.NamespacedName{
			Name:      siteConfig.Name,
			Namespace: siteConfig.Namespace,
		}
		Expect(c.Get(ctx, key, sc)).To(Succeed())
		for _, expManifest := range expectedManifests {
			manifest := findManifestRendered(&expManifest, sc.Status.ManifestsRendered)
			Expect(manifest).ToNot(Equal(nil))
			Expect(manifest.Status).To(Equal(expManifest.Status))
		}
	})

	It("correctly suppresses cluster and node level manifests when specified", func() {

		siteConfig.Spec.SuppressedManifests = []string{"ClusterDeployment"}
		siteConfig.Spec.Nodes[0].SuppressedManifests = []string{"BareMetalHost"}

		siteConfig.Status = v1alpha1.SiteConfigStatus{
			ManifestsRendered: Manifests,
		}
		Expect(c.Create(ctx, siteConfig)).To(Succeed())

		err := r.updateSuppressedManifestsStatus(ctx, siteConfig)
		Expect(err).ToNot(HaveOccurred())

		// Verify handling of suppression
		expectedManifests := []v1alpha1.ManifestReference{
			{
				APIGroup: &cdApiGroup,
				Kind:     "ClusterDeployment",
				Name:     "test-cd",
				Status:   v1alpha1.ManifestSuppressed,
			},
			{
				APIGroup: &aciApiGroup,
				Kind:     "AgentClusterInstall",
				Name:     "test-aci",
				Status:   v1alpha1.ManifestRenderedSuccess,
			},
			{
				APIGroup: &bmhApilGroup,
				Kind:     "BareMetalHost",
				Name:     "test-bmh",
				Status:   v1alpha1.ManifestSuppressed,
			},
			{
				APIGroup: &nmscApiGroup,
				Kind:     "NMStateConfig",
				Name:     "test-aci",
				Status:   v1alpha1.ManifestRenderedSuccess,
			},
		}
		sc := &v1alpha1.SiteConfig{}
		key := types.NamespacedName{
			Name:      siteConfig.Name,
			Namespace: siteConfig.Namespace,
		}
		Expect(c.Get(ctx, key, sc)).To(Succeed())
		for _, expManifest := range expectedManifests {
			manifest := findManifestRendered(&expManifest, sc.Status.ManifestsRendered)
			Expect(manifest).ToNot(Equal(nil))
			Expect(manifest.Status).To(Equal(expManifest.Status))
		}
	})

})

var _ = DescribeTable("groupAndSortManifests",
	func(manifests []interface{}, expected map[int][]interface{}, wantError bool) {
		got, err1 := groupAndSortManifests(manifests)
		if wantError {
			Expect(err1).To(HaveOccurred())
		}
		Expect(reflect.DeepEqual(got, expected)).To(BeTrue())
	},

	Entry("missing field 'kind'", []interface{}{
		map[string]interface{}{"apiVersion": "animal", "metadata": map[string]interface{}{"annotations": map[string]interface{}{WaveAnnotation: "1"}}},
		map[string]interface{}{"apiVersion": "fruit", "kind": "grape", "metadata": map[string]interface{}{"annotations": map[string]interface{}{WaveAnnotation: "0"}}},
		map[string]interface{}{"apiVersion": "animal", "kind": "elephant", "metadata": map[string]interface{}{"annotations": map[string]interface{}{WaveAnnotation: "1"}}},
	}, nil, true),

	Entry("all wave annotations supplied", []interface{}{
		map[string]interface{}{"apiVersion": "car", "kind": "mercedez", "metadata": map[string]interface{}{"annotations": map[string]interface{}{WaveAnnotation: "2"}}},
		map[string]interface{}{"apiVersion": "animal", "kind": "dog", "metadata": map[string]interface{}{"annotations": map[string]interface{}{WaveAnnotation: "1"}}},
		map[string]interface{}{"apiVersion": "car", "kind": "mazda", "metadata": map[string]interface{}{"annotations": map[string]interface{}{WaveAnnotation: "2"}}},
		map[string]interface{}{"apiVersion": "fruit", "kind": "banana", "metadata": map[string]interface{}{"annotations": map[string]interface{}{WaveAnnotation: "0"}}},
		map[string]interface{}{"apiVersion": "fruit", "kind": "apple", "metadata": map[string]interface{}{"annotations": map[string]interface{}{WaveAnnotation: "0"}}},
		map[string]interface{}{"apiVersion": "animal", "kind": "cat", "metadata": map[string]interface{}{"annotations": map[string]interface{}{WaveAnnotation: "1"}}},
		map[string]interface{}{"apiVersion": "fruit", "kind": "grape", "metadata": map[string]interface{}{"annotations": map[string]interface{}{WaveAnnotation: "0"}}},
		map[string]interface{}{"apiVersion": "animal", "kind": "elephant", "metadata": map[string]interface{}{"annotations": map[string]interface{}{WaveAnnotation: "1"}}},
	}, map[int][]interface{}{
		0: {
			map[string]interface{}{"apiVersion": "fruit", "kind": "apple", "metadata": map[string]interface{}{"annotations": map[string]interface{}{WaveAnnotation: "0"}}},
			map[string]interface{}{"apiVersion": "fruit", "kind": "banana", "metadata": map[string]interface{}{"annotations": map[string]interface{}{WaveAnnotation: "0"}}},
			map[string]interface{}{"apiVersion": "fruit", "kind": "grape", "metadata": map[string]interface{}{"annotations": map[string]interface{}{WaveAnnotation: "0"}}},
		},

		1: {
			map[string]interface{}{"apiVersion": "animal", "kind": "cat", "metadata": map[string]interface{}{"annotations": map[string]interface{}{WaveAnnotation: "1"}}},
			map[string]interface{}{"apiVersion": "animal", "kind": "dog", "metadata": map[string]interface{}{"annotations": map[string]interface{}{WaveAnnotation: "1"}}},
			map[string]interface{}{"apiVersion": "animal", "kind": "elephant", "metadata": map[string]interface{}{"annotations": map[string]interface{}{WaveAnnotation: "1"}}},
		},

		2: {
			map[string]interface{}{"apiVersion": "car", "kind": "mazda", "metadata": map[string]interface{}{"annotations": map[string]interface{}{WaveAnnotation: "2"}}},
			map[string]interface{}{"apiVersion": "car", "kind": "mercedez", "metadata": map[string]interface{}{"annotations": map[string]interface{}{WaveAnnotation: "2"}}},
		},
	}, false),

	Entry("test that default wave annotation is applied if not defined", []interface{}{
		map[string]interface{}{"apiVersion": "fruit", "kind": "banana"},
		map[string]interface{}{"apiVersion": "fruit", "kind": "apple"},
		map[string]interface{}{"apiVersion": "car", "kind": "mercedez", "metadata": map[string]interface{}{"annotations": map[string]interface{}{WaveAnnotation: "2"}}},
		map[string]interface{}{"apiVersion": "animal", "kind": "dog", "metadata": map[string]interface{}{"annotations": map[string]interface{}{WaveAnnotation: "1"}}},
		map[string]interface{}{"apiVersion": "car", "kind": "mazda", "metadata": map[string]interface{}{"annotations": map[string]interface{}{WaveAnnotation: "2"}}},
		map[string]interface{}{"apiVersion": "animal", "kind": "cat", "metadata": map[string]interface{}{"annotations": map[string]interface{}{WaveAnnotation: "1"}}},
		map[string]interface{}{"apiVersion": "animal", "kind": "elephant", "metadata": map[string]interface{}{"annotations": map[string]interface{}{WaveAnnotation: "1"}}},
		map[string]interface{}{"apiVersion": "fruit", "kind": "grape", "metadata": map[string]interface{}{"annotations": map[string]interface{}{WaveAnnotation: "0"}}},
	}, map[int][]interface{}{
		0: {
			map[string]interface{}{"apiVersion": "fruit", "kind": "apple"},
			map[string]interface{}{"apiVersion": "fruit", "kind": "banana"},
			map[string]interface{}{"apiVersion": "fruit", "kind": "grape", "metadata": map[string]interface{}{"annotations": map[string]interface{}{WaveAnnotation: "0"}}},
		},

		1: {
			map[string]interface{}{"apiVersion": "animal", "kind": "cat", "metadata": map[string]interface{}{"annotations": map[string]interface{}{WaveAnnotation: "1"}}},
			map[string]interface{}{"apiVersion": "animal", "kind": "dog", "metadata": map[string]interface{}{"annotations": map[string]interface{}{WaveAnnotation: "1"}}},
			map[string]interface{}{"apiVersion": "animal", "kind": "elephant", "metadata": map[string]interface{}{"annotations": map[string]interface{}{WaveAnnotation: "1"}}},
		},

		2: {
			map[string]interface{}{"apiVersion": "car", "kind": "mazda", "metadata": map[string]interface{}{"annotations": map[string]interface{}{WaveAnnotation: "2"}}},
			map[string]interface{}{"apiVersion": "car", "kind": "mercedez", "metadata": map[string]interface{}{"annotations": map[string]interface{}{WaveAnnotation: "2"}}},
		},
	}, false),
)

var _ = Describe("executeRenderedManifests", func() {
	var (
		c                client.Client
		r                *SiteConfigReconciler
		ctx              = context.Background()
		siteConfig       *v1alpha1.SiteConfig
		clusterName      = "test-cluster"
		clusterNamespace = "test-cluster"
		key              = types.NamespacedName{
			Name:      clusterName,
			Namespace: clusterNamespace,
		}
		apiGroup    = "hive.openshift.io/v1"
		expManifest = v1alpha1.ManifestReference{
			APIGroup: &apiGroup,
			Kind:     "ClusterDeployment",
			Name:     clusterName,
		}
		manifestGroup = map[int][]interface{}{
			0: {
				map[string]interface{}{
					"apiVersion": *expManifest.APIGroup,
					"kind":       expManifest.Kind,
					"metadata":   map[string]interface{}{"name": clusterName, "namespace": clusterNamespace}},
			},
		}
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.SiteConfig{}).
			Build()
		testLogger := ctrl.Log.WithName("SiteConfigBuilder")
		scBuilder := NewSiteConfigBuilder(testLogger)
		r = &SiteConfigReconciler{
			Client:    c,
			Scheme:    scheme.Scheme,
			Log:       testLogger,
			ScBuilder: scBuilder,
		}

		siteConfig = &v1alpha1.SiteConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: clusterNamespace,
			},
			Spec: v1alpha1.SiteConfigSpec{
				ClusterName: clusterName,
			},
		}
		Expect(c.Create(ctx, siteConfig)).To(Succeed())
	})

	It("succeeds in creating a manifest", func() {
		expManifest.Status = v1alpha1.ManifestRenderedSuccess

		called := false
		testClient := fakeclient.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				return apierrors.NewNotFound(schema.GroupResource{Group: "", Resource: expManifest.Kind}, expManifest.Name)
			},
			Create: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				called = true
				return nil
			},
		}).Build()

		result, err := r.executeRenderedManifests(ctx, testClient, siteConfig, manifestGroup, expManifest.Status)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(BeTrue())
		Expect(called).To(BeTrue())

		// Verify SiteConfig status
		Expect(c.Get(ctx, key, siteConfig)).To(Succeed())
		manifest := findManifestRendered(&expManifest, siteConfig.Status.ManifestsRendered)
		Expect(manifest).ToNot(Equal(nil))
		Expect(manifest.Status).To(Equal(expManifest.Status))
	})

	It("fails to apply the manifest due to an error while creating the kubernetes resource", func() {
		testError := "create-test-error"
		expManifest.Status = v1alpha1.ManifestRenderedFailure

		called := false
		testClient := fakeclient.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				return apierrors.NewNotFound(schema.GroupResource{Group: "", Resource: expManifest.Kind}, expManifest.Name)
			},
			Create: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				called = true
				return fmt.Errorf("%s", testError)
			},
		}).Build()

		result, err := r.executeRenderedManifests(ctx, testClient, siteConfig, manifestGroup, v1alpha1.ManifestRenderedSuccess)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(BeFalse())
		Expect(called).To(BeTrue())

		// Verify SiteConfig status
		Expect(c.Get(ctx, key, siteConfig)).To(Succeed())
		manifest := findManifestRendered(&expManifest, siteConfig.Status.ManifestsRendered)
		Expect(manifest).ToNot(Equal(nil))
		Expect(manifest.Status).To(Equal(expManifest.Status))
		Expect(manifest.Message).To(ContainSubstring(testError))

	})

	It("succeeds in updating a manifest", func() {
		expManifest.Status = v1alpha1.ManifestRenderedSuccess

		called := false
		testClient := fakeclient.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				return nil
			},
			Patch: func(ctx context.Context, client client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				called = true
				return nil
			},
		}).Build()

		result, err := r.executeRenderedManifests(ctx, testClient, siteConfig, manifestGroup, expManifest.Status)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(BeTrue())
		Expect(called).To(BeTrue())

		// Verify SiteConfig status
		Expect(c.Get(ctx, key, siteConfig)).To(Succeed())
		manifest := findManifestRendered(&expManifest, siteConfig.Status.ManifestsRendered)
		Expect(manifest).ToNot(Equal(nil))
		Expect(manifest.Status).To(Equal(expManifest.Status))
	})

	It("fails to update the manifest due to an error while patching the kubernetes resource", func() {
		testError := "update-test-error"
		expManifest.Status = v1alpha1.ManifestRenderedFailure

		called := false
		testClient := fakeclient.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				return nil
			},
			Patch: func(ctx context.Context, client client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				called = true
				return fmt.Errorf("%s", testError)
			},
		}).Build()

		result, err := r.executeRenderedManifests(ctx, testClient, siteConfig, manifestGroup, expManifest.Status)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(BeFalse())
		Expect(called).To(BeTrue())

		// Verify SiteConfig status
		Expect(c.Get(ctx, key, siteConfig)).To(Succeed())
		manifest := findManifestRendered(&expManifest, siteConfig.Status.ManifestsRendered)
		Expect(manifest).ToNot(Equal(nil))
		Expect(manifest.Status).To(Equal(expManifest.Status))
		Expect(manifest.Message).To(ContainSubstring(testError))
	})

})
