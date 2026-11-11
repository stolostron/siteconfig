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

	"github.com/onsi/gomega"
	"github.com/stolostron/siteconfig/api/v1alpha1"
)

func TestSharedManifestSync(t *testing.T) {
	for _, scenario := range []string{"fanout", "metadata-only", "unmanaged", "wrong-owner", "wrong-source", "missing-copy", "missing-source", "retry", "aggregate", "drift", "hand-edit-only", "staged-rollout", "cleanup"} {
		t.Run(scenario, func(t *testing.T) {
			g := gomega.NewWithT(t)
			ctx := context.Background()
			scheme := runtime.NewScheme()
			g.Expect(corev1.AddToScheme(scheme)).To(gomega.Succeed())
			g.Expect(v1alpha1.AddToScheme(scheme)).To(gomega.Succeed())
			source := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "common", Namespace: v1alpha1.SharedResourcesNamespace}, Data: map[string]string{"manifest": "new"}, BinaryData: map[string][]byte{"binary": {1, 2}}}
			instances := []*v1alpha1.ClusterInstance{}
			objects := []client.Object{source}
			for _, name := range []string{"a", "b"} {
				ci := &v1alpha1.ClusterInstance{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: name, UID: types.UID(name), Generation: 3}, Spec: v1alpha1.ClusterInstanceSpec{ClusterName: name, ExtraManifestsRefs: []v1alpha1.ExtraManifestReference{{Name: "common", Namespace: v1alpha1.SharedResourcesNamespace}}}, Status: v1alpha1.ClusterInstanceStatus{ObservedGeneration: 3, Conditions: []metav1.Condition{{Type: string(v1alpha1.ClusterProvisioned), Status: metav1.ConditionTrue, Reason: string(v1alpha1.Completed)}}}}
				instances = append(instances, ci)
				objects = append(objects, ci)
			}
			failUpdate := false
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithStatusSubresource(&v1alpha1.ClusterInstance{}).
				WithIndex(&v1alpha1.ClusterInstance{}, SharedExtraManifestRefIndexKey, sharedManifestNames).WithInterceptorFuncs(interceptor.Funcs{Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				if failUpdate && (obj.GetNamespace() == "a" || scenario == "aggregate") {
					return fmt.Errorf("injected write failure in %s", obj.GetNamespace())
				}
				return c.Update(ctx, obj, opts...)
			}}).Build()
			copier := &ClusterInstanceReconciler{Client: c, Scheme: scheme, Log: zap.NewNop()}
			for _, ci := range instances {
				g.Expect(copier.copySharedExtraManifests(ctx, zap.NewNop(), ci)).To(gomega.Succeed())
			}
			getCopy := func(ns string) *corev1.ConfigMap {
				cm := &corev1.ConfigMap{}
				g.Expect(c.Get(ctx, client.ObjectKey{Name: "common", Namespace: ns}, cm)).To(gomega.Succeed())
				return cm
			}
			original := getCopy("a")
			g.Expect(c.Get(ctx, client.ObjectKeyFromObject(source), source)).To(gomega.Succeed())
			source.Data = map[string]string{"manifest": "updated"}
			source.BinaryData = map[string][]byte{"binary": {3}}
			if scenario == "metadata-only" {
				source.Data = original.Data
				source.BinaryData = original.BinaryData
				source.Annotations = map[string]string{"note": "changed"}
			}
			if scenario != "hand-edit-only" {
				g.Expect(c.Update(ctx, source)).To(gomega.Succeed())
			} else {
				source.Data = original.Data
				source.BinaryData = original.BinaryData
			}
			switch scenario {
			case "unmanaged", "wrong-owner", "wrong-source":
				cm := getCopy("a")
				if scenario == "unmanaged" {
					cm.Labels = nil
				}
				if scenario == "wrong-owner" {
					cm.OwnerReferences[0].UID = "someone-else"
				}
				if scenario == "wrong-source" {
					cm.Annotations[SourceNamespaceAnnotation] = "other"
				}
				g.Expect(c.Update(ctx, cm)).To(gomega.Succeed())
			case "missing-copy":
				g.Expect(c.Delete(ctx, original)).To(gomega.Succeed())
			case "missing-source":
				g.Expect(c.Delete(ctx, source)).To(gomega.Succeed())
			case "retry", "aggregate":
				failUpdate = true
			case "drift":
				cm := getCopy("a")
				cm.Data = map[string]string{"manifest": "hand-edited"}
				cm.Annotations[SourceResourceVersionAnnotation] = source.ResourceVersion
				g.Expect(c.Update(ctx, cm)).To(gomega.Succeed())
			case "hand-edit-only":
				// A later source reconciliation overwrites unsupported manual content edits.
				// This does not assert a watch or prompt repair of copy edits.
				cm := getCopy("a")
				cm.Data = map[string]string{"manifest": "hand-edited"}
				g.Expect(c.Update(ctx, cm)).To(gomega.Succeed())
			case "staged-rollout", "cleanup":
				ci := instances[0]
				ci.Spec.ExtraManifestsRefs = []v1alpha1.ExtraManifestReference{{Name: "v2", Namespace: v1alpha1.SharedResourcesNamespace}}
				g.Expect(c.Update(ctx, ci)).To(gomega.Succeed())
				v2 := source.DeepCopy()
				v2.Name = "v2"
				v2.ResourceVersion = ""
				v2.UID = ""
				g.Expect(c.Create(ctx, v2)).To(gomega.Succeed())
				g.Expect(copier.copySharedExtraManifests(ctx, zap.NewNop(), ci)).To(gomega.Succeed())
			}
			recorder := events.NewFakeRecorder(20)
			syncer := &SharedManifestSync{Client: c, Log: zap.NewNop(), Scheme: scheme, Recorder: recorder}
			_, err := syncer.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(source)})
			if scenario == "aggregate" {
				g.Expect(err).To(gomega.MatchError(gomega.And(gomega.ContainSubstring("failure in a"), gomega.ContainSubstring("failure in b"))))
				return
			}
			if scenario == "retry" {
				g.Expect(err).To(gomega.HaveOccurred())
				g.Expect(getCopy("b").Data).To(gomega.Equal(source.Data))
				failUpdate = false
				_, err = syncer.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(source)})
			}
			g.Expect(err).NotTo(gomega.HaveOccurred())
			switch scenario {
			case "missing-copy":
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(original), &corev1.ConfigMap{})).To(gomega.MatchError(gomega.ContainSubstring("not found")))
			case "unmanaged", "wrong-owner", "wrong-source", "missing-source", "staged-rollout":
				if scenario != "staged-rollout" {
					expected := "SharedExtraManifestNotManaged"
					if scenario == "missing-source" {
						expected = "SharedExtraManifestDeleted"
					}
					g.Expect(recorder.Events).To(gomega.Receive(gomega.ContainSubstring(expected)))
				}
				g.Expect(getCopy("a").Data).To(gomega.Equal(original.Data))
			case "metadata-only":
				g.Expect(getCopy("a").ResourceVersion).To(gomega.Equal(original.ResourceVersion))
			case "hand-edit-only":
				// The hand-edited copy is repaired back to the source content even though the
				// source itself never changed, and the untouched copy keeps matching the source.
				g.Expect(getCopy("a").Data).To(gomega.Equal(source.Data))
				g.Expect(getCopy("b").Data).To(gomega.Equal(source.Data))
			case "cleanup":
				// A copy controlled by another instance and a user ConfigMap must survive the sweep.
				foreign := original.DeepCopy()
				foreign.Name = "foreign"
				foreign.ResourceVersion = ""
				foreign.UID = ""
				foreign.OwnerReferences[0].UID = "other"
				g.Expect(c.Create(ctx, foreign)).To(gomega.Succeed())
				local := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "local", Namespace: "a"}}
				g.Expect(c.Create(ctx, local)).To(gomega.Succeed())
				g.Expect(copier.cleanupSharedExtraManifests(ctx, instances[0])).To(gomega.Succeed())
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(original), &corev1.ConfigMap{})).To(gomega.MatchError(gomega.ContainSubstring("not found")))
				for _, name := range []string{"v2", "foreign", "local"} {
					g.Expect(c.Get(ctx, client.ObjectKey{Name: name, Namespace: "a"}, &corev1.ConfigMap{})).To(gomega.Succeed())
				}
				g.Expect(getCopy("b").Data).To(gomega.Equal(source.Data))
			default:
				for _, ns := range []string{"a", "b"} {
					cm := getCopy(ns)
					g.Expect(cm.Data).To(gomega.Equal(source.Data))
					g.Expect(cm.BinaryData).To(gomega.Equal(source.BinaryData))
					g.Expect(cm.Annotations[InitialResourceVersionAnnotation]).To(gomega.Equal(original.Annotations[InitialResourceVersionAnnotation]))
					g.Expect(cm.Annotations[SourceResourceVersionAnnotation]).To(gomega.Equal(source.ResourceVersion))
				}
			}
			// Sync does not change the ClusterInstance spec/status or trigger re-render by changing generation.
			for _, ci := range instances {
				actual := &v1alpha1.ClusterInstance{}
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(ci), actual)).To(gomega.Succeed())
				g.Expect(actual.Spec).To(gomega.Equal(ci.Spec))
				g.Expect(actual.Status).To(gomega.Equal(ci.Status))
			}
		})
	}
}

