//go:build envtest

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
	"net"
	"net/url"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

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
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/stolostron/siteconfig/api/v1alpha1"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
	"github.com/stolostron/siteconfig/internal/controller/configuration"
	"github.com/stolostron/siteconfig/internal/controller/reinstall"
	ai_templates "github.com/stolostron/siteconfig/internal/templates/assisted-installer"
	ibi_templates "github.com/stolostron/siteconfig/internal/templates/image-based-installer"
)

type bareMetalHostOnlineEnvtestFixture struct {
	ctx     context.Context
	c       client.WithWatch
	cluster *v1alpha1.ClusterInstance
	r       *ClusterInstanceReconciler
	log     *zap.Logger
	counter int
}

// A real API server is required: the fake client does not implement SSA field
// ownership, optimistic locking, or the consequences of omitting an owned field.
func TestBareMetalHostOnlineEnvtest(t *testing.T) {
	f := newBareMetalHostOnlineEnvtestFixture(t)
	f.testRenderedTemplatePipeline(t)
	f.testRestartAfterPartialWrite(t)
	f.testMultipleHosts(t)
	f.testInitializeAndRelease(t)
	f.testInstallerPowerChange(t)
	f.testInventorySetup(t)
	f.testLegacyHostAdoption(t)
	f.testDryRunCreation(t)
	f.testUnsuccessfulReinstall(t)
	f.testLifecycleBoundaries(t)
	f.testRestoration(t)
	f.testOlderRestoration(t)
	f.testConcurrentPowerChanges(t)
	f.testConcurrentCreation(t)
	testBareMetalHostOnlineEnvtestCleanup(t)
}

func newBareMetalHostOnlineEnvtestFixture(t *testing.T) *bareMetalHostOnlineEnvtestFixture {
	t.Helper()
	g := NewWithT(t)
	// Exercise the local-only guard on every run. If it is removed, the invalid
	// kubeconfig makes the test fail before it can access any existing cluster.
	kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
	g.Expect(os.WriteFile(kubeconfig, []byte("invalid: [kubeconfig"), 0o600)).To(Succeed(),
		"the regression must have an invalid kubeconfig so an accidental existing-cluster attempt fails safely")
	t.Setenv("KUBECONFIG", kubeconfig)
	t.Setenv("USE_EXISTING_CLUSTER", "true")

	// The structural fixture retains the BMH fields used by these power-state
	// and template-pipeline tests, notably required boolean spec.online. These
	// tests do not simulate BMO or installer webhooks/controllers.
	crd := &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "baremetalhosts.metal3.io"},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "metal3.io", Scope: apiextensionsv1.NamespaceScoped,
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Plural: "baremetalhosts", Singular: "baremetalhost", Kind: "BareMetalHost",
			},
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
				Name: "v1alpha1", Served: true, Storage: true,
				Schema: &apiextensionsv1.CustomResourceValidation{OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
					Type: "object", Properties: map[string]apiextensionsv1.JSONSchemaProps{
						"spec": {Type: "object", Required: []string{"online"}, Properties: map[string]apiextensionsv1.JSONSchemaProps{
							"online": {Type: "boolean"}, "externallyProvisioned": {Type: "boolean"}, "bootMode": {Type: "string"},
							"bootMACAddress": {Type: "string"}, "automatedCleaningMode": {Type: "string"}, "architecture": {Type: "string"},
							"bmc": {Type: "object", Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"address": {Type: "string"}, "credentialsName": {Type: "string"},
								"disableCertificateVerification": {Type: "boolean"},
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
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Plural: "clusterimagesets", Singular: "clusterimageset", Kind: "ClusterImageSet",
			},
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
		// Fixture CRDs must never be installed into a developer's cluster.
		UseExistingCluster:    ptr.To(false),
		BinaryAssetsDirectory: os.Getenv("KUBEBUILDER_ASSETS"),
		CRDs:                  []*apiextensionsv1.CustomResourceDefinition{crd, imageSetCRD},
		// Exercise status updates against the actual ClusterInstance CRD.
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := startBareMetalHostOnlineEnvtest(t, server)
	g.Expect(err).ToNot(HaveOccurred(), "start the local API server and install the fixture CRDs")
	scheme := runtime.NewScheme()
	g.Expect(corev1.AddToScheme(scheme)).To(Succeed(),
		"register core resources so fixture namespaces and ConfigMaps can be created")
	g.Expect(v1alpha1.AddToScheme(scheme)).To(Succeed(),
		"register ClusterInstance resources so the real status-update path can be exercised")
	g.Expect(hivev1.AddToScheme(scheme)).To(Succeed(),
		"register ClusterImageSets so production templates can resolve the release image")
	c, err := client.NewWithWatch(cfg, client.Options{Scheme: scheme})
	g.Expect(err).ToNot(HaveOccurred(), "create an uncached client for API-server field-ownership tests")
	ctx := context.Background()
	g.Expect(c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "online-tests"}})).To(Succeed(),
		"create the namespace shared by independent host scenarios")
	cluster := &v1alpha1.ClusterInstance{ObjectMeta: metav1.ObjectMeta{
		Name: "cluster", Namespace: "online-tests", UID: types.UID("cluster-uid"),
	}}
	r := &ClusterInstanceReconciler{Client: c, APIReader: c}
	log := zap.NewNop()
	return &bareMetalHostOnlineEnvtestFixture{ctx: ctx, c: c, cluster: cluster, r: r, log: log}
}

// Register cleanup before Start: CRD installation can fail after etcd and the
// API server are already running. Report shutdown separately from setup failure.
func startBareMetalHostOnlineEnvtest(t *testing.T, server *envtest.Environment) (*rest.Config, error) {
	t.Helper()
	t.Cleanup(func() {
		if err := server.Stop(); err != nil {
			t.Errorf("stop the local envtest control plane: %v", err)
		}
	})
	cfg, err := server.Start()
	if cfg != nil {
		// Keep fixture and failed-startup probe requests bounded without changing
		// the control plane's own configuration or its cleanup behavior.
		cfg = rest.CopyConfig(cfg)
		cfg.Timeout = 10 * time.Second
	}
	return cfg, err
}

