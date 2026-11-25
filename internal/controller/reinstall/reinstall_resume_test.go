/*
Copyright 2026 Red Hat, Inc.

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
	"testing"
	"time"

	bmhv1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	. "github.com/onsi/gomega"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
	"github.com/stolostron/siteconfig/internal/controller/configuration"
	"github.com/stolostron/siteconfig/internal/controller/deletion"
)

func TestReinstallRetriesFailedTeardown(t *testing.T) {
	g := NewWithT(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	scheme := runtime.NewScheme()
	g.Expect(v1alpha1.AddToScheme(scheme)).To(Succeed(), "register the ClusterInstance status API")
	g.Expect(bmhv1.AddToScheme(scheme)).To(Succeed(), "register the host retained after failed teardown")
	// Preservation uses Secret and ConfigMap lists even when no data is retained.
	g.Expect(corev1.AddToScheme(scheme)).To(Succeed(), "register preservation resources for the real reinstall handler")
	instance := &v1alpha1.ClusterInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "test"},
		Spec: v1alpha1.ClusterInstanceSpec{Reinstall: &v1alpha1.ReinstallSpec{
			Generation: "attempt-2", PreservationMode: v1alpha1.PreservationModeNone,
		}},
		Status: v1alpha1.ClusterInstanceStatus{
			Reinstall: &v1alpha1.ReinstallStatus{
				ObservedGeneration: "attempt-1", InProgressGeneration: "attempt-2",
				RequestStartTime: metav1.Now(),
				Conditions: []metav1.Condition{*reinstallRequestProcessedConditionStatus(
					metav1.ConditionFalse, v1alpha1.InProgress, "Processing reinstall request")},
			},
			ManifestsRendered: []v1alpha1.ManifestReference{{
				APIGroup: ptr.To("metal3.io/v1alpha1"), Kind: "BareMetalHost", Name: "host", Namespace: "test",
				Status: v1alpha1.ManifestRenderedSuccess,
			}},
		},
	}
	host := &bmhv1.BareMetalHost{
		ObjectMeta: metav1.ObjectMeta{
			Name: "host", Namespace: "test", Finalizers: []string{"example.com/hold-delete"},
			Labels: map[string]string{ci.OwnedByLabel: ci.GenerateOwnedByLabelValue("test", "cluster")},
		},
		Spec: bmhv1.BareMetalHostSpec{Online: true},
	}
	deleteFailure := errors.New("injected failure before the host delete reaches the API")
	failDeletion, deleteCalls := true, 0
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&v1alpha1.ClusterInstance{}).WithObjects(instance, host).
		WithInterceptorFuncs(interceptor.Funcs{
			Delete: func(ctx context.Context, base client.WithWatch, object client.Object, opts ...client.DeleteOption) error {
				if client.ObjectKeyFromObject(object) == client.ObjectKeyFromObject(host) {
					deleteCalls++
					if failDeletion {
						return deleteFailure
					}
				}
				return base.Delete(ctx, object, opts...)
			},
		}).Build()
	store, err := configuration.NewConfigurationStore(configuration.NewDefaultConfiguration())
	g.Expect(err).ToNot(HaveOccurred(), "initialize reinstall configuration")
	store.SetAllowReinstalls(true)
	log := zap.NewNop()
	handler := &ReinstallHandler{Client: c, ConfigStore: store, Logger: log,
		DeletionHandler: &deletion.DeletionHandler{Client: c, Logger: log}}
	reload := func() {
		t.Helper()
		g.Expect(c.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed(), "read persisted reinstall state")
	}
	runUntilPaused := func() {
		t.Helper()
		for range 16 {
			_, err := handler.ProcessRequest(ctx, instance)
			if err != nil {
				g.Expect(errors.Is(err, deleteFailure)).To(BeTrue(), "only the injected teardown failure is expected")
			}
			reload()
			g.Expect(instance.Status.Reinstall.ObservedGeneration).To(Equal("attempt-1"),
				"a failed teardown must not authorize the template to initialize power on the surviving host")
			if instance.IsPaused() {
				break
			}
		}
		g.Expect(instance.IsPaused()).To(BeTrue(), "a failed delete must pause the same reinstall request")
		condition := findReinstallStatusCondition(instance, v1alpha1.ReinstallRenderedManifestsDeleted)
		g.Expect(condition).ToNot(BeNil(), "the failed teardown must be recorded for resume")
		g.Expect(condition.Reason).To(Equal(string(v1alpha1.Failed)), "the delete failure must remain visible")
		g.Expect(instance.Status.Reinstall.RequestEndTime.IsZero()).To(BeTrue(), "the paused request must remain resumable")
		g.Expect(c.Get(ctx, client.ObjectKeyFromObject(host), host)).To(Succeed(), "the failed delete must retain the host")
		g.Expect(host.Spec.Online).To(BeTrue(), "the retained running host must not have its power default reapplied")
	}
	runUntilPaused()
	g.Expect(deleteCalls).To(Equal(1), "the first failed request must attempt host deletion")
	g.Expect(ci.RemovePause(ctx, c, log, instance)).To(Succeed(), "resume the failed request without changing generation")
	runUntilPaused()
	g.Expect(deleteCalls).To(Equal(2), "resume must retry the failed delete instead of treating it as complete")

	failDeletion = false
	g.Expect(ci.RemovePause(ctx, c, log, instance)).To(Succeed(), "resume again after the delete API recovers")
	for range 2 {
		_, err := handler.ProcessRequest(ctx, instance)
		g.Expect(err).ToNot(HaveOccurred(), "a recovered delete may wait for the host finalizer")
		reload()
		g.Expect(instance.Status.Reinstall.ObservedGeneration).To(Equal("attempt-1"),
			"accepted deletion is not completed teardown while the host finalizer remains")
	}
	g.Expect(c.Get(ctx, client.ObjectKeyFromObject(host), host)).To(Succeed(), "observe the terminating host")
	g.Expect(host.DeletionTimestamp.IsZero()).To(BeFalse(), "the resumed request must actually issue host deletion")
	host.SetFinalizers(nil)
	g.Expect(c.Update(ctx, host)).To(Succeed(), "simulate completion of the host cleanup finalizer")
	g.Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(host), &bmhv1.BareMetalHost{}))).To(BeTrue(),
		"the original host must be absent before the reinstall generation can advance")
	for range 16 {
		_, err := handler.ProcessRequest(ctx, instance)
		g.Expect(err).ToNot(HaveOccurred(), "finish teardown and restoration after the host has disappeared")
		reload()
		if instance.Status.Reinstall.ObservedGeneration == "attempt-2" {
			break
		}
	}
	g.Expect(instance.Status.Reinstall.ObservedGeneration).To(Equal("attempt-2"),
		"only successful teardown and restoration may authorize the new installation")
	condition := findReinstallStatusCondition(instance, v1alpha1.ReinstallRenderedManifestsDeleted)
	g.Expect(condition.Status).To(Equal(metav1.ConditionTrue), "finalization requires positively completed deletion")
	g.Expect(condition.Reason).To(Equal(string(v1alpha1.Completed)), "the failed deletion must be replaced by completion")
	g.Expect(instance.Status.Reinstall.RequestEndTime.IsZero()).To(BeFalse(), "the successfully resumed request must end")
	g.Expect(deleteCalls).To(Equal(3), "waiting for deletion must not issue repeated delete requests")
}
