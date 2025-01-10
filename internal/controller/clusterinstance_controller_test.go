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
	"encoding/base64"
	"fmt"
	"os"

	"go.uber.org/zap"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	bmh_v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
	"github.com/stolostron/siteconfig/internal/controller/configuration"
	"github.com/stolostron/siteconfig/internal/controller/deletion"
	cierrors "github.com/stolostron/siteconfig/internal/controller/errors"
	ai_templates "github.com/stolostron/siteconfig/internal/templates/assisted-installer"
	ibi_templates "github.com/stolostron/siteconfig/internal/templates/image-based-installer"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	TestClusterInstanceName      = "test-cluster"
	TestClusterInstanceNamespace = TestClusterInstanceName
	TestNode1Hostname            = "test-node-1"
	TestNode2Hostname            = "test-node-2"
	TestPullSecret               = "pull-secret"
	TestBMHSecret                = "bmh-secret"
	TestConfigMap1               = TestClusterInstanceName
	TestConfigMap2               = TestClusterInstanceName + "-2"
	OwnedByOtherCI               = "not-the-owner"
)

// Define API Version
const (
	AgentClusterInstallApiVersion = "extensions.ClusterDeploymentApiVersionbeta1"
	BareMetalHostApiVersion       = "metal3.io/v1alpha1"
	ClusterDeploymentApiVersion   = "hive.openshift.io/v1"
	ManagedClusterApiVersion      = "cluster.open-cluster-management.io/v1"
	NMStateConfigApiVersion       = "agent-install.openshift.io/v1beta1"
	V1ApiVersion                  = "v1"
)

// Define Kind
const (
	AgentClusterInstallKind = "AgentClusterInstall"
	BareMetalHostKind       = "BareMetalHost"
	ClusterDeploymentKind   = "ClusterDeployment"
	ConfigMapKind           = "ConfigMap"
	ManagedClusterKind      = "ManagedCluster"
	NMStateConfigKind       = "NMStateConfig"
	SecretKind              = "Secret"
)

var _ = Describe("Reconcile", func() {
	var (
		c          client.Client
		r          *ClusterInstanceReconciler
		ctx        = context.Background()
		testLogger = zap.NewNop().Named("Test")
		testParams = &ci.TestParams{
			ClusterName:      TestClusterInstanceName,
			ClusterNamespace: TestClusterInstanceNamespace,
			PullSecret:       TestPullSecret,
		}
		clusterInstance *v1alpha1.ClusterInstance
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			Build()

		configStore, err := configuration.NewConfigurationStore(configuration.NewDefaultConfiguration())
		Expect(err).ToNot(HaveOccurred())

		r = &ClusterInstanceReconciler{
			Client:          c,
			Scheme:          scheme.Scheme,
			Log:             testLogger,
			TmplEngine:      ci.NewTemplateEngine(),
			ConfigStore:     configStore,
			DeletionHandler: &deletion.DeletionHandler{Client: c, Logger: testLogger},
		}

		Expect(c.Create(ctx, testParams.GeneratePullSecret())).To(Succeed())

		clusterInstance = &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:       testParams.ClusterName,
				Namespace:  testParams.ClusterNamespace,
				Finalizers: []string{clusterInstanceFinalizer},
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				ClusterName:            testParams.ClusterName,
				PullSecretRef:          corev1.LocalObjectReference{Name: testParams.PullSecret},
				ClusterImageSetNameRef: "testimage:foobar",
				SSHPublicKey:           "test-ssh",
				BaseDomain:             "abcd",
				ClusterType:            v1alpha1.ClusterTypeSNO,
				TemplateRefs: []v1alpha1.TemplateRef{
					{Name: "test-cluster-template", Namespace: "default"}},
				Nodes: []v1alpha1.NodeSpec{{
					BmcAddress:         "192.0.2.1",
					BmcCredentialsName: v1alpha1.BmcCredentialsName{Name: "bmc"},
					TemplateRefs: []v1alpha1.TemplateRef{
						{Name: "test-node-template", Namespace: "default"}}}}},
		}
	})

	It("creates the correct ClusterInstance manifest", func() {
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		key := types.NamespacedName{
			Namespace: testParams.ClusterName,
			Name:      testParams.ClusterNamespace,
		}
		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))
	})

	It("doesn't error for a missing ClusterInstance", func() {
		key := types.NamespacedName{
			Namespace: testParams.ClusterName,
			Name:      testParams.ClusterNamespace,
		}
		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))
	})

	It("continues to validate the ClusterInstance when the ObjectMeta.Generation and ObservedGeneration are different", func() {
		generation := int64(2)
		clusterInstance.ObjectMeta.Generation = generation
		clusterInstance.Status = v1alpha1.ClusterInstanceStatus{
			ObservedGeneration: generation - 1,
		}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		key := types.NamespacedName{
			Namespace: testParams.ClusterName,
			Name:      testParams.ClusterNamespace,
		}
		Expect(c.Get(ctx, key, clusterInstance)).To(Succeed())

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		// Expect errors to occur in the ClusterInstance validations stage
		Expect(err).To(HaveOccurred())
		Expect(res).To(Equal(requeueWithDelay(DefaultValidationErrorDelay)))
	})

	It("pre-empts the reconcile-loop when the ObjectMeta.Generation and ObservedGeneration are the same", func() {
		generation := int64(2)
		clusterInstance.ObjectMeta.Generation = generation
		clusterInstance.Status = v1alpha1.ClusterInstanceStatus{
			ObservedGeneration: generation,
		}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		key := types.NamespacedName{
			Namespace: testParams.ClusterName,
			Name:      testParams.ClusterNamespace,
		}
		Expect(c.Get(ctx, key, clusterInstance)).To(Succeed())

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		// Although the ClusterInstance CR should fail validation, the expected behaviour of this test is that the
		// reconcile should stop early since we have intentionally set the ObservedGeneration to be the same as
		// ObjectMeta.Generation
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))
	})
})

