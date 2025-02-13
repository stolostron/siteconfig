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
	"encoding/json"
	"errors"
	"time"

	"go.uber.org/zap"

	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
	"github.com/stolostron/siteconfig/internal/controller/configuration"
	"github.com/stolostron/siteconfig/internal/controller/deletion"
	cierrors "github.com/stolostron/siteconfig/internal/controller/errors"
	"github.com/stolostron/siteconfig/internal/controller/preservation"
	"github.com/wI2L/jsondiff"

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
				ClusterName: "foobar",
				Reinstall: &v1alpha1.ReinstallSpec{
					Generation:       "gen-2",
					PreservationMode: v1alpha1.PreservationModeNone,
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

	It("should set the ReinstallHistory", func() {
		lastApplied := clusterInstance.DeepCopy()
		lastApplied.Spec.ClusterName = "old-name"
		lastAppliedBytes, _ := json.Marshal(lastApplied.Spec)
		metav1.SetMetaDataAnnotation(&clusterInstance.ObjectMeta, v1alpha1.LastClusterInstanceSpecAnnotation, string(lastAppliedBytes))

		clusterInstance.Spec.ClusterName = "new-name"
		Expect(c.Update(ctx, clusterInstance)).To(Succeed())

		// Update Reinstall Status
		testReinstallStartTime := metav1.NewTime(time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local))
		testReinstallEndTime := metav1.NewTime(time.Date(2025, 1, 1, 0, 5, 0, 0, time.Local))

		clusterInstance.Status.Reinstall = &v1alpha1.ReinstallStatus{
			ObservedGeneration:   "gen-2",
			InProgressGeneration: "",
			RequestStartTime:     testReinstallStartTime,
			RequestEndTime:       testReinstallEndTime,
		}
		Expect(c.Status().Update(ctx, clusterInstance)).To(Succeed())

		err := handler.finalizeReinstallRequest(ctx, testLogger, clusterInstance)
		Expect(err).NotTo(HaveOccurred())
		Expect(len(clusterInstance.Status.Reinstall.History)).To(Equal(1))

		expectedDiff := jsondiff.Patch{
			{
				Value: "new-name",
				Type:  jsondiff.OperationReplace,
				Path:  "/clusterName",
			},
		}
		ClusterInstanceSpecDiff, err := json.Marshal(expectedDiff)
		Expect(err).ToNot(HaveOccurred())
		Expect(clusterInstance.Status.Reinstall.History).To(Equal([]v1alpha1.ReinstallHistory{
			{
				Generation:              "gen-2",
				RequestStartTime:        testReinstallStartTime,
				RequestEndTime:          testReinstallEndTime,
				ClusterInstanceSpecDiff: string(ClusterInstanceSpecDiff),
			},
		}))
	})

	It("should not update the ReinstallHistory if previously set for the same generation", func() {
		lastApplied := clusterInstance.DeepCopy()
		lastApplied.Spec.ClusterName = "old-name"
		lastAppliedBytes, _ := json.Marshal(lastApplied.Spec)
		metav1.SetMetaDataAnnotation(&clusterInstance.ObjectMeta, v1alpha1.LastClusterInstanceSpecAnnotation, string(lastAppliedBytes))
		clusterInstance.Spec.ClusterName = "new-name"
		Expect(c.Update(ctx, clusterInstance)).To(Succeed())

		// Update Reinstall Status
		testReinstallStartTime := metav1.NewTime(time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local))
		testReinstallEndTime := metav1.NewTime(time.Date(2025, 1, 1, 0, 5, 0, 0, time.Local))

		clusterInstance.Status.Reinstall = &v1alpha1.ReinstallStatus{
			ObservedGeneration:   "gen-2",
			InProgressGeneration: "",
			RequestStartTime:     testReinstallStartTime,
			RequestEndTime:       testReinstallEndTime,
			History: []v1alpha1.ReinstallHistory{
				{
					Generation:              "gen-2",
					RequestStartTime:        testReinstallStartTime,
					RequestEndTime:          testReinstallEndTime,
					ClusterInstanceSpecDiff: "this-should-not-change",
				},
			},
		}
		Expect(c.Status().Update(ctx, clusterInstance)).To(Succeed())

		err := handler.finalizeReinstallRequest(ctx, testLogger, clusterInstance)
		Expect(err).NotTo(HaveOccurred())
		Expect(len(clusterInstance.Status.Reinstall.History)).To(Equal(1))
		Expect(clusterInstance.Status.Reinstall.History[0].ClusterInstanceSpecDiff).To(Equal("this-should-not-change"))
	})

	It("should add a new ReinstallHistory record for a new reinstall request", func() {
		lastApplied := clusterInstance.DeepCopy()
		lastApplied.Spec.ClusterName = "old-name"
		lastApplied.Spec.Reinstall.Generation = "old-generation"
		lastAppliedBytes, _ := json.Marshal(lastApplied.Spec)
		metav1.SetMetaDataAnnotation(&clusterInstance.ObjectMeta, v1alpha1.LastClusterInstanceSpecAnnotation, string(lastAppliedBytes))
		clusterInstance.Spec.ClusterName = "new-name"
		Expect(c.Update(ctx, clusterInstance)).To(Succeed())

		// Update Reinstall Status
		testReinstallStartTime := metav1.NewTime(time.Date(2025, 1, 1, 0, 0, 0, 0, time.Local))
		testReinstallEndTime := metav1.NewTime(time.Date(2025, 1, 1, 0, 5, 0, 0, time.Local))

		clusterInstance.Status.Reinstall = &v1alpha1.ReinstallStatus{
			ObservedGeneration:   "gen-2",
			InProgressGeneration: "",
			RequestStartTime:     testReinstallStartTime,
			RequestEndTime:       testReinstallEndTime,
			History: []v1alpha1.ReinstallHistory{
				{
					Generation:              "gen-1",
					RequestStartTime:        testReinstallStartTime,
					RequestEndTime:          testReinstallEndTime,
					ClusterInstanceSpecDiff: "foobar",
				},
			},
		}
		Expect(c.Status().Update(ctx, clusterInstance)).To(Succeed())

		err := handler.finalizeReinstallRequest(ctx, testLogger, clusterInstance)
		Expect(err).NotTo(HaveOccurred())
		Expect(len(clusterInstance.Status.Reinstall.History)).To(Equal(2))
		expectedDiff := jsondiff.Patch{
			{
				Value: "new-name",
				Type:  jsondiff.OperationReplace,
				Path:  "/clusterName",
			},
			{
				Value: clusterInstance.Spec.Reinstall.Generation,
				Type:  jsondiff.OperationReplace,
				Path:  "/reinstall/generation",
			},
		}
		ClusterInstanceSpecDiff, err := json.Marshal(expectedDiff)
		Expect(err).ToNot(HaveOccurred())
		Expect(clusterInstance.Status.Reinstall.History[1]).To(Equal(v1alpha1.ReinstallHistory{
			Generation:              "gen-2",
			RequestStartTime:        testReinstallStartTime,
			RequestEndTime:          testReinstallEndTime,
			ClusterInstanceSpecDiff: string(ClusterInstanceSpecDiff),
		}))
	})

	It("should return an error if the ClusterInstance.Spec diff cannot be computed", func() {
		metav1.SetMetaDataAnnotation(&clusterInstance.ObjectMeta, v1alpha1.LastClusterInstanceSpecAnnotation, "error")
		Expect(c.Update(ctx, clusterInstance)).To(Succeed())

		err := handler.finalizeReinstallRequest(ctx, testLogger, clusterInstance)
		Expect(err).To(HaveOccurred())
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
		Expect(result).To(BeZero())
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
		Expect(result).To(BeZero())
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

		pullSecret, bmcCredential *corev1.Secret

		name, namespace string
	)

	BeforeEach(func() {
		ctx = context.Background()
		testLogger = zap.NewNop().Named("Test")

		var err error
		configStore, err = configuration.NewConfigurationStore(configuration.NewDefaultConfiguration())
		Expect(err).ToNot(HaveOccurred())

		name = "test"
		namespace = "test"

		pullSecret = generateClusterInstanceSecret("pull-secret", namespace)
		bmcCredential = generateClusterInstanceSecret("bmc-secret-1", namespace)

		clusterInstance = &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				Reinstall: &v1alpha1.ReinstallSpec{
					Generation:       "1234",
					PreservationMode: v1alpha1.PreservationModeAll,
				},
				PullSecretRef: corev1.LocalObjectReference{Name: pullSecret.Name},
				Nodes: []v1alpha1.NodeSpec{
					{BmcCredentialsName: v1alpha1.BmcCredentialsName{Name: bmcCredential.Name}},
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
			WithObjects(clusterInstance, pullSecret, bmcCredential).
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
		result, err := handler.ProcessRequest(ctx, clusterInstance)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Requeue).To(BeTrue())
	})

	It("should complete the reinstall process successfully", func() {

		clusterInstance.Spec.Reinstall.PreservationMode = v1alpha1.PreservationModeNone
		clusterInstance.Status.Reinstall = &v1alpha1.ReinstallStatus{
			InProgressGeneration: "1234",
			RequestStartTime:     metav1.Now(),
			RequestEndTime:       metav1.Time{},
		}
		setReinstallStatusCondition(clusterInstance, *reinstallRequestValidatedConditionStatus(metav1.ConditionTrue, v1alpha1.Completed, "Valid reinstall request"))
		setReinstallStatusCondition(clusterInstance, *reinstallPreservationDataBackedupConditionStatus(metav1.ConditionFalse, v1alpha1.PreservationNotRequired, "PreservationMode is set to None."))
		setReinstallStatusCondition(clusterInstance, *reinstallClusterIdentityDataDetectedConditionStatus(metav1.ConditionFalse, v1alpha1.PreservationNotRequired, "PreservationMode is set to None."))
		setReinstallStatusCondition(clusterInstance, *reinstallRenderedManifestsDeletedConditionStatus(metav1.ConditionTrue, v1alpha1.Completed, "Successfully deleted rendered manifests"))
		setReinstallStatusCondition(clusterInstance, *reinstallPreservationDataRestoredConditionStatus(metav1.ConditionFalse, v1alpha1.PreservationNotRequired, "PreservationMode is set to None."))
		setReinstallStatusCondition(clusterInstance, *reinstallRequestProcessedConditionStatus(metav1.ConditionFalse, v1alpha1.InProgress, ""))

		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			WithObjects(clusterInstance, pullSecret, bmcCredential).
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

		// Invoke 1st execution of ProcessRequest -> this will trigger updates to clusterInstance.Status.Reinstall
		result, err := handler.ProcessRequest(ctx, clusterInstance)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(BeZero())

		// Verify the expected clusterInstance.Status.Reinstall
		Expect(c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), clusterInstance)).To(Succeed())
		reinstallStatus := clusterInstance.Status.Reinstall
		Expect(reinstallStatus.InProgressGeneration).To(BeEmpty())
		Expect(clusterInstance.Status.Reinstall.RequestEndTime).ToNot(BeZero())
		Expect(clusterInstance.Status.Reinstall.ObservedGeneration).To(Equal(clusterInstance.Spec.Reinstall.Generation))
		cond := findReinstallStatusCondition(clusterInstance, v1alpha1.ReinstallRequestProcessed)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal(string(v1alpha1.Completed)))

		// Invoke 2nd execution of ProcessRequest should do nothing -> reinstall request should be processed
		result, err = handler.ProcessRequest(ctx, clusterInstance)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(BeZero())
	})

	It("should set the Paused annotation on the ClusterInstance when an error occurs", func() {

		object := &corev1.ConfigMap{
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
					PreservationMode: v1alpha1.PreservationModeNone,
					Generation:       "1234",
				},
			},
			Status: v1alpha1.ClusterInstanceStatus{
				Conditions: []metav1.Condition{},
				Reinstall: &v1alpha1.ReinstallStatus{
					InProgressGeneration: "1234",
					RequestStartTime:     metav1.Now(),
					RequestEndTime:       metav1.Time{},
				},
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

		setReinstallStatusCondition(clusterInstance, *reinstallRequestValidatedConditionStatus(metav1.ConditionTrue, v1alpha1.Completed, "Valid reinstall request"))
		setReinstallStatusCondition(clusterInstance, *reinstallPreservationDataBackedupConditionStatus(metav1.ConditionFalse, v1alpha1.PreservationNotRequired, "PreservationMode is set to None."))
		setReinstallStatusCondition(clusterInstance, *reinstallClusterIdentityDataDetectedConditionStatus(metav1.ConditionFalse, v1alpha1.PreservationNotRequired, "PreservationMode is set to None."))
		setReinstallStatusCondition(clusterInstance, *reinstallRenderedManifestsDeletedConditionStatus(metav1.ConditionFalse, v1alpha1.InProgress, "Rendered manifests are being deleted"))
		setReinstallStatusCondition(clusterInstance, *reinstallPreservationDataRestoredConditionStatus(metav1.ConditionFalse, v1alpha1.PreservationNotRequired, "PreservationMode is set to None."))
		setReinstallStatusCondition(clusterInstance, *reinstallRequestProcessedConditionStatus(metav1.ConditionFalse, v1alpha1.InProgress, "In progress"))

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

		handler.ConfigStore.SetAllowReinstalls(true)

		// Verify that the paused annotation is not set on the ClusterInstance
		Expect(c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), clusterInstance)).To(Succeed())
		Expect(clusterInstance.IsPaused()).To(BeFalse())

		result, err := handler.ProcessRequest(ctx, clusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(cierrors.IsDeletionTimeoutError(err)).To(BeTrue())
		Expect(result).To(BeZero())

		// Verify that the paused annotation is set on the ClusterInstance
		Expect(c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), clusterInstance)).To(Succeed())
		Expect(clusterInstance.IsPaused()).To(BeTrue())
	})
})

