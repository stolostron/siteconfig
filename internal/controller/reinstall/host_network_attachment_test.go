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

	bmo "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stolostron/siteconfig/api/v1alpha1"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
	"github.com/stolostron/siteconfig/internal/controller/deletion"
	"go.uber.org/zap"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

var _ = Describe("Host network attachment reinstall cleanup", func() {
	ctx := context.Background()
	log := zap.NewNop()
	var (
		host             *bmo.BareMetalHost
		managed          *bmo.HostNetworkAttachment
		external         *bmo.HostNetworkAttachment
		originalExternal *bmo.HostNetworkAttachment
		cluster          *v1alpha1.ClusterInstance
		deleted          []string
		c                client.Client
		handler          *ReinstallHandler
	)

	BeforeEach(func() {
		testScheme := runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(testScheme)).To(Succeed(),
			"register the ClusterInstance API for tracking reinstall status")
		Expect(bmo.AddToScheme(testScheme)).To(Succeed(),
			"register the BareMetalHost and HostNetworkAttachment APIs for cleanup")
		owner := ci.GenerateOwnedByLabelValue("cluster", "cluster")
		host = &bmo.BareMetalHost{ObjectMeta: metav1.ObjectMeta{Name: "host", Namespace: "cluster",
			Labels: map[string]string{ci.OwnedByLabel: owner}, Finalizers: []string{"test.example.com/bmh-cleanup"}},
			Spec: bmo.BareMetalHostSpec{NetworkInterfaces: []bmo.NetworkInterface{
				{Name: "eno1", HostNetworkAttachment: bmo.HostNetworkAttachmentRef{Name: "managed-net", Namespace: "cluster"}},
				{Name: "eno2", HostNetworkAttachment: bmo.HostNetworkAttachmentRef{Name: "external-net", Namespace: "shared"}},
			}},
		}
		managed = &bmo.HostNetworkAttachment{ObjectMeta: metav1.ObjectMeta{Name: "managed-net", Namespace: "cluster", Labels: map[string]string{ci.OwnedByLabel: owner}}}
		external = &bmo.HostNetworkAttachment{ObjectMeta: metav1.ObjectMeta{Name: "external-net", Namespace: "shared", Labels: map[string]string{"owner": "external"}}}
		cluster = &v1alpha1.ClusterInstance{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "cluster"},
			Spec: v1alpha1.ClusterInstanceSpec{Reinstall: &v1alpha1.ReinstallSpec{Generation: "new-generation"}},
			Status: v1alpha1.ClusterInstanceStatus{Reinstall: &v1alpha1.ReinstallStatus{}, ManifestsRendered: []v1alpha1.ManifestReference{
				{APIGroup: ptr.To("metal3.io/v1alpha1"), Kind: "HostNetworkAttachment", Name: "managed-net", Namespace: "cluster", SyncWave: 0, Status: v1alpha1.ManifestRenderedSuccess},
				{APIGroup: ptr.To("metal3.io/v1alpha1"), Kind: "BareMetalHost", Name: "host", Namespace: "cluster", SyncWave: 3, Status: v1alpha1.ManifestRenderedSuccess},
			}},
		}
		setReinstallStatusCondition(cluster, *reinstallRenderedManifestsDeletedConditionStatus(metav1.ConditionFalse, v1alpha1.InProgress, "Cleanup in progress"))
		deleted = nil
		c = fakeclient.NewClientBuilder().WithScheme(testScheme).WithStatusSubresource(cluster).
			WithObjects(cluster, host, managed, external).WithInterceptorFuncs(interceptor.Funcs{
			Delete: func(ctx context.Context, base client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				deleted = append(deleted, obj.GetName())
				return base.Delete(ctx, obj, opts...)
			},
		}).Build()
		Expect(c.Get(ctx, client.ObjectKeyFromObject(external), external)).To(Succeed(),
			"load the external HNA before reinstall to compare its persisted state")
		originalExternal = external.DeepCopy()
		handler = &ReinstallHandler{Client: c, Logger: log, DeletionHandler: &deletion.DeletionHandler{Client: c, Logger: log}}
	})

	It("waits for BMH cleanup and leaves external attachments untouched", func() {
		for range 2 {
			result, err := handler.ensureRenderedManifestsAreDeleted(ctx, log, cluster)
			Expect(err).NotTo(HaveOccurred(), "reinstall cleanup must tolerate a BMH with pending finalization")
			Expect(result.IsZero()).To(BeFalse(), "pending BMH cleanup must be requeued")
			Expect(findReinstallStatusCondition(cluster, v1alpha1.ReinstallRenderedManifestsDeleted).Reason).To(Equal(string(v1alpha1.InProgress)),
				"reinstall must remain in progress until the BMH is gone")
			Expect(c.Get(ctx, client.ObjectKeyFromObject(managed), managed)).To(Succeed(),
				"the managed attachment must remain available to the BMH during cleanup")
			Expect(managed.DeletionTimestamp.IsZero()).To(BeTrue(),
				"the attachment must not receive a deletion request while the BMH is still present")
		}
		Expect(deleted).To(Equal([]string{"host"}),
			"only the BMH may receive a deletion request before its finalizer clears")
		Expect(c.Get(ctx, client.ObjectKeyFromObject(host), host)).To(Succeed(),
			"the BMH must still exist until its finalizer clears")
		host.Finalizers = nil
		Expect(c.Update(ctx, host)).To(Succeed(),
			"persist removal of the BMH finalizer to simulate completed host cleanup")
		_, err := handler.ensureRenderedManifestsAreDeleted(ctx, log, cluster)
		Expect(err).NotTo(HaveOccurred(), "cleanup must proceed once the BMH has finished deleting")
		Expect(deleted).To(Equal([]string{"host", "managed-net"}),
			"reinstall must delete the managed HNA after the BMH and leave the external HNA alone")
		Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(managed), managed))).To(BeTrue(),
			"the managed HNA must be gone after BMH cleanup completes")
		_, err = handler.ensureRenderedManifestsAreDeleted(ctx, log, cluster)
		Expect(err).NotTo(HaveOccurred(), "the final confirmation pass must complete reinstall cleanup")
		Expect(findReinstallStatusCondition(cluster, v1alpha1.ReinstallRenderedManifestsDeleted).Reason).To(Equal(string(v1alpha1.Completed)),
			"reinstall cleanup must report completion after both managed resources are gone")
		Expect(cluster.Status.ManifestsRendered).To(BeEmpty(),
			"reinstall cleanup must remove the deleted resources from the inventory")
		Expect(c.Get(ctx, client.ObjectKeyFromObject(external), external)).To(Succeed(),
			"the external HNA must still exist after reinstall cleanup")
		Expect(external).To(Equal(originalExternal), "the external HNA must retain its original metadata and spec")
	})
})
