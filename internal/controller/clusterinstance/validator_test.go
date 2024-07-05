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
*/

package clusterinstance

import (
	"context"

	hivev1 "github.com/openshift/hive/apis/hive/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/stolostron/siteconfig/api/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Validate", func() {
	var (
		bmcCredentialsName                           = "bmh-secret"
		bmc, pullSecret                              *corev1.Secret
		c                                            client.Client
		ctx                                          = context.Background()
		clusterName                                  = "test-cluster"
		clusterNamespace                             = "test-cluster"
		clusterImageSetName                          = "testimage:foobar"
		clusterImageSet                              *hivev1.ClusterImageSet
		clusterTemplateRef                           = "cluster-template-ref"
		clusterTemplate, nodeTemplate, extraManifest *corev1.ConfigMap
		extraManifestName                            = "extra-manifest"
		nodeTemplateRef                              = "node-template-ref"
		clusterInstance                              *v1alpha1.ClusterInstance
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			Build()

		bmc = GetMockBmcSecret(bmcCredentialsName, clusterNamespace)
		clusterImageSet = GetMockClusterImageSet(clusterImageSetName)
		pullSecret = GetMockPullSecret("pull-secret", clusterNamespace)
		clusterTemplate = GetMockClusterTemplate(clusterTemplateRef, clusterNamespace)
		nodeTemplate = GetMockNodeTemplate(nodeTemplateRef, clusterNamespace)
		extraManifest = GetMockExtraManifest(extraManifestName, clusterNamespace)

		SetupTestPrereqs(ctx, c, bmc, pullSecret, clusterImageSet, clusterTemplate, nodeTemplate, extraManifest)

		clusterInstance = GetMockSNOClusterInstance(clusterName, clusterNamespace, pullSecret.Name, bmcCredentialsName, clusterImageSetName, extraManifestName, clusterTemplateRef, nodeTemplateRef)

	})

	It("successfully validates a well-defined ClusterInstance", func() {
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := Validate(ctx, c, clusterInstance)
		Expect(err).ToNot(HaveOccurred())
	})

	It("fails validation when cluster name is not defined", func() {
		clusterInstance.Spec.ClusterName = ""
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := Validate(ctx, c, clusterInstance)
		Expect(err).To(MatchError(ContainSubstring("missing cluster name")))
	})

	It("fails validation when clusterImageSetName reference is not defined", func() {
		clusterInstance.Spec.ClusterImageSetNameRef = ""
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := Validate(ctx, c, clusterInstance)
		Expect(err).To(MatchError(ContainSubstring("clusterImageSetNameRef cannot be empty")))
	})

	It("fails validation when clusterImageSet resource does not exist", func() {
		clusterInstance.Spec.ClusterImageSetNameRef = "does-not-exist"
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := Validate(ctx, c, clusterInstance)
		Expect(err).To(MatchError(ContainSubstring("encountered error validating ClusterImageSetNameRef")))
	})

	It("fails validation when cluster-level template refs are not defined", func() {
		clusterInstance.Spec.TemplateRefs = []v1alpha1.TemplateRef{}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := Validate(ctx, c, clusterInstance)
		Expect(err).To(MatchError("missing cluster-level TemplateRefs"))
	})

	It("fails validation due to missing pull secret", func() {
		clusterInstance.Spec.PullSecretRef = &corev1.LocalObjectReference{Name: "does-not-exist"}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := Validate(ctx, c, clusterInstance)
		Expect(err).To(MatchError(ContainSubstring("failed to validate Pull Secret")))
	})

	It("fails validation due to invalid cluster-level installConfigOverrides JSON-formatted strings", func() {
		clusterInstance.Spec.InstallConfigOverrides = "foobar"
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())
		err := Validate(ctx, c, clusterInstance)
		Expect(err).To(MatchError(ContainSubstring("installConfigOverrides is not a valid JSON-formatted string")))
	})

	It("fails validation due to invalid cluster-level ignitionConfigOverride JSON-formatted strings", func() {
		clusterInstance.Spec.IgnitionConfigOverride = "{foo:bar}"
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())
		err := Validate(ctx, c, clusterInstance)
		Expect(err).To(MatchError(ContainSubstring("cluster-level ignitionConfigOverride is not a valid JSON-formatted string")))
	})

	It("fails validation when an ExtraManifest reference does not exist", func() {
		clusterInstance.Spec.ExtraManifestsRefs = []corev1.LocalObjectReference{{Name: "does-not-exist"}}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())
		err := Validate(ctx, c, clusterInstance)
		Expect(err).To(MatchError(ContainSubstring("failed to retrieve ExtraManifest")))
	})

	It("fails validation when node-level template refs are not defined", func() {
		clusterInstance.Spec.Nodes[0].TemplateRefs = []v1alpha1.TemplateRef{}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := Validate(ctx, c, clusterInstance)
		Expect(err).To(MatchError(ContainSubstring("missing node-level template refs")))
	})

	It("fails validation when a node-level template ref does not exist", func() {
		clusterInstance.Spec.Nodes[0].TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "does-not-exist", Namespace: clusterName}}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := Validate(ctx, c, clusterInstance)
		Expect(err).To(MatchError(ContainSubstring("failed to validate node-level TemplateRef")))
	})

	It("fails validation due to missing BMC credential secret", func() {
		clusterInstance.Spec.Nodes[0].BmcCredentialsName = v1alpha1.BmcCredentialsName{Name: "does-not-exist"}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := Validate(ctx, c, clusterInstance)
		Expect(err).To(MatchError(ContainSubstring("failed to validate BMC credentials")))
	})

	It("fails validation due to invalid node-level installerArgs JSON-formatted strings", func() {
		clusterInstance.Spec.Nodes[0].InstallerArgs = "{foo:bar}"
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := Validate(ctx, c, clusterInstance)
		Expect(err).To(MatchError(ContainSubstring("installerArgs is not a valid JSON-formatted string")))
	})

	It("fails validation due to invalid node-level ignitionConfigOverride JSON-formatted strings", func() {
		clusterInstance.Spec.Nodes[0].IgnitionConfigOverride = "{foo:bar}"
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())
		err := Validate(ctx, c, clusterInstance)

		Expect(err).To(MatchError(ContainSubstring("ignitionConfigOverride is not a valid JSON-formatted string")))
	})

	It("fails validation when no control-plane agents are configured", func() {
		clusterInstance.Spec.Nodes[0].Role = "worker"
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := Validate(ctx, c, clusterInstance)
		Expect(err).To(MatchError(ContainSubstring("at least 1 ControlPlane agent is required")))
	})

	It("fails validation when worker agents are defined for SNO-based cluster-type", func() {
		clusterInstance.Spec.Nodes = []v1alpha1.NodeSpec{
			{
				Role:               "master",
				BmcAddress:         "1:2:3:4",
				BmcCredentialsName: v1alpha1.BmcCredentialsName{Name: bmcCredentialsName},
				TemplateRefs: []v1alpha1.TemplateRef{
					{Name: nodeTemplateRef, Namespace: clusterName}}},
			{
				Role:               "worker",
				BmcAddress:         "4:5:6:7",
				BmcCredentialsName: v1alpha1.BmcCredentialsName{Name: bmcCredentialsName},
				TemplateRefs: []v1alpha1.TemplateRef{
					{Name: nodeTemplateRef, Namespace: clusterName}}}}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := Validate(ctx, c, clusterInstance)
		Expect(err).To(MatchError(ContainSubstring("sno cluster-type requires 1 control-plane agent and no worker agents")))
	})
})