var _ = Describe("handleFinalizer", func() {
	var (
		c                client.Client
		r                *ClusterInstanceReconciler
		ctx              = context.Background()
		testLogger       = zap.NewNop().Named("Test")
		clusterName      = TestClusterInstanceName
		clusterNamespace = TestClusterInstanceNamespace
	)

	ensureDeletionInProgressStatus := func(manifests []v1alpha1.ManifestReference) bool {
		Expect(len(manifests)).To(BeNumerically(">", 0))
		for _, m := range manifests {
			Expect(m.Status).To(Equal(v1alpha1.ManifestDeletionInProgress))
		}
		return true
	}

	var triggerAndVerifyFinalizerHandling = func(
		r *ClusterInstanceReconciler,
		clusterInstanceKey types.NamespacedName,
		expectedNumManifestsToBeDeleted int,
	) {
		// Trigger deletion of ClusterInstance.
		clusterInstance := &v1alpha1.ClusterInstance{}
		Expect(r.Client.Get(ctx, clusterInstanceKey, clusterInstance)).To(Succeed())
		Expect(r.Client.Delete(ctx, clusterInstance)).To(Succeed())

		// Fetch the ClusterInstance to get the DeletionTimestamp.
		Expect(r.Client.Get(ctx, clusterInstanceKey, clusterInstance)).To(Succeed())

		// First call of r.handleFinalizer should trigger deletion of rendered manifests.
		res, err := r.handleFinalizer(ctx, testLogger, clusterInstance)
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(requeueForDeletion()))
		Expect(r.Client.Get(ctx, clusterInstanceKey, clusterInstance)).To(Succeed())

		Expect(len(clusterInstance.Status.ManifestsRendered)).To(Equal(expectedNumManifestsToBeDeleted))
		Expect(clusterInstance.Status.ManifestsRendered).To(Satisfy(ensureDeletionInProgressStatus))

		// Second call of r.handleFinalizer should result in the deletion of all the rendered objects.
		res, err = r.handleFinalizer(ctx, testLogger, clusterInstance)
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(doNotRequeue()))

		// Check that the ClusterInstance is deleted.
		Expect(r.Client.Get(ctx, clusterInstanceKey, clusterInstance)).ToNot(Succeed())
	}

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			Build()

		r = &ClusterInstanceReconciler{
			Client:          c,
			Scheme:          scheme.Scheme,
			Log:             testLogger,
			DeletionHandler: &deletion.DeletionHandler{Client: c, Logger: testLogger},
		}
	})

	It("adds the finalizer if the ClusterInstance is not being deleted", func() {
		clusterInstance := &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: clusterNamespace,
			},
		}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		res, err := r.handleFinalizer(ctx, testLogger, clusterInstance)
		Expect(res).To(Equal(ctrl.Result{Requeue: true}))
		Expect(err).ToNot(HaveOccurred())

		Expect(c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), clusterInstance)).To(Succeed())
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

		res, err := r.handleFinalizer(ctx, testLogger, clusterInstance)
		Expect(res).To(Equal(ctrl.Result{}))
		Expect(err).ToNot(HaveOccurred())
	})

	It("deletes all rendered manifests owned-by ClusterInstance", func() {

		manifestName := TestClusterInstanceName
		manifest2Name := fmt.Sprintf("%s-2", TestClusterInstanceName)

		clusterInstance := &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:       clusterName,
				Namespace:  clusterNamespace,
				Finalizers: []string{clusterInstanceFinalizer},
			},
			Status: v1alpha1.ClusterInstanceStatus{
				ManifestsRendered: []v1alpha1.ManifestReference{
					{
						APIGroup:  ptr.To(ClusterDeploymentApiVersion),
						Kind:      ClusterDeploymentKind,
						Name:      manifestName,
						Namespace: clusterNamespace,
						SyncWave:  1,
						Status:    v1alpha1.ManifestRenderedSuccess,
					},
					{
						APIGroup:  ptr.To(BareMetalHostApiVersion),
						Kind:      BareMetalHostKind,
						Name:      manifestName,
						Namespace: clusterNamespace,
						SyncWave:  2,
						Status:    v1alpha1.ManifestRenderedSuccess,
					},
					{
						APIGroup: ptr.To(ManagedClusterApiVersion),
						Kind:     ManagedClusterKind,
						Name:     manifestName,
						SyncWave: 3,
						Status:   v1alpha1.ManifestRenderedSuccess,
					},
					{
						APIGroup:  ptr.To(V1ApiVersion),
						Kind:      ConfigMapKind,
						Name:      manifest2Name,
						Namespace: clusterNamespace,
						SyncWave:  4,
						Status:    v1alpha1.ManifestRenderedSuccess,
					},
					{
						APIGroup:  ptr.To(V1ApiVersion),
						Kind:      ConfigMapKind,
						Name:      manifestName,
						Namespace: clusterNamespace,
						SyncWave:  4,
						Status:    v1alpha1.ManifestRenderedSuccess,
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
				Labels: map[string]string{
					ci.OwnedByLabel: ci.GenerateOwnedByLabelValue(clusterInstance.Namespace, clusterInstance.Name),
				},
			},
		}
		Expect(c.Create(ctx, cd)).To(Succeed())

		bmh := &bmh_v1alpha1.BareMetalHost{
			ObjectMeta: metav1.ObjectMeta{
				Name:      manifestName,
				Namespace: clusterNamespace,
				Labels: map[string]string{
					ci.OwnedByLabel: ci.GenerateOwnedByLabelValue(clusterInstance.Namespace, clusterInstance.Name),
				},
			},
		}
		Expect(c.Create(ctx, bmh)).To(Succeed())

		mc := &clusterv1.ManagedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name: manifestName,
				Labels: map[string]string{
					ci.OwnedByLabel: ci.GenerateOwnedByLabelValue(clusterInstance.Namespace, clusterInstance.Name),
				},
			},
		}
		Expect(c.Create(ctx, mc)).To(Succeed())

		// This resource should not be deleted because the owned-by label is not set
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      manifest2Name,
				Namespace: clusterNamespace,
			},
		}
		Expect(c.Create(ctx, cm)).To(Succeed())

		// This resource should not be deleted because the ClusterInstance is not the owner
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      manifestName,
				Namespace: clusterNamespace,
				Labels: map[string]string{
					ci.OwnedByLabel: OwnedByOtherCI,
				},
			},
		}
		Expect(c.Create(ctx, cm)).To(Succeed())

		// Get the created manfiests to confirm they exist before calling finalizer
		key := types.NamespacedName{
			Name:      manifestName,
			Namespace: clusterNamespace,
		}
		key2 := types.NamespacedName{
			Name:      manifest2Name,
			Namespace: clusterNamespace,
		}
		keyMc := types.NamespacedName{
			Name: manifestName,
		}

		Expect(c.Get(ctx, key, cd)).To(Succeed())
		Expect(c.Get(ctx, key, bmh)).To(Succeed())
		Expect(c.Get(ctx, keyMc, mc)).To(Succeed())
		Expect(c.Get(ctx, key2, cm)).To(Succeed())
		Expect(c.Get(ctx, key, cm)).To(Succeed())

		clusterInstanceKey := client.ObjectKeyFromObject(clusterInstance)
		expectedNumManifestsToBeDeleted := 3 // ClusterDeployment, BareMetalHost, ManagedCluster
		triggerAndVerifyFinalizerHandling(r, clusterInstanceKey, expectedNumManifestsToBeDeleted)

		// Verify ClusterDeployment, BareMetalHost, ManagedCluster manifests are deleted
		Expect(c.Get(ctx, key, cd)).ToNot(Succeed())
		Expect(c.Get(ctx, key, bmh)).ToNot(Succeed())
		Expect(c.Get(ctx, keyMc, mc)).ToNot(Succeed())

		// Verify both ConfigMap manifests are NOT deleted
		Expect(c.Get(ctx, key2, cm)).To(Succeed())
		Expect(c.Get(ctx, key, cm)).To(Succeed())
	})

	It("does not fail to handle the finalizer when attempting to delete a missing manifest", func() {
		manifestName := TestClusterInstanceName
		clusterInstance := &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:       TestClusterInstanceName,
				Namespace:  TestClusterInstanceNamespace,
				Finalizers: []string{clusterInstanceFinalizer},
			},
			Status: v1alpha1.ClusterInstanceStatus{
				ManifestsRendered: []v1alpha1.ManifestReference{
					{
						APIGroup:  ptr.To(ClusterDeploymentApiVersion),
						Kind:      ClusterDeploymentKind,
						Name:      manifestName,
						Namespace: TestClusterInstanceNamespace,
						SyncWave:  1,
						Status:    v1alpha1.ManifestRenderedSuccess,
					},
					{
						// BMH resource will not be created
						APIGroup:  ptr.To(BareMetalHostApiVersion),
						Kind:      BareMetalHostKind,
						Name:      manifestName,
						Namespace: TestClusterInstanceNamespace,
						SyncWave:  2,
						Status:    v1alpha1.ManifestRenderedSuccess,
					},
					{
						APIGroup: ptr.To(ManagedClusterApiVersion),
						Kind:     ManagedClusterKind,
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
				Namespace: TestClusterInstanceNamespace,
				Labels: map[string]string{
					ci.OwnedByLabel: ci.GenerateOwnedByLabelValue(clusterInstance.Namespace, clusterInstance.Name),
				},
			},
		}
		Expect(c.Create(ctx, cd)).To(Succeed())

		mc := &clusterv1.ManagedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name: manifestName,
				Labels: map[string]string{
					ci.OwnedByLabel: ci.GenerateOwnedByLabelValue(clusterInstance.Namespace, clusterInstance.Name),
				},
			},
		}
		Expect(c.Create(ctx, mc)).To(Succeed())

		// Get the created manifests to confirm they exist before calling finalizer
		key := types.NamespacedName{
			Name:      manifestName,
			Namespace: TestClusterInstanceNamespace,
		}
		keyMc := types.NamespacedName{
			Name: manifestName,
		}
		Expect(c.Get(ctx, key, cd)).To(Succeed())
		Expect(c.Get(ctx, keyMc, mc)).To(Succeed())

		// BareMetalHost manifest is not created!
		bmh := &bmh_v1alpha1.BareMetalHost{}
		Expect(c.Get(ctx, key, bmh)).ToNot(Succeed())

		clusterInstanceKey := client.ObjectKeyFromObject(clusterInstance)
		expectedNumManifestsToBeDeleted := 2 // ClusterDeployment, ManagedCluster
		triggerAndVerifyFinalizerHandling(r, clusterInstanceKey, expectedNumManifestsToBeDeleted)

		// Ensure that rendered manifests are deleted
		Expect(c.Get(ctx, key, cd)).ToNot(Succeed())
		Expect(c.Get(ctx, keyMc, mc)).ToNot(Succeed())
	})

	When("a timeout occurs", func() {
		It("does not requeue the reconciler", func() {

			deleteWithoutTimeout := false
			testClient := fakeclient.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					return c.Get(ctx, key, obj, opts...)
				},
				Create: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					return c.Create(ctx, obj, opts...)
				},
				Delete: func(ctx context.Context, fclient client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					if deleteWithoutTimeout {
						return c.Delete(ctx, obj, opts...)
					}
					return cierrors.NewDeletionTimeoutError("")
				},
				Patch: func(ctx context.Context, client client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
					return c.Patch(ctx, obj, patch, opts...)
				},
				SubResourcePatch: func(ctx context.Context, client client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					return c.Status().Patch(ctx, obj, patch, opts...)
				},
			}).Build()

			r = &ClusterInstanceReconciler{
				Client:          testClient,
				Scheme:          scheme.Scheme,
				Log:             testLogger,
				DeletionHandler: &deletion.DeletionHandler{Client: c, Logger: testLogger},
			}

			manifestName := TestClusterInstanceName
			clusterInstance := &v1alpha1.ClusterInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:       clusterName,
					Namespace:  clusterNamespace,
					Finalizers: []string{clusterInstanceFinalizer},
				},
				Status: v1alpha1.ClusterInstanceStatus{
					ManifestsRendered: []v1alpha1.ManifestReference{
						{
							APIGroup:  ptr.To(ClusterDeploymentApiVersion),
							Kind:      ClusterDeploymentKind,
							Name:      manifestName,
							Namespace: clusterNamespace,
							SyncWave:  1,
							Status:    v1alpha1.ManifestRenderedSuccess,
						},
					},
				},
			}
			Expect(testClient.Create(ctx, clusterInstance)).To(Succeed())

			// Create BareMetalHost object
			bmh := &bmh_v1alpha1.BareMetalHost{
				ObjectMeta: metav1.ObjectMeta{
					Name:      manifestName,
					Namespace: clusterNamespace,
					Labels: map[string]string{
						ci.OwnedByLabel: ci.GenerateOwnedByLabelValue(clusterInstance.Namespace, clusterInstance.Name),
					},
					Finalizers: []string{"block-deletion"},
				},
			}
			Expect(testClient.Create(ctx, bmh)).To(Succeed())

			// Delete ClusterInstance for finalizer
			deleteWithoutTimeout = true
			Expect(testClient.Delete(ctx, clusterInstance)).To(Succeed())

			// Enable trigger timeout error on deletion
			deleteWithoutTimeout = false

			// Fetch the ClusterInstance to get the DeletionTimestamp
			Expect(testClient.Get(ctx, client.ObjectKeyFromObject(clusterInstance), clusterInstance)).To(Succeed())

			res, err := r.handleFinalizer(ctx, testLogger, clusterInstance)
			Expect(err).NotTo(HaveOccurred())
			Expect(res).To(Equal(ctrl.Result{Requeue: false}))
		})
	})
})