func testBareMetalHostOnlineEnvtestCleanup(t *testing.T) {
	t.Run("cleanup-after-CRD-install-failure", func(t *testing.T) {
		g := NewWithT(t)
		var endpoints [2]string
		server := &envtest.Environment{
			UseExistingCluster:    ptr.To(false),
			BinaryAssetsDirectory: os.Getenv("KUBEBUILDER_ASSETS"),
			CRDDirectoryPaths:     []string{filepath.Join(t.TempDir(), "missing-crds")},
			ErrorIfCRDPathMissing: true,
		}
		// A fallback prevents this regression test itself from leaking processes
		// if the cleanup behavior under test is broken.
		t.Cleanup(func() {
			if err := server.Stop(); err != nil {
				t.Errorf("stop the failed-startup regression control plane: %v", err)
			}
		})
		// Use a nested test so its registered cleanup runs before checking that
		// the API server is gone. A missing CRD directory fails after startup.
		if !t.Run("partial-startup", func(t *testing.T) {
			g := NewWithT(t)
			cfg, err := startBareMetalHostOnlineEnvtest(t, server)
			g.Expect(err).To(MatchError(ContainSubstring("unable to install CRDs")),
				"the regression must fail during CRD installation after starting the control plane")
			g.Expect(cfg).ToNot(BeNil(), "a failed CRD installation must still have started a local API server")
			c, err := client.New(cfg, client.Options{})
			g.Expect(err).ToNot(HaveOccurred(), "create a direct client to check the partially started API server")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			g.Expect(c.Get(ctx, client.ObjectKey{Name: "default"}, &corev1.Namespace{})).To(Succeed(),
				"the API server must be running before cleanup so this test detects a leaked control plane")
			apiURL, err := url.Parse(cfg.Host)
			g.Expect(err).ToNot(HaveOccurred(), "locate the actual local API listener started by envtest")
			g.Expect(server.ControlPlane.Etcd.URL).ToNot(BeNil(), "locate the local etcd client listener")
			endpoints = [2]string{apiURL.Host, server.ControlPlane.Etcd.URL.Host}
			for _, address := range endpoints {
				connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
				g.Expect(err).ToNot(HaveOccurred(), "both control-plane listeners must accept connections before cleanup")
				g.Expect(connection.Close()).To(Succeed(), "release the pre-cleanup listener probe connection")
			}
		}) {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for i, address := range endpoints {
			connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
			if connection != nil {
				g.Expect(connection.Close()).To(Succeed(), "release a connection to an unexpectedly surviving listener")
			}
			g.Expect(errors.Is(err, syscall.ECONNREFUSED)).To(BeTrue(),
				"listener %d (%s) must refuse connections before fallback cleanup; a timeout is not proof of exit: %v",
				i, address, err)
		}
	})
}

func (f *bareMetalHostOnlineEnvtestFixture) host(online bool) *unstructured.Unstructured {
	f.counter++
	owner := ci.GenerateOwnedByLabelValue(f.cluster.Namespace, f.cluster.Name)
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "metal3.io/v1alpha1", "kind": "BareMetalHost",
		"metadata": map[string]interface{}{
			"name": fmt.Sprintf("host-%d", f.counter), "namespace": f.cluster.Namespace,
			"labels":      map[string]interface{}{ci.OwnedByLabel: owner, "example.com/rack": "a"},
			"annotations": map[string]interface{}{"example.com/note": "a"},
		},
		"spec": map[string]interface{}{"online": online, "externallyProvisioned": true, "bootMode": "UEFI"},
	}}
}

func (f *bareMetalHostOnlineEnvtestFixture) read(
	t *testing.T, desired *unstructured.Unstructured,
) *unstructured.Unstructured {
	t.Helper()
	actual := &unstructured.Unstructured{}
	actual.SetGroupVersionKind(desired.GroupVersionKind())
	NewWithT(t).Expect(f.c.Get(f.ctx, client.ObjectKeyFromObject(desired), actual)).To(Succeed(),
		"read the persisted BareMetalHost")
	return actual
}

func (f *bareMetalHostOnlineEnvtestFixture) assertOnline(
	t *testing.T, desired *unstructured.Unstructured, expected bool,
) {
	t.Helper()
	g := NewWithT(t)
	value, found, err := unstructured.NestedBool(f.read(t, desired).Object, "spec", "online")
	g.Expect(err).ToNot(HaveOccurred(), "read the current power value")
	g.Expect(found).To(BeTrue(), "spec.online must remain present after applying metadata")
	g.Expect(value).To(Equal(expected), "the persisted power value must match this installation step")
}

func (f *bareMetalHostOnlineEnvtestFixture) power(
	t *testing.T, desired *unstructured.Unstructured, online bool,
) {
	t.Helper()
	g := NewWithT(t)
	actual := f.read(t, desired)
	patch := client.MergeFromWithOptions(actual.DeepCopy(), client.MergeFromWithOptimisticLock{})
	g.Expect(unstructured.SetNestedField(actual.Object, online, "spec", "online")).To(Succeed(),
		"prepare the simulated power-controller update")
	g.Expect(f.c.Patch(f.ctx, actual, patch, client.FieldOwner("power-controller"))).To(Succeed(),
		"persist the simulated power-controller update")
}

func (f *bareMetalHostOnlineEnvtestFixture) apply(desired *unstructured.Unstructured, dryRun bool) error {
	var writer client.Client = f.c
	if dryRun {
		writer = client.NewDryRunClient(f.c)
	}
	_, err := f.r.applyRenderedObject(f.ctx, writer, f.log, f.cluster, *desired, dryRun)
	return err
}

