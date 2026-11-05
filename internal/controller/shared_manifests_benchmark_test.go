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
	"github.com/stolostron/siteconfig/api/v1alpha1"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestSharedManifestIndex(t *testing.T) {
	g := gomega.NewWithT(t)
	ci := &v1alpha1.ClusterInstance{Spec: v1alpha1.ClusterInstanceSpec{ExtraManifestsRefs: []v1alpha1.ExtraManifestReference{
		{Name: "one", Namespace: v1alpha1.SharedResourcesNamespace},
		{Name: "local"}, {Name: "explicit-local", Namespace: "cluster"},
		{Name: "two", Namespace: v1alpha1.SharedResourcesNamespace},
	}}}
	g.Expect(sharedManifestNames(ci)).To(gomega.Equal([]string{"one", "two"}))
	g.Expect(sharedManifestNames(&corev1.ConfigMap{})).To(gomega.BeNil())
}

// BenchmarkSharedManifestFanout exercises fanout with a fake client. This measures
// controller CPU/allocation cost, not real API throughput or fleet-scale acceptance.
func BenchmarkSharedManifestFanout(b *testing.B) {
	for _, count := range []int{100, 1000, 3500} {
		b.Run(fmt.Sprintf("copies-%d", count), func(b *testing.B) {
			ctx := context.Background()
			scheme := runtime.NewScheme()
			if err := corev1.AddToScheme(scheme); err != nil {
				b.Fatal(err)
			}
			if err := v1alpha1.AddToScheme(scheme); err != nil {
				b.Fatal(err)
			}
			source := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "common", Namespace: v1alpha1.SharedResourcesNamespace, Labels: map[string]string{acmBackupLabel: acmBackupLabelValue}}, Data: map[string]string{"payload": "initial"}}
			objects := []client.Object{source}
			controller := true
			for i := 0; i < count; i++ {
				name := fmt.Sprintf("cluster-%d", i)
				ci := &v1alpha1.ClusterInstance{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: name, UID: types.UID(name)}, Spec: v1alpha1.ClusterInstanceSpec{ClusterName: name, ExtraManifestsRefs: []v1alpha1.ExtraManifestReference{{Name: source.Name, Namespace: source.Namespace}}}}
				cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: source.Name, Namespace: name, Labels: map[string]string{ManagedCopyLabel: ManagedCopyLabelValue}, Annotations: map[string]string{SourceNamespaceAnnotation: source.Namespace}, OwnerReferences: []metav1.OwnerReference{{APIVersion: v1alpha1.GroupVersion.String(), Kind: v1alpha1.ClusterInstanceKind, Name: name, UID: ci.UID, Controller: &controller}}}, Data: map[string]string{"payload": "initial"}}
				objects = append(objects, ci, cm)
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithIndex(&v1alpha1.ClusterInstance{}, SharedExtraManifestRefIndexKey, sharedManifestNames).Build()
			r := &SharedManifestSync{Client: c, Log: zap.NewNop(), Recorder: events.NewFakeRecorder(1)}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := c.Get(ctx, client.ObjectKeyFromObject(source), source); err != nil {
					b.Fatal(err)
				}
				source.Data["payload"] = fmt.Sprint(i)
				if err := c.Update(ctx, source); err != nil {
					b.Fatal(err)
				}
				if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(source)}); err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			for i := 0; i < count; i++ {
				cm := &corev1.ConfigMap{}
				if err := c.Get(ctx, client.ObjectKey{Name: source.Name, Namespace: fmt.Sprintf("cluster-%d", i)}, cm); err != nil {
					b.Fatal(err)
				}
				if cm.Data["payload"] != source.Data["payload"] {
					b.Fatalf("copy %d did not converge", i)
				}
			}
			b.ReportMetric(float64(count), "copies/op")
		})
	}
}