var _ = Describe("pruneManifests", func() {

	var (
		c          client.Client
		r          *ClusterInstanceReconciler
		ctx        = context.Background()
		testLogger = zap.NewNop().Named("Test")

		// objects to create for pruning test
		cdManifest, bmh1Manifest, bmh2Manifest, mcManifest, cm1Manifest, cm2Manifest map[string]interface{}

		// RenderedObject
		cdRenderedObject, bmh1RenderedObject, bmh2RenderedObject, mcRenderedObject, cm1RenderedObject, cm2RenderedObject ci.RenderedObject
		// list of objects
		objects []ci.RenderedObject

		// references for retrieving the objects
		cdKey, bmh1Key, bmh2Key, mcKey, cm1Key, cm2Key types.NamespacedName
		// list of keys
		objectKeys []types.NamespacedName

		manifestsRenderedRefs []v1alpha1.ManifestReference

		verifyPruningFn = func(clusterInstanceKey types.NamespacedName, pruneList, doNotPruneList []ci.RenderedObject, pruneKeys, doNotPruneKeys []types.NamespacedName) {
			Expect(len(pruneList)).To(Equal(len(pruneKeys)))
			Expect(len(doNotPruneList)).To(Equal(len(doNotPruneKeys)))

			for index, object := range pruneList {
				obj := object.GetObject()
				obj2 := &unstructured.Unstructured{}
				obj2.SetGroupVersionKind(obj.GroupVersionKind())
				Expect(c.Get(ctx, pruneKeys[index], obj2)).ToNot(Succeed())
			}

			for index, object := range doNotPruneList {
				obj := object.GetObject()
				obj2 := &unstructured.Unstructured{}
				obj2.SetGroupVersionKind(obj.GroupVersionKind())
				Expect(c.Get(ctx, doNotPruneKeys[index], obj2)).To(Succeed())
			}

			// Verify that clusterInstance.status.RenderedManifests are updated on second run of pruneManifests
			clusterInstance := &v1alpha1.ClusterInstance{}
			Expect(c.Get(ctx, clusterInstanceKey, clusterInstance)).To(Succeed())
			Expect(clusterInstance.Status.ManifestsRendered).To(Satisfy(func(manifests []v1alpha1.ManifestReference) bool {
				for _, obj := range doNotPruneList {
					if obj.GetName() != clusterInstanceKey.Name {
						continue
					}
					_, err := v1alpha1.IndexOfManifestByIdentity(obj.ManifestReference(), manifests)
					Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("object (%s) should be in RenderedManifests", obj.GetResourceId()))
				}

				for _, obj := range pruneList {
					if obj.GetName() != clusterInstanceKey.Name {
						continue
					}
					_, err := v1alpha1.IndexOfManifestByIdentity(obj.ManifestReference(), manifests)
					Expect(err).To(HaveOccurred(), fmt.Sprintf("object (%s) should not be in RenderedManifests [%v]", obj.GetResourceId(), manifests))
				}

				return true
			}))
		}
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			Build()

		r = &ClusterInstanceReconciler{
			Client:          c,
			Scheme:          scheme.Scheme,
			Log:             testLogger,
			TmplEngine:      ci.NewTemplateEngine(),
			DeletionHandler: &deletion.DeletionHandler{Client: c, Logger: testLogger},
		}

		annotations := map[string]string{
			ci.WaveAnnotation: "0",
		}
		labels := map[string]string{
			ci.OwnedByLabel: ci.GenerateOwnedByLabelValue(TestClusterInstanceNamespace, TestClusterInstanceName),
		}

		cdManifest = map[string]interface{}{
			"apiVersion": ClusterDeploymentApiVersion,
			"kind":       ClusterDeploymentKind,
			"metadata": map[string]interface{}{
				"name":        TestClusterInstanceName,
				"namespace":   TestClusterInstanceNamespace,
				"annotations": annotations,
				"labels":      labels,
			},
		}

		bmh1Manifest = map[string]interface{}{
			"apiVersion": BareMetalHostApiVersion,
			"kind":       BareMetalHostKind,
			"metadata": map[string]interface{}{
				"name":        TestNode1Hostname,
				"namespace":   TestClusterInstanceNamespace,
				"annotations": annotations,
				"labels":      labels,
			},
		}

		bmh2Manifest = map[string]interface{}{
			"apiVersion": BareMetalHostApiVersion,
			"kind":       BareMetalHostKind,
			"metadata": map[string]interface{}{
				"name":        TestNode2Hostname,
				"namespace":   TestClusterInstanceNamespace,
				"annotations": annotations,
				"labels":      labels,
			},
		}

		mcManifest = map[string]interface{}{
			"apiVersion": ManagedClusterApiVersion,
			"kind":       ManagedClusterKind,
			"metadata": map[string]interface{}{
				"name":        TestClusterInstanceName,
				"annotations": annotations,
				"labels":      labels,
			},
		}

		cm1Manifest = map[string]interface{}{
			"apiVersion": V1ApiVersion,
			"kind":       ConfigMapKind,
			"metadata": map[string]interface{}{
				"name":        TestConfigMap1,
				"namespace":   TestClusterInstanceNamespace,
				"annotations": annotations,
				"labels":      labels,
			},
		}

		cm2Manifest = map[string]interface{}{
			"apiVersion": V1ApiVersion,
			"kind":       ConfigMapKind,
			"metadata": map[string]interface{}{
				"name":        TestConfigMap2,
				"namespace":   TestClusterInstanceNamespace,
				"annotations": annotations,
				"labels":      labels,
			},
		}

		// define keys
		cdKey = types.NamespacedName{Name: TestClusterInstanceName, Namespace: TestClusterInstanceNamespace}
		bmh1Key = types.NamespacedName{Name: TestNode1Hostname, Namespace: TestClusterInstanceNamespace}
		bmh2Key = types.NamespacedName{Name: TestNode2Hostname, Namespace: TestClusterInstanceNamespace}
		mcKey = types.NamespacedName{Name: TestClusterInstanceName}
		cm1Key = types.NamespacedName{Name: TestConfigMap1, Namespace: TestClusterInstanceNamespace}
		cm2Key = types.NamespacedName{Name: TestConfigMap2, Namespace: TestClusterInstanceNamespace}

		// convert objects to RenderedObject
		Expect(cdRenderedObject.SetObject(cdManifest)).ToNot(HaveOccurred())
		Expect(bmh1RenderedObject.SetObject(bmh1Manifest)).ToNot(HaveOccurred())
		Expect(bmh2RenderedObject.SetObject(bmh2Manifest)).ToNot(HaveOccurred())
		Expect(mcRenderedObject.SetObject(mcManifest)).ToNot(HaveOccurred())
		Expect(cm1RenderedObject.SetObject(cm1Manifest)).ToNot(HaveOccurred())
		Expect(cm2RenderedObject.SetObject(cm2Manifest)).ToNot(HaveOccurred())

		objects = []ci.RenderedObject{cdRenderedObject, bmh1RenderedObject, bmh2RenderedObject, mcRenderedObject, cm1RenderedObject, cm2RenderedObject}
		objectKeys = []types.NamespacedName{cdKey, bmh1Key, bmh2Key, mcKey, cm1Key, cm2Key}

		// Create the manifests and confirm they exist
		manifestsRenderedRefs = []v1alpha1.ManifestReference(nil)
		for index, object := range objects {
			obj := object.GetObject()
			Expect(c.Create(ctx, &obj)).To(Succeed())
			obj2 := &unstructured.Unstructured{}
			obj2.SetGroupVersionKind(obj.GroupVersionKind())
			Expect(c.Get(ctx, objectKeys[index], obj2)).To(Succeed())

			manifestsRenderedRefs = append(manifestsRenderedRefs, *object.ManifestReference())
		}
	})

	It("prunes manifests defined at the cluster-level", func() {

		pruneList := []ci.RenderedObject{
			cdRenderedObject, bmh1RenderedObject, bmh2RenderedObject, mcRenderedObject, cm1RenderedObject, cm2RenderedObject,
		}
		pruneKeys := []types.NamespacedName{cdKey, bmh1Key, bmh2Key, mcKey, cm1Key, cm2Key}

		doNotPruneList := []ci.RenderedObject{}
		doNotPruneKeys := []types.NamespacedName{}

		clusterInstanceKey := types.NamespacedName{Namespace: TestClusterInstanceNamespace, Name: TestClusterInstanceName}
		clusterInstance := &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterInstanceKey.Name,
				Namespace: clusterInstanceKey.Namespace,
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				PruneManifests: []v1alpha1.ResourceRef{
					{
						APIVersion: V1ApiVersion,
						Kind:       ConfigMapKind,
					},
					{
						APIVersion: ClusterDeploymentApiVersion,
						Kind:       ClusterDeploymentKind,
					},
					{
						APIVersion: ManagedClusterApiVersion,
						Kind:       ManagedClusterKind,
					},
					{
						APIVersion: BareMetalHostApiVersion,
						Kind:       BareMetalHostKind,
					},
				},
				Nodes: []v1alpha1.NodeSpec{
					{
						HostName: TestNode1Hostname,
					},
					{
						HostName: TestNode2Hostname,
					},
				},
			},
			Status: v1alpha1.ClusterInstanceStatus{
				ManifestsRendered: manifestsRenderedRefs,
			},
		}
		// Create the ClusterInstance CR
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		// key := client.ObjectKeyFromObject(clusterInstance)
		Expect(c.Get(ctx, clusterInstanceKey, clusterInstance)).To(Succeed())
		pruningCompleted, err := r.pruneManifests(ctx, testLogger, clusterInstance, pruneList)
		Expect(err).ToNot(HaveOccurred())
		Expect(pruningCompleted).To(BeFalse())

		Expect(c.Get(ctx, clusterInstanceKey, clusterInstance)).To(Succeed())
		for _, m := range clusterInstance.Status.ManifestsRendered {
			Expect(m.Status).Should(Equal(v1alpha1.ManifestDeletionInProgress))
		}

		pruningCompleted, err = r.pruneManifests(ctx, testLogger, clusterInstance, pruneList)
		Expect(err).ToNot(HaveOccurred())
		Expect(pruningCompleted).To(BeTrue())

		// Expect the objects previously created to be deleted after pruneManifests is called
		verifyPruningFn(clusterInstanceKey, pruneList, doNotPruneList, pruneKeys, doNotPruneKeys)
	})

	It("prunes manifests defined at the node-level", func() {
		pruneList := []ci.RenderedObject{bmh1RenderedObject}
		pruneKeys := []types.NamespacedName{bmh1Key}

		doNotPruneList := []ci.RenderedObject{
			cdRenderedObject, bmh2RenderedObject, mcRenderedObject, cm1RenderedObject, cm2RenderedObject,
		}
		doNotPruneKeys := []types.NamespacedName{cdKey, bmh2Key, mcKey, cm1Key, cm2Key}

		clusterInstanceKey := types.NamespacedName{Namespace: TestClusterInstanceNamespace, Name: TestClusterInstanceName}
		clusterInstance := &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterInstanceKey.Name,
				Namespace: clusterInstanceKey.Namespace,
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				PruneManifests: []v1alpha1.ResourceRef{},
				Nodes: []v1alpha1.NodeSpec{
					{
						HostName: TestNode1Hostname,
						PruneManifests: []v1alpha1.ResourceRef{
							{
								APIVersion: BareMetalHostApiVersion,
								Kind:       BareMetalHostKind,
							},
						},
					},
					{
						HostName: TestNode2Hostname,
					},
				},
			},
			Status: v1alpha1.ClusterInstanceStatus{
				ManifestsRendered: manifestsRenderedRefs,
			},
		}
		// Create the ClusterInstance CR
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		Expect(c.Get(ctx, clusterInstanceKey, clusterInstance)).To(Succeed())

		pruningCompleted, err := r.pruneManifests(ctx, testLogger, clusterInstance, pruneList)
		Expect(err).ToNot(HaveOccurred())
		Expect(pruningCompleted).To(BeFalse())

		Expect(c.Get(ctx, clusterInstanceKey, clusterInstance)).To(Succeed())
		Expect(clusterInstance.Status.ManifestsRendered).To(Satisfy(func(manifests []v1alpha1.ManifestReference) bool {
			for _, obj := range pruneList {
				obj.ManifestReference()
				index, err := v1alpha1.IndexOfManifestByIdentity(obj.ManifestReference(), manifests)
				Expect(err).ToNot(HaveOccurred(),
					fmt.Sprintf("object %s missing from RenderedManifests", obj.GetResourceId()))
				Expect(manifests[index].Status).To(Equal(v1alpha1.ManifestDeletionInProgress),
					fmt.Sprintf("object's %s status should be %s", obj.GetResourceId(), v1alpha1.ManifestDeletionInProgress))
			}
			return true
		}))

		pruningCompleted, err = r.pruneManifests(ctx, testLogger, clusterInstance, pruneList)
		Expect(err).ToNot(HaveOccurred())
		Expect(pruningCompleted).To(BeTrue())

		Expect(c.Get(ctx, clusterInstanceKey, clusterInstance)).To(Succeed())

		// Expect the objects previously created to be deleted after pruneManifests is called
		verifyPruningFn(clusterInstanceKey, pruneList, doNotPruneList, pruneKeys, doNotPruneKeys)
	})

	It("does not prune manifests not-owned by the ClusterInstance", func() {

		pruneList := []ci.RenderedObject{
			mcRenderedObject, cm1RenderedObject, cm2RenderedObject,
		}

		doNotPruneList := []ci.RenderedObject{
			cdRenderedObject, bmh1RenderedObject, bmh2RenderedObject, mcRenderedObject, cm1RenderedObject, cm2RenderedObject,
		}
		doNotPruneKeys := []types.NamespacedName{cdKey, bmh1Key, bmh2Key, mcKey, cm1Key, cm2Key}

		clusterInstanceKey := types.NamespacedName{Namespace: TestClusterInstanceNamespace, Name: "not-the-owner"}
		clusterInstance := &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterInstanceKey.Name,
				Namespace: clusterInstanceKey.Namespace,
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				PruneManifests: []v1alpha1.ResourceRef{
					{
						APIVersion: V1ApiVersion,
						Kind:       ConfigMapKind,
					},
					{
						APIVersion: ManagedClusterApiVersion,
						Kind:       ManagedClusterKind,
					},
				},
				Nodes: []v1alpha1.NodeSpec{
					{
						HostName: TestNode1Hostname,
					},
					{
						HostName: TestNode2Hostname,
						PruneManifests: []v1alpha1.ResourceRef{
							{
								APIVersion: BareMetalHostApiVersion,
								Kind:       BareMetalHostKind,
							},
						},
					},
				},
			},
			Status: v1alpha1.ClusterInstanceStatus{
				ManifestsRendered: manifestsRenderedRefs,
			},
		}
		// Create the ClusterInstance CR
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		// Expect the objects previously created to not be deleted after pruneManifests is called
		expectedPruneList := []ci.RenderedObject{}
		expectedPruneKeys := []types.NamespacedName{}

		Expect(c.Get(ctx, clusterInstanceKey, clusterInstance)).To(Succeed())
		pruningCompleted, err := r.pruneManifests(ctx, testLogger, clusterInstance, pruneList)
		Expect(err).ToNot(HaveOccurred())
		Expect(pruningCompleted).To(BeTrue())

		verifyPruningFn(clusterInstanceKey, expectedPruneList, doNotPruneList, expectedPruneKeys, doNotPruneKeys)

	})

})

