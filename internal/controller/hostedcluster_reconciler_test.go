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
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/stolostron/siteconfig/api/v1alpha1"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
	"github.com/stolostron/siteconfig/internal/controller/conditions"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var _ = Describe("Reconcile", func() {
	var (
		c                client.Client
		r                *HostedClusterReconciler
		ctx              = context.Background()
		testLogger       = zap.NewNop().Named("Test")
		clusterName      = "test-cluster"
		clusterNamespace = "test-namespace"
		clusterInstance  *v1alpha1.ClusterInstance
	)
	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			Build()
		r = &HostedClusterReconciler{
			Client: c,
			Scheme: scheme.Scheme,
			Log:    testLogger,
		}

		clusterInstance = &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:       clusterName,
				Namespace:  clusterNamespace,
				Finalizers: []string{clusterInstanceFinalizer},
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				ClusterName:            clusterName,
				PullSecretRef:          corev1.LocalObjectReference{Name: "pull-secret"},
				ClusterImageSetNameRef: "testimage:foobar",
				SSHPublicKey:           "test-ssh",
				BaseDomain:             "abcd",
				ClusterType:            v1alpha1.ClusterTypeHostedControlPlane,
				TemplateRefs: []v1alpha1.TemplateRef{
					{Name: "test-cluster-template", Namespace: "default"}},
				Nodes: []v1alpha1.NodeSpec{{
					BmcAddress:         "192.0.2.0",
					BmcCredentialsName: v1alpha1.BmcCredentialsName{Name: "bmc"},
					TemplateRefs: []v1alpha1.TemplateRef{
						{Name: "test-node-template", Namespace: "default"}}}}},
		}

		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: clusterNamespace,
			},
		}
		Expect(c.Create(ctx, ns)).To(Succeed())
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())
	})

	It("doesn't error for a missing HostedCluster", func() {
		key := types.NamespacedName{
			Namespace: clusterNamespace,
			Name:      clusterName,
		}

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))
	})

	It("doesn't reconcile a HostedCluster that is not owned by ClusterInstance", func() {
		key := types.NamespacedName{
			Namespace: clusterNamespace,
			Name:      clusterName,
		}
		hostedCluster := &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: clusterNamespace,
				Labels: map[string]string{
					ci.OwnedByLabel: ci.GenerateOwnedByLabelValue(clusterNamespace, "not-the-owner"),
				},
			},
			Status: hypershiftv1beta1.HostedClusterStatus{
				Conditions: []metav1.Condition{
					{
						Type:    string(hypershiftv1beta1.HostedClusterAvailable),
						Status:  metav1.ConditionFalse,
						Reason:  string(hypershiftv1beta1.StatusUnknownReason),
						Message: "The hosted cluster status is unknown"},
				},
			},
		}
		Expect(c.Create(ctx, hostedCluster)).To(Succeed())

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))

		// Fetch ClusterInstance and verify that the status is unchanged
		ci := &v1alpha1.ClusterInstance{}
		Expect(c.Get(ctx, key, ci)).To(Succeed())
		Expect(ci.Status).To(Equal(clusterInstance.Status))
	})

	It("tests that HostedClusterReconciler initializes ClusterInstance Conditions correctly", func() {
		key := types.NamespacedName{
			Namespace: clusterNamespace,
			Name:      clusterName,
		}
		hostedCluster := &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: clusterNamespace,
				Labels: map[string]string{
					ci.OwnedByLabel: ci.GenerateOwnedByLabelValue(clusterNamespace, clusterName),
				},
			},
		}
		Expect(c.Create(ctx, hostedCluster)).To(Succeed())

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(doNotRequeue()))

		expectedConditions := []metav1.Condition{
			{
				Type:    string(hypershiftv1beta1.HostedClusterAvailable),
				Status:  metav1.ConditionUnknown,
				Message: "Unknown",
			},
			{
				Type:    string(hypershiftv1beta1.HostedClusterProgressing),
				Status:  metav1.ConditionUnknown,
				Message: "Unknown",
			},
			{
				Type:    string(hypershiftv1beta1.HostedClusterDegraded),
				Status:  metav1.ConditionUnknown,
				Message: "Unknown",
			},
		}

		ci := &v1alpha1.ClusterInstance{}
		Expect(c.Get(ctx, key, ci)).To(Succeed())
		Expect(ci.Status.HostedClusterRef.Name).To(Equal(clusterName))

		for _, cond := range expectedConditions {
			found := conditions.FindStatusCondition(ci.Status.Conditions, cond.Type)
			Expect(found).ToNot(BeNil(), "Condition %s was not found", cond.Type)
			Expect(found.Status).To(Equal(cond.Status))
		}
	})

	It("tests that HostedClusterReconciler updates ClusterInstance Conditions", func() {
		key := types.NamespacedName{
			Namespace: clusterNamespace,
			Name:      clusterName,
		}
		hostedCluster := &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: clusterNamespace,
				Labels: map[string]string{
					ci.OwnedByLabel: ci.GenerateOwnedByLabelValue(clusterNamespace, clusterName),
				},
			},
		}
		Expect(c.Create(ctx, hostedCluster)).To(Succeed())

		Expect(c.Get(ctx, key, clusterInstance)).To(Succeed())

		// Set initial condition for CI to be overridden by reconciler
		conditions.SetStatusCondition(&clusterInstance.Status.Conditions,
			v1alpha1.ClusterProvisioned,
			v1alpha1.InProgress,
			metav1.ConditionTrue,
			"Provisioning cluster")
		err := conditions.UpdateCIStatus(ctx, c, clusterInstance)
		Expect(err).ToNot(HaveOccurred())

		ci := &v1alpha1.ClusterInstance{}
		Expect(c.Get(ctx, key, ci)).To(Succeed())

		ConditionSets := [][]metav1.Condition{
			{
				{
					Type:    string(hypershiftv1beta1.HostedClusterAvailable),
					Status:  metav1.ConditionFalse,
					Reason:  "InstallationNotStarted",
					Message: "The hosted cluster installation has not started",
				},
				{
					Type:    string(hypershiftv1beta1.HostedClusterProgressing),
					Status:  metav1.ConditionFalse,
					Reason:  "InstallationNotStarted",
					Message: "The hosted cluster installation has not started",
				},
				{
					Type:    string(hypershiftv1beta1.HostedClusterDegraded),
					Status:  metav1.ConditionFalse,
					Reason:  "InstallationNotFailed",
					Message: "The hosted cluster installation has not started",
				},
			},

			{
				{
					Type:    string(hypershiftv1beta1.HostedClusterAvailable),
					Status:  metav1.ConditionTrue,
					Reason:  "AsExpected",
					Message: "The hosted control plane is available",
				},
				{
					Type:    string(hypershiftv1beta1.HostedClusterProgressing),
					Status:  metav1.ConditionFalse,
					Reason:  "AsExpected",
					Message: "HostedCluster is at expected version",
				},
				{
					Type:    string(hypershiftv1beta1.HostedClusterDegraded),
					Status:  metav1.ConditionFalse,
					Reason:  "AsExpected",
					Message: "The hosted cluster is not degraded",
				},
			},
		}

		for _, conditionSet := range ConditionSets {
			hostedCluster.Status.Conditions = conditionSet
			Expect(c.Update(ctx, hostedCluster)).To(Succeed())

			res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())
			Expect(res).To(Equal(doNotRequeue()))

			ci := &v1alpha1.ClusterInstance{}
			Expect(c.Get(ctx, key, ci)).To(Succeed())

			for _, cond := range conditionSet {
				found := conditions.FindStatusCondition(ci.Status.Conditions, cond.Type)
				Expect(found).ToNot(BeNil(), "Condition %s was not found", cond.Type)
				Expect(found.Status).To(Equal(cond.Status))
				Expect(found.Message).To(Equal(cond.Message))
				Expect(found.Reason).To(Equal(cond.Reason))
			}
		}
	})

	It("tests that ClusterInstance provisioned status condition is set to True with reason set to AsExpected when provisioning succeeded", func() {
		key := types.NamespacedName{
			Namespace: clusterNamespace,
			Name:      clusterName,
		}

		hostedCluster := &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: clusterNamespace,
				Labels: map[string]string{
					ci.OwnedByLabel: ci.GenerateOwnedByLabelValue(clusterNamespace, clusterName),
				},
			},
			Status: hypershiftv1beta1.HostedClusterStatus{
				Conditions: []metav1.Condition{
					{
						Type:    string(hypershiftv1beta1.HostedClusterAvailable),
						Status:  metav1.ConditionTrue,
						Reason:  "AsExpected",
						Message: "The hosted control plane is available",
					},
					{
						Type:    string(hypershiftv1beta1.HostedClusterProgressing),
						Status:  metav1.ConditionFalse,
						Reason:  "AsExpected",
						Message: "HostedCluster is at expected version",
					},
					{
						Type:    string(hypershiftv1beta1.HostedClusterDegraded),
						Status:  metav1.ConditionFalse,
						Reason:  "AsExpected",
						Message: "The hosted cluster is not degraded",
					},
				},
			},
		}

		Expect(c.Create(ctx, hostedCluster)).To(Succeed())

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))

		ci := &v1alpha1.ClusterInstance{}
		Expect(c.Get(ctx, key, ci)).To(Succeed())

		expectedCondition := &metav1.Condition{
			Type:   string(v1alpha1.ClusterProvisioned),
			Status: metav1.ConditionTrue,
			Reason: string(v1alpha1.Completed),
		}

		found := conditions.FindStatusCondition(ci.Status.Conditions, expectedCondition.Type)
		compareToExpectedCondition(found, expectedCondition)
	})

	It("tests that ClusterInstance provisioned status condition is set to False with reason set to Failed when provisioning failed", func() {
		key := types.NamespacedName{
			Namespace: clusterNamespace,
			Name:      clusterName,
		}

		hostedCluster := &hypershiftv1beta1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: clusterNamespace,
				Labels: map[string]string{
					ci.OwnedByLabel: ci.GenerateOwnedByLabelValue(clusterNamespace, clusterName),
				},
			},
			Spec: hypershiftv1beta1.HostedClusterSpec{},
			Status: hypershiftv1beta1.HostedClusterStatus{
				Conditions: []metav1.Condition{
					{
						Type:    string(hypershiftv1beta1.HostedClusterDegraded),
						Status:  metav1.ConditionTrue,
						Reason:  "Blocked",
						Message: "The hosted cluster installation is blocked",
					},
					{
						Type:    string(hypershiftv1beta1.HostedClusterProgressing),
						Status:  metav1.ConditionFalse,
						Reason:  "Blocked",
						Message: "The hosted cluster installation is blocked",
					},
					{
						Type:    string(hypershiftv1beta1.HostedClusterAvailable),
						Status:  metav1.ConditionFalse,
						Reason:  "Blocked",
						Message: "The hosted cluster installation is blocked",
					},
				},
			},
		}

		Expect(c.Create(ctx, hostedCluster)).To(Succeed())

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))

		ci := &v1alpha1.ClusterInstance{}
		Expect(c.Get(ctx, key, ci)).To(Succeed())

		expectedCondition := &metav1.Condition{
			Type:   string(v1alpha1.ClusterProvisioned),
			Status: metav1.ConditionFalse,
			Reason: string(v1alpha1.Failed),
		}

		found := conditions.FindStatusCondition(ci.Status.Conditions, expectedCondition.Type)
		compareToExpectedCondition(found, expectedCondition)
	})
})
