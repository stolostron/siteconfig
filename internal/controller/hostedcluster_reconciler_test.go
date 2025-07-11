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
	hivev1 "github.com/openshift/hive/apis/hive/v1"
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

	It("tests that HostedClusterReconciler initializes ClusterInstance DeploymentConditions when HC conditions are missing", func() {
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
			// No Status.Conditions - simulates early in lifecycle
		}
		Expect(c.Create(ctx, hostedCluster)).To(Succeed())

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(doNotRequeue()))

		// When HostedCluster conditions are missing, DeploymentConditions should all be False
		// since none of the requirements are met
		expectedConditions := []hivev1.ClusterDeploymentCondition{
			{
				Type:   hivev1.ClusterInstallRequirementsMetClusterDeploymentCondition,
				Status: corev1.ConditionFalse,
				Reason: "RequirementsNotMet",
			},
			{
				Type:   hivev1.ClusterInstallCompletedClusterDeploymentCondition,
				Status: corev1.ConditionFalse,
				Reason: "InstallationInProgress",
			},
			{
				Type:   hivev1.ClusterInstallFailedClusterDeploymentCondition,
				Status: corev1.ConditionFalse,
				Reason: "InstallationNotFailed",
			},
			{
				Type:   hivev1.ClusterInstallStoppedClusterDeploymentCondition,
				Status: corev1.ConditionFalse,
				Reason: "InstallationInProgress",
			},
		}

		ci := &v1alpha1.ClusterInstance{}
		Expect(c.Get(ctx, key, ci)).To(Succeed())
		Expect(ci.Status.HostedClusterRef.Name).To(Equal(clusterName))
		Expect(len(ci.Status.DeploymentConditions)).To(Equal(len(expectedConditions)))

		for _, cond := range expectedConditions {
			found := conditions.FindCDConditionType(ci.Status.DeploymentConditions, cond.Type)
			Expect(found).ToNot(BeNil(), "Condition %s was not found", cond.Type)
			Expect(found.Status).To(Equal(cond.Status))
			Expect(found.Reason).To(Equal(cond.Reason))
		}
	})

	It("tests that HostedClusterReconciler updates ClusterInstance DeploymentConditions", func() {
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

		// Test various HostedCluster condition states and verify correct DeploymentConditions
		type testCase struct {
			name                    string
			hostedClusterConditions []metav1.Condition
			expectedDeploymentConds []hivev1.ClusterDeploymentCondition
		}

		testCases := []testCase{
			{
				name: "Requirements met, installation in progress",
				hostedClusterConditions: []metav1.Condition{
					{Type: string(hypershiftv1beta1.ValidHostedClusterConfiguration), Status: metav1.ConditionTrue},
					{Type: string(hypershiftv1beta1.SupportedHostedCluster), Status: metav1.ConditionTrue},
					{Type: string(hypershiftv1beta1.ValidReleaseImage), Status: metav1.ConditionTrue},
					{Type: string(hypershiftv1beta1.HostedClusterAvailable), Status: metav1.ConditionFalse},
					{Type: string(hypershiftv1beta1.HostedClusterProgressing), Status: metav1.ConditionTrue},
					{Type: string(hypershiftv1beta1.HostedClusterDegraded), Status: metav1.ConditionFalse},
					{Type: string(hypershiftv1beta1.ClusterVersionSucceeding), Status: metav1.ConditionFalse},
				},
				expectedDeploymentConds: []hivev1.ClusterDeploymentCondition{
					{Type: hivev1.ClusterInstallRequirementsMetClusterDeploymentCondition, Status: corev1.ConditionTrue, Reason: "RequirementsMet"},
					{Type: hivev1.ClusterInstallCompletedClusterDeploymentCondition, Status: corev1.ConditionFalse, Reason: "InstallationInProgress"},
					{Type: hivev1.ClusterInstallFailedClusterDeploymentCondition, Status: corev1.ConditionFalse, Reason: "InstallationNotFailed"},
					{Type: hivev1.ClusterInstallStoppedClusterDeploymentCondition, Status: corev1.ConditionFalse, Reason: "InstallationInProgress"},
				},
			},
			{
				name: "Installation completed successfully",
				hostedClusterConditions: []metav1.Condition{
					{Type: string(hypershiftv1beta1.ValidHostedClusterConfiguration), Status: metav1.ConditionTrue},
					{Type: string(hypershiftv1beta1.SupportedHostedCluster), Status: metav1.ConditionTrue},
					{Type: string(hypershiftv1beta1.ValidReleaseImage), Status: metav1.ConditionTrue},
					{Type: string(hypershiftv1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue},
					{Type: string(hypershiftv1beta1.HostedClusterProgressing), Status: metav1.ConditionFalse},
					{Type: string(hypershiftv1beta1.HostedClusterDegraded), Status: metav1.ConditionFalse},
					{Type: string(hypershiftv1beta1.ClusterVersionSucceeding), Status: metav1.ConditionTrue},
				},
				expectedDeploymentConds: []hivev1.ClusterDeploymentCondition{
					{Type: hivev1.ClusterInstallRequirementsMetClusterDeploymentCondition, Status: corev1.ConditionTrue, Reason: "RequirementsMet"},
					{Type: hivev1.ClusterInstallCompletedClusterDeploymentCondition, Status: corev1.ConditionTrue, Reason: "InstallationCompleted"},
					{Type: hivev1.ClusterInstallFailedClusterDeploymentCondition, Status: corev1.ConditionFalse, Reason: "InstallationNotFailed"},
					{Type: hivev1.ClusterInstallStoppedClusterDeploymentCondition, Status: corev1.ConditionTrue, Reason: "InstallationStopped"},
				},
			},
		}

		for _, tc := range testCases {
			hostedCluster.Status.Conditions = tc.hostedClusterConditions
			Expect(c.Update(ctx, hostedCluster)).To(Succeed())

			res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())
			Expect(res).To(Equal(doNotRequeue()))

			ci := &v1alpha1.ClusterInstance{}
			Expect(c.Get(ctx, key, ci)).To(Succeed())

			for _, expectedCond := range tc.expectedDeploymentConds {
				found := conditions.FindCDConditionType(ci.Status.DeploymentConditions, expectedCond.Type)
				Expect(found).ToNot(BeNil(), "[%s] Condition %s was not found", tc.name, expectedCond.Type)
				Expect(found.Status).To(Equal(expectedCond.Status), "[%s] Condition %s status mismatch", tc.name, expectedCond.Type)
				Expect(found.Reason).To(Equal(expectedCond.Reason), "[%s] Condition %s reason mismatch", tc.name, expectedCond.Type)
			}
		}
	})

	It("tests that ClusterInstance provisioned status condition is set to True with reason set to Completed when provisioning succeeded", func() {
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
					{
						Type:    string(hypershiftv1beta1.ClusterVersionSucceeding),
						Status:  metav1.ConditionTrue,
						Reason:  "AsExpected",
						Message: "Cluster version is succeeding",
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
						Type:    string(hypershiftv1beta1.ValidReleaseInfo),
						Status:  metav1.ConditionTrue,
						Reason:  "AsExpected",
						Message: "Release info is valid",
					},
					{
						Type:    string(hypershiftv1beta1.ClusterVersionFailing),
						Status:  metav1.ConditionTrue,
						Reason:  "Blocked",
						Message: "Cluster version is failing",
					},
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

	It("tests DeploymentConditions when Degraded=True during early provisioning", func() {
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
					{Type: string(hypershiftv1beta1.ValidHostedClusterConfiguration), Status: metav1.ConditionTrue},
					{Type: string(hypershiftv1beta1.SupportedHostedCluster), Status: metav1.ConditionTrue},
					{Type: string(hypershiftv1beta1.ValidReleaseImage), Status: metav1.ConditionTrue},
					{Type: string(hypershiftv1beta1.HostedClusterDegraded), Status: metav1.ConditionTrue},
					{Type: string(hypershiftv1beta1.HostedClusterProgressing), Status: metav1.ConditionFalse},
					{Type: string(hypershiftv1beta1.HostedClusterAvailable), Status: metav1.ConditionFalse},
					// ValidReleaseInfo not True yet - still early provisioning
				},
			},
		}

		Expect(c.Create(ctx, hostedCluster)).To(Succeed())

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))

		ci := &v1alpha1.ClusterInstance{}
		Expect(c.Get(ctx, key, ci)).To(Succeed())

		// Verify Failed=False (Degraded alone doesn't indicate failure)
		failedCond := conditions.FindCDConditionType(ci.Status.DeploymentConditions, hivev1.ClusterInstallFailedClusterDeploymentCondition)
		Expect(failedCond).ToNot(BeNil())
		Expect(failedCond.Status).To(Equal(corev1.ConditionFalse))

		// Verify Stopped=False (not failed, so not stopped)
		stoppedCond := conditions.FindCDConditionType(ci.Status.DeploymentConditions, hivev1.ClusterInstallStoppedClusterDeploymentCondition)
		Expect(stoppedCond).ToNot(BeNil())
		Expect(stoppedCond.Status).To(Equal(corev1.ConditionFalse))
	})

	It("tests DeploymentConditions for failed installation due to ClusterVersionFailing=True", func() {
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
					{Type: string(hypershiftv1beta1.ValidHostedClusterConfiguration), Status: metav1.ConditionTrue},
					{Type: string(hypershiftv1beta1.SupportedHostedCluster), Status: metav1.ConditionTrue},
					{Type: string(hypershiftv1beta1.ValidReleaseImage), Status: metav1.ConditionTrue},
					{Type: string(hypershiftv1beta1.ValidReleaseInfo), Status: metav1.ConditionTrue},
					{Type: string(hypershiftv1beta1.ClusterVersionFailing), Status: metav1.ConditionTrue},
					{Type: string(hypershiftv1beta1.HostedClusterProgressing), Status: metav1.ConditionFalse},
					{Type: string(hypershiftv1beta1.HostedClusterDegraded), Status: metav1.ConditionFalse},
				},
			},
		}

		Expect(c.Create(ctx, hostedCluster)).To(Succeed())

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))

		ci := &v1alpha1.ClusterInstance{}
		Expect(c.Get(ctx, key, ci)).To(Succeed())

		// Verify Failed=True
		failedCond := conditions.FindCDConditionType(ci.Status.DeploymentConditions, hivev1.ClusterInstallFailedClusterDeploymentCondition)
		Expect(failedCond).ToNot(BeNil())
		Expect(failedCond.Status).To(Equal(corev1.ConditionTrue))

		// Verify Stopped=True (failed and not progressing)
		stoppedCond := conditions.FindCDConditionType(ci.Status.DeploymentConditions, hivev1.ClusterInstallStoppedClusterDeploymentCondition)
		Expect(stoppedCond).ToNot(BeNil())
		Expect(stoppedCond.Status).To(Equal(corev1.ConditionTrue))
	})

	It("tests DeploymentConditions when requirements are not met", func() {
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
					// ValidConfiguration is False - requirements not met
					{Type: string(hypershiftv1beta1.ValidHostedClusterConfiguration), Status: metav1.ConditionFalse},
					{Type: string(hypershiftv1beta1.SupportedHostedCluster), Status: metav1.ConditionTrue},
					{Type: string(hypershiftv1beta1.ValidReleaseImage), Status: metav1.ConditionTrue},
				},
			},
		}

		Expect(c.Create(ctx, hostedCluster)).To(Succeed())

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))

		ci := &v1alpha1.ClusterInstance{}
		Expect(c.Get(ctx, key, ci)).To(Succeed())

		// Verify RequirementsMet=False
		reqMetCond := conditions.FindCDConditionType(ci.Status.DeploymentConditions, hivev1.ClusterInstallRequirementsMetClusterDeploymentCondition)
		Expect(reqMetCond).ToNot(BeNil())
		Expect(reqMetCond.Status).To(Equal(corev1.ConditionFalse))
		Expect(reqMetCond.Reason).To(Equal("RequirementsNotMet"))
	})

	It("tests that ClusterInstance is not marked as Completed when ClusterVersionSucceeding is missing", func() {
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
					// ClusterVersionSucceeding is missing - should NOT be marked as Completed
				},
			},
		}

		Expect(c.Create(ctx, hostedCluster)).To(Succeed())

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))

		ci := &v1alpha1.ClusterInstance{}
		Expect(c.Get(ctx, key, ci)).To(Succeed())

		// Verify Provisioned is InProgress, not Completed
		expectedCondition := &metav1.Condition{
			Type:   string(v1alpha1.ClusterProvisioned),
			Status: metav1.ConditionFalse,
			Reason: string(v1alpha1.InProgress),
		}

		found := conditions.FindStatusCondition(ci.Status.Conditions, expectedCondition.Type)
		compareToExpectedCondition(found, expectedCondition)
	})

	It("tests DeploymentConditions when installation is still progressing", func() {
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
					{Type: string(hypershiftv1beta1.ValidHostedClusterConfiguration), Status: metav1.ConditionTrue},
					{Type: string(hypershiftv1beta1.SupportedHostedCluster), Status: metav1.ConditionTrue},
					{Type: string(hypershiftv1beta1.ValidReleaseImage), Status: metav1.ConditionTrue},
					{Type: string(hypershiftv1beta1.HostedClusterAvailable), Status: metav1.ConditionTrue},
					{Type: string(hypershiftv1beta1.ClusterVersionSucceeding), Status: metav1.ConditionTrue},
					// Still progressing - should not be stopped yet
					{Type: string(hypershiftv1beta1.HostedClusterProgressing), Status: metav1.ConditionTrue},
					{Type: string(hypershiftv1beta1.HostedClusterDegraded), Status: metav1.ConditionFalse},
				},
			},
		}

		Expect(c.Create(ctx, hostedCluster)).To(Succeed())

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))

		ci := &v1alpha1.ClusterInstance{}
		Expect(c.Get(ctx, key, ci)).To(Succeed())

		// Verify Stopped=False (still progressing even though completed)
		stoppedCond := conditions.FindCDConditionType(ci.Status.DeploymentConditions, hivev1.ClusterInstallStoppedClusterDeploymentCondition)
		Expect(stoppedCond).ToNot(BeNil())
		Expect(stoppedCond.Status).To(Equal(corev1.ConditionFalse))
		Expect(stoppedCond.Reason).To(Equal("InstallationInProgress"))

		// Verify Completed=True (requirements met)
		completedCond := conditions.FindCDConditionType(ci.Status.DeploymentConditions, hivev1.ClusterInstallCompletedClusterDeploymentCondition)
		Expect(completedCond).ToNot(BeNil())
		Expect(completedCond.Status).To(Equal(corev1.ConditionTrue))
	})
})
