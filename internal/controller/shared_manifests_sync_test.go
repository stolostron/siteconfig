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
	"fmt"
	"testing"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/stolostron/siteconfig/api/v1alpha1"
)

// A migrated copy can carry a resourceVersion from an unrelated API server.
// Content comparison must still propagate the target source. The annotation is
// provenance and does not order the two snapshots.
func TestManualMigratedCopyRecovery(t *testing.T) {
	g := gomega.NewWithT(t)
	ctx := context.Background()
	scheme := runtime.NewScheme()
	g.Expect(corev1.AddToScheme(scheme)).To(gomega.Succeed())
	g.Expect(v1alpha1.AddToScheme(scheme)).To(gomega.Succeed())
	ci := &v1alpha1.ClusterInstance{ObjectMeta: metav1.ObjectMeta{Name: "target", Namespace: "target", UID: "target-ci", Generation: 3},
		Spec:   v1alpha1.ClusterInstanceSpec{ExtraManifestsRefs: []v1alpha1.ExtraManifestReference{{Name: "common", Namespace: v1alpha1.SharedResourcesNamespace}}},
		Status: v1alpha1.ClusterInstanceStatus{ObservedGeneration: 3}}
	source := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "common", Namespace: v1alpha1.SharedResourcesNamespace, ResourceVersion: "10"},
		Data: map[string]string{"payload": "target"}, BinaryData: map[string][]byte{"binary": {1, 2}}}
	copy := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "common", Namespace: ci.Namespace,
		Labels: map[string]string{v1alpha1.ManagedCopyLabel: v1alpha1.ManagedCopyLabelValue},
		Annotations: map[string]string{v1alpha1.SourceNamespaceAnnotation: v1alpha1.SharedResourcesNamespace,
			v1alpha1.SourceResourceVersionAnnotation: "999999999", v1alpha1.InitialResourceVersionAnnotation: "999999998"}},
		Data: map[string]string{"payload": "old-hub"}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ci, source, copy).WithStatusSubresource(ci).Build()
	syncer := &SharedManifestSync{Client: c, Recorder: events.NewFakeRecorder(10)}
	key := client.ObjectKeyFromObject(copy)
	g.Expect(c.Get(ctx, key, copy)).To(gomega.Succeed())
	// A numerically higher old-hub version must not block content synchronization.
	copy.OwnerReferences = []metav1.OwnerReference{*metav1.NewControllerRef(ci, v1alpha1.GroupVersion.WithKind("ClusterInstance"))}
	g.Expect(c.Update(ctx, copy)).To(gomega.Succeed())
	g.Expect(syncer.propagate(ctx, zap.NewNop(), ci, source)).To(gomega.Succeed())
	g.Expect(c.Get(ctx, client.ObjectKeyFromObject(source), source)).To(gomega.Succeed())
	g.Expect(c.Get(ctx, key, copy)).To(gomega.Succeed())
	g.Expect(copy.Data).To(gomega.Equal(source.Data))
	g.Expect(copy.BinaryData).To(gomega.Equal(source.BinaryData))
	g.Expect(copy.Annotations[v1alpha1.SourceResourceVersionAnnotation]).To(gomega.Equal(source.ResourceVersion))
	g.Expect(copy.Annotations[v1alpha1.InitialResourceVersionAnnotation]).To(gomega.Equal("999999998"))
	source.Data = map[string]string{"payload": "target-update"}
	source.BinaryData = map[string][]byte{"binary": {3, 4}}
	g.Expect(c.Update(ctx, source)).To(gomega.Succeed())
	g.Expect(syncer.propagate(ctx, zap.NewNop(), ci, source)).To(gomega.Succeed())
	g.Expect(c.Get(ctx, key, copy)).To(gomega.Succeed())
	g.Expect(copy.Data).To(gomega.Equal(source.Data))
	g.Expect(copy.BinaryData).To(gomega.Equal(source.BinaryData))
	g.Expect(copy.Annotations[v1alpha1.SourceResourceVersionAnnotation]).To(gomega.Equal(source.ResourceVersion))
	g.Expect(copy.Annotations[v1alpha1.InitialResourceVersionAnnotation]).To(gomega.Equal("999999998"))
	actualCI := &v1alpha1.ClusterInstance{}
	g.Expect(c.Get(ctx, client.ObjectKeyFromObject(ci), actualCI)).To(gomega.Succeed())
	g.Expect(actualCI.Spec).To(gomega.Equal(ci.Spec))
	g.Expect(actualCI.Status).To(gomega.Equal(ci.Status))
}

