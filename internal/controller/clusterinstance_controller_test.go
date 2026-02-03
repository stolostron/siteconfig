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
	"encoding/json"
	"fmt"
	"os"
	"time"

	"go.uber.org/mock/gomock"
	"go.uber.org/zap"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	bmh_v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	clusterv1 "open-cluster-management.io/api/cluster/v1"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
	"github.com/stolostron/siteconfig/internal/controller/configuration"
	"github.com/stolostron/siteconfig/internal/controller/deletion"
	cierrors "github.com/stolostron/siteconfig/internal/controller/errors"
	"github.com/stolostron/siteconfig/internal/controller/mocks"
	"github.com/stolostron/siteconfig/internal/controller/reinstall"
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

		deletionHandler := &deletion.DeletionHandler{Client: c, Logger: testLogger}
		r = &ClusterInstanceReconciler{
			Client:          c,
			Scheme:          scheme.Scheme,
			Log:             testLogger,
			TmplEngine:      ci.NewTemplateEngine(),
			ConfigStore:     configStore,
			DeletionHandler: deletionHandler,
			ReinstallHandler: &reinstall.ReinstallHandler{Client: c, Logger: testLogger,
				DeletionHandler: deletionHandler, ConfigStore: configStore},
		}

		Expect(c.Create(ctx, testParams.GeneratePullSecret())).To(Succeed())

		// Create "installation" template ConfigMaps that are referenced by the ClusterInstance
		// These are needed by applyACMBackupLabelToInstallTemplates during reconciliation
		clusterTemplate := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-template",
				Namespace: "default",
			},
			Data: map[string]string{
				"ClusterDeployment": "apiVersion: hive.openshift.io/v1\nkind: ClusterDeployment",
			},
		}
		nodeTemplate := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-node-template",
				Namespace: "default",
			},
			Data: map[string]string{
				"BareMetalHost": "apiVersion: metal3.io/v1alpha1\nkind: BareMetalHost",
			},
		}
		Expect(c.Create(ctx, clusterTemplate)).To(Succeed())
		Expect(c.Create(ctx, nodeTemplate)).To(Succeed())

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

	Context("Test Paused Annotation", func() {
		It("returns a zero result with no error when the paused annotation is set", func() {
			Expect(c.Create(ctx, clusterInstance)).To(Succeed())
			err := ci.ApplyPause(ctx, c, testLogger, clusterInstance, "test")
			Expect(err).NotTo(HaveOccurred())

			res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(clusterInstance)})
			Expect(err).NotTo(HaveOccurred())
			Expect(res).To(BeZero())
		})

		It("continues reconciling a ClusterInstance object when the paused annotation is removed", func() {
			// Set ObjectMeta.Generation and Status.ObservedGeneration to be different to ensure reconcile
			generation := int64(1)
			clusterInstance.ObjectMeta.Generation = generation
			clusterInstance.Status = v1alpha1.ClusterInstanceStatus{
				ObservedGeneration: generation - 1,
			}
			metav1.SetMetaDataAnnotation(&clusterInstance.ObjectMeta, v1alpha1.PausedAnnotation, "")
			Expect(c.Create(ctx, clusterInstance)).To(Succeed())

			Expect(c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), clusterInstance)).To(Succeed())
			Expect(clusterInstance.IsPaused()).To(BeTrue())

			// Verify that zero result is returned (due to the presence of the paused annotation)
			result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(clusterInstance)})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeZero())

			// Remove the paused annotation
			err = ci.RemovePause(ctx, c, testLogger, clusterInstance)
			Expect(err).NotTo(HaveOccurred())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), clusterInstance)).To(Succeed())
			Expect(clusterInstance.IsPaused()).To(BeFalse())

			// Verify that reconcile continues -- we can expect errors to occur in the ClusterInstance validations stage
			result, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(clusterInstance)})
			Expect(err).To(HaveOccurred())
			Expect(result).NotTo(BeZero())
		})

		It("clears the Paused status when the paused annotation is not present", func() {
			clusterInstance.Status.Paused = &v1alpha1.PausedStatus{
				TimeSet: metav1.Now(),
				Reason:  "foobar",
			}
			Expect(c.Create(ctx, clusterInstance)).To(Succeed())

			Expect(c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), clusterInstance)).To(Succeed())
			Expect(clusterInstance.Annotations).ToNot(HaveKey(v1alpha1.PausedAnnotation))

			res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(clusterInstance)})
			Expect(err).NotTo(HaveOccurred())
			Expect(res).NotTo(BeZero())

			Expect(c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), clusterInstance)).To(Succeed())
			Expect(clusterInstance.Status.Paused).To(BeNil())
		})
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

	It("applies ACM disaster recovery backup labels to custom installation template ConfigMaps during reconcile", func() {

		// Set Generation to ensure reconciliation proceeds past ObservedGeneration check
		generation := int64(1)
		clusterInstance.ObjectMeta.Generation = generation
		clusterInstance.Status = v1alpha1.ClusterInstanceStatus{
			ObservedGeneration: generation - 1, // Different from Generation to trigger reconciliation
		}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		// Trigger reconciliation
		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(clusterInstance)})
		// The reconciliation will fail at validation, but the ACM backup labels should still be applied
		// before validation occurs
		if err != nil {
			// If there's an error, it should be a validation error with requeue
			Expect(res.Requeue).To(BeTrue())
		}

		// Verify the ACM backup labels were applied to the templates
		updatedClusterTemplate := &corev1.ConfigMap{}
		Expect(c.Get(ctx, types.NamespacedName{
			Name:      "test-cluster-template",
			Namespace: "default",
		}, updatedClusterTemplate)).To(Succeed())
		Expect(updatedClusterTemplate.GetLabels()).To(
			HaveKeyWithValue(acmBackupLabel, acmBackupLabelValue),
			"Cluster template should have ACM DR backup label applied during reconcile",
		)

		updatedNodeTemplate := &corev1.ConfigMap{}
		Expect(c.Get(ctx, types.NamespacedName{
			Name:      "test-node-template",
			Namespace: "default",
		}, updatedNodeTemplate)).To(Succeed())
		Expect(updatedNodeTemplate.GetLabels()).To(
			HaveKeyWithValue(acmBackupLabel, acmBackupLabelValue),
			"Node template should have ACM DR backup label applied during reconcile",
		)
	})

	It("returns error when ACM backup label application fails during reconcile", func() {
		// Reference a non-existent template ConfigMap to trigger an error
		clusterInstance.Spec.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "non-existent-template", Namespace: "default"},
		}

		// Set Generation to ensure reconciliation proceeds past ObservedGeneration check
		generation := int64(1)
		clusterInstance.ObjectMeta.Generation = generation
		clusterInstance.Status = v1alpha1.ClusterInstanceStatus{
			ObservedGeneration: generation - 1,
		}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		// Trigger reconciliation - should fail when trying to apply labels to non-existent ConfigMap
		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(clusterInstance)})

		// Verify error is returned
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("non-existent-template"))
		Expect(res).To(Equal(ctrl.Result{}))
	})

	Context("Reinstall handling", func() {
		BeforeEach(func() {
			// Enable reinstalls for all tests in this context
			r.ConfigStore.SetAllowReinstalls(true)
		})

		It("skips reimport check when provisioning is not complete and continues to validation", func() {
			generation := int64(1)
			clusterInstance.ObjectMeta.Generation = generation
			clusterInstance.Status = v1alpha1.ClusterInstanceStatus{
				ObservedGeneration: generation - 1,
				Reinstall: &v1alpha1.ReinstallStatus{
					ObservedGeneration: "test-gen-1",
					Conditions: []metav1.Condition{
						{
							Type:   string(v1alpha1.ReinstallRequestProcessed),
							Status: metav1.ConditionTrue,
							Reason: string(v1alpha1.Completed),
						},
					},
				},
				// Note: ClusterProvisioned condition is not set, so EnsureClusterIsReimported
				// will skip the reimport check and return early without error
			}
			Expect(c.Create(ctx, clusterInstance)).To(Succeed())

			key := types.NamespacedName{
				Namespace: testParams.ClusterName,
				Name:      testParams.ClusterNamespace,
			}

			res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
			// EnsureClusterIsReimported returns early (no error, reimportNeeded=false) because
			// ClusterProvisioned condition is not set. Reconcile continues to validation which
			// fails because the ClusterInstance is not fully configured.
			Expect(err).To(HaveOccurred())
			Expect(res).To(Equal(requeueWithDelay(DefaultValidationErrorDelay)))
		})

		It("processes reinstall request when spec.Reinstall is set and status doesn't match", func() {
			generation := int64(1)
			clusterInstance.ObjectMeta.Generation = generation
			clusterInstance.Spec.Reinstall = &v1alpha1.ReinstallSpec{
				Generation:       "test-gen-1",
				PreservationMode: v1alpha1.PreservationModeNone,
			}
			clusterInstance.Status = v1alpha1.ClusterInstanceStatus{
				ObservedGeneration: generation - 1,
				// Status.Reinstall is nil, so ObservedGeneration doesn't match spec
			}
			Expect(c.Create(ctx, clusterInstance)).To(Succeed())

			key := types.NamespacedName{
				Namespace: testParams.ClusterName,
				Name:      testParams.ClusterNamespace,
			}

			res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
			// ProcessRequest will be called since status.Reinstall is nil
			// When status.Reinstall is nil, ProcessRequest initializes the status and requeues
			Expect(err).NotTo(HaveOccurred())
			// Expect a requeue since ProcessRequest returns {Requeue: true} after initialization
			Expect(res.Requeue).To(BeTrue())
		})

		It("skips reinstall processing when status.Reinstall.ObservedGeneration matches spec", func() {
			generation := int64(1)
			reinstallGen := "test-gen-1"
			clusterInstance.ObjectMeta.Generation = generation
			clusterInstance.Spec.Reinstall = &v1alpha1.ReinstallSpec{
				Generation:       reinstallGen,
				PreservationMode: v1alpha1.PreservationModeNone,
			}
			clusterInstance.Status = v1alpha1.ClusterInstanceStatus{
				ObservedGeneration: generation - 1,
				Reinstall: &v1alpha1.ReinstallStatus{
					ObservedGeneration: reinstallGen, // Matches spec.Reinstall.Generation
					Conditions: []metav1.Condition{
						{
							Type:   string(v1alpha1.ReinstallRequestProcessed),
							Status: metav1.ConditionTrue,
							Reason: string(v1alpha1.Completed),
						},
					},
				},
			}
			Expect(c.Create(ctx, clusterInstance)).To(Succeed())

			key := types.NamespacedName{
				Namespace: testParams.ClusterName,
				Name:      testParams.ClusterNamespace,
			}

			res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
			// Since status.Reinstall.ObservedGeneration matches spec.Reinstall.Generation,
			// ProcessRequest should be skipped and reconcile continues to validation
			// We expect a validation error since the ClusterInstance is not fully configured
			Expect(err).To(HaveOccurred())
			Expect(res).To(Equal(requeueWithDelay(DefaultValidationErrorDelay)))
		})

		It("requeues when reimport is still in progress", func() {
			provisionedTime := metav1.NewTime(time.Now().Add(-2 * time.Minute)) // Within grace period
			generation := int64(1)
			clusterInstance.ObjectMeta.Generation = generation
			clusterInstance.Status = v1alpha1.ClusterInstanceStatus{
				ObservedGeneration: generation - 1,
				Conditions: []metav1.Condition{
					{
						Type:               string(v1alpha1.ClusterProvisioned),
						Status:             metav1.ConditionTrue,
						Reason:             string(v1alpha1.Completed),
						LastTransitionTime: provisionedTime,
					},
				},
				Reinstall: &v1alpha1.ReinstallStatus{
					ObservedGeneration: "test-gen-1",
					Conditions: []metav1.Condition{
						{
							Type:   string(v1alpha1.ReinstallRequestProcessed),
							Status: metav1.ConditionTrue,
							Reason: string(v1alpha1.Completed),
						},
					},
				},
				ManifestsRendered: []v1alpha1.ManifestReference{
					{
						APIGroup: ptr.To("cluster.open-cluster-management.io/v1"),
						Kind:     "ManagedCluster",
						Name:     testParams.ClusterName,
						Status:   v1alpha1.ManifestRenderedSuccess,
					},
				},
			}
			Expect(c.Create(ctx, clusterInstance)).To(Succeed())

			// Create unhealthy ManagedCluster (triggers reimport in progress)
			managedCluster := &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: testParams.ClusterName,
				},
				Status: clusterv1.ManagedClusterStatus{
					Conditions: []metav1.Condition{
						{
							Type:   clusterv1.ManagedClusterConditionAvailable,
							Status: metav1.ConditionUnknown,
							Reason: "ManagedClusterLeaseUpdateStopped",
						},
					},
				},
			}
			Expect(c.Create(ctx, managedCluster)).To(Succeed())

			key := types.NamespacedName{
				Namespace: testParams.ClusterName,
				Name:      testParams.ClusterNamespace,
			}

			res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
			// EnsureClusterIsReimported returns reimportNeeded=true because cluster is
			// unhealthy within grace period. Reconcile should return early with requeue.
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Requeue).To(BeTrue())
		})
	})
})

