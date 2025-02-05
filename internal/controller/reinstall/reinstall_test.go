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
limitations under the License.
*/

package reinstall

import (
	"context"
	"errors"

	"go.uber.org/zap"

	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
	"github.com/stolostron/siteconfig/internal/controller/configuration"
	"github.com/stolostron/siteconfig/internal/controller/deletion"
	cierrors "github.com/stolostron/siteconfig/internal/controller/errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("isRequestProcessed", func() {
	var (
		clusterInstance *v1alpha1.ClusterInstance
	)

	BeforeEach(func() {
		clusterInstance = &v1alpha1.ClusterInstance{
			Spec: v1alpha1.ClusterInstanceSpec{
				Reinstall: &v1alpha1.ReinstallSpec{
					Generation: "1234",
				},
			},
			Status: v1alpha1.ClusterInstanceStatus{
				Conditions: []metav1.Condition{},
				Reinstall:  &v1alpha1.ReinstallStatus{},
			},
		}
	})

	It("should return false if reinstall status is nil", func() {
		clusterInstance.Status.Reinstall = nil
		Expect(isRequestProcessed(clusterInstance)).To(BeFalse())
	})

	It("should return false if no relevant condition exists", func() {
		Expect(isRequestProcessed(clusterInstance)).To(BeFalse())
	})

	It("should return false if observed generation does not match reinstall generation", func() {
		clusterInstance.Status.Reinstall = &v1alpha1.ReinstallStatus{
			Conditions: []metav1.Condition{
				*reinstallRequestProcessedConditionStatus(metav1.ConditionTrue, v1alpha1.Completed, "Processed"),
			},
			ObservedGeneration: "4567",
		}
		Expect(isRequestProcessed(clusterInstance)).To(BeFalse())
	})

	It("should return true if observed generation matches reinstall generation", func() {
		clusterInstance.Status.Reinstall = &v1alpha1.ReinstallStatus{
			Conditions: []metav1.Condition{
				*reinstallRequestProcessedConditionStatus(metav1.ConditionTrue, v1alpha1.Completed, "Processed"),
			},
			ObservedGeneration: "1234",
		}
		Expect(isRequestProcessed(clusterInstance)).To(BeTrue())
	})

	It("should return true if request is processed but InProgressGeneration is not empty", func() {
		clusterInstance.Status.Reinstall = &v1alpha1.ReinstallStatus{
			Conditions: []metav1.Condition{
				*reinstallRequestProcessedConditionStatus(metav1.ConditionTrue, v1alpha1.Completed, "Completed"),
			},
			ObservedGeneration:   "1234",
			InProgressGeneration: "1234",
		}
		Expect(isRequestProcessed(clusterInstance)).To(BeTrue())
	})
})

var _ = Describe("isNewRequest", func() {
	var (
		clusterInstance *v1alpha1.ClusterInstance
	)

	BeforeEach(func() {
		clusterInstance = &v1alpha1.ClusterInstance{
			Spec: v1alpha1.ClusterInstanceSpec{
				Reinstall: &v1alpha1.ReinstallSpec{
					Generation: "1234",
				},
			},
			Status: v1alpha1.ClusterInstanceStatus{
				Conditions: []metav1.Condition{},
				Reinstall:  &v1alpha1.ReinstallStatus{},
			},
		}
	})

	It("should return true if reinstall status is nil", func() {
		clusterInstance.Status.Reinstall = nil
		Expect(isNewRequest(clusterInstance)).To(BeTrue())
	})

	It("should return true if in-progress generation does not match reinstall generation", func() {
		clusterInstance.Status.Reinstall.InProgressGeneration = "5678"
		Expect(isNewRequest(clusterInstance)).To(BeTrue())
	})

	It("should return false if in-progress generation matches reinstall generation", func() {
		clusterInstance.Status.Reinstall.InProgressGeneration = "1234"
		Expect(isNewRequest(clusterInstance)).To(BeFalse())
	})

	It("should return true if request is processed but InProgressGeneration is empty", func() {
		clusterInstance.Status.Reinstall = &v1alpha1.ReinstallStatus{
			Conditions: []metav1.Condition{
				*reinstallRequestProcessedConditionStatus(metav1.ConditionTrue, v1alpha1.Completed, "Completed"),
			},
			ObservedGeneration:   "1234",
			InProgressGeneration: "",
		}
		Expect(isNewRequest(clusterInstance)).To(BeTrue())
	})
})

