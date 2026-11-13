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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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
				"labels": map[string]interface{}{ci.OwnedByLabel: ci.GenerateOwnedByLabelValue(cluster.Namespace, cluster.Name)},
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
				Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, object client.Object, _ ...client.GetOption) error {
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
		}, nil, "no valid spec.online"),
		Entry("unreadable managed fields", func() {
			current.SetManagedFields([]metav1.ManagedFieldsEntry{{
				Manager: ClusterInstanceFieldManager, Operation: metav1.ManagedFieldsOperationApply,
				FieldsV1: &metav1.FieldsV1{Raw: []byte(`{"f:spec":"not-a-field-map"}`)},
			}})
		}, nil, "failed to read BMH online field ownership"),
	)
})