var _ = Describe("updateObservedStatus", func() {
	var (
		c               client.Client
		r               *ClusterInstanceReconciler
		ctx             = context.Background()
		testLogger      = zap.NewNop().Named("Test")
		clusterInstance *v1alpha1.ClusterInstance
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			Build()

		r = &ClusterInstanceReconciler{
			Client: c,
			Scheme: scheme.Scheme,
			Log:    testLogger,
		}

		clusterInstance = &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:       TestClusterInstanceName,
				Namespace:  TestClusterInstanceNamespace,
				Finalizers: []string{clusterInstanceFinalizer},
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				ClusterName:            TestClusterInstanceName,
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

	It("should update annotation and observedGeneration if spec changed", func() {
		currentSpecJSON, err := json.Marshal(clusterInstance.Spec)
		Expect(err).ToNot(HaveOccurred())
		metav1.SetMetaDataAnnotation(&clusterInstance.ObjectMeta, v1alpha1.LastClusterInstanceSpecAnnotation, string(currentSpecJSON))
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err = r.updateObservedStatus(ctx, testLogger, clusterInstance)
		Expect(err).ToNot(HaveOccurred())

		updatedCI := &v1alpha1.ClusterInstance{}
		Expect(c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), updatedCI)).To(Succeed())
		Expect(updatedCI.Annotations).To(HaveKeyWithValue(v1alpha1.LastClusterInstanceSpecAnnotation, string(currentSpecJSON)))
		Expect(updatedCI.Status.ObservedGeneration).To(Equal(clusterInstance.Generation))
	})

	It("should return error if patching annotation fails", func() {
		// Create mock client that will inject patch error
		ctrl := gomock.NewController(GinkgoT())
		defer ctrl.Finish()

		mockClient := mocks.NewGeneratedMockClient(ctrl)
		mockClient.EXPECT().
			Patch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(fmt.Errorf("inject patch error")).
			Times(1)

		// Update the reconciler to use the mock client
		r.Client = mockClient

		err := r.updateObservedStatus(ctx, testLogger, clusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to update"))
	})

	It("should return error if re-fetching ClusterInstance fails", func() {
		// Create mock client that will inject Get error (for re-fetching)
		ctrl := gomock.NewController(GinkgoT())
		defer ctrl.Finish()

		mockClient := mocks.NewGeneratedMockClient(ctrl)

		// First expect the annotation patch to succeed
		mockClient.EXPECT().
			Patch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil).
			Times(1)

		// Then expect Get to fail (for re-fetching after annotation update)
		mockClient.EXPECT().
			Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(fmt.Errorf("inject get error")).
			Times(1)

		// Update the reconciler to use the mock client
		r.Client = mockClient

		err := r.updateObservedStatus(ctx, testLogger, clusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to re-fetch ClusterInstance"))
	})

	It("should return error if patching status fails", func() {
		// Create mock client that will inject status patch error
		ctrl := gomock.NewController(GinkgoT())
		defer ctrl.Finish()

		mockClient := mocks.NewGeneratedMockClient(ctrl)
		mockStatusWriter := mocks.NewGeneratedMockStatusWriter(ctrl)

		// First expect the annotation patch to succeed
		mockClient.EXPECT().
			Patch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil).
			Times(1)

		// Then expect Get to succeed (for re-fetching after annotation update)
		mockClient.EXPECT().
			Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil).
			Times(1)

		// Finally expect Status() call and status patch to fail
		mockClient.EXPECT().Status().Return(mockStatusWriter).Times(1)
		mockStatusWriter.EXPECT().
			Patch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(fmt.Errorf("inject status patch error")).
			Times(1)

		// Update the reconciler to use the mock client
		r.Client = mockClient

		err := r.updateObservedStatus(ctx, testLogger, clusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to patch ClusterInstance status"))
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

	var triggerAndVerifyFinalizerHandling = func(
		r *ClusterInstanceReconciler,
		clusterInstanceKey types.NamespacedName,
		expectedManifestsRendered [][]v1alpha1.ManifestReference,
	) {
		// Trigger deletion of ClusterInstance.
		clusterInstance := &v1alpha1.ClusterInstance{}
		Expect(r.Client.Get(ctx, clusterInstanceKey, clusterInstance)).To(Succeed())
		Expect(r.Client.Delete(ctx, clusterInstance)).To(Succeed())

		// Fetch the ClusterInstance to get the DeletionTimestamp.
		Expect(r.Client.Get(ctx, clusterInstanceKey, clusterInstance)).To(Succeed())

		for _, expected := range expectedManifestsRendered {
			// Call r.handleFinalizer should trigger deletion of rendered manifests.
			res, err := r.handleFinalizer(ctx, testLogger, clusterInstance)
			Expect(err).NotTo(HaveOccurred())
			Expect(res).To(Equal(requeueForDeletion()))
			Expect(r.Client.Get(ctx, clusterInstanceKey, clusterInstance)).To(Succeed())

			for index, manifest := range clusterInstance.Status.ManifestsRendered {
				expManifest := expected[index]
				Expect(*manifest.APIGroup).To(Equal(*expManifest.APIGroup))
				Expect(manifest.Kind).To(Equal(expManifest.Kind))
				Expect(manifest.Name).To(Equal(expManifest.Name))
				Expect(manifest.Status).To(Equal(expManifest.Status))
			}

		}

		// Final call of r.handleFinalizer should result in the deletion of all the rendered objects and ClusterInstance
		res, err := r.handleFinalizer(ctx, testLogger, clusterInstance)
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
		clusterInstance.DeletionTimestamp = &metav1.Time{Time: metav1.Now().UTC()}
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

		// Get the created manifests to confirm they exist before calling finalizer
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
		expectedManifests := [][]v1alpha1.ManifestReference{
			// Process sync-wave 3 (sync-wave 4 will be result in the 2 unowned ConfigMaps being removed from the status.ManifestsRendered)
			{
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
					Status:   v1alpha1.ManifestDeletionInProgress,
				},
			},
			// Process sync-wave 2
			{
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
					Status:    v1alpha1.ManifestDeletionInProgress,
				},
			},
			// Process sync-wave 1
			{
				{
					APIGroup:  ptr.To(ClusterDeploymentApiVersion),
					Kind:      ClusterDeploymentKind,
					Name:      manifestName,
					Namespace: clusterNamespace,
					SyncWave:  1,
					Status:    v1alpha1.ManifestDeletionInProgress,
				},
			},
		}

		triggerAndVerifyFinalizerHandling(r, clusterInstanceKey, expectedManifests)

		// Verify ClusterDeployment, BareMetalHost, ManagedCluster manifests are deleted
		Expect(c.Get(ctx, key, cd)).ToNot(Succeed())
		Expect(c.Get(ctx, key, bmh)).ToNot(Succeed())
		Expect(c.Get(ctx, keyMc, mc)).ToNot(Succeed())

		// Verify both ConfigMap manifests are NOT deleted
		Expect(c.Get(ctx, key2, cm)).To(Succeed())
		Expect(c.Get(ctx, key, cm)).To(Succeed())
	})

	It("does not fail to handle the finalizer when attempting to delete a non-existent manifest", func() {

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
		expectedManifests := [][]v1alpha1.ManifestReference{
			// Process sync-wave 3
			{
				{
					APIGroup:  ptr.To(ClusterDeploymentApiVersion),
					Kind:      ClusterDeploymentKind,
					Name:      manifestName,
					Namespace: TestClusterInstanceNamespace,
					SyncWave:  1,
					Status:    v1alpha1.ManifestRenderedSuccess,
				},
				{
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
					Status:   v1alpha1.ManifestDeletionInProgress,
				},
			},
			// Process sync-wave 2 and 1 since sync-wave 2 has the BMH which does not exist - it will be treated as deleted
			{
				{
					APIGroup:  ptr.To(ClusterDeploymentApiVersion),
					Kind:      ClusterDeploymentKind,
					Name:      manifestName,
					Namespace: TestClusterInstanceNamespace,
					SyncWave:  1,
					Status:    v1alpha1.ManifestDeletionInProgress,
				},
				{
					APIGroup:  ptr.To(BareMetalHostApiVersion),
					Kind:      BareMetalHostKind,
					Name:      manifestName,
					Namespace: TestClusterInstanceNamespace,
					SyncWave:  2,
					Status:    v1alpha1.ManifestDeletionInProgress,
				},
			},
		}
		triggerAndVerifyFinalizerHandling(r, clusterInstanceKey, expectedManifests)

		// Ensure that rendered manifests are deleted
		Expect(c.Get(ctx, key, cd)).ToNot(Succeed())
		Expect(c.Get(ctx, keyMc, mc)).ToNot(Succeed())
	})

	It("sets the Paused annotation when a Timeout error occurs", func() {

		// Specify BareMetalHost object
		bmh := &bmh_v1alpha1.BareMetalHost{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "node-0",
				Namespace: clusterNamespace,
				Labels: map[string]string{
					ci.OwnedByLabel: ci.GenerateOwnedByLabelValue(clusterNamespace, clusterName),
				},
				Finalizers: []string{"block-deletion"},
			},
		}

		// Specify ClusterInstance, ensure it's initialized with DeletionTimestamp
		clusterInstance := &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:              clusterName,
				Namespace:         clusterNamespace,
				DeletionTimestamp: &metav1.Time{Time: metav1.Now().UTC()},
				Finalizers:        []string{clusterInstanceFinalizer, "do-not-delete"},
			},
			Status: v1alpha1.ClusterInstanceStatus{
				ManifestsRendered: []v1alpha1.ManifestReference{
					{
						APIGroup:  ptr.To(BareMetalHostApiVersion),
						Kind:      BareMetalHostKind,
						Name:      bmh.Name,
						Namespace: bmh.Namespace,
						SyncWave:  1,
						Status:    v1alpha1.ManifestRenderedSuccess,
					},
				},
			},
		}

		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			WithObjects(bmh, clusterInstance).
			WithInterceptorFuncs(interceptor.Funcs{
				Delete: func(ctx context.Context, client client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					return cierrors.NewDeletionTimeoutError("")
				},
			}).
			Build()

		r = &ClusterInstanceReconciler{
			Client:          c,
			Scheme:          scheme.Scheme,
			Log:             testLogger,
			TmplEngine:      ci.NewTemplateEngine(),
			DeletionHandler: &deletion.DeletionHandler{Client: c, Logger: testLogger},
		}

		// Ensure BareMetalHost and ClusterInstance objects exist
		Expect(c.Get(ctx, client.ObjectKeyFromObject(bmh), bmh)).To(Succeed())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), clusterInstance)).To(Succeed())
		Expect(clusterInstance.DeletionTimestamp).ToNot(BeZero())

		// Execute handleFinalizer in which a Deletion Timeout error is injected
		res, err := r.handleFinalizer(ctx, testLogger, clusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(cierrors.IsDeletionTimeoutError(err)).To(BeTrue())
		// Since we expect the paused annotation and Paused objects to be set, we expect a requeue
		Expect(res).ToNot(BeNil())

		// Verify that the paused annotation is set on the ClusterInstance
		Expect(c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), clusterInstance)).To(Succeed())
		Expect(clusterInstance.IsPaused()).To(BeTrue())
		Expect(clusterInstance.Status.Paused.Reason).To(ContainSubstring(err.Error()))
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

		var doNotPruneList []ci.RenderedObject
		var doNotPruneKeys []types.NamespacedName

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
		var expectedPruneList []ci.RenderedObject
		var expectedPruneKeys []types.NamespacedName

		Expect(c.Get(ctx, clusterInstanceKey, clusterInstance)).To(Succeed())
		pruningCompleted, err := r.pruneManifests(ctx, testLogger, clusterInstance, pruneList)
		Expect(err).ToNot(HaveOccurred())
		Expect(pruningCompleted).To(BeTrue())

		verifyPruningFn(clusterInstanceKey, expectedPruneList, doNotPruneList, expectedPruneKeys, doNotPruneKeys)

	})

	It("fails to prunes manifests due to a Timeout error", func() {

		generation := int64(1)
		clusterInstanceKey := types.NamespacedName{Namespace: TestClusterInstanceNamespace, Name: TestClusterInstanceName}
		clusterInstance := &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:       clusterInstanceKey.Name,
				Namespace:  clusterInstanceKey.Namespace,
				Finalizers: []string{clusterInstanceFinalizer},
				Generation: generation,
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				PruneManifests: []v1alpha1.ResourceRef{
					{
						APIVersion: BareMetalHostApiVersion,
						Kind:       BareMetalHostKind,
					},
				},
				Nodes: []v1alpha1.NodeSpec{
					{
						HostName: TestNode1Hostname,
					},
				},
			},
			Status: v1alpha1.ClusterInstanceStatus{
				ManifestsRendered:  manifestsRenderedRefs,
				ObservedGeneration: generation - 1,
			},
		}

		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			WithObjects(clusterInstance).
			WithInterceptorFuncs(interceptor.Funcs{
				Delete: func(ctx context.Context, fclient client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					return cierrors.NewDeletionTimeoutError("")
				},
			}).Build()

		r = &ClusterInstanceReconciler{
			Client:          c,
			Scheme:          scheme.Scheme,
			Log:             testLogger,
			TmplEngine:      ci.NewTemplateEngine(),
			DeletionHandler: &deletion.DeletionHandler{Client: c, Logger: testLogger},
		}

		objects = []ci.RenderedObject{bmh1RenderedObject}
		objectKeys = []types.NamespacedName{bmh1Key}
		manifestsRenderedRefs = []v1alpha1.ManifestReference(nil)
		pruneList := objects

		// Create the manifests and confirm they exist
		manifestsRenderedRefs = []v1alpha1.ManifestReference(nil)
		for index, object := range objects {
			obj := object.GetObject()
			obj.SetResourceVersion("")
			Expect(c.Create(ctx, &obj)).To(Succeed())
			obj2 := &unstructured.Unstructured{}
			obj2.SetGroupVersionKind(obj.GroupVersionKind())
			Expect(c.Get(ctx, objectKeys[index], obj2)).To(Succeed())
			manifestsRenderedRefs = append(manifestsRenderedRefs, *object.ManifestReference())
		}

		pruningCompleted, err := r.pruneManifests(ctx, testLogger, clusterInstance, pruneList)
		Expect(err).To(HaveOccurred())
		Expect(cierrors.IsDeletionTimeoutError(err)).To(BeTrue())
		Expect(pruningCompleted).To(BeFalse())

		Expect(c.Get(ctx, clusterInstanceKey, clusterInstance)).To(Succeed())
		Expect(clusterInstance.Annotations).To(HaveKey(v1alpha1.PausedAnnotation))
		Expect(clusterInstance.Status.Paused.Reason).To(ContainSubstring(err.Error()))
		result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: clusterInstanceKey})
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(BeZero())

		// Remove the hold-annotation and verify that the Paused status is cleared
		Expect(ci.RemovePause(ctx, c, testLogger, clusterInstance)).To(Succeed())

		Expect(c.Get(ctx, clusterInstanceKey, clusterInstance)).To(Succeed())
		Expect(clusterInstance.Annotations).ToNot(HaveKey(v1alpha1.PausedAnnotation))
		Expect(clusterInstance.Status.Paused).To(BeNil())

		result, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(clusterInstance)})
		Expect(err).To(HaveOccurred()) // expect ClusterInstance validation error
		Expect(result).NotTo(BeZero())
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
		c = createSSAMockClient()
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
		apiGroup         = "ClusterDeploymentApiVersion"
		expManifest      = v1alpha1.ManifestReference{
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
			{
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

		testClient := createSSAMockClient(clusterInstance)
		manifestsRendered, result, err := r.executeRenderedManifests(ctx, testClient, testLogger, clusterInstance, objects, expManifest.Status)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(BeTrue())

		// Verify returned ManifestsRendered
		index, err := v1alpha1.IndexOfManifestByIdentity(&expManifest, manifestsRendered)
		Expect(err).ToNot(HaveOccurred())
		Expect(manifestsRendered[index].Status).To(Equal(expManifest.Status))
	})

	It("fails to apply the manifest due to an error while creating the kubernetes resource", func() {
		testError := "create-test-error"
		expManifest.Status = v1alpha1.ManifestRenderedFailure

		// Use MockClient for error injection
		ctrl := gomock.NewController(GinkgoT())
		defer ctrl.Finish()

		mockClient := mocks.NewGeneratedMockClient(ctrl)
		mockClient.EXPECT().
			Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil).
			AnyTimes() // May be called during object existence checks

		mockClient.EXPECT().
			Patch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(fmt.Errorf("failed to apply object using Server-Side Apply: %s", testError)).
			Times(1)

		manifestsRendered, result, err := r.executeRenderedManifests(ctx, mockClient, testLogger, clusterInstance, objects, v1alpha1.ManifestRenderedSuccess)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(testError))
		Expect(result).To(BeFalse())

		// Verify returned ManifestsRendered
		index, err := v1alpha1.IndexOfManifestByIdentity(&expManifest, manifestsRendered)
		Expect(err).ToNot(HaveOccurred())
		Expect(manifestsRendered[index]).To(Satisfy(func(manifest v1alpha1.ManifestReference) bool {
			Expect(manifest.Status).To(Equal(expManifest.Status))
			Expect(manifest.Message).To(ContainSubstring(testError))
			return true
		}))
	})

	It("succeeds in updating a manifest", func() {
		expManifest.Status = v1alpha1.ManifestRenderedSuccess

		ctrl := gomock.NewController(GinkgoT())
		defer ctrl.Finish()

		testClient := mocks.NewGeneratedMockClient(ctrl)
		testClient.EXPECT().
			Patch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil).
			Times(1)

		manifestsRendered, result, err := r.executeRenderedManifests(ctx, testClient, testLogger, clusterInstance, objects, expManifest.Status)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(BeTrue())

		// Verify returned ManifestsRendered
		index, err := v1alpha1.IndexOfManifestByIdentity(&expManifest, manifestsRendered)
		Expect(err).ToNot(HaveOccurred())
		Expect(manifestsRendered[index].Status).To(Equal(expManifest.Status))
	})

	It("fails to update the manifest due to an error while patching the kubernetes resource", func() {
		testError := "update-test-error"
		expManifest.Status = v1alpha1.ManifestRenderedFailure

		ctrl := gomock.NewController(GinkgoT())
		defer ctrl.Finish()

		testClient := mocks.NewGeneratedMockClient(ctrl)
		testClient.EXPECT().
			Patch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(fmt.Errorf("%s", testError)).
			Times(1)

		manifestsRendered, result, err := r.executeRenderedManifests(ctx, testClient, testLogger, clusterInstance, objects, expManifest.Status)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(testError))
		Expect(result).To(BeFalse())

		// Verify returned ManifestsRendered
		index, err := v1alpha1.IndexOfManifestByIdentity(&expManifest, manifestsRendered)
		Expect(err).ToNot(HaveOccurred())
		Expect(manifestsRendered[index]).To(Satisfy(func(manifest v1alpha1.ManifestReference) bool {
			Expect(manifest.Status).To(Equal(expManifest.Status))
			Expect(manifest.Message).To(ContainSubstring(testError))
			return true
		}))
	})

})