// TestSharedCopyReferenceReordering verifies that changing only the order of extraManifestsRefs is a
// no-op for the managed copies: no copy is recreated and none is garbage-collected. The copier is
// name-keyed and cleanup uses an order-independent referenced set, so reordering must not churn copies.
func TestSharedCopyReferenceReordering(t *testing.T) {
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

	// Reorder the references and re-run the copy + cleanup path.
	ci.Spec.ExtraManifestsRefs = []v1alpha1.ExtraManifestReference{{Name: "b", Namespace: v1alpha1.SharedResourcesNamespace}, {Name: "a", Namespace: v1alpha1.SharedResourcesNamespace}}
	g.Expect(c.Update(ctx, ci)).To(gomega.Succeed())
	g.Expect(r.copySharedExtraManifests(ctx, zap.NewNop(), ci)).To(gomega.Succeed())
	g.Expect(r.cleanupSharedExtraManifests(ctx, ci)).To(gomega.Succeed())

	aAfter, bAfter := getCopy("a"), getCopy("b")
	g.Expect(aAfter.UID).To(gomega.Equal(aBefore.UID))
	g.Expect(bAfter.UID).To(gomega.Equal(bBefore.UID))
	g.Expect(aAfter.ResourceVersion).To(gomega.Equal(aBefore.ResourceVersion))
	g.Expect(bAfter.ResourceVersion).To(gomega.Equal(bBefore.ResourceVersion))
	g.Expect(sharedManifestNames(ci)).To(gomega.ConsistOf("a", "b"))
}
