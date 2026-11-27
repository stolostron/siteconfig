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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

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
		Expect(copy.Labels).To(HaveKeyWithValue(v1alpha1.ManagedCopyLabel, v1alpha1.ManagedCopyLabelValue))
		Expect(copy.Annotations).To(HaveKeyWithValue(v1alpha1.SourceNamespaceAnnotation, v1alpha1.SharedResourcesNamespace))
		Expect(copy.Annotations).To(HaveKeyWithValue(v1alpha1.SourceResourceVersionAnnotation, source.ResourceVersion))
		Expect(copy.Annotations).To(HaveKeyWithValue(v1alpha1.InitialResourceVersionAnnotation, source.ResourceVersion))

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
		Expect(copy.Annotations).To(HaveKeyWithValue(v1alpha1.SourceResourceVersionAnnotation, source.ResourceVersion))
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
			Expect(copy.Labels).To(Equal(map[string]string{v1alpha1.ManagedCopyLabel: v1alpha1.ManagedCopyLabelValue}))
			Expect(copy.Annotations).NotTo(HaveKey("argocd.argoproj.io/tracking-id"))
			if phase == "creation" {
				initialVersion = source.ResourceVersion
			}
			Expect(copy.Annotations).To(Equal(map[string]string{v1alpha1.InitialResourceVersionAnnotation: initialVersion, v1alpha1.SourceResourceVersionAnnotation: source.ResourceVersion, v1alpha1.SourceNamespaceAnnotation: v1alpha1.SharedResourcesNamespace}))
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

	It("adopts an ownerless managed copy transferred by migration or restore", func() {
		source := createSharedCM(map[string]string{"foo": "bar"})
		ci := newClusterInstance(v1alpha1.ExtraManifestReference{Name: sharedCMName, Namespace: v1alpha1.SharedResourcesNamespace})
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
			Name:      sharedCMName,
			Namespace: clusterNamespace,
			Labels:    map[string]string{v1alpha1.ManagedCopyLabel: v1alpha1.ManagedCopyLabelValue},
			Annotations: map[string]string{
				v1alpha1.SourceNamespaceAnnotation:        "old-hub-source",
				v1alpha1.SourceResourceVersionAnnotation:  "999999",
				v1alpha1.InitialResourceVersionAnnotation: "999998",
			},
		}, Data: map[string]string{"user": "old-hub"}}
		Expect(c.Create(ctx, cm)).To(Succeed())

		Expect(r.copySharedExtraManifests(ctx, testLogger, ci)).To(Succeed())

		existing := &corev1.ConfigMap{}
		Expect(c.Get(ctx, clusterKey, existing)).To(Succeed())
		Expect(existing.Data).To(Equal(source.Data))
		Expect(existing.OwnerReferences).To(HaveLen(1))
		Expect(existing.OwnerReferences[0].UID).To(Equal(ci.UID))
		Expect(existing.Annotations).To(HaveKeyWithValue(v1alpha1.SourceNamespaceAnnotation, v1alpha1.SharedResourcesNamespace))
		Expect(existing.Annotations).To(HaveKeyWithValue(v1alpha1.SourceResourceVersionAnnotation, source.ResourceVersion))
		Expect(existing.Annotations).To(HaveKeyWithValue(v1alpha1.InitialResourceVersionAnnotation, source.ResourceVersion))
	})

	It("rejects a labeled ConfigMap owned by another object", func() {
		createSharedCM(map[string]string{"foo": "bar"})
		ci := newClusterInstance(v1alpha1.ExtraManifestReference{Name: sharedCMName, Namespace: v1alpha1.SharedResourcesNamespace})
		controller := true
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
			Name:      sharedCMName,
			Namespace: clusterNamespace,
			Labels:    map[string]string{v1alpha1.ManagedCopyLabel: v1alpha1.ManagedCopyLabelValue},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: v1alpha1.GroupVersion.String(),
				Kind:       v1alpha1.ClusterInstanceKind,
				Name:       "another-cluster",
				UID:        "another-uid",
				Controller: &controller,
			}},
		}, Data: map[string]string{"user": "data"}}
		Expect(c.Create(ctx, cm)).To(Succeed())
		Expect(r.copySharedExtraManifests(ctx, testLogger, ci)).To(MatchError(ContainSubstring("name collision")))
		existing := &corev1.ConfigMap{}
		Expect(c.Get(ctx, clusterKey, existing)).To(Succeed())
		Expect(existing.Data).To(Equal(cm.Data))
		Expect(existing.OwnerReferences).To(HaveLen(1))
		Expect(existing.OwnerReferences[0].UID).To(Equal(types.UID("another-uid")))
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

	It("re-reads the source while copying so a stale snapshot cannot overwrite the copy", func() {
		reads := 0
		c = fakeclient.NewClientBuilder().WithScheme(scheme.Scheme).WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if err := c.Get(ctx, key, obj, opts...); err != nil {
					return err
				}
				cm, ok := obj.(*corev1.ConfigMap)
				if !ok || key.Name != sharedCMName || key.Namespace != v1alpha1.SharedResourcesNamespace {
					return nil
				}
				reads++
				// The backup read can observe a stale object. The write must use a later read.
				if reads == 1 {
					cm.Data = map[string]string{"foo": "stale"}
					cm.ResourceVersion = "1"
				}
				return nil
			},
		}).Build()
		r = &ClusterInstanceReconciler{Client: c, Scheme: scheme.Scheme, Log: testLogger}
		source := createSharedCM(map[string]string{"foo": "current"})

		ci := newClusterInstance(v1alpha1.ExtraManifestReference{
			Name:      sharedCMName,
			Namespace: v1alpha1.SharedResourcesNamespace,
		})
		Expect(r.copySharedExtraManifests(ctx, testLogger, ci)).To(Succeed())

		copy := &corev1.ConfigMap{}
		Expect(c.Get(ctx, clusterKey, copy)).To(Succeed())
		Expect(copy.Data).To(Equal(map[string]string{"foo": "current"}))
		Expect(copy.Annotations).To(HaveKeyWithValue(v1alpha1.SourceResourceVersionAnnotation, source.ResourceVersion))
		Expect(reads).To(BeNumerically(">", 1))
	})

	It("cleans up obsolete copies but preserves foreign and user ConfigMaps", func() {
		createSharedCM(map[string]string{"foo": "initial"})
		v2 := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "v2", Namespace: v1alpha1.SharedResourcesNamespace},
			Data:       map[string]string{"foo": "v2"},
		}
		Expect(c.Create(ctx, v2)).To(Succeed())

		ci := newClusterInstance(v1alpha1.ExtraManifestReference{Name: sharedCMName, Namespace: v1alpha1.SharedResourcesNamespace})
		Expect(r.copySharedExtraManifests(ctx, testLogger, ci)).To(Succeed())

		original := &corev1.ConfigMap{}
		Expect(c.Get(ctx, clusterKey, original)).To(Succeed())

		ci.Spec.ExtraManifestsRefs = []v1alpha1.ExtraManifestReference{{Name: v2.Name, Namespace: v2.Namespace}}
		Expect(r.copySharedExtraManifests(ctx, testLogger, ci)).To(Succeed())

		foreign := original.DeepCopy()
		foreign.Name = "foreign"
		foreign.ResourceVersion = ""
		foreign.UID = ""
		foreign.OwnerReferences[0].UID = "other"
		Expect(c.Create(ctx, foreign)).To(Succeed())

		local := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "local", Namespace: clusterNamespace}}
		Expect(c.Create(ctx, local)).To(Succeed())

		Expect(r.cleanupSharedExtraManifests(ctx, ci)).To(Succeed())

		Expect(c.Get(ctx, clusterKey, &corev1.ConfigMap{})).To(MatchError(ContainSubstring("not found")))
		for _, name := range []string{v2.Name, "foreign", "local"} {
			Expect(c.Get(ctx, client.ObjectKey{Name: name, Namespace: clusterNamespace}, &corev1.ConfigMap{})).To(Succeed())
		}
	})
})
