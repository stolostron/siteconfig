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

	"go.uber.org/zap"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
	"github.com/stolostron/siteconfig/internal/controller/preservation"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Helper functions to create a test Secrets

func generateClusterInstanceSecret(name, namespace string) *corev1.Secret {
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: corev1.SchemeGroupVersion.String(),
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"username": []byte("admin"), "password": []byte("password")},
	}
}

func createTestSecret(name, namespace string, mode v1alpha1.PreservationMode, isRestored bool) *corev1.Secret {
	createPreservedObjectMeta := func(name string, preservationMode v1alpha1.PreservationMode) metav1.ObjectMeta {
		labels := map[string]string{}
		annotations := map[string]string{}

		labels[ci.OwnedByLabel] = ci.GenerateOwnedByLabelValue(namespace, namespace)

		// Set additional label and annotation for retrieving and identifying backed-up resources.
		preservedDataLabelKey := v1alpha1.Group + "/preserved-data"
		additionalAnnotation := v1alpha1.PreservationLabelKey

		labels[preservedDataLabelKey] = "timestamp"
		annotations[additionalAnnotation] = string(preservationMode)

		if preservationMode == v1alpha1.PreservationModeClusterIdentity {
			annotations[preservation.ClusterIdentityDataAnnotationKey] = ""
		}

		resourceTypeAnnotationKey := v1alpha1.PreservationLabelKey + ".resource-type"
		annotations[resourceTypeAnnotationKey] = "Secret"

		return metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Labels:      labels,
			Annotations: annotations,
		}
	}

	createRestoredObjectMeta := func(name string, preservationMode v1alpha1.PreservationMode) metav1.ObjectMeta {
		labels := map[string]string{}
		preservationKey := v1alpha1.PreservationLabelKey
		preservationValue := ""
		if preservationMode == v1alpha1.PreservationModeClusterIdentity {
			preservationValue = v1alpha1.ClusterIdentityLabelValue
		}
		labels[preservationKey] = preservationValue

		annotations := map[string]string{}
		annotations[preservation.RestoredAtAnnotationKey] = "timestamp"

		return metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Labels:      labels,
			Annotations: annotations,
		}
	}

	createObjectMeta := func(name string, mode v1alpha1.PreservationMode, isRestored bool) metav1.ObjectMeta {
		if isRestored {
			return createRestoredObjectMeta(name, mode)
		}
		return createPreservedObjectMeta(name, mode)
	}

	getRawData := func() map[string][]byte {
		return map[string][]byte{
			"username": []byte("admin"), "password": []byte("password")}
	}

	createData := func(isRestored bool) map[string][]byte {
		if isRestored {
			return getRawData()
		}

		labels := map[string]string{}
		preservationValue := ""
		if mode == v1alpha1.PreservationModeClusterIdentity {
			preservationValue = v1alpha1.ClusterIdentityLabelValue
		}
		labels[v1alpha1.PreservationLabelKey] = preservationValue

		originalResource := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				Labels:    labels,
			},
			Type: corev1.SecretTypeOpaque,
			Data: getRawData(),
		}
		data, err := yaml.Marshal(originalResource)
		Expect(err).ToNot(HaveOccurred())

		return map[string][]byte{
			"original-resource": []byte(data),
		}
	}

	return &corev1.Secret{
		ObjectMeta: createObjectMeta(name, mode, isRestored),
		Type:       corev1.SecretTypeOpaque,
		Data:       createData(isRestored),
	}
}