func TestSharedManifestSyncDoesNotAdoptOwnerlessCopy(t *testing.T) {
	g := gomega.NewWithT(t)
	ctx := context.Background()
	scheme := runtime.NewScheme()
	g.Expect(corev1.AddToScheme(scheme)).To(gomega.Succeed())
	g.Expect(v1alpha1.AddToScheme(scheme)).To(gomega.Succeed())
	ci := &v1alpha1.ClusterInstance{ObjectMeta: metav1.ObjectMeta{Name: "target", Namespace: "target", UID: "target-ci"}}
	source := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "common", Namespace: v1alpha1.SharedResourcesNamespace, ResourceVersion: "10"},
		Data: map[string]string{"payload": "new"}}
	copy := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "common", Namespace: ci.Namespace,
		Labels: map[string]string{v1alpha1.ManagedCopyLabel: v1alpha1.ManagedCopyLabelValue},
		Annotations: map[string]string{
			v1alpha1.SourceNamespaceAnnotation:       v1alpha1.SharedResourcesNamespace,
			v1alpha1.SourceResourceVersionAnnotation: "999999",
		}},
		Data: map[string]string{"payload": "old-hub"}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ci, source, copy).WithStatusSubresource(ci).Build()
	recorder := events.NewFakeRecorder(10)
	syncer := &SharedManifestSync{Client: c, Recorder: recorder}
	key := client.ObjectKeyFromObject(copy)

	g.Expect(syncer.propagate(ctx, zap.NewNop(), ci, source)).To(gomega.Succeed())
	g.Expect(c.Get(ctx, key, copy)).To(gomega.Succeed())
	g.Expect(copy.Data).To(gomega.Equal(map[string]string{"payload": "old-hub"}))
	g.Expect(copy.OwnerReferences).To(gomega.BeEmpty())
	g.Expect(copy.Annotations[v1alpha1.SourceResourceVersionAnnotation]).To(gomega.Equal("999999"))
	g.Expect(len(recorder.Events)).To(gomega.Equal(0))
}

type syncFixture struct {
	ctx        context.Context
	c          client.Client
	source     *corev1.ConfigMap
	instances  []*v1alpha1.ClusterInstance
	copier     *ClusterInstanceReconciler
	syncer     *SharedManifestSync
	recorder   *events.FakeRecorder
	failA      *bool
	failAll    *bool
	original   *corev1.ConfigMap
	sourceKey  client.ObjectKey
	clusterKey func(string) client.ObjectKey
}