func (f *bareMetalHostOnlineEnvtestFixture) testRenderedTemplatePipeline(t *testing.T) {
	c := f.c
	ctx := f.ctx
	log := f.log
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
			g.Expect(c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}})).To(Succeed(),
				"create an isolated namespace for this installer template pipeline")
			g.Expect(c.Create(ctx, &hivev1.ClusterImageSet{
				ObjectMeta: metav1.ObjectMeta{Name: name},
				Spec:       hivev1.ClusterImageSetSpec{ReleaseImage: "quay.io/example/release:4.22.0"},
			})).To(Succeed(), "provide the release image required by the production node template")
			g.Expect(c.Create(ctx, &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "cluster-templates", Namespace: name},
			})).To(Succeed(), "provide cluster template references so the actual rendering pipeline can run")
			g.Expect(c.Create(ctx, &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "node-templates", Namespace: name},
				// Keep the production BMH template unchanged, isolating this path
				// from the other installer resources/controllers.
				Data: map[string]string{"BareMetalHost": installer.template},
			})).To(Succeed(), "install the unchanged production BMH template for this installer")
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
			g.Expect(c.Create(ctx, instance)).To(Succeed(),
				"persist the ClusterInstance so the pipeline can update its real status")
			pipeline := &ClusterInstanceReconciler{Client: c, APIReader: c, TmplEngine: ci.NewTemplateEngine()}
			var desired *unstructured.Unstructured
			runPipeline := func() {
				t.Helper()
				rendered, err := pipeline.TmplEngine.ProcessTemplates(ctx, c, log, *instance)
				g.Expect(err).ToNot(HaveOccurred(), "render the production node template without changing its power default")
				objects := rendered.GetRenderObjects()
				g.Expect(objects).To(HaveLen(1), "isolate the BMH path from resources managed by other installer controllers")
				object := objects[0].GetObject()
				desired = &object
				g.Expect(desired.GetKind()).To(Equal("BareMetalHost"),
					"the production node template must render a BareMetalHost for this regression")
				value, found, err := unstructured.NestedBool(desired.Object, "spec", "online")
				g.Expect(err).ToNot(HaveOccurred(), "read the power default rendered by the unchanged production template")
				g.Expect(found).To(BeTrue(), "the rendered BMH must retain its required online field")
				g.Expect(value).To(Equal(installer.online), "metadata updates must not alter the source template default")

				before := &unstructured.Unstructured{}
				before.SetGroupVersionKind(desired.GroupVersionKind())
				readErr := c.Get(ctx, client.ObjectKeyFromObject(desired), before)
				g.Expect(readErr == nil || apierrors.IsNotFound(readErr)).To(BeTrue(),
					"reading a rendered host may succeed or find no host, but must not hide an API failure: %v", readErr)
				validated, err := pipeline.validateRenderedManifests(ctx, log, instance, objects)
				g.Expect(err).ToNot(HaveOccurred(), "validate the rendered BMH without modifying persisted power or metadata")
				g.Expect(validated).To(BeTrue(), "the rendered BMH must pass dry-run validation before it can be applied")
				if apierrors.IsNotFound(readErr) {
					g.Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(desired), before))).To(BeTrue(),
						"dry-run validation must not create a missing host")
				} else {
					g.Expect(f.read(t, desired).GetResourceVersion()).To(
						Equal(before.GetResourceVersion()),
						"validation must not write the BMH",
					)
				}
				applied, err := pipeline.applyRenderedManifests(ctx, log, instance, objects)
				g.Expect(err).ToNot(HaveOccurred(), "apply the rendered host while preserving any initialized power state")
				g.Expect(applied).To(BeTrue(), "the complete BMH render must be reported as successfully applied")
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed(),
					"read the persisted pipeline conditions rather than only the local object")
				g.Expect(meta.IsStatusConditionTrue(instance.Status.Conditions, string(v1alpha1.RenderedTemplatesValidated))).To(
					BeTrue(),
					"successful validation must be visible in ClusterInstance status",
				)
				g.Expect(meta.IsStatusConditionTrue(instance.Status.Conditions, string(v1alpha1.RenderedTemplatesApplied))).To(
					BeTrue(),
					"successful apply must be visible in ClusterInstance status",
				)
				g.Expect(instance.Status.ManifestsRendered).To(HaveLen(1), "status must track the one rendered BMH independently")
				g.Expect(instance.Status.ManifestsRendered[0].Status).To(Equal(v1alpha1.ManifestRenderedSuccess),
					"status must record the BMH as successfully rendered and applied")
			}
			updateInstance := func(mutate func()) {
				t.Helper()
				before := instance.DeepCopy()
				mutate()
				g.Expect(c.Patch(ctx, instance, client.MergeFrom(before))).To(Succeed(),
					"persist each metadata change before rerunning the template pipeline")
				g.Expect(instance.Generation).To(BeNumerically(">", before.Generation),
					"the metadata patch must change the spec so the pipeline sees a new generation")
				runPipeline()
			}

			runPipeline()
			f.assertOnline(t, desired, installer.online)
			g.Expect(f.read(t, desired).GetAnnotations()).To(
				HaveKeyWithValue(bmhOnlineInitializedAnnotation, string(instance.UID)),
				"the first apply must record which installation initialized host power",
			)
			if !installer.online {
				// IBI turns the host on; assisted installation leaves the initial
				// true unchanged, so the first update also exercises sole ownership.
				f.power(t, desired, true)
			}
			updateInstance(func() {
				instance.Spec.ExtraLabels = map[string]map[string]string{"BareMetalHost": {"example.com/pipeline-test": "cluster"}}
				instance.Spec.ExtraAnnotations = map[string]map[string]string{
					"BareMetalHost": {"example.com/pipeline-test": "cluster"},
				}
			})
			f.assertOnline(t, desired, true)
			g.Expect(f.read(t, desired).GetLabels()).To(HaveKeyWithValue("example.com/pipeline-test", "cluster"),
				"cluster-level labels must continue to update after power initialization")
			g.Expect(f.read(t, desired).GetAnnotations()).To(HaveKeyWithValue("example.com/pipeline-test", "cluster"),
				"cluster-level annotations must continue to update after power initialization")
			owns, err := siteConfigOwnsOnline(f.read(t, desired))
			g.Expect(err).ToNot(HaveOccurred(), "inspect managed fields to verify the power-ownership handoff")
			g.Expect(owns).To(BeFalse(), "the normal SiteConfig manager must release online after the first metadata update")

			updateInstance(func() {
				instance.Spec.Nodes[0].ExtraLabels = map[string]map[string]string{
					"BareMetalHost": {"example.com/pipeline-test": "node"},
				}
				instance.Spec.Nodes[0].ExtraAnnotations = map[string]map[string]string{
					"BareMetalHost": {"example.com/pipeline-test": "node"},
				}
			})
			f.assertOnline(t, desired, true)
			g.Expect(f.read(t, desired).GetLabels()).To(HaveKeyWithValue("example.com/pipeline-test", "node"),
				"node-level labels must take precedence over cluster-level metadata")
			g.Expect(f.read(t, desired).GetAnnotations()).To(HaveKeyWithValue("example.com/pipeline-test", "node"),
				"node-level annotations must take precedence over cluster-level metadata")

			updateInstance(func() {
				instance.Spec.ExtraLabels, instance.Spec.ExtraAnnotations = nil, nil
				instance.Spec.Nodes[0].ExtraLabels, instance.Spec.Nodes[0].ExtraAnnotations = nil, nil
			})
			f.assertOnline(t, desired, true)
			g.Expect(f.read(t, desired).GetLabels()).ToNot(HaveKey("example.com/pipeline-test"),
				"removed template labels must be deleted without restoring the initial power value")
			g.Expect(f.read(t, desired).GetAnnotations()).ToNot(HaveKey("example.com/pipeline-test"),
				"removed template annotations must be deleted without restoring the initial power value")

			f.power(t, desired, false)
			updateInstance(func() {
				instance.Spec.ExtraLabels = map[string]map[string]string{
					"BareMetalHost": {"example.com/pipeline-test": "powered-off"},
				}
			})
			f.assertOnline(t, desired, false)
			g.Expect(f.read(t, desired).GetLabels()).To(HaveKeyWithValue("example.com/pipeline-test", "powered-off"),
				"metadata updates must remain effective when a power controller has turned the host off")

			// A rejected reinstall still resumes normal rendering. Exercise the
			// actual default-disabled handler before reapplying the production template.
			f.power(t, desired, !installer.online)
			marker := f.read(t, desired).GetAnnotations()[bmhOnlineInitializedAnnotation]
			store, err := configuration.NewConfigurationStore(configuration.NewDefaultConfiguration())
			g.Expect(err).ToNot(HaveOccurred(), "the default configuration must be available")
			g.Expect(store.GetAllowReinstalls()).To(BeFalse(), "this regression requires reinstalls to be disabled")
			handler := &reinstall.ReinstallHandler{Client: c, Logger: log, ConfigStore: store}
			before := instance.DeepCopy()
			instance.Spec.Reinstall = &v1alpha1.ReinstallSpec{
				Generation: "rejected-attempt", PreservationMode: v1alpha1.PreservationModeNone,
			}
			g.Expect(c.Patch(ctx, instance, client.MergeFrom(before))).To(Succeed(), "the reinstall request must be stored")
			for range 5 {
				_, err := handler.ProcessRequest(ctx, instance)
				if err != nil {
					g.Expect(err).To(MatchError(ContainSubstring("not configured for cluster reinstalls")),
						"only the expected disabled-reinstall failure is allowed")
				}
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(instance), instance)).To(Succeed(), "read reinstall progress")
				if !instance.Status.Reinstall.RequestEndTime.IsZero() {
					break
				}
			}
			g.Expect(instance.Status.Reinstall.RequestEndTime.IsZero()).To(BeFalse(), "the request must reach terminal failure")
			g.Expect(meta.IsStatusConditionFalse(instance.Status.Reinstall.Conditions,
				string(v1alpha1.ReinstallRequestValidated))).To(BeTrue(), "the request must be rejected by validation")
			result, err := handler.ProcessRequest(ctx, instance)
			g.Expect(err).ToNot(HaveOccurred(), "a rejected request is already considered processed")
			g.Expect(result.IsZero()).To(BeTrue(), "normal template rendering is allowed to resume")
			runPipeline()
			f.assertOnline(t, desired, !installer.online)
			g.Expect(f.read(t, desired).GetAnnotations()).To(HaveKeyWithValue(bmhOnlineInitializedAnnotation, marker),
				"a rejected request must not advance the initialization marker")
		})
	}
}