var _ = Describe("findReinstallStatusCondition", func() {
	var clusterInstance *v1alpha1.ClusterInstance

	BeforeEach(func() {
		clusterInstance = &v1alpha1.ClusterInstance{
			Status: v1alpha1.ClusterInstanceStatus{
				Reinstall: &v1alpha1.ReinstallStatus{
					Conditions: []metav1.Condition{},
				},
			},
		}
	})

	It("should return nil if Reinstall is nil", func() {
		clusterInstance.Status.Reinstall = nil
		cond := findReinstallStatusCondition(clusterInstance, v1alpha1.ReinstallRequestProcessed)
		Expect(cond).To(BeNil())
	})

	It("should return the correct condition", func() {
		condition := metav1.Condition{Type: string(v1alpha1.ReinstallRequestProcessed)}
		clusterInstance.Status.Reinstall.Conditions = append(clusterInstance.Status.Reinstall.Conditions, condition)
		cond := findReinstallStatusCondition(clusterInstance, v1alpha1.ReinstallRequestProcessed)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Type).To(Equal(string(v1alpha1.ReinstallRequestProcessed)))
	})
})

var _ = Describe("setReinstallStatusCondition", func() {
	var clusterInstance *v1alpha1.ClusterInstance

	BeforeEach(func() {
		clusterInstance = &v1alpha1.ClusterInstance{
			Status: v1alpha1.ClusterInstanceStatus{
				Reinstall: &v1alpha1.ReinstallStatus{
					Conditions: []metav1.Condition{},
				},
			},
		}
	})

	It("should return false if Reinstall is nil", func() {
		clusterInstance.Status.Reinstall = nil
		result := setReinstallStatusCondition(clusterInstance, metav1.Condition{})
		Expect(result).To(BeFalse())
	})

	It("should add a new condition", func() {
		condition := metav1.Condition{Type: string(v1alpha1.ReinstallRequestProcessed)}
		result := setReinstallStatusCondition(clusterInstance, condition)
		Expect(result).To(BeTrue())
		Expect(meta.FindStatusCondition(clusterInstance.Status.Reinstall.Conditions, string(v1alpha1.ReinstallRequestProcessed))).NotTo(BeNil())
	})
})

var _ = Describe("conditionSetter functions", func() {
	It("should return a condition with the correct values", func() {
		condition := conditionSetter(v1alpha1.ReinstallRequestProcessed, metav1.ConditionTrue, v1alpha1.Initialized, "Test Message")
		Expect(condition.Type).To(Equal(string(v1alpha1.ReinstallRequestProcessed)))
		Expect(condition.Status).To(Equal(metav1.ConditionTrue))
		Expect(condition.Reason).To(Equal(string(v1alpha1.Initialized)))
		Expect(condition.Message).To(Equal("Test Message"))
	})
})

var _ = Describe("Reinstall Condition Status Functions", func() {
	DescribeTable("should return the correct condition",
		func(reinstallConditionStatusFn func(metav1.ConditionStatus, v1alpha1.ClusterInstanceConditionReason, string) *metav1.Condition,
			typeExpected v1alpha1.ClusterInstanceConditionType) {

			testStatus := metav1.ConditionTrue
			testReason := v1alpha1.ClusterInstanceConditionReason("TestReason")
			testMessage := "Test Message"
			condition := reinstallConditionStatusFn(testStatus, testReason, testMessage)

			Expect(condition).NotTo(BeNil())
			Expect(condition.Type).To(Equal(string(typeExpected)))
			Expect(condition.Status).To(Equal(testStatus))
			Expect(condition.Reason).To(Equal(string(testReason)))
			Expect(condition.Message).To(Equal(testMessage))
		},
		Entry("ReinstallRequestProcessed", reinstallRequestProcessedConditionStatus, v1alpha1.ReinstallRequestProcessed),
		Entry("ReinstallRequestValidated", reinstallRequestValidatedConditionStatus, v1alpha1.ReinstallRequestValidated),
		Entry("ReinstallPreservationDataBackedup", reinstallPreservationDataBackedupConditionStatus, v1alpha1.ReinstallPreservationDataBackedup),
		Entry("ReinstallClusterIdentityDataDetected", reinstallClusterIdentityDataDetectedConditionStatus, v1alpha1.ReinstallClusterIdentityDataDetected),
		Entry("ReinstallRenderedManifestsDeleted", reinstallRenderedManifestsDeletedConditionStatus, v1alpha1.ReinstallRenderedManifestsDeleted),
		Entry("ReinstallPreservationDataRestored", reinstallPreservationDataRestoredConditionStatus, v1alpha1.ReinstallPreservationDataRestored),
	)
})