var _ = Describe("finalizeReinstallRequest", func() {
	var (
		ctx         context.Context
		c           client.Client
		configStore *configuration.ConfigurationStore
		handler     *ReinstallHandler
		testLogger  *zap.Logger

		clusterInstance *v1alpha1.ClusterInstance
	)

	BeforeEach(func() {
		ctx = context.Background()
		testLogger = zap.NewNop().Named("Test")

		configStore, err := configuration.NewConfigurationStore(configuration.NewDefaultConfiguration())
		Expect(err).ToNot(HaveOccurred())

		clusterInstance = &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "test",
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				Reinstall: &v1alpha1.ReinstallSpec{
					Generation: "gen-2",
				},
			},
			Status: v1alpha1.ClusterInstanceStatus{
				Conditions: []metav1.Condition{},
				Reinstall:  &v1alpha1.ReinstallStatus{},
			},
		}

		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			WithObjects(clusterInstance).
			Build()

		handler = &ReinstallHandler{
			ConfigStore: configStore,
			Client:      c,
			Logger:      testLogger,
		}
	})

	It("should update ObservedGeneration if it differs from Spec.Reinstall.Generation", func() {
		clusterInstance.Status.Reinstall.ObservedGeneration = "gen-1"
		Expect(c.Update(ctx, clusterInstance)).To(Succeed())

		err := handler.finalizeReinstallRequest(ctx, testLogger, clusterInstance)
		Expect(err).NotTo(HaveOccurred())
		Expect(clusterInstance.Status.Reinstall.ObservedGeneration).To(Equal("gen-2"))
	})

	It("should clear InProgressGeneration if it is set", func() {
		clusterInstance.Status.Reinstall.InProgressGeneration = "gen-1"
		Expect(c.Update(ctx, clusterInstance)).To(Succeed())

		err := handler.finalizeReinstallRequest(ctx, testLogger, clusterInstance)
		Expect(err).NotTo(HaveOccurred())
		Expect(clusterInstance.Status.Reinstall.InProgressGeneration).To(BeEmpty())
	})

	It("should set RequestEndTime if it is zero", func() {
		clusterInstance.Status.Reinstall.RequestEndTime = metav1.Time{}
		Expect(c.Update(ctx, clusterInstance)).To(Succeed())

		err := handler.finalizeReinstallRequest(ctx, testLogger, clusterInstance)
		Expect(err).NotTo(HaveOccurred())
		Expect(clusterInstance.Status.Reinstall.RequestEndTime.IsZero()).To(BeFalse())
	})

	It("should return an error if patching fails", func() {
		errorMsg := "inject error"
		c = fakeclient.NewClientBuilder().
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(ctx context.Context, client client.Client, subResourceName string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					return errors.New(errorMsg)
				},
			}).
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			WithObjects(clusterInstance).
			Build()

		handler = &ReinstallHandler{
			ConfigStore: configStore,
			Client:      c,
			Logger:      testLogger,
		}

		err := handler.finalizeReinstallRequest(ctx, testLogger, clusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(errorMsg))
	})
})

var _ = Describe("ensureValidReinstallRequest", func() {
	var (
		ctx        context.Context
		c          client.Client
		handler    *ReinstallHandler
		testLogger *zap.Logger

		clusterInstance *v1alpha1.ClusterInstance
	)

	BeforeEach(func() {
		ctx = context.Background()
		testLogger = zap.NewNop().Named("Test")

		configStore, err := configuration.NewConfigurationStore(configuration.NewDefaultConfiguration())
		Expect(err).ToNot(HaveOccurred())

		clusterInstance = &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "test",
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				Reinstall: &v1alpha1.ReinstallSpec{
					Generation: "1234",
				},
			},
			Status: v1alpha1.ClusterInstanceStatus{
				Conditions: []metav1.Condition{},
				Reinstall:  &v1alpha1.ReinstallStatus{},
			},
		}

		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			WithObjects(clusterInstance).
			Build()

		handler = &ReinstallHandler{
			ConfigStore: configStore,
			Client:      c,
			Logger:      testLogger,
		}
	})

	It("should return an error if reinstalls are not allowed", func() {
		handler.ConfigStore.SetAllowReinstalls(false)

		_, err := handler.ensureValidReinstallRequest(ctx, testLogger, clusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("siteConfig operator is not configured for cluster reinstalls"))
	})
})

