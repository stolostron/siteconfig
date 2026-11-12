//go:build envtest

/*
Copyright 2026.

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
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
	ai_templates "github.com/stolostron/siteconfig/internal/templates/assisted-installer"
	ibi_templates "github.com/stolostron/siteconfig/internal/templates/image-based-installer"
)

// A real API server is required: the fake client does not implement SSA field
// ownership, optimistic locking, or the consequences of omitting an owned field.
func TestBareMetalHostOnlineEnvtest(t *testing.T) {
	g := NewWithT(t)
	// The structural fixture retains the BMH fields used by these power-state
	// and template-pipeline tests, notably required boolean spec.online. These
	// tests do not simulate BMO or installer webhooks/controllers.
	crd := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "baremetalhosts.metal3.io"},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "metal3.io", Scope: apiextensionsv1.NamespaceScoped,
			Names: apiextensionsv1.CustomResourceDefinitionNames{Plural: "baremetalhosts", Singular: "baremetalhost", Kind: "BareMetalHost"},
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
				Name: "v1alpha1", Served: true, Storage: true,
				Schema: &apiextensionsv1.CustomResourceValidation{OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
					Type: "object", Properties: map[string]apiextensionsv1.JSONSchemaProps{
						"spec": {Type: "object", Required: []string{"online"}, Properties: map[string]apiextensionsv1.JSONSchemaProps{
							"online": {Type: "boolean"}, "externallyProvisioned": {Type: "boolean"}, "bootMode": {Type: "string"},
							"bootMACAddress": {Type: "string"}, "automatedCleaningMode": {Type: "string"}, "architecture": {Type: "string"},
							"bmc": {Type: "object", Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"address": {Type: "string"}, "credentialsName": {Type: "string"}, "disableCertificateVerification": {Type: "boolean"},
							}},
						}},
					},
				}},
			}},
		},
	}
	// Template rendering resolves the release image through a ClusterImageSet.
	imageSetCRD := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "clusterimagesets.hive.openshift.io"},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "hive.openshift.io", Scope: apiextensionsv1.ClusterScoped,
			Names: apiextensionsv1.CustomResourceDefinitionNames{Plural: "clusterimagesets", Singular: "clusterimageset", Kind: "ClusterImageSet"},
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
				Name: "v1", Served: true, Storage: true,
				Schema: &apiextensionsv1.CustomResourceValidation{OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
					Type: "object", Properties: map[string]apiextensionsv1.JSONSchemaProps{
						"spec": {Type: "object", Properties: map[string]apiextensionsv1.JSONSchemaProps{
							"releaseImage": {Type: "string"},
						}},
					},
				}},
			}},
		},
	}
	server := &envtest.Environment{
		BinaryAssetsDirectory: os.Getenv("KUBEBUILDER_ASSETS"),
		CRDs:                  []*apiextensionsv1.CustomResourceDefinition{crd, imageSetCRD},
		// Exercise status updates against the actual ClusterInstance CRD.
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := server.Start()
	g.Expect(err).ToNot(HaveOccurred())
	t.Cleanup(func() { g.Expect(server.Stop()).To(Succeed()) })
	scheme := runtime.NewScheme()
	g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
	g.Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	g.Expect(hivev1.AddToScheme(scheme)).To(Succeed())
	c, err := client.NewWithWatch(cfg, client.Options{Scheme: scheme})
	g.Expect(err).ToNot(HaveOccurred())
	ctx := context.Background()
	g.Expect(c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "online-tests"}})).To(Succeed())
	cluster := &v1alpha1.ClusterInstance{ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "online-tests", UID: types.UID("cluster-uid")}}
	owner := ci.GenerateOwnedByLabelValue(cluster.Namespace, cluster.Name)
	r := &ClusterInstanceReconciler{Client: c, APIReader: c}
	log := zap.NewNop()
	counter := 0
	host := func(online bool) *unstructured.Unstructured {
		counter++
		return &unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "metal3.io/v1alpha1", "kind": "BareMetalHost",
			"metadata": map[string]interface{}{
				"name": fmt.Sprintf("host-%d", counter), "namespace": cluster.Namespace,
				"labels":      map[string]interface{}{ci.OwnedByLabel: owner, "example.com/rack": "a"},
				"annotations": map[string]interface{}{"example.com/note": "a"},
			},
			"spec": map[string]interface{}{"online": online, "externallyProvisioned": true, "bootMode": "UEFI"},
		}}
	}
	read := func(t *testing.T, desired *unstructured.Unstructured) *unstructured.Unstructured {
		t.Helper()
		actual := &unstructured.Unstructured{}
		actual.SetGroupVersionKind(desired.GroupVersionKind())
		NewWithT(t).Expect(c.Get(ctx, client.ObjectKeyFromObject(desired), actual)).To(Succeed())
		return actual
	}
	assertOnline := func(t *testing.T, desired *unstructured.Unstructured, expected bool) {
		t.Helper()
		value, found, err := unstructured.NestedBool(read(t, desired).Object, "spec", "online")
		NewWithT(t).Expect(err).ToNot(HaveOccurred())
		NewWithT(t).Expect(found).To(BeTrue())
		NewWithT(t).Expect(value).To(Equal(expected))
	}
	power := func(t *testing.T, desired *unstructured.Unstructured, online bool) {
		t.Helper()
		actual := read(t, desired)
		patch := client.MergeFromWithOptions(actual.DeepCopy(), client.MergeFromWithOptimisticLock{})
		NewWithT(t).Expect(unstructured.SetNestedField(actual.Object, online, "spec", "online")).To(Succeed())
		NewWithT(t).Expect(c.Patch(ctx, actual, patch, client.FieldOwner("power-controller"))).To(Succeed())
	}
	apply := func(desired *unstructured.Unstructured, dryRun bool) error {
		var writer client.Client = c
		if dryRun {
			writer = client.NewDryRunClient(c)
		}
		_, err := r.applyRenderedObject(ctx, writer, log, cluster, *desired, dryRun)
		return err
	}
	for _, installer := range []struct {
		name     string
		template string
		online   bool
	}{
		{name: "ibi", template: ibi_templates.BareMetalHost, online: false},
		{name: "assisted", template: ai_templates.BareMetalHost, online: true},
	} {
		t.Run("rendered-template-pipeline-"+installer.name, func(t *testing.T) {
			g := NewWithT(t)
			name := "pipeline-" + installer.name
			g.Expect(c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}})).To(Succeed())
			g.Expect(c.Create(ctx, &hivev1.ClusterImageSet{
				ObjectMeta: metav1.ObjectMeta{Name: name},
				Spec:       hivev1.ClusterImageSetSpec{ReleaseImage: "quay.io/example/release:4.22.0"},
			})).To(Succeed())
			g.Expect(c.Create(ctx, &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster-templates", Namespace: name},
			})).To(Succeed())
			g.Expect(c.Create(ctx, &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "node-templates", Namespace: name},
				// Keep the production BMH template unchanged, isolating this path
				// from the other installer resources/controllers.
				Data: map[string]string{"BareMetalHost": installer.template},
			})).To(Succeed())
			instance := &v1alpha1.ClusterInstance{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: name},
				Spec: v1alpha1.ClusterInstanceSpec{
					ClusterName: name, BaseDomain: "example.com", ClusterType: v1alpha1.ClusterTypeSNO,
					ClusterImageSetNameRef: name, PullSecretRef: corev1.LocalObjectReference{Name: "pull-secret"},
					TemplateRefs: []v1alpha1.TemplateRef{{Name: "cluster-templates", Namespace: name}},
					Nodes: []v1alpha1.NodeSpec{{
						HostName: "node-0", Role: "master", BmcAddress: "redfish+https://192.0.2.1/redfish/v1/Systems/1",
						BmcCredentialsName: v1alpha1.BmcCredentialsName{Name: "bmc-credentials"},
						BootMACAddress:     "02:00:00:00:00:01", BootMode: "UEFI", AutomatedCleaningMode: "disabled",
						TemplateRefs: []v1alpha1.TemplateRef{{Name: "node-templates", Namespace: name}},
					}},
				},
			}
			g.Expect(c.Create(ctx, instance)).To(Succeed())
			pipeline := &ClusterInstanceReconciler{Client: c, APIReader: c, TmplEngine: ci.NewTemplateEngine()}
			var desired *unstructured.Unstructured
			runPipeline := func() {
				t.Helper()
				rendered, err := pipeline.TmplEngine.ProcessTemplates(ctx, c, log, *instance)
				g.Expect(err).ToNot(HaveOccurred())
				objects := rendered.GetRenderObjects()
				g.Expect(objects).To(HaveLen(1))
				object := objects[0].GetObject()
				desired = &object
				g.Expect(desired.GetKind()).To(Equal("BareMetalHost"))
				value, found, err := unstructured.NestedBool(desired.Object, "spec", "online")
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(found).To(BeTrue())
				g.Expect(value).To(Equal(installer.online), "metadata updates must not alter the source template default")

				before := &unstructured.Unstructured{}
				before.SetGroupVersionKind(desired.GroupVersionKind())
				readErr := c.Get(ctx, client.ObjectKeyFromObject(desired), before)
				g.Expect(readErr == nil || apierrors.IsNotFound(readErr)).To(BeTrue(), "%v", readErr)
				validated, err := pipeline.validateRenderedManifests(ctx, log, instance, objects)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(validated).To(BeTrue())
				if apierrors.IsNotFound(readErr) {
					g.Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(desired), before))).To(BeTrue())
				} else {
					g.Expect(read(t, desired).GetResourceVersion()).To(Equal(before.GetResourceVersion()), "validation must not write the BMH")
				}
				applied, err := pipeline.applyRenderedManifests(ctx, log, instance, objects)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(applied).To(BeTrue())
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed())
				g.Expect(meta.IsStatusConditionTrue(instance.Status.Conditions, string(v1alpha1.RenderedTemplatesValidated))).To(BeTrue())
				g.Expect(meta.IsStatusConditionTrue(instance.Status.Conditions, string(v1alpha1.RenderedTemplatesApplied))).To(BeTrue())
				g.Expect(instance.Status.ManifestsRendered).To(HaveLen(1))
				g.Expect(instance.Status.ManifestsRendered[0].Status).To(Equal(v1alpha1.ManifestRenderedSuccess))
			}
			updateInstance := func(mutate func()) {
				t.Helper()
				before := instance.DeepCopy()
				mutate()
				g.Expect(c.Patch(ctx, instance, client.MergeFrom(before))).To(Succeed())
				g.Expect(instance.Generation).To(BeNumerically(">", before.Generation))
				runPipeline()
			}

			runPipeline()
			assertOnline(t, desired, installer.online)
			g.Expect(read(t, desired).GetAnnotations()).To(HaveKeyWithValue(bmhOnlineInitializedAnnotation, string(instance.UID)))
			if !installer.online {
				// IBI turns the host on; assisted installation leaves the initial
				// true unchanged, so the first update also exercises sole ownership.
				power(t, desired, true)
			}
			updateInstance(func() {
				instance.Spec.ExtraLabels = map[string]map[string]string{"BareMetalHost": {"example.com/pipeline-test": "cluster"}}
				instance.Spec.ExtraAnnotations = map[string]map[string]string{"BareMetalHost": {"example.com/pipeline-test": "cluster"}}
			})
			assertOnline(t, desired, true)
			g.Expect(read(t, desired).GetLabels()).To(HaveKeyWithValue("example.com/pipeline-test", "cluster"))
			g.Expect(read(t, desired).GetAnnotations()).To(HaveKeyWithValue("example.com/pipeline-test", "cluster"))
			owns, err := siteConfigOwnsOnline(read(t, desired))
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(owns).To(BeFalse())

			updateInstance(func() {
				instance.Spec.Nodes[0].ExtraLabels = map[string]map[string]string{"BareMetalHost": {"example.com/pipeline-test": "node"}}
				instance.Spec.Nodes[0].ExtraAnnotations = map[string]map[string]string{"BareMetalHost": {"example.com/pipeline-test": "node"}}
			})
			assertOnline(t, desired, true)
			g.Expect(read(t, desired).GetLabels()).To(HaveKeyWithValue("example.com/pipeline-test", "node"))
			g.Expect(read(t, desired).GetAnnotations()).To(HaveKeyWithValue("example.com/pipeline-test", "node"))

			updateInstance(func() {
				instance.Spec.ExtraLabels, instance.Spec.ExtraAnnotations = nil, nil
				instance.Spec.Nodes[0].ExtraLabels, instance.Spec.Nodes[0].ExtraAnnotations = nil, nil
			})
			assertOnline(t, desired, true)
			g.Expect(read(t, desired).GetLabels()).ToNot(HaveKey("example.com/pipeline-test"))
			g.Expect(read(t, desired).GetAnnotations()).ToNot(HaveKey("example.com/pipeline-test"))

			power(t, desired, false)
			updateInstance(func() {
				instance.Spec.ExtraLabels = map[string]map[string]string{"BareMetalHost": {"example.com/pipeline-test": "powered-off"}}
			})
			assertOnline(t, desired, false)
			g.Expect(read(t, desired).GetLabels()).To(HaveKeyWithValue("example.com/pipeline-test", "powered-off"))
		})
	}
	for _, failure := range []string{"initial-write-timeout", "handoff-then-metadata-failure"} {
		t.Run("restart-after-"+failure, func(t *testing.T) {
			g := NewWithT(t)
			desired := host(false)
			if failure == "handoff-then-metadata-failure" {
				g.Expect(apply(desired, false)).To(Succeed())
			}
			labels, annotations := desired.GetLabels(), desired.GetAnnotations()
			labels["example.com/rack"], annotations["example.com/note"] = "b", "b"
			desired.SetLabels(labels)
			desired.SetAnnotations(annotations)
			injected, handedOff := false, false
			failingClient := interceptor.NewClient(c, interceptor.Funcs{
				Patch: func(ctx context.Context, base client.WithWatch, object client.Object, patch client.Patch, opts ...client.PatchOption) error {
					options := &client.PatchOptions{}
					options.ApplyOptions(opts)
					if options.FieldManager == bmhInitialPowerFieldManager {
						err := base.Patch(ctx, object, patch, opts...)
						handedOff = err == nil
						return err
					}
					if !injected && options.FieldManager == ClusterInstanceFieldManager {
						injected = true
						if failure == "initial-write-timeout" {
							if err := base.Patch(ctx, object, patch, opts...); err != nil {
								return err
							}
							return apierrors.NewTimeoutError("initial write committed but response lost", 1)
						}
						return apierrors.NewInternalError(errors.New("injected metadata apply failure"))
					}
					return base.Patch(ctx, object, patch, opts...)
				},
			})
			_, err := applyBareMetalHost(ctx, failingClient, c, log, cluster, *desired, false)
			g.Expect(err).To(HaveOccurred())
			g.Expect(injected).To(BeTrue())
			g.Expect(read(t, desired).GetAnnotations()).To(HaveKeyWithValue(bmhOnlineInitializedAnnotation, string(cluster.UID)))
			assertOnline(t, desired, false)
			expected := false
			if failure == "initial-write-timeout" {
				g.Expect(apierrors.IsTimeout(err)).To(BeTrue())
				power(t, desired, true)
				expected = true
			} else {
				g.Expect(apierrors.IsInternalError(err)).To(BeTrue())
				g.Expect(handedOff).To(BeTrue())
				g.Expect(read(t, desired).GetLabels()).To(HaveKeyWithValue("example.com/rack", "a"))
				owns, err := siteConfigOwnsOnline(read(t, desired))
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(owns).To(BeTrue(), "the failed omission must leave the original ownership intact")
				// Keep power unchanged across the failure, but change the template
				// default to expose an erroneous replay of initialization on retry.
				g.Expect(unstructured.SetNestedField(desired.Object, true, "spec", "online")).To(Succeed())
			}
			restarted := &ClusterInstanceReconciler{Client: c, APIReader: c}
			_, err = restarted.applyRenderedObject(ctx, c, log, cluster, *desired, false)
			g.Expect(err).ToNot(HaveOccurred())
			assertOnline(t, desired, expected)
			g.Expect(read(t, desired).GetLabels()).To(HaveKeyWithValue("example.com/rack", "b"))
			g.Expect(read(t, desired).GetAnnotations()).To(HaveKeyWithValue("example.com/note", "b"))
			owns, err := siteConfigOwnsOnline(read(t, desired))
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(owns).To(BeFalse())
		})
	}
	for _, initial := range []bool{false, true} {
		t.Run(fmt.Sprintf("multiple-hosts-retry-partial-initialization-%t", initial), func(t *testing.T) {
			g := NewWithT(t)
			first, second := host(initial), host(initial)
			var objects []ci.RenderedObject
			for _, desired := range []*unstructured.Unstructured{first, second} {
				object, err := ci.NewRenderedObject(desired.Object)
				g.Expect(err).ToNot(HaveOccurred())
				objects = append(objects, *object)
			}
			injected := false
			failingClient := interceptor.NewClient(c, interceptor.Funcs{
				Patch: func(ctx context.Context, base client.WithWatch, object client.Object, patch client.Patch, opts ...client.PatchOption) error {
					if !injected && client.ObjectKeyFromObject(object) == client.ObjectKeyFromObject(second) {
						injected = true
						return apierrors.NewServiceUnavailable("second host temporarily unavailable")
					}
					return base.Patch(ctx, object, patch, opts...)
				},
			})
			manifests, applied, err := r.executeRenderedManifests(ctx, failingClient, log, cluster, objects, v1alpha1.ManifestRenderedSuccess, false)
			g.Expect(err).To(HaveOccurred())
			g.Expect(injected).To(BeTrue())
			g.Expect(applied).To(BeFalse())
			g.Expect(manifests).To(HaveLen(2))
			g.Expect(manifests[0].Status).To(Equal(v1alpha1.ManifestRenderedSuccess))
			g.Expect(manifests[1].Status).To(Equal(v1alpha1.ManifestRenderedFailure))
			g.Expect(read(t, first).GetAnnotations()).To(HaveKeyWithValue(bmhOnlineInitializedAnnotation, string(cluster.UID)))
			absent := &unstructured.Unstructured{}
			absent.SetGroupVersionKind(second.GroupVersionKind())
			g.Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(second), absent))).To(BeTrue())
			power(t, first, !initial)

			restarted := &ClusterInstanceReconciler{Client: c, APIReader: c}
			// Carry the partial manifest status forward, as the controller does,
			// while initialization itself must be determined separately per BMH.
			instance := cluster.DeepCopy()
			instance.Status.ManifestsRendered = manifests
			manifests, applied, err = restarted.executeRenderedManifests(ctx, c, log, instance, objects, v1alpha1.ManifestRenderedSuccess, false)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(applied).To(BeTrue())
			g.Expect(manifests).To(HaveLen(2))
			for _, manifest := range manifests {
				g.Expect(manifest.Status).To(Equal(v1alpha1.ManifestRenderedSuccess))
				g.Expect(manifest.Message).To(BeEmpty())
			}
			assertOnline(t, first, !initial)
			assertOnline(t, second, initial)
			g.Expect(read(t, second).GetAnnotations()).To(HaveKeyWithValue(bmhOnlineInitializedAnnotation, string(cluster.UID)))
			power(t, second, !initial)
			_, applied, err = restarted.executeRenderedManifests(ctx, c, log, instance, objects, v1alpha1.ManifestRenderedSuccess, false)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(applied).To(BeTrue())
			assertOnline(t, first, !initial)
			assertOnline(t, second, !initial)
		})
	}
	for _, initial := range []bool{false, true} {
		t.Run(fmt.Sprintf("initialize-and-release-%t", initial), func(t *testing.T) {
			g := NewWithT(t)
			desired := host(initial)
			original := desired.DeepCopy()
			g.Expect(apply(desired, false)).To(Succeed())
			g.Expect(desired.Object).To(Equal(original.Object))
			assertOnline(t, desired, initial)
			before := read(t, desired)
			owns, err := siteConfigOwnsOnline(before)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(owns).To(BeTrue())
			g.Expect(unstructured.SetNestedField(desired.Object, !initial, "spec", "online")).To(Succeed())
			g.Expect(apply(desired, true)).To(Succeed())
			g.Expect(read(t, desired).GetResourceVersion()).To(Equal(before.GetResourceVersion()))
			g.Expect(apply(desired, false)).To(Succeed())
			assertOnline(t, desired, initial)
			owns, err = siteConfigOwnsOnline(read(t, desired))
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(owns).To(BeFalse())

			labels := desired.GetLabels()
			labels["example.com/rack"] = "b"
			desired.SetLabels(labels)
			annotations := desired.GetAnnotations()
			annotations["example.com/note"] = "b"
			desired.SetAnnotations(annotations)
			g.Expect(apply(desired, false)).To(Succeed())
			g.Expect(read(t, desired).GetLabels()).To(HaveKeyWithValue("example.com/rack", "b"))
			g.Expect(read(t, desired).GetAnnotations()).To(HaveKeyWithValue("example.com/note", "b"))
			delete(labels, "example.com/rack")
			desired.SetLabels(labels)
			delete(annotations, "example.com/note")
			desired.SetAnnotations(annotations)
			g.Expect(apply(desired, false)).To(Succeed())
			g.Expect(read(t, desired).GetLabels()).ToNot(HaveKey("example.com/rack"))
			g.Expect(read(t, desired).GetAnnotations()).ToNot(HaveKey("example.com/note"))
			for _, next := range []bool{!initial, initial} {
				power(t, desired, next)
				g.Expect(apply(desired, false)).To(Succeed())
				assertOnline(t, desired, next)
				actual := read(t, desired)
				owns, err = siteConfigOwnsOnline(actual)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(owns).To(BeFalse())
				for _, field := range actual.GetManagedFields() {
					g.Expect(field.Manager).ToNot(Equal(bmhInitialPowerFieldManager), "must not reclaim power ownership")
				}
			}
		})
	}
	t.Run("IBI-changes-online-before-handoff", func(t *testing.T) {
		g := NewWithT(t)
		desired := host(false)
		g.Expect(apply(desired, false)).To(Succeed())
		power(t, desired, true)
		g.Expect(apply(desired, false)).To(Succeed())
		assertOnline(t, desired, true)
	})
	t.Run("inventory-host-first-setup", func(t *testing.T) {
		g := NewWithT(t)
		desired := host(true)
		inventory := desired.DeepCopy()
		inventory.SetLabels(nil)
		inventory.SetAnnotations(nil)
		g.Expect(unstructured.SetNestedField(inventory.Object, false, "spec", "online")).To(Succeed())
		g.Expect(c.Create(ctx, inventory, client.FieldOwner("inventory"))).To(Succeed())
		g.Expect(apply(desired, false)).To(Succeed())
		assertOnline(t, desired, true)
		power(t, desired, false)
		g.Expect(apply(desired, false)).To(Succeed())
		assertOnline(t, desired, false)
	})
	for _, legacyCreate := range []bool{false, true} {
		t.Run(fmt.Sprintf("adopt-existing-SiteConfig-host-create-%t", legacyCreate), func(t *testing.T) {
			g := NewWithT(t)
			desired := host(false)
			old := desired.DeepCopy()
			g.Expect(unstructured.SetNestedField(old.Object, true, "spec", "online")).To(Succeed())
			if legacyCreate {
				g.Expect(c.Create(ctx, old, client.FieldOwner(ClusterInstanceFieldManager))).To(Succeed())
			} else {
				_, err := applyObject(ctx, c, log, *old)
				g.Expect(err).ToNot(HaveOccurred())
			}
			g.Expect(apply(desired, true)).To(Succeed())
			g.Expect(read(t, desired).GetAnnotations()).ToNot(HaveKey(bmhOnlineInitializedAnnotation))
			g.Expect(apply(desired, false)).To(Succeed())
			assertOnline(t, desired, true)
			g.Expect(read(t, desired).GetAnnotations()).To(HaveKeyWithValue(bmhOnlineInitializedAnnotation, string(cluster.UID)))
			owns, err := siteConfigOwnsOnline(read(t, desired))
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(owns).To(BeFalse())
		})
	}
	t.Run("dry-run-new-host-does-not-record-initialization", func(t *testing.T) {
		g := NewWithT(t)
		desired := host(false)
		g.Expect(apply(desired, true)).To(Succeed())
		actual := &unstructured.Unstructured{}
		actual.SetGroupVersionKind(desired.GroupVersionKind())
		g.Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(desired), actual))).To(BeTrue())
		g.Expect(apply(desired, false)).To(Succeed())
		assertOnline(t, desired, false)
	})
	for _, boundary := range []string{"reinstall", "new-ci", "reallocated-inventory"} {
		t.Run(boundary, func(t *testing.T) {
			g := NewWithT(t)
			desired := host(false)
			g.Expect(apply(desired, false)).To(Succeed())
			power(t, desired, true)
			nextCluster := cluster.DeepCopy()
			switch boundary {
			case "reinstall":
				nextCluster.Spec.Reinstall = &v1alpha1.ReinstallSpec{Generation: "attempt-2"}
			case "new-ci":
				nextCluster.UID = types.UID("next-cluster-uid")
			case "reallocated-inventory":
				actual := read(t, desired)
				patch := client.MergeFrom(actual.DeepCopy())
				labels := actual.GetLabels()
				delete(labels, ci.OwnedByLabel)
				actual.SetLabels(labels)
				g.Expect(c.Patch(ctx, actual, patch)).To(Succeed())
			}
			_, err := applyBareMetalHost(ctx, c, c, log, nextCluster, *desired, false)
			g.Expect(err).ToNot(HaveOccurred())
			assertOnline(t, desired, false)
			power(t, desired, true)
			_, err = applyBareMetalHost(ctx, c, c, log, nextCluster, *desired, false)
			g.Expect(err).ToNot(HaveOccurred())
			assertOnline(t, desired, true)
		})
	}
	for _, alreadyReinstalled := range []bool{false, true} {
		t.Run(fmt.Sprintf("restore-preserves-installation-reinstalled-%t", alreadyReinstalled), func(t *testing.T) {
			g := NewWithT(t)
			desired := host(false)
			originalCluster := cluster.DeepCopy()
			if alreadyReinstalled {
				originalCluster.Spec.Reinstall = &v1alpha1.ReinstallSpec{Generation: "first-reinstall"}
			}
			_, err := applyBareMetalHost(ctx, c, c, log, originalCluster, *desired, false)
			g.Expect(err).ToNot(HaveOccurred())
			power(t, desired, true)
			restored := originalCluster.DeepCopy()
			restored.UID = types.UID("restored-uid")
			restored.Labels = map[string]string{veleroRestoreNameLabel: "restore-1"}
			restored.Annotations = map[string]string{veleroRestoreHandledAnnotation: "restore-1"}
			// Reconstruct the reconciler to verify there is no in-memory lifecycle state.
			restarted := &ClusterInstanceReconciler{Client: c, APIReader: c}
			_, err = restarted.applyRenderedObject(ctx, c, log, restored, *desired, false)
			g.Expect(err).ToNot(HaveOccurred())
			assertOnline(t, desired, true)
			g.Expect(read(t, desired).GetAnnotations()[bmhOnlineInitializedAnnotation]).To(HavePrefix("restored-uid"))
			restored.Spec.Reinstall = &v1alpha1.ReinstallSpec{Generation: "next-reinstall"}
			_, err = restarted.applyRenderedObject(ctx, c, log, restored, *desired, false)
			g.Expect(err).ToNot(HaveOccurred())
			assertOnline(t, desired, false)
		})
	}
	for _, atHandoff := range []bool{true, false} {
		t.Run(fmt.Sprintf("concurrent-power-change-at-handoff-%t", atHandoff), func(t *testing.T) {
			g := NewWithT(t)
			desired := host(false)
			g.Expect(apply(desired, false)).To(Succeed())
			raced := false
			racingClient := interceptor.NewClient(c, interceptor.Funcs{
				Patch: func(ctx context.Context, base client.WithWatch, object client.Object, patch client.Patch, options ...client.PatchOption) error {
					applied := &client.PatchOptions{}
					applied.ApplyOptions(options)
					shouldRace := applied.FieldManager == bmhInitialPowerFieldManager
					if !atHandoff {
						shouldRace = applied.FieldManager == ClusterInstanceFieldManager
					}
					if !raced && shouldRace {
						raced = true
						power(t, desired, true)
					}
					return base.Patch(ctx, object, patch, options...)
				},
			})
			_, err := applyBareMetalHost(ctx, racingClient, c, log, cluster, *desired, false)
			g.Expect(raced).To(BeTrue())
			g.Expect(apierrors.IsConflict(err)).To(BeTrue(), "%v", err)
			assertOnline(t, desired, true)
			g.Expect(apply(desired, false)).To(Succeed())
			assertOnline(t, desired, true)
		})
	}
	t.Run("host-created-after-not-found-is-not-overwritten", func(t *testing.T) {
		g := NewWithT(t)
		desired := host(false)
		readOnce := false
		racingReader := interceptor.NewClient(c, interceptor.Funcs{
			Get: func(ctx context.Context, base client.WithWatch, key client.ObjectKey, object client.Object, options ...client.GetOption) error {
				if !readOnce {
					readOnce = true
					appeared := desired.DeepCopy()
					g.Expect(unstructured.SetNestedField(appeared.Object, true, "spec", "online")).To(Succeed())
					g.Expect(c.Create(ctx, appeared, client.FieldOwner("installer"))).To(Succeed())
					return apierrors.NewNotFound(schema.GroupResource{Group: "metal3.io", Resource: "baremetalhosts"}, key.Name)
				}
				return base.Get(ctx, key, object, options...)
			},
		})
		_, err := applyBareMetalHost(ctx, c, racingReader, log, cluster, *desired, false)
		g.Expect(err).To(HaveOccurred())
		assertOnline(t, desired, true)
		g.Expect(apply(desired, false)).To(Succeed())
		assertOnline(t, desired, true)
	})
}
