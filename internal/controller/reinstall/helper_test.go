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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/stolostron/siteconfig/api/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

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