var _ = Describe("ensureStartTimeIsSet", func() {
	var (
		ctx        context.Context
		c          client.Client
		handler    *ReinstallHandler
		testLogger *zap.Logger

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
			Spec: v1alpha1.ClusterInstanceSpec{
				Reinstall: &v1alpha1.ReinstallSpec{
					Generation: "1234",
				},
			},
			Status: v1alpha1.ClusterInstanceStatus{
				Conditions: []metav1.Condition{},
				Reinstall:  &v1alpha1.ReinstallStatus{},
			},
		}

		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			WithObjects(clusterInstance).
			Build()

		handler = &ReinstallHandler{
			Client: c,
			Logger: testLogger,
		}
	})

	It("should set the request start time if not already set", func() {
		clusterInstance.Status.Reinstall.RequestStartTime = metav1.Time{}
		Expect(clusterInstance.Status.Reinstall.RequestStartTime.IsZero()).To(BeTrue())
		result, err := handler.ensureStartTimeIsSet(ctx, testLogger, clusterInstance)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Requeue).To(BeTrue())
		Expect(clusterInstance.Status.Reinstall.RequestStartTime.IsZero()).To(BeFalse())
	})

	It("should not update RequestStartTime if already set", func() {
		clusterInstance.Status.Reinstall.RequestStartTime = metav1.Now()
		Expect(clusterInstance.Status.Reinstall.RequestStartTime.IsZero()).To(BeFalse())
		result, err := handler.ensureStartTimeIsSet(ctx, testLogger, clusterInstance)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Requeue).To(BeFalse())
	})
})