var _ = Describe("handleValidate", func() {
	var (
		c          client.Client
		r          *ClusterInstanceReconciler
		ctx        = context.Background()
		testLogger = zap.NewNop().Named("Test")
		testParams = &ci.TestParams{
			BmcCredentialsName:  TestBMHSecret,
			ClusterName:         TestClusterInstanceName,
			ClusterNamespace:    TestClusterInstanceNamespace,
			ClusterImageSetName: "testimage:foobar",
			ExtraManifestName:   "extra-manifest",
			ClusterTemplateRef:  "cluster-template-ref",
			NodeTemplateRef:     "node-template-ref",
			PullSecret:          TestPullSecret,
		}
		clusterInstance *v1alpha1.ClusterInstance
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			Build()

		r = &ClusterInstanceReconciler{
			Client:          c,
			Scheme:          scheme.Scheme,
			Log:             testLogger,
			TmplEngine:      ci.NewTemplateEngine(),
			DeletionHandler: &deletion.DeletionHandler{Client: c, Logger: testLogger},
		}

		ci.SetupTestResources(ctx, c, testParams)
		clusterInstance = testParams.GenerateSNOClusterInstance()
	})

	AfterEach(func() {
		ci.TeardownTestResources(ctx, c, testParams)
	})

	It("successfully sets the ClusterInstanceValidated condition to true for a valid ClusterInstance", func() {
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := r.handleValidate(ctx, testLogger, clusterInstance)
		Expect(err).ToNot(HaveOccurred())

		key := types.NamespacedName{
			Name:      testParams.ClusterName,
			Namespace: testParams.ClusterNamespace,
		}
		Expect(c.Get(ctx, key, clusterInstance)).To(Succeed())
		matched := false
		for _, cond := range clusterInstance.Status.Conditions {
			if cond.Type == string(v1alpha1.ClusterInstanceValidated) && cond.Status == metav1.ConditionTrue {
				matched = true
			}
		}
		Expect(matched).To(BeTrue())
	})

	It("successfully sets the ClusterInstanceValidated condition to false for an invalid ClusterInstance", func() {
		clusterInstance.Spec.ClusterName = ""
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := r.handleValidate(ctx, testLogger, clusterInstance)
		Expect(err).To(HaveOccurred())

		key := types.NamespacedName{
			Name:      testParams.ClusterName,
			Namespace: testParams.ClusterNamespace,
		}
		Expect(c.Get(ctx, key, clusterInstance)).To(Succeed())
		matched := false
		for _, cond := range clusterInstance.Status.Conditions {
			if cond.Type == string(v1alpha1.ClusterInstanceValidated) && cond.Status == metav1.ConditionFalse {
				matched = true
			}
		}
		Expect(matched).To(BeTrue())
	})

	It("does not require a reconcile when the ClusterInstanceValidated condition remains unchanged", func() {
		clusterInstance.Status.Conditions = []metav1.Condition{
			{
				Type:    string(v1alpha1.ClusterInstanceValidated),
				Reason:  string(v1alpha1.Completed),
				Status:  metav1.ConditionTrue,
				Message: "Validation succeeded",
			},
		}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := r.handleValidate(ctx, testLogger, clusterInstance)
		Expect(err).ToNot(HaveOccurred())

		key := types.NamespacedName{
			Name:      testParams.ClusterName,
			Namespace: testParams.ClusterNamespace,
		}
		Expect(c.Get(ctx, key, clusterInstance)).To(Succeed())
		matched := false
		for _, cond := range clusterInstance.Status.Conditions {
			if cond.Type == string(v1alpha1.ClusterInstanceValidated) && cond.Status == metav1.ConditionTrue {
				matched = true
			}
		}
		Expect(matched).To(BeTrue())
	})

	It("requires a reconcile when the ClusterInstanceValidated condition has changed", func() {
		clusterInstance.Status.Conditions = []metav1.Condition{
			{
				Type:    string(v1alpha1.ClusterInstanceValidated),
				Reason:  string(v1alpha1.Failed),
				Status:  metav1.ConditionFalse,
				Message: "Validation failed",
			},
		}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := r.handleValidate(ctx, testLogger, clusterInstance)
		Expect(err).ToNot(HaveOccurred())

		key := types.NamespacedName{
			Name:      testParams.ClusterName,
			Namespace: testParams.ClusterNamespace,
		}
		Expect(c.Get(ctx, key, clusterInstance)).To(Succeed())
		matched := false
		for _, cond := range clusterInstance.Status.Conditions {
			if cond.Type == string(v1alpha1.ClusterInstanceValidated) && cond.Status == metav1.ConditionTrue {
				matched = true
			}
		}
		Expect(matched).To(BeTrue())
	})

})

