/*
Copyright 2025.

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

package reinstall

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"
	clusterv1 "open-cluster-management.io/api/cluster/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	"github.com/stolostron/siteconfig/internal/controller/configuration"
	cierrors "github.com/stolostron/siteconfig/internal/controller/errors"
	"github.com/stolostron/siteconfig/internal/controller/mocks"

	"go.uber.org/mock/gomock"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type testClusterInstanceBuilder struct {
	ci *v1alpha1.ClusterInstance
}

func newTestClusterInstance(name, namespace string) *testClusterInstanceBuilder {
	return &testClusterInstanceBuilder{
		ci: &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				ClusterName: name,
			},
			Status: v1alpha1.ClusterInstanceStatus{
				Conditions: []metav1.Condition{},
			},
		},
	}
}

func (b *testClusterInstanceBuilder) WithReinstallSpec(generation string, mode v1alpha1.PreservationMode) *testClusterInstanceBuilder {
	b.ci.Spec.Reinstall = &v1alpha1.ReinstallSpec{
		Generation:       generation,
		PreservationMode: mode,
	}
	return b
}

func (b *testClusterInstanceBuilder) WithReinstallStatus(observedGen, inProgressGen string) *testClusterInstanceBuilder {
	b.ci.Status.Reinstall = &v1alpha1.ReinstallStatus{
		ObservedGeneration:   observedGen,
		InProgressGeneration: inProgressGen,
		Conditions:           []metav1.Condition{},
	}
	return b
}

func (b *testClusterInstanceBuilder) WithReinstallCondition(condType v1alpha1.ClusterInstanceConditionType, status metav1.ConditionStatus, reason v1alpha1.ClusterInstanceConditionReason) *testClusterInstanceBuilder {
	if b.ci.Status.Reinstall == nil {
		b.ci.Status.Reinstall = &v1alpha1.ReinstallStatus{
			Conditions: []metav1.Condition{},
		}
	}
	condition := metav1.Condition{
		Type:               string(condType),
		Status:             status,
		Reason:             string(reason),
		LastTransitionTime: metav1.Now(),
	}
	b.ci.Status.Reinstall.Conditions = append(b.ci.Status.Reinstall.Conditions, condition)
	return b
}

func (b *testClusterInstanceBuilder) WithProvisionedCondition(status metav1.ConditionStatus, transitionTime time.Time) *testClusterInstanceBuilder {
	condition := metav1.Condition{
		Type:               string(v1alpha1.ClusterProvisioned),
		Status:             status,
		Reason:             "TestReason",
		Message:            "Test message",
		LastTransitionTime: metav1.NewTime(transitionTime),
	}
	b.ci.Status.Conditions = append(b.ci.Status.Conditions, condition)
	return b
}

func (b *testClusterInstanceBuilder) WithManagedClusterManifest(name string) *testClusterInstanceBuilder {
	apiGroup := ptr.To("cluster.open-cluster-management.io/v1")
	b.ci.Status.ManifestsRendered = append(b.ci.Status.ManifestsRendered, v1alpha1.ManifestReference{
		APIGroup: apiGroup,
		Kind:     "ManagedCluster",
		Name:     name,
		Status:   v1alpha1.ManifestRenderedSuccess,
	})
	return b
}

func (b *testClusterInstanceBuilder) Build() *v1alpha1.ClusterInstance {
	return b.ci
}

type testManagedClusterBuilder struct {
	mc *clusterv1.ManagedCluster
}

func newTestManagedCluster(name string) *testManagedClusterBuilder {
	return &testManagedClusterBuilder{
		mc: &clusterv1.ManagedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Annotations: make(map[string]string),
			},
			Status: clusterv1.ManagedClusterStatus{
				Conditions: []metav1.Condition{},
			},
		},
	}
}

func (b *testManagedClusterBuilder) WithHealthyConditions() *testManagedClusterBuilder {
	b.mc.Status.Conditions = []metav1.Condition{
		{
			Type:   clusterv1.ManagedClusterConditionAvailable,
			Status: metav1.ConditionTrue,
			Reason: "ManagedClusterAvailable",
		},
		{
			Type:   "ManagedClusterImportSucceeded",
			Status: metav1.ConditionTrue,
			Reason: "ManagedClusterImported",
		},
		{
			Type:   clusterv1.ManagedClusterConditionJoined,
			Status: metav1.ConditionTrue,
			Reason: "ManagedClusterJoined",
		},
	}
	return b
}

func (b *testManagedClusterBuilder) WithUnhealthyAvailableCondition() *testManagedClusterBuilder {
	b.mc.Status.Conditions = []metav1.Condition{
		{
			Type:    clusterv1.ManagedClusterConditionAvailable,
			Status:  metav1.ConditionUnknown,
			Reason:  "ManagedClusterLeaseUpdateStopped",
			Message: "Cluster agent stopped updating its lease.",
		},
		{
			Type:   "ManagedClusterImportSucceeded",
			Status: metav1.ConditionTrue,
			Reason: "ManagedClusterImported",
		},
		{
			Type:   clusterv1.ManagedClusterConditionJoined,
			Status: metav1.ConditionTrue,
			Reason: "ManagedClusterJoined",
		},
	}
	return b
}

func (b *testManagedClusterBuilder) WithImportInProgress() *testManagedClusterBuilder {
	b.mc.Status.Conditions = []metav1.Condition{
		{
			Type:   clusterv1.ManagedClusterConditionAvailable,
			Status: metav1.ConditionTrue,
			Reason: "ManagedClusterAvailable",
		},
		{
			Type:   "ManagedClusterImportSucceeded",
			Status: metav1.ConditionFalse,
			Reason: "ManagedClusterImporting",
		},
		{
			Type:   clusterv1.ManagedClusterConditionJoined,
			Status: metav1.ConditionTrue,
			Reason: "ManagedClusterJoined",
		},
	}
	return b
}

func (b *testManagedClusterBuilder) Build() *clusterv1.ManagedCluster {
	return b.mc
}

var _ = Describe("EnsureClusterIsReimported", func() {
	var (
		ctx             context.Context
		testLogger      *zap.Logger
		configStore     *configuration.ConfigurationStore
		handler         *ReinstallHandler
		c               client.Client
		clusterInstance *v1alpha1.ClusterInstance
		managedCluster  *clusterv1.ManagedCluster
	)

	BeforeEach(func() {
		ctx = context.Background()
		testLogger = zap.NewNop()

		var err error
		configStore, err = configuration.NewConfigurationStore(configuration.NewDefaultConfiguration())
		Expect(err).ToNot(HaveOccurred())
		// Enable reinstalls for tests that need it
		configStore.SetAllowReinstalls(true)
	})

	Context("when reinstalls are disabled", func() {
		BeforeEach(func() {
			configStore.SetAllowReinstalls(false)

			clusterInstance = newTestClusterInstance("test-cluster", "test-ns").
				WithReinstallSpec("gen-1", v1alpha1.PreservationModeNone).
				WithReinstallStatus("gen-1", "gen-1").
				WithReinstallCondition(v1alpha1.ReinstallRequestProcessed, metav1.ConditionTrue, v1alpha1.Completed).
				Build()

			c = fakeclient.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(clusterInstance).
				WithStatusSubresource(&v1alpha1.ClusterInstance{}).
				Build()

			handler = &ReinstallHandler{
				ConfigStore: configStore,
				Client:      c,
				Logger:      testLogger,
			}
		})

		It("should return immediately without checking reimport status", func() {
			result, reimportNeeded, err := handler.EnsureClusterIsReimported(ctx, testLogger, clusterInstance)

			Expect(err).NotTo(HaveOccurred())
			Expect(reimportNeeded).To(BeFalse())
			Expect(result.Requeue).To(BeFalse())

			// Verify no condition was set
			updatedCI := &v1alpha1.ClusterInstance{}
			err = c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), updatedCI)
			Expect(err).NotTo(HaveOccurred())

			condition := findReinstallStatusCondition(updatedCI, v1alpha1.ReinstallClusterReimported)
			Expect(condition).To(BeNil())
		})
	})

	Context("when no reinstall has been processed", func() {
		BeforeEach(func() {
			clusterInstance = newTestClusterInstance("test-cluster", "test-ns").Build()
			clusterInstance.Status.Reinstall = nil

			c = fakeclient.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(clusterInstance).
				WithStatusSubresource(&v1alpha1.ClusterInstance{}).
				Build()

			handler = &ReinstallHandler{
				ConfigStore: configStore,
				Client:      c,
				Logger:      testLogger,
			}
		})

		It("should return immediately with reimportNeeded=false", func() {
			result, reimportNeeded, err := handler.EnsureClusterIsReimported(ctx, testLogger, clusterInstance)

			Expect(err).NotTo(HaveOccurred())
			Expect(reimportNeeded).To(BeFalse())
			Expect(result.Requeue).To(BeFalse())
		})
	})

	Context("when reinstall has not completed yet", func() {
		BeforeEach(func() {
			clusterInstance = newTestClusterInstance("test-cluster", "test-ns").
				WithReinstallSpec("gen-1", v1alpha1.PreservationModeNone).
				WithReinstallStatus("", "gen-1").
				WithReinstallCondition(v1alpha1.ReinstallRequestProcessed, metav1.ConditionFalse, v1alpha1.InProgress).
				Build()

			c = fakeclient.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(clusterInstance).
				WithStatusSubresource(&v1alpha1.ClusterInstance{}).
				Build()

			handler = &ReinstallHandler{
				ConfigStore: configStore,
				Client:      c,
				Logger:      testLogger,
			}
		})

		It("should return immediately with reimportNeeded=false", func() {
			result, reimportNeeded, err := handler.EnsureClusterIsReimported(ctx, testLogger, clusterInstance)

			Expect(err).NotTo(HaveOccurred())
			Expect(reimportNeeded).To(BeFalse())
			Expect(result.Requeue).To(BeFalse())
		})
	})

	Context("when reimport already completed", func() {
		BeforeEach(func() {
			clusterInstance = newTestClusterInstance("test-cluster", "test-ns").
				WithReinstallSpec("gen-1", v1alpha1.PreservationModeNone).
				WithReinstallStatus("gen-1", "gen-1").
				WithReinstallCondition(v1alpha1.ReinstallRequestProcessed, metav1.ConditionTrue, v1alpha1.Completed).
				WithReinstallCondition(v1alpha1.ReinstallClusterReimported, metav1.ConditionTrue, v1alpha1.Completed).
				Build()

			c = fakeclient.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(clusterInstance).
				WithStatusSubresource(&v1alpha1.ClusterInstance{}).
				Build()

			handler = &ReinstallHandler{
				ConfigStore: configStore,
				Client:      c,
				Logger:      testLogger,
			}
		})

		It("should return immediately with reimportNeeded=false", func() {
			result, reimportNeeded, err := handler.EnsureClusterIsReimported(ctx, testLogger, clusterInstance)

			Expect(err).NotTo(HaveOccurred())
			Expect(reimportNeeded).To(BeFalse())
			Expect(result.Requeue).To(BeFalse())
		})
	})

	Context("when provisioning not yet complete", func() {
		BeforeEach(func() {
			clusterInstance = newTestClusterInstance("test-cluster", "test-ns").
				WithReinstallSpec("gen-1", v1alpha1.PreservationModeNone).
				WithReinstallStatus("gen-1", "gen-1").
				WithReinstallCondition(v1alpha1.ReinstallRequestProcessed, metav1.ConditionTrue, v1alpha1.Completed).
				Build()
			// No Provisioned condition set

			c = fakeclient.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(clusterInstance).
				WithStatusSubresource(&v1alpha1.ClusterInstance{}).
				Build()

			handler = &ReinstallHandler{
				ConfigStore: configStore,
				Client:      c,
				Logger:      testLogger,
			}
		})

		It("should return with reimportNeeded=false and wait for Provisioned condition predicate", func() {
			result, reimportNeeded, err := handler.EnsureClusterIsReimported(ctx, testLogger, clusterInstance)

			Expect(err).NotTo(HaveOccurred())
			Expect(reimportNeeded).To(BeFalse())
			Expect(result.Requeue).To(BeFalse())
		})
	})

	Context("when ManagedCluster is healthy after provisioning", func() {
		BeforeEach(func() {
			provisionedTime := time.Now().Add(-5 * time.Minute)
			clusterInstance = newTestClusterInstance("test-cluster", "test-ns").
				WithReinstallSpec("gen-1", v1alpha1.PreservationModeNone).
				WithReinstallStatus("gen-1", "gen-1").
				WithReinstallCondition(v1alpha1.ReinstallRequestProcessed, metav1.ConditionTrue, v1alpha1.Completed).
				WithProvisionedCondition(metav1.ConditionTrue, provisionedTime).
				WithManagedClusterManifest("test-cluster").
				Build()

			managedCluster = newTestManagedCluster("test-cluster").
				WithHealthyConditions().
				Build()

			c = fakeclient.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(clusterInstance, managedCluster).
				WithStatusSubresource(&v1alpha1.ClusterInstance{}, &clusterv1.ManagedCluster{}).
				Build()

			handler = &ReinstallHandler{
				ConfigStore: configStore,
				Client:      c,
				Logger:      testLogger,
			}
		})

		It("should mark reimport as completed and return reimportNeeded=false", func() {
			result, reimportNeeded, err := handler.EnsureClusterIsReimported(ctx, testLogger, clusterInstance)

			Expect(err).NotTo(HaveOccurred())
			Expect(reimportNeeded).To(BeFalse())
			Expect(result.Requeue).To(BeFalse())

			updatedCI := &v1alpha1.ClusterInstance{}
			err = c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), updatedCI)
			Expect(err).NotTo(HaveOccurred())

			condition := findReinstallStatusCondition(updatedCI, v1alpha1.ReinstallClusterReimported)
			Expect(condition).NotTo(BeNil())
			Expect(condition.Status).To(Equal(metav1.ConditionTrue))
			Expect(condition.Reason).To(Equal(string(v1alpha1.Completed)))
		})
	})

	Context("when cluster is unhealthy but within grace period", func() {
		BeforeEach(func() {
			provisionedTime := time.Now().Add(-2 * time.Minute) // Within 5-minute grace period
			clusterInstance = newTestClusterInstance("test-cluster", "test-ns").
				WithReinstallSpec("gen-1", v1alpha1.PreservationModeNone).
				WithReinstallStatus("gen-1", "gen-1").
				WithReinstallCondition(v1alpha1.ReinstallRequestProcessed, metav1.ConditionTrue, v1alpha1.Completed).
				WithProvisionedCondition(metav1.ConditionTrue, provisionedTime).
				WithManagedClusterManifest("test-cluster").
				Build()

			managedCluster = newTestManagedCluster("test-cluster").
				WithUnhealthyAvailableCondition().
				Build()

			c = fakeclient.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(clusterInstance, managedCluster).
				WithStatusSubresource(&v1alpha1.ClusterInstance{}, &clusterv1.ManagedCluster{}).
				Build()

			handler = &ReinstallHandler{
				ConfigStore: configStore,
				Client:      c,
				Logger:      testLogger,
			}
		})

		It("should wait for auto-recovery with reimportNeeded=true", func() {
			result, reimportNeeded, err := handler.EnsureClusterIsReimported(ctx, testLogger, clusterInstance)

			Expect(err).NotTo(HaveOccurred())
			Expect(reimportNeeded).To(BeTrue())
			Expect(result.Requeue).To(BeTrue())
			Expect(result.RequeueAfter).To(Equal(GracePeriodRecheckInterval))

			updatedCI := &v1alpha1.ClusterInstance{}
			err = c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), updatedCI)
			Expect(err).NotTo(HaveOccurred())

			condition := findReinstallStatusCondition(updatedCI, v1alpha1.ReinstallClusterReimported)
			Expect(condition).NotTo(BeNil())
			Expect(condition.Status).To(Equal(metav1.ConditionFalse))
			Expect(condition.Reason).To(Equal(string(v1alpha1.InProgress)))
			Expect(condition.Message).To(ContainSubstring("auto-recovery"))
		})
	})

	Context("when cluster is unhealthy and grace period has expired", func() {
		BeforeEach(func() {
			provisionedTime := time.Now().Add(-15 * time.Minute) // Beyond 5-minute grace period
			clusterInstance = newTestClusterInstance("test-cluster", "test-ns").
				WithReinstallSpec("gen-1", v1alpha1.PreservationModeNone).
				WithReinstallStatus("gen-1", "gen-1").
				WithReinstallCondition(v1alpha1.ReinstallRequestProcessed, metav1.ConditionTrue, v1alpha1.Completed).
				WithProvisionedCondition(metav1.ConditionTrue, provisionedTime).
				WithManagedClusterManifest("test-cluster").
				Build()

			managedCluster = newTestManagedCluster("test-cluster").
				WithUnhealthyAvailableCondition().
				Build()

			c = fakeclient.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(clusterInstance, managedCluster).
				WithStatusSubresource(&v1alpha1.ClusterInstance{}, &clusterv1.ManagedCluster{}).
				Build()

			handler = &ReinstallHandler{
				ConfigStore:        configStore,
				Client:             c,
				Logger:             testLogger,
				SpokeClientFactory: &DefaultSpokeClientFactory{},
			}
		})

		It("should attempt reimport and fail when admin kubeconfig secret is missing", func() {
			// This tests the orchestration flow up to the performReimport step.
			// Without an admin kubeconfig secret, extractKubeconfigFromSecret fails at secret retrieval.
			result, reimportNeeded, err := handler.EnsureClusterIsReimported(ctx, testLogger, clusterInstance)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to get kubeconfig"))
			Expect(err.Error()).To(ContainSubstring("failed to get admin kubeconfig secret"))
			Expect(reimportNeeded).To(BeFalse())
			Expect(result.Requeue).To(BeFalse())

			updatedCI := &v1alpha1.ClusterInstance{}
			err = c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), updatedCI)
			Expect(err).NotTo(HaveOccurred())

			condition := findReinstallStatusCondition(updatedCI, v1alpha1.ReinstallClusterReimported)
			Expect(condition).NotTo(BeNil())
			Expect(condition.Status).To(Equal(metav1.ConditionFalse))
			Expect(condition.Reason).To(Equal(string(v1alpha1.Failed)))
			Expect(condition.Message).To(ContainSubstring("Failed to perform reimport"))
		})
	})

	Context("when cluster is unhealthy, grace period expired, and admin kubeconfig exists", func() {
		BeforeEach(func() {
			provisionedTime := time.Now().Add(-15 * time.Minute) // Beyond 5-minute grace period
			clusterInstance = newTestClusterInstance("test-cluster", "test-cluster").
				WithReinstallSpec("gen-1", v1alpha1.PreservationModeNone).
				WithReinstallStatus("gen-1", "gen-1").
				WithReinstallCondition(v1alpha1.ReinstallRequestProcessed, metav1.ConditionTrue, v1alpha1.Completed).
				WithProvisionedCondition(metav1.ConditionTrue, provisionedTime).
				WithManagedClusterManifest("test-cluster").
				Build()

			managedCluster = newTestManagedCluster("test-cluster").
				WithUnhealthyAvailableCondition().
				Build()

			// Create a valid admin kubeconfig secret with proper format
			// This tests the orchestration flow further into performReimport
			validKubeconfig := []byte(`
apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://unreachable-spoke.example.com:6443
    insecure-skip-tls-verify: true
  name: test-cluster
contexts:
- context:
    cluster: test-cluster
    user: admin
  name: default
current-context: default
users:
- name: admin
  user:
    token: test-token
`)
			adminKubeconfigSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster-admin-kubeconfig",
					Namespace: "test-cluster",
				},
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{
					"kubeconfig": validKubeconfig,
				},
			}

			c = fakeclient.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(clusterInstance, managedCluster, adminKubeconfigSecret).
				WithStatusSubresource(&v1alpha1.ClusterInstance{}, &clusterv1.ManagedCluster{}).
				Build()

			handler = &ReinstallHandler{
				ConfigStore:        configStore,
				Client:             c,
				Logger:             testLogger,
				SpokeClientFactory: &DefaultSpokeClientFactory{},
			}
		})

		It("should progress to spoke connectivity validation and fail (unreachable spoke)", func() {
			// This tests the full orchestration flow up to the unmockable boundary.
			// With a valid kubeconfig format, the code progresses to validateSpokeConnectivity
			// which fails because the spoke cluster is unreachable.
			result, reimportNeeded, err := handler.EnsureClusterIsReimported(ctx, testLogger, clusterInstance)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to create spoke client"))
			// The error should indicate connectivity validation failure
			Expect(err.Error()).To(ContainSubstring("spoke cluster connectivity validation failed"))
			Expect(reimportNeeded).To(BeFalse())
			Expect(result.Requeue).To(BeFalse())

			updatedCI := &v1alpha1.ClusterInstance{}
			err = c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), updatedCI)
			Expect(err).NotTo(HaveOccurred())

			condition := findReinstallStatusCondition(updatedCI, v1alpha1.ReinstallClusterReimported)
			Expect(condition).NotTo(BeNil())
			Expect(condition.Status).To(Equal(metav1.ConditionFalse))
			Expect(condition.Reason).To(Equal(string(v1alpha1.Failed)))
		})
	})

	Context("when import is already in progress", func() {
		BeforeEach(func() {
			provisionedTime := time.Now().Add(-15 * time.Minute)
			clusterInstance = newTestClusterInstance("test-cluster", "test-ns").
				WithReinstallSpec("gen-1", v1alpha1.PreservationModeNone).
				WithReinstallStatus("gen-1", "gen-1").
				WithReinstallCondition(v1alpha1.ReinstallRequestProcessed, metav1.ConditionTrue, v1alpha1.Completed).
				WithProvisionedCondition(metav1.ConditionTrue, provisionedTime).
				WithManagedClusterManifest("test-cluster").
				Build()

			managedCluster = newTestManagedCluster("test-cluster").
				WithImportInProgress().
				Build()

			c = fakeclient.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(clusterInstance, managedCluster).
				WithStatusSubresource(&v1alpha1.ClusterInstance{}, &clusterv1.ManagedCluster{}).
				Build()

			handler = &ReinstallHandler{
				ConfigStore: configStore,
				Client:      c,
				Logger:      testLogger,
			}
		})

		It("should wait for import completion with reimportNeeded=true", func() {
			result, reimportNeeded, err := handler.EnsureClusterIsReimported(ctx, testLogger, clusterInstance)

			Expect(err).NotTo(HaveOccurred())
			Expect(reimportNeeded).To(BeTrue())
			Expect(result.Requeue).To(BeTrue())
			Expect(result.RequeueAfter).To(Equal(30 * time.Second))

			updatedCI := &v1alpha1.ClusterInstance{}
			err = c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), updatedCI)
			Expect(err).NotTo(HaveOccurred())

			condition := findReinstallStatusCondition(updatedCI, v1alpha1.ReinstallClusterReimported)
			Expect(condition).NotTo(BeNil())
			Expect(condition.Status).To(Equal(metav1.ConditionFalse))
			Expect(condition.Reason).To(Equal(string(v1alpha1.InProgress)))
			Expect(condition.Message).To(ContainSubstring("Import already in progress"))
		})
	})

	Context("when reimport already initiated (condition has ReimportInitiated reason)", func() {
		BeforeEach(func() {
			provisionedTime := time.Now().Add(-15 * time.Minute)
			clusterInstance = newTestClusterInstance("test-cluster", "test-ns").
				WithReinstallSpec("gen-1", v1alpha1.PreservationModeNone).
				WithReinstallStatus("gen-1", "gen-1").
				WithReinstallCondition(v1alpha1.ReinstallRequestProcessed, metav1.ConditionTrue, v1alpha1.Completed).
				WithReinstallCondition(v1alpha1.ReinstallClusterReimported, metav1.ConditionFalse, v1alpha1.ReimportInitiated).
				WithProvisionedCondition(metav1.ConditionTrue, provisionedTime).
				WithManagedClusterManifest("test-cluster").
				Build()

			managedCluster = newTestManagedCluster("test-cluster").
				WithUnhealthyAvailableCondition().
				Build()

			c = fakeclient.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(clusterInstance, managedCluster).
				WithStatusSubresource(&v1alpha1.ClusterInstance{}, &clusterv1.ManagedCluster{}).
				Build()

			handler = &ReinstallHandler{
				ConfigStore: configStore,
				Client:      c,
				Logger:      testLogger,
			}
		})

		It("should skip performReimport and wait for cluster to become healthy", func() {
			result, reimportNeeded, err := handler.EnsureClusterIsReimported(ctx, testLogger, clusterInstance)

			Expect(err).NotTo(HaveOccurred())
			Expect(reimportNeeded).To(BeTrue())
			Expect(result.Requeue).To(BeTrue())
			Expect(result.RequeueAfter).To(Equal(30 * time.Second))

			// Condition should NOT be updated - it should keep the existing ReimportInitiated reason
			updatedCI := &v1alpha1.ClusterInstance{}
			err = c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), updatedCI)
			Expect(err).NotTo(HaveOccurred())

			condition := findReinstallStatusCondition(updatedCI, v1alpha1.ReinstallClusterReimported)
			Expect(condition).NotTo(BeNil())
			Expect(condition.Status).To(Equal(metav1.ConditionFalse))
			Expect(condition.Reason).To(Equal(string(v1alpha1.ReimportInitiated)))
		})
	})

	Context("when ManagedCluster not found in manifests", func() {
		BeforeEach(func() {
			provisionedTime := time.Now().Add(-5 * time.Minute)
			clusterInstance = newTestClusterInstance("test-cluster", "test-ns").
				WithReinstallSpec("gen-1", v1alpha1.PreservationModeNone).
				WithReinstallStatus("gen-1", "gen-1").
				WithReinstallCondition(v1alpha1.ReinstallRequestProcessed, metav1.ConditionTrue, v1alpha1.Completed).
				WithProvisionedCondition(metav1.ConditionTrue, provisionedTime).
				Build()
			// No ManagedCluster manifest added

			c = fakeclient.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(clusterInstance).
				WithStatusSubresource(&v1alpha1.ClusterInstance{}).
				Build()

			handler = &ReinstallHandler{
				ConfigStore: configStore,
				Client:      c,
				Logger:      testLogger,
			}
		})

		It("should set condition to InProgress waiting for rendering", func() {
			result, reimportNeeded, err := handler.EnsureClusterIsReimported(ctx, testLogger, clusterInstance)

			Expect(err).NotTo(HaveOccurred())
			Expect(reimportNeeded).To(BeTrue())
			Expect(result.Requeue).To(BeFalse())

			updatedCI := &v1alpha1.ClusterInstance{}
			err = c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), updatedCI)
			Expect(err).NotTo(HaveOccurred())

			condition := findReinstallStatusCondition(updatedCI, v1alpha1.ReinstallClusterReimported)
			Expect(condition).NotTo(BeNil())
			Expect(condition.Status).To(Equal(metav1.ConditionFalse))
			Expect(condition.Reason).To(Equal(string(v1alpha1.InProgress)))
			Expect(condition.Message).To(ContainSubstring("Waiting for ManagedCluster to be rendered"))
		})
	})

	Context("when ManagedCluster manifest exists but resource not yet created", func() {
		BeforeEach(func() {
			provisionedTime := time.Now().Add(-5 * time.Minute)
			clusterInstance = newTestClusterInstance("test-cluster", "test-ns").
				WithReinstallSpec("gen-1", v1alpha1.PreservationModeNone).
				WithReinstallStatus("gen-1", "gen-1").
				WithReinstallCondition(v1alpha1.ReinstallRequestProcessed, metav1.ConditionTrue, v1alpha1.Completed).
				WithProvisionedCondition(metav1.ConditionTrue, provisionedTime).
				WithManagedClusterManifest("test-cluster").
				Build()
			// ManagedCluster manifest exists in status, but ManagedCluster resource not created

			c = fakeclient.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(clusterInstance).
				WithStatusSubresource(&v1alpha1.ClusterInstance{}).
				Build()

			handler = &ReinstallHandler{
				ConfigStore: configStore,
				Client:      c,
				Logger:      testLogger,
			}
		})

		It("should set condition to InProgress waiting for resource creation", func() {
			result, reimportNeeded, err := handler.EnsureClusterIsReimported(ctx, testLogger, clusterInstance)

			Expect(err).NotTo(HaveOccurred())
			Expect(reimportNeeded).To(BeTrue())
			Expect(result.Requeue).To(BeFalse())

			updatedCI := &v1alpha1.ClusterInstance{}
			err = c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), updatedCI)
			Expect(err).NotTo(HaveOccurred())

			condition := findReinstallStatusCondition(updatedCI, v1alpha1.ReinstallClusterReimported)
			Expect(condition).NotTo(BeNil())
			Expect(condition.Status).To(Equal(metav1.ConditionFalse))
			Expect(condition.Reason).To(Equal(string(v1alpha1.InProgress)))
			Expect(condition.Message).To(ContainSubstring("Waiting for ManagedCluster resource to be created"))
		})
	})
})

func createTestManifestWork(namespace, name string, manifests []map[string]interface{}) *unstructured.Unstructured {
	mw := &unstructured.Unstructured{}
	mw.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "work.open-cluster-management.io",
		Version: "v1",
		Kind:    "ManifestWork",
	})
	mw.SetNamespace(namespace)
	mw.SetName(name)

	if manifests != nil {
		manifestList := make([]interface{}, len(manifests))
		for i, m := range manifests {
			manifestList[i] = m
		}
		_ = unstructured.SetNestedSlice(mw.Object, manifestList, "spec", "workload", "manifests")
	}

	return mw
}

func createTestConfigMapManifest(name, namespace string) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
		},
		"data": map[string]interface{}{
			"key": "value",
		},
	}
}

func createTestNamespaceManifest(name string) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata": map[string]interface{}{
			"name": name,
		},
	}
}

var _ = Describe("applyManifestWorkToSpoke", func() {
	var (
		ctx        context.Context
		testLogger *zap.Logger
	)

	BeforeEach(func() {
		ctx = context.Background()
		testLogger = zap.NewNop()
	})

	It("should return error when ManifestWork is not found", func() {
		hubClient := fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			Build()
		spokeClient := fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			Build()

		err := applyManifestWorkToSpoke(ctx, hubClient, testLogger, "test-cluster", "nonexistent-mw", spokeClient)

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to get ManifestWork"))
	})

	It("should return error when ManifestWork has no manifests field", func() {
		mw := &unstructured.Unstructured{}
		mw.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "work.open-cluster-management.io",
			Version: "v1",
			Kind:    "ManifestWork",
		})
		mw.SetNamespace("test-cluster")
		mw.SetName("test-mw")

		hubClient := fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(mw).
			Build()
		spokeClient := fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			Build()

		err := applyManifestWorkToSpoke(ctx, hubClient, testLogger, "test-cluster", "test-mw", spokeClient)

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("does not contain spec.workload.manifests"))
	})

	It("should succeed when ManifestWork has empty manifests list", func() {
		mw := createTestManifestWork("test-cluster", "test-mw", []map[string]interface{}{})
		hubClient := fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(mw).
			Build()
		spokeClient := fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			Build()

		err := applyManifestWorkToSpoke(ctx, hubClient, testLogger, "test-cluster", "test-mw", spokeClient)

		Expect(err).NotTo(HaveOccurred())
	})

	It("should apply all valid manifests to spoke cluster with correct patch options", func() {
		manifests := []map[string]interface{}{
			createTestNamespaceManifest("open-cluster-management-agent"),
			createTestConfigMapManifest("klusterlet-config", "open-cluster-management-agent"),
		}
		mw := createTestManifestWork("test-cluster", "test-mw", manifests)
		hubClient := fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(mw).
			Build()

		mockCtrl := gomock.NewController(GinkgoT())
		defer mockCtrl.Finish()
		mockSpokeClient := mocks.NewGeneratedMockClient(mockCtrl)

		// Track the objects and options passed to Patch
		appliedObjects := []client.Object{}
		mockSpokeClient.EXPECT().
			Patch(ctx, gomock.Any(), client.Apply, gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, obj client.Object, _ client.Patch, opts ...client.PatchOption) error {
				appliedObjects = append(appliedObjects, obj)

				// Verify patch options are present (ForceOwnership and FieldOwner)
				// We expect exactly 2 options: ForceOwnership and FieldOwner
				Expect(opts).To(HaveLen(2), "Expected ForceOwnership and FieldOwner options")

				// Verify FieldOwner is set correctly
				hasFieldOwner := false
				for _, opt := range opts {
					if fo, ok := opt.(client.FieldOwner); ok {
						hasFieldOwner = true
						Expect(string(fo)).To(Equal("siteconfig-controller"))
					}
				}
				Expect(hasFieldOwner).To(BeTrue(), "FieldOwner option should be present")

				// Verify ManagedFields are cleared for server-side apply
				u := obj.(*unstructured.Unstructured)
				Expect(u.GetManagedFields()).To(BeNil())

				return nil
			}).Times(2)

		err := applyManifestWorkToSpoke(ctx, hubClient, testLogger, "test-cluster", "test-mw", mockSpokeClient)

		Expect(err).NotTo(HaveOccurred())
		Expect(appliedObjects).To(HaveLen(2))

		kinds := []string{}
		for _, obj := range appliedObjects {
			u := obj.(*unstructured.Unstructured)
			kinds = append(kinds, u.GetKind())
		}
		Expect(kinds).To(ContainElements("Namespace", "ConfigMap"))
	})

	It("should skip invalid manifest format and continue", func() {
		mw := &unstructured.Unstructured{}
		mw.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "work.open-cluster-management.io",
			Version: "v1",
			Kind:    "ManifestWork",
		})
		mw.SetNamespace("test-cluster")
		mw.SetName("test-mw")
		_ = unstructured.SetNestedSlice(mw.Object, []interface{}{"invalid-string"}, "spec", "workload", "manifests")

		hubClient := fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(mw).
			Build()
		spokeClient := fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			Build()

		err := applyManifestWorkToSpoke(ctx, hubClient, testLogger, "test-cluster", "test-mw", spokeClient)

		Expect(err).NotTo(HaveOccurred())
	})

	It("should return error when manifests field is not a list", func() {
		mw := &unstructured.Unstructured{}
		mw.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "work.open-cluster-management.io",
			Version: "v1",
			Kind:    "ManifestWork",
		})
		mw.SetNamespace("test-cluster")
		mw.SetName("test-mw")
		_ = unstructured.SetNestedField(mw.Object, "not-a-list", "spec", "workload", "manifests")

		hubClient := fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(mw).
			Build()
		spokeClient := fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			Build()

		err := applyManifestWorkToSpoke(ctx, hubClient, testLogger, "test-cluster", "test-mw", spokeClient)

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("is not a list"))
	})
})

var _ = Describe("applyToSpoke", func() {
	var (
		ctx context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("should call Patch with server-side apply options and clear managed fields", func() {
		mockCtrl := gomock.NewController(GinkgoT())
		defer mockCtrl.Finish()
		mockClient := mocks.NewGeneratedMockClient(mockCtrl)

		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "",
			Version: "v1",
			Kind:    "ConfigMap",
		})
		obj.SetName("test-cm")
		obj.SetNamespace("test-ns")
		obj.SetManagedFields([]metav1.ManagedFieldsEntry{
			{Manager: "old-manager"},
		})

		mockClient.EXPECT().
			Patch(ctx, gomock.Any(), client.Apply, gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, obj client.Object, _ client.Patch, _ ...client.PatchOption) error {
				u := obj.(*unstructured.Unstructured)
				Expect(u.GetManagedFields()).To(BeNil())
				return nil
			})

		err := applyToSpoke(ctx, mockClient, obj)

		Expect(err).NotTo(HaveOccurred())
	})

	It("should return error when Patch fails", func() {
		mockCtrl := gomock.NewController(GinkgoT())
		defer mockCtrl.Finish()
		mockClient := mocks.NewGeneratedMockClient(mockCtrl)

		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "",
			Version: "v1",
			Kind:    "ConfigMap",
		})
		obj.SetName("test-cm")
		obj.SetNamespace("test-ns")

		expectedErr := errors.New("patch failed")
		mockClient.EXPECT().
			Patch(ctx, gomock.Any(), client.Apply, gomock.Any(), gomock.Any()).
			Return(expectedErr)

		err := applyToSpoke(ctx, mockClient, obj)

		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, expectedErr)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring("failed to apply object test-ns/test-cm"))
	})

	It("should apply cluster-scoped resource successfully", func() {
		mockCtrl := gomock.NewController(GinkgoT())
		defer mockCtrl.Finish()
		mockClient := mocks.NewGeneratedMockClient(mockCtrl)

		obj := &unstructured.Unstructured{}
		obj.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "",
			Version: "v1",
			Kind:    "Namespace",
		})
		obj.SetName("test-namespace")

		mockClient.EXPECT().
			Patch(ctx, gomock.Any(), client.Apply, gomock.Any(), gomock.Any()).
			Return(nil)

		err := applyToSpoke(ctx, mockClient, obj)

		Expect(err).NotTo(HaveOccurred())
	})
})

var _ = Describe("performReimport", func() {
	var (
		ctx            context.Context
		c              client.Client
		handler        *ReinstallHandler
		managedCluster *clusterv1.ManagedCluster
		testLogger     *zap.Logger
	)

	BeforeEach(func() {
		ctx = context.Background()
		testLogger = zap.NewNop()
		managedCluster = &clusterv1.ManagedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-cluster",
			},
		}
	})

	Context("when kubeconfig extraction fails", func() {
		It("should fail when admin kubeconfig secret is missing", func() {
			c = fakeclient.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(managedCluster).
				Build()

			handler = &ReinstallHandler{
				Client:             c,
				SpokeClientFactory: &DefaultSpokeClientFactory{},
			}

			err := handler.performReimport(ctx, testLogger, managedCluster)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to get kubeconfig"))
			Expect(err.Error()).To(ContainSubstring("failed to get admin kubeconfig secret"))
		})

		It("should fail when admin kubeconfig secret has invalid type", func() {
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster-admin-kubeconfig",
					Namespace: "test-cluster",
				},
				Type: corev1.SecretTypeTLS,
				Data: map[string][]byte{
					"kubeconfig": []byte("data"),
				},
			}

			c = fakeclient.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(managedCluster, secret).
				Build()

			handler = &ReinstallHandler{
				Client:             c,
				SpokeClientFactory: &DefaultSpokeClientFactory{},
			}

			err := handler.performReimport(ctx, testLogger, managedCluster)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to get kubeconfig"))
			Expect(err.Error()).To(ContainSubstring("invalid secret type"))
		})

		It("should fail when kubeconfig has no data", func() {
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster-admin-kubeconfig",
					Namespace: "test-cluster",
				},
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{
					"wrong-key": []byte("data"),
				},
			}

			c = fakeclient.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(managedCluster, secret).
				Build()

			handler = &ReinstallHandler{
				Client:             c,
				SpokeClientFactory: &DefaultSpokeClientFactory{},
			}

			err := handler.performReimport(ctx, testLogger, managedCluster)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to get kubeconfig"))
			Expect(err.Error()).To(ContainSubstring("missing kubeconfig data"))
		})

		It("should handle empty cluster name", func() {
			emptyNameCluster := &clusterv1.ManagedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name: "",
				},
			}

			c = fakeclient.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(emptyNameCluster).
				Build()

			handler = &ReinstallHandler{
				Client:             c,
				SpokeClientFactory: &DefaultSpokeClientFactory{},
			}

			err := handler.performReimport(ctx, testLogger, emptyNameCluster)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to get kubeconfig"))
		})
	})

	Context("when spoke client factory fails", func() {
		It("should propagate factory errors", func() {
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster-admin-kubeconfig",
					Namespace: "test-cluster",
				},
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{
					"kubeconfig": []byte("valid-kubeconfig-data"),
				},
			}

			c = fakeclient.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(managedCluster, secret).
				Build()

			mockCtrl := gomock.NewController(GinkgoT())
			defer mockCtrl.Finish()

			mockFactory := mocks.NewMockSpokeClientFactory(mockCtrl)
			mockFactory.EXPECT().
				CreateClient(ctx, []byte("valid-kubeconfig-data")).
				Return(nil, errors.New("factory error: connection refused"))

			handler = &ReinstallHandler{
				Client:             c,
				SpokeClientFactory: mockFactory,
			}

			err := handler.performReimport(ctx, testLogger, managedCluster)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to create spoke client"))
			Expect(err.Error()).To(ContainSubstring("factory error: connection refused"))
		})
	})

	Context("when spoke client is successfully created", func() {
		var (
			mockCtrl        *gomock.Controller
			mockFactory     *mocks.MockSpokeClientFactory
			mockSpokeClient *mocks.GeneratedMockClient
			adminSecret     *corev1.Secret
		)

		BeforeEach(func() {
			adminSecret = &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster-admin-kubeconfig",
					Namespace: "test-cluster",
				},
				Type: corev1.SecretTypeOpaque,
				Data: map[string][]byte{
					"kubeconfig": []byte("valid-kubeconfig-data"),
				},
			}

			mockCtrl = gomock.NewController(GinkgoT())
			mockFactory = mocks.NewMockSpokeClientFactory(mockCtrl)
			mockSpokeClient = mocks.NewGeneratedMockClient(mockCtrl)
		})

		AfterEach(func() {
			mockCtrl.Finish()
		})

		It("should fail when klusterlet CRDs ManifestWork is not found", func() {
			c = fakeclient.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(managedCluster, adminSecret).
				Build()

			mockFactory.EXPECT().
				CreateClient(ctx, []byte("valid-kubeconfig-data")).
				Return(mockSpokeClient, nil)

			handler = &ReinstallHandler{
				Client:             c,
				SpokeClientFactory: mockFactory,
			}

			err := handler.performReimport(ctx, testLogger, managedCluster)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to apply klusterlet CRDs"))
			Expect(err.Error()).To(ContainSubstring("test-cluster-klusterlet-crds"))
		})

		It("should fail when klusterlet ManifestWork is not found after CRDs applied", func() {
			crdsManifestWork := createTestManifestWork("test-cluster", "test-cluster-klusterlet-crds",
				[]map[string]interface{}{createTestNamespaceManifest("open-cluster-management-agent")})

			c = fakeclient.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(managedCluster, adminSecret, crdsManifestWork).
				Build()

			mockFactory.EXPECT().
				CreateClient(ctx, []byte("valid-kubeconfig-data")).
				Return(mockSpokeClient, nil)

			// Expect CRDs to be applied successfully
			mockSpokeClient.EXPECT().
				Patch(ctx, gomock.Any(), client.Apply, gomock.Any(), gomock.Any()).
				Return(nil).Times(1)

			handler = &ReinstallHandler{
				Client:             c,
				SpokeClientFactory: mockFactory,
			}

			err := handler.performReimport(ctx, testLogger, managedCluster)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to apply klusterlet resources"))
			Expect(err.Error()).To(ContainSubstring("test-cluster-klusterlet"))
		})

		It("should complete full reimport flow successfully", func() {
			crdsManifestWork := createTestManifestWork("test-cluster", "test-cluster-klusterlet-crds",
				[]map[string]interface{}{createTestNamespaceManifest("open-cluster-management-agent")})
			klusterletManifestWork := createTestManifestWork("test-cluster", "test-cluster-klusterlet",
				[]map[string]interface{}{createTestConfigMapManifest("klusterlet-config", "open-cluster-management-agent")})

			c = fakeclient.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(managedCluster, adminSecret, crdsManifestWork, klusterletManifestWork).
				WithStatusSubresource(&clusterv1.ManagedCluster{}).
				Build()

			mockFactory.EXPECT().
				CreateClient(ctx, []byte("valid-kubeconfig-data")).
				Return(mockSpokeClient, nil)

			// Expect CRDs and klusterlet manifests to be applied
			mockSpokeClient.EXPECT().
				Patch(ctx, gomock.Any(), client.Apply, gomock.Any(), gomock.Any()).
				Return(nil).Times(2) // 1 CRD manifest + 1 klusterlet manifest

			handler = &ReinstallHandler{
				Client:             c,
				SpokeClientFactory: mockFactory,
			}

			err := handler.performReimport(ctx, testLogger, managedCluster)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})

var _ = Describe("getManagedClusterResource", func() {
	var (
		ctx             context.Context
		c               client.Client
		clusterInstance *v1alpha1.ClusterInstance
	)

	BeforeEach(func() {
		ctx = context.Background()
		clusterInstance = &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "test-ns",
			},
			Status: v1alpha1.ClusterInstanceStatus{
				ManifestsRendered: []v1alpha1.ManifestReference{},
			},
		}
	})

	It("should return ManagedCluster when it exists", func() {
		apiGroup := ptr.To("cluster.open-cluster-management.io/v1")
		clusterInstance.Status.ManifestsRendered = []v1alpha1.ManifestReference{
			{
				APIGroup: apiGroup,
				Kind:     "ManagedCluster",
				Name:     "test-cluster",
				Status:   v1alpha1.ManifestRenderedSuccess,
			},
		}

		mc := &clusterv1.ManagedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test-cluster",
			},
		}

		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(mc).
			Build()

		result, err := getManagedClusterResource(ctx, c, clusterInstance)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).NotTo(BeNil())
		Expect(result.Name).To(Equal("test-cluster"))
	})

	It("should return error when ManagedCluster not found in rendered manifests", func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			Build()

		result, err := getManagedClusterResource(ctx, c, clusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(cierrors.IsManagedClusterNotInManifestError(err)).To(BeTrue())
		Expect(result).To(BeNil())
	})

	It("should return error when ManagedCluster not found in cluster", func() {
		apiGroup := ptr.To("cluster.open-cluster-management.io/v1")
		clusterInstance.Status.ManifestsRendered = []v1alpha1.ManifestReference{
			{
				APIGroup: apiGroup,
				Kind:     "ManagedCluster",
				Name:     "test-cluster",
				Status:   v1alpha1.ManifestRenderedSuccess,
			},
		}

		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			Build()

		result, err := getManagedClusterResource(ctx, c, clusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to get ManagedCluster"))
		Expect(result).To(BeNil())
	})
})

var _ = Describe("assessClusterHealth", func() {
	It("should return healthy status when all conditions are True", func() {
		mc := &clusterv1.ManagedCluster{
			Status: clusterv1.ManagedClusterStatus{
				Conditions: []metav1.Condition{
					{
						Type:   clusterv1.ManagedClusterConditionAvailable,
						Status: metav1.ConditionTrue,
						Reason: "ManagedClusterAvailable",
					},
					{
						Type:   "ManagedClusterImportSucceeded",
						Status: metav1.ConditionTrue,
						Reason: "ManagedClusterImported",
					},
					{
						Type:   clusterv1.ManagedClusterConditionJoined,
						Status: metav1.ConditionTrue,
						Reason: "ManagedClusterJoined",
					},
				},
			},
		}

		health := assessClusterHealth(mc)
		Expect(health.isHealthy).To(BeTrue())
		Expect(health.availableStatus).To(Equal(metav1.ConditionTrue))
		Expect(health.availableReason).To(Equal("ManagedClusterAvailable"))
		Expect(health.importStatus).To(Equal(metav1.ConditionTrue))
		Expect(health.importReason).To(Equal("ManagedClusterImported"))
		Expect(health.joinedStatus).To(Equal(metav1.ConditionTrue))
	})

	It("should return unhealthy when Available condition is Unknown", func() {
		mc := &clusterv1.ManagedCluster{
			Status: clusterv1.ManagedClusterStatus{
				Conditions: []metav1.Condition{
					{
						Type:    clusterv1.ManagedClusterConditionAvailable,
						Status:  metav1.ConditionUnknown,
						Reason:  "ManagedClusterLeaseUpdateStopped",
						Message: "Cluster agent stopped updating its lease.",
					},
					{
						Type:   "ManagedClusterImportSucceeded",
						Status: metav1.ConditionTrue,
						Reason: "ManagedClusterImported",
					},
					{
						Type:   clusterv1.ManagedClusterConditionJoined,
						Status: metav1.ConditionTrue,
						Reason: "ManagedClusterJoined",
					},
				},
			},
		}

		health := assessClusterHealth(mc)
		Expect(health.isHealthy).To(BeFalse())
		Expect(health.availableStatus).To(Equal(metav1.ConditionUnknown))
		Expect(health.availableReason).To(Equal("ManagedClusterLeaseUpdateStopped"))
		Expect(health.availableMessage).To(ContainSubstring("Cluster agent stopped"))
	})

	It("should return unhealthy when ImportSucceeded is False", func() {
		mc := &clusterv1.ManagedCluster{
			Status: clusterv1.ManagedClusterStatus{
				Conditions: []metav1.Condition{
					{
						Type:   clusterv1.ManagedClusterConditionAvailable,
						Status: metav1.ConditionTrue,
						Reason: "ManagedClusterAvailable",
					},
					{
						Type:    "ManagedClusterImportSucceeded",
						Status:  metav1.ConditionFalse,
						Reason:  "ManagedClusterImportFailed",
						Message: "Import failed due to error",
					},
					{
						Type:   clusterv1.ManagedClusterConditionJoined,
						Status: metav1.ConditionTrue,
						Reason: "ManagedClusterJoined",
					},
				},
			},
		}

		health := assessClusterHealth(mc)
		Expect(health.isHealthy).To(BeFalse())
		Expect(health.importStatus).To(Equal(metav1.ConditionFalse))
		Expect(health.importReason).To(Equal("ManagedClusterImportFailed"))
		Expect(health.importMessage).To(ContainSubstring("Import failed"))
	})

	It("should return unhealthy when Joined is False", func() {
		mc := &clusterv1.ManagedCluster{
			Status: clusterv1.ManagedClusterStatus{
				Conditions: []metav1.Condition{
					{
						Type:   clusterv1.ManagedClusterConditionAvailable,
						Status: metav1.ConditionTrue,
						Reason: "ManagedClusterAvailable",
					},
					{
						Type:   "ManagedClusterImportSucceeded",
						Status: metav1.ConditionTrue,
						Reason: "ManagedClusterImported",
					},
					{
						Type:   clusterv1.ManagedClusterConditionJoined,
						Status: metav1.ConditionFalse,
						Reason: "ManagedClusterNotJoined",
					},
				},
			},
		}

		health := assessClusterHealth(mc)
		Expect(health.isHealthy).To(BeFalse())
		Expect(health.joinedStatus).To(Equal(metav1.ConditionFalse))
	})

	It("should handle missing conditions gracefully", func() {
		mc := &clusterv1.ManagedCluster{
			Status: clusterv1.ManagedClusterStatus{
				Conditions: []metav1.Condition{},
			},
		}

		health := assessClusterHealth(mc)
		Expect(health.isHealthy).To(BeFalse())
		Expect(health.availableStatus).To(BeEmpty())
		Expect(health.importStatus).To(BeEmpty())
		Expect(health.joinedStatus).To(BeEmpty())
	})
})

var _ = Describe("isWithinGracePeriod", func() {
	var clusterInstance *v1alpha1.ClusterInstance

	BeforeEach(func() {
		clusterInstance = &v1alpha1.ClusterInstance{
			Status: v1alpha1.ClusterInstanceStatus{
				Conditions: []metav1.Condition{},
			},
		}
	})

	It("should return true with zero duration when provisioning not complete", func() {
		inGracePeriod, remaining := isWithinGracePeriod(clusterInstance)
		Expect(inGracePeriod).To(BeTrue())
		Expect(remaining).To(Equal(time.Duration(0)))
	})

	It("should return true when within grace period", func() {
		// Provisioned 2 minutes ago (within 5-minute grace period)
		provisionedTime := time.Now().Add(-2 * time.Minute)
		clusterInstance.Status.Conditions = []metav1.Condition{
			{
				Type:               string(v1alpha1.ClusterProvisioned),
				Status:             metav1.ConditionTrue,
				LastTransitionTime: metav1.NewTime(provisionedTime),
			},
		}

		inGracePeriod, remaining := isWithinGracePeriod(clusterInstance)
		Expect(inGracePeriod).To(BeTrue())
		Expect(remaining).To(BeNumerically(">", 0))
		Expect(remaining).To(BeNumerically("<=", PostProvisioningGracePeriod))
	})

	It("should return false when grace period has expired", func() {
		// Provisioned 15 minutes ago (beyond 5-minute grace period)
		provisionedTime := time.Now().Add(-15 * time.Minute)
		clusterInstance.Status.Conditions = []metav1.Condition{
			{
				Type:               string(v1alpha1.ClusterProvisioned),
				Status:             metav1.ConditionTrue,
				LastTransitionTime: metav1.NewTime(provisionedTime),
			},
		}

		inGracePeriod, remaining := isWithinGracePeriod(clusterInstance)
		Expect(inGracePeriod).To(BeFalse())
		Expect(remaining).To(BeNumerically("<", 0))
	})

	It("should return true when Provisioned condition is False", func() {
		clusterInstance.Status.Conditions = []metav1.Condition{
			{
				Type:   string(v1alpha1.ClusterProvisioned),
				Status: metav1.ConditionFalse,
			},
		}

		inGracePeriod, remaining := isWithinGracePeriod(clusterInstance)
		Expect(inGracePeriod).To(BeTrue())
		Expect(remaining).To(Equal(time.Duration(0)))
	})
})

var _ = Describe("isImportInProgress", func() {
	DescribeTable("should evaluate import status correctly",
		func(status metav1.ConditionStatus, reason string, expected bool) {
			mc := &clusterv1.ManagedCluster{
				Status: clusterv1.ManagedClusterStatus{
					Conditions: []metav1.Condition{
						{
							Type:   "ManagedClusterImportSucceeded",
							Status: status,
							Reason: reason,
						},
					},
				},
			}
			Expect(isImportInProgress(mc)).To(Equal(expected))
		},
		Entry("importing", metav1.ConditionFalse, "ManagedClusterImporting", true),
		Entry("waiting for import", metav1.ConditionFalse, "ManagedClusterWaitForImporting", true),
		Entry("import succeeded", metav1.ConditionTrue, "ManagedClusterImported", false),
		Entry("import failed", metav1.ConditionFalse, "ManagedClusterImportFailed", false),
	)

	It("should return false when import condition is missing", func() {
		mc := &clusterv1.ManagedCluster{
			Status: clusterv1.ManagedClusterStatus{
				Conditions: []metav1.Condition{},
			},
		}
		Expect(isImportInProgress(mc)).To(BeFalse())
	})
})

var _ = Describe("extractKubeconfig", func() {
	It("should extract kubeconfig from 'kubeconfig' key", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: "test-ns",
			},
			Data: map[string][]byte{
				"kubeconfig": []byte("test-kubeconfig-data"),
			},
		}

		data, err := extractKubeconfig(secret)
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(Equal([]byte("test-kubeconfig-data")))
	})

	It("should return error when kubeconfig key does not exist", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: "test-ns",
			},
			Data: map[string][]byte{
				"invalid-key": []byte("test-kubeconfig-data"),
			},
		}

		data, err := extractKubeconfig(secret)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("missing kubeconfig data"))
		Expect(err.Error()).To(ContainSubstring("expected key: kubeconfig"))
		Expect(data).To(BeNil())
	})

	It("should return error when kubeconfig data is empty", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: "test-ns",
			},
			Data: map[string][]byte{
				"kubeconfig": []byte(""),
			},
		}

		data, err := extractKubeconfig(secret)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("missing kubeconfig data"))
		Expect(data).To(BeNil())
	})
})

var _ = Describe("validateAdminKubeconfigSecret", func() {
	DescribeTable("should validate secret correctly",
		func(secretName, secretNamespace string, secretType corev1.SecretType, clusterName string, expectError bool, errorSubstring string) {
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: secretNamespace,
				},
				Type: secretType,
			}
			err := validateAdminKubeconfigSecret(secret, clusterName)
			if expectError {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(errorSubstring))
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
		},
		Entry("valid secret",
			"test-cluster-admin-kubeconfig", "test-cluster", corev1.SecretTypeOpaque, "test-cluster",
			false, ""),
		Entry("wrong type (TLS)",
			"test-cluster-admin-kubeconfig", "test-cluster", corev1.SecretTypeTLS, "test-cluster",
			true, "invalid secret type"),
		Entry("wrong type (BasicAuth)",
			"test-cluster-admin-kubeconfig", "test-cluster", corev1.SecretTypeBasicAuth, "test-cluster",
			true, "invalid secret type"),
		Entry("namespace mismatch",
			"test-cluster-admin-kubeconfig", "wrong-namespace", corev1.SecretTypeOpaque, "test-cluster",
			true, "secret namespace mismatch"),
		Entry("cross-namespace access attempt",
			"attacker-cluster-admin-kubeconfig", "attacker-cluster", corev1.SecretTypeOpaque, "victim-cluster",
			true, "secret namespace mismatch"),
		Entry("name mismatch",
			"wrong-name", "test-cluster", corev1.SecretTypeOpaque, "test-cluster",
			true, "secret name mismatch"),
		Entry("incorrect name format (missing -admin-)",
			"test-cluster-kubeconfig", "test-cluster", corev1.SecretTypeOpaque, "test-cluster",
			true, "secret name mismatch"),
		Entry("secret swap attack",
			"other-cluster-admin-kubeconfig", "test-cluster", corev1.SecretTypeOpaque, "test-cluster",
			true, "secret name mismatch"),
		Entry("cluster name with numbers",
			"test-cluster-123-admin-kubeconfig", "test-cluster-123", corev1.SecretTypeOpaque, "test-cluster-123",
			false, ""),
		Entry("empty cluster name",
			"test-admin-kubeconfig", "test", corev1.SecretTypeOpaque, "",
			true, "mismatch"),
	)

	It("should validate type before namespace before name", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "wrong-name",
				Namespace: "wrong-namespace",
			},
			Type: corev1.SecretTypeTLS,
		}
		err := validateAdminKubeconfigSecret(secret, "test-cluster")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("invalid secret type"))
	})
})

var _ = Describe("extractKubeconfigFromSecret", func() {
	var (
		ctx context.Context
		c   client.Client
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("should extract kubeconfig from admin-kubeconfig secret", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-admin-kubeconfig",
				Namespace: "test-cluster",
			},
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				"kubeconfig": []byte("test-kubeconfig-data"),
			},
		}

		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(secret).
			Build()

		data, err := extractKubeconfigFromSecret(ctx, c, "test-cluster")
		Expect(err).NotTo(HaveOccurred())
		Expect(data).To(Equal([]byte("test-kubeconfig-data")))
	})

	It("should return error when secret does not exist", func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			Build()

		data, err := extractKubeconfigFromSecret(ctx, c, "test-cluster")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to get admin kubeconfig secret"))
		Expect(data).To(BeNil())
	})

	It("should return error when secret has no valid kubeconfig key", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-admin-kubeconfig",
				Namespace: "test-cluster",
			},
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				"invalid": []byte("test-data"),
			},
		}

		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(secret).
			Build()

		data, err := extractKubeconfigFromSecret(ctx, c, "test-cluster")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("missing kubeconfig data"))
		Expect(data).To(BeNil())
	})

	It("should reject secret with invalid type during extraction", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-admin-kubeconfig",
				Namespace: "test-cluster",
			},
			Type: corev1.SecretTypeTLS, // Invalid type
			Data: map[string][]byte{
				"kubeconfig": []byte("test-kubeconfig-data"),
			},
		}

		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(secret).
			Build()

		data, err := extractKubeconfigFromSecret(ctx, c, "test-cluster")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("invalid secret type"))
		Expect(data).To(BeNil())
	})

	It("should reject secret with namespace mismatch during extraction", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster-admin-kubeconfig",
				Namespace: "wrong-namespace",
			},
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				"kubeconfig": []byte("test-kubeconfig-data"),
			},
		}

		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(secret).
			Build()

		// This will fail to get the secret because it's looking in "test-cluster" namespace
		data, err := extractKubeconfigFromSecret(ctx, c, "test-cluster")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to get admin kubeconfig secret"))
		Expect(data).To(BeNil())
	})
})

var _ = Describe("validateSpokeConnectivity", func() {
	var (
		ctx context.Context
		c   client.Client
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("should succeed when default namespace exists", func() {
		defaultNs := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "default",
			},
		}

		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithObjects(defaultNs).
			Build()

		err := validateSpokeConnectivity(ctx, c)
		Expect(err).NotTo(HaveOccurred())
	})

	It("should return error when default namespace does not exist", func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			Build()

		err := validateSpokeConnectivity(ctx, c)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("spoke cluster connectivity validation failed"))
	})

	It("should handle context timeout", func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			Build()

		timeoutCtx, cancel := context.WithTimeout(ctx, 1*time.Nanosecond)
		defer cancel()
		time.Sleep(10 * time.Millisecond)

		err := validateSpokeConnectivity(timeoutCtx, c)
		Expect(err).To(HaveOccurred())
	})
})
