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

package controller

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/zap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
)

var _ = Describe("BareMetalHost apply safety", func() {
	var (
		cluster *v1alpha1.ClusterInstance
		desired *unstructured.Unstructured
		current *unstructured.Unstructured
	)
	BeforeEach(func() {
		cluster = &v1alpha1.ClusterInstance{ObjectMeta: metav1.ObjectMeta{
			Name: "cluster", Namespace: "test", UID: types.UID("cluster-uid"),
		}}
		desired = &unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "metal3.io/v1alpha1", "kind": "BareMetalHost",
			"metadata": map[string]interface{}{
				"name": "node", "namespace": cluster.Namespace,
				"labels": map[string]interface{}{
					ci.OwnedByLabel: ci.GenerateOwnedByLabelValue(cluster.Namespace, cluster.Name),
				},
			},
			"spec": map[string]interface{}{"online": false},
		}}
		current = desired.DeepCopy()
		current.SetUID(types.UID("host-uid"))
	})

	DescribeTable("refuses unsafe changes without writing",
		func(prepare func(), readError error, expected string) {
			prepare()
			writes := 0
			c := fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
				Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey,
					object client.Object, _ ...client.GetOption,
				) error {
					if readError != nil {
						return readError
					}
					*object.(*unstructured.Unstructured) = *current.DeepCopy()
					return nil
				},
				Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
					writes++
					return errors.New("unexpected write")
				},
				Apply: func(context.Context, client.WithWatch, runtime.ApplyConfiguration, ...client.ApplyOption) error {
					writes++
					return errors.New("unexpected ownership handoff")
				},
			}).Build()
			r := &ClusterInstanceReconciler{Client: c, APIReader: c}
			result, err := r.applyRenderedObject(context.Background(), c, zap.NewNop(), cluster, *desired, false)
			Expect(err).To(HaveOccurred(),
				"an unsafe BMH apply must be rejected before the resource can be modified")
			Expect(err.Error()).To(ContainSubstring(expected),
				"the rejection must identify the failed safety check so its cause can be diagnosed")
			if readError != nil {
				Expect(errors.Is(err, readError)).To(BeTrue(),
					"the underlying read error must remain discoverable so callers can diagnose and retry the failure")
			}
			if _, _, fieldError := unstructured.NestedBool(current.Object, "spec", "online"); fieldError != nil {
				Expect(errors.Unwrap(err)).To(MatchError(fieldError),
					"a malformed power value must retain its type error so the bad persisted data can be diagnosed")
			}
			Expect(result).To(Equal(controllerutil.OperationResultNone),
				"a rejected BMH apply must not report a successful resource change")
			Expect(writes).To(BeZero(),
				"a rejected apply must not patch the BMH because a write can change spec.online")
		},
		Entry("deleting host", func() {
			current.SetFinalizers([]string{"example.com/finalizer"})
			timestamp := metav1.Now()
			current.SetDeletionTimestamp(&timestamp)
		}, nil, "cannot apply deleting BMH"),
		Entry("host owned by a different ClusterInstance", func() {
			current.SetLabels(map[string]string{ci.OwnedByLabel: "another_cluster"})
		}, nil, "belongs to another ClusterInstance"),
		Entry("read error", func() {}, errors.New("API unavailable"), "failed to read BMH"),
		Entry("missing ClusterInstance identity", func() {
			cluster.UID = ""
		}, nil, "without a ClusterInstance UID"),
		Entry("rendered owner mismatch", func() {
			desired.SetLabels(nil)
		}, nil, "rendered BMH does not belong"),
		Entry("missing online value on an initialized host", func() {
			unstructured.RemoveNestedField(current.Object, "spec", "online")
		}, nil, "has no spec.online value"),
		Entry("malformed online value on an initialized host", func() {
			current.Object["spec"].(map[string]interface{})["online"] = "false"
		}, nil, "failed to read existing BMH spec.online"),
		Entry("unreadable managed fields", func() {
			current.SetManagedFields([]metav1.ManagedFieldsEntry{{
				Manager: ClusterInstanceFieldManager, Operation: metav1.ManagedFieldsOperationApply,
				FieldsV1: metav1.NewFieldsV1(`{"f:spec":"not-a-field-map"}`),
			}})
		}, nil, "failed to read BMH online field ownership"),
	)

	type lifecycleCase struct {
		previousLifecycle   string
		reinstall           string
		observedReinstall   string
		failedReason        v1alpha1.ClusterInstanceConditionReason
		expectedLifecycle   string
		restore             string
		handledRestore      string
		restoreAcknowledged bool
		unallocated         bool
	}
	DescribeTable("recognizes the installation that initialized power",
		func(tc lifecycleCase, expected bool) {
			current.SetAnnotations(map[string]string{bmhOnlineInitializedAnnotation: tc.previousLifecycle})
			if tc.unallocated {
				current.SetLabels(nil)
			}
			if tc.reinstall != "" {
				cluster.Spec.Reinstall = &v1alpha1.ReinstallSpec{Generation: tc.reinstall}
			}
			if tc.observedReinstall != "" || tc.failedReason != "" {
				cluster.Status.Reinstall = &v1alpha1.ReinstallStatus{ObservedGeneration: tc.observedReinstall}
			}
			if tc.failedReason != "" {
				cluster.Status.Reinstall.InProgressGeneration = tc.reinstall
				cluster.Status.Reinstall.RequestEndTime = metav1.Now()
				cluster.Status.Reinstall.Conditions = []metav1.Condition{{
					Type: string(v1alpha1.ReinstallRequestProcessed), Status: metav1.ConditionFalse,
					Reason: string(tc.failedReason),
				}}
			}
			cluster.Labels = map[string]string{veleroRestoreNameLabel: tc.restore}
			cluster.Annotations = map[string]string{veleroRestoreHandledAnnotation: tc.handledRestore}
			if tc.restoreAcknowledged {
				annotations := current.GetAnnotations()
				annotations[bmhOnlineRestoreAnnotation] = string(cluster.UID) + ":" + tc.restore
				current.SetAnnotations(annotations)
			}
			lifecycle := bareMetalHostOnlineLifecycle(cluster, tc.previousLifecycle)
			prepared := desired.DeepCopy()
			prepared.SetAnnotations(map[string]string{bmhOnlineInitializedAnnotation: lifecycle})
			Expect(bareMetalHostOnlineInitialized(current, prepared, cluster)).To(Equal(expected),
				"only a confirmed new installation or association may reset the current power state")
			if tc.expectedLifecycle != "" {
				Expect(lifecycle).To(Equal(tc.expectedLifecycle),
					"an unprocessed reinstall must retain the last initialized installation in the marker")
			}
		},
		Entry("legacy assigned host", lifecycleCase{}, true),
		Entry("current installation", lifecycleCase{previousLifecycle: "cluster-uid"}, true),
		Entry("current reinstall", lifecycleCase{
			previousLifecycle: "cluster-uid:install-1", reinstall: "install-1", observedReinstall: "install-1",
		}, true),
		Entry("inventory returned to the pool", lifecycleCase{
			previousLifecycle: "cluster-uid", unallocated: true,
		}, false),
		Entry("new ClusterInstance", lifecycleCase{previousLifecycle: "old-cluster-uid"}, false),
		Entry("successfully processed new reinstall", lifecycleCase{
			previousLifecycle: "cluster-uid:install-1", reinstall: "install-2", observedReinstall: "install-2",
		}, false),
		Entry("requested first reinstall", lifecycleCase{
			previousLifecycle: "cluster-uid", reinstall: "install-1", expectedLifecycle: "cluster-uid",
		}, true),
		Entry("requested next reinstall", lifecycleCase{
			previousLifecycle: "cluster-uid:install-1", reinstall: "install-2", observedReinstall: "install-1",
			expectedLifecycle: "cluster-uid:install-1",
		}, true),
		Entry("rejected first reinstall", lifecycleCase{
			previousLifecycle: "cluster-uid", reinstall: "install-1", failedReason: v1alpha1.Failed,
			expectedLifecycle: "cluster-uid",
		}, true),
		Entry("rejected next reinstall", lifecycleCase{
			previousLifecycle: "cluster-uid:install-1", reinstall: "install-2", observedReinstall: "install-1",
			failedReason: v1alpha1.Failed, expectedLifecycle: "cluster-uid:install-1",
		}, true),
		Entry("timed-out next reinstall", lifecycleCase{
			previousLifecycle: "cluster-uid:install-1", reinstall: "install-2", observedReinstall: "install-1",
			failedReason: v1alpha1.TimedOut, expectedLifecycle: "cluster-uid:install-1",
		}, true),
		Entry("lost reinstall status", lifecycleCase{
			previousLifecycle: "cluster-uid:install-1", reinstall: "install-2",
			expectedLifecycle: "cluster-uid:install-1",
		}, true),
		Entry("restore not yet handled", lifecycleCase{
			previousLifecycle: "old-cluster-uid", restore: "restore-1",
		}, false),
		Entry("different restore handled", lifecycleCase{
			previousLifecycle: "old-cluster-uid", restore: "restore-2", handledRestore: "restore-1",
		}, false),
		Entry("handled restore without a reinstall", lifecycleCase{
			previousLifecycle: "old-cluster-uid", restore: "restore-1", handledRestore: "restore-1",
		}, true),
		Entry("handled restore with the same reinstall", lifecycleCase{
			previousLifecycle: "old-cluster-uid:install-1", reinstall: "install-1", observedReinstall: "install-1",
			restore: "restore-1", handledRestore: "restore-1",
		}, true),
		Entry("handled restore with a subsequent new reinstall", lifecycleCase{
			previousLifecycle: "cluster-uid:install-1", reinstall: "install-2", observedReinstall: "install-2",
			restore: "restore-1", handledRestore: "restore-1", restoreAcknowledged: true,
		}, false),
		Entry("handled restore from an older backup", lifecycleCase{
			previousLifecycle: "old-cluster-uid:later-install", reinstall: "backup-install",
			observedReinstall: "backup-install", restore: "restore-1", handledRestore: "restore-1",
		}, true),
		Entry("in-place handled restore from an older backup", lifecycleCase{
			previousLifecycle: "cluster-uid:later-install", reinstall: "backup-install",
			observedReinstall: "backup-install", restore: "restore-1", handledRestore: "restore-1",
		}, true),
		Entry("handled restore without reinstall status", lifecycleCase{
			previousLifecycle: "old-cluster-uid:install-1", reinstall: "install-1",
			restore: "restore-1", handledRestore: "restore-1", expectedLifecycle: "cluster-uid:install-1",
		}, true),
		Entry("handled restore without status after an unprocessed request", lifecycleCase{
			previousLifecycle: "old-cluster-uid:install-1", reinstall: "install-2",
			restore: "restore-1", handledRestore: "restore-1", expectedLifecycle: "cluster-uid:install-1",
		}, true),
	)

	// These tests verify the API requests and failure boundaries. Envtest covers
	// the API server's actual field-ownership and optimistic-locking behavior.
	DescribeTable("preserves power during an unrelated host update",
		func(online, ownsOnline, dryRun bool, handoffError error) {
			current.Object["spec"].(map[string]interface{})["online"] = online
			current.SetResourceVersion("10")
			operation := metav1.ManagedFieldsOperationUpdate
			if ownsOnline {
				operation = metav1.ManagedFieldsOperationApply
			}
			current.SetManagedFields([]metav1.ManagedFieldsEntry{{
				Manager: ClusterInstanceFieldManager, Operation: operation,
				FieldsV1: metav1.NewFieldsV1(`{"f:spec":{"f:online":{}}}`),
			}})
			desired.SetAnnotations(map[string]string{"example.com/note": "updated"})
			original := desired.DeepCopy()
			writes := 0
			c := fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
				Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey,
					object client.Object, _ ...client.GetOption,
				) error {
					*object.(*unstructured.Unstructured) = *current.DeepCopy()
					return nil
				},
				Apply: func(_ context.Context, _ client.WithWatch, configuration runtime.ApplyConfiguration,
					opts ...client.ApplyOption,
				) error {
					writes++
					options := (&client.ApplyOptions{}).ApplyOptions(opts)
					object, ok := configuration.(client.Object)
					Expect(ok).To(BeTrue(), "handoff must retain access to the returned object metadata")
					Expect(options.FieldManager).To(Equal(bmhInitialPowerFieldManager),
						"only the initial-power manager may share online ownership")
					Expect(writes).To(Equal(1), "ownership must be shared before the normal manager omits online")
					Expect(options.Force).To(BeNil(), "handoff must not force ownership away from a power controller")
					Expect(object.GetUID()).To(Equal(current.GetUID()), "handoff must identify the host read before apply")
					Expect(object.GetResourceVersion()).To(Equal("10"), "handoff must reject a concurrent host update")
					content, ok := configuration.(runtime.Unstructured)
					Expect(ok).To(BeTrue(), "handoff must send only the explicitly supplied unstructured fields")
					Expect(content.UnstructuredContent()["spec"]).To(Equal(map[string]interface{}{"online": online}),
						"handoff must write only the current power state")
					if handoffError != nil {
						return handoffError
					}
					object.SetResourceVersion("11")
					return nil
				},
				Patch: func(_ context.Context, _ client.WithWatch, object client.Object,
					patch client.Patch, opts ...client.PatchOption,
				) error {
					writes++
					options := (&client.PatchOptions{}).ApplyOptions(opts)
					Expect(patch.Type()).To(Equal(types.ApplyPatchType), "host writes must use SSA")
					Expect(object.GetUID()).To(Equal(current.GetUID()), "writes must identify the host read before the apply")
					spec := object.(*unstructured.Unstructured).Object["spec"]

					Expect(options.FieldManager).To(Equal(ClusterInstanceFieldManager), "metadata updates must use the normal manager")
					expectedVersion := "10"
					if ownsOnline && !dryRun {
						expectedVersion = "11"
					}
					Expect(object.GetResourceVersion()).To(Equal(expectedVersion),
						"the apply must guard against writes after the handoff")
					if dryRun {
						Expect(spec).To(HaveKeyWithValue("online", online), "validation must retain the current power value")
					} else {
						Expect(spec).ToNot(HaveKey("online"), "updates must leave power control to its current owner")
					}
					Expect(object.GetAnnotations()).To(HaveKeyWithValue("example.com/note", "updated"),
						"metadata changes must still apply")
					Expect(object.GetAnnotations()).To(HaveKeyWithValue(bmhOnlineInitializedAnnotation, "cluster-uid"),
						"legacy hosts must record adoption")
					return nil
				},
			}).Build()
			r := &ClusterInstanceReconciler{Client: c, APIReader: c}
			result, err := r.applyRenderedObject(context.Background(), c, zap.NewNop(), cluster, *desired, dryRun)
			if handoffError != nil {
				Expect(err).To(MatchError(ContainSubstring("failed to hand off")), "handoff failure must prevent the final apply")
				Expect(apierrors.IsConflict(err)).To(BeTrue(), "a handoff conflict must remain retryable")
				Expect(result).To(Equal(controllerutil.OperationResultNone), "a failed handoff must not report an applied host")
				Expect(writes).To(Equal(1), "a failed handoff must stop before any online omission")
			} else {
				Expect(err).ToNot(HaveOccurred(), "an unrelated host update must preserve the current power state")
				Expect(result).To(Equal(controllerutil.OperationResultUpdated), "a successful metadata update must be reported")
				expectedWrites := 1
				if ownsOnline && !dryRun {
					expectedWrites = 2
				}
				Expect(writes).To(Equal(expectedWrites), "only an active SSA owner requires a separate handoff")
			}
			Expect(desired.Object).To(Equal(original.Object), "applying a host must not mutate the rendered template")
		},
		Entry("legacy Update ownership", true, false, false, nil),
		Entry("active SSA ownership", true, true, false, nil),
		Entry("dry-run with legacy ownership", true, false, true, nil),
		Entry("dry-run with active SSA ownership", true, true, true, nil),
		Entry("powered-off host with legacy ownership", false, false, false, nil),
		Entry("powered-off host with active SSA ownership", false, true, false, nil),
		Entry("powered-off dry-run with legacy ownership", false, false, true, nil),
		Entry("powered-off dry-run with active SSA ownership", false, true, true, nil),
		Entry("concurrent write during handoff", true, true, false,
			apierrors.NewConflict(schema.GroupResource{Group: "metal3.io", Resource: "baremetalhosts"},
				"node", errors.New("host changed"))),
	)
})