var _ = Describe("getManagedCluster", func() {
	var (
		apiGroup        = ptr.To("cluster.open-cluster-management.io/v1")
		clusterInstance *v1alpha1.ClusterInstance
	)

	BeforeEach(func() {
		clusterInstance = &v1alpha1.ClusterInstance{
			Status: v1alpha1.ClusterInstanceStatus{
				ManifestsRendered: []v1alpha1.ManifestReference{},
			},
		}
	})

	It("should return a RenderedObject when a ManagedCluster manifest exists", func() {
		clusterInstance.Status.ManifestsRendered = append(clusterInstance.Status.ManifestsRendered, v1alpha1.ManifestReference{
			APIGroup: apiGroup,
			Kind:     "ManagedCluster",
			Name:     "test-cluster",
			SyncWave: 1,
		})
		managedCluster, err := getManagedCluster(clusterInstance)
		Expect(err).NotTo(HaveOccurred())
		Expect(managedCluster).ToNot(BeNil())
		Expect(managedCluster.GetKind()).To(Equal("ManagedCluster"))
		Expect(managedCluster.GetName()).To(Equal("test-cluster"))
		syncWave, err := managedCluster.GetSyncWave()
		Expect(err).NotTo(HaveOccurred())
		Expect(syncWave).To(Equal(1))
	})

	It("should return nil when no ManagedCluster manifest exists", func() {
		managedCluster, err := getManagedCluster(clusterInstance)
		Expect(err).NotTo(HaveOccurred())
		Expect(managedCluster).To(BeNil())
	})

})

var _ = Describe("updateLabelIfNeeded", func() {
	var objectMeta metav1.ObjectMeta

	BeforeEach(func() {
		objectMeta = metav1.ObjectMeta{
			Labels: map[string]string{},
		}
	})

	It("should add a label if it does not exist", func() {
		changed := setOrUpdateLabel(&objectMeta, "foo", "bar")
		Expect(changed).To(BeTrue())
		Expect(objectMeta.Labels).To(HaveKeyWithValue("foo", "bar"))
	})

	It("should update the label if the value is different", func() {
		objectMeta.Labels["foo"] = "bar"
		changed := setOrUpdateLabel(&objectMeta, "foo", "something")
		Expect(changed).To(BeTrue())
		Expect(objectMeta.Labels).To(HaveKeyWithValue("foo", "something"))
	})

	It("should not update the label if the value is the same", func() {
		objectMeta.Labels["foo"] = "bar"
		changed := setOrUpdateLabel(&objectMeta, "foo", "bar")
		Expect(changed).To(BeFalse())
		Expect(objectMeta.Labels).To(HaveKeyWithValue("foo", "bar"))
	})

	It("should initialize labels if nil and add the new label", func() {
		objectMeta.Labels = nil
		changed := setOrUpdateLabel(&objectMeta, "foo", "bar")
		Expect(changed).To(BeTrue())
		Expect(objectMeta.Labels).To(HaveKeyWithValue("foo", "bar"))
	})
})

