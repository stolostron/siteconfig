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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/stolostron/siteconfig/api/v1alpha1"
)

var _ = Describe("NamespaceMonitor Reconcile", func() {
	var (
		c          client.Client
		r          *NamespaceMonitor
		ctx        = context.Background()
		testLogger = zap.NewNop().Named("Test")
		sharedKey  = types.NamespacedName{Name: v1alpha1.SharedResourcesNamespace}
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().WithScheme(scheme.Scheme).Build()
		r = &NamespaceMonitor{Client: c, Scheme: scheme.Scheme, Log: testLogger}
	})

	It("re-creates the shared-resources namespace when it is missing", func() {
		ns := &corev1.Namespace{}
		Expect(c.Get(ctx, sharedKey, ns)).ToNot(Succeed())

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: sharedKey})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))

		Expect(c.Get(ctx, sharedKey, ns)).To(Succeed())
		Expect(ns.Labels).To(HaveKeyWithValue(SharedResourcesNamespaceManagedByLabel, SharedResourcesNamespaceManagedByValue))
	})

	It("is a no-op when the namespace already exists", func() {
		existing := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: v1alpha1.SharedResourcesNamespace}}
		Expect(c.Create(ctx, existing)).To(Succeed())

		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: sharedKey})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))

		ns := &corev1.Namespace{}
		Expect(c.Get(ctx, sharedKey, ns)).To(Succeed())
	})

	It("ignores reconcile requests for unrelated namespaces", func() {
		res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: "some-other-namespace"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(ctrl.Result{}))

		// The shared-resources namespace must not have been created.
		ns := &corev1.Namespace{}
		Expect(c.Get(ctx, sharedKey, ns)).ToNot(Succeed())
	})
})

var _ = Describe("EnsureSharedResourcesNamespace", func() {
	var (
		c         client.Client
		ctx       = context.Background()
		sharedKey = types.NamespacedName{Name: v1alpha1.SharedResourcesNamespace}
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().WithScheme(scheme.Scheme).Build()
	})

	It("creates the namespace with the managed-by label when missing", func() {
		Expect(EnsureSharedResourcesNamespace(ctx, c)).To(Succeed())

		ns := &corev1.Namespace{}
		Expect(c.Get(ctx, sharedKey, ns)).To(Succeed())
		Expect(ns.Labels).To(HaveKeyWithValue(SharedResourcesNamespaceManagedByLabel, SharedResourcesNamespaceManagedByValue))
	})

	It("is idempotent when the namespace already exists", func() {
		Expect(EnsureSharedResourcesNamespace(ctx, c)).To(Succeed())
		Expect(EnsureSharedResourcesNamespace(ctx, c)).To(Succeed())

		ns := &corev1.Namespace{}
		Expect(c.Get(ctx, sharedKey, ns)).To(Succeed())
	})

	It("reconciles the managed-by label when the namespace exists without it", func() {
		existing := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: v1alpha1.SharedResourcesNamespace}}
		Expect(c.Create(ctx, existing)).To(Succeed())

		Expect(EnsureSharedResourcesNamespace(ctx, c)).To(Succeed())

		ns := &corev1.Namespace{}
		Expect(c.Get(ctx, sharedKey, ns)).To(Succeed())
		Expect(ns.Labels).To(HaveKeyWithValue(SharedResourcesNamespaceManagedByLabel, SharedResourcesNamespaceManagedByValue))
	})
})