var _ = Describe("applyPreservedLabelToClusterInstanceSecrets", func() {
	var (
		ctx        context.Context
		c          client.Client
		handler    *ReinstallHandler
		testLogger *zap.Logger

		testNamespace   = "test"
		clusterInstance *v1alpha1.ClusterInstance

		pullSecret, bmcCredential1, bmcCredential2 *corev1.Secret
	)

	BeforeEach(func() {

		ctx = context.Background()
		testLogger = zap.NewNop().Named("Test")

		pullSecret = generateClusterInstanceSecret("pull-secret", testNamespace)
		bmcCredential1 = generateClusterInstanceSecret("bmc-secret-1", testNamespace)
		bmcCredential2 = generateClusterInstanceSecret("bmc-secret-2", testNamespace)

		clusterInstance = &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testNamespace,
				Namespace: testNamespace,
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				PullSecretRef: corev1.LocalObjectReference{Name: pullSecret.Name},
				Nodes: []v1alpha1.NodeSpec{
					{BmcCredentialsName: v1alpha1.BmcCredentialsName{Name: bmcCredential1.Name}},
					{BmcCredentialsName: v1alpha1.BmcCredentialsName{Name: bmcCredential2.Name}},
				},
			},
			Status: v1alpha1.ClusterInstanceStatus{
				Conditions: []metav1.Condition{},
				Reinstall: &v1alpha1.ReinstallStatus{
					Conditions: []metav1.Condition{{
						Type:   string(v1alpha1.ReinstallPreservationDataBackedup),
						Status: metav1.ConditionFalse,
						Reason: string(v1alpha1.Initialized),
					}},
				},
			},
		}

		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			WithObjects(clusterInstance, pullSecret, bmcCredential1, bmcCredential2).
			Build()

		handler = &ReinstallHandler{
			Client: c,
			Logger: testLogger,
		}
	})

	It("should update secrets with preservation label when ClusterInstance secrets do not have the preservation label", func() {
		for _, object := range []client.Object{pullSecret, bmcCredential1, bmcCredential2} {
			Expect(c.Get(ctx, client.ObjectKeyFromObject(object), object)).To(Succeed())
			Expect(object.GetLabels()).ToNot(HaveKey(preservation.InternalPreservationLabelKey))
		}

		result, err := handler.applyPreservedLabelToClusterInstanceSecrets(ctx, testLogger, clusterInstance)
		Expect(err).ToNot(HaveOccurred())
		Expect(result).To(BeZero())

		for _, object := range []client.Object{pullSecret, bmcCredential1, bmcCredential2} {
			Expect(c.Get(ctx, client.ObjectKeyFromObject(object), object)).To(Succeed())
			Expect(object.GetLabels()).To(HaveKeyWithValue(preservation.InternalPreservationLabelKey, preservation.InternalPreservationLabelValue))
		}
	})

	It("should not update secrets with preservation label when ReinstallPreservationDataBackedup is different from Initialized", func() {
		clusterInstance.Status.Reinstall.Conditions = []metav1.Condition{{
			Type:   string(v1alpha1.ReinstallPreservationDataBackedup),
			Status: metav1.ConditionFalse,
			Reason: string(v1alpha1.InProgress),
		}}

		result, err := handler.applyPreservedLabelToClusterInstanceSecrets(ctx, testLogger, clusterInstance)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(BeZero())

		for _, object := range []client.Object{pullSecret, bmcCredential1, bmcCredential2} {
			Expect(c.Get(ctx, client.ObjectKeyFromObject(object), object)).To(Succeed())
			Expect(object.GetLabels()).To(HaveKey(preservation.InternalPreservationLabelKey))
		}
	})

	It("should return an error when the ReinstallPreservationDataBackedup condition is not set", func() {
		clusterInstance.Status.Reinstall.Conditions = []metav1.Condition{}

		result, err := handler.applyPreservedLabelToClusterInstanceSecrets(ctx, testLogger, clusterInstance)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(BeZero())

		for _, object := range []client.Object{pullSecret, bmcCredential1, bmcCredential2} {
			Expect(c.Get(ctx, client.ObjectKeyFromObject(object), object)).To(Succeed())
			Expect(object.GetLabels()).To(HaveKey(preservation.InternalPreservationLabelKey))
		}
	})

	It("should not return an error if a secret does not exist", func() {
		Expect(c.Delete(ctx, bmcCredential1)).To(Succeed())

		_, err := handler.applyPreservedLabelToClusterInstanceSecrets(ctx, testLogger, clusterInstance)
		Expect(err).NotTo(HaveOccurred())
	})

	It("should return an error when secret update fails", func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			WithObjects(clusterInstance, pullSecret, bmcCredential1, bmcCredential2).
			WithInterceptorFuncs(interceptor.Funcs{
				Patch: func(ctx context.Context, client client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
					return errors.New("inject error")
				},
			}).
			Build()

		handler = &ReinstallHandler{
			Client: c,
			Logger: testLogger,
		}

		_, err := handler.applyPreservedLabelToClusterInstanceSecrets(ctx, testLogger, clusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to update Secret"))
	})
})

