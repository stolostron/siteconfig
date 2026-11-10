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

	bmo "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stolostron/siteconfig/api/v1alpha1"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
	"github.com/stolostron/siteconfig/internal/controller/deletion"
	aiTemplates "github.com/stolostron/siteconfig/internal/templates/assisted-installer"
	hcpTemplates "github.com/stolostron/siteconfig/internal/templates/hosted-cluster"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

var _ = Describe("Host network attachment reconciliation", func() {
	var (
		ctx        = context.Background()
		log        = zap.NewNop()
		c          client.WithWatch
		r          *ClusterInstanceReconciler
		cluster    *v1alpha1.ClusterInstance
		operations []string
		missingCRD bool
	)
	BeforeEach(func() {
		operations = nil
		missingCRD = false
		c = interceptor.NewClient(createSSAMockClient().(client.WithWatch), interceptor.Funcs{
			Patch: func(ctx context.Context, base client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				if patch.Type() == "application/apply-patch+yaml" {
					options := &client.PatchOptions{}
					for _, opt := range opts {
						opt.ApplyToPatch(options)
					}
					identity := obj.GetObjectKind().GroupVersionKind().Kind + "/" + obj.GetName()
					if len(options.DryRun) > 0 {
						operations = append(operations, "validate:"+identity)
						if missingCRD && identity == "HostNetworkAttachment/storage-net" {
							return &meta.NoKindMatchError{
								GroupKind:        schema.GroupKind{Group: "metal3.io", Kind: "HostNetworkAttachment"},
								SearchedVersions: []string{"v1alpha1"},
							}
						}
						return nil
					}
					operations = append(operations, "apply:"+identity)
				}
				return base.Patch(ctx, obj, patch, opts...)
			},
			Delete: func(ctx context.Context, base client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				operations = append(operations, "delete:"+obj.GetName())
				return base.Delete(ctx, obj, opts...)
			},
		})
		r = &ClusterInstanceReconciler{Client: c, Log: log, TmplEngine: ci.NewTemplateEngine(),
			DeletionHandler: &deletion.DeletionHandler{Client: c, Logger: log}}
		cluster = &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{Name: "hna-cluster", Namespace: "hna-cluster", Finalizers: []string{clusterInstanceFinalizer}},
			Spec: v1alpha1.ClusterInstanceSpec{ClusterName: "hna-cluster", ClusterImageSetNameRef: "release",
				TemplateRefs: []v1alpha1.TemplateRef{{Name: "cluster-templates", Namespace: "hna-cluster"}},
				HostNetworkAttachmentTemplates: []v1alpha1.HostNetworkAttachmentTemplate{{Name: "storage-net",
					Spec: bmo.HostNetworkAttachmentSpec{Mode: bmo.SwitchportModeAccess, NativeVLAN: 100}}},
				Nodes: []v1alpha1.NodeSpec{{HostName: "host", Role: "master",
					TemplateRefs:      []v1alpha1.TemplateRef{{Name: "node-templates", Namespace: "hna-cluster"}},
					NetworkInterfaces: []bmo.NetworkInterface{{Name: "eno1", HostNetworkAttachment: bmo.HostNetworkAttachmentRef{Name: "storage-net"}}}}},
			},
		}
		templates := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cluster-templates", Namespace: cluster.Namespace},
			Data: map[string]string{"HostNetworkAttachment": aiTemplates.HostNetworkAttachment}}
		nodeTemplates := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "node-templates", Namespace: cluster.Namespace},
			Data: map[string]string{"BareMetalHost": aiTemplates.BareMetalHost}}
		Expect(c.Create(ctx, ci.GetMockClusterImageSet("release", "image"))).To(Succeed(),
			"create the image set referenced by the ClusterInstance fixture")
		Expect(c.Create(ctx, templates)).To(Succeed(),
			"create the cluster template ConfigMap that renders the managed HNA")
		Expect(c.Create(ctx, nodeTemplates)).To(Succeed(),
			"create the node template ConfigMap that renders the BareMetalHost")
		Expect(c.Create(ctx, cluster)).To(Succeed(), "persist the ClusterInstance before reconciliation")
	})

	It("validates all resources before applying and retries once the HNA CRD is available", func() {
		missingCRD = true
		ok, err := r.handleRenderTemplates(ctx, log, cluster)
		Expect(err).To(HaveOccurred(), "the missing HNA CRD must surface as a dry-run validation error")
		Expect(ok).To(BeFalse(), "a failed HNA dry run must prevent the apply phase")
		Expect(operations).To(Equal([]string{"validate:HostNetworkAttachment/storage-net", "validate:BareMetalHost/host"}),
			"both manifests must be dry-run validated in wave order before any live writes")
		Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKey{Name: "host", Namespace: cluster.Namespace},
			&bmo.BareMetalHost{}))).To(BeTrue(), "failed validation must not create the host")
		Expect(c.Get(ctx, client.ObjectKeyFromObject(cluster), cluster)).To(Succeed(),
			"reload the ClusterInstance to verify persisted validation status")
		condition := meta.FindStatusCondition(cluster.Status.Conditions, string(v1alpha1.RenderedTemplatesValidated))
		Expect(condition).NotTo(BeNil(), "a failed dry run must set the validation condition")
		Expect(condition.Status).To(Equal(metav1.ConditionFalse),
			"the validation condition must report that the manifests failed validation")
		Expect(condition.Reason).To(Equal(string(v1alpha1.Failed)),
			"the validation condition must record the failure reason")
		Expect(cluster.Status.ManifestsRendered).To(HaveLen(2),
			"validation must track both rendered manifests for reporting and cleanup")
		found := false
		for _, manifest := range cluster.Status.ManifestsRendered {
			if manifest.Kind == "HostNetworkAttachment" {
				found = true
				Expect(manifest.Name).To(Equal("storage-net"),
					"the failed manifest status must identify the managed attachment")
				Expect(manifest.Status).To(Equal(v1alpha1.ManifestRenderedFailure),
					"the HNA dry-run rejection must be recorded on the attachment")
				Expect(manifest.Message).NotTo(BeEmpty(),
					"the attachment status must explain why validation failed")
			}
		}
		Expect(found).To(BeTrue(), "the failed attachment must appear in the manifest inventory")
		missingCRD = false
		operations = nil
		ok, err = r.handleRenderTemplates(ctx, log, cluster)
		Expect(err).NotTo(HaveOccurred(), "validation must succeed on retry once the HNA CRD is available")
		Expect(ok).To(BeTrue(), "reconciliation must complete after successful validation and apply")
		Expect(operations).To(Equal([]string{"validate:HostNetworkAttachment/storage-net", "validate:BareMetalHost/host",
			"apply:HostNetworkAttachment/storage-net", "apply:BareMetalHost/host"}),
			"the retry must validate both resources before applying the HNA and then the BMH")
		Expect(meta.IsStatusConditionTrue(cluster.Status.Conditions, string(v1alpha1.RenderedTemplatesValidated))).To(BeTrue(),
			"a successful retry must clear the validation failure condition")
	})

	DescribeTable("waits for BMH finalizers before deleting a freely deletable HNA", func(bmhTemplate string) {
		nodeTemplates := &corev1.ConfigMap{}
		Expect(c.Get(ctx, client.ObjectKey{Name: "node-templates", Namespace: cluster.Namespace}, nodeTemplates)).To(Succeed(),
			"load the node template ConfigMap before selecting an installer template")
		nodeTemplates.Data["BareMetalHost"] = bmhTemplate
		Expect(c.Update(ctx, nodeTemplates)).To(Succeed(),
			"persist the selected installer template before rendering")
		ok, err := r.handleRenderTemplates(ctx, log, cluster)
		Expect(err).NotTo(HaveOccurred(), "render and apply the managed HNA and BMH before testing cleanup")
		Expect(ok).To(BeTrue(), "the fixture must contain both applied resources before deletion")
		host := &bmo.BareMetalHost{}
		Expect(c.Get(ctx, client.ObjectKey{Name: "host", Namespace: cluster.Namespace}, host)).To(Succeed(),
			"retrieve the rendered BMH to set a finalizer that delays deletion")
		host.Finalizers = []string{"test.example.com/bmh-cleanup"}
		Expect(c.Update(ctx, host)).To(Succeed(), "persist the BMH finalizer to simulate pending host cleanup")
		attachment := &bmo.HostNetworkAttachment{}
		Expect(c.Get(ctx, client.ObjectKey{Name: "storage-net", Namespace: cluster.Namespace}, attachment)).To(Succeed(),
			"retrieve the managed attachment before testing cleanup ordering")
		Expect(attachment.Finalizers).To(BeEmpty(), "HNA deletion must not be blocked by an HNA finalizer or webhook")
		Expect(c.Delete(ctx, cluster)).To(Succeed(), "request ClusterInstance deletion to start finalization")
		Expect(c.Get(ctx, client.ObjectKeyFromObject(cluster), cluster)).To(Succeed(),
			"reload the terminating ClusterInstance for finalization")
		operations = nil
		for range 2 {
			result, err := r.handleFinalizer(ctx, log, cluster)
			Expect(err).NotTo(HaveOccurred(), "finalization must tolerate a BMH with pending cleanup")
			Expect(result).To(Equal(requeueForDeletion()),
				"finalization must retry while the BMH still has a finalizer")
			Expect(c.Get(ctx, client.ObjectKeyFromObject(attachment), attachment)).To(Succeed(),
				"the managed attachment must remain present while the BMH exists")
			Expect(attachment.DeletionTimestamp.IsZero()).To(BeTrue(),
				"deletion of the managed attachment must wait for BMH cleanup")
		}
		Expect(operations).To(Equal([]string{"delete:host"}),
			"only the BMH deletion request is allowed while its finalizer remains")
		Expect(c.Get(ctx, client.ObjectKeyFromObject(host), host)).To(Succeed(),
			"the BMH must still exist while its finalizer is present")
		Expect(host.DeletionTimestamp.IsZero()).To(BeFalse(),
			"the BMH must have received a deletion request before the attachment")
		host.Finalizers = nil
		Expect(c.Update(ctx, host)).To(Succeed(), "persist finalizer removal to simulate completed BMH cleanup")
		result, err := r.handleFinalizer(ctx, log, cluster)
		Expect(err).NotTo(HaveOccurred(), "finalization must proceed once BMH cleanup completes")
		Expect(result).To(Equal(requeueForDeletion()),
			"the controller must requeue to confirm deletion of the managed HNA")
		Expect(operations).To(Equal([]string{"delete:host", "delete:storage-net"}),
			"the managed attachment must be deleted after the BMH")
		Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(attachment), attachment))).To(BeTrue(),
			"the managed attachment must be gone after BMH cleanup")
		_, err = r.handleFinalizer(ctx, log, cluster)
		Expect(err).NotTo(HaveOccurred(), "finalization must complete after both managed resources are removed")
		Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(cluster), cluster))).To(BeTrue(),
			"ClusterInstance deletion must finish after managed resource cleanup")
	}, Entry("AI", aiTemplates.BareMetalHost), Entry("HCP", hcpTemplates.BareMetalHost))

	It("does not apply, label, or delete externally referenced HNAs", func() {
		cluster.Spec.HostNetworkAttachmentTemplates = nil
		cluster.Spec.Nodes[0].NetworkInterfaces[0].HostNetworkAttachment = bmo.HostNetworkAttachmentRef{Name: "external-net", Namespace: "shared"}
		Expect(c.Update(ctx, cluster)).To(Succeed(),
			"persist the external reference without a managed attachment template")
		external := &bmo.HostNetworkAttachment{ObjectMeta: metav1.ObjectMeta{Name: "external-net", Namespace: "shared",
			Labels: map[string]string{"owner": "external"}}, Spec: bmo.HostNetworkAttachmentSpec{Mode: bmo.SwitchportModeAccess, NativeVLAN: 200}}
		Expect(c.Create(ctx, external)).To(Succeed(), "create the pre-existing external HNA before reconciliation")
		original := external.DeepCopy()
		ok, err := r.handleRenderTemplates(ctx, log, cluster)
		Expect(err).NotTo(HaveOccurred(), "reconciliation must succeed with an external attachment reference")
		Expect(ok).To(BeTrue(), "the BMH may be applied with a reference to an existing external attachment")
		Expect(operations).To(Equal([]string{"validate:BareMetalHost/host", "apply:BareMetalHost/host"}),
			"only the BMH should be validated and applied because the HNA is external")
		Expect(cluster.Status.ManifestsRendered).To(HaveLen(1),
			"the inventory must track only resources managed by SiteConfig")
		Expect(cluster.Status.ManifestsRendered[0].Kind).To(Equal("BareMetalHost"),
			"the BMH must be the only resource tracked for cleanup")
		Expect(c.Delete(ctx, cluster)).To(Succeed(),
			"request deletion to exercise cleanup with an external HNA reference")
		Expect(c.Get(ctx, client.ObjectKeyFromObject(cluster), cluster)).To(Succeed(),
			"reload the terminating ClusterInstance before finalizing")
		operations = nil
		for range 2 {
			_, err = r.handleFinalizer(ctx, log, cluster)
			Expect(err).NotTo(HaveOccurred(), "cleanup must succeed without managing the external HNA")
		}
		Expect(operations).To(Equal([]string{"delete:host"}), "only the managed BMH may receive a deletion request")
		Expect(c.Get(ctx, client.ObjectKeyFromObject(external), external)).To(Succeed(),
			"the external attachment must still exist after the ClusterInstance is deleted")
		Expect(external).To(Equal(original), "external HNA metadata and spec must remain unchanged")
	})

})
