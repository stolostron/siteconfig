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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stolostron/siteconfig/api/v1alpha1"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	clusterv1 "open-cluster-management.io/api/cluster/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// createHostedManagedCluster creates a ManagedCluster with hosted cluster labels and annotations
func createHostedManagedCluster(name string, availableStatus metav1.ConditionStatus, reason, message string) *clusterv1.ManagedCluster {
	mc := &clusterv1.ManagedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				"ishosted": "true",
			},
			Annotations: map[string]string{
				"open-cluster-management/created-via": "hypershift",
			},
		},
		Status: clusterv1.ManagedClusterStatus{},
	}

	if availableStatus != "" {
		mc.Status.Conditions = []metav1.Condition{
			{
				Type:               clusterv1.ManagedClusterConditionAvailable,
				Status:             availableStatus,
				Reason:             reason,
				Message:            message,
				LastTransitionTime: metav1.Now(),
			},
		}
	}

	return mc
}

var _ = Describe("ManagedClusterReconciler", func() {
	var (
		c                client.Client
		r                *ManagedClusterReconciler
		ctx              = context.Background()
		testLogger       = zap.NewNop().Named("Test")
		clusterName      = "test-hosted-cluster"
		clusterNamespace = "test-namespace"
		clusterInstance  *v1alpha1.ClusterInstance
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			Build()
		r = &ManagedClusterReconciler{
			Client: c,
			Scheme: scheme.Scheme,
			Log:    testLogger,
		}

		clusterInstance = &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: clusterNamespace,
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				ClusterName:            clusterName,
				ClusterType:            v1alpha1.ClusterTypeHostedControlPlane,
				PullSecretRef:          corev1.LocalObjectReference{Name: "pull-secret"},
				ClusterImageSetNameRef: "testimage:foobar",
				SSHPublicKey:           "test-ssh",
				BaseDomain:             "abcd",
				TemplateRefs: []v1alpha1.TemplateRef{
					{Name: "test-cluster-template", Namespace: "default"},
				},
			},
		}

		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: clusterNamespace,
			},
		}
		Expect(c.Create(ctx, ns)).To(Succeed())
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())
	})

	It("doesn't error for a missing ManagedCluster", func() {
		key := types.NamespacedName{
			Name: clusterName,
		}

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(doNotRequeue()))
	})

	It("doesn't reconcile when no matching ClusterInstance found", func() {
		key := types.NamespacedName{
			Name: "non-existent-cluster",
		}

		managedCluster := createHostedManagedCluster("non-existent-cluster", metav1.ConditionTrue, "ManagedClusterAvailable", "Cluster is available")
		Expect(c.Create(ctx, managedCluster)).To(Succeed())

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(doNotRequeue()))
	})

	It("initializes ManagedClusterRef correctly", func() {
		key := types.NamespacedName{
			Name: clusterName,
		}

		managedCluster := createHostedManagedCluster(clusterName, metav1.ConditionTrue, "ManagedClusterAvailable", "Cluster is available")
		Expect(c.Create(ctx, managedCluster)).To(Succeed())

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(doNotRequeue()))

		// Fetch ClusterInstance and verify ManagedClusterRef is set
		ci := &v1alpha1.ClusterInstance{}
		Expect(c.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: clusterNamespace}, ci)).To(Succeed())
		Expect(ci.Status.ManagedClusterRef).ToNot(BeNil())
		Expect(ci.Status.ManagedClusterRef.Name).To(Equal(clusterName))
	})

	It("initializes Provisioned condition with Unknown status", func() {
		key := types.NamespacedName{
			Name: clusterName,
		}

		// Create ManagedCluster with no conditions
		managedCluster := createHostedManagedCluster(clusterName, "", "", "")
		Expect(c.Create(ctx, managedCluster)).To(Succeed())

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(doNotRequeue()))

		// Fetch ClusterInstance and verify Provisioned condition is initialized
		ci := &v1alpha1.ClusterInstance{}
		Expect(c.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: clusterNamespace}, ci)).To(Succeed())

		expectedCondition := &metav1.Condition{
			Type:   string(v1alpha1.ClusterProvisioned),
			Status: metav1.ConditionUnknown,
			Reason: string(v1alpha1.Unknown),
		}
		provisionedCondition := findCondition(ci.Status.Conditions, string(v1alpha1.ClusterProvisioned))
		compareToExpectedCondition(provisionedCondition, expectedCondition)
	})

	It("sets Provisioned to Completed when Available is True", func() {
		key := types.NamespacedName{
			Name: clusterName,
		}

		managedCluster := createHostedManagedCluster(clusterName, metav1.ConditionTrue, "ManagedClusterAvailable", "Cluster is available")
		Expect(c.Create(ctx, managedCluster)).To(Succeed())

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(doNotRequeue()))

		// Fetch ClusterInstance and verify Provisioned condition is Completed
		ci := &v1alpha1.ClusterInstance{}
		Expect(c.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: clusterNamespace}, ci)).To(Succeed())

		expectedCondition := &metav1.Condition{
			Type:   string(v1alpha1.ClusterProvisioned),
			Status: metav1.ConditionTrue,
			Reason: string(v1alpha1.Completed),
		}
		provisionedCondition := findCondition(ci.Status.Conditions, string(v1alpha1.ClusterProvisioned))
		compareToExpectedCondition(provisionedCondition, expectedCondition)
	})

	It("sets Provisioned to InProgress when Available is False", func() {
		key := types.NamespacedName{
			Name: clusterName,
		}

		managedCluster := createHostedManagedCluster(clusterName, metav1.ConditionFalse, "ManagedClusterNotAvailable", "Cluster is not available")
		Expect(c.Create(ctx, managedCluster)).To(Succeed())

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(doNotRequeue()))

		// Fetch ClusterInstance and verify Provisioned condition is InProgress
		ci := &v1alpha1.ClusterInstance{}
		Expect(c.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: clusterNamespace}, ci)).To(Succeed())

		expectedCondition := &metav1.Condition{
			Type:   string(v1alpha1.ClusterProvisioned),
			Status: metav1.ConditionFalse,
			Reason: string(v1alpha1.InProgress),
		}
		provisionedCondition := findCondition(ci.Status.Conditions, string(v1alpha1.ClusterProvisioned))
		compareToExpectedCondition(provisionedCondition, expectedCondition)
	})

	It("sets Provisioned to Unknown when Available is Unknown", func() {
		key := types.NamespacedName{
			Name: clusterName,
		}

		managedCluster := createHostedManagedCluster(clusterName, metav1.ConditionUnknown, "ManagedClusterStatusUnknown", "Cluster status is unknown")
		Expect(c.Create(ctx, managedCluster)).To(Succeed())

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(doNotRequeue()))

		// Fetch ClusterInstance and verify Provisioned condition is Unknown
		ci := &v1alpha1.ClusterInstance{}
		Expect(c.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: clusterNamespace}, ci)).To(Succeed())

		expectedCondition := &metav1.Condition{
			Type:   string(v1alpha1.ClusterProvisioned),
			Status: metav1.ConditionUnknown,
			Reason: string(v1alpha1.Unknown),
		}
		provisionedCondition := findCondition(ci.Status.Conditions, string(v1alpha1.ClusterProvisioned))
		compareToExpectedCondition(provisionedCondition, expectedCondition)
	})

	It("does not update status when conditions are unchanged", func() {
		key := types.NamespacedName{
			Name: clusterName,
		}

		managedCluster := createHostedManagedCluster(clusterName, metav1.ConditionTrue, "ManagedClusterAvailable", "Cluster is available")
		Expect(c.Create(ctx, managedCluster)).To(Succeed())

		// First reconcile
		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(doNotRequeue()))

		// Get ClusterInstance and save ResourceVersion
		ci := &v1alpha1.ClusterInstance{}
		Expect(c.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: clusterNamespace}, ci)).To(Succeed())
		resourceVersionAfterFirst := ci.GetResourceVersion()

		// Second reconcile with no changes
		res, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(doNotRequeue()))

		// Verify ResourceVersion is unchanged (no status update)
		Expect(c.Get(ctx, types.NamespacedName{Name: clusterName, Namespace: clusterNamespace}, ci)).To(Succeed())
		Expect(ci.GetResourceVersion()).To(Equal(resourceVersionAfterFirst))
	})

	It("maps ClusterInstance to ManagedCluster correctly", func() {
		// Create a ClusterInstance with ManagedClusterRef already set
		ci := &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "mapped-cluster",
				Namespace: "mapped-namespace",
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				ClusterName: "mapped-cluster",
				ClusterType: v1alpha1.ClusterTypeHostedControlPlane,
			},
			Status: v1alpha1.ClusterInstanceStatus{
				ManagedClusterRef: &corev1.LocalObjectReference{
					Name: "mapped-cluster",
				},
			},
		}

		requests := r.mapClusterInstanceToManagedCluster(ctx, ci)
		Expect(requests).To(HaveLen(1))
		Expect(requests[0].Name).To(Equal("mapped-cluster"))
		Expect(requests[0].Namespace).To(Equal(""))
	})

	It("does not map non-HostedControlPlane ClusterInstance", func() {
		// Create a non-hosted ClusterInstance
		ci := &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "standard-cluster",
				Namespace: "standard-namespace",
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				ClusterName: "standard-cluster",
				ClusterType: v1alpha1.ClusterTypeSNO,
			},
		}

		requests := r.mapClusterInstanceToManagedCluster(ctx, ci)
		Expect(requests).To(HaveLen(0))
	})
})

// findCondition finds a condition by type in a list of conditions
func findCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}