func (f *bareMetalHostOnlineEnvtestFixture) testRestartAfterPartialWrite(t *testing.T) {
	c := f.c
	ctx := f.ctx
	cluster := f.cluster
	log := f.log
	for _, failure := range []string{"initial-write-timeout", "handoff-then-metadata-failure"} {
		t.Run("restart-after-"+failure, func(t *testing.T) {
			g := NewWithT(t)
			desired := f.host(false)
			if failure == "handoff-then-metadata-failure" {
				g.Expect(f.apply(desired, false)).To(Succeed(),
					"initialize the existing host before injecting a failure after the ownership handoff")
			}
			labels, annotations := desired.GetLabels(), desired.GetAnnotations()
			labels["example.com/rack"], annotations["example.com/note"] = "b", "b"
			desired.SetLabels(labels)
			desired.SetAnnotations(annotations)
			injected, handedOff := false, false
			failingClient := interceptor.NewClient(c, interceptor.Funcs{
				Apply: func(ctx context.Context, base client.WithWatch,
					object runtime.ApplyConfiguration, opts ...client.ApplyOption,
				) error {
					err := base.Apply(ctx, object, opts...)
					handedOff = err == nil
					return err
				},
				Patch: func(ctx context.Context, base client.WithWatch,
					object client.Object, patch client.Patch, opts ...client.PatchOption,
				) error {
					options := &client.PatchOptions{}
					options.ApplyOptions(opts)
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
			g.Expect(err).To(HaveOccurred(), "the injected partial-write failure must propagate so reconciliation can retry")
			g.Expect(injected).To(BeTrue(), "the test must reach the intended write failure before exercising restart recovery")
			g.Expect(f.read(t, desired).GetAnnotations()).To(
				HaveKeyWithValue(bmhOnlineInitializedAnnotation, string(cluster.UID)),
				"the committed write must retain initialization so a retry cannot replay the power default",
			)
			f.assertOnline(t, desired, false)
			expected := false
			if failure == "initial-write-timeout" {
				g.Expect(apierrors.IsTimeout(err)).To(BeTrue(),
					"simulate a lost response after the initial apply has already committed")
				f.power(t, desired, true)
				expected = true
			} else {
				g.Expect(apierrors.IsInternalError(err)).To(BeTrue(),
					"simulate metadata apply failure after the separate ownership handoff commits")
				g.Expect(handedOff).To(BeTrue(), "ownership sharing must commit before the injected metadata failure")
				g.Expect(f.read(t, desired).GetLabels()).To(HaveKeyWithValue("example.com/rack", "a"),
					"the failed metadata apply must leave the original label unchanged")
				owns, err := siteConfigOwnsOnline(f.read(t, desired))
				g.Expect(err).ToNot(HaveOccurred(),
					"inspect field ownership left by the committed handoff and failed metadata apply")
				g.Expect(owns).To(BeTrue(), "the failed omission must leave the original ownership intact")
				// Keep power unchanged across the failure, but change the template
				// default to expose an erroneous replay of initialization on retry.
				g.Expect(unstructured.SetNestedField(desired.Object, true, "spec", "online")).To(Succeed(),
					"change the template default so a retry would expose accidental power reinitialization")
			}
			restarted := &ClusterInstanceReconciler{Client: c, APIReader: c}
			_, err = restarted.applyRenderedObject(ctx, c, log, cluster, *desired, false)
			g.Expect(err).ToNot(HaveOccurred(), "a restarted reconciler must recover using the persisted initialization record")
			f.assertOnline(t, desired, expected)
			g.Expect(f.read(t, desired).GetLabels()).To(HaveKeyWithValue("example.com/rack", "b"),
				"the retry must persist the previously failed label update")
			g.Expect(f.read(t, desired).GetAnnotations()).To(HaveKeyWithValue("example.com/note", "b"),
				"the retry must persist the previously failed annotation update")
			owns, err := siteConfigOwnsOnline(f.read(t, desired))
			g.Expect(err).ToNot(HaveOccurred(), "inspect field ownership after the restarted reconciler completes the retry")
			g.Expect(owns).To(BeFalse(), "the successful retry must release normal-manager ownership of online")
		})
	}
}

func (f *bareMetalHostOnlineEnvtestFixture) testMultipleHosts(t *testing.T) {
	c := f.c
	ctx := f.ctx
	cluster := f.cluster
	r := f.r
	log := f.log
	for _, initial := range []bool{false, true} {
		t.Run(fmt.Sprintf("multiple-hosts-retry-partial-initialization-%t", initial), func(t *testing.T) {
			g := NewWithT(t)
			first, second := f.host(initial), f.host(initial)
			var objects []ci.RenderedObject
			for _, desired := range []*unstructured.Unstructured{first, second} {
				object, err := ci.NewRenderedObject(desired.Object)
				g.Expect(err).ToNot(HaveOccurred(),
					"wrap both hosts as rendered objects so the real multi-manifest apply path is exercised")
				objects = append(objects, *object)
			}
			injected := false
			failingClient := interceptor.NewClient(c, interceptor.Funcs{
				Patch: func(ctx context.Context, base client.WithWatch,
					object client.Object, patch client.Patch, opts ...client.PatchOption,
				) error {
					if !injected && client.ObjectKeyFromObject(object) == client.ObjectKeyFromObject(second) {
						injected = true
						return apierrors.NewServiceUnavailable("second host temporarily unavailable")
					}
					return base.Patch(ctx, object, patch, opts...)
				},
			})
			manifests, applied, err := r.executeRenderedManifests(
				ctx, failingClient, log, cluster, objects, v1alpha1.ManifestRenderedSuccess, false,
			)
			g.Expect(err).To(HaveOccurred(), "the second-host failure must propagate even though the first host was initialized")
			g.Expect(injected).To(BeTrue(),
				"the second host must reach the injected failure for partial initialization to be tested")
			g.Expect(applied).To(BeFalse(), "a partial apply must not report that every host succeeded")
			g.Expect(manifests).To(HaveLen(2), "partial status must retain a result for each host")
			g.Expect(manifests[0].Status).To(Equal(v1alpha1.ManifestRenderedSuccess),
				"the first host must retain its successful initialization status")
			g.Expect(manifests[1].Status).To(Equal(v1alpha1.ManifestRenderedFailure),
				"the failed second host must have its own failure status")
			g.Expect(f.read(t, first).GetAnnotations()).To(HaveKeyWithValue(bmhOnlineInitializedAnnotation, string(cluster.UID)),
				"the first host must retain its initialization marker across another host failure")
			absent := &unstructured.Unstructured{}
			absent.SetGroupVersionKind(second.GroupVersionKind())
			g.Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(second), absent))).To(BeTrue(),
				"the failed second-host write must not create or initialize that host")
			f.power(t, first, !initial)

			restarted := &ClusterInstanceReconciler{Client: c, APIReader: c}
			// Carry the partial manifest status forward, as the controller does,
			// while initialization itself must be determined separately per BMH.
			instance := cluster.DeepCopy()
			instance.Status.ManifestsRendered = manifests
			manifests, applied, err = restarted.executeRenderedManifests(
				ctx, c, log, instance, objects, v1alpha1.ManifestRenderedSuccess, false,
			)
			g.Expect(err).ToNot(HaveOccurred(), "retry the partial apply using only persisted per-host initialization state")
			g.Expect(applied).To(BeTrue(), "both hosts must apply successfully after the transient failure clears")
			g.Expect(manifests).To(HaveLen(2), "the successful retry must still report each host independently")
			for _, manifest := range manifests {
				g.Expect(manifest.Status).To(Equal(v1alpha1.ManifestRenderedSuccess),
					"the retry must update every host result to success")
				g.Expect(manifest.Message).To(BeEmpty(), "the retry must clear stale failure details from manifest status")
			}
			f.assertOnline(t, first, !initial)
			f.assertOnline(t, second, initial)
			g.Expect(f.read(t, second).GetAnnotations()).To(
				HaveKeyWithValue(bmhOnlineInitializedAnnotation, string(cluster.UID)),
				"the second host must record initialization when its retry first succeeds",
			)
			f.power(t, second, !initial)
			_, applied, err = restarted.executeRenderedManifests(
				ctx, c, log, instance, objects, v1alpha1.ManifestRenderedSuccess, false,
			)
			g.Expect(err).ToNot(HaveOccurred(), "later metadata applies must continue to succeed for both initialized hosts")
			g.Expect(applied).To(BeTrue(), "later applies must not replay either host power default")
			f.assertOnline(t, first, !initial)
			f.assertOnline(t, second, !initial)
		})
	}
}

