/*
Copyright 2025 Red Hat, Inc.

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
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/stolostron/siteconfig/api/v1alpha1"
)

var _ = Describe("copySharedExtraManifests", func() {
	const (
		clusterName      = "cluster-1"
		clusterNamespace = "cluster-1-ns"
		sharedCMName     = "shared-cm"
	)

	var (
		c          client.Client
		r          *ClusterInstanceReconciler
		ctx        = context.Background()
		testLogger = zap.NewNop().Named("Test")
		clusterKey = types.NamespacedName{Name: sharedCMName, Namespace: clusterNamespace}
	)

	newClusterInstance := func(refs ...v1alpha1.ExtraManifestReference) *v1alpha1.ClusterInstance {
		return &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: clusterNamespace,
				UID:       "test-uid",
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				ExtraManifestsRefs: refs,
			},
		}
	}

	createSharedCM := func(data map[string]string) *corev1.ConfigMap {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: sharedCMName, Namespace: v1alpha1.SharedResourcesNamespace, Labels: map[string]string{acmBackupLabel: acmBackupLabelValue}},
			Data:       data,
		}
		Expect(c.Create(ctx, cm)).To(Succeed())
		return cm
	}

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().WithScheme(scheme.Scheme).Build()
		r = &ClusterInstanceReconciler{Client: c, Scheme: scheme.Scheme, Log: testLogger}
	})

	It("copies a shared ConfigMap with ownerReference, managed-copy label, and provenance annotations", func() {
		source := createSharedCM(map[string]string{"foo": "bar"})

		ci := newClusterInstance(v1alpha1.ExtraManifestReference{
			Name:      sharedCMName,
			Namespace: v1alpha1.SharedResourcesNamespace,
		})

		Expect(r.copySharedExtraManifests(ctx, testLogger, ci)).To(Succeed())

		copy := &corev1.ConfigMap{}
		Expect(c.Get(ctx, clusterKey, copy)).To(Succeed())
		Expect(copy.Data).To(Equal(source.Data))
		Expect(copy.Labels).To(HaveKeyWithValue(ManagedCopyLabel, ManagedCopyLabelValue))
		Expect(copy.Annotations).To(HaveKeyWithValue(SourceNamespaceAnnotation, v1alpha1.SharedResourcesNamespace))
		Expect(copy.Annotations).To(HaveKeyWithValue(SourceResourceVersionAnnotation, source.ResourceVersion))
		Expect(copy.Annotations).To(HaveKeyWithValue(InitialResourceVersionAnnotation, source.ResourceVersion))

		Expect(copy.OwnerReferences).To(HaveLen(1))
		Expect(copy.OwnerReferences[0].Kind).To(Equal(v1alpha1.ClusterInstanceKind))
		Expect(copy.OwnerReferences[0].Name).To(Equal(clusterName))

		// Copying must preserve source references for subsequent reconciliation.
		Expect(ci.Spec.ExtraManifestsRefs[0].Namespace).To(Equal(v1alpha1.SharedResourcesNamespace))
	})

	It("updates an existing managed copy with new data and provenance annotations", func() {
		createSharedCM(map[string]string{"foo": "bar"})
		ci := newClusterInstance(v1alpha1.ExtraManifestReference{
			Name:      sharedCMName,
			Namespace: v1alpha1.SharedResourcesNamespace,
		})
		Expect(r.copySharedExtraManifests(ctx, testLogger, ci)).To(Succeed())

		// Update the source and re-copy.
		source := &corev1.ConfigMap{}
		Expect(c.Get(ctx, types.NamespacedName{Name: sharedCMName, Namespace: v1alpha1.SharedResourcesNamespace}, source)).To(Succeed())
		source.Data = map[string]string{"foo": "baz", "new": "value"}
		Expect(c.Update(ctx, source)).To(Succeed())

		ci2 := newClusterInstance(v1alpha1.ExtraManifestReference{
			Name:      sharedCMName,
			Namespace: v1alpha1.SharedResourcesNamespace,
		})
		Expect(r.copySharedExtraManifests(ctx, testLogger, ci2)).To(Succeed())

		copy := &corev1.ConfigMap{}
		Expect(c.Get(ctx, clusterKey, copy)).To(Succeed())
		Expect(copy.Data).To(Equal(source.Data))
		Expect(copy.Annotations).To(HaveKeyWithValue(SourceResourceVersionAnnotation, source.ResourceVersion))
	})

	It("does not propagate GitOps tracking metadata during creation, re-copy, or sync", func() {
		source := createSharedCM(map[string]string{"foo": "initial"})
		source.Labels = map[string]string{"argocd.argoproj.io/instance": "shared-config", acmBackupLabel: acmBackupLabelValue, v1alpha1.Group + "/preserve": "All", v1alpha1.Group + "/preserve.internal": "true"}
		source.Annotations = map[string]string{"argocd.argoproj.io/tracking-id": "shared-config:source"}
		Expect(c.Update(ctx, source)).To(Succeed())
		ci := newClusterInstance(v1alpha1.ExtraManifestReference{
			Name: sharedCMName, Namespace: v1alpha1.SharedResourcesNamespace,
		})
		syncer := &SharedManifestSync{Client: c}
		var initialVersion string
		for _, phase := range []string{"creation", "re-copy", "sync"} {
			source.Data = map[string]string{"foo": phase}
			Expect(c.Update(ctx, source)).To(Succeed())
			if phase == "sync" {
				Expect(syncer.propagate(ctx, testLogger, ci, source)).To(Succeed())
			} else {
				Expect(r.copySharedExtraManifests(ctx, testLogger, ci)).To(Succeed())
			}
			copy := &corev1.ConfigMap{}
			Expect(c.Get(ctx, clusterKey, copy)).To(Succeed())
			Expect(copy.Data).To(Equal(source.Data))
			Expect(copy.Labels).To(Equal(map[string]string{ManagedCopyLabel: ManagedCopyLabelValue}))
			Expect(copy.Annotations).NotTo(HaveKey("argocd.argoproj.io/tracking-id"))
			if phase == "creation" {
				initialVersion = source.ResourceVersion
			}
			Expect(copy.Annotations).To(Equal(map[string]string{InitialResourceVersionAnnotation: initialVersion, SourceResourceVersionAnnotation: source.ResourceVersion, SourceNamespaceAnnotation: v1alpha1.SharedResourcesNamespace}))
		}
	})

	It("fails when a user-created ConfigMap with the same name already exists locally", func() {
		createSharedCM(map[string]string{"foo": "bar"})

		// User-created ConfigMap without the managed-copy label.
		userCM := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: sharedCMName, Namespace: clusterNamespace},
			Data:       map[string]string{"user": "data"},
		}
		Expect(c.Create(ctx, userCM)).To(Succeed())

		ci := newClusterInstance(v1alpha1.ExtraManifestReference{
			Name:      sharedCMName,
			Namespace: v1alpha1.SharedResourcesNamespace,
		})
		err := r.copySharedExtraManifests(ctx, testLogger, ci)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("name collision"))

		// The user-created ConfigMap must not be overwritten.
		existing := &corev1.ConfigMap{}
		Expect(c.Get(ctx, clusterKey, existing)).To(Succeed())
		Expect(existing.Data).To(Equal(map[string]string{"user": "data"}))
	})

	It("does not adopt a labeled ConfigMap without the expected controller owner", func() {
		createSharedCM(map[string]string{"foo": "bar"})
		ci := newClusterInstance(v1alpha1.ExtraManifestReference{Name: sharedCMName, Namespace: v1alpha1.SharedResourcesNamespace})
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
			Name: sharedCMName, Namespace: clusterNamespace,
			Labels: map[string]string{ManagedCopyLabel: ManagedCopyLabelValue},
		}, Data: map[string]string{"user": "data"}}
		Expect(c.Create(ctx, cm)).To(Succeed())
		Expect(r.copySharedExtraManifests(ctx, testLogger, ci)).To(MatchError(ContainSubstring("name collision")))
		existing := &corev1.ConfigMap{}
		Expect(c.Get(ctx, clusterKey, existing)).To(Succeed())
		Expect(existing.Data).To(Equal(cm.Data))
		Expect(existing.OwnerReferences).To(BeEmpty())
	})

	It("fails when the shared ConfigMap does not exist", func() {
		ci := newClusterInstance(v1alpha1.ExtraManifestReference{
			Name:      sharedCMName,
			Namespace: v1alpha1.SharedResourcesNamespace,
		})
		err := r.copySharedExtraManifests(ctx, testLogger, ci)
		Expect(err).To(HaveOccurred())
	})

	It("does not copy or mutate local references", func() {
		ci := newClusterInstance(
			v1alpha1.ExtraManifestReference{Name: "local-implicit"},
			v1alpha1.ExtraManifestReference{Name: "local-explicit", Namespace: clusterNamespace},
		)
		Expect(r.copySharedExtraManifests(ctx, testLogger, ci)).To(Succeed())

		Expect(ci.Spec.ExtraManifestsRefs[0].Namespace).To(BeEmpty())
		Expect(ci.Spec.ExtraManifestsRefs[1].Namespace).To(Equal(clusterNamespace))

		// No copies were created for local references.
		cm := &corev1.ConfigMap{}
		Expect(c.Get(ctx, types.NamespacedName{Name: "local-explicit", Namespace: clusterNamespace}, cm)).ToNot(Succeed())
	})

	It("does not overwrite a newer synchronized copy with a stale source snapshot", func() {
		createSharedCM(map[string]string{"foo": "old"})
		ci := newClusterInstance(v1alpha1.ExtraManifestReference{
			Name:      sharedCMName,
			Namespace: v1alpha1.SharedResourcesNamespace,
		})
		Expect(r.copySharedExtraManifests(ctx, testLogger, ci)).To(Succeed())

		copy := &corev1.ConfigMap{}
		Expect(c.Get(ctx, clusterKey, copy)).To(Succeed())
		recordedVersion, err := strconv.ParseUint(copy.Annotations[SourceResourceVersionAnnotation], 10, 64)
		Expect(err).ToNot(HaveOccurred())
		newerVersion := strconv.FormatUint(recordedVersion+1000, 10)
		copy.Annotations[SourceResourceVersionAnnotation] = newerVersion
		copy.Data = map[string]string{"foo": "newer"}
		Expect(c.Update(ctx, copy)).To(Succeed())

		source := &corev1.ConfigMap{}
		Expect(c.Get(ctx, types.NamespacedName{Name: sharedCMName, Namespace: v1alpha1.SharedResourcesNamespace}, source)).To(Succeed())
		source.Data = map[string]string{"foo": "stale"}
		Expect(c.Update(ctx, source)).To(Succeed())

		Expect(r.copySharedExtraManifests(ctx, testLogger, ci)).To(Succeed())

		Expect(c.Get(ctx, clusterKey, copy)).To(Succeed())
		Expect(copy.Data).To(HaveKeyWithValue("foo", "newer"))
		Expect(copy.Annotations).To(HaveKeyWithValue(SourceResourceVersionAnnotation, newerVersion))
	})
})

var _ = Describe("isSourceVersionAtLeast", func() {
	DescribeTable("compares source and copy resource versions",
		func(sourceRV, copyRV string, expected bool) {
			Expect(isSourceVersionAtLeast(sourceRV, copyRV)).To(Equal(expected))
		},
		Entry("no recorded copy version", "10", "", true),
		Entry("source older than copy", "10", "20", false),
		Entry("source newer than copy", "20", "10", true),
		Entry("equal versions", "20", "20", true),
		Entry("unparseable versions", "not-a-number", "20", true),
	)
})