var _ = Describe("handleRenderTemplates", func() {
	var (
		c          client.Client
		r          *ClusterInstanceReconciler
		ctx        = context.Background()
		testLogger = zap.NewNop().Named("Test")
		testParams = &ci.TestParams{
			BmcCredentialsName:  TestBMHSecret,
			ClusterName:         TestClusterInstanceName,
			ClusterNamespace:    TestClusterInstanceNamespace,
			ClusterImageSetName: "testimage:foobar",
			ExtraManifestName:   "extra-manifest",
			ClusterTemplateRef:  "cluster-template-ref",
			NodeTemplateRef:     "node-template-ref",
			PullSecret:          TestPullSecret,
		}
		clusterInstance *v1alpha1.ClusterInstance
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			Build()

		r = &ClusterInstanceReconciler{
			Client:          c,
			Scheme:          scheme.Scheme,
			Log:             testLogger,
			TmplEngine:      ci.NewTemplateEngine(),
			DeletionHandler: &deletion.DeletionHandler{Client: c, Logger: testLogger},
		}

		ci.SetupTestResources(ctx, c, testParams)
		clusterInstance = testParams.GenerateSNOClusterInstance()
	})

	AfterEach(func() {
		ci.TeardownTestResources(ctx, c, testParams)
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
  name: "{{ .Spec.ClusterName }}"
  namespace: "{{ .Spec.ClusterName }}"
  annotations:
    siteconfig.open-cluster-management.io/sync-wave: "1"
kind: Test
spec:
  name: "{{ .Spec.ClusterNamee }}"`

		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "default",
			},
			Data: map[string]string{"Test": templateStr},
		}
		Expect(c.Create(ctx, cm)).To(Succeed())
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := r.handleValidate(ctx, testLogger, clusterInstance)
		Expect(err).ToNot(HaveOccurred())

		rendered, err := r.handleRenderTemplates(ctx, testLogger, clusterInstance)
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
			if cond.Type == string(v1alpha1.RenderedTemplates) && cond.Status == metav1.ConditionFalse {
				matched = true
			}
		}
		Expect(matched).To(Equal(true), "Condition %s was not found", v1alpha1.RenderedTemplates)
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
  name: "{{ .Spec.ClusterName }}"
  namespace: "{{ .Spec.ClusterName }}"
  annotations:
    siteconfig.open-cluster-management.io/sync-wave: "1"
kind: Test
spec:
  name: "{{ .Spec.ClusterName }}"`

		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "default",
			},
			Data: map[string]string{"Test": templateStr},
		}
		Expect(c.Create(ctx, cm)).To(Succeed())
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := r.handleValidate(ctx, testLogger, clusterInstance)
		Expect(err).ToNot(HaveOccurred())

		rendered, err := r.handleRenderTemplates(ctx, testLogger, clusterInstance)
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
				Type:   string(v1alpha1.ClusterInstanceValidated),
				Reason: string(v1alpha1.Completed),
				Status: metav1.ConditionTrue,
			},
			{
				Type:   string(v1alpha1.RenderedTemplates),
				Reason: string(v1alpha1.Completed),
				Status: metav1.ConditionTrue,
			},
			{
				Type:   string(v1alpha1.RenderedTemplatesValidated),
				Reason: string(v1alpha1.Completed),
				Status: metav1.ConditionTrue,
			},
			{
				Type:   string(v1alpha1.RenderedTemplatesApplied),
				Reason: string(v1alpha1.Completed),
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

type suppressManifestTestArgs struct {
	ClusterLevelSuppressedManifests []string
	NodeLevelSuppressedManifests    [][]string
	ExpectedManifests               []v1alpha1.ManifestReference
}

var _ = DescribeTable("updateSuppressedManifestsStatus",
	func(
		ciSpec v1alpha1.ClusterInstanceSpec,
		args suppressManifestTestArgs,
	) {

		ctx := context.Background()

		c := fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			Build()
		testLogger := zap.NewNop().Named("Test")

		r := &ClusterInstanceReconciler{
			Client:          c,
			Scheme:          scheme.Scheme,
			Log:             testLogger,
			TmplEngine:      ci.NewTemplateEngine(),
			DeletionHandler: &deletion.DeletionHandler{Client: c, Logger: testLogger},
		}

		clusterInstance := &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      TestClusterInstanceName,
				Namespace: TestClusterInstanceNamespace,
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				ClusterName:         TestClusterInstanceName,
				SuppressedManifests: args.ClusterLevelSuppressedManifests,
				Nodes: []v1alpha1.NodeSpec{
					{
						HostName:            TestNode1Hostname,
						Role:                "master",
						BmcAddress:          "192.0.2.1",
						SuppressedManifests: make([]string, 0),
					},
					{
						HostName:            TestNode2Hostname,
						Role:                "master",
						BmcAddress:          "192.0.2.2",
						SuppressedManifests: make([]string, 0),
					},
				}},
			Status: v1alpha1.ClusterInstanceStatus{
				ManifestsRendered: []v1alpha1.ManifestReference{
					{
						APIGroup:  ptr.To(ClusterDeploymentApiVersion),
						Kind:      ClusterDeploymentKind,
						Name:      TestClusterInstanceName,
						Namespace: TestClusterInstanceNamespace,
						Status:    v1alpha1.ManifestRenderedSuccess,
						SyncWave:  0,
					},
					{
						APIGroup:  ptr.To(AgentClusterInstallApiVersion),
						Kind:      AgentClusterInstallKind,
						Name:      TestClusterInstanceName,
						Namespace: TestClusterInstanceNamespace,
						Status:    v1alpha1.ManifestRenderedSuccess,
						SyncWave:  0,
					},
					{
						APIGroup:  ptr.To(BareMetalHostApiVersion),
						Kind:      BareMetalHostKind,
						Name:      TestNode1Hostname,
						Namespace: TestClusterInstanceNamespace,
						Status:    v1alpha1.ManifestRenderedSuccess,
						SyncWave:  0,
					},
					{
						APIGroup:  ptr.To(BareMetalHostApiVersion),
						Kind:      BareMetalHostKind,
						Name:      TestNode2Hostname,
						Namespace: TestClusterInstanceNamespace,
						Status:    v1alpha1.ManifestRenderedSuccess,
						SyncWave:  0,
					},
					{
						APIGroup:  ptr.To(NMStateConfigApiVersion),
						Kind:      NMStateConfigKind,
						Name:      TestNode1Hostname,
						Namespace: TestClusterInstanceNamespace,
						Status:    v1alpha1.ManifestRenderedSuccess,
						SyncWave:  0,
					},
					{
						APIGroup:  ptr.To(NMStateConfigApiVersion),
						Kind:      NMStateConfigKind,
						Name:      TestNode2Hostname,
						Namespace: TestClusterInstanceNamespace,
						Status:    v1alpha1.ManifestRenderedSuccess,
						SyncWave:  0,
					},
				},
			},
		}

		// Define node-level suppressed manifests
		Expect(len(args.NodeLevelSuppressedManifests) <= 2).To(BeTrue())
		for k, v := range args.NodeLevelSuppressedManifests {
			clusterInstance.Spec.Nodes[k].SuppressedManifests = append(
				clusterInstance.Spec.Nodes[k].SuppressedManifests, v...)
		}

		// Create the ClusterInstance CR
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		suppressList := make([]ci.RenderedObject, 0)
		for _, manifestRef := range clusterInstance.Status.ManifestsRendered {

			shouldSuppress := false
			// check cluster-level suppressed manifests args
			for _, m := range args.ClusterLevelSuppressedManifests {
				if manifestRef.Kind == m {
					shouldSuppress = true
				}
			}

			// check node-level suppressed manifests args
			for k, nodeManifests := range args.NodeLevelSuppressedManifests {
				nodeName := clusterInstance.Spec.Nodes[k].HostName
				for _, m := range nodeManifests {
					if manifestRef.Kind == m && manifestRef.Name == nodeName {
						shouldSuppress = true
					}
				}
			}

			if !shouldSuppress {
				continue
			}

			object := ci.RenderedObject{}
			err := object.SetObject(map[string]interface{}{
				"apiVersion": *manifestRef.APIGroup,
				"kind":       manifestRef.Kind,
				"metadata": map[string]interface{}{
					"name":      manifestRef.Name,
					"namespace": manifestRef.Namespace,
					"annotations": map[string]string{
						ci.WaveAnnotation: fmt.Sprintf("%d", manifestRef.SyncWave),
					},
					"labels": map[string]string{
						ci.OwnedByLabel: ci.GenerateOwnedByLabelValue(clusterInstance.Namespace,
							clusterInstance.Name),
					},
				},
			})
			Expect(err).ToNot(HaveOccurred())
			suppressList = append(suppressList, object)
		}

		err := r.updateSuppressedManifestsStatus(ctx, testLogger, clusterInstance, suppressList)
		Expect(err).ToNot(HaveOccurred())

		// Verify handling of suppression
		key := types.NamespacedName{
			Name:      clusterInstance.Name,
			Namespace: clusterInstance.Namespace,
		}
		Expect(c.Get(ctx, key, clusterInstance)).To(Succeed())
		for _, expManifest := range args.ExpectedManifests {
			index, err := v1alpha1.IndexOfManifestByIdentity(&expManifest, clusterInstance.Status.ManifestsRendered)
			Expect(err).ToNot(HaveOccurred())
			Expect(clusterInstance.Status.ManifestsRendered[index].Status).To(Equal(expManifest.Status))
		}
	},

	Entry("does not suppress manifests if nothing is specified",
		v1alpha1.ClusterInstanceSpec{},
		suppressManifestTestArgs{
			ClusterLevelSuppressedManifests: []string{},
			NodeLevelSuppressedManifests:    [][]string{{}, {}},
			ExpectedManifests: []v1alpha1.ManifestReference{
				{
					APIGroup:  ptr.To(ClusterDeploymentApiVersion),
					Kind:      ClusterDeploymentKind,
					Name:      TestClusterInstanceName,
					Namespace: TestClusterInstanceNamespace,
					Status:    v1alpha1.ManifestRenderedSuccess,
					SyncWave:  0,
				},
				{
					APIGroup:  ptr.To(AgentClusterInstallApiVersion),
					Kind:      AgentClusterInstallKind,
					Name:      TestClusterInstanceName,
					Namespace: TestClusterInstanceNamespace,
					Status:    v1alpha1.ManifestRenderedSuccess,
					SyncWave:  0,
				},
				{
					APIGroup:  ptr.To(BareMetalHostApiVersion),
					Kind:      BareMetalHostKind,
					Name:      TestNode1Hostname,
					Namespace: TestClusterInstanceNamespace,
					Status:    v1alpha1.ManifestRenderedSuccess,
					SyncWave:  0,
				},
				{
					APIGroup:  ptr.To(BareMetalHostApiVersion),
					Kind:      BareMetalHostKind,
					Name:      TestNode2Hostname,
					Namespace: TestClusterInstanceNamespace,
					Status:    v1alpha1.ManifestRenderedSuccess,
					SyncWave:  0,
				},
				{
					APIGroup:  ptr.To(NMStateConfigApiVersion),
					Kind:      NMStateConfigKind,
					Name:      TestNode1Hostname,
					Namespace: TestClusterInstanceNamespace,
					Status:    v1alpha1.ManifestRenderedSuccess,
					SyncWave:  0,
				},
				{
					APIGroup:  ptr.To(NMStateConfigApiVersion),
					Kind:      NMStateConfigKind,
					Name:      TestNode2Hostname,
					Namespace: TestClusterInstanceNamespace,
					Status:    v1alpha1.ManifestRenderedSuccess,
					SyncWave:  0,
				},
			},
		}),

	Entry("correctly suppresses cluster-level manifests when specified",
		v1alpha1.ClusterInstanceSpec{},
		suppressManifestTestArgs{
			ClusterLevelSuppressedManifests: []string{ClusterDeploymentKind},
			NodeLevelSuppressedManifests:    [][]string{{}, {}},
			ExpectedManifests: []v1alpha1.ManifestReference{
				{
					APIGroup:  ptr.To(ClusterDeploymentApiVersion),
					Kind:      ClusterDeploymentKind,
					Name:      TestClusterInstanceName,
					Namespace: TestClusterInstanceNamespace,
					Status:    v1alpha1.ManifestSuppressed,
				},
				{
					APIGroup:  ptr.To(AgentClusterInstallApiVersion),
					Kind:      AgentClusterInstallKind,
					Name:      TestClusterInstanceName,
					Namespace: TestClusterInstanceNamespace,
					Status:    v1alpha1.ManifestRenderedSuccess,
				},
				{
					APIGroup:  ptr.To(BareMetalHostApiVersion),
					Kind:      BareMetalHostKind,
					Name:      TestNode1Hostname,
					Namespace: TestClusterInstanceNamespace,
					Status:    v1alpha1.ManifestRenderedSuccess,
				},
				{
					APIGroup:  ptr.To(BareMetalHostApiVersion),
					Kind:      BareMetalHostKind,
					Name:      TestNode2Hostname,
					Namespace: TestClusterInstanceNamespace,
					Status:    v1alpha1.ManifestRenderedSuccess,
				},
				{
					APIGroup:  ptr.To(NMStateConfigApiVersion),
					Kind:      NMStateConfigKind,
					Name:      TestNode1Hostname,
					Namespace: TestClusterInstanceNamespace,
					Status:    v1alpha1.ManifestRenderedSuccess,
				},
				{
					APIGroup:  ptr.To(NMStateConfigApiVersion),
					Kind:      NMStateConfigKind,
					Name:      TestNode2Hostname,
					Namespace: TestClusterInstanceNamespace,
					Status:    v1alpha1.ManifestRenderedSuccess,
				},
			},
		}),

	Entry("correctly suppresses cluster and node level manifests when specified",
		v1alpha1.ClusterInstanceSpec{},
		suppressManifestTestArgs{
			ClusterLevelSuppressedManifests: []string{ClusterDeploymentKind},
			NodeLevelSuppressedManifests: [][]string{
				{NMStateConfigKind}, // suppress NMStateConfig for node[0]
				{BareMetalHostKind}, // suppress BareMetalHost for node[1]
			},
			ExpectedManifests: []v1alpha1.ManifestReference{
				{
					APIGroup:  ptr.To(ClusterDeploymentApiVersion),
					Kind:      ClusterDeploymentKind,
					Name:      TestClusterInstanceName,
					Namespace: TestClusterInstanceNamespace,
					Status:    v1alpha1.ManifestSuppressed,
				},
				{
					APIGroup:  ptr.To(AgentClusterInstallApiVersion),
					Kind:      AgentClusterInstallKind,
					Name:      TestClusterInstanceName,
					Namespace: TestClusterInstanceNamespace,
					Status:    v1alpha1.ManifestRenderedSuccess,
				},
				{
					APIGroup:  ptr.To(BareMetalHostApiVersion),
					Kind:      BareMetalHostKind,
					Name:      TestNode1Hostname,
					Namespace: TestClusterInstanceNamespace,
					Status:    v1alpha1.ManifestRenderedSuccess,
				},
				{
					APIGroup:  ptr.To(BareMetalHostApiVersion),
					Kind:      BareMetalHostKind,
					Name:      TestNode2Hostname,
					Namespace: TestClusterInstanceNamespace,
					Status:    v1alpha1.ManifestSuppressed,
				},
				{
					APIGroup:  ptr.To(NMStateConfigApiVersion),
					Kind:      NMStateConfigKind,
					Name:      TestNode1Hostname,
					Namespace: TestClusterInstanceNamespace,
					Status:    v1alpha1.ManifestSuppressed,
				},
				{
					APIGroup:  ptr.To(NMStateConfigApiVersion),
					Kind:      NMStateConfigKind,
					Name:      TestNode2Hostname,
					Namespace: TestClusterInstanceNamespace,
					Status:    v1alpha1.ManifestRenderedSuccess,
				},
			},
		}),

	Entry("correctly suppresses node level manifests specified globally in ClusterInstance.Spec.SuppressedManifests",
		v1alpha1.ClusterInstanceSpec{},
		suppressManifestTestArgs{
			ClusterLevelSuppressedManifests: []string{BareMetalHostKind},
			NodeLevelSuppressedManifests:    [][]string{{""}, {""}},
			ExpectedManifests: []v1alpha1.ManifestReference{
				{
					APIGroup:  ptr.To(ClusterDeploymentApiVersion),
					Kind:      ClusterDeploymentKind,
					Name:      TestClusterInstanceName,
					Namespace: TestClusterInstanceNamespace,
					Status:    v1alpha1.ManifestRenderedSuccess,
				},
				{
					APIGroup:  ptr.To(AgentClusterInstallApiVersion),
					Kind:      AgentClusterInstallKind,
					Name:      TestClusterInstanceName,
					Namespace: TestClusterInstanceNamespace,
					Status:    v1alpha1.ManifestRenderedSuccess,
				},
				{
					APIGroup:  ptr.To(BareMetalHostApiVersion),
					Kind:      BareMetalHostKind,
					Name:      TestNode1Hostname,
					Namespace: TestClusterInstanceNamespace,
					Status:    v1alpha1.ManifestSuppressed,
				},
				{
					APIGroup:  ptr.To(BareMetalHostApiVersion),
					Kind:      BareMetalHostKind,
					Name:      TestNode2Hostname,
					Namespace: TestClusterInstanceNamespace,
					Status:    v1alpha1.ManifestSuppressed,
				},
				{
					APIGroup:  ptr.To(NMStateConfigApiVersion),
					Kind:      NMStateConfigKind,
					Name:      TestNode1Hostname,
					Namespace: TestClusterInstanceNamespace,
					Status:    v1alpha1.ManifestRenderedSuccess,
				},
				{
					APIGroup:  ptr.To(NMStateConfigApiVersion),
					Kind:      NMStateConfigKind,
					Name:      TestNode2Hostname,
					Namespace: TestClusterInstanceNamespace,
					Status:    v1alpha1.ManifestRenderedSuccess,
				},
			},
		}),
)

