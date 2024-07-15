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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/stolostron/siteconfig/api/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	doesNotExist = "does-not-exist"
)

var _ = Describe("Validate", func() {
	var (
		c          client.Client
		ctx        = context.Background()
		testParams = &TestParams{
			BmcCredentialsName:  "bmh-secret",
			ClusterName:         "test-cluster",
			ClusterNamespace:    "test-cluster",
			ClusterImageSetName: "testimage:foobar",
			ExtraManifestName:   "extra-manifest",
			ClusterTemplateRef:  "cluster-template-ref",
			NodeTemplateRef:     "node-template-ref",
			PullSecret:          "pull-secret",
		}
		clusterInstance *v1alpha1.ClusterInstance
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			Build()

		SetupTestResources(ctx, c, testParams)
		clusterInstance = testParams.GenerateSNOClusterInstance()
	})

	AfterEach(func() {
		TeardownTestResources(ctx, c, testParams)
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
		clusterInstance.Spec.ClusterImageSetNameRef = doesNotExist
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
		clusterInstance.Spec.PullSecretRef = corev1.LocalObjectReference{Name: doesNotExist}
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
		clusterInstance.Spec.ExtraManifestsRefs = []corev1.LocalObjectReference{{Name: doesNotExist}}
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
			{Name: doesNotExist, Namespace: testParams.ClusterName}}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := Validate(ctx, c, clusterInstance)
		Expect(err).To(MatchError(ContainSubstring("failed to validate node-level TemplateRef")))
	})

	It("fails validation due to missing BMC credential secret", func() {
		clusterInstance.Spec.Nodes[0].BmcCredentialsName = v1alpha1.BmcCredentialsName{Name: doesNotExist}
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

	It("fails validation when more than 1 ControlPlane agent is defined for SNO-based cluster-type", func() {
		clusterInstance.Spec.Nodes = []v1alpha1.NodeSpec{
			{
				Role:               "master",
				BmcAddress:         "192.0.2.1",
				BmcCredentialsName: v1alpha1.BmcCredentialsName{Name: testParams.BmcCredentialsName},
				TemplateRefs: []v1alpha1.TemplateRef{
					{Name: testParams.NodeTemplateRef, Namespace: testParams.ClusterName}}},
			{
				Role:               "master",
				BmcAddress:         "192.0.2.2",
				BmcCredentialsName: v1alpha1.BmcCredentialsName{Name: testParams.BmcCredentialsName},
				TemplateRefs: []v1alpha1.TemplateRef{
					{Name: testParams.NodeTemplateRef, Namespace: testParams.ClusterName}}}}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := Validate(ctx, c, clusterInstance)
		Expect(err).To(MatchError(ContainSubstring("sno cluster-type can only have 1 control-plane agent")))
	})
})
