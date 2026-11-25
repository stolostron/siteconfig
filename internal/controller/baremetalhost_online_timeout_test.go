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
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
)

func TestBareMetalHostApplyDeadline(t *testing.T) {
	for _, stage := range []struct {
		name  string
		calls []string
	}{
		{name: "read", calls: []string{"read"}},
		{name: "handoff", calls: []string{"read", "handoff"}},
		{name: "apply", calls: []string{"read", "handoff", "apply"}},
	} {
		for _, boundedParent := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/parent-deadline-%t", stage.name, boundedParent), func(t *testing.T) {
				g := NewWithT(t)
				var parent context.Context
				var cancel context.CancelFunc
				if boundedParent {
					parent, cancel = context.WithTimeout(context.Background(), 5*time.Second)
				} else {
					parent, cancel = context.WithCancel(context.Background())
				}
				defer cancel()
				parentDeadline, _ := parent.Deadline()
				var operationDeadline time.Time
				var calls []string
				apiCall := func(ctx context.Context, name string) error {
					t.Helper()
					deadline, bounded := ctx.Deadline()
					g.Expect(bounded).To(BeTrue(), "every BMH API operation must have a finite deadline")
					g.Expect(time.Until(deadline)).To(BeNumerically(">", 0), "an API request must start within its budget")
					g.Expect(time.Until(deadline)).To(BeNumerically("<=", bmhApplyTimeout),
						"an unbounded parent must not leave a BMH request unbounded")
					if boundedParent {
						g.Expect(deadline).To(Equal(parentDeadline), "the operation must not extend an earlier parent deadline")
					}
					if operationDeadline.IsZero() {
						operationDeadline = deadline
					}
					g.Expect(deadline).To(Equal(operationDeadline), "read, handoff and apply must share one operation budget")
					calls = append(calls, name)
					if name == stage.name {
						cancel()
						g.Expect(ctx.Err()).To(MatchError(context.Canceled),
							"parent cancellation must propagate immediately to the active BMH API operation")
						return ctx.Err()
					}
					return nil
				}
				cluster := &v1alpha1.ClusterInstance{ObjectMeta: metav1.ObjectMeta{
					Name: "cluster", Namespace: "test", UID: types.UID("cluster-uid"),
				}}
				existing := &unstructured.Unstructured{Object: map[string]interface{}{
					"apiVersion": "metal3.io/v1alpha1", "kind": "BareMetalHost",
					"metadata": map[string]interface{}{
						"name": "host", "namespace": "test", "uid": "host-uid", "resourceVersion": "10",
						"labels": map[string]interface{}{ci.OwnedByLabel: ci.GenerateOwnedByLabelValue("test", "cluster")},
					},
					"spec": map[string]interface{}{"online": true},
				}}
				existing.SetManagedFields([]metav1.ManagedFieldsEntry{{
					Manager: ClusterInstanceFieldManager, Operation: metav1.ManagedFieldsOperationApply,
					FieldsV1: metav1.NewFieldsV1(`{"f:spec":{"f:online":{}}}`),
				}})
				desired := existing.DeepCopy()
				c := fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, _ client.WithWatch, _ client.ObjectKey,
						object client.Object, _ ...client.GetOption,
					) error {
						if err := apiCall(ctx, "read"); err != nil {
							return err
						}
						*object.(*unstructured.Unstructured) = *existing.DeepCopy()
						return nil
					},
					Apply: func(ctx context.Context, _ client.WithWatch, configuration runtime.ApplyConfiguration,
						_ ...client.ApplyOption,
					) error {
						if err := apiCall(ctx, "handoff"); err != nil {
							return err
						}
						object, ok := configuration.(client.Object)
						g.Expect(ok).To(BeTrue(), "the handoff must retain returned resource-version metadata")
						object.SetResourceVersion("11")
						return nil
					},
					Patch: func(ctx context.Context, _ client.WithWatch, _ client.Object,
						_ client.Patch, _ ...client.PatchOption,
					) error {
						return apiCall(ctx, "apply")
					},
				}).Build()
				r := &ClusterInstanceReconciler{Client: c, APIReader: c}
				result, err := r.applyRenderedObject(parent, c, zap.NewNop(), cluster, *desired, false)
				g.Expect(errors.Is(err, context.Canceled)).To(BeTrue(),
					"the cancelled API error must remain available to the reconciliation caller")
				g.Expect(result).To(Equal(controllerutil.OperationResultNone), "cancellation must not report a successful apply")
				g.Expect(calls).To(Equal(stage.calls), "cancellation must stop before any later host mutation")
			})
		}
	}
}
