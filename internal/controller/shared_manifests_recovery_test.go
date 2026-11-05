/*
Copyright 2025.

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

	"github.com/onsi/gomega"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/stolostron/siteconfig/api/v1alpha1"
)

func TestMissingSharedCopyRecovery(t *testing.T) {
	for _, scenario := range []string{"startup", "existing", "unmanaged", "paused", "no-shared-refs", "missing-source", "restore"} {
		t.Run(scenario, func(t *testing.T) {
			g := gomega.NewWithT(t)
			ctx := context.Background()
			scheme := runtime.NewScheme()
			g.Expect(corev1.AddToScheme(scheme)).To(gomega.Succeed())
			g.Expect(v1alpha1.AddToScheme(scheme)).To(gomega.Succeed())
			ci := &v1alpha1.ClusterInstance{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "cluster", UID: types.UID("cluster-uid"), Generation: 3, Finalizers: []string{clusterInstanceFinalizer}, Annotations: map[string]string{veleroRestoreStatusAnnotation: "true"}}, Spec: v1alpha1.ClusterInstanceSpec{ClusterName: "cluster", ExtraManifestsRefs: []v1alpha1.ExtraManifestReference{{Name: "common", Namespace: v1alpha1.SharedResourcesNamespace}}}, Status: v1alpha1.ClusterInstanceStatus{ObservedGeneration: 3}}
			source := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "common", Namespace: v1alpha1.SharedResourcesNamespace}, Data: map[string]string{"payload": "source"}}
			rendered := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "rendered", Namespace: "cluster"}, Data: map[string]string{"payload": "unchanged"}}
			if scenario == "paused" {
				ci.Annotations[v1alpha1.PausedAnnotation] = "test"
			}
			if scenario == "no-shared-refs" {
				ci.Spec.ExtraManifestsRefs = nil
			}
			if scenario == "restore" {
				ci.Labels = map[string]string{veleroRestoreNameLabel: "restore-test"}
				ci.Annotations[veleroRestoreHandledAnnotation] = "restore-test"
			}
			objects := []client.Object{ci, rendered}
			if scenario != "missing-source" {
				objects = append(objects, source)
			}
			if scenario == "existing" || scenario == "unmanaged" {
				cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "common", Namespace: "cluster"}, Data: map[string]string{"payload": "leave intact"}}
				if scenario == "existing" {
					cm.Labels = map[string]string{ManagedCopyLabel: ManagedCopyLabelValue}
				}
				objects = append(objects, cm)
			}
			reads, writes := 0, 0
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithStatusSubresource(ci).WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, ok := obj.(*corev1.ConfigMap); ok {
						reads++
					}
					return c.Get(ctx, key, obj, opts...)
				},
				Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					writes++
					return c.Create(ctx, obj, opts...)
				},
				Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
					writes++
					return c.Update(ctx, obj, opts...)
				},
				Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
					writes++
					return c.Patch(ctx, obj, patch, opts...)
				},
			}).Build()
			before := &corev1.ConfigMap{}
			g.Expect(c.Get(ctx, client.ObjectKeyFromObject(rendered), before)).To(gomega.Succeed())
			reads = 0
			r := &ClusterInstanceReconciler{Client: c, Scheme: scheme, Log: zap.NewNop()}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(ci)})
			if scenario == "missing-source" {
				g.Expect(err).To(gomega.HaveOccurred())
				return
			}
			g.Expect(err).NotTo(gomega.HaveOccurred())
			if scenario == "no-shared-refs" || scenario == "paused" {
				g.Expect(reads).To(gomega.BeZero())
				g.Expect(writes).To(gomega.BeZero())
				return
			}
			if scenario == "existing" || scenario == "unmanaged" {
				g.Expect(writes).To(gomega.BeZero())
			}
			actual := &v1alpha1.ClusterInstance{}
			g.Expect(c.Get(ctx, client.ObjectKeyFromObject(ci), actual)).To(gomega.Succeed())
			g.Expect(actual.Status).To(gomega.Equal(ci.Status))
			g.Expect(actual.Generation).To(gomega.Equal(ci.Generation))
			g.Expect(actual.Spec).To(gomega.Equal(ci.Spec))
			after := &corev1.ConfigMap{}
			g.Expect(c.Get(ctx, client.ObjectKeyFromObject(rendered), after)).To(gomega.Succeed())
			g.Expect(after).To(gomega.Equal(before))
			copy := &corev1.ConfigMap{}
			g.Expect(c.Get(ctx, client.ObjectKey{Name: "common", Namespace: "cluster"}, copy)).To(gomega.Succeed())
			if scenario == "restore" {
				// Hub-restore guarantee: recovering a missing shared copy must not re-render or
				// otherwise touch already-rendered manifests, so their resourceVersion is preserved
				// and GitOps consumers see no spurious change.
				g.Expect(after.ResourceVersion).To(gomega.Equal(before.ResourceVersion))
			}
			if scenario == "startup" || scenario == "restore" {
				g.Expect(copy.Data).To(gomega.Equal(source.Data))
				g.Expect(metav1.IsControlledBy(copy, ci)).To(gomega.BeTrue())
				g.Expect(copy.Labels).To(gomega.Equal(map[string]string{ManagedCopyLabel: ManagedCopyLabelValue}))
				g.Expect(copy.Annotations).To(gomega.HaveKey(InitialResourceVersionAnnotation))
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(source), source)).To(gomega.Succeed())
				g.Expect(source.Labels).To(gomega.HaveKeyWithValue(acmBackupLabel, acmBackupLabelValue))
			} else {
				g.Expect(copy.Data["payload"]).To(gomega.Equal("leave intact"))
			}
		})
	}
}

func TestSharedCopyEventMapping(t *testing.T) {
	g := gomega.NewWithT(t)
	ctx := context.Background()
	controller := true
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "common", Namespace: "cluster", Labels: map[string]string{ManagedCopyLabel: ManagedCopyLabelValue}, Annotations: map[string]string{SourceNamespaceAnnotation: v1alpha1.SharedResourcesNamespace}, OwnerReferences: []metav1.OwnerReference{{APIVersion: v1alpha1.GroupVersion.String(), Kind: v1alpha1.ClusterInstanceKind, Name: "ci", UID: types.UID("uid"), Controller: &controller}}}}
	g.Expect(sharedCopyOwnerRequests(ctx, cm)).To(gomega.Equal([]ctrl.Request{{NamespacedName: client.ObjectKey{Namespace: "cluster", Name: "ci"}}}))
	g.Expect(sharedCopySourceRequests(ctx, cm)).To(gomega.Equal([]ctrl.Request{{NamespacedName: client.ObjectKey{Namespace: v1alpha1.SharedResourcesNamespace, Name: "common"}}}))
	cm.Labels = nil
	g.Expect(sharedCopyOwnerRequests(ctx, cm)).To(gomega.BeEmpty())
	g.Expect(sharedCopySourceRequests(ctx, cm)).To(gomega.BeEmpty())
}

func TestSourceBackupLabelFailure(t *testing.T) {
	g := gomega.NewWithT(t)
	scheme := runtime.NewScheme()
	g.Expect(corev1.AddToScheme(scheme)).To(gomega.Succeed())
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "source", Namespace: v1alpha1.SharedResourcesNamespace}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cm).WithInterceptorFuncs(interceptor.Funcs{Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
		return fmt.Errorf("backup label denied")
	}}).Build()
	g.Expect(ensureSharedSourceBackup(context.Background(), c, cm)).To(gomega.MatchError(gomega.ContainSubstring("backup label denied")))
}