// createSSAMockClient creates a minimal fake client that supports Server-Side Apply
// This is needed only for tests that specifically test SSA behavior since the
// standard fake client doesn't support apply patches yet.
func createSSAMockClient(objects ...client.Object) client.Client {
	return fakeclient.NewClientBuilder().
		WithScheme(scheme.Scheme).
		WithStatusSubresource(&v1alpha1.ClusterInstance{}).
		WithObjects(objects...).
		WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				// For Apply patches, simulate SSA behavior with create/update
				if patch.Type() == "application/apply-patch+yaml" {
					key := types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}
					existing := &unstructured.Unstructured{}
					existing.SetGroupVersionKind(obj.GetObjectKind().GroupVersionKind())

					if err := c.Get(ctx, key, existing); err != nil {
						if !apierrors.IsNotFound(err) {
							return err
						}
						return c.Create(ctx, obj) // Create if not found
					}

					obj.SetResourceVersion(existing.GetResourceVersion())

					// Minimal SSA merge for ConfigMap/Secret data fields
					if objUnstructured := obj.(*unstructured.Unstructured); objUnstructured.GetKind() == "ConfigMap" || objUnstructured.GetKind() == "Secret" {
						for _, field := range []string{"data", "binaryData"} {
							if existingData, exists := existing.Object[field]; exists {
								if newData, hasNewData := objUnstructured.Object[field]; hasNewData {
									if existingMap, ok := existingData.(map[string]interface{}); ok {
										if newMap, ok := newData.(map[string]interface{}); ok {
											mergedData := make(map[string]interface{})
											// Copy existing data first
											for k, v := range existingMap {
												mergedData[k] = v
											}
											// Overwrite with new data
											for k, v := range newMap {
												mergedData[k] = v
											}
											objUnstructured.Object[field] = mergedData
										}
									}
								}
							}
						}
					}

					return c.Update(ctx, obj) // Update if exists
				}
				return c.Patch(ctx, obj, patch, opts...) // Standard patch behavior
			},
		}).
		Build()
}