func newSyncFixture() *syncFixture {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	gomega.Expect(corev1.AddToScheme(scheme)).To(gomega.Succeed())
	gomega.Expect(v1alpha1.AddToScheme(scheme)).To(gomega.Succeed())

	source := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "common", Namespace: v1alpha1.SharedResourcesNamespace},
		Data:       map[string]string{"manifest": "new"},
		BinaryData: map[string][]byte{"binary": {1, 2}},
	}
	instances := make([]*v1alpha1.ClusterInstance, 0, 2)
	objects := []client.Object{source}
	for _, name := range []string{"a", "b"} {
		ci := &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: name, UID: types.UID(name), Generation: 3},
			Spec: v1alpha1.ClusterInstanceSpec{
				ClusterName:        name,
				ExtraManifestsRefs: []v1alpha1.ExtraManifestReference{{Name: source.Name, Namespace: v1alpha1.SharedResourcesNamespace}},
			},
			Status: v1alpha1.ClusterInstanceStatus{
				ObservedGeneration: 3,
				Conditions: []metav1.Condition{{
					Type:   string(v1alpha1.ClusterProvisioned),
					Status: metav1.ConditionTrue,
					Reason: string(v1alpha1.Completed),
				}},
			},
		}
		instances = append(instances, ci)
		objects = append(objects, ci)
	}

	failA := false
	failAll := false
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		WithStatusSubresource(&v1alpha1.ClusterInstance{}).
		WithIndex(&v1alpha1.ClusterInstance{}, SharedExtraManifestRefIndexKey, sharedManifestNames).
		WithInterceptorFuncs(interceptor.Funcs{
			Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				if failAll || (failA && obj.GetNamespace() == "a") {
					return fmt.Errorf("injected write failure in %s", obj.GetNamespace())
				}
				return c.Update(ctx, obj, opts...)
			},
		}).
		Build()

	copier := &ClusterInstanceReconciler{Client: c, Scheme: scheme, Log: zap.NewNop()}
	for _, ci := range instances {
		gomega.Expect(copier.copySharedExtraManifests(ctx, zap.NewNop(), ci)).To(gomega.Succeed())
	}

	original := &corev1.ConfigMap{}
	sourceKey := client.ObjectKeyFromObject(source)
	gomega.Expect(c.Get(ctx, sourceKey, source)).To(gomega.Succeed())
	gomega.Expect(c.Get(ctx, client.ObjectKey{Name: "common", Namespace: "a"}, original)).To(gomega.Succeed())
	recorder := events.NewFakeRecorder(20)

	return &syncFixture{
		ctx:       ctx,
		c:         c,
		source:    source,
		instances: instances,
		copier:    copier,
		syncer: &SharedManifestSync{
			Client:   c,
			Log:      zap.NewNop(),
			Scheme:   scheme,
			Recorder: recorder,
		},
		recorder:  recorder,
		failA:     &failA,
		failAll:   &failAll,
		original:  original,
		sourceKey: sourceKey,
		clusterKey: func(namespace string) client.ObjectKey {
			return client.ObjectKey{Name: "common", Namespace: namespace}
		},
	}
}

func (f *syncFixture) getCopy(namespace string) *corev1.ConfigMap {
	cm := &corev1.ConfigMap{}
	gomega.Expect(f.c.Get(f.ctx, f.clusterKey(namespace), cm)).To(gomega.Succeed())
	return cm
}

func (f *syncFixture) refreshSource() {
	gomega.Expect(f.c.Get(f.ctx, f.sourceKey, f.source)).To(gomega.Succeed())
}

func (f *syncFixture) updateSource() {
	f.source.Data = map[string]string{"manifest": "updated"}
	f.source.BinaryData = map[string][]byte{"binary": {3}}
	gomega.Expect(f.c.Update(f.ctx, f.source)).To(gomega.Succeed())
	f.refreshSource()
}

func (f *syncFixture) reconcile() error {
	_, err := f.syncer.Reconcile(f.ctx, ctrl.Request{NamespacedName: f.sourceKey})
	return err
}

func (f *syncFixture) expectCopyMatchesSource(namespace string) {
	cm := f.getCopy(namespace)
	gomega.Expect(cm.Data).To(gomega.Equal(f.source.Data))
	gomega.Expect(cm.BinaryData).To(gomega.Equal(f.source.BinaryData))
	gomega.Expect(cm.Annotations[v1alpha1.InitialResourceVersionAnnotation]).
		To(gomega.Equal(f.original.Annotations[v1alpha1.InitialResourceVersionAnnotation]))
	gomega.Expect(cm.Annotations[v1alpha1.SourceResourceVersionAnnotation]).
		To(gomega.Equal(f.source.ResourceVersion))
}

func (f *syncFixture) expectCopiesMatchSource() {
	for _, namespace := range []string{"a", "b"} {
		f.expectCopyMatchesSource(namespace)
	}
}

func (f *syncFixture) expectInstancesUnchanged() {
	for _, ci := range f.instances {
		actual := &v1alpha1.ClusterInstance{}
		gomega.Expect(f.c.Get(f.ctx, client.ObjectKeyFromObject(ci), actual)).To(gomega.Succeed())
		gomega.Expect(actual.Spec).To(gomega.Equal(ci.Spec))
		gomega.Expect(actual.Status).To(gomega.Equal(ci.Status))
	}
}