var _ = Describe("executeRenderedManifests", func() {
	var (
		c                client.Client
		r                *ClusterInstanceReconciler
		ctx              = context.Background()
		testLogger       = zap.NewNop().Named("Test")
		clusterInstance  *v1alpha1.ClusterInstance
		clusterName      = TestClusterInstanceName
		clusterNamespace = TestClusterInstanceNamespace
		baseDomain       = "foobar"
		key              = types.NamespacedName{
			Name:      clusterName,
			Namespace: clusterNamespace,
		}
		apiGroup    = "ClusterDeploymentApiVersion"
		expManifest = v1alpha1.ManifestReference{
			APIGroup:  &apiGroup,
			Kind:      ClusterDeploymentKind,
			Name:      clusterName,
			Namespace: clusterNamespace,
		}
		objects []ci.RenderedObject
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			Build()

		r = &ClusterInstanceReconciler{
			Client:          c,
			Scheme:          scheme.Scheme,
			Log:             testLogger,
			TmplEngine:      ci.NewTemplateEngine(),
			DeletionHandler: &deletion.DeletionHandler{Client: c, Logger: testLogger},
		}

		clusterInstance = &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: clusterNamespace,
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				ClusterName: clusterName,
				BaseDomain:  baseDomain,
			},
		}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		manifests := []map[string]interface{}{
			map[string]interface{}{
				"apiVersion": *expManifest.APIGroup,
				"kind":       expManifest.Kind,
				"metadata":   map[string]interface{}{"name": clusterName, "namespace": clusterNamespace},
				"spec":       map[string]interface{}{"foo": "bar"},
			},
		}

		objects = make([]ci.RenderedObject, 0)
		for _, manifest := range manifests {
			object := ci.RenderedObject{}
			err := object.SetObject(manifest)
			Expect(err).ToNot(HaveOccurred())
			objects = append(objects, object)
		}
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

		result, err := r.executeRenderedManifests(ctx, testClient, testLogger, clusterInstance, objects, expManifest.Status)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(BeTrue())
		Expect(called).To(BeTrue())

		// Verify ClusterInstance status
		Expect(c.Get(ctx, key, clusterInstance)).To(Succeed())
		index, err := v1alpha1.IndexOfManifestByIdentity(&expManifest, clusterInstance.Status.ManifestsRendered)
		Expect(err).ToNot(HaveOccurred())
		Expect(clusterInstance.Status.ManifestsRendered[index].Status).To(Equal(expManifest.Status))
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

		result, err := r.executeRenderedManifests(ctx, testClient, testLogger, clusterInstance, objects, v1alpha1.ManifestRenderedSuccess)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(testError))
		Expect(result).To(BeFalse())
		Expect(called).To(BeTrue())

		// Verify ClusterInstance status
		Expect(c.Get(ctx, key, clusterInstance)).To(Succeed())
		index, err := v1alpha1.IndexOfManifestByIdentity(&expManifest, clusterInstance.Status.ManifestsRendered)
		Expect(err).ToNot(HaveOccurred())
		Expect(clusterInstance.Status.ManifestsRendered[index]).To(Satisfy(func(manifest v1alpha1.ManifestReference) bool {
			Expect(manifest.Status).To(Equal(expManifest.Status))
			Expect(manifest.Message).To(ContainSubstring(testError))
			return true
		}))
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

		result, err := r.executeRenderedManifests(ctx, testClient, testLogger, clusterInstance, objects, expManifest.Status)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(BeTrue())
		Expect(called).To(BeTrue())

		// Verify ClusterInstance status
		Expect(c.Get(ctx, key, clusterInstance)).To(Succeed())
		index, err := v1alpha1.IndexOfManifestByIdentity(&expManifest, clusterInstance.Status.ManifestsRendered)
		Expect(err).ToNot(HaveOccurred())
		Expect(clusterInstance.Status.ManifestsRendered[index].Status).To(Equal(expManifest.Status))
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

		result, err := r.executeRenderedManifests(ctx, testClient, testLogger, clusterInstance, objects, expManifest.Status)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(testError))
		Expect(result).To(BeFalse())
		Expect(called).To(BeTrue())

		// Verify ClusterInstance status
		Expect(c.Get(ctx, key, clusterInstance)).To(Succeed())
		index, err := v1alpha1.IndexOfManifestByIdentity(&expManifest, clusterInstance.Status.ManifestsRendered)
		Expect(err).ToNot(HaveOccurred())
		Expect(clusterInstance.Status.ManifestsRendered[index]).To(Satisfy(func(manifest v1alpha1.ManifestReference) bool {
			Expect(manifest.Status).To(Equal(expManifest.Status))
			Expect(manifest.Message).To(ContainSubstring(testError))
			return true
		}))
	})

})