func (f *bareMetalHostOnlineEnvtestFixture) testInitializeAndRelease(t *testing.T) {
	for _, initial := range []bool{false, true} {
		t.Run(fmt.Sprintf("initialize-and-release-%t", initial), func(t *testing.T) {
			g := NewWithT(t)
			desired := f.host(initial)
			original := desired.DeepCopy()
			g.Expect(f.apply(desired, false)).To(Succeed(), "initialize the host from its first rendered power value")
			g.Expect(desired.Object).To(Equal(original.Object),
				"applying a BMH must not mutate the rendered template used by later reconciliations")
			f.assertOnline(t, desired, initial)
			before := f.read(t, desired)
			owns, err := siteConfigOwnsOnline(before)
			g.Expect(err).ToNot(HaveOccurred(), "inspect field ownership immediately after initialization")
			g.Expect(owns).To(BeTrue(), "the normal manager must initially own online so this test exercises a real handoff")
			g.Expect(unstructured.SetNestedField(desired.Object, !initial, "spec", "online")).To(Succeed(),
				"change the template default to expose any accidental replay during validation or updates")
			g.Expect(f.apply(desired, true)).To(Succeed(),
				"validate the changed template without modifying the initialized host")
			g.Expect(f.read(t, desired).GetResourceVersion()).To(Equal(before.GetResourceVersion()),
				"dry-run validation must not persist the handoff or alter the host resource version")
			g.Expect(f.apply(desired, false)).To(Succeed(),
				"the real update must complete the handoff without replaying the changed power default")
			f.assertOnline(t, desired, initial)
			owns, err = siteConfigOwnsOnline(f.read(t, desired))
			g.Expect(err).ToNot(HaveOccurred(), "inspect field ownership after the first real update")
			g.Expect(owns).To(BeFalse(), "the normal manager must release online before later applies omit it")

			labels := desired.GetLabels()
			labels["example.com/rack"] = "b"
			desired.SetLabels(labels)
			annotations := desired.GetAnnotations()
			annotations["example.com/note"] = "b"
			desired.SetAnnotations(annotations)
			g.Expect(f.apply(desired, false)).To(Succeed(),
				"apply label and annotation changes after the power-ownership handoff")
			g.Expect(f.read(t, desired).GetLabels()).To(HaveKeyWithValue("example.com/rack", "b"),
				"labels must remain managed after the normal manager releases online")
			g.Expect(f.read(t, desired).GetAnnotations()).To(HaveKeyWithValue("example.com/note", "b"),
				"annotations must remain managed after the normal manager releases online")
			delete(labels, "example.com/rack")
			desired.SetLabels(labels)
			delete(annotations, "example.com/note")
			desired.SetAnnotations(annotations)
			g.Expect(f.apply(desired, false)).To(Succeed(), "apply metadata removals without reclaiming power ownership")
			g.Expect(f.read(t, desired).GetLabels()).ToNot(HaveKey("example.com/rack"),
				"removing a rendered label must still remove it from the existing host")
			g.Expect(f.read(t, desired).GetAnnotations()).ToNot(HaveKey("example.com/note"),
				"removing a rendered annotation must still remove it from the existing host")
			for _, next := range []bool{!initial, initial} {
				f.power(t, desired, next)
				g.Expect(f.apply(desired, false)).To(Succeed(),
					"reconcile metadata after an independent power controller changes online")
				f.assertOnline(t, desired, next)
				actual := f.read(t, desired)
				owns, err = siteConfigOwnsOnline(actual)
				g.Expect(err).ToNot(HaveOccurred(), "inspect field ownership after the independent power update")
				g.Expect(owns).To(BeFalse(), "later reconciliations must not reclaim online from its power controller")
				for _, field := range actual.GetManagedFields() {
					g.Expect(field.Manager).ToNot(Equal(bmhInitialPowerFieldManager), "must not reclaim power ownership")
				}
			}
		})
	}
}