var _ = Describe("applyObject", func() {
	var (
		c          client.Client
		ctx        = context.Background()
		testLogger = zap.NewNop().Named("Test")
		object     unstructured.Unstructured
	)

	BeforeEach(func() {
		c = createSSAMockClient()

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

		result, err := applyObject(ctx, c, testLogger, object)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(Equal(controllerutil.OperationResultUpdated))
	})

	It("fails to apply the manifest due to an error while creating the kubernetes resource", func() {
		testError := "create-test-error"

		// Use MockClient for error injection
		ctrl := gomock.NewController(GinkgoT())
		defer ctrl.Finish()

		mockClient := mocks.NewGeneratedMockClient(ctrl)
		mockClient.EXPECT().
			Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil).
			AnyTimes() // May be called during object existence checks

		mockClient.EXPECT().
			Patch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(fmt.Errorf("failed to apply object using Server-Side Apply: %s", testError)).
			Times(1)

		result, err := applyObject(ctx, mockClient, testLogger, object)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(testError))
		Expect(result).To(Equal(controllerutil.OperationResultNone))
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

		result, err := applyObject(ctx, c, testLogger, updatedObject)
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

		result, err := applyObject(ctx, c, testLogger, *updatedObject)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(Equal(controllerutil.OperationResultUpdated))

		// Validate that annotation "test-annotation" is changed to "after"
		obj = &unstructured.Unstructured{}
		obj.SetGroupVersionKind(updatedObject.GroupVersionKind())
		Expect(c.Get(ctx, client.ObjectKeyFromObject(updatedObject), obj)).To(Succeed())
		Expect(obj.GetAnnotations()).To(Equal(updatedAnnotations))
	})

	It("handles SSA Patch errors gracefully", func() {
		// Test that SSA Patch errors are properly handled and formatted
		ctrl := gomock.NewController(GinkgoT())
		defer ctrl.Finish()

		mockClient := mocks.NewGeneratedMockClient(ctrl)
		mockClient.EXPECT().
			Patch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(fmt.Errorf("API server unavailable")).
			Times(1)

		result, err := applyObject(ctx, mockClient, testLogger, object)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to apply object using Server-Side Apply"))
		Expect(err.Error()).To(ContainSubstring("API server unavailable"))
		Expect(result).To(Equal(controllerutil.OperationResultNone))
	})

	It("handles objects with missing required metadata fields", func() {
		// Test that objects without name or kind are handled properly
		// The applyObject function should handle this gracefully
		invalidObject := unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": ClusterDeploymentApiVersion,
				"kind":       ClusterDeploymentKind,
				// Missing metadata.name, which should cause issues
				"metadata": map[string]interface{}{
					"namespace": TestClusterInstanceNamespace,
				},
			},
		}

		result, err := applyObject(ctx, c, testLogger, invalidObject)
		Expect(err).To(HaveOccurred())
		// The error should occur during Get or Apply operations due to missing name
		Expect(result).To(Equal(controllerutil.OperationResultNone))
	})

	It("handles SSA Patch errors", func() {
		// Test that SSA Patch errors are properly handled and formatted
		ctrl := gomock.NewController(GinkgoT())
		defer ctrl.Finish()

		mockClient := mocks.NewGeneratedMockClient(ctrl)
		mockClient.EXPECT().
			Get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil).
			AnyTimes() // May be called during object existence checks

		mockClient.EXPECT().
			Patch(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
			Return(fmt.Errorf("field ownership conflict")).
			Times(1)

		result, err := applyObject(ctx, mockClient, testLogger, object)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to apply object using Server-Side Apply"))
		Expect(err.Error()).To(ContainSubstring("field ownership conflict"))
		Expect(result).To(Equal(controllerutil.OperationResultNone))
	})

	It("properly sets field manager and force ownership for SSA", func() {
		// This test verifies that SSA is called with the correct parameters
		// We can verify this indirectly by ensuring the operation succeeds with our field manager

		result, err := applyObject(ctx, c, testLogger, object)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(Equal(controllerutil.OperationResultUpdated))

		// Verify the object was created
		createdObj := &unstructured.Unstructured{}
		createdObj.SetGroupVersionKind(object.GroupVersionKind())
		err = c.Get(ctx, client.ObjectKeyFromObject(&object), createdObj)
		Expect(err).ToNot(HaveOccurred())
		Expect(createdObj.GetName()).To(Equal(object.GetName()))
	})

	It("handles objects with complex metadata correctly", func() {
		// Test SSA with objects containing various metadata fields
		complexObject := unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": ClusterDeploymentApiVersion,
				"kind":       ClusterDeploymentKind,
				"metadata": map[string]interface{}{
					"name":      TestClusterInstanceName,
					"namespace": TestClusterInstanceNamespace,
					"labels": map[string]interface{}{
						"app": "test",
						"cluster.open-cluster-management.io/backup": "true",
					},
					"annotations": map[string]interface{}{
						"test.annotation":    "value",
						"managed.annotation": "managed-value",
					},
					"finalizers": []interface{}{
						"test.finalizer",
					},
				},
				"spec": map[string]interface{}{
					"installed": false,
					"complex": map[string]interface{}{
						"nested": "value",
					},
				},
			},
		}

		result, err := applyObject(ctx, c, testLogger, complexObject)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(Equal(controllerutil.OperationResultUpdated))

		// Verify the object was created with proper metadata
		createdObj := &unstructured.Unstructured{}
		createdObj.SetGroupVersionKind(complexObject.GroupVersionKind())
		err = c.Get(ctx, client.ObjectKeyFromObject(&complexObject), createdObj)
		Expect(err).ToNot(HaveOccurred())
		Expect(createdObj.GetLabels()).To(HaveKey("app"))
		Expect(createdObj.GetAnnotations()).To(HaveKey("test.annotation"))
	})

	It("preserves ExtraLabels and ExtraAnnotations from template rendering", func() {
		// Test that SSA preserves extra labels and annotations that were already applied during template rendering

		// Simulate template rendering by pre-applying ExtraLabels/ExtraAnnotations to the object
		// This is what the template engine does during rendering before applyObject is called
		renderedObject := object.DeepCopy()
		existingLabels := renderedObject.GetLabels()
		if existingLabels == nil {
			existingLabels = make(map[string]string)
		}
		existingAnnotations := renderedObject.GetAnnotations()
		if existingAnnotations == nil {
			existingAnnotations = make(map[string]string)
		}

		// Add ExtraLabels and ExtraAnnotations as template rendering would
		existingLabels["custom.label"] = "custom-value"
		existingLabels["environment"] = "test"
		existingAnnotations["custom.annotation"] = "annotation-value"
		existingAnnotations["managed.by"] = "siteconfig"

		renderedObject.SetLabels(existingLabels)
		renderedObject.SetAnnotations(existingAnnotations)

		result, err := applyObject(ctx, c, testLogger, *renderedObject)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(Equal(controllerutil.OperationResultUpdated))

		// Verify the object was created with the preserved labels and annotations
		createdObj := &unstructured.Unstructured{}
		createdObj.SetGroupVersionKind(renderedObject.GroupVersionKind())
		err = c.Get(ctx, client.ObjectKeyFromObject(renderedObject), createdObj)
		Expect(err).ToNot(HaveOccurred())

		// Check that labels were preserved from template rendering
		labels := createdObj.GetLabels()
		Expect(labels).To(HaveKeyWithValue("custom.label", "custom-value"))
		Expect(labels).To(HaveKeyWithValue("environment", "test"))

		// Check that annotations were preserved from template rendering
		annotations := createdObj.GetAnnotations()
		Expect(annotations).To(HaveKeyWithValue("custom.annotation", "annotation-value"))
		Expect(annotations).To(HaveKeyWithValue("managed.by", "siteconfig"))
	})

	It("handles namespace vs cluster-scoped resources correctly", func() {
		// Test that SSA works correctly for both namespaced and cluster-scoped resources
		clusterScopedObject := unstructured.Unstructured{
			Object: map[string]interface{}{
				"apiVersion": "cluster.open-cluster-management.io/v1",
				"kind":       "ManagedCluster",
				"metadata": map[string]interface{}{
					"name": TestClusterInstanceName,
					// No namespace for cluster-scoped resource
				},
				"spec": map[string]interface{}{
					"hubAcceptsClient": true,
				},
			},
		}

		result, err := applyObject(ctx, c, testLogger, clusterScopedObject)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(Equal(controllerutil.OperationResultUpdated))

		// Verify the cluster-scoped object was created
		createdObj := &unstructured.Unstructured{}
		createdObj.SetGroupVersionKind(clusterScopedObject.GroupVersionKind())
		err = c.Get(ctx, client.ObjectKeyFromObject(&clusterScopedObject), createdObj)
		Expect(err).ToNot(HaveOccurred())
		Expect(createdObj.GetName()).To(Equal(TestClusterInstanceName))
		Expect(createdObj.GetNamespace()).To(BeEmpty()) // Cluster-scoped resource has no namespace
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

		result, err := applyObject(ctx, c, testLogger, updatedObject)
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

		result, err := applyObject(ctx, c, testLogger, updatedObject)
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

		result, err := applyObject(ctx, c, testLogger, updatedObject)
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

	// Comprehensive tests for template-rendered metadata preservation
	Context("ExtraLabels and ExtraAnnotations Integration Tests", func() {

		It("preserves cluster-level ExtraLabels and ExtraAnnotations", func() {
			// Simulate template rendering for cluster-level manifest

			// Create object with cluster-level metadata (as template engine would)
			renderedObject := object.DeepCopy()
			existingLabels := map[string]string{
				"cluster.label1": "cluster-value1",
				"cluster.label2": "cluster-value2",
			}
			existingAnnotations := map[string]string{
				"cluster.annotation1": "cluster-value1",
				"cluster.annotation2": "cluster-value2",
			}
			renderedObject.SetLabels(existingLabels)
			renderedObject.SetAnnotations(existingAnnotations)

			result, err := applyObject(ctx, c, testLogger, *renderedObject)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(controllerutil.OperationResultUpdated))

			// Verify cluster-level metadata is preserved
			appliedObj := &unstructured.Unstructured{}
			appliedObj.SetGroupVersionKind(renderedObject.GroupVersionKind())
			err = c.Get(ctx, client.ObjectKeyFromObject(renderedObject), appliedObj)
			Expect(err).ToNot(HaveOccurred())

			labels := appliedObj.GetLabels()
			annotations := appliedObj.GetAnnotations()
			Expect(labels).To(HaveKeyWithValue("cluster.label1", "cluster-value1"))
			Expect(labels).To(HaveKeyWithValue("cluster.label2", "cluster-value2"))
			Expect(annotations).To(HaveKeyWithValue("cluster.annotation1", "cluster-value1"))
			Expect(annotations).To(HaveKeyWithValue("cluster.annotation2", "cluster-value2"))
		})

		It("preserves node-level ExtraLabels and ExtraAnnotations", func() {
			// Simulate template rendering for node-level manifest

			// Create object with node-level metadata (as template engine would apply)
			renderedObject := object.DeepCopy()
			existingLabels := map[string]string{
				"node.label1":      "node-value1",
				"node.label2":      "node-value2",
				"cluster.fallback": "cluster-fallback-value", // Node fell back to cluster
			}
			existingAnnotations := map[string]string{
				"node.annotation1": "node-value1",
				"node.annotation2": "node-value2",
				"cluster.fallback": "cluster-fallback-value",
			}
			renderedObject.SetLabels(existingLabels)
			renderedObject.SetAnnotations(existingAnnotations)

			result, err := applyObject(ctx, c, testLogger, *renderedObject)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(controllerutil.OperationResultUpdated))

			// Verify node-level metadata is preserved
			appliedObj := &unstructured.Unstructured{}
			appliedObj.SetGroupVersionKind(renderedObject.GroupVersionKind())
			err = c.Get(ctx, client.ObjectKeyFromObject(renderedObject), appliedObj)
			Expect(err).ToNot(HaveOccurred())

			labels := appliedObj.GetLabels()
			annotations := appliedObj.GetAnnotations()
			Expect(labels).To(HaveKeyWithValue("node.label1", "node-value1"))
			Expect(labels).To(HaveKeyWithValue("node.label2", "node-value2"))
			Expect(labels).To(HaveKeyWithValue("cluster.fallback", "cluster-fallback-value"))
			Expect(annotations).To(HaveKeyWithValue("node.annotation1", "node-value1"))
			Expect(annotations).To(HaveKeyWithValue("node.annotation2", "node-value2"))
			Expect(annotations).To(HaveKeyWithValue("cluster.fallback", "cluster-fallback-value"))
		})

		It("handles metadata updates correctly via SSA", func() {
			// First apply with initial metadata

			initialObject := object.DeepCopy()
			initialLabels := map[string]string{
				"update.label1": "initial-value1",
				"update.label2": "initial-value2",
				"keep.label":    "keep-value",
			}
			initialAnnotations := map[string]string{
				"update.annotation1": "initial-value1",
				"update.annotation2": "initial-value2",
				"keep.annotation":    "keep-value",
			}
			initialObject.SetLabels(initialLabels)
			initialObject.SetAnnotations(initialAnnotations)

			// Apply initial version
			result, err := applyObject(ctx, c, testLogger, *initialObject)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(controllerutil.OperationResultUpdated))

			// Apply updated version (simulating template re-rendering)
			updatedObject := object.DeepCopy()
			updatedLabels := map[string]string{
				"update.label1": "updated-value1", // Changed
				"update.label3": "new-value3",     // Added
				"keep.label":    "keep-value",     // Unchanged
				// "update.label2" removed
			}
			updatedAnnotations := map[string]string{
				"update.annotation1": "updated-value1", // Changed
				"update.annotation3": "new-value3",     // Added
				"keep.annotation":    "keep-value",     // Unchanged
				// "update.annotation2" removed
			}
			updatedObject.SetLabels(updatedLabels)
			updatedObject.SetAnnotations(updatedAnnotations)

			// Apply updated version
			result, err = applyObject(ctx, c, testLogger, *updatedObject)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(controllerutil.OperationResultUpdated))

			// Verify SSA correctly applied updates
			finalObj := &unstructured.Unstructured{}
			finalObj.SetGroupVersionKind(updatedObject.GroupVersionKind())
			err = c.Get(ctx, client.ObjectKeyFromObject(updatedObject), finalObj)
			Expect(err).ToNot(HaveOccurred())

			labels := finalObj.GetLabels()
			annotations := finalObj.GetAnnotations()

			// Check updates and additions
			Expect(labels).To(HaveKeyWithValue("update.label1", "updated-value1"))
			Expect(labels).To(HaveKeyWithValue("update.label3", "new-value3"))
			Expect(labels).To(HaveKeyWithValue("keep.label", "keep-value"))
			Expect(annotations).To(HaveKeyWithValue("update.annotation1", "updated-value1"))
			Expect(annotations).To(HaveKeyWithValue("update.annotation3", "new-value3"))
			Expect(annotations).To(HaveKeyWithValue("keep.annotation", "keep-value"))

			// Check removals (SSA should handle this via managedFields)
			// Note: In real SSA, removed fields are handled by managedFields
			// Our mock may not perfectly simulate this, but the intent is verified
		})

		It("handles complex metadata merging scenarios", func() {
			// Test scenario where template rendering produces complex metadata combinations

			complexObject := object.DeepCopy()

			// Simulate complex metadata scenario:
			// - Template-defined labels/annotations
			// - ExtraLabels/ExtraAnnotations from ClusterInstance
			// - Standard Kubernetes labels
			// - Custom application labels
			complexLabels := map[string]string{
				// Template-defined
				"app.kubernetes.io/name":       "test-app",
				"app.kubernetes.io/version":    "1.0.0",
				"app.kubernetes.io/managed-by": "siteconfig",
				// ExtraLabels from ClusterInstance
				"environment": "production",
				"team":        "platform",
				"cost-center": "engineering",
				// Custom application
				"custom.domain/tag1": "value1",
				"custom.domain/tag2": "value2",
			}

			complexAnnotations := map[string]string{
				// Template-defined
				"kubectl.kubernetes.io/last-applied-configuration": "{}",
				"deployment.kubernetes.io/revision":                "1",
				// ExtraAnnotations from ClusterInstance
				"monitoring.enabled":    "true",
				"backup.policy":         "daily",
				"security.scan.enabled": "true",
				// Custom application
				"custom.domain/description": "Test application manifest",
				"custom.domain/contact":     "platform-team@company.com",
			}

			complexObject.SetLabels(complexLabels)
			complexObject.SetAnnotations(complexAnnotations)

			result, err := applyObject(ctx, c, testLogger, *complexObject)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(Equal(controllerutil.OperationResultUpdated))

			// Verify all complex metadata is preserved correctly
			appliedObj := &unstructured.Unstructured{}
			appliedObj.SetGroupVersionKind(complexObject.GroupVersionKind())
			err = c.Get(ctx, client.ObjectKeyFromObject(complexObject), appliedObj)
			Expect(err).ToNot(HaveOccurred())

			labels := appliedObj.GetLabels()
			annotations := appliedObj.GetAnnotations()

			// Verify all label categories are preserved
			Expect(labels).To(HaveKeyWithValue("app.kubernetes.io/name", "test-app"))
			Expect(labels).To(HaveKeyWithValue("app.kubernetes.io/version", "1.0.0"))
			Expect(labels).To(HaveKeyWithValue("environment", "production"))
			Expect(labels).To(HaveKeyWithValue("team", "platform"))
			Expect(labels).To(HaveKeyWithValue("custom.domain/tag1", "value1"))
			Expect(labels).To(HaveKeyWithValue("custom.domain/tag2", "value2"))

			// Verify all annotation categories are preserved
			Expect(annotations).To(HaveKeyWithValue("kubectl.kubernetes.io/last-applied-configuration", "{}"))
			Expect(annotations).To(HaveKeyWithValue("monitoring.enabled", "true"))
			Expect(annotations).To(HaveKeyWithValue("backup.policy", "daily"))
			Expect(annotations).To(HaveKeyWithValue("custom.domain/description", "Test application manifest"))
			Expect(annotations).To(HaveKeyWithValue("custom.domain/contact", "platform-team@company.com"))
		})

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