var _ = Describe("createOrPatch", func() {
	var (
		c          client.Client
		ctx        = context.Background()
		testLogger = zap.NewNop().Named("Test")
		object     unstructured.Unstructured
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			Build()

		object = unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": ClusterDeploymentApiVersion,
				"kind":       ClusterDeploymentKind,
				"metadata": map[string]interface{}{
					"name":      TestClusterInstanceName,
					"namespace": TestClusterInstanceNamespace,
				},
				"spec": map[string]interface{}{
					"installed": false,
				},
			},
		}
	})

	It("succeeds in creating a manifest", func() {
		result, err := createOrPatch(ctx, c, testLogger, object)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(Equal(controllerutil.OperationResultCreated))
	})

	It("fails to apply the manifest due to an error while creating the kubernetes resource", func() {
		testError := "create-test-error"

		called := false
		testClient := fakeclient.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, client client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				return apierrors.NewNotFound(schema.GroupResource{Group: "", Resource: ClusterDeploymentKind}, TestClusterInstanceName)
			},
			Create: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				called = true
				return fmt.Errorf("%s", testError)
			},
		}).Build()

		result, err := createOrPatch(ctx, testClient, testLogger, object)
		Expect(err).To(HaveOccurred())
		Expect(result).To(Equal(controllerutil.OperationResultNone))
		Expect(called).To(BeTrue())
	})

	It("successfully patches an existing manifest that has changed", func() {

		Expect(c.Create(ctx, &object)).To(Succeed())

		// Update manifest by:
		// - change spec.baseDomain value
		// - change status by adding apiURL
		updatedObject := unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": object.GetAPIVersion(),
				"kind":       object.GetKind(),
				"metadata": map[string]interface{}{
					"name":      object.GetName(),
					"namespace": object.GetNamespace(),
				},
				"spec": map[string]interface{}{
					// change baseDomain
					"baseDomain": "new-domain",
				},
				// change status by adding apiUrl
				"status": map[string]interface{}{
					"apiURL": "https://api.foo.bar.redhat.com:6443",
				},
			},
		}
		// - add new label
		updatedObject.SetLabels(map[string]string{
			"ownedBy": "foo",
		})
		result, err := createOrPatch(ctx, c, testLogger, updatedObject)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(Equal(controllerutil.OperationResultUpdated))
	})

	It("successfully patches an existing manifest with changes to existing annotation", func() {

		originalAnnotations := map[string]string{
			"test-annotation": "before",
		}
		object.SetAnnotations(originalAnnotations)
		Expect(c.Create(ctx, &object)).To(Succeed())
		// Validate that annotation "test-annotation" is set to "before"
		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(object.GroupVersionKind())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(&object), obj)).To(Succeed())
		Expect(obj.GetAnnotations()).To(Equal(originalAnnotations))

		// Update manifest by:
		// - update existing annotation "test-annotation"
		updatedAnnotations := map[string]string{
			"test-annotation": "after",
		}
		updatedObject := object.DeepCopy()
		updatedObject.SetAnnotations(updatedAnnotations)

		result, err := createOrPatch(ctx, c, testLogger, *updatedObject)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(Equal(controllerutil.OperationResultUpdated))

		// Validate that annotation "test-annotation" is changed to "after"
		obj = &unstructured.Unstructured{}
		obj.SetGroupVersionKind(updatedObject.GroupVersionKind())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(updatedObject), obj)).To(Succeed())
		Expect(obj.GetAnnotations()).To(Equal(updatedAnnotations))
	})

	It("does not update a manifest that has not changed", func() {

		Expect(c.Create(ctx, &object)).To(Succeed())

		updatedObject := object.DeepCopy()

		result, err := createOrPatch(ctx, c, testLogger, *updatedObject)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(Equal(controllerutil.OperationResultNone))
	})

	It("does not update a manifest that has changes in the status only", func() {

		Expect(c.Create(ctx, &object)).To(Succeed())

		// Update manifest by changing the status by adding apiURL
		existingSpec, ok, err := unstructured.NestedFieldCopy(object.Object, []string{"spec"}...)
		Expect(ok).To(BeTrue())
		Expect(err).ToNot(HaveOccurred())

		updatedObject := unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": object.GetAPIVersion(),
				"kind":       object.GetKind(),
				"metadata": map[string]interface{}{
					"name":      object.GetName(),
					"namespace": object.GetNamespace(),
				},
				"spec": existingSpec,
				// change status by adding apiUrl
				"status": map[string]interface{}{
					"apiURL": "https://api.foo.bar.redhat.com:6443",
				},
			},
		}

		result, err := createOrPatch(ctx, c, testLogger, updatedObject)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(Equal(controllerutil.OperationResultNone))
	})

	It("successfully patches an existing ConfigMap that has changed", func() {

		object = unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       ConfigMapKind,
				"metadata": map[string]interface{}{
					"name":      TestClusterInstanceName,
					"namespace": TestClusterInstanceNamespace,
				},
				"data": map[string]string{
					"installed": "foobar",
				},
				"binaryData": map[string]interface{}{
					"test": []byte("old"),
					"this": []byte("should-not-change"),
				},
			},
		}

		Expect(c.Create(ctx, &object)).To(Succeed())

		// Update manifest by:
		// - update data and binaryData
		// - change status by adding apiURL
		updatedObject := unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": object.GetAPIVersion(),
				"kind":       object.GetKind(),
				"metadata": map[string]interface{}{
					"name":      object.GetName(),
					"namespace": object.GetNamespace(),
				},
				"data": map[string]interface{}{
					// update existing key
					"installed": "new-domain",
					// add new key
					"foo": "bar",
				},
				"binaryData": map[string]interface{}{
					// update existing key
					"test": []byte("new"),
					// add new key
					"foo": []byte("bar"),
				},
				// change status by adding apiUrl
				"status": map[string]interface{}{
					"apiURL": "https://api.foo.bar.redhat.com:6443",
				},
			},
		}
		// - add new label
		updatedObject.SetLabels(map[string]string{
			"ownedBy": "foo",
		})

		result, err := createOrPatch(ctx, c, testLogger, updatedObject)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(Equal(controllerutil.OperationResultUpdated))

		// Check that the existing object has been successfully patched
		actual := &corev1.ConfigMap{}
		err = c.Get(ctx, client.ObjectKeyFromObject(&object), actual)
		Expect(err).ToNot(HaveOccurred())

		expected := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      object.GetName(),
				Namespace: object.GetNamespace(),
				Labels: map[string]string{
					"ownedBy": "foo",
				},
			},
			Data: map[string]string{
				"installed": "new-domain",
				"foo":       "bar",
			},
			BinaryData: map[string][]byte{
				"test": []byte("new"),
				"foo":  []byte("bar"),
				"this": []byte("should-not-change"),
			},
		}

		Expect(actual.Annotations).To(Equal(expected.Annotations))
		Expect(actual.Labels).To(Equal(expected.Labels))
		Expect(actual.Data).To(Equal(expected.Data))
		Expect(actual.BinaryData).To(Equal(expected.BinaryData))

	})

	It("successfully patches an existing Secret that has changed", func() {

		object = unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       SecretKind,
				"metadata": map[string]interface{}{
					"name":      TestClusterInstanceName,
					"namespace": TestClusterInstanceNamespace,
				},
				"data": map[string]interface{}{
					"password": base64.StdEncoding.EncodeToString([]byte("old-password")),
				},
				"stringData": map[string]interface{}{
					"username": "old-user",
				},
			},
		}

		Expect(c.Create(ctx, &object)).To(Succeed())

		// Update manifest by:
		// - update data and stringData
		updatedObject := unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": object.GetAPIVersion(),
				"kind":       object.GetKind(),
				"metadata": map[string]interface{}{
					"name":      object.GetName(),
					"namespace": object.GetNamespace(),
				},
				"data": map[string]interface{}{
					"password": base64.StdEncoding.EncodeToString([]byte("new-password")),
					"token":    base64.StdEncoding.EncodeToString([]byte("new-token")),
				},
				"stringData": map[string]interface{}{
					"username": "new-user",
					"foo":      "bar",
				},
				"status": map[string]interface{}{
					"secretStatus": "updated",
				},
			},
		}
		// - add new annotation
		updatedObject.SetAnnotations(map[string]string{
			"updatedBy": "admin",
		})

		result, err := createOrPatch(ctx, c, testLogger, updatedObject)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(Equal(controllerutil.OperationResultUpdated))

		// Check that the existing object has been successfully patched
		actual := &corev1.Secret{}
		err = c.Get(ctx, client.ObjectKeyFromObject(&object), actual)
		Expect(err).ToNot(HaveOccurred())

		expected := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      object.GetName(),
				Namespace: object.GetNamespace(),
				Annotations: map[string]string{
					"updatedBy": "admin",
				},
			},
			Data: map[string][]byte{
				"password": []byte("new-password"),
				"token":    []byte("new-token"),
			},
			StringData: map[string]string{
				"username": "new-user",
				"foo":      "bar",
			},
		}

		Expect(actual.Annotations).To(Equal(expected.Annotations))
		Expect(actual.Data).To(Equal(expected.Data))
		Expect(actual.StringData).To(Equal(expected.StringData))

	})

	It("successfully patches an existing manifest (with a spec field) that has changed", func() {

		object = unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "Pod",
				"metadata": map[string]interface{}{
					"name":      TestClusterInstanceName,
					"namespace": TestClusterInstanceNamespace,
				},
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{
							"name":  "test",
							"image": "test:1.0",
						},
					},
				},
			},
		}

		Expect(c.Create(ctx, &object)).To(Succeed())

		// Update manifest by:
		// - changing the container image
		// - adding a new environment variable
		updatedObject := unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": object.GetAPIVersion(),
				"kind":       object.GetKind(),
				"metadata": map[string]interface{}{
					"name":      object.GetName(),
					"namespace": object.GetNamespace(),
				},
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{
							"name":  "test",
							"image": "test:2.0", // updated image
							"env": []interface{}{
								map[string]interface{}{
									"name":  "foo",
									"value": "bar",
								},
							},
						},
					},
				},
				"status": map[string]interface{}{
					"phase": "Running",
				},
			},
		}
		// - add a new label
		updatedObject.SetLabels(map[string]string{
			"env": "test",
		})

		result, err := createOrPatch(ctx, c, testLogger, updatedObject)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(Equal(controllerutil.OperationResultUpdated))

		// Check that the existing object has been successfully patched
		actual := &corev1.Pod{}
		err = c.Get(ctx, client.ObjectKeyFromObject(&object), actual)
		Expect(err).ToNot(HaveOccurred())

		expected := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      object.GetName(),
				Namespace: object.GetNamespace(),
				Labels: map[string]string{
					"env": "test",
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "test",
						Image: "test:2.0",
						Env: []corev1.EnvVar{
							{
								Name:  "foo",
								Value: "bar",
							},
						},
					},
				},
			},
		}

		Expect(actual.Labels).To(Equal(expected.Labels))
		Expect(actual.Spec.Containers).To(Equal(expected.Spec.Containers))
	})

})

