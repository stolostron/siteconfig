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
	"testing"

	"github.com/onsi/gomega"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/stolostron/siteconfig/api/v1alpha1"
)

// Copy recovery is outside the shared-manifests scope. An unchanged, reconciled
// ClusterInstance must not recreate a missing copy, including after a handled restore.
func TestObservedClusterDoesNotRecoverSharedCopies(t *testing.T) {
	for _, scenario := range []string{"reconciled", "restore-handled"} {
		t.Run(scenario, func(t *testing.T) {
			g := gomega.NewWithT(t)
			ctx := context.Background()
			scheme := runtime.NewScheme()
			g.Expect(corev1.AddToScheme(scheme)).To(gomega.Succeed())
			g.Expect(v1alpha1.AddToScheme(scheme)).To(gomega.Succeed())
			ci := &v1alpha1.ClusterInstance{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "cluster", Generation: 3,
					Finalizers:  []string{clusterInstanceFinalizer},
					Annotations: map[string]string{veleroRestoreStatusAnnotation: "true"}},
				Spec:   v1alpha1.ClusterInstanceSpec{ClusterName: "cluster", ExtraManifestsRefs: []v1alpha1.ExtraManifestReference{{Name: "common", Namespace: v1alpha1.SharedResourcesNamespace}}},
				Status: v1alpha1.ClusterInstanceStatus{ObservedGeneration: 3},
			}
			if scenario == "restore-handled" {
				ci.Labels = map[string]string{veleroRestoreNameLabel: "restore-test"}
				ci.Annotations[veleroRestoreHandledAnnotation] = "restore-test"
			}
			source := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "common", Namespace: v1alpha1.SharedResourcesNamespace}, Data: map[string]string{"payload": "source"}}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ci, source).WithStatusSubresource(ci).Build()
			before := &v1alpha1.ClusterInstance{}
			g.Expect(c.Get(ctx, client.ObjectKeyFromObject(ci), before)).To(gomega.Succeed())
			r := &ClusterInstanceReconciler{Client: c, Scheme: scheme, Log: zap.NewNop()}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(ci)})
			g.Expect(err).NotTo(gomega.HaveOccurred())
			err = c.Get(ctx, client.ObjectKey{Namespace: ci.Namespace, Name: "common"}, &corev1.ConfigMap{})
			g.Expect(apierrors.IsNotFound(err)).To(gomega.BeTrue())
			after := &v1alpha1.ClusterInstance{}
			g.Expect(c.Get(ctx, client.ObjectKeyFromObject(ci), after)).To(gomega.Succeed())
			g.Expect(after).To(gomega.Equal(before))
		})
	}
}