var _ = Describe("Data Preservation Tests", func() {
	var (
		ctx        context.Context
		c          client.Client
		testLogger *zap.Logger

		testNamespace = "test"

		clusterInstance *v1alpha1.ClusterInstance

		secret1, secret2, secret3, secret4 *corev1.Secret
	)

	Describe("getDataPreservationSummary", func() {

		BeforeEach(func() {
			ctx = context.Background()
			testLogger = zap.NewNop()

			clusterInstance = &v1alpha1.ClusterInstance{
				Spec: v1alpha1.ClusterInstanceSpec{
					Reinstall: &v1alpha1.ReinstallSpec{
						PreservationMode: v1alpha1.PreservationModeAll,
					},
				},
			}

			// Create preserved secrets
			secret1 = createTestSecret("secret1", testNamespace, v1alpha1.PreservationModeClusterIdentity, false)
			secret2 = createTestSecret("secret2", testNamespace, v1alpha1.PreservationModeClusterIdentity, false)
			secret3 = createTestSecret("secret3", testNamespace, v1alpha1.PreservationModeAll, false)
			secret4 = createTestSecret("secret4", testNamespace, v1alpha1.PreservationModeAll, false)

			c = fakeclient.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(secret1, secret2, secret3, secret4).
				Build()
		})

		It("should return success conditions when resources are preserved", func() {
			preserveCondition, identityCondition, err := getDataPreservationSummary(ctx, c, testLogger, clusterInstance)
			Expect(err).NotTo(HaveOccurred())

			Expect(preserveCondition.Status).To(Equal(metav1.ConditionTrue))
			Expect(preserveCondition.Reason).To(Equal(string(v1alpha1.Completed)))
			Expect(preserveCondition.Message).To(ContainSubstring("Number of resources preserved: 4"))

			Expect(identityCondition.Status).To(Equal(metav1.ConditionTrue))
			Expect(identityCondition.Reason).To(Equal(string(v1alpha1.DataAvailable)))
			Expect(identityCondition.Message).To(ContainSubstring("Number of cluster identity resources detected: 2"))
		})

		It("should return a DataUnavailable condition when no resources are preserved with PreservationMode=All", func() {
			for _, object := range []client.Object{secret1, secret2, secret3, secret4} {
				Expect(c.Delete(ctx, object)).To(Succeed())
			}
			clusterInstance.Spec.Reinstall.PreservationMode = v1alpha1.PreservationModeAll
			preserveCondition, identityCondition, err := getDataPreservationSummary(ctx, c, testLogger, clusterInstance)
			Expect(err).NotTo(HaveOccurred())

			Expect(preserveCondition.Status).To(Equal(metav1.ConditionFalse))
			Expect(preserveCondition.Reason).To(Equal(string(v1alpha1.DataUnavailable)))
			Expect(preserveCondition.Message).To(ContainSubstring("No resources were found to be preserved"))

			Expect(identityCondition.Status).To(Equal(metav1.ConditionFalse))
			Expect(identityCondition.Reason).To(Equal(string(v1alpha1.DataUnavailable)))
			Expect(identityCondition.Message).To(Equal("No cluster identity resources were detected for preservation"))
		})

		It("should return a Failed condition when no cluster identity resources are found with PreservationMode=ClusterIdentity", func() {
			clusterInstance.Spec.Reinstall.PreservationMode = v1alpha1.PreservationModeClusterIdentity
			for _, object := range []client.Object{secret1, secret2, secret3, secret4} {
				Expect(c.Delete(ctx, object)).To(Succeed())
			}

			preserveCondition, identityCondition, err := getDataPreservationSummary(ctx, c, testLogger, clusterInstance)
			Expect(err).To(HaveOccurred())

			Expect(preserveCondition.Status).To(Equal(metav1.ConditionFalse))
			Expect(preserveCondition.Reason).To(Equal(string(v1alpha1.DataUnavailable)))
			Expect(preserveCondition.Message).To(ContainSubstring("No resources were found"))

			Expect(identityCondition.Status).To(Equal(metav1.ConditionFalse))
			Expect(identityCondition.Reason).To(Equal(string(v1alpha1.Failed)))
			Expect(identityCondition.Message).To(ContainSubstring("preservationMode set to 'ClusterIdentity', found no cluster-identity resources"))
		})

		It("should return a Failed condition when GetPreservedResourceCounts returns an error", func() {
			newFakeMode := v1alpha1.PreservationMode("fakeMode")
			clusterInstance.Spec.Reinstall.PreservationMode = newFakeMode

			preserveCondition, identityCondition, err := getDataPreservationSummary(ctx, c, testLogger, clusterInstance)
			Expect(err).To(HaveOccurred())

			Expect(preserveCondition.Status).To(Equal(metav1.ConditionFalse))
			Expect(preserveCondition.Reason).To(Equal(string(v1alpha1.Failed)))
			Expect(preserveCondition.Message).To(ContainSubstring("unknown PreservationMode"))

			Expect(identityCondition.Status).To(Equal(metav1.ConditionFalse))
			Expect(identityCondition.Reason).To(Equal(string(v1alpha1.Failed)))
			Expect(identityCondition.Message).To(ContainSubstring("unknown PreservationMode"))
		})

	})

	Describe("getDataRestorationSummary", func() {

		BeforeEach(func() {
			ctx = context.Background()
			testLogger = zap.NewNop()

			clusterInstance = &v1alpha1.ClusterInstance{
				Spec: v1alpha1.ClusterInstanceSpec{
					Reinstall: &v1alpha1.ReinstallSpec{
						PreservationMode: v1alpha1.PreservationModeAll,
					},
				},
			}

			// Create preserved secrets
			secret1 = createTestSecret("secret1", testNamespace, v1alpha1.PreservationModeClusterIdentity, true)
			secret2 = createTestSecret("secret2", testNamespace, v1alpha1.PreservationModeClusterIdentity, true)
			secret3 = createTestSecret("secret3", testNamespace, v1alpha1.PreservationModeAll, true)
			secret4 = createTestSecret("secret4", testNamespace, v1alpha1.PreservationModeAll, true)

			c = fakeclient.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(secret1, secret2, secret3, secret4).
				Build()
		})

		It("should return success condition when resources are restored", func() {
			condition, err := getDataRestorationSummary(ctx, c, testLogger, clusterInstance)
			Expect(err).NotTo(HaveOccurred())
			Expect(condition.Status).To(Equal(metav1.ConditionTrue))
			Expect(condition.Reason).To(Equal(string(v1alpha1.Completed)))
			Expect(condition.Message).To(ContainSubstring("4 resources were successfully restored"))
		})

		It("should return a DataUnavailable condition when no resources are restored", func() {
			for _, object := range []client.Object{secret1, secret2, secret3, secret4} {
				Expect(c.Delete(ctx, object)).To(Succeed())
			}

			condition, err := getDataRestorationSummary(ctx, c, testLogger, clusterInstance)
			Expect(err).NotTo(HaveOccurred())
			Expect(condition.Status).To(Equal(metav1.ConditionFalse))
			Expect(condition.Reason).To(Equal(string(v1alpha1.DataUnavailable)))
			Expect(condition.Message).To(Equal("No restored resources found"))
		})

		It("should return a Failed condition when no resources are restored and PreservationMode is ClusterIdentity", func() {
			clusterInstance.Spec.Reinstall.PreservationMode = v1alpha1.PreservationModeClusterIdentity

			for _, object := range []client.Object{secret1, secret2, secret3, secret4} {
				Expect(c.Delete(ctx, object)).To(Succeed())
			}

			condition, err := getDataRestorationSummary(ctx, c, testLogger, clusterInstance)
			Expect(err).To(HaveOccurred())
			Expect(condition.Status).To(Equal(metav1.ConditionFalse))
			Expect(condition.Reason).To(Equal(string(v1alpha1.Failed)))
			Expect(condition.Message).To(ContainSubstring("no restored resources found"))
		})

		It("should return a Failed condition when GetRestoredResourceCounts returns an error", func() {
			newFakeMode := v1alpha1.PreservationMode("fakeMode")
			clusterInstance.Spec.Reinstall.PreservationMode = newFakeMode

			condition, err := getDataRestorationSummary(ctx, c, testLogger, clusterInstance)
			Expect(err).To(HaveOccurred())
			Expect(condition.Status).To(Equal(metav1.ConditionFalse))
			Expect(condition.Reason).To(Equal(string(v1alpha1.Failed)))
			Expect(condition.Message).To(ContainSubstring("unknown PreservationMode"))
		})

	})

})