var _ = ginkgo.Describe("SharedManifestSync", func() {
	var f *syncFixture

	ginkgo.BeforeEach(func() {
		f = newSyncFixture()
	})

	ginkgo.AfterEach(func() {
		f.expectInstancesUnchanged()
	})

	ginkgo.It("propagates source content to every managed copy", func() {
		f.updateSource()
		gomega.Expect(f.reconcile()).To(gomega.Succeed())
		f.expectCopiesMatchSource()
	})

	ginkgo.It("does not write copies for metadata-only source updates", func() {
		f.source.Annotations = map[string]string{"note": "changed"}
		gomega.Expect(f.c.Update(f.ctx, f.source)).To(gomega.Succeed())
		f.refreshSource()
		before := f.getCopy("a")

		gomega.Expect(f.reconcile()).To(gomega.Succeed())

		after := f.getCopy("a")
		gomega.Expect(after.ResourceVersion).To(gomega.Equal(before.ResourceVersion))
	})

	ginkgo.It("skips a copy that is not labelled as managed", func() {
		cm := f.getCopy("a")
		cm.Labels = nil
		gomega.Expect(f.c.Update(f.ctx, cm)).To(gomega.Succeed())
		f.updateSource()

		gomega.Expect(f.reconcile()).To(gomega.Succeed())

		gomega.Expect(f.getCopy("a").Data).To(gomega.Equal(f.original.Data))
		gomega.Expect(f.recorder.Events).To(gomega.Receive(gomega.ContainSubstring("SharedExtraManifestNotManaged")))
		f.expectCopyMatchesSource("b")
	})

	ginkgo.It("skips a copy controlled by another object", func() {
		cm := f.getCopy("a")
		cm.OwnerReferences[0].UID = "someone-else"
		gomega.Expect(f.c.Update(f.ctx, cm)).To(gomega.Succeed())
		f.updateSource()

		gomega.Expect(f.reconcile()).To(gomega.Succeed())

		gomega.Expect(f.getCopy("a").Data).To(gomega.Equal(f.original.Data))
		gomega.Expect(f.recorder.Events).To(gomega.Receive(gomega.ContainSubstring("SharedExtraManifestNotManaged")))
		f.expectCopyMatchesSource("b")
	})

	ginkgo.It("propagates to an owned copy whose source-namespace annotation is wrong", func() {
		cm := f.getCopy("a")
		cm.Annotations[v1alpha1.SourceNamespaceAnnotation] = "other"
		gomega.Expect(f.c.Update(f.ctx, cm)).To(gomega.Succeed())
		f.updateSource()

		gomega.Expect(f.reconcile()).To(gomega.Succeed())

		f.expectCopiesMatchSource()
		gomega.Expect(f.getCopy("a").Annotations[v1alpha1.SourceNamespaceAnnotation]).To(gomega.Equal("other"))
		gomega.Expect(len(f.recorder.Events)).To(gomega.Equal(0))
	})

	ginkgo.It("does not recreate a missing copy", func() {
		gomega.Expect(f.c.Delete(f.ctx, f.original)).To(gomega.Succeed())
		f.updateSource()

		gomega.Expect(f.reconcile()).To(gomega.Succeed())

		gomega.Expect(f.c.Get(f.ctx, f.clusterKey("a"), &corev1.ConfigMap{})).
			To(gomega.MatchError(gomega.ContainSubstring("not found")))
		f.expectCopyMatchesSource("b")
	})

	ginkgo.It("warns and retains copies when the source is deleted", func() {
		gomega.Expect(f.c.Delete(f.ctx, f.source)).To(gomega.Succeed())
		before := f.getCopy("a")

		gomega.Expect(f.reconcile()).To(gomega.Succeed())

		gomega.Expect(f.getCopy("a").Data).To(gomega.Equal(before.Data))
		gomega.Expect(f.recorder.Events).To(gomega.Receive(gomega.ContainSubstring("SharedExtraManifestDeleted")))
	})

	ginkgo.It("retries an update failure on the next reconcile", func() {
		f.updateSource()
		*f.failA = true

		gomega.Expect(f.reconcile()).To(gomega.HaveOccurred())
		f.expectCopyMatchesSource("b")

		*f.failA = false
		gomega.Expect(f.reconcile()).To(gomega.Succeed())
		f.expectCopiesMatchSource()
	})

	ginkgo.It("aggregates update failures from multiple copies", func() {
		f.updateSource()
		*f.failAll = true

		err := f.reconcile()
		gomega.Expect(err).To(gomega.MatchError(gomega.And(
			gomega.ContainSubstring("failure in a"),
			gomega.ContainSubstring("failure in b"),
		)))
	})

	ginkgo.It("repairs a hand-edited copy even when the source is unchanged", func() {
		cm := f.getCopy("a")
		cm.Data = map[string]string{"manifest": "hand-edited"}
		gomega.Expect(f.c.Update(f.ctx, cm)).To(gomega.Succeed())

		gomega.Expect(f.reconcile()).To(gomega.Succeed())

		f.expectCopiesMatchSource()
	})

	ginkgo.It("does not update a copy after its reference moves to a new source", func() {
		ci := f.instances[0]
		ci.Spec.ExtraManifestsRefs = []v1alpha1.ExtraManifestReference{{Name: "v2", Namespace: v1alpha1.SharedResourcesNamespace}}
		gomega.Expect(f.c.Update(f.ctx, ci)).To(gomega.Succeed())

		v2 := f.source.DeepCopy()
		v2.Name = "v2"
		v2.ResourceVersion = ""
		v2.UID = ""
		gomega.Expect(f.c.Create(f.ctx, v2)).To(gomega.Succeed())
		gomega.Expect(f.copier.copySharedExtraManifests(f.ctx, zap.NewNop(), ci)).To(gomega.Succeed())

		gomega.Expect(f.reconcile()).To(gomega.Succeed())

		gomega.Expect(f.getCopy("a").Data).To(gomega.Equal(f.original.Data))
		gomega.Expect(len(f.recorder.Events)).To(gomega.Equal(0))
	})
})

