/*
Copyright 2025 Red Hat, Inc.

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
	"testing"

	"github.com/onsi/gomega"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
)

// Simulate a rejecting installer admission webhook, then exercise the existing
// suppression path. See docs/shared-extra-manifests-testing.md for recorded live results.
func TestDayNInstallerSuppression(t *testing.T) {
	for _, kind := range []string{"AgentClusterInstall", "ImageClusterInstall"} {
		t.Run(kind, func(t *testing.T) {
			g := gomega.NewWithT(t)
			ctx := context.Background()
			scheme := runtime.NewScheme()
			g.Expect(corev1.AddToScheme(scheme)).To(gomega.Succeed())
			g.Expect(hivev1.AddToScheme(scheme)).To(gomega.Succeed())
			g.Expect(v1alpha1.AddToScheme(scheme)).To(gomega.Succeed())
			field := "manifestsConfigMapRefs"
			version := "v1beta1"
			if kind == "ImageClusterInstall" {
				field = "extraManifestsRefs"
				version = "v1alpha1"
			}
			instance := &v1alpha1.ClusterInstance{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "cluster", UID: "ci"},
				Spec: v1alpha1.ClusterInstanceSpec{
					ClusterName:            "cluster",
					ClusterImageSetNameRef: "image",
					TemplateRefs:           []v1alpha1.TemplateRef{{Name: "template", Namespace: "cluster"}},
					ExtraManifestsRefs:     []v1alpha1.ExtraManifestReference{{Name: "new", Namespace: v1alpha1.SharedResourcesNamespace}},
				},
				Status: v1alpha1.ClusterInstanceStatus{Conditions: []metav1.Condition{{Type: string(v1alpha1.ClusterProvisioned), Status: metav1.ConditionTrue, Reason: string(v1alpha1.Completed)}}},
			}
			template := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "template", Namespace: "cluster"}, Data: map[string]string{"installer": fmt.Sprintf(`apiVersion: extensions.hive.openshift.io/%s
kind: %s
metadata:
  name: cluster
  namespace: cluster
spec:
  %s:
{{ range .Spec.ExtraManifestsRefs }}
    - name: {{ .Name }}
{{ end }}
`, version, kind, field)}}
			source := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "new", Namespace: v1alpha1.SharedResourcesNamespace}, Data: map[string]string{"payload": "new"}}
			live := &unstructured.Unstructured{Object: map[string]interface{}{
				"apiVersion": "extensions.hive.openshift.io/" + version, "kind": kind,
				"metadata": map[string]interface{}{"name": "cluster", "namespace": "cluster"},
				"spec":     map[string]interface{}{field: []interface{}{map[string]interface{}{"name": "old"}}},
			}}
			attempts := 0
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(instance, template, source, live, &hivev1.ClusterImageSet{ObjectMeta: metav1.ObjectMeta{Name: "image"}}).WithStatusSubresource(instance).
				WithInterceptorFuncs(interceptor.Funcs{Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
					if obj.GetObjectKind().GroupVersionKind().Kind == kind {
						attempts++
						refs, _, err := unstructured.NestedSlice(obj.(*unstructured.Unstructured).Object, "spec", field)
						g.Expect(err).NotTo(gomega.HaveOccurred())
						g.Expect(refs).To(gomega.Equal([]interface{}{map[string]interface{}{"name": "new"}}))
						options := (&client.PatchOptions{}).ApplyOptions(opts)
						g.Expect(options.DryRun).To(gomega.ContainElement(metav1.DryRunAll))
						return fmt.Errorf("installer rejects reference changes")
					}
					return c.Patch(ctx, obj, patch, opts...)
				}}).Build()
			r := &ClusterInstanceReconciler{Client: c, Scheme: scheme, Log: zap.NewNop(), TmplEngine: ci.NewTemplateEngine()}
			g.Expect(r.handleCopySharedExtraManifests(ctx, r.Log, instance)).To(gomega.Succeed())
			ok, err := r.handleRenderTemplates(ctx, r.Log, instance)
			g.Expect(err).To(gomega.MatchError(gomega.ContainSubstring("installer rejects reference changes")))
			g.Expect(ok).To(gomega.BeFalse())
			g.Expect(attempts).To(gomega.Equal(1))
			g.Expect(meta.IsStatusConditionFalse(instance.Status.Conditions, string(v1alpha1.RenderedTemplatesValidated))).To(gomega.BeTrue())
			copy := &corev1.ConfigMap{}
			g.Expect(c.Get(ctx, client.ObjectKey{Namespace: "cluster", Name: "new"}, copy)).To(gomega.Succeed())
			g.Expect(copy.Data).To(gomega.Equal(source.Data))

			syncer := &SharedManifestSync{Client: c, Log: r.Log, Scheme: scheme}
			checkSync := func(payload string) {
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(source), source)).To(gomega.Succeed())
				source.Data["payload"] = payload
				g.Expect(c.Update(ctx, source)).To(gomega.Succeed())
				g.Expect(syncer.propagate(ctx, r.Log, instance, source)).To(gomega.Succeed())
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(copy), copy)).To(gomega.Succeed())
				g.Expect(copy.Data).To(gomega.Equal(source.Data))
			}
			checkSync("while-validation-fails")

			// The CI reference persists despite the failed downstream dry-run.
			g.Expect(c.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(gomega.Succeed())
			g.Expect(instance.Spec.ExtraManifestsRefs[0].Name).To(gomega.Equal("new"))
			instance.Spec.SuppressedManifests = []string{kind}
			g.Expect(c.Update(ctx, instance)).To(gomega.Succeed())
			ok, err = r.handleRenderTemplates(ctx, r.Log, instance)
			g.Expect(err).NotTo(gomega.HaveOccurred())
			g.Expect(ok).To(gomega.BeTrue())
			g.Expect(attempts).To(gomega.Equal(1), "suppressed installers must not be validated or applied")
			g.Expect(meta.IsStatusConditionTrue(instance.Status.Conditions, string(v1alpha1.RenderedTemplatesValidated))).To(gomega.BeTrue())
			g.Expect(meta.IsStatusConditionTrue(instance.Status.Conditions, string(v1alpha1.RenderedTemplatesApplied))).To(gomega.BeTrue())
			g.Expect(instance.Status.ManifestsRendered).To(gomega.HaveLen(1))
			g.Expect(instance.Status.ManifestsRendered[0].Status).To(gomega.Equal(v1alpha1.ManifestSuppressed))
			actual := live.DeepCopy()
			g.Expect(c.Get(ctx, client.ObjectKeyFromObject(live), actual)).To(gomega.Succeed())
			g.Expect(actual.Object["spec"]).To(gomega.Equal(live.Object["spec"]))
			checkSync("while-suppressed")

			// Removing suppression restores normal installer validation.
			instance.Spec.SuppressedManifests = nil
			g.Expect(c.Update(ctx, instance)).To(gomega.Succeed())
			ok, err = r.handleRenderTemplates(ctx, r.Log, instance)
			g.Expect(err).To(gomega.HaveOccurred())
			g.Expect(ok).To(gomega.BeFalse())
			g.Expect(attempts).To(gomega.Equal(2))
		})
	}
}
