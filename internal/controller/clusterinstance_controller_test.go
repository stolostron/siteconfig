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

	bmh_v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	"github.com/stolostron/siteconfig/api/v1alpha1"
	"github.com/stolostron/siteconfig/internal/controller/conditions"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

var _ = Describe("Reconcile", func() {
	var (
		c                 client.Client
		r                 *ClusterInstanceReconciler
		ctx               = context.Background()
		clusterName       = "test-cluster"
		clusterNamespace  = "test-namespace"
		clusterInstance   *v1alpha1.ClusterInstance
		pullSecret        *corev1.Secret
		testPullSecretVal = `{"auths":{"cloud.openshift.com":{"auth":"dXNlcjpwYXNzd29yZAo=","email":"r@r.com"}}}`
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			Build()
		testLogger := ctrl.Log.WithName("SiteConfigBuilder")
		scBuilder := NewClusterInstanceBuilder(testLogger)
		r = &ClusterInstanceReconciler{
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

		clusterInstance = &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:       clusterName,
				Namespace:  clusterNamespace,
				Finalizers: []string{clusterInstanceFinalizer},
			},
			Spec: v1alpha1.ClusterInstanceSpec{
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
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

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
		r                *ClusterInstanceReconciler
		ctx              = context.Background()
		clusterName      = "test-cluster"
		clusterNamespace = "test-namespace"
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			Build()
		testLogger := ctrl.Log.WithName("SiteConfigBuilder")
		scBuilder := NewClusterInstanceBuilder(testLogger)
		r = &ClusterInstanceReconciler{
			Client:    c,
			Scheme:    scheme.Scheme,
			Log:       testLogger,
			ScBuilder: scBuilder,
		}
	})

	It("adds the finalizer if the SiteConfig is not being deleted", func() {
		clusterInstance := &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: clusterNamespace,
			},
		}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		res, stop, err := r.handleFinalizer(ctx, clusterInstance)
		Expect(res).To(Equal(ctrl.Result{Requeue: true}))
		Expect(stop).To(BeTrue())
		Expect(err).ToNot(HaveOccurred())

		key := types.NamespacedName{
			Name:      clusterName,
			Namespace: clusterNamespace,
		}
		Expect(c.Get(ctx, key, clusterInstance)).To(Succeed())
		Expect(clusterInstance.GetFinalizers()).To(ContainElement(clusterInstanceFinalizer))
	})

	It("does nothing if the finalizer is already present", func() {
		clusterInstance := &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:       clusterName,
				Namespace:  clusterNamespace,
				Finalizers: []string{clusterInstanceFinalizer},
			},
		}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		res, stop, err := r.handleFinalizer(ctx, clusterInstance)
		Expect(res).To(Equal(ctrl.Result{}))
		Expect(stop).To(BeFalse())
		Expect(err).ToNot(HaveOccurred())
	})

	It("deletes all rendered manifests", func() {

		manifestName := "test"
		bmhApilGroup := "metal3.io/v1alpha1"
		cdApiGroup := "hive.openshift.io/v1"
		mcApiGroup := "cluster.open-cluster-management.io/v1"

		clusterInstance := &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:       clusterName,
				Namespace:  clusterNamespace,
				Finalizers: []string{clusterInstanceFinalizer},
			},
			Status: v1alpha1.ClusterInstanceStatus{
				ManifestsRendered: []v1alpha1.ManifestReference{
					{
						APIGroup:  &cdApiGroup,
						Kind:      "ClusterDeployment",
						Name:      manifestName,
						Namespace: clusterNamespace,
						SyncWave:  1,
						Status:    v1alpha1.ManifestRenderedSuccess,
					},
					{
						APIGroup:  &bmhApilGroup,
						Kind:      "BareMetalHost",
						Name:      manifestName,
						Namespace: clusterNamespace,
						SyncWave:  2,
						Status:    v1alpha1.ManifestRenderedSuccess,
					},
					{
						APIGroup: &mcApiGroup,
						Kind:     "ManagedCluster",
						Name:     manifestName,
						SyncWave: 3,
						Status:   v1alpha1.ManifestRenderedSuccess,
					},
				},
			},
		}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		// Create manifests
		cd := &hivev1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      manifestName,
				Namespace: clusterNamespace,
			},
		}
		Expect(c.Create(ctx, cd)).To(Succeed())

		bmh := &bmh_v1alpha1.BareMetalHost{
			ObjectMeta: metav1.ObjectMeta{
				Name:      manifestName,
				Namespace: clusterNamespace,
			},
		}
		Expect(c.Create(ctx, bmh)).To(Succeed())

		mc := &clusterv1.ManagedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name: manifestName,
			},
		}
		Expect(c.Create(ctx, mc)).To(Succeed())

		// Get the created manfiests to confirm they exist before calling finalizer
		key := types.NamespacedName{
			Name:      manifestName,
			Namespace: clusterNamespace,
		}
		keyMc := types.NamespacedName{
			Name: manifestName,
		}
		Expect(c.Get(ctx, key, cd)).To(Succeed())
		Expect(c.Get(ctx, key, bmh)).To(Succeed())
		Expect(c.Get(ctx, keyMc, mc)).To(Succeed())

		// Set the deletionTimestamp to force deletion of siteconfig manifests
		deletionTimeStamp := metav1.Now()
		clusterInstance.ObjectMeta.DeletionTimestamp = &deletionTimeStamp

		// Expect the manifests previously created to be deleted after the handleFinalizer is called
		res, stop, err := r.handleFinalizer(ctx, clusterInstance)
		Expect(res).To(Equal(ctrl.Result{}))
		Expect(stop).To(BeTrue())
		Expect(err).ToNot(HaveOccurred())

		Expect(c.Get(ctx, key, cd)).ToNot(Succeed())
		Expect(c.Get(ctx, key, bmh)).ToNot(Succeed())
		Expect(c.Get(ctx, keyMc, mc)).ToNot(Succeed())
	})

	It("does not fail to handle the finalizer when attempting to delete a missing manifest", func() {

		manifestName := "test"
		bmhApilGroup := "metal3.io/v1alpha1"
		cdApiGroup := "hive.openshift.io/v1"
		mcApiGroup := "cluster.open-cluster-management.io/v1"

		clusterInstance := &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:       clusterName,
				Namespace:  clusterNamespace,
				Finalizers: []string{clusterInstanceFinalizer},
			},
			Status: v1alpha1.ClusterInstanceStatus{
				ManifestsRendered: []v1alpha1.ManifestReference{
					{
						APIGroup:  &cdApiGroup,
						Kind:      "ClusterDeployment",
						Name:      manifestName,
						Namespace: clusterNamespace,
						SyncWave:  1,
						Status:    v1alpha1.ManifestRenderedSuccess,
					},
					{
						APIGroup:  &bmhApilGroup,
						Kind:      "BareMetalHost",
						Name:      manifestName,
						Namespace: clusterNamespace,
						SyncWave:  2,
						Status:    v1alpha1.ManifestRenderedSuccess,
					},
					{
						APIGroup: &mcApiGroup,
						Kind:     "ManagedCluster",
						Name:     manifestName,
						SyncWave: 3,
						Status:   v1alpha1.ManifestRenderedSuccess,
					},
				},
			},
		}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		// Create manifests
		cd := &hivev1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      manifestName,
				Namespace: clusterNamespace,
			},
		}
		Expect(c.Create(ctx, cd)).To(Succeed())

		mc := &clusterv1.ManagedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name: manifestName,
			},
		}
		Expect(c.Create(ctx, mc)).To(Succeed())

		// Get the created manfiests to confirm they exist before calling finalizer
		key := types.NamespacedName{
			Name:      manifestName,
			Namespace: clusterNamespace,
		}
		keyMc := types.NamespacedName{
			Name: manifestName,
		}
		Expect(c.Get(ctx, key, cd)).To(Succeed())
		Expect(c.Get(ctx, keyMc, mc)).To(Succeed())

		// BareMetalHost manifest is not created!
		bmh := &bmh_v1alpha1.BareMetalHost{}
		Expect(c.Get(ctx, key, bmh)).ToNot(Succeed())

		// Set the deletionTimestamp to force deletion of siteconfig manifests
		deletionTimeStamp := metav1.Now()
		clusterInstance.ObjectMeta.DeletionTimestamp = &deletionTimeStamp

		// Expect the manifests previously created to be deleted after the handleFinalizer is called
		res, stop, err := r.handleFinalizer(ctx, clusterInstance)
		Expect(res).To(Equal(ctrl.Result{}))
		Expect(stop).To(BeTrue())
		Expect(err).ToNot(HaveOccurred())

		Expect(c.Get(ctx, key, cd)).ToNot(Succeed())
		Expect(c.Get(ctx, keyMc, mc)).ToNot(Succeed())
	})

})