// TestSharedCopyReconcileIdempotent verifies that running copy and cleanup again with the same
// references does not recreate or garbage-collect existing copies. The copier is name-keyed and
// cleanup uses a referenced set, so reference order is not significant.
func TestSharedCopyReconcileIdempotent(t *testing.T) {
	g := gomega.NewWithT(t)
	ctx := context.Background()
	scheme := runtime.NewScheme()
	g.Expect(corev1.AddToScheme(scheme)).To(gomega.Succeed())
	g.Expect(v1alpha1.AddToScheme(scheme)).To(gomega.Succeed())

	srcA := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "a", Namespace: v1alpha1.SharedResourcesNamespace}, Data: map[string]string{"k": "a"}}
	srcB := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "b", Namespace: v1alpha1.SharedResourcesNamespace}, Data: map[string]string{"k": "b"}}
	ci := &v1alpha1.ClusterInstance{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "cluster", UID: types.UID("ci")}, Spec: v1alpha1.ClusterInstanceSpec{ClusterName: "cluster", ExtraManifestsRefs: []v1alpha1.ExtraManifestReference{{Name: "a", Namespace: v1alpha1.SharedResourcesNamespace}, {Name: "b", Namespace: v1alpha1.SharedResourcesNamespace}}}}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(srcA, srcB, ci).Build()
	r := &ClusterInstanceReconciler{Client: c, Scheme: scheme, Log: zap.NewNop()}
	g.Expect(r.copySharedExtraManifests(ctx, zap.NewNop(), ci)).To(gomega.Succeed())
	g.Expect(r.cleanupSharedExtraManifests(ctx, ci)).To(gomega.Succeed())

	getCopy := func(name string) *corev1.ConfigMap {
		cm := &corev1.ConfigMap{}
		g.Expect(c.Get(ctx, client.ObjectKey{Name: name, Namespace: "cluster"}, cm)).To(gomega.Succeed())
		return cm
	}
	aBefore, bBefore := getCopy("a"), getCopy("b")

	// Re-run the same reconcile path. Existing copies must not be recreated or swept.
	g.Expect(r.copySharedExtraManifests(ctx, zap.NewNop(), ci)).To(gomega.Succeed())
	g.Expect(r.cleanupSharedExtraManifests(ctx, ci)).To(gomega.Succeed())

	aAfter, bAfter := getCopy("a"), getCopy("b")
	g.Expect(aAfter.UID).To(gomega.Equal(aBefore.UID))
	g.Expect(bAfter.UID).To(gomega.Equal(bBefore.UID))
	g.Expect(aAfter.ResourceVersion).To(gomega.Equal(aBefore.ResourceVersion))
	g.Expect(bAfter.ResourceVersion).To(gomega.Equal(bBefore.ResourceVersion))
}