var _ = Describe("ensureRenderedManifestsAreDeleted", func() {
	var (
		ctx        context.Context
		c          client.Client
		handler    *ReinstallHandler
		testLogger *zap.Logger

		configStore     *configuration.ConfigurationStore
		clusterInstance *v1alpha1.ClusterInstance
		object          client.Object
	)

	BeforeEach(func() {
		ctx = context.Background()
		testLogger = zap.NewNop().Named("Test")

		configStore, err := configuration.NewConfigurationStore(configuration.NewDefaultConfiguration())
		Expect(err).ToNot(HaveOccurred())

		name := "test"
		namespace := "test"

		object = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cm1",
				Namespace: namespace,
				Labels:    map[string]string{ci.OwnedByLabel: ci.GenerateOwnedByLabelValue(namespace, name)},
			},
		}

		clusterInstance = &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				Reinstall: &v1alpha1.ReinstallSpec{
					Generation: "gen-2",
				},
			},
			Status: v1alpha1.ClusterInstanceStatus{
				Conditions: []metav1.Condition{},
				Reinstall:  &v1alpha1.ReinstallStatus{},
				ManifestsRendered: []v1alpha1.ManifestReference{
					{
						APIGroup:  ptr.To("v1"),
						Kind:      "ConfigMap",
						Name:      object.GetName(),
						Namespace: object.GetNamespace(),
						SyncWave:  0,
						Status:    v1alpha1.ManifestRenderedSuccess,
					},
				},
			},
		}

		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			WithObjects(clusterInstance, object).
			Build()

		handler = &ReinstallHandler{
			ConfigStore:     configStore,
			Client:          c,
			DeletionHandler: &deletion.DeletionHandler{Client: c, Logger: testLogger},
			Logger:          testLogger,
		}
	})

	It("should set condition to InProgress if not initialized", func() {
		changed := setReinstallStatusCondition(clusterInstance, *reinstallRenderedManifestsDeletedConditionStatus(metav1.ConditionFalse, v1alpha1.Initialized, "Initialized"))
		Expect(changed).To(BeTrue())
		Expect(c.Update(ctx, clusterInstance)).To(Succeed())

		result, err := handler.ensureRenderedManifestsAreDeleted(ctx, testLogger, clusterInstance)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Requeue).To(BeTrue())

		cond := findReinstallStatusCondition(clusterInstance, v1alpha1.ReinstallRenderedManifestsDeleted)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Reason).To(Equal(string(v1alpha1.InProgress)))
	})

	It("should return if deletion has already completed", func() {
		setReinstallStatusCondition(clusterInstance, *reinstallRenderedManifestsDeletedConditionStatus(metav1.ConditionTrue, v1alpha1.Completed, "Success"))

		result, err := handler.ensureRenderedManifestsAreDeleted(ctx, testLogger, clusterInstance)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Requeue).To(BeFalse())
	})

	It("should return if deletion has already failed", func() {
		setReinstallStatusCondition(clusterInstance, *reinstallRenderedManifestsDeletedConditionStatus(metav1.ConditionFalse, v1alpha1.Failed, "Failed"))

		result, err := handler.ensureRenderedManifestsAreDeleted(ctx, testLogger, clusterInstance)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Requeue).To(BeFalse())
	})

	It("should return a timeout condition if deletion times out", func() {

		setReinstallStatusCondition(clusterInstance, *reinstallRenderedManifestsDeletedConditionStatus(metav1.ConditionFalse, v1alpha1.InProgress, "Deletion in progress"))

		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			WithObjects(clusterInstance, object).
			WithInterceptorFuncs(interceptor.Funcs{
				Delete: func(ctx context.Context, fclient client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					return cierrors.NewDeletionTimeoutError("")
				},
			}).
			Build()

		handler = &ReinstallHandler{
			ConfigStore:     configStore,
			Client:          c,
			DeletionHandler: &deletion.DeletionHandler{Client: c, Logger: testLogger},
			Logger:          testLogger,
		}

		result, err := handler.ensureRenderedManifestsAreDeleted(ctx, testLogger, clusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(cierrors.IsDeletionTimeoutError(err)).To(BeTrue())
		Expect(result.Requeue).To(BeTrue())
	})

	It("should return an error if deletion fails", func() {
		setReinstallStatusCondition(clusterInstance, *reinstallRenderedManifestsDeletedConditionStatus(metav1.ConditionFalse, v1alpha1.InProgress, "Deletion in progress"))

		errorMsg := "inject error"
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			WithObjects(clusterInstance, object).
			WithInterceptorFuncs(interceptor.Funcs{
				Delete: func(ctx context.Context, fclient client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					return errors.New(errorMsg)
				},
			}).
			Build()

		handler = &ReinstallHandler{
			ConfigStore:     configStore,
			Client:          c,
			DeletionHandler: &deletion.DeletionHandler{Client: c, Logger: testLogger},
			Logger:          testLogger,
		}

		result, err := handler.ensureRenderedManifestsAreDeleted(ctx, testLogger, clusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring(errorMsg))
		Expect(result.Requeue).To(BeTrue())
	})

	It("should requeue if deletion is still in progress", func() {
		setReinstallStatusCondition(clusterInstance, *reinstallRenderedManifestsDeletedConditionStatus(metav1.ConditionFalse, v1alpha1.InProgress, "Deletion in progress"))

		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			WithObjects(clusterInstance, object).
			Build()

		handler = &ReinstallHandler{
			ConfigStore:     configStore,
			Client:          c,
			DeletionHandler: &deletion.DeletionHandler{Client: c, Logger: testLogger},
			Logger:          testLogger,
		}

		result, err := handler.ensureRenderedManifestsAreDeleted(ctx, testLogger, clusterInstance)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).ToNot(BeZero())
	})

	It("should complete successfully if deletion is done", func() {
		setReinstallStatusCondition(clusterInstance, *reinstallRenderedManifestsDeletedConditionStatus(metav1.ConditionFalse, v1alpha1.InProgress, "Deletion in progress"))

		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			WithObjects(clusterInstance, object).
			Build()

		handler = &ReinstallHandler{
			ConfigStore:     configStore,
			Client:          c,
			DeletionHandler: &deletion.DeletionHandler{Client: c, Logger: testLogger},
			Logger:          testLogger,
		}

		// Invoke deletion first time - to delete the resource, which triggers a requeue
		result, err := handler.ensureRenderedManifestsAreDeleted(ctx, testLogger, clusterInstance)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Requeue).To(BeTrue())
		Expect(result.RequeueAfter).To(Equal(deletionRequeueWithMediumInterval))

		// Invoke deletion second time - to ensure that deletion was successful and that the status condition is correctly set
		result, err = handler.ensureRenderedManifestsAreDeleted(ctx, testLogger, clusterInstance)
		Expect(err).NotTo(HaveOccurred())

		// Verify that requeue is set to reflect the update in ReinstallRenderedManifestsDeleted status condition
		Expect(result.Requeue).To(BeTrue())
		cond := findReinstallStatusCondition(clusterInstance, v1alpha1.ReinstallRenderedManifestsDeleted)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(string(v1alpha1.Completed)))
	})
})