var _ = Describe("applyACMBackupLabelToInstallTemplates", func() {
	var (
		c                   client.Client
		r                   *ClusterInstanceReconciler
		ctx                 = context.Background()
		testLogger          = zap.NewNop().Named("Test")
		siteConfigNamespace = os.Getenv("POD_NAMESPACE")
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			Build()
		tmplEngine := ci.NewTemplateEngine()
		r = &ClusterInstanceReconciler{
			Client:          c,
			Scheme:          scheme.Scheme,
			Log:             testLogger,
			TmplEngine:      tmplEngine,
			DeletionHandler: &deletion.DeletionHandler{Client: c, Logger: testLogger},
		}

		siteConfigNS := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: siteConfigNamespace,
			},
		}
		Expect(c.Create(ctx, siteConfigNS)).To(Succeed())
	})

	It("adds the ACM DR backup label to custom install template ConfigMaps", func() {
		clusterInstance := &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      TestClusterInstanceName,
				Namespace: TestClusterInstanceNamespace,
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				TemplateRefs: []v1alpha1.TemplateRef{
					{
						Name:      "test1",
						Namespace: siteConfigNamespace,
					},
					{
						Name:      "test2",
						Namespace: siteConfigNamespace,
					},
				},
				Nodes: []v1alpha1.NodeSpec{
					{
						TemplateRefs: []v1alpha1.TemplateRef{
							{
								Name:      "test3",
								Namespace: siteConfigNamespace,
							},
							{
								Name:      "test4",
								Namespace: siteConfigNamespace,
							},
						},
					},
				},
			},
		}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		// Create install template ConfigMaps
		installTemplateConfigMaps := []*corev1.ConfigMap{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test1",
					Namespace: siteConfigNamespace,
				},
				Data: map[string]string{
					"ClusterDeployment": "foo: bar",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test2",
					Namespace: siteConfigNamespace,
				},
				Data: map[string]string{
					"ClusterDeployment": "foo: bar",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test3",
					Namespace: siteConfigNamespace,
				},
				Data: map[string]string{
					"BareMetalHost": "foo: bar",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test4",
					Namespace: siteConfigNamespace,
				},
				Data: map[string]string{
					"BareMetalHost": "foo: bar",
				},
			},
		}
		for _, cm := range installTemplateConfigMaps {
			Expect(c.Create(ctx, cm)).To(Succeed())
		}

		err := r.applyACMBackupLabelToInstallTemplates(ctx, testLogger, clusterInstance)
		Expect(err).ToNot(HaveOccurred())

		for _, cm := range installTemplateConfigMaps {
			installTemplateCM := &corev1.ConfigMap{}
			key := types.NamespacedName{
				Name:      cm.Name,
				Namespace: cm.Namespace,
			}
			Expect(c.Get(ctx, key, installTemplateCM)).To(Succeed())
			Expect(installTemplateCM.GetLabels()).To(
				HaveKeyWithValue(acmBackupLabel, acmBackupLabelValue),
				"Install template ConfigMap %s/%s missing ACM DR backup label",
				installTemplateCM.Namespace, installTemplateCM.Name,
			)
		}
	})

	It("does not add the label to the default provided install template ConfigMaps", func() {
		clusterInstance := &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      TestClusterInstanceName,
				Namespace: TestClusterInstanceNamespace,
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				TemplateRefs: []v1alpha1.TemplateRef{
					{
						Name:      ai_templates.ClusterLevelInstallTemplates,
						Namespace: siteConfigNamespace,
					},
					{
						Name:      ibi_templates.ClusterLevelInstallTemplates,
						Namespace: siteConfigNamespace,
					},
				},
				Nodes: []v1alpha1.NodeSpec{
					{
						TemplateRefs: []v1alpha1.TemplateRef{
							{
								Name:      ai_templates.NodeLevelInstallTemplates,
								Namespace: siteConfigNamespace,
							},
							{
								Name:      ibi_templates.NodeLevelInstallTemplates,
								Namespace: siteConfigNamespace,
							},
						},
					},
				},
			},
		}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		// Create install template ConfigMaps
		installTemplateConfigMaps := []*corev1.ConfigMap{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ai_templates.ClusterLevelInstallTemplates,
					Namespace: siteConfigNamespace,
				},
				Data: map[string]string{
					"ClusterDeployment": "foo: bar",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ibi_templates.ClusterLevelInstallTemplates,
					Namespace: siteConfigNamespace,
				},
				Data: map[string]string{
					"ClusterDeployment": "foo: bar",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ai_templates.NodeLevelInstallTemplates,
					Namespace: siteConfigNamespace,
				},
				Data: map[string]string{
					"BareMetalHost": "foo: bar",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ibi_templates.NodeLevelInstallTemplates,
					Namespace: siteConfigNamespace,
				},
				Data: map[string]string{
					"BareMetalHost": "foo: bar",
				},
			},
		}
		for _, cm := range installTemplateConfigMaps {
			Expect(c.Create(ctx, cm)).To(Succeed())
		}

		err := r.applyACMBackupLabelToInstallTemplates(ctx, testLogger, clusterInstance)
		Expect(err).ToNot(HaveOccurred())

		for _, cm := range installTemplateConfigMaps {
			installTemplateCM := &corev1.ConfigMap{}
			key := types.NamespacedName{
				Name:      cm.Name,
				Namespace: cm.Namespace,
			}
			Expect(c.Get(ctx, key, installTemplateCM)).To(Succeed())
			Expect(installTemplateCM.GetLabels()).ToNot(
				HaveKeyWithValue(acmBackupLabel, acmBackupLabelValue),
				"Default provided install template ConfigMap %s/%s should not contain the ACM DR backup label",
				installTemplateCM.Namespace, installTemplateCM.Name,
			)
		}
	})
})