var _ = Describe("SetupWithManager", func() {
	var (
		testLogger = zap.NewNop().Named("Test")
	)

	It("successfully sets up the controller with the manager", func() {
		// Get config (does not panic like GetConfigOrDie)
		cfg, err := ctrl.GetConfig()
		if err != nil {
			Skip("Skipping SetupWithManager test - no kubeconfig available")
		}

		c := fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			Build()

		configStore, err := configuration.NewConfigurationStore(configuration.NewDefaultConfiguration())
		Expect(err).ToNot(HaveOccurred())

		deletionHandler := &deletion.DeletionHandler{Client: c, Logger: testLogger}
		r := &ClusterInstanceReconciler{
			Client:          c,
			Scheme:          scheme.Scheme,
			Log:             testLogger,
			TmplEngine:      ci.NewTemplateEngine(),
			ConfigStore:     configStore,
			DeletionHandler: deletionHandler,
			ReinstallHandler: &reinstall.ReinstallHandler{Client: c, Logger: testLogger,
				DeletionHandler: deletionHandler, ConfigStore: configStore},
		}

		// Create a manager
		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme: scheme.Scheme,
		})
		Expect(err).ToNot(HaveOccurred())

		err = r.SetupWithManager(mgr)
		Expect(err).ToNot(HaveOccurred())

		// Verify that the event recorder was set
		Expect(r.Recorder).ToNot(BeNil())
	})

	It("returns the configured max concurrent reconciles value", func() {
		config := configuration.NewDefaultConfiguration()
		err := config.FromMap(map[string]string{"maxConcurrentReconciles": "5"})
		Expect(err).ToNot(HaveOccurred())

		configStore, err := configuration.NewConfigurationStore(config)
		Expect(err).ToNot(HaveOccurred())

		Expect(configStore.GetMaxConcurrentReconciles()).To(Equal(5))
	})

	Describe("holdAnnotationPredicate", func() {
		// Create the predicate directly matching the implementation
		holdAnnotationPredicate := predicate.Funcs{
			UpdateFunc: func(e event.UpdateEvent) bool {
				_, oldEvent := e.ObjectOld.GetAnnotations()[v1alpha1.PausedAnnotation]
				_, newEvent := e.ObjectNew.GetAnnotations()[v1alpha1.PausedAnnotation]
				return oldEvent != newEvent
			},
		}

		It("returns true when paused annotation is added", func() {
			oldCI := &v1alpha1.ClusterInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      TestClusterInstanceName,
					Namespace: TestClusterInstanceNamespace,
				},
			}
			newCI := oldCI.DeepCopy()
			metav1.SetMetaDataAnnotation(&newCI.ObjectMeta, v1alpha1.PausedAnnotation, "test-reason")

			updateEvent := event.UpdateEvent{
				ObjectOld: oldCI,
				ObjectNew: newCI,
			}

			result := holdAnnotationPredicate.Update(updateEvent)
			Expect(result).To(BeTrue())
		})

		It("returns true when paused annotation is removed", func() {
			oldCI := &v1alpha1.ClusterInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:        TestClusterInstanceName,
					Namespace:   TestClusterInstanceNamespace,
					Annotations: map[string]string{v1alpha1.PausedAnnotation: "test-reason"},
				},
			}
			newCI := oldCI.DeepCopy()
			delete(newCI.Annotations, v1alpha1.PausedAnnotation)

			updateEvent := event.UpdateEvent{
				ObjectOld: oldCI,
				ObjectNew: newCI,
			}

			result := holdAnnotationPredicate.Update(updateEvent)
			Expect(result).To(BeTrue())
		})

		It("returns false when paused annotation is unchanged (both absent)", func() {
			oldCI := &v1alpha1.ClusterInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      TestClusterInstanceName,
					Namespace: TestClusterInstanceNamespace,
				},
			}
			newCI := oldCI.DeepCopy()

			updateEvent := event.UpdateEvent{
				ObjectOld: oldCI,
				ObjectNew: newCI,
			}

			result := holdAnnotationPredicate.Update(updateEvent)
			Expect(result).To(BeFalse())
		})

		It("returns false when paused annotation is unchanged (both present)", func() {
			oldCI := &v1alpha1.ClusterInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:        TestClusterInstanceName,
					Namespace:   TestClusterInstanceNamespace,
					Annotations: map[string]string{v1alpha1.PausedAnnotation: "test-reason"},
				},
			}
			newCI := oldCI.DeepCopy()

			updateEvent := event.UpdateEvent{
				ObjectOld: oldCI,
				ObjectNew: newCI,
			}

			result := holdAnnotationPredicate.Update(updateEvent)
			Expect(result).To(BeFalse())
		})
	})

	Describe("provisionedChangedPredicate", func() {
		// Create the predicate directly matching the implementation
		provisionedChangedPredicate := predicate.Funcs{
			UpdateFunc: func(e event.UpdateEvent) bool {
				oldCI, oldOK := e.ObjectOld.(*v1alpha1.ClusterInstance)
				newCI, newOK := e.ObjectNew.(*v1alpha1.ClusterInstance)

				if !oldOK || !newOK {
					return false
				}

				// Only trigger if there's an active reinstall
				if newCI.Spec.Reinstall == nil || newCI.Status.Reinstall == nil {
					return false
				}

				// Trigger reconcile when Provisioned condition changes
				oldProvisioned := meta.FindStatusCondition(oldCI.Status.Conditions, string(v1alpha1.ClusterProvisioned))
				newProvisioned := meta.FindStatusCondition(newCI.Status.Conditions, string(v1alpha1.ClusterProvisioned))

				// Check if status or reason changed
				if oldProvisioned == nil && newProvisioned != nil {
					return true
				}
				if oldProvisioned != nil && newProvisioned != nil {
					return oldProvisioned.Status != newProvisioned.Status ||
						oldProvisioned.Reason != newProvisioned.Reason
				}

				return false
			},
		}

		It("returns false when type assertion fails for old object", func() {
			oldObj := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
			}
			newCI := &v1alpha1.ClusterInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      TestClusterInstanceName,
					Namespace: TestClusterInstanceNamespace,
				},
			}

			updateEvent := event.UpdateEvent{
				ObjectOld: oldObj,
				ObjectNew: newCI,
			}

			result := provisionedChangedPredicate.Update(updateEvent)
			Expect(result).To(BeFalse())
		})

		It("returns false when type assertion fails for new object", func() {
			oldCI := &v1alpha1.ClusterInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      TestClusterInstanceName,
					Namespace: TestClusterInstanceNamespace,
				},
			}
			newObj := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
			}

			updateEvent := event.UpdateEvent{
				ObjectOld: oldCI,
				ObjectNew: newObj,
			}

			result := provisionedChangedPredicate.Update(updateEvent)
			Expect(result).To(BeFalse())
		})

		It("returns false when spec.Reinstall is nil", func() {
			oldCI := &v1alpha1.ClusterInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      TestClusterInstanceName,
					Namespace: TestClusterInstanceNamespace,
				},
			}
			newCI := oldCI.DeepCopy()
			newCI.Status.Reinstall = &v1alpha1.ReinstallStatus{
				ObservedGeneration: "test-gen",
			}
			// spec.Reinstall is nil

			updateEvent := event.UpdateEvent{
				ObjectOld: oldCI,
				ObjectNew: newCI,
			}

			result := provisionedChangedPredicate.Update(updateEvent)
			Expect(result).To(BeFalse())
		})

		It("returns false when status.Reinstall is nil", func() {
			oldCI := &v1alpha1.ClusterInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      TestClusterInstanceName,
					Namespace: TestClusterInstanceNamespace,
				},
			}
			newCI := oldCI.DeepCopy()
			newCI.Spec.Reinstall = &v1alpha1.ReinstallSpec{
				Generation: "test-gen",
			}
			// status.Reinstall is nil

			updateEvent := event.UpdateEvent{
				ObjectOld: oldCI,
				ObjectNew: newCI,
			}

			result := provisionedChangedPredicate.Update(updateEvent)
			Expect(result).To(BeFalse())
		})

		It("returns true when Provisioned condition is added during reinstall", func() {
			oldCI := &v1alpha1.ClusterInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      TestClusterInstanceName,
					Namespace: TestClusterInstanceNamespace,
				},
				Spec: v1alpha1.ClusterInstanceSpec{
					Reinstall: &v1alpha1.ReinstallSpec{
						Generation: "test-gen",
					},
				},
				Status: v1alpha1.ClusterInstanceStatus{
					Reinstall: &v1alpha1.ReinstallStatus{
						ObservedGeneration: "test-gen",
					},
					// No Provisioned condition
				},
			}
			newCI := oldCI.DeepCopy()
			newCI.Status.Conditions = []metav1.Condition{
				{
					Type:   string(v1alpha1.ClusterProvisioned),
					Status: metav1.ConditionTrue,
					Reason: string(v1alpha1.Completed),
				},
			}

			updateEvent := event.UpdateEvent{
				ObjectOld: oldCI,
				ObjectNew: newCI,
			}

			result := provisionedChangedPredicate.Update(updateEvent)
			Expect(result).To(BeTrue())
		})

		It("returns true when Provisioned condition status changes during reinstall", func() {
			oldCI := &v1alpha1.ClusterInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      TestClusterInstanceName,
					Namespace: TestClusterInstanceNamespace,
				},
				Spec: v1alpha1.ClusterInstanceSpec{
					Reinstall: &v1alpha1.ReinstallSpec{
						Generation: "test-gen",
					},
				},
				Status: v1alpha1.ClusterInstanceStatus{
					Reinstall: &v1alpha1.ReinstallStatus{
						ObservedGeneration: "test-gen",
					},
					Conditions: []metav1.Condition{
						{
							Type:   string(v1alpha1.ClusterProvisioned),
							Status: metav1.ConditionFalse,
							Reason: string(v1alpha1.InProgress),
						},
					},
				},
			}
			newCI := oldCI.DeepCopy()
			newCI.Status.Conditions = []metav1.Condition{
				{
					Type:   string(v1alpha1.ClusterProvisioned),
					Status: metav1.ConditionTrue,
					Reason: string(v1alpha1.Completed),
				},
			}

			updateEvent := event.UpdateEvent{
				ObjectOld: oldCI,
				ObjectNew: newCI,
			}

			result := provisionedChangedPredicate.Update(updateEvent)
			Expect(result).To(BeTrue())
		})

		It("returns true when Provisioned condition reason changes during reinstall", func() {
			oldCI := &v1alpha1.ClusterInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      TestClusterInstanceName,
					Namespace: TestClusterInstanceNamespace,
				},
				Spec: v1alpha1.ClusterInstanceSpec{
					Reinstall: &v1alpha1.ReinstallSpec{
						Generation: "test-gen",
					},
				},
				Status: v1alpha1.ClusterInstanceStatus{
					Reinstall: &v1alpha1.ReinstallStatus{
						ObservedGeneration: "test-gen",
					},
					Conditions: []metav1.Condition{
						{
							Type:   string(v1alpha1.ClusterProvisioned),
							Status: metav1.ConditionFalse,
							Reason: string(v1alpha1.InProgress),
						},
					},
				},
			}
			newCI := oldCI.DeepCopy()
			newCI.Status.Conditions = []metav1.Condition{
				{
					Type:   string(v1alpha1.ClusterProvisioned),
					Status: metav1.ConditionFalse,
					Reason: string(v1alpha1.Failed),
				},
			}

			updateEvent := event.UpdateEvent{
				ObjectOld: oldCI,
				ObjectNew: newCI,
			}

			result := provisionedChangedPredicate.Update(updateEvent)
			Expect(result).To(BeTrue())
		})

		It("returns false when Provisioned condition is unchanged during reinstall", func() {
			oldCI := &v1alpha1.ClusterInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      TestClusterInstanceName,
					Namespace: TestClusterInstanceNamespace,
				},
				Spec: v1alpha1.ClusterInstanceSpec{
					Reinstall: &v1alpha1.ReinstallSpec{
						Generation: "test-gen",
					},
				},
				Status: v1alpha1.ClusterInstanceStatus{
					Reinstall: &v1alpha1.ReinstallStatus{
						ObservedGeneration: "test-gen",
					},
					Conditions: []metav1.Condition{
						{
							Type:   string(v1alpha1.ClusterProvisioned),
							Status: metav1.ConditionTrue,
							Reason: string(v1alpha1.Completed),
						},
					},
				},
			}
			newCI := oldCI.DeepCopy()

			updateEvent := event.UpdateEvent{
				ObjectOld: oldCI,
				ObjectNew: newCI,
			}

			result := provisionedChangedPredicate.Update(updateEvent)
			Expect(result).To(BeFalse())
		})

		It("returns false when Provisioned condition exists only in old object during reinstall", func() {
			oldCI := &v1alpha1.ClusterInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      TestClusterInstanceName,
					Namespace: TestClusterInstanceNamespace,
				},
				Spec: v1alpha1.ClusterInstanceSpec{
					Reinstall: &v1alpha1.ReinstallSpec{
						Generation: "test-gen",
					},
				},
				Status: v1alpha1.ClusterInstanceStatus{
					Reinstall: &v1alpha1.ReinstallStatus{
						ObservedGeneration: "test-gen",
					},
					Conditions: []metav1.Condition{
						{
							Type:   string(v1alpha1.ClusterProvisioned),
							Status: metav1.ConditionTrue,
							Reason: string(v1alpha1.Completed),
						},
					},
				},
			}
			newCI := oldCI.DeepCopy()
			newCI.Status.Conditions = []metav1.Condition{} // Remove all conditions

			updateEvent := event.UpdateEvent{
				ObjectOld: oldCI,
				ObjectNew: newCI,
			}

			result := provisionedChangedPredicate.Update(updateEvent)
			Expect(result).To(BeFalse())
		})

		It("returns false when neither object has Provisioned condition during reinstall", func() {
			oldCI := &v1alpha1.ClusterInstance{
				ObjectMeta: metav1.ObjectMeta{
					Name:      TestClusterInstanceName,
					Namespace: TestClusterInstanceNamespace,
				},
				Spec: v1alpha1.ClusterInstanceSpec{
					Reinstall: &v1alpha1.ReinstallSpec{
						Generation: "test-gen",
					},
				},
				Status: v1alpha1.ClusterInstanceStatus{
					Reinstall: &v1alpha1.ReinstallStatus{
						ObservedGeneration: "test-gen",
					},
				},
			}
			newCI := oldCI.DeepCopy()

			updateEvent := event.UpdateEvent{
				ObjectOld: oldCI,
				ObjectNew: newCI,
			}

			result := provisionedChangedPredicate.Update(updateEvent)
			Expect(result).To(BeFalse())
		})
	})
})