var _ = Describe("handleValidate", func() {
	var (
		c                                            client.Client
		r                                            *ClusterInstanceReconciler
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
		clusterInstance                              *v1alpha1.ClusterInstance
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			Build()
		testLogger := ctrl.Log.WithName("SiteConfigBuilder")
		scBuilder := NewClusterInstanceBuilder(testLogger)
		r = &ClusterInstanceReconciler{
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
		clusterInstance = getMockSNOClusterInstance(clusterName, clusterNamespace, pullSecret.Name, bmcCredentialsName, clusterImageSetName, extraManifestName, clusterTemplateRef, nodeTemplateRef)
	})

	It("successfully sets the SiteConfigValidated condition to true for a valid SiteConfig", func() {
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := r.handleValidate(ctx, clusterInstance)
		Expect(err).ToNot(HaveOccurred())

		key := types.NamespacedName{
			Name:      clusterName,
			Namespace: clusterNamespace,
		}
		Expect(c.Get(ctx, key, clusterInstance)).To(Succeed())
		matched := false
		for _, cond := range clusterInstance.Status.Conditions {
			if cond.Type == string(conditions.ClusterInstanceValidated) && cond.Status == metav1.ConditionTrue {
				matched = true
			}
		}
		Expect(matched).To(BeTrue())
	})

	It("successfully sets the SiteConfigValidated condition to false for an invalid SiteConfig", func() {
		clusterInstance.Spec.ClusterName = ""
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := r.handleValidate(ctx, clusterInstance)
		Expect(err).To(HaveOccurred())

		key := types.NamespacedName{
			Name:      clusterName,
			Namespace: clusterNamespace,
		}
		Expect(c.Get(ctx, key, clusterInstance)).To(Succeed())
		matched := false
		for _, cond := range clusterInstance.Status.Conditions {
			if cond.Type == string(conditions.ClusterInstanceValidated) && cond.Status == metav1.ConditionFalse {
				matched = true
			}
		}
		Expect(matched).To(BeTrue())
	})

	It("does not require a reconcile when the SiteConfigValidated condition remains unchanged", func() {
		clusterInstance.Status.Conditions = []metav1.Condition{
			{
				Type:    string(conditions.ClusterInstanceValidated),
				Reason:  string(conditions.Completed),
				Status:  metav1.ConditionTrue,
				Message: "Validation succeeded",
			},
		}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := r.handleValidate(ctx, clusterInstance)
		Expect(err).ToNot(HaveOccurred())

		key := types.NamespacedName{
			Name:      clusterName,
			Namespace: clusterNamespace,
		}
		Expect(c.Get(ctx, key, clusterInstance)).To(Succeed())
		matched := false
		for _, cond := range clusterInstance.Status.Conditions {
			if cond.Type == string(conditions.ClusterInstanceValidated) && cond.Status == metav1.ConditionTrue {
				matched = true
			}
		}
		Expect(matched).To(BeTrue())
	})

	It("requires a reconcile when the SiteConfigValidated condition has changed", func() {
		clusterInstance.Status.Conditions = []metav1.Condition{
			{
				Type:    string(conditions.ClusterInstanceValidated),
				Reason:  string(conditions.Failed),
				Status:  metav1.ConditionFalse,
				Message: "Validation failed",
			},
		}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := r.handleValidate(ctx, clusterInstance)
		Expect(err).ToNot(HaveOccurred())

		key := types.NamespacedName{
			Name:      clusterName,
			Namespace: clusterNamespace,
		}
		Expect(c.Get(ctx, key, clusterInstance)).To(Succeed())
		matched := false
		for _, cond := range clusterInstance.Status.Conditions {
			if cond.Type == string(conditions.ClusterInstanceValidated) && cond.Status == metav1.ConditionTrue {
				matched = true
			}
		}
		Expect(matched).To(BeTrue())
	})

})