var _ = Describe("ProcessRequest", func() {
	var (
		ctx        context.Context
		c          client.Client
		handler    *ReinstallHandler
		testLogger *zap.Logger

		configStore     *configuration.ConfigurationStore
		clusterInstance *v1alpha1.ClusterInstance
	)

	BeforeEach(func() {
		ctx = context.Background()
		testLogger = zap.NewNop().Named("Test")

		var err error
		configStore, err = configuration.NewConfigurationStore(configuration.NewDefaultConfiguration())
		Expect(err).ToNot(HaveOccurred())

		name := "test"
		namespace := "test"

		clusterInstance = &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				Reinstall: &v1alpha1.ReinstallSpec{
					Generation: "1234",
				},
			},
			Status: v1alpha1.ClusterInstanceStatus{
				Conditions: []metav1.Condition{},
				Reinstall: &v1alpha1.ReinstallStatus{
					InProgressGeneration: "1234",
					RequestStartTime:     metav1.Now(),
					RequestEndTime:       metav1.Time{},
				},
			},
		}

		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			WithObjects(clusterInstance).
			Build()

		handler = &ReinstallHandler{
			ConfigStore:     configStore,
			Client:          c,
			DeletionHandler: &deletion.DeletionHandler{Client: c, Logger: testLogger},
			Logger:          testLogger,
		}
	})

	It("should return an error if ReinstallSpec is missing", func() {
		clusterInstance.Spec.Reinstall = nil
		_, err := handler.ProcessRequest(ctx, clusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("missing ReinstallSpec"))
	})

	It("should detect a new request and requeue", func() {
		result, err := handler.ProcessRequest(context.TODO(), clusterInstance)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Requeue).To(BeTrue())
	})

	It("should complete the reinstall process successfully", func() {

		setReinstallStatusCondition(clusterInstance, *reinstallRequestValidatedConditionStatus(metav1.ConditionTrue, v1alpha1.Completed, "Completed Valid reinstall request"))
		setReinstallStatusCondition(clusterInstance, *reinstallRenderedManifestsDeletedConditionStatus(metav1.ConditionTrue, v1alpha1.Completed, "Finished"))
		setReinstallStatusCondition(clusterInstance, *reinstallRequestProcessedConditionStatus(metav1.ConditionFalse, v1alpha1.InProgress, "In progress"))

		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			WithObjects(clusterInstance).
			Build()

		handler = &ReinstallHandler{
			ConfigStore:     configStore,
			Client:          c,
			DeletionHandler: &deletion.DeletionHandler{Client: c, Logger: testLogger},
			Logger:          testLogger,
		}

		handler.ConfigStore.SetAllowReinstalls(true)

		Expect(clusterInstance.Status.Reinstall.RequestEndTime).To(BeZero())
		Expect(clusterInstance.Status.Reinstall.ObservedGeneration).To(BeEmpty())

		// Invoke first execution of ProcessRequest -> this will trigger updates to clusterInstance.Status.Reinstall
		result, err := handler.ProcessRequest(ctx, clusterInstance)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Requeue).To(BeTrue())

		// Invoke second execution of ProcessRequest and verify the expected clusterInstance.Status.Reinstall
		result, err = handler.ProcessRequest(ctx, clusterInstance)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Requeue).To(BeFalse())

		Expect(clusterInstance.Status.Reinstall).NotTo(BeNil())
		Expect(clusterInstance.Status.Reinstall.RequestEndTime).NotTo(BeZero())
		Expect(clusterInstance.Status.Reinstall.ObservedGeneration).To(Equal(clusterInstance.Spec.Reinstall.Generation))
		Expect(clusterInstance.Status.Reinstall.InProgressGeneration).To(BeEmpty())

		cond := findReinstallStatusCondition(clusterInstance, v1alpha1.ReinstallRequestProcessed)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(string(v1alpha1.Completed)))
	})
})