var _ = Describe("ensureDataIsPreserved", func() {
	var (
		ctx        context.Context
		c          client.Client
		testLogger *zap.Logger
		handler    *ReinstallHandler

		testNamespace = "test"

		clusterInstance *v1alpha1.ClusterInstance

		secret1, secret2, secret3, secret4 *corev1.Secret
	)

	BeforeEach(func() {
		ctx = context.Background()
		testLogger = zap.NewNop().Named("Test")

		clusterInstance = &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testNamespace,
				Namespace: testNamespace,
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				Reinstall: &v1alpha1.ReinstallSpec{
					PreservationMode: v1alpha1.PreservationModeAll,
				},
			},
			Status: v1alpha1.ClusterInstanceStatus{
				Conditions: []metav1.Condition{},
				Reinstall:  &v1alpha1.ReinstallStatus{},
			},
		}

		// Create preserved secrets
		secret1 = createTestSecret("secret1", testNamespace, v1alpha1.PreservationModeClusterIdentity, true)
		secret2 = createTestSecret("secret2", testNamespace, v1alpha1.PreservationModeClusterIdentity, true)
		secret3 = createTestSecret("secret3", testNamespace, v1alpha1.PreservationModeAll, true)
		secret4 = createTestSecret("secret4", testNamespace, v1alpha1.PreservationModeAll, true)

		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			WithObjects(clusterInstance, secret1, secret2, secret3, secret4).
			Build()

		handler = &ReinstallHandler{
			Client:          c,
			DeletionHandler: &deletion.DeletionHandler{Client: c, Logger: testLogger},
			Logger:          testLogger,
		}
	})

	It("should return immediately if PreservationMode is None", func() {
		clusterInstance.Spec.Reinstall.PreservationMode = v1alpha1.PreservationModeNone

		result, err := handler.ensureDataIsPreserved(ctx, testLogger, clusterInstance)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(reconcile.Result{Requeue: true}))
		cond := findReinstallStatusCondition(clusterInstance, v1alpha1.ReinstallPreservationDataBackedup)
		Expect(cond).ToNot(BeNil())
		Expect(cond.Reason).To(Equal(string(v1alpha1.PreservationNotRequired)))
	})

	It("should complete preservation successfully even if preservation was previously completed", func() {

		clusterInstance = &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testNamespace,
				Namespace: testNamespace,
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				Reinstall: &v1alpha1.ReinstallSpec{
					PreservationMode: v1alpha1.PreservationModeAll,
				},
			},
			Status: v1alpha1.ClusterInstanceStatus{
				Conditions: []metav1.Condition{},
				Reinstall: &v1alpha1.ReinstallStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(v1alpha1.ReinstallPreservationDataBackedup),
							Status: metav1.ConditionFalse,
							Reason: string(v1alpha1.Initialized),
						},
						{
							Type:   string(v1alpha1.ReinstallClusterIdentityDataDetected),
							Status: metav1.ConditionFalse,
							Reason: string(v1alpha1.Initialized),
						},
					},
				},
			},
		}

		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			WithObjects(clusterInstance, secret1, secret2, secret3, secret4).
			Build()

		handler = &ReinstallHandler{
			Client: c,
			Logger: testLogger,
		}

		repeatDataPreservationTest := 5
		for i := range repeatDataPreservationTest {
			result, err := handler.ensureDataIsPreserved(ctx, testLogger, clusterInstance)
			Expect(err).NotTo(HaveOccurred())
			if i == 0 {
				Expect(result).ToNot(BeZero())
			} else {
				Expect(result).To(BeZero())
			}

			cond := findReinstallStatusCondition(clusterInstance, v1alpha1.ReinstallPreservationDataBackedup)
			Expect(cond).ToNot(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(string(v1alpha1.Completed)))
			Expect(cond.Message).To(ContainSubstring("Number of resources preserved: 4"))

			cond = findReinstallStatusCondition(clusterInstance, v1alpha1.ReinstallClusterIdentityDataDetected)
			Expect(cond).ToNot(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(string(v1alpha1.DataAvailable)))
			Expect(cond.Message).To(ContainSubstring("Number of cluster identity resources detected: 2"))
		}
	})

	It("should handle errors during data preservation", func() {
		// Trigger a preservation failure by setting PreservationMode to ClusterIdentity and
		// do not create the resources to be preserved.
		clusterInstance = &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testNamespace,
				Namespace: testNamespace,
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				Reinstall: &v1alpha1.ReinstallSpec{
					PreservationMode: v1alpha1.PreservationModeClusterIdentity,
				},
			},
			Status: v1alpha1.ClusterInstanceStatus{
				Conditions: []metav1.Condition{},
				Reinstall: &v1alpha1.ReinstallStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(v1alpha1.ReinstallPreservationDataBackedup),
							Status: metav1.ConditionFalse,
							Reason: string(v1alpha1.InProgress),
						},
						{
							Type:   string(v1alpha1.ReinstallClusterIdentityDataDetected),
							Status: metav1.ConditionFalse,
							Reason: string(v1alpha1.InProgress),
						},
					},
				},
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

		result, err := handler.ensureDataIsPreserved(ctx, testLogger, clusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(result).To(Equal(reconcile.Result{Requeue: true}))

		cond := findReinstallStatusCondition(clusterInstance, v1alpha1.ReinstallPreservationDataBackedup)
		Expect(cond).ToNot(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(string(v1alpha1.DataUnavailable)))

		cond = findReinstallStatusCondition(clusterInstance, v1alpha1.ReinstallClusterIdentityDataDetected)
		Expect(cond).ToNot(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(string(v1alpha1.Failed)))
	})

})

