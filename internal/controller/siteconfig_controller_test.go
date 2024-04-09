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

	siteconfigv1alpha1 "github.com/sakhoury/siteconfig/api/v1alpha1"

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
		siteConfig        *siteconfigv1alpha1.SiteConfig
		pullSecret        *corev1.Secret
		testPullSecretVal = `{"auths":{"cloud.openshift.com":{"auth":"dXNlcjpwYXNzd29yZAo=","email":"r@r.com"}}}`
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&siteconfigv1alpha1.SiteConfig{}).
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

		siteConfig = &siteconfigv1alpha1.SiteConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:       clusterName,
				Namespace:  clusterNamespace,
				Finalizers: []string{siteConfigFinalizer},
			},
			Spec: siteconfigv1alpha1.SiteConfigSpec{
				ClusterName:            clusterName,
				PullSecretRef:          &corev1.LocalObjectReference{Name: pullSecret.Name},
				ClusterImageSetNameRef: "testimage:foobar",
				SSHPublicKey:           "test-ssh",
				BaseDomain:             "abcd",
				ClusterType:            siteconfigv1alpha1.ClusterTypeSNO,
				TemplateRefs: []siteconfigv1alpha1.TemplateRef{
					{Name: "test-cluster-template", Namespace: "default"}},
				Nodes: []siteconfigv1alpha1.NodeSpec{{
					BmcAddress:         "1:2:3:4",
					BmcCredentialsName: siteconfigv1alpha1.BmcCredentialsName{Name: "bmc"},
					TemplateRefs: []siteconfigv1alpha1.TemplateRef{
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
			WithStatusSubresource(&siteconfigv1alpha1.SiteConfig{}).
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
		siteConfig := &siteconfigv1alpha1.SiteConfig{
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
		siteConfig := &siteconfigv1alpha1.SiteConfig{
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