var _ = Describe("handleRenderTemplates", func() {
	var (
		c               client.Client
		r               *ClusterInstanceReconciler
		ctx             = context.Background()
		clusterInstance *v1alpha1.ClusterInstance
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
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			Build()
		testLogger := ctrl.Log.WithName("SiteConfigBuilder")
		scBuilder := NewClusterInstanceBuilder(testLogger)
		r = &ClusterInstanceReconciler{
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
		clusterInstance = getMockSNOClusterInstance(clusterName, clusterNamespace, pullSecret.Name, bmcCredentialsName, clusterImageSetName, extraManifestName, clusterTemplateRef, nodeTemplateRef)
	})

	It("fails to render templates and updates the status correctly", func() {
		clusterInstance.Spec.Nodes[0].TemplateRefs = []v1alpha1.TemplateRef{
			{
				Name:      "test",
				Namespace: "default",
			},
		}

		clusterInstance.Spec.TemplateRefs = []v1alpha1.TemplateRef{
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
    siteconfig.open-cluster-management.io/sync-wave: "1"
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
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := r.handleValidate(ctx, clusterInstance)
		Expect(err).ToNot(HaveOccurred())

		rendered, err := r.handleRenderTemplates(ctx, clusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(rendered).To(Equal(false))

		// Verify correct status conditions are set
		key := types.NamespacedName{
			Name:      clusterInstance.Name,
			Namespace: clusterInstance.Namespace,
		}
		Expect(c.Get(ctx, key, clusterInstance)).To(Succeed())

		matched := false
		for _, cond := range clusterInstance.Status.Conditions {
			if cond.Type == string(conditions.RenderedTemplates) && cond.Status == metav1.ConditionFalse {
				matched = true
			}
		}
		Expect(matched).To(Equal(true), "Condition %s was not found", conditions.RenderedTemplates)
	})

	It("successfully renders templates and updates the status correctly", func() {
		clusterInstance.Spec.Nodes[0].TemplateRefs = []v1alpha1.TemplateRef{
			{
				Name:      "test",
				Namespace: "default",
			},
		}

		clusterInstance.Spec.TemplateRefs = []v1alpha1.TemplateRef{
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
    siteconfig.open-cluster-management.io/sync-wave: "1"
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
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := r.handleValidate(ctx, clusterInstance)
		Expect(err).ToNot(HaveOccurred())

		rendered, err := r.handleRenderTemplates(ctx, clusterInstance)
		Expect(err).ToNot(HaveOccurred())
		Expect(rendered).To(Equal(true))

		// Verify correct status conditions are set
		key := types.NamespacedName{
			Name:      clusterInstance.Name,
			Namespace: clusterInstance.Namespace,
		}
		Expect(c.Get(ctx, key, clusterInstance)).To(Succeed())

		expectedConditions := []metav1.Condition{
			{
				Type:   string(conditions.ClusterInstanceValidated),
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
			for _, cond := range clusterInstance.Status.Conditions {
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
		c               client.Client
		r               *ClusterInstanceReconciler
		ctx             = context.Background()
		clusterInstance *v1alpha1.ClusterInstance
		Manifests       []v1alpha1.ManifestReference
		aciApiGroup     = "extensions.hive.openshift.io/v1beta1"
		cdApiGroup      = "hive.openshift.io/v1"
		bmhApilGroup    = "metal3.io/v1alpha1"
		nmscApiGroup    = "agent-install.openshift.io/v1beta1"
	)

	BeforeEach(func() {

		var (
			clusterName      = "test-cluster"
			clusterNamespace = "test-cluster"
		)

		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			Build()
		testLogger := ctrl.Log.WithName("SiteConfigBuilder")
		scBuilder := NewClusterInstanceBuilder(testLogger)
		r = &ClusterInstanceReconciler{
			Client:    c,
			Scheme:    scheme.Scheme,
			Log:       testLogger,
			ScBuilder: scBuilder,
		}

		clusterInstance = &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: clusterNamespace,
			},
			Spec: v1alpha1.ClusterInstanceSpec{
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

		clusterInstance.Spec.SuppressedManifests = []string{}
		clusterInstance.Status = v1alpha1.ClusterInstanceStatus{
			ManifestsRendered: Manifests,
		}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := r.updateSuppressedManifestsStatus(ctx, clusterInstance)
		Expect(err).ToNot(HaveOccurred())

		// Verify status.ManifestRendered is unchanged
		sc := &v1alpha1.ClusterInstance{}
		key := types.NamespacedName{
			Name:      clusterInstance.Name,
			Namespace: clusterInstance.Namespace,
		}
		Expect(c.Get(ctx, key, sc)).To(Succeed())
		for _, expManifest := range Manifests {
			manifest := findManifestRendered(&expManifest, sc.Status.ManifestsRendered)
			Expect(manifest).ToNot(Equal(nil))
			Expect(manifest.Status).To(Equal(expManifest.Status))
		}
	})

	It("correctly suppresses cluster-level manifests when specified", func() {

		clusterInstance.Spec.SuppressedManifests = []string{"ClusterDeployment"}
		clusterInstance.Status = v1alpha1.ClusterInstanceStatus{
			ManifestsRendered: Manifests,
		}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := r.updateSuppressedManifestsStatus(ctx, clusterInstance)
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
		sc := &v1alpha1.ClusterInstance{}
		key := types.NamespacedName{
			Name:      clusterInstance.Name,
			Namespace: clusterInstance.Namespace,
		}
		Expect(c.Get(ctx, key, sc)).To(Succeed())
		for _, expManifest := range expectedManifests {
			manifest := findManifestRendered(&expManifest, sc.Status.ManifestsRendered)
			Expect(manifest).ToNot(Equal(nil))
			Expect(manifest.Status).To(Equal(expManifest.Status))
		}
	})

	It("correctly suppresses cluster and node level manifests when specified", func() {

		clusterInstance.Spec.SuppressedManifests = []string{"ClusterDeployment"}
		clusterInstance.Spec.Nodes[0].SuppressedManifests = []string{"BareMetalHost"}

		clusterInstance.Status = v1alpha1.ClusterInstanceStatus{
			ManifestsRendered: Manifests,
		}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := r.updateSuppressedManifestsStatus(ctx, clusterInstance)
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
		sc := &v1alpha1.ClusterInstance{}
		key := types.NamespacedName{
			Name:      clusterInstance.Name,
			Namespace: clusterInstance.Namespace,
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
		r                *ClusterInstanceReconciler
		ctx              = context.Background()
		clusterInstance  *v1alpha1.ClusterInstance
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
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			Build()
		testLogger := ctrl.Log.WithName("SiteConfigBuilder")
		scBuilder := NewClusterInstanceBuilder(testLogger)
		r = &ClusterInstanceReconciler{
			Client:    c,
			Scheme:    scheme.Scheme,
			Log:       testLogger,
			ScBuilder: scBuilder,
		}

		clusterInstance = &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: clusterNamespace,
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				ClusterName: clusterName,
			},
		}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())
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

		result, err := r.executeRenderedManifests(ctx, testClient, clusterInstance, manifestGroup, expManifest.Status)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(BeTrue())
		Expect(called).To(BeTrue())

		// Verify SiteConfig status
		Expect(c.Get(ctx, key, clusterInstance)).To(Succeed())
		manifest := findManifestRendered(&expManifest, clusterInstance.Status.ManifestsRendered)
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

		result, err := r.executeRenderedManifests(ctx, testClient, clusterInstance, manifestGroup, v1alpha1.ManifestRenderedSuccess)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(BeFalse())
		Expect(called).To(BeTrue())

		// Verify SiteConfig status
		Expect(c.Get(ctx, key, clusterInstance)).To(Succeed())
		manifest := findManifestRendered(&expManifest, clusterInstance.Status.ManifestsRendered)
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

		result, err := r.executeRenderedManifests(ctx, testClient, clusterInstance, manifestGroup, expManifest.Status)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(BeTrue())
		Expect(called).To(BeTrue())

		// Verify SiteConfig status
		Expect(c.Get(ctx, key, clusterInstance)).To(Succeed())
		manifest := findManifestRendered(&expManifest, clusterInstance.Status.ManifestsRendered)
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

		result, err := r.executeRenderedManifests(ctx, testClient, clusterInstance, manifestGroup, expManifest.Status)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(BeFalse())
		Expect(called).To(BeTrue())

		// Verify SiteConfig status
		Expect(c.Get(ctx, key, clusterInstance)).To(Succeed())
		manifest := findManifestRendered(&expManifest, clusterInstance.Status.ManifestsRendered)
		Expect(manifest).ToNot(Equal(nil))
		Expect(manifest.Status).To(Equal(expManifest.Status))
		Expect(manifest.Message).To(ContainSubstring(testError))
	})

})
