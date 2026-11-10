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
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stolostron/siteconfig/api/v1alpha1"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
	"github.com/stolostron/siteconfig/internal/controller/deletion"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func streamConfigMapDocument(name string, wave int) string {
	return fmt.Sprintf(`apiVersion: v1
kind: ConfigMap
metadata:
  name: %q
  namespace: stream
  annotations:
    siteconfig.open-cluster-management.io/sync-wave: %q
data:
  value: original
`, name, fmt.Sprint(wave))
}

// Exercise multi-document output through reconciliation with a fake client that
// records validation, application, and deletion calls. These scenarios check that
// failures prevent unintended changes, every resource is tracked, and sync-wave
// ordering and resource retention rules hold through subsequent reconciliations
// and ClusterInstance deletion.
var _ = Describe("multi-document template reconciliation", func() {
	var (
		ctx        = context.Background()
		log        = zap.NewNop()
		c          client.Client
		r          *ClusterInstanceReconciler
		cluster    *v1alpha1.ClusterInstance
		templates  *corev1.ConfigMap
		operations []string
		rejectName string
	)

	BeforeEach(func() {
		operations = nil
		rejectName = ""
		base := createSSAMockClient()
		c = interceptor.NewClient(base.(client.WithWatch), interceptor.Funcs{
			Patch: func(ctx context.Context, c client.WithWatch, object client.Object, patch client.Patch, opts ...client.PatchOption) error {
				if patch.Type() == "application/apply-patch+yaml" {
					options := &client.PatchOptions{}
					for _, opt := range opts {
						opt.ApplyToPatch(options)
					}
					if len(options.DryRun) > 0 {
						operations = append(operations, "validate:"+object.GetName())
						if object.GetName() == rejectName {
							return fmt.Errorf("simulated API validation failure")
						}
						return nil
					}
					operations = append(operations, "apply:"+object.GetName())
				}
				return c.Patch(ctx, object, patch, opts...)
			},
			Delete: func(ctx context.Context, c client.WithWatch, object client.Object, opts ...client.DeleteOption) error {
				operations = append(operations, "delete:"+object.GetName())
				return c.Delete(ctx, object, opts...)
			},
		})
		r = &ClusterInstanceReconciler{Client: c, Log: log, TmplEngine: ci.NewTemplateEngine(),
			DeletionHandler: &deletion.DeletionHandler{Client: c, Logger: log}}
		cluster = &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{Name: "stream", Namespace: "stream"},
			Spec: v1alpha1.ClusterInstanceSpec{
				ClusterName: "stream", ClusterImageSetNameRef: "release",
				TemplateRefs: []v1alpha1.TemplateRef{{Name: "templates", Namespace: "stream"}},
			},
		}
		templates = &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "templates", Namespace: "stream"},
			Data: map[string]string{"resources.yaml": streamConfigMapDocument("high", 2) + "---\n" + streamConfigMapDocument("low", 1)}}
		Expect(c.Create(ctx, ci.GetMockClusterImageSet("release", "image"))).To(Succeed(), "create the ClusterImageSet fixture needed by rendering")
		Expect(c.Create(ctx, templates)).To(Succeed(), "create the multi-document template fixture")
		Expect(c.Create(ctx, cluster)).To(Succeed(), "create the ClusterInstance fixture")
	})

	DescribeTable("fails rendering before any pruning, dry-run, or application", func(invalid string, nodeLevel bool) {
		cluster.Spec.PruneManifests = []v1alpha1.ResourceRef{{APIVersion: "v1", Kind: "ConfigMap"}}
		Expect(c.Update(ctx, cluster)).To(Succeed(), "persist ClusterInstance inputs before reconciliation")
		pruneTarget := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "high", Namespace: "stream",
			Labels: map[string]string{ci.OwnedByLabel: "stream_stream"}}}
		Expect(c.Create(ctx, pruneTarget)).To(Succeed(), "create an existing resource to detect unintended pruning")
		cluster.Status.ManifestsRendered = []v1alpha1.ManifestReference{{APIGroup: ptr.To("v1"),
			Kind: "ConfigMap", Name: "high", Namespace: "stream", SyncWave: 2, Status: v1alpha1.ManifestRenderedSuccess}}
		Expect(c.Status().Update(ctx, cluster)).To(Succeed(), "persist the resource identity tracked before rendering fails")
		// Include both a prune candidate and an apply candidate before the bad document.
		stream := streamConfigMapDocument("high", 2) + `---
apiVersion: v1
kind: Secret
metadata: {name: low, namespace: stream}
stringData: {value: original}
---
` + invalid
		if nodeLevel {
			nodeTemplates := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "node-templates", Namespace: "stream"},
				Data: map[string]string{"resources.yaml": stream}}
			Expect(c.Create(ctx, nodeTemplates)).To(Succeed(), "create templates referenced by node streams")
			cluster.Spec.Nodes = []v1alpha1.NodeSpec{{HostName: "node", TemplateRefs: []v1alpha1.TemplateRef{{Name: "node-templates", Namespace: "stream"}}}}
			Expect(c.Update(ctx, cluster)).To(Succeed(), "persist ClusterInstance inputs before reconciliation")
		} else {
			templates.Data["resources.yaml"] = stream
			Expect(c.Update(ctx, templates)).To(Succeed(), "persist changed template output before reconciliation")
		}
		ok, err := r.handleRenderTemplates(ctx, log, cluster)
		Expect(err).To(HaveOccurred(), "an invalid stream document must fail rendering")
		Expect(ok).To(BeFalse(), "reconciliation must stop on rendering failure")
		Expect(operations).To(BeEmpty(), "this reconciliation must not prune, validate, apply, or delete resources")
		Expect(c.Get(ctx, client.ObjectKeyFromObject(pruneTarget), pruneTarget)).To(Succeed(), "rendering failure must preserve the existing prune candidate")
		Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKey{Name: "low", Namespace: "stream"}, &corev1.Secret{}))).To(BeTrue(), "rendering failure must prevent application of the valid Secret document")
		Expect(c.Get(ctx, client.ObjectKeyFromObject(cluster), cluster)).To(Succeed(), "reload the ClusterInstance to check persisted status")
		condition := meta.FindStatusCondition(cluster.Status.Conditions, string(v1alpha1.RenderedTemplates))
		Expect(condition).NotTo(BeNil(), "rendering failure should set the RenderedTemplates condition")
		Expect(condition.Status).To(Equal(metav1.ConditionFalse), "the rendering condition should report failure")
		Expect(condition.Reason).To(Equal(string(v1alpha1.Failed)), "the rendering condition should record the Failed reason")
		Expect(condition.Message).To(ContainSubstring("YAML document 3"), "status should identify the invalid document after two valid resources")
		Expect(condition.Message).To(ContainSubstring("template resources.yaml"), "status should identify the failing template key")
		Expect(cluster.Status.ManifestsRendered).To(HaveLen(1), "status should contain one entry for the tracked resource identity")
		Expect(cluster.Status.Paused).To(BeNil(), "template rendering errors must allow correction and retry without unpausing")
	},
		Entry("malformed cluster document", "apiVersion: [v1\n", false),
		Entry("malformed middle document", "apiVersion: [v1\n---\n"+streamConfigMapDocument("later", 1), false),
		Entry("invalid cluster document", "{}\n", false),
		Entry("malformed node document after a valid cluster stream", "apiVersion: [v1\n", true),
		Entry("invalid node document after a valid cluster stream", "{}\n", true),
	)

	It("validates every resource before applying in sync-wave order and tracks each identity", func() {
		ok, err := r.handleRenderTemplates(ctx, log, cluster)
		Expect(err).NotTo(HaveOccurred(), "reconciliation should succeed for valid template output")
		Expect(ok).To(BeTrue(), "reconciliation should complete for valid template output")
		Expect(operations).To(Equal([]string{"validate:low", "validate:high", "apply:low", "apply:high"}), "all dry-runs must precede application, with ascending sync waves in each phase")
		Expect(cluster.Status.ManifestsRendered).To(HaveLen(2), "status should retain both resource identities from the stream")
		for _, manifest := range cluster.Status.ManifestsRendered {
			Expect(manifest.Status).To(Equal(v1alpha1.ManifestRenderedSuccess), "each applied resource should be tracked as successfully rendered")
			Expect(manifest.Namespace).To(Equal("stream"), "tracked identities should retain the rendered namespace")
			object := &corev1.ConfigMap{}
			Expect(c.Get(ctx, client.ObjectKey{Name: manifest.Name, Namespace: manifest.Namespace}, object)).To(Succeed(), "each tracked resource should exist after application")
			Expect(object.Labels).To(HaveKeyWithValue(ci.OwnedByLabel, "stream_stream"), "each applied resource should carry ClusterInstance ownership")
		}
	})

	It("does not apply any resource when a later dry-run fails", func() {
		rejectName = "high"
		ok, err := r.handleRenderTemplates(ctx, log, cluster)
		Expect(err).To(HaveOccurred(), "the rejected high-wave dry-run must fail validation")
		Expect(ok).To(BeFalse(), "reconciliation must stop on dry-run validation failure")
		Expect(operations).To(Equal([]string{"validate:low", "validate:high"}), "a later dry-run failure must prevent every apply operation")
		Expect(meta.IsStatusConditionFalse(cluster.Status.Conditions, string(v1alpha1.RenderedTemplatesValidated))).To(BeTrue(), "status should record the dry-run validation failure")
		for _, name := range []string{"low", "high"} {
			Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKey{Name: name, Namespace: "stream"}, &corev1.ConfigMap{}))).To(BeTrue(), "ConfigMap %s must not be applied after dry-run validation fails", name)
		}
	})

	It("tracks cluster and node streams independently for every node", func() {
		nodeTemplates := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "node-templates", Namespace: "stream"},
			Data: map[string]string{"node-resources": streamConfigMapDocument("{{ .SpecialVars.CurrentNode.HostName }}-high", 2) +
				"---\n" + streamConfigMapDocument("{{ .SpecialVars.CurrentNode.HostName }}-low", 1)}}
		Expect(c.Create(ctx, nodeTemplates)).To(Succeed(), "create templates referenced by node streams")
		for _, hostname := range []string{"first", "second"} {
			cluster.Spec.Nodes = append(cluster.Spec.Nodes, v1alpha1.NodeSpec{HostName: hostname,
				TemplateRefs: []v1alpha1.TemplateRef{{Name: "node-templates", Namespace: "stream"}}})
		}
		Expect(c.Update(ctx, cluster)).To(Succeed(), "persist ClusterInstance inputs before reconciliation")
		ok, err := r.handleRenderTemplates(ctx, log, cluster)
		Expect(err).NotTo(HaveOccurred(), "reconciliation should succeed for valid template output")
		Expect(ok).To(BeTrue(), "reconciliation should complete for valid template output")
		Expect(operations).To(HaveLen(12), "two cluster resources and two resources per node should each be validated and applied")
		Expect(operations[:6]).To(ConsistOf("validate:low", "validate:high", "validate:first-low", "validate:first-high", "validate:second-low", "validate:second-high"), "every cluster and node resource should be validated before any application")
		Expect(operations[6:]).To(ConsistOf("apply:low", "apply:high", "apply:first-low", "apply:first-high", "apply:second-low", "apply:second-high"), "every cluster and node resource should be applied after validation")
		Expect(cluster.Status.ManifestsRendered).To(HaveLen(6), "status should track all cluster and node resources independently")
		for _, manifest := range cluster.Status.ManifestsRendered {
			Expect(manifest.Status).To(Equal(v1alpha1.ManifestRenderedSuccess), "each applied resource should be tracked as successfully rendered")
			Expect(c.Get(ctx, client.ObjectKey{Name: manifest.Name, Namespace: "stream"}, &corev1.ConfigMap{})).To(Succeed(), "tracked resource %s should remain present", manifest.Name)
		}
	})

	It("can retry corrected template output without unpausing", func() {
		templates.Data["resources.yaml"] += "---\n{}\n"
		Expect(c.Update(ctx, templates)).To(Succeed(), "persist changed template output before reconciliation")
		ok, err := r.handleRenderTemplates(ctx, log, cluster)
		Expect(err).To(HaveOccurred(), "the invalid trailing document must fail rendering before retry")
		Expect(ok).To(BeFalse(), "reconciliation must stop until the template is corrected")
		Expect(cluster.Status.Paused).To(BeNil(), "template rendering errors must allow correction and retry without unpausing")
		Expect(operations).To(BeEmpty(), "this reconciliation must not prune, validate, apply, or delete resources")
		templates.Data["resources.yaml"] = strings.TrimSuffix(templates.Data["resources.yaml"], "---\n{}\n")
		Expect(c.Update(ctx, templates)).To(Succeed(), "persist changed template output before reconciliation")
		ok, err = r.handleRenderTemplates(ctx, log, cluster)
		Expect(err).NotTo(HaveOccurred(), "reconciliation should succeed for valid template output")
		Expect(ok).To(BeTrue(), "reconciliation should complete for valid template output")
		Expect(meta.IsStatusConditionTrue(cluster.Status.Conditions, string(v1alpha1.RenderedTemplates))).To(BeTrue(), "a corrected template should restore the successful rendering condition")
		Expect(cluster.Status.Paused).To(BeNil(), "template rendering errors must allow correction and retry without unpausing")
	})

	It("processes duplicate identities independently while retaining one status entry", func() {
		templates.Data["resources.yaml"] = streamConfigMapDocument("duplicate", 1) + "---\n" + streamConfigMapDocument("duplicate", 1)
		Expect(c.Update(ctx, templates)).To(Succeed(), "persist changed template output before reconciliation")
		ok, err := r.handleRenderTemplates(ctx, log, cluster)
		Expect(err).NotTo(HaveOccurred(), "reconciliation should succeed for valid template output")
		Expect(ok).To(BeTrue(), "reconciliation should complete for valid template output")
		Expect(operations).To(Equal([]string{"validate:duplicate", "validate:duplicate", "apply:duplicate", "apply:duplicate"}), "duplicate documents should each be validated and applied")
		Expect(cluster.Status.ManifestsRendered).To(HaveLen(1), "status should contain one entry for the tracked resource identity")
		Expect(cluster.Status.ManifestsRendered[0].Name).To(Equal("duplicate"), "duplicate identities should share one status entry")
	})

	It("retains omitted and renamed resources without applying them again", func() {
		ok, err := r.handleRenderTemplates(ctx, log, cluster)
		Expect(err).NotTo(HaveOccurred(), "reconciliation should succeed for valid template output")
		Expect(ok).To(BeTrue(), "reconciliation should complete for valid template output")
		templates.Data["resources.yaml"] = streamConfigMapDocument("renamed", 1)
		Expect(c.Update(ctx, templates)).To(Succeed(), "persist changed template output before reconciliation")
		operations = nil
		ok, err = r.handleRenderTemplates(ctx, log, cluster)
		Expect(err).NotTo(HaveOccurred(), "reconciliation should succeed for valid template output")
		Expect(ok).To(BeTrue(), "reconciliation should complete for valid template output")
		Expect(operations).To(Equal([]string{"validate:renamed", "apply:renamed"}), "only the newly emitted identity should be validated and applied")
		Expect(cluster.Status.ManifestsRendered).To(HaveLen(3), "omitting resources from output must retain their tracked identities")
		for _, name := range []string{"low", "high", "renamed"} {
			Expect(c.Get(ctx, client.ObjectKey{Name: name, Namespace: "stream"}, &corev1.ConfigMap{})).To(Succeed(), "resource %s should remain present after a rename or omission", name)
		}
		// A false whole-template condition also retains the previously tracked resources.
		templates.Data["resources.yaml"] = "{{ if .Spec.HoldInstallation }}" + templates.Data["resources.yaml"] + "{{ end }}"
		Expect(c.Update(ctx, templates)).To(Succeed(), "persist changed template output before reconciliation")
		operations = nil
		ok, err = r.handleRenderTemplates(ctx, log, cluster)
		Expect(err).NotTo(HaveOccurred(), "reconciliation should succeed for valid template output")
		Expect(ok).To(BeTrue(), "reconciliation should complete for valid template output")
		Expect(operations).To(BeEmpty(), "this reconciliation must not prune, validate, apply, or delete resources")
		Expect(cluster.Status.ManifestsRendered).To(HaveLen(3), "omitting resources from output must retain their tracked identities")
	})

	It("suppresses all resources of the selected kind without deleting them", func() {
		ok, err := r.handleRenderTemplates(ctx, log, cluster)
		Expect(err).NotTo(HaveOccurred(), "reconciliation should succeed for valid template output")
		Expect(ok).To(BeTrue(), "reconciliation should complete for valid template output")
		cluster.Spec.SuppressedManifests = []string{"ConfigMap"}
		Expect(c.Update(ctx, cluster)).To(Succeed(), "persist ClusterInstance inputs before reconciliation")
		operations = nil
		ok, err = r.handleRenderTemplates(ctx, log, cluster)
		Expect(err).NotTo(HaveOccurred(), "reconciliation should succeed for valid template output")
		Expect(ok).To(BeTrue(), "reconciliation should complete for valid template output")
		Expect(operations).To(BeEmpty(), "this reconciliation must not prune, validate, apply, or delete resources")
		Expect(cluster.Status.ManifestsRendered).To(HaveLen(2), "status should retain both resource identities from the stream")
		for _, manifest := range cluster.Status.ManifestsRendered {
			Expect(manifest.Status).To(Equal(v1alpha1.ManifestSuppressed), "each selected resource should be marked suppressed")
			Expect(c.Get(ctx, client.ObjectKey{Name: manifest.Name, Namespace: "stream"}, &corev1.ConfigMap{})).To(Succeed(), "tracked resource %s should remain present", manifest.Name)
		}
	})

	It("prunes all resources of the selected API version and kind in descending waves", func() {
		ok, err := r.handleRenderTemplates(ctx, log, cluster)
		Expect(err).NotTo(HaveOccurred(), "reconciliation should succeed for valid template output")
		Expect(ok).To(BeTrue(), "reconciliation should complete for valid template output")
		cluster.Spec.PruneManifests = []v1alpha1.ResourceRef{{APIVersion: "v1", Kind: "ConfigMap"}}
		Expect(c.Update(ctx, cluster)).To(Succeed(), "persist ClusterInstance inputs before reconciliation")
		operations = nil
		for pass := range 3 {
			ok, err = r.handleRenderTemplates(ctx, log, cluster)
			Expect(err).NotTo(HaveOccurred(), "pruning pass %d should succeed", pass)
			Expect(ok).To(Equal(pass == 2), "pruning pass %d should complete only after both sync waves have been confirmed deleted", pass)
		}
		Expect(operations).To(Equal([]string{"delete:high", "delete:low"}), "deletion should process resources in descending sync-wave order")
		Expect(cluster.Status.ManifestsRendered).To(BeEmpty(), "completed pruning should remove deleted resource identities from status")
		for _, name := range []string{"low", "high"} {
			Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKey{Name: name, Namespace: "stream"}, &corev1.ConfigMap{}))).To(BeTrue(), "ConfigMap %s should be absent after pruning completes", name)
		}
	})

	It("deletes every tracked stream resource in descending waves during CI deletion", func() {
		ok, err := r.handleRenderTemplates(ctx, log, cluster)
		Expect(err).NotTo(HaveOccurred(), "reconciliation should succeed for valid template output")
		Expect(ok).To(BeTrue(), "reconciliation should complete for valid template output")
		// Cleanup uses tracked identities even when the template no longer emits them.
		templates.Data["resources.yaml"] = ""
		Expect(c.Update(ctx, templates)).To(Succeed(), "persist changed template output before reconciliation")
		ok, err = r.handleRenderTemplates(ctx, log, cluster)
		Expect(err).NotTo(HaveOccurred(), "reconciliation should succeed for valid template output")
		Expect(ok).To(BeTrue(), "reconciliation should complete for valid template output")
		Expect(cluster.Status.ManifestsRendered).To(HaveLen(2), "status should retain both resource identities from the stream")
		cluster.Finalizers = []string{clusterInstanceFinalizer}
		Expect(c.Update(ctx, cluster)).To(Succeed(), "persist ClusterInstance inputs before reconciliation")
		Expect(c.Delete(ctx, cluster)).To(Succeed(), "request ClusterInstance deletion while its finalizer is set")
		operations = nil
		// Each wave takes a deletion pass and a confirmation pass; the final pass removes the CI finalizer.
		for range 3 {
			Expect(c.Get(ctx, client.ObjectKeyFromObject(cluster), cluster)).To(Succeed(), "the ClusterInstance should remain until its finalizer completes resource deletion")
			_, err := r.handleFinalizer(ctx, log, cluster)
			Expect(err).NotTo(HaveOccurred(), "the finalizer should delete each wave and then remove itself")
		}
		Expect(operations).To(Equal([]string{"delete:high", "delete:low"}), "deletion should process resources in descending sync-wave order")
		Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(cluster), cluster))).To(BeTrue(), "ClusterInstance deletion should finish after all tracked resources are removed")
	})
})