var _ = Describe("ensurePreservedDataIsRestored", func() {
	var (
		ctx        context.Context
		c          client.Client
		testLogger *zap.Logger
		handler    *ReinstallHandler

		testNamespace = "test"

		clusterInstance *v1alpha1.ClusterInstance

		secret1, secret2, secret3, secret4 *corev1.Secret
	)

	BeforeEach(func() {
		ctx = context.Background()
		testLogger = zap.NewNop().Named("Test")

		clusterInstance = &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testNamespace,
				Namespace: testNamespace,
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				Reinstall: &v1alpha1.ReinstallSpec{
					PreservationMode: v1alpha1.PreservationModeAll,
				},
			},
			Status: v1alpha1.ClusterInstanceStatus{
				Conditions: []metav1.Condition{},
				Reinstall:  &v1alpha1.ReinstallStatus{},
			},
		}

		// Create preserved secrets
		secret1 = createTestSecret("secret1", testNamespace, v1alpha1.PreservationModeClusterIdentity, false)
		secret2 = createTestSecret("secret2", testNamespace, v1alpha1.PreservationModeClusterIdentity, false)
		secret3 = createTestSecret("secret3", testNamespace, v1alpha1.PreservationModeAll, false)
		secret4 = createTestSecret("secret4", testNamespace, v1alpha1.PreservationModeAll, false)

		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			WithObjects(clusterInstance, secret1, secret2, secret3, secret4).
			Build()

		handler = &ReinstallHandler{
			Client:          c,
			DeletionHandler: &deletion.DeletionHandler{Client: c, Logger: testLogger},
			Logger:          testLogger,
		}
	})

	It("should return immediately if PreservationMode is None", func() {
		clusterInstance.Spec.Reinstall.PreservationMode = v1alpha1.PreservationModeNone

		result, err := handler.ensurePreservedDataIsRestored(ctx, testLogger, clusterInstance)
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(reconcile.Result{Requeue: true}))
		cond := findReinstallStatusCondition(clusterInstance, v1alpha1.ReinstallPreservationDataRestored)
		Expect(cond.Reason).To(Equal(string(v1alpha1.PreservationNotRequired)))
	})

	It("should complete restoration successfully even if restoration was previously completed", func() {

		clusterInstance = &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testNamespace,
				Namespace: testNamespace,
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				Reinstall: &v1alpha1.ReinstallSpec{
					PreservationMode: v1alpha1.PreservationModeAll,
				},
			},
			Status: v1alpha1.ClusterInstanceStatus{
				Conditions: []metav1.Condition{},
				Reinstall: &v1alpha1.ReinstallStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(v1alpha1.ReinstallPreservationDataRestored),
							Status: metav1.ConditionFalse,
							Reason: string(v1alpha1.Initialized),
						},
					},
				},
			},
		}

		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			WithObjects(clusterInstance, secret1, secret2, secret3, secret4).
			Build()

		handler = &ReinstallHandler{
			Client: c,
			Logger: testLogger,
		}

		repeatDataRestorationTest := 5
		for i := range repeatDataRestorationTest {
			Expect(c.Get(ctx, client.ObjectKeyFromObject(clusterInstance), clusterInstance)).To(Succeed())
			result, err := handler.ensurePreservedDataIsRestored(ctx, testLogger, clusterInstance)
			Expect(err).NotTo(HaveOccurred())
			if i == 0 {
				Expect(result).ToNot(BeZero())
			} else {
				Expect(result).To(BeZero())
			}

			cond := findReinstallStatusCondition(clusterInstance, v1alpha1.ReinstallPreservationDataRestored)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To((Equal(metav1.ConditionTrue)))
			Expect(cond.Reason).To(Equal(string(v1alpha1.Completed)))
			Expect(cond.Message).To(ContainSubstring("4 resources were successfully restored: 2 cluster-identity resources, 2 other resources"))
		}
	})

	It("should return an error if restoration fails", func() {
		// Induce error by deleting preserved objects and setting PreservationMode=ClusterIdentity
		for _, object := range []client.Object{secret1, secret2, secret3, secret4} {
			Expect(c.Delete(ctx, object)).To(Succeed())
		}
		clusterInstance.Spec.Reinstall.PreservationMode = v1alpha1.PreservationModeClusterIdentity

		clusterInstance.Status.Reinstall.Conditions = []metav1.Condition{
			{
				Type:   string(v1alpha1.ReinstallPreservationDataRestored),
				Status: metav1.ConditionFalse,
				Reason: string(v1alpha1.InProgress),
			}}

		result, err := handler.ensurePreservedDataIsRestored(ctx, testLogger, clusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(result).To(Equal(reconcile.Result{Requeue: true}))

		cond := findReinstallStatusCondition(clusterInstance, v1alpha1.ReinstallPreservationDataRestored)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal(string(v1alpha1.Failed)))
	})

})