func (f *bareMetalHostOnlineEnvtestFixture) testInstallerPowerChange(t *testing.T) {
	t.Run("IBI-changes-online-before-handoff", func(t *testing.T) {
		g := NewWithT(t)
		desired := f.host(false)
		g.Expect(f.apply(desired, false)).To(Succeed(), "initialize the IBI host with the template power-off default")
		f.power(t, desired, true)
		g.Expect(f.apply(desired, false)).To(Succeed(),
			"handoff must preserve the power-on value written by IBI before the first metadata update")
		f.assertOnline(t, desired, true)
	})
}

func (f *bareMetalHostOnlineEnvtestFixture) testInventorySetup(t *testing.T) {
	c := f.c
	ctx := f.ctx
	t.Run("inventory-host-first-setup", func(t *testing.T) {
		g := NewWithT(t)
		desired := f.host(true)
		inventory := desired.DeepCopy()
		inventory.SetLabels(nil)
		inventory.SetAnnotations(nil)
		g.Expect(unstructured.SetNestedField(inventory.Object, false, "spec", "online")).To(Succeed(),
			"prepare an inventory host with a power value different from the template default")
		g.Expect(c.Create(ctx, inventory, client.FieldOwner("inventory"))).To(Succeed(),
			"create an unallocated inventory host without SiteConfig initialization metadata")
		g.Expect(f.apply(desired, false)).To(Succeed(), "the first allocation must apply the new installation power default")
		f.assertOnline(t, desired, true)
		f.power(t, desired, false)
		g.Expect(f.apply(desired, false)).To(Succeed(),
			"a later update must preserve power changes made after the inventory host was initialized")
		f.assertOnline(t, desired, false)
	})
}

func (f *bareMetalHostOnlineEnvtestFixture) testLegacyHostAdoption(t *testing.T) {
	c := f.c
	ctx := f.ctx
	cluster := f.cluster
	log := f.log
	for _, legacyCreate := range []bool{false, true} {
		t.Run(fmt.Sprintf("adopt-existing-SiteConfig-host-create-%t", legacyCreate), func(t *testing.T) {
			g := NewWithT(t)
			desired := f.host(false)
			old := desired.DeepCopy()
			g.Expect(unstructured.SetNestedField(old.Object, true, "spec", "online")).To(Succeed(),
				"prepare a legacy host that is running despite the current template power-off default")
			if legacyCreate {
				g.Expect(c.Create(ctx, old, client.FieldOwner(ClusterInstanceFieldManager))).To(Succeed(),
					"create legacy Update ownership so adoption covers hosts created before SSA")
			} else {
				_, err := applyObject(ctx, c, log, *old)
				g.Expect(err).ToNot(HaveOccurred(), "create legacy Apply ownership so adoption covers existing SSA-managed hosts")
			}
			g.Expect(f.apply(desired, true)).To(Succeed(),
				"validate legacy-host adoption without persisting initialization metadata")
			g.Expect(f.read(t, desired).GetAnnotations()).ToNot(HaveKey(bmhOnlineInitializedAnnotation),
				"dry-run adoption must not persist the initialization marker")
			g.Expect(f.apply(desired, false)).To(Succeed(),
				"adopt the legacy host without replaying the template power-off default")
			f.assertOnline(t, desired, true)
			g.Expect(f.read(t, desired).GetAnnotations()).To(
				HaveKeyWithValue(bmhOnlineInitializedAnnotation, string(cluster.UID)),
				"successful adoption must record the current installation identity",
			)
			owns, err := siteConfigOwnsOnline(f.read(t, desired))
			g.Expect(err).ToNot(HaveOccurred(), "inspect ownership after legacy-host adoption")
			g.Expect(owns).To(BeFalse(), "adoption must release normal-manager ownership so later updates cannot reset power")
		})
	}
}

