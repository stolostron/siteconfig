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

package clusterinstance

import (
	"context"

	"github.com/stolostron/siteconfig/api/v1alpha1"

	"go.uber.org/zap"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Pause Functionality", func() {
	var (
		ctx             context.Context
		c               client.Client
		testLogger      *zap.Logger
		clusterInstance *v1alpha1.ClusterInstance
	)

	BeforeEach(func() {
		ctx = context.Background()
		testLogger = zap.NewNop().Named("Test")

		clusterInstance = &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "test",
			},
		}

		c = fakeclient.
			NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			WithObjects(clusterInstance).
			Build()

	})

	Describe("ApplyPause", func() {
		It("should set the paused annotation and update status", func() {

			originalResourceVersion := clusterInstance.ResourceVersion

			reason := "Testing pause"
			Expect(ApplyPause(ctx, c, testLogger, clusterInstance, reason)).To(Succeed())

			updatedInstance := &v1alpha1.ClusterInstance{}
			Expect(c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), updatedInstance)).To(Succeed())

			// Check annotation
			Expect(updatedInstance.Annotations).To(HaveKey(v1alpha1.PausedAnnotation))

			// Check status
			Expect(updatedInstance.Status.Paused).NotTo(BeNil())
			Expect(updatedInstance.Status.Paused.Reason).To(Equal(reason))
			Expect(updatedInstance.Status.Paused.TimeSet.Time).NotTo(BeZero())
			Expect(updatedInstance.ResourceVersion).ToNot(Equal(originalResourceVersion))
		})

		It("should return nil if pause annotation already exists", func() {

			// Apply initial pause
			initialReason := "Initial pause"
			Expect(ApplyPause(ctx, c, testLogger, clusterInstance, initialReason)).To(Succeed())

			// Apply a new reason
			newReason := "Updated pause reason"
			err := ApplyPause(ctx, c, testLogger, clusterInstance, newReason)
			Expect(err).To(BeNil())

			updatedInstance := &v1alpha1.ClusterInstance{}
			Expect(c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), updatedInstance)).To(Succeed())

			// Check annotation is still present and status is unchanged
			Expect(updatedInstance.Annotations).To(HaveKey(v1alpha1.PausedAnnotation))
			Expect(updatedInstance.Status.Paused).NotTo(BeNil())
			Expect(updatedInstance.Status.Paused.Reason).To(Equal(initialReason))
		})
	})

	Describe("RemovePause", func() {
		BeforeEach(func() {
			Expect(ApplyPause(ctx, c, testLogger, clusterInstance, "Testing pause")).To(Succeed())
		})

		It("should remove the paused annotation and clear status", func() {
			Expect(RemovePause(ctx, c, testLogger, clusterInstance)).To(Succeed())

			// Check annotation is removed
			Expect(clusterInstance.Annotations).NotTo(HaveKey(v1alpha1.PausedAnnotation))

			// Check status is cleared
			Expect(clusterInstance.Status.Paused).To(BeNil())
		})
	})
})
