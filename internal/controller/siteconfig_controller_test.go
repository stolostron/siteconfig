package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	hivev1 "github.com/openshift/hive/apis/hive/v1"
	"github.com/sakhoury/siteconfig/api/v1alpha1"
	"github.com/sakhoury/siteconfig/internal/controller/conditions"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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
		Expect(err).ToNot(HaveOccurred())

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

})

var _ = Describe("isSiteConfigValidated", func() {
	var (
		c                client.Client
		r                *SiteConfigReconciler
		ctx              = context.Background()
		clusterName      = "test-cluster"
		clusterNamespace = "test-cluster"
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

	It("returns false when SiteConfig has not conditions set", func() {
		siteConfig := &v1alpha1.SiteConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: clusterNamespace,
			},
			Status: v1alpha1.SiteConfigStatus{},
		}
		Expect(c.Create(ctx, siteConfig)).To(Succeed())

		res := r.isSiteConfigValidated(siteConfig)
		Expect(res).To(BeFalse())
	})

	It("returns true when SiteConfig is validated", func() {
		siteConfig := &v1alpha1.SiteConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: clusterNamespace,
			},
			Status: v1alpha1.SiteConfigStatus{
				Conditions: []metav1.Condition{
					{
						Type:   string(conditions.SiteConfigValidated),
						Status: metav1.ConditionTrue,
					},
				},
			},
		}
		Expect(c.Create(ctx, siteConfig)).To(Succeed())

		res := r.isSiteConfigValidated(siteConfig)
		Expect(res).To(BeTrue())
	})

	It("returns false when SiteConfig has SiteConfigValidated condition set to false", func() {
		siteConfig := &v1alpha1.SiteConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: clusterNamespace,
			},
			Status: v1alpha1.SiteConfigStatus{
				Conditions: []metav1.Condition{
					{
						Type:   string(conditions.SiteConfigValidated),
						Status: metav1.ConditionFalse,
					},
				},
			},
		}
		Expect(c.Create(ctx, siteConfig)).To(Succeed())

		res := r.isSiteConfigValidated(siteConfig)
		Expect(res).To(BeFalse())
	})
})