func (f *bareMetalHostOnlineEnvtestFixture) testDryRunCreation(t *testing.T) {
	c := f.c
	ctx := f.ctx
	t.Run("dry-run-new-host-does-not-record-initialization", func(t *testing.T) {
		g := NewWithT(t)
		desired := f.host(false)
		g.Expect(f.apply(desired, true)).To(Succeed(), "validate a new host without creating or initializing it")
		actual := &unstructured.Unstructured{}
		actual.SetGroupVersionKind(desired.GroupVersionKind())
		g.Expect(apierrors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(desired), actual))).To(BeTrue(),
			"dry-run creation must leave no persisted host or initialization marker")
		g.Expect(f.apply(desired, false)).To(Succeed(),
			"a real apply must still initialize the host after a preceding dry run")
		f.assertOnline(t, desired, false)
	})
}

func (f *bareMetalHostOnlineEnvtestFixture) testUnsuccessfulReinstall(t *testing.T) {
	c := f.c
	ctx := f.ctx
	cluster := f.cluster
	log := f.log
	for _, previousReinstall := range []string{"", "first-success"} {
		for _, reason := range []v1alpha1.ClusterInstanceConditionReason{v1alpha1.Failed, v1alpha1.TimedOut} {
			t.Run(fmt.Sprintf("unsuccessful-reinstall-previous-%s-reason-%s", previousReinstall, reason), func(t *testing.T) {
				g := NewWithT(t)
				desired := f.host(false)
				original := cluster.DeepCopy()
				if previousReinstall != "" {
					original.Spec.Reinstall = &v1alpha1.ReinstallSpec{Generation: previousReinstall}
					original.Status.Reinstall = &v1alpha1.ReinstallStatus{ObservedGeneration: previousReinstall}
				}
				_, err := applyBareMetalHost(ctx, c, c, log, original, *desired, false)
				g.Expect(err).ToNot(HaveOccurred(), "initialize the existing installation")
				f.power(t, desired, true)
				marker := f.read(t, desired).GetAnnotations()[bmhOnlineInitializedAnnotation]
				next := original.DeepCopy()
				next.Spec.Reinstall = &v1alpha1.ReinstallSpec{Generation: "unsuccessful-attempt"}
				next.Status.Reinstall = &v1alpha1.ReinstallStatus{
					ObservedGeneration: previousReinstall, InProgressGeneration: "unsuccessful-attempt",
					RequestEndTime: metav1.Now(),
					Conditions: []metav1.Condition{{
						Type: string(v1alpha1.ReinstallRequestProcessed), Status: metav1.ConditionFalse, Reason: string(reason),
					}},
				}
				handler := &reinstall.ReinstallHandler{Logger: log}
				result, err := handler.ProcessRequest(ctx, next)
				g.Expect(err).ToNot(HaveOccurred(), "the unsuccessful request is already considered processed")
				g.Expect(result.IsZero()).To(BeTrue(), "normal manifest rendering can resume")
				_, err = applyBareMetalHost(ctx, c, c, log, next, *desired, false)
				g.Expect(err).ToNot(HaveOccurred(), "metadata must remain applicable after the failed request")
				f.assertOnline(t, desired, true)
				g.Expect(f.read(t, desired).GetAnnotations()).To(HaveKeyWithValue(bmhOnlineInitializedAnnotation, marker),
					"a failed request must retain the last initialized installation")
			})
		}
	}
}

func (f *bareMetalHostOnlineEnvtestFixture) testLifecycleBoundaries(t *testing.T) {
	c := f.c
	ctx := f.ctx
	cluster := f.cluster
	log := f.log
	for _, boundary := range []string{"reinstall", "new-ci", "reallocated-inventory"} {
		t.Run(boundary, func(t *testing.T) {
			g := NewWithT(t)
			desired := f.host(false)
			g.Expect(f.apply(desired, false)).To(Succeed(),
				"initialize the current installation before changing its lifecycle boundary")
			f.power(t, desired, true)
			nextCluster := cluster.DeepCopy()
			switch boundary {
			case "reinstall":
				nextCluster.Spec.Reinstall = &v1alpha1.ReinstallSpec{Generation: "attempt-2"}
				nextCluster.Status.Reinstall = &v1alpha1.ReinstallStatus{ObservedGeneration: "attempt-2"}
			case "new-ci":
				nextCluster.UID = types.UID("next-cluster-uid")
			case "reallocated-inventory":
				actual := f.read(t, desired)
				patch := client.MergeFrom(actual.DeepCopy())
				labels := actual.GetLabels()
				delete(labels, ci.OwnedByLabel)
				actual.SetLabels(labels)
				g.Expect(c.Patch(ctx, actual, patch)).To(Succeed(),
					"remove allocation ownership so reuse by the same ClusterInstance is a new association")
			}
			_, err := applyBareMetalHost(ctx, c, c, log, nextCluster, *desired, false)
			g.Expect(err).ToNot(HaveOccurred(),
				"a confirmed new installation or host association must initialize its own power default")
			f.assertOnline(t, desired, false)
			f.power(t, desired, true)
			_, err = applyBareMetalHost(ctx, c, c, log, nextCluster, *desired, false)
			g.Expect(err).ToNot(HaveOccurred(), "later updates in the new lifecycle must preserve independent power changes")
			f.assertOnline(t, desired, true)
		})
	}
}

