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
	"github.com/stolostron/siteconfig/api/v1alpha1"
	"github.com/stolostron/siteconfig/internal/controller/conditions"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const ClusterInstanceApiVersion = v1alpha1.Group + "/" + v1alpha1.Version

// compareToExpectedCondition compares the observed condition to the expected condition
func compareToExpectedCondition(observed, expected *metav1.Condition) {
	Expect(observed).ToNot(BeNil())
	Expect(observed.Type).To(Equal(expected.Type))
	Expect(observed.Status).To(Equal(expected.Status))
	Expect(observed.Reason).To(Equal(expected.Reason))
}

var _ = Describe("Reconcile", func() {
	var (
		c                client.Client
		r                *ClusterDeploymentReconciler
		ctx              = context.Background()
		clusterName      = "test-cluster"
		clusterNamespace = "test-namespace"
		clusterInstance  *v1alpha1.ClusterInstance
	)
	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			Build()
		testLogger := ctrl.Log.WithName("ClusterDeploymentReconciler")
		r = &ClusterDeploymentReconciler{
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
				ClusterType:            v1alpha1.ClusterTypeSNO,
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

	It("doesn't error for a missing ClusterDeployment", func() {
		key := types.NamespacedName{
			Namespace: clusterNamespace,
			Name:      clusterName,
		}

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))
	})

	It("doesn't reconcile a ClusterDeployment that is not owned by ClusterInstance", func() {
		key := types.NamespacedName{
			Namespace: clusterNamespace,
			Name:      clusterName,
		}
		clusterDeployment := &hivev1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: clusterNamespace,
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: "foobar.group.io",
						Kind:       "foobar",
						Name:       clusterName,
					},
				},
			},
			Status: hivev1.ClusterDeploymentStatus{
				Conditions: []hivev1.ClusterDeploymentCondition{
					{
						Type:    hivev1.ClusterInstallFailedClusterDeploymentCondition,
						Status:  corev1.ConditionFalse,
						Reason:  "InstallationNotFailed",
						Message: "The installation has not failed"},
				},
			},
		}
		Expect(c.Create(ctx, clusterDeployment)).To(Succeed())

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))

		// Fetch ClusterInstance and verify that the status is unchanged
		ci := &v1alpha1.ClusterInstance{}
		Expect(c.Get(ctx, key, ci)).To(Succeed())
		Expect(ci.Status).To(Equal(clusterInstance.Status))
	})

	It("tests that ClusterDeploymentReconciler initializes ClusterInstance ClusterDeployment correctly", func() {
		key := types.NamespacedName{
			Namespace: clusterNamespace,
			Name:      clusterName,
		}
		clusterDeployment := &hivev1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: clusterNamespace,
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: ClusterInstanceApiVersion,
						Kind:       v1alpha1.ClusterInstanceKind,
						Name:       clusterName,
					},
				},
			},
		}
		Expect(c.Create(ctx, clusterDeployment)).To(Succeed())

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(doNotRequeue()))

		expectedConditions := []hivev1.ClusterDeploymentCondition{
			{
				Type:    hivev1.ClusterInstallRequirementsMetClusterDeploymentCondition,
				Status:  corev1.ConditionUnknown,
				Message: "Unknown",
			},
			{
				Type:    hivev1.ClusterInstallStoppedClusterDeploymentCondition,
				Status:  corev1.ConditionUnknown,
				Message: "Unknown",
			},
			{
				Type:    hivev1.ClusterInstallCompletedClusterDeploymentCondition,
				Status:  corev1.ConditionUnknown,
				Message: "Unknown",
			},
			{
				Type:    hivev1.ClusterInstallFailedClusterDeploymentCondition,
				Status:  corev1.ConditionUnknown,
				Message: "Unknown",
			},
		}

		ci := &v1alpha1.ClusterInstance{}
		Expect(c.Get(ctx, key, ci)).To(Succeed())
		Expect(ci.Status.ClusterDeploymentRef.Name).To(Equal(clusterName))
		Expect(len(ci.Status.DeploymentConditions)).To(Equal(len(expectedConditions)))

		for _, cond := range expectedConditions {
			found := conditions.FindCDConditionType(ci.Status.DeploymentConditions, cond.Type)
			Expect(found).ToNot(BeNil(), "Condition %s was not found", cond.Type)
			Expect(found.Status).To(Equal(cond.Status))
		}
	})

	It("tests that ClusterDeploymentReconciler updates ClusterInstance deploymentConditions", func() {
		key := types.NamespacedName{
			Namespace: clusterNamespace,
			Name:      clusterName,
		}
		clusterDeployment := &hivev1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: clusterNamespace,
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: ClusterInstanceApiVersion,
						Kind:       v1alpha1.ClusterInstanceKind,
						Name:       clusterName,
					},
				},
			},
		}
		Expect(c.Create(ctx, clusterDeployment)).To(Succeed())

		Expect(c.Get(ctx, key, clusterInstance)).To(Succeed())
		conditions.SetStatusCondition(&clusterInstance.Status.Conditions,
			conditions.Provisioned,
			conditions.InProgress,
			metav1.ConditionTrue,
			"Provisioning cluster")
		err := conditions.UpdateCIStatus(ctx, c, clusterInstance)
		Expect(err).ToNot(HaveOccurred())

		ci := &v1alpha1.ClusterInstance{}
		Expect(c.Get(ctx, key, ci)).To(Succeed())

		DeploymentConditions := [][]hivev1.ClusterDeploymentCondition{
			{
				{
					Type:    hivev1.ClusterInstallRequirementsMetClusterDeploymentCondition,
					Status:  corev1.ConditionFalse,
					Reason:  "ClusterNotReady",
					Message: "The cluster is not ready to begin the installation",
				},
				{
					Type:    hivev1.ClusterInstallStoppedClusterDeploymentCondition,
					Status:  corev1.ConditionFalse,
					Reason:  "InstallationNotStopped",
					Message: "The installation is waiting to start or in progress",
				},
				{
					Type:    hivev1.ClusterInstallCompletedClusterDeploymentCondition,
					Status:  corev1.ConditionTrue,
					Reason:  "InstallationNotFailed",
					Message: "The installation has not started",
				},
				{
					Type:    hivev1.ClusterInstallFailedClusterDeploymentCondition,
					Status:  corev1.ConditionFalse,
					Reason:  "InstallationNotFailed",
					Message: "The installation has not started",
				},
			},

			{
				{
					Type:    hivev1.ClusterInstallRequirementsMetClusterDeploymentCondition,
					Status:  corev1.ConditionTrue,
					Reason:  "ClusterInstallationStopped",
					Message: "The cluster installation stopped",
				},
				{
					Type:    hivev1.ClusterInstallStoppedClusterDeploymentCondition,
					Status:  corev1.ConditionTrue,
					Reason:  "ClusterInstallStopped",
					Message: "The installation has stopped because it completed successfully",
				},
				{
					Type:    hivev1.ClusterInstallCompletedClusterDeploymentCondition,
					Status:  corev1.ConditionTrue,
					Reason:  "InstallationCompleted",
					Message: "The installation has completed: Cluster is installed",
				},
				{
					Type:    hivev1.ClusterInstallFailedClusterDeploymentCondition,
					Status:  corev1.ConditionFalse,
					Reason:  "InstallationNotFailed",
					Message: "The installation has not failed",
				},
			},
		}

		for _, deploymentCondition := range DeploymentConditions {
			clusterDeployment.Status.Conditions = deploymentCondition
			Expect(c.Update(ctx, clusterDeployment)).To(Succeed())

			res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
			Expect(err).NotTo(HaveOccurred())
			Expect(res).To(Equal(doNotRequeue()))

			ci := &v1alpha1.ClusterInstance{}
			Expect(c.Get(ctx, key, ci)).To(Succeed())

			for _, cond := range deploymentCondition {
				found := conditions.FindCDConditionType(ci.Status.DeploymentConditions, cond.Type)
				Expect(found).ToNot(BeNil(), "Condition %s was not found", cond.Type)
				Expect(found.Status).To(Equal(cond.Status))
				Expect(found.Message).To(Equal(cond.Message))
				Expect(found.Reason).To(Equal(cond.Reason))
			}
		}
	})

	It("tests that ClusterInstance provisioned status condition is set to True with reason set to Completed when provisioning succeeded", func() {
		key := types.NamespacedName{
			Namespace: clusterNamespace,
			Name:      clusterName,
		}

		clusterDeployment := &hivev1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: clusterNamespace,
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: ClusterInstanceApiVersion,
						Kind:       v1alpha1.ClusterInstanceKind,
						Name:       clusterName,
					},
				},
			},
			Spec: hivev1.ClusterDeploymentSpec{
				Installed: true,
			},
			Status: hivev1.ClusterDeploymentStatus{
				Conditions: []hivev1.ClusterDeploymentCondition{
					{
						Type:    hivev1.ClusterInstallRequirementsMetClusterDeploymentCondition,
						Status:  corev1.ConditionTrue,
						Reason:  "ClusterInstallationStopped",
						Message: "The cluster installation stopped",
					},
					{
						Type:    hivev1.ClusterInstallStoppedClusterDeploymentCondition,
						Status:  corev1.ConditionTrue,
						Reason:  "ClusterInstallStopped",
						Message: "The installation has stopped because it completed successfully",
					},
					{
						Type:    hivev1.ClusterInstallCompletedClusterDeploymentCondition,
						Status:  corev1.ConditionTrue,
						Reason:  "InstallationCompleted",
						Message: "The installation has completed: Cluster is installed",
					},
					{
						Type:    hivev1.ClusterInstallFailedClusterDeploymentCondition,
						Status:  corev1.ConditionFalse,
						Reason:  "InstallationNotFailed",
						Message: "The installation has not failed",
					},
				},
			},
		}

		Expect(c.Create(ctx, clusterDeployment)).To(Succeed())

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))

		ci := &v1alpha1.ClusterInstance{}
		Expect(c.Get(ctx, key, ci)).To(Succeed())

		expectedCondition := &metav1.Condition{
			Type:   string(conditions.Provisioned),
			Status: metav1.ConditionTrue,
			Reason: string(conditions.Completed),
		}

		found := conditions.FindStatusCondition(ci.Status.Conditions, expectedCondition.Type)
		compareToExpectedCondition(found, expectedCondition)
	})

	It("tests that ClusterInstance provisioned status condition is set to False with reason set to Failed when provisioning failed", func() {
		key := types.NamespacedName{
			Namespace: clusterNamespace,
			Name:      clusterName,
		}

		clusterDeployment := &hivev1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: clusterNamespace,
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: ClusterInstanceApiVersion,
						Kind:       v1alpha1.ClusterInstanceKind,
						Name:       clusterName,
					},
				},
			},
			Spec: hivev1.ClusterDeploymentSpec{
				Installed: false,
			},
			Status: hivev1.ClusterDeploymentStatus{
				Conditions: []hivev1.ClusterDeploymentCondition{
					{
						Type:    hivev1.ClusterInstallRequirementsMetClusterDeploymentCondition,
						Status:  corev1.ConditionTrue,
						Reason:  "ClusterInstallationStopped",
						Message: "The cluster installation stopped",
					},
					{
						Type:    hivev1.ClusterInstallStoppedClusterDeploymentCondition,
						Status:  corev1.ConditionTrue,
						Reason:  "ClusterInstallStopped",
						Message: "The installation has stopped because it failed",
					},
					{
						Type:    hivev1.ClusterInstallCompletedClusterDeploymentCondition,
						Status:  corev1.ConditionFalse,
						Reason:  "InstallationCompleted",
						Message: "The installation has completed: Cluster failed to install",
					},
					{
						Type:    hivev1.ClusterInstallFailedClusterDeploymentCondition,
						Status:  corev1.ConditionTrue,
						Reason:  "InstallationFailed",
						Message: "The installation has failed",
					},
				},
			},
		}

		Expect(c.Create(ctx, clusterDeployment)).To(Succeed())

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))

		ci := &v1alpha1.ClusterInstance{}
		Expect(c.Get(ctx, key, ci)).To(Succeed())

		expectedCondition := &metav1.Condition{
			Type:   string(conditions.Provisioned),
			Status: metav1.ConditionFalse,
			Reason: string(conditions.Failed),
		}

		found := conditions.FindStatusCondition(ci.Status.Conditions, expectedCondition.Type)
		compareToExpectedCondition(found, expectedCondition)
	})

	It("tests that ClusterInstance provisioned status condition is set to Unknown with reason set to StaleConditions "+
		"when ClusterDeployment.Spec.Installed=true and the deployment conditions have not been updated", func() {
		key := types.NamespacedName{
			Namespace: clusterNamespace,
			Name:      clusterName,
		}

		clusterDeployment := &hivev1.ClusterDeployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: clusterNamespace,
				OwnerReferences: []metav1.OwnerReference{
					{
						APIVersion: ClusterInstanceApiVersion,
						Kind:       v1alpha1.ClusterInstanceKind,
						Name:       clusterName,
					},
				},
			},
			Spec: hivev1.ClusterDeploymentSpec{
				Installed: false,
			},
			Status: hivev1.ClusterDeploymentStatus{
				Conditions: []hivev1.ClusterDeploymentCondition{
					{
						Type:    hivev1.ClusterInstallRequirementsMetClusterDeploymentCondition,
						Status:  corev1.ConditionFalse,
						Reason:  "ClusterNotReady",
						Message: "The cluster is not ready to begin the installation",
					},
					{
						Type:    hivev1.ClusterInstallStoppedClusterDeploymentCondition,
						Status:  corev1.ConditionFalse,
						Reason:  "InstallationNotStopped",
						Message: "The installation is waiting to start or in progress",
					},
					{
						Type:    hivev1.ClusterInstallCompletedClusterDeploymentCondition,
						Status:  corev1.ConditionTrue,
						Reason:  "InstallationNotFailed",
						Message: "The installation has not started",
					},
					{
						Type:    hivev1.ClusterInstallFailedClusterDeploymentCondition,
						Status:  corev1.ConditionFalse,
						Reason:  "InstallationNotFailed",
						Message: "The installation has not started",
					},
				},
			},
		}

		Expect(c.Create(ctx, clusterDeployment)).To(Succeed())

		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())

		// Set cluster installed -> true
		clusterDeployment.Spec.Installed = true
		Expect(c.Update(ctx, clusterDeployment)).To(Succeed())

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))

		ci := &v1alpha1.ClusterInstance{}
		Expect(c.Get(ctx, key, ci)).To(Succeed())

		expectedCondition := &metav1.Condition{
			Type:   string(conditions.Provisioned),
			Status: metav1.ConditionUnknown,
			Reason: string(conditions.StaleConditions),
		}

		found := conditions.FindStatusCondition(ci.Status.Conditions, expectedCondition.Type)
		compareToExpectedCondition(found, expectedCondition)
	})

})