func (f *bareMetalHostOnlineEnvtestFixture) testRestoration(t *testing.T) {
	c := f.c
	ctx := f.ctx
	cluster := f.cluster
	log := f.log
	for _, alreadyReinstalled := range []bool{false, true} {
		t.Run(fmt.Sprintf("restore-preserves-installation-reinstalled-%t", alreadyReinstalled), func(t *testing.T) {
			g := NewWithT(t)
			desired := f.host(false)
			originalCluster := cluster.DeepCopy()
			if alreadyReinstalled {
				originalCluster.Spec.Reinstall = &v1alpha1.ReinstallSpec{Generation: "first-reinstall"}
				originalCluster.Status.Reinstall = &v1alpha1.ReinstallStatus{ObservedGeneration: "first-reinstall"}
			}
			_, err := applyBareMetalHost(ctx, c, c, log, originalCluster, *desired, false)
			g.Expect(err).ToNot(HaveOccurred(), "initialize the original installation before simulating its Velero restoration")
			f.power(t, desired, true)
			restored := originalCluster.DeepCopy()
			restored.UID = types.UID("restored-uid")
			restored.Labels = map[string]string{veleroRestoreNameLabel: "restore-1"}
			restored.Annotations = map[string]string{veleroRestoreHandledAnnotation: "restore-1"}
			// Reconstruct the reconciler to verify there is no in-memory lifecycle state.
			restarted := &ClusterInstanceReconciler{Client: c, APIReader: c}
			_, err = restarted.applyRenderedObject(ctx, c, log, restored, *desired, false)
			g.Expect(err).ToNot(HaveOccurred(), "adopt a handled restore without resetting the installed host power state")
			f.assertOnline(t, desired, true)
			g.Expect(f.read(t, desired).GetAnnotations()[bmhOnlineInitializedAnnotation]).To(HavePrefix("restored-uid"),
				"restore adoption must migrate initialization tracking to the restored ClusterInstance UID")
			// Restore may omit status and may contain an unprocessed reinstall
			// request. The surviving BMH marker must remain authoritative then.
			restored.Status.Reinstall = nil
			restored.Spec.Reinstall = &v1alpha1.ReinstallSpec{Generation: "unprocessed-after-restore"}
			_, err = restarted.applyRenderedObject(ctx, c, log, restored, *desired, false)
			g.Expect(err).ToNot(HaveOccurred(), "restoring without reinstall status must preserve power")
			f.assertOnline(t, desired, true)
			expectedLifecycle := "restored-uid"
			if alreadyReinstalled {
				expectedLifecycle += ":first-reinstall"
			}
			g.Expect(f.read(t, desired).GetAnnotations()).To(HaveKeyWithValue(bmhOnlineInitializedAnnotation, expectedLifecycle),
				"the restored marker must retain the previous initialized generation")
			restored.Spec.Reinstall = &v1alpha1.ReinstallSpec{Generation: "next-reinstall"}
			restored.Status.Reinstall = &v1alpha1.ReinstallStatus{ObservedGeneration: "next-reinstall"}
			_, err = restarted.applyRenderedObject(ctx, c, log, restored, *desired, false)
			g.Expect(err).ToNot(HaveOccurred(), "a successful reinstall after restoration must initialize its own power default")
			f.assertOnline(t, desired, false)
		})
	}
}

func (f *bareMetalHostOnlineEnvtestFixture) testConcurrentPowerChanges(t *testing.T) {
	c := f.c
	ctx := f.ctx
	cluster := f.cluster
	log := f.log
	for _, atHandoff := range []bool{true, false} {
		t.Run(fmt.Sprintf("concurrent-power-change-at-handoff-%t", atHandoff), func(t *testing.T) {
			g := NewWithT(t)
			desired := f.host(false)
			g.Expect(f.apply(desired, false)).To(Succeed(),
				"initialize the host before racing the ownership handoff with a power update")
			raced := false
			racingClient := interceptor.NewClient(c, interceptor.Funcs{
				Apply: func(ctx context.Context, base client.WithWatch,
					object runtime.ApplyConfiguration, options ...client.ApplyOption,
				) error {
					if !raced && atHandoff {
						raced = true
						f.power(t, desired, true)
					}
					return base.Apply(ctx, object, options...)
				},
				Patch: func(ctx context.Context, base client.WithWatch,
					object client.Object, patch client.Patch, options ...client.PatchOption,
				) error {
					applied := &client.PatchOptions{}
					applied.ApplyOptions(options)
					if !raced && !atHandoff && applied.FieldManager == ClusterInstanceFieldManager {
						raced = true
						f.power(t, desired, true)
					}
					return base.Patch(ctx, object, patch, options...)
				},
			})
			_, err := applyBareMetalHost(ctx, racingClient, c, log, cluster, *desired, false)
			g.Expect(raced).To(BeTrue(), "the competing power update must occur at the selected handoff boundary")
			g.Expect(apierrors.IsConflict(err)).To(BeTrue(),
				"a concurrent power write must reject the stale handoff or metadata apply with a conflict: %v", err)
			f.assertOnline(t, desired, true)
			g.Expect(f.apply(desired, false)).To(Succeed(),
				"retry after a concurrent power write using the latest persisted host state")
			f.assertOnline(t, desired, true)
		})
	}
}

func (f *bareMetalHostOnlineEnvtestFixture) testConcurrentCreation(t *testing.T) {
	c := f.c
	ctx := f.ctx
	cluster := f.cluster
	log := f.log
	t.Run("host-created-after-not-found-is-not-overwritten", func(t *testing.T) {
		g := NewWithT(t)
		desired := f.host(false)
		readOnce := false
		racingReader := interceptor.NewClient(c, interceptor.Funcs{
			Get: func(ctx context.Context, base client.WithWatch,
				key client.ObjectKey, object client.Object, options ...client.GetOption,
			) error {
				if !readOnce {
					readOnce = true
					appeared := desired.DeepCopy()
					g.Expect(unstructured.SetNestedField(appeared.Object, true, "spec", "online")).To(Succeed(),
						"give the concurrently created host a running state different from the template default")
					g.Expect(c.Create(ctx, appeared, client.FieldOwner("installer"))).To(Succeed(),
						"create the competing host after the initial read reports it as missing")
					return apierrors.NewNotFound(schema.GroupResource{Group: "metal3.io", Resource: "baremetalhosts"}, key.Name)
				}
				return base.Get(ctx, key, object, options...)
			},
		})
		_, err := applyBareMetalHost(ctx, c, racingReader, log, cluster, *desired, false)
		g.Expect(apierrors.IsInvalid(err)).To(BeTrue(),
			"the API must reject version-zero apply against a host created after the initial read: %v", err)
		var status apierrors.APIStatus
		g.Expect(errors.As(err, &status)).To(BeTrue(), "the rejected create must retain its API status details")
		g.Expect(status.Status().Details).ToNot(BeNil(), "the API must identify the rejected field")
		g.Expect(status.Status().Details.Causes).To(ContainElement(And(
			HaveField("Field", "metadata.resourceVersion"),
			HaveField("Type", metav1.CauseTypeFieldValueInvalid),
		)), "the create-only resourceVersion precondition must cause the failure, rather than an unrelated error")
		f.assertOnline(t, desired, true)
		g.Expect(f.apply(desired, false)).To(Succeed(),
			"retry against the newly discovered host without replaying the template power default")
		f.assertOnline(t, desired, true)
	})
}
