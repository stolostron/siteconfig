/*
Copyright 2024.

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

package clusterinstance

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"

	bmh_v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	aiv1beta1 "github.com/openshift/assisted-service/api/v1beta1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	"github.com/stolostron/siteconfig/api/v1alpha1"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func isEqualRenderedObject(got, expected []RenderedObject) bool {
	sort.Slice(got, func(x, y int) bool {
		return got[x].GetKind() < got[y].GetKind()
	})

	sort.Slice(expected, func(x, y int) bool {
		return expected[x].GetKind() < expected[y].GetKind()
	})

	return reflect.DeepEqual(got, expected)
}

// TestTemplateEngineRender checks template expansion and the complete decoded
// output for representative resources, including multiple documents in one template.
// It also covers template errors and conditions that produce no output.
func TestTemplateEngineRender(t *testing.T) {

	NetConfig := GetMockNetConfig()
	TestClusterImageSet := GetMockClusterImageSet("openshift-test", "test-image")
	TestClusterInstance := &v1alpha1.ClusterInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "site-sno-du-1",
			Namespace: "site-sno-du-1",
		},
		Spec: v1alpha1.ClusterInstanceSpec{
			ClusterName:            "site-sno-du-1",
			PullSecretRef:          corev1.LocalObjectReference{Name: "pullSecretName"},
			ClusterImageSetNameRef: "openshift-test",
			SSHPublicKey:           "ssh-rsa",
			BaseDomain:             "example.com",
			ApiVIPs:                []string{"192.0.2.1", "192.0.2.2"},
			HoldInstallation:       false,
			AdditionalNTPSources:   []string{"NTP.server1", "192.0.2.3"},
			MachineNetwork:         []v1alpha1.MachineNetworkEntry{{CIDR: "203.0.113.0/24"}},
			ClusterNetwork:         []v1alpha1.ClusterNetworkEntry{{CIDR: "203.0.113.0/24", HostPrefix: 23}},
			ServiceNetwork:         []v1alpha1.ServiceNetworkEntry{{CIDR: "203.0.113.0/24"}},
			NetworkType:            "OVNKubernetes",
			ExtraLabels:            map[string]map[string]string{"ManagedCluster": {"group-du-sno": "test", "common": "true", "sites": "site-sno-du-1"}},
			InstallConfigOverrides: "{\"capabilities\":{\"baselineCapabilitySet\": \"None\", \"additionalEnabledCapabilities\": [ \"marketplace\", \"NodeTuning\" ] }}",
			IgnitionConfigOverride: "igen",
			DiskEncryption: &v1alpha1.DiskEncryption{
				Type: "nbde",
				Tang: []v1alpha1.TangConfig{{URL: "http://203.0.113.1:7500", Thumbprint: "1234567890"}}},
			Proxy:              &aiv1beta1.Proxy{NoProxy: "foobar"},
			ExtraManifestsRefs: []corev1.LocalObjectReference{{Name: "foobar1"}, {Name: "foobar2"}},
			TemplateRefs:       []v1alpha1.TemplateRef{{Name: "cluster-v1", Namespace: "site-sno-du-1"}},
			Nodes: []v1alpha1.NodeSpec{{
				BmcAddress:             "idrac-virtualmedia+https://198.51.100.0/redfish/v1/Systems/System.Embedded.1",
				BmcCredentialsName:     v1alpha1.BmcCredentialsName{Name: "bmc-secret"},
				BootMACAddress:         "00:00:5E:00:53:00",
				AutomatedCleaningMode:  "disabled",
				RootDeviceHints:        &bmh_v1alpha1.RootDeviceHints{HCTL: "1:2:0:0"},
				HostName:               "node1",
				Role:                   "master",
				IronicInspect:          "enabled",
				BootMode:               "UEFI",
				InstallerArgs:          "[\"--append-karg\", \"nameserver=198.51.100.0\", \"-n\"]",
				IgnitionConfigOverride: "{\"ignition\": {\"version\": \"3.1.0\"}, \"storage\": {\"files\": [{\"path\": \"/etc/containers/registries.conf\", \"overwrite\": true, \"contents\": {\"source\": \"data:text/plain;base64,foobar==\"}}]}}",
				TemplateRefs:           []v1alpha1.TemplateRef{{Name: "node-template", Namespace: "site-sno-du-1"}},
				NodeLabels: map[string]string{
					"node-role.kubernetes.io/infra":  "",
					"node-role.kubernetes.io/master": "",
					"foo":                            "bar",
				},
				NodeNetwork: &aiv1beta1.NMStateConfigSpec{
					NetConfig:  aiv1beta1.NetConfig{Raw: []byte(NetConfig.RawNetConfig())},
					Interfaces: NetConfig.Interfaces,
				},
			}},
		},
	}

	// Set up fake client with ClusterImageSet for buildClusterData
	testScheme := scheme.Scheme
	_ = hivev1.AddToScheme(testScheme)
	c := fakeclient.NewClientBuilder().
		WithScheme(testScheme).
		WithObjects(TestClusterImageSet).
		Build()
	ctx := context.Background()

	TestData, _ := buildClusterData(ctx, c, TestClusterInstance, &TestClusterInstance.Spec.Nodes[0])

	type args struct {
		templateType string
		templateStr  string
		data         *ClusterData
	}
	tests := []struct {
		name    string
		args    args
		want    []map[string]interface{}
		wantErr bool
	}{
		{
			name: "Test with invalid template",
			args: args{
				templateType: "AgentClusterInstall",
				templateStr:  "foobar {{if}}",
				data:         TestData,
			},
			want:    nil,
			wantErr: true,
		},
		{
			name:    "Test with template execution error",
			args:    args{templateType: "InvalidField", templateStr: "{{ .Spec.doesNotExist }}", data: TestData},
			wantErr: true,
		},
		{
			name: "Test with empty output",
			args: args{templateType: "Empty", templateStr: "", data: TestData},
		},
		{
			name: "Test with whitespace-only output",
			args: args{templateType: "Whitespace", templateStr: " \t\n", data: TestData},
		},
		{
			name: "Test with false conditional output",
			args: args{templateType: "Conditional", templateStr: "{{ if .Spec.HoldInstallation }}apiVersion: v1{{ end }}", data: TestData},
		},

		{
			name: "Test with a valid AgentClusterInstall-like template",
			args: args{
				templateType: "AgentClusterInstall",
				templateStr:  GetMockAgentClusterInstallTemplate(),
				data:         TestData,
			},
			want: []map[string]interface{}{{
				"apiVersion": "extensions.hive.openshift.io/v1beta1",
				"kind":       "AgentClusterInstall",
				"metadata": map[string]interface{}{
					"annotations": map[string]interface{}{
						"agent-install.openshift.io/install-config-overrides": "{\"networking\":{\"networkType\":\"OVNKubernetes\"},\"capabilities\":{\"baselineCapabilitySet\": \"None\", \"additionalEnabledCapabilities\": [ \"marketplace\", \"NodeTuning\" ] }}",
						"siteconfig.open-cluster-management.io/sync-wave":     "1"},
					"name":      "site-sno-du-1",
					"namespace": "site-sno-du-1",
				},
				"spec": map[string]interface{}{
					"apiVIPs":               []interface{}{"192.0.2.1", "192.0.2.2"},
					"clusterDeploymentRef":  map[string]interface{}{"name": "site-sno-du-1"},
					"holdInstallation":      false,
					"imageSetRef":           map[string]interface{}{"name": "openshift-test"},
					"proxy":                 map[string]interface{}{"noProxy": "foobar"},
					"manifestsConfigMapRef": map[string]interface{}{"name": "site-sno-du-1"},
					"networking": map[string]interface{}{
						"clusterNetwork": []interface{}{map[string]interface{}{"cidr": "203.0.113.0/24", "hostPrefix": 23}},
						"machineNetwork": []interface{}{map[string]interface{}{"cidr": "203.0.113.0/24"}},
						"serviceNetwork": []interface{}{"203.0.113.0/24"}},
					"provisionRequirements": map[string]interface{}{"controlPlaneAgents": 1, "workerAgents": 0, "arbiterAgents": 0},
					"sshPublicKey":          "ssh-rsa"}}},
			wantErr: false,
		},

		{
			name: "Test with valid NMState-like template",
			args: args{
				templateType: "NMStateConfig",
				templateStr:  GetMockNMStateConfigTemplate(),
				data:         TestData,
			},
			want: []map[string]interface{}{{
				"apiVersion": "agent-install.openshift.io/v1beta1",
				"kind":       "NMStateConfig",
				"metadata": map[string]interface{}{
					"annotations": map[string]interface{}{"siteconfig.open-cluster-management.io/sync-wave": "1"},
					"labels":      map[string]interface{}{"nmstate-label": "site-sno-du-1"},
					"name":        "node1",
					"namespace":   "site-sno-du-1",
				},
				"spec": map[string]interface{}{
					"config":     NetConfig.Config,
					"interfaces": NetConfig.GetInterfaces(),
				},
			}},
			wantErr: false,
		},

		{
			name: "Test with a valid ImageClusterInstall-like template",
			args: args{
				templateType: "ImageClusterInstall",
				templateStr:  GetMockImageClusterInstallTemplate(),
				data:         TestData,
			},
			want: []map[string]interface{}{{
				"apiVersion": "extensions.hive.openshift.io/v1alpha1",
				"kind":       "ImageClusterInstall",
				"metadata": map[string]interface{}{
					"annotations": map[string]interface{}{
						"siteconfig.open-cluster-management.io/sync-wave": "1"},
					"name":      "site-sno-du-1",
					"namespace": "site-sno-du-1",
				},
				"spec": map[string]interface{}{
					"hostname":             "node1",
					"clusterDeploymentRef": map[string]interface{}{"name": "site-sno-du-1"},
					"imageSetRef":          map[string]interface{}{"name": "openshift-test"},
					"proxy":                map[string]interface{}{"noProxy": "foobar"},
					"additionalNTPSources": []interface{}{"NTP.server1", "192.0.2.3"},
					"extraManifestsRefs": []interface{}{
						map[string]interface{}{
							"name": "foobar1",
						},
						map[string]interface{}{
							"name": "foobar2",
						},
					},
					"machineNetwork":   "203.0.113.0/24",
					"bareMetalHostRef": map[string]interface{}{"name": "node1", "namespace": "site-sno-du-1"},
					"sshKey":           "ssh-rsa",
				}}},
			wantErr: false,
		},

		{
			name: "Test with valid BMH-like template",
			args: args{
				templateType: "BareMetalHost",
				templateStr: `apiVersion: metal3.io/v1alpha1
kind: BareMetalHost
metadata:
  name: "{{ .SpecialVars.CurrentNode.HostName }}"
  namespace: "{{ .Spec.ClusterName }}"
  annotations:
    siteconfig.open-cluster-management.io/sync-wave: "1"
    inspect.metal3.io: "{{ .SpecialVars.CurrentNode.IronicInspect }}"
{{ if .SpecialVars.CurrentNode.NodeLabels }}
{{ range $key, $value := .SpecialVars.CurrentNode.NodeLabels }}
    bmac.agent-install.openshift.io.node-label.{{ $key }}: {{ $value | quote}}
{{ end }}
{{ end }}
`,
				data: TestData,
			},
			want: []map[string]interface{}{{
				"apiVersion": "metal3.io/v1alpha1",
				"kind":       "BareMetalHost",
				"metadata": map[string]interface{}{
					"annotations": map[string]interface{}{
						"siteconfig.open-cluster-management.io/sync-wave": "1",
						"inspect.metal3.io": "enabled",
						"bmac.agent-install.openshift.io.node-label.node-role.kubernetes.io/infra":  "",
						"bmac.agent-install.openshift.io.node-label.node-role.kubernetes.io/master": "",
						"bmac.agent-install.openshift.io.node-label.foo":                            "bar",
					},
					"name":      "node1",
					"namespace": "site-sno-du-1",
				},
			}},
			wantErr: false,
		},

		{
			name: "Test with a valid NodePool-like template",
			args: args{
				templateType: "NodePool",
				templateStr:  GetMockNodePoolTemplate(),
				data:         TestData,
			},
			want: []map[string]interface{}{{
				"apiVersion": "hypershift.openshift.io/v1beta1",
				"kind":       "NodePool",
				"metadata": map[string]interface{}{
					"annotations": map[string]interface{}{
						"siteconfig.open-cluster-management.io/sync-wave": "2",
					},
					"name":      "site-sno-du-1",
					"namespace": "site-sno-du-1",
				},
				"spec": map[string]interface{}{
					"clusterName": "site-sno-du-1",
					"replicas":    1,
					"release": map[string]interface{}{
						"image": "test-image",
					},
				},
			}},
			wantErr: false,
		},

		{
			name: "Test with multi-document YAML template",
			args: args{
				templateType: "MultiDoc",
				templateStr: `---
apiVersion: v1
kind: ConfigMap
metadata:
  name: "{{ .Spec.ClusterName }}-config1"
  namespace: "{{ .Spec.ClusterName }}"
data:
  key1: value1
---
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: "{{ .Spec.ClusterName }}-config2"
  namespace: "{{ .Spec.ClusterName }}"
data:
  key2: value2
---
apiVersion: v1
kind: Secret
metadata:
  name: "{{ .Spec.ClusterName }}-secret"
  namespace: "{{ .Spec.ClusterName }}"
stringData:
  password: "secret123"
---
`,
				data: TestData,
			},
			want: []map[string]interface{}{
				{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "site-sno-du-1-config1",
						"namespace": "site-sno-du-1",
					},
					"data": map[string]interface{}{
						"key1": "value1",
					},
				},
				{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "site-sno-du-1-config2",
						"namespace": "site-sno-du-1",
					},
					"data": map[string]interface{}{
						"key2": "value2",
					},
				},
				{
					"apiVersion": "v1",
					"kind":       "Secret",
					"metadata": map[string]interface{}{
						"name":      "site-sno-du-1-secret",
						"namespace": "site-sno-du-1",
					},
					"stringData": map[string]interface{}{
						"password": "secret123",
					},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmplEngine := NewTemplateEngine()
			got, err := tmplEngine.render(tt.args.templateType, tt.args.templateStr, tt.args.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("TemplateEngine.render() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("TemplateEngine.render() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestTemplateEngineRenderDocumentErrors checks that decoding and validation errors
// identify the template and failing Decode call, counting skipped documents too.
// Decoder errors must omit rendered values so callers can safely log them.
func TestTemplateEngineRenderDocumentErrors(t *testing.T) {
	const validDocument = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: valid\n"
	// Four skipped documents: empty, comment-only, and both YAML null spellings.
	const skippedDocuments = "---\n---\n# comment\n---\nnull\n---\n~\n---\n"
	positions := []struct {
		name   string
		prefix string
		index  int
	}{
		{name: "first document", index: 1},
		{name: "after valid document", prefix: validDocument + "---\n", index: 2},
		{name: "after skipped documents", prefix: skippedDocuments, index: 5},
		{name: "after valid and skipped documents", prefix: validDocument + skippedDocuments, index: 6},
	}
	tests := []struct {
		name       string
		document   string
		diagnostic string
		decode     bool
	}{
		{name: "missing apiVersion", document: "kind: ConfigMap\nmetadata: {name: invalid}\n", diagnostic: "missing apiVersion"},
		{name: "empty mapping", document: "{}\n", diagnostic: "missing apiVersion"},
		{name: "invalid apiVersion", document: "apiVersion: 1\nkind: ConfigMap\nmetadata: {name: invalid}\n", diagnostic: "missing apiVersion"},
		{name: "missing kind", document: "apiVersion: v1\nmetadata: {name: invalid}\n", diagnostic: "missing kind"},
		{name: "invalid kind", document: "apiVersion: v1\nkind: []\nmetadata: {name: invalid}\n", diagnostic: "missing kind"},
		{name: "missing metadata", document: "apiVersion: v1\nkind: ConfigMap\n", diagnostic: "missing metadata"},
		{name: "invalid metadata", document: "apiVersion: v1\nkind: ConfigMap\nmetadata: invalid\n", diagnostic: "missing metadata"},
		{name: "missing name", document: "apiVersion: v1\nkind: ConfigMap\nmetadata: {}\n", diagnostic: "missing metadata.name"},
		{name: "invalid name", document: "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: 1}\n", diagnostic: "missing metadata.name"},
		{name: "malformed YAML", document: "apiVersion: [v1\n", decode: true},
		{name: "scalar", document: "scalar\n", decode: true},
		{name: "sequence", document: "- item\n", decode: true},
	}
	for _, position := range positions {
		for _, tt := range tests {
			t.Run(position.name+"/"+tt.name, func(t *testing.T) {
				g := NewWithT(t)
				_, err := NewTemplateEngine().render("resources.yaml", position.prefix+tt.document, nil)
				g.Expect(err).To(HaveOccurred(), "reject %s at %s", tt.name, position.name)
				g.Expect(err.Error()).To(ContainSubstring(fmt.Sprintf("YAML document %d", position.index)), "document numbering should count skipped documents")
				g.Expect(err.Error()).To(ContainSubstring("template resources.yaml"), "the error should identify the failing template key")
				if tt.decode {
					g.Expect(err).To(MatchError(fmt.Sprintf("failed to unmarshal YAML document %d in template resources.yaml", position.index)), "decoder errors should expose only the document location")
					g.Expect(errors.Unwrap(err)).To(BeNil(), "raw decoder errors must not expose rendered values through unwrapping")
				} else {
					g.Expect(errors.Unwrap(err)).NotTo(BeNil(), "validation errors should retain the missing-field diagnostic")
					g.Expect(err.Error()).To(Equal(fmt.Sprintf("failed to validate YAML document %d: %s in template resources.yaml", position.index, tt.diagnostic)), "validation errors should identify the invalid field and document")
				}
			})
		}
	}
	t.Run("decoder read-ahead reports the failing Decode call", func(t *testing.T) {
		g := NewWithT(t)
		_, err := NewTemplateEngine().render("resources.yaml", validDocument+"---\n@invalid\n", nil)
		g.Expect(err).To(HaveOccurred(), "decoder read-ahead should detect malformed content in the next document")
		g.Expect(err.Error()).To(ContainSubstring("YAML document 1 in template resources.yaml"), "the index should identify the failing Decode call")
		g.Expect(err).To(MatchError("failed to unmarshal YAML document 1 in template resources.yaml"), "read-ahead errors should still omit rendered content")
		g.Expect(errors.Unwrap(err)).To(BeNil(), "the raw read-ahead error must not be exposed through unwrapping")
	})
}

var _ = Describe("renderTemplates", func() {
	var (
		c                   client.Client
		ctx                 = context.Background()
		testLogger          = zap.NewNop().Named("Test")
		tmplEngine          = NewTemplateEngine()
		TestClusterInstance *v1alpha1.ClusterInstance
		TestClusterImageSet *hivev1.ClusterImageSet
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			Build()

		TestClusterInstance = &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "site-sno-du-1",
				Namespace: "site-sno-du-1",
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				ClusterName: "site-sno-du-1",
				Nodes: []v1alpha1.NodeSpec{{
					HostName: "node1",
				}},
				ClusterImageSetNameRef: "openshift-test",
			},
		}
		TestClusterImageSet = GetMockClusterImageSet("openshift-test", "test-image")
		Expect(c.Create(ctx, TestClusterImageSet)).To(Succeed())
	})

	It("fails when the template reference cannot be retrieved", func() {
		TestClusterInstance.Spec.TemplateRefs = []v1alpha1.TemplateRef{{Name: "does-not-exist", Namespace: "test"}}

		_, err := tmplEngine.renderTemplates(ctx, c, testLogger, TestClusterInstance, nil)
		Expect(err).To(HaveOccurred())
	})

	It("fails to render template because it cannot build the site data", func() {
		TestClusterInstance.Spec.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "cluster-level", Namespace: "test"},
		}

		clusterTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-level", Namespace: "test"},
			Data: map[string]string{
				"TestA": GetMockBasicClusterTemplate("TestA"),
			},
		}
		Expect(c.Create(ctx, clusterTemplates)).To(Succeed())

		TestClusterInstance.Spec.InstallConfigOverrides = "{foobar}"
		_, err := tmplEngine.renderTemplates(ctx, c, testLogger, TestClusterInstance, nil)
		Expect(err).To(HaveOccurred())
		Expect(err).To(MatchError(ContainSubstring("invalid json parameter set at installConfigOverride")))
	})

	It("fails to render template due to invalid template", func() {
		TestClusterInstance.Spec.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "cluster-level", Namespace: "test"},
		}

		clusterTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-level", Namespace: "test"},
			Data: map[string]string{
				"TestA": "{{.Spec.doesNotExist}}",
			},
		}
		Expect(c.Create(ctx, clusterTemplates)).To(Succeed())

		_, err := tmplEngine.renderTemplates(ctx, c, testLogger, TestClusterInstance, nil)
		Expect(err).To(HaveOccurred())
		Expect(err).To(MatchError(ContainSubstring("field doesNotExist")))
	})

	DescribeTable("rejects the entire template when a later document fails", func(invalidDocument string) {
		const validDocument = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: valid\n"
		objects, err := tmplEngine.renderManifestFromTemplate(ctx, c, testLogger, TestClusterInstance, nil,
			"cluster-level", "resources.yaml", validDocument+"---\n"+invalidDocument)
		Expect(err).To(HaveOccurred(), "an invalid later document should reject the entire template")
		Expect(err.Error()).To(ContainSubstring("YAML document 2"), "the error should identify the invalid later document")
		Expect(err.Error()).To(ContainSubstring("template resources.yaml"), "the error should identify the failing template key")
		Expect(objects).To(BeEmpty(), "the valid first document must not escape a rejected template")
	},
		Entry("malformed YAML", "apiVersion: [v1\n"),
		Entry("missing required field", "apiVersion: v1\nkind: ConfigMap\n"),
	)

	It("suppresses rendering manifests at cluster-level", func() {

		TestClusterInstance.Spec.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "cluster-level", Namespace: "test"},
		}

		clusterTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-level", Namespace: "test"},
			Data: map[string]string{
				"TestA": GetMockBasicClusterTemplate("TestA"),
				"TestB": GetMockBasicClusterTemplate("TestB"),
			},
		}
		Expect(c.Create(ctx, clusterTemplates)).To(Succeed())

		TestClusterInstance.Spec.SuppressedManifests = []string{"TestA", "TestC"}

		got, err := tmplEngine.renderTemplates(ctx, c, testLogger, TestClusterInstance, nil)
		Expect(err).ToNot(HaveOccurred())

		Expect(len(got)).To(Equal(2))

		expected := []RenderedObject{
			{
				action: actionSuppress,
				object: unstructured.Unstructured{Object: map[string]interface{}{
					"apiVersion": "test.io/v1",
					"kind":       "TestA",
					"metadata": map[string]interface{}{
						"name":      TestClusterInstance.Name,
						"namespace": TestClusterInstance.Namespace,
						// should not contain labels
					},
					"spec": map[string]interface{}{
						"name": "site-sno-du-1",
					},
				}},
			},
			{
				action: actionRender,
				object: unstructured.Unstructured{Object: map[string]interface{}{
					"apiVersion": "test.io/v1",
					"kind":       "TestB",
					"metadata": map[string]interface{}{
						"name":      TestClusterInstance.Name,
						"namespace": TestClusterInstance.Namespace,
						"labels": map[string]interface{}{
							OwnedByLabel: GenerateOwnedByLabelValue(TestClusterInstance.Namespace,
								TestClusterInstance.Name),
						},
					},
					"spec": map[string]interface{}{
						"name": "site-sno-du-1",
					},
				}},
			},
		}

		Expect(isEqualRenderedObject(got, expected)).To(BeTrue())
	})

	It("processes each document in a multi-document template", func() {
		TestClusterInstance.Spec.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "cluster-level", Namespace: "test"},
		}

		clusterTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-level", Namespace: "test"},
			Data: map[string]string{
				"MultiDoc": `---
apiVersion: test.io/v1
kind: TestRendered
metadata:
  name: rendered
  namespace: "{{ .Spec.ClusterName }}"
---
apiVersion: test.io/v1
kind: TestPruned
metadata:
  name: pruned
  namespace: "{{ .Spec.ClusterName }}"
---
apiVersion: test.io/v1
kind: TestSuppressed
metadata:
  name: suppressed
  namespace: "{{ .Spec.ClusterName }}"
---
`,
			},
		}
		Expect(c.Create(ctx, clusterTemplates)).To(Succeed(), "create the template containing render, prune, and suppress candidates")

		TestClusterInstance.Spec.PruneManifests = []v1alpha1.ResourceRef{{
			APIVersion: "test.io/v1",
			Kind:       "TestPruned",
		}}
		TestClusterInstance.Spec.SuppressedManifests = []string{"TestSuppressed"}
		TestClusterInstance.Spec.ExtraLabels = map[string]map[string]string{
			"TestRendered": {"test-label": "test-value"},
		}

		got, err := tmplEngine.renderTemplates(ctx, c, testLogger, TestClusterInstance, nil)
		Expect(err).ToNot(HaveOccurred(), "a stream with mixed render, prune, and suppress actions should succeed")
		Expect(got).To(HaveLen(3), "each document should produce an independently processed resource")

		objectsByKind := make(map[string]RenderedObject, len(got))
		for _, object := range got {
			objectsByKind[object.GetKind()] = object
		}

		rendered := objectsByKind["TestRendered"]
		Expect(rendered.action).To(Equal(actionRender), "the unselected kind should retain the default render action")
		Expect(rendered.GetLabels()[OwnedByLabel]).To(Equal(GenerateOwnedByLabelValue(
			TestClusterInstance.Namespace, TestClusterInstance.Name)), "the rendered document should receive ClusterInstance ownership")
		Expect(rendered.GetLabels()["test-label"]).To(Equal("test-value"), "the rendered document should receive user-specified labels")

		Expect(objectsByKind["TestPruned"].action).To(Equal(actionPrune), "pruning should affect only the selected document kind")
		Expect(objectsByKind["TestSuppressed"].action).To(Equal(actionSuppress), "suppression should affect only the selected document kind")
	})

	It("suppresses rendering manifests at node-level", func() {

		node := &TestClusterInstance.Spec.Nodes[0]
		node.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "node-level", Namespace: "test"},
		}

		nodeTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "node-level", Namespace: "test"},
			Data: map[string]string{
				"TestC": GetMockBasicNodeTemplate("TestC"),
				"TestD": GetMockBasicNodeTemplate("TestD"),
			},
		}
		Expect(c.Create(ctx, nodeTemplates)).To(Succeed())

		node.SuppressedManifests = []string{"TestA", "TestC"}

		got, err := tmplEngine.renderTemplates(ctx, c, testLogger, TestClusterInstance, node)
		Expect(err).ToNot(HaveOccurred())

		expected := []RenderedObject{
			{
				action: actionSuppress,
				object: unstructured.Unstructured{Object: map[string]interface{}{
					"apiVersion": "test.io/v1",
					"kind":       "TestC",
					"metadata": map[string]interface{}{
						"name":      TestClusterInstance.Spec.Nodes[0].HostName,
						"namespace": TestClusterInstance.Namespace,
					},
					"spec": map[string]interface{}{
						"name": "node1",
					},
				},
				}},
			{
				action: actionRender,
				object: unstructured.Unstructured{Object: map[string]interface{}{
					"apiVersion": "test.io/v1",
					"kind":       "TestD",
					"metadata": map[string]interface{}{
						"name":      TestClusterInstance.Spec.Nodes[0].HostName,
						"namespace": TestClusterInstance.Namespace,
						"labels": map[string]interface{}{
							OwnedByLabel: GenerateOwnedByLabelValue(TestClusterInstance.Namespace,
								TestClusterInstance.Name),
						},
					},
					"spec": map[string]interface{}{
						"name": "node1",
					},
				}},
			},
		}

		Expect(isEqualRenderedObject(got, expected)).To(BeTrue())
	})

	It("renders a cluster-level template with extra annotations and labels", func() {

		TestClusterInstance.Spec.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "cluster-level", Namespace: "test"},
		}

		clusterTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-level", Namespace: "test"},
			Data: map[string]string{
				"Cluster": GetMockBasicClusterTemplate("Cluster"),
			},
		}
		Expect(c.Create(ctx, clusterTemplates)).To(Succeed())

		TestClusterInstance.Spec.ExtraAnnotations = map[string]map[string]string{
			"Cluster": {
				"extra-annotation-l1": "test",
				"extra-annotation-l2": "test",
			},
		}
		TestClusterInstance.Spec.ExtraLabels = map[string]map[string]string{
			"Cluster": {
				"extra-labels-l1": "test",
				"extra-labels-l2": "test",
			},
		}
		got, err := tmplEngine.renderTemplates(ctx, c, testLogger, TestClusterInstance, nil)
		Expect(err).ToNot(HaveOccurred())

		expected := []RenderedObject{
			{
				action: actionRender,
				object: unstructured.Unstructured{Object: map[string]interface{}{
					"apiVersion": "test.io/v1",
					"kind":       "Cluster",
					"metadata": map[string]interface{}{
						"name":      TestClusterInstance.Spec.ClusterName,
						"namespace": TestClusterInstance.Namespace,
						"annotations": map[string]interface{}{
							"extra-annotation-l1": "test",
							"extra-annotation-l2": "test",
						},
						"labels": map[string]interface{}{
							OwnedByLabel:      GenerateOwnedByLabelValue(TestClusterInstance.Namespace, TestClusterInstance.Name),
							"extra-labels-l1": "test",
							"extra-labels-l2": "test",
						},
					},
					"spec": map[string]interface{}{
						"name": "site-sno-du-1",
					},
				}},
			},
		}

		Expect(isEqualRenderedObject(got, expected)).To(BeTrue())
	})

	It("renders a node-level template with extra annotations and labels defined at cluster-level", func() {
		node := &TestClusterInstance.Spec.Nodes[0]
		node.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "node-level", Namespace: "test"},
		}

		clusterTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "node-level", Namespace: "test"},
			Data: map[string]string{
				"Node": GetMockBasicNodeTemplate("Node"),
			},
		}
		Expect(c.Create(ctx, clusterTemplates)).To(Succeed())

		// Extra annotations defined at cluster-level
		TestClusterInstance.Spec.ExtraAnnotations = map[string]map[string]string{
			"Node": {
				"extra-node-annotation-l1": "test",
				"extra-node-annotation-l2": "test",
			},
		}
		TestClusterInstance.Spec.ExtraLabels = map[string]map[string]string{
			"Node": {
				"extra-labels-l1": "test",
				"extra-labels-l2": "test",
			},
		}
		got, err := tmplEngine.renderTemplates(ctx, c, testLogger, TestClusterInstance, node)
		Expect(err).ToNot(HaveOccurred())

		expected := []RenderedObject{
			{
				action: actionRender,
				object: unstructured.Unstructured{Object: map[string]interface{}{
					"apiVersion": "test.io/v1",
					"kind":       "Node",
					"metadata": map[string]interface{}{
						"name":      TestClusterInstance.Spec.Nodes[0].HostName,
						"namespace": TestClusterInstance.Namespace,
						"annotations": map[string]interface{}{
							"extra-node-annotation-l1": "test",
							"extra-node-annotation-l2": "test",
						},
						"labels": map[string]interface{}{
							OwnedByLabel: GenerateOwnedByLabelValue(TestClusterInstance.Namespace,
								TestClusterInstance.Name),
							"extra-labels-l1": "test",
							"extra-labels-l2": "test",
						},
					},
					"spec": map[string]interface{}{
						"name": TestClusterInstance.Spec.Nodes[0].HostName,
					},
				}},
			},
		}

		Expect(isEqualRenderedObject(got, expected)).To(BeTrue())
	})

	It("renders a node-level template with extra annotations and labels defined at node-level", func() {
		node := &TestClusterInstance.Spec.Nodes[0]
		node.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "node-level", Namespace: "test"},
		}

		clusterTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "node-level", Namespace: "test"},
			Data: map[string]string{
				"Node": GetMockBasicNodeTemplate("Node"),
			},
		}
		Expect(c.Create(ctx, clusterTemplates)).To(Succeed())

		// Extra annotations defined at node-level
		node.ExtraAnnotations = map[string]map[string]string{
			"Node": {
				"extra-node-annotation-l1": "test",
				"extra-node-annotation-l2": "test",
			},
		}
		node.ExtraLabels = map[string]map[string]string{
			"Node": {
				"extra-node-labels-l1": "test",
				"extra-node-labels-l2": "test",
			},
		}
		got, err := tmplEngine.renderTemplates(ctx, c, testLogger, TestClusterInstance, node)
		Expect(err).ToNot(HaveOccurred())

		expected := []RenderedObject{
			{
				action: actionRender,
				object: unstructured.Unstructured{Object: map[string]interface{}{
					"apiVersion": "test.io/v1",
					"kind":       "Node",
					"metadata": map[string]interface{}{
						"name":      TestClusterInstance.Spec.Nodes[0].HostName,
						"namespace": TestClusterInstance.Namespace,
						"annotations": map[string]interface{}{
							"extra-node-annotation-l1": "test",
							"extra-node-annotation-l2": "test",
						},
						"labels": map[string]interface{}{
							OwnedByLabel: GenerateOwnedByLabelValue(TestClusterInstance.Namespace,
								TestClusterInstance.Name),
							"extra-node-labels-l1": "test",
							"extra-node-labels-l2": "test",
						},
					},
					"spec": map[string]interface{}{
						"name": TestClusterInstance.Spec.Nodes[0].HostName,
					},
				}},
			},
		}

		Expect(isEqualRenderedObject(got, expected)).To(BeTrue())
	})

})

var _ = Describe("ProcessTemplates", func() {
	var (
		c                   client.Client
		ctx                 = context.Background()
		tmplEngine          = NewTemplateEngine()
		testLogger          = zap.NewNop().Named("Test")
		TestClusterInstance v1alpha1.ClusterInstance
		TestClusterImageSet *hivev1.ClusterImageSet
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			Build()

		TestClusterInstance = v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "site-sno-du-1",
				Namespace: "site-sno-du-1",
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				ClusterName: "site-sno-du-1",
				Nodes: []v1alpha1.NodeSpec{{
					HostName: "node1",
				}},
				ClusterImageSetNameRef: "openshift-test",
			},
		}
		TestClusterImageSet = GetMockClusterImageSet("openshift-test", "test-image")
	})

	It("fails to process cluster-level templates due to an erroneous cluster template", func() {
		// Define and create cluster-level template refs
		clusterTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-level", Namespace: "test"},
			Data: map[string]string{
				"TestA": GetMockBasicClusterTemplate("TestA"),
				"TestB": `{{.foobar}}`,
			},
		}
		Expect(c.Create(ctx, clusterTemplates)).To(Succeed())
		Expect(c.Create(ctx, TestClusterImageSet)).To(Succeed())

		TestClusterInstance.Spec.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "cluster-level", Namespace: "test"},
		}

		_, err := tmplEngine.ProcessTemplates(ctx, c, testLogger, TestClusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(err).To(MatchError(ContainSubstring("can't evaluate field")))
	})

	It("fails to process node-level templates due to an erroneous node template", func() {
		// Define and create cluster-level template refs
		clusterTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-level", Namespace: "test"},
			Data: map[string]string{
				"TestA": GetMockBasicClusterTemplate("TestA"),
			},
		}
		Expect(c.Create(ctx, clusterTemplates)).To(Succeed())
		Expect(c.Create(ctx, TestClusterImageSet)).To(Succeed())

		TestClusterInstance.Spec.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "cluster-level", Namespace: "test"},
		}

		// Define and create node-level template refs
		node := &TestClusterInstance.Spec.Nodes[0]
		nodeTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "node-level", Namespace: "test"},
			Data: map[string]string{
				"TestC": GetMockBasicNodeTemplate("TestC"),
				"TestD": `{{.foobar}}`,
			},
		}
		Expect(c.Create(ctx, nodeTemplates)).To(Succeed())
		node.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "node-level", Namespace: "test"},
		}

		_, err := tmplEngine.ProcessTemplates(ctx, c, testLogger, TestClusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(err).To(MatchError(ContainSubstring("can't evaluate field")))
	})

	It("fails to build cluster data due to a missing clusterimageset", func() {
		// Set up a template ref so ProcessTemplates will try to render templates
		clusterTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-level", Namespace: "test"},
			Data: map[string]string{
				"TestA": GetMockBasicClusterTemplate("TestA"),
			},
		}
		Expect(c.Create(ctx, clusterTemplates)).To(Succeed())

		TestClusterInstance.Spec.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "cluster-level", Namespace: "test"},
		}

		// Sabotage clusterInstance with incorrect clusterImageSetNameRef
		TestClusterInstance.Spec.ClusterImageSetNameRef = "foobar"
		_, err := tmplEngine.ProcessTemplates(ctx, c, testLogger, TestClusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(err).To(MatchError(ContainSubstring("failed to get ClusterImageSet foobar")))
	})

	It("successfully processes cluster and node level templates with manifest suppression", func() {

		// Define and create cluster-level template refs
		clusterTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-level", Namespace: "test"},
			Data: map[string]string{
				"TestA": GetMockBasicClusterTemplate("TestA"),
				"TestB": GetMockBasicClusterTemplate("TestB"),
			},
		}
		Expect(c.Create(ctx, clusterTemplates)).To(Succeed())
		Expect(c.Create(ctx, TestClusterImageSet)).To(Succeed())

		TestClusterInstance.Spec.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "cluster-level", Namespace: "test"},
		}

		// Define and create node-level template refs
		node := &TestClusterInstance.Spec.Nodes[0]
		node.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "node-level", Namespace: "test"},
		}
		nodeTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "node-level", Namespace: "test"},
			Data: map[string]string{
				"TestC": GetMockBasicNodeTemplate("TestC"),
				"TestD": GetMockBasicNodeTemplate("TestD"),
			},
		}
		Expect(c.Create(ctx, nodeTemplates)).To(Succeed())

		// Define suppressed manifest lists for both cluster and node levels
		TestClusterInstance.Spec.SuppressedManifests = []string{"TestB"}
		node.SuppressedManifests = []string{"TestC"}

		// Define extra annotations for both cluster and node levels
		TestClusterInstance.Spec.ExtraAnnotations = map[string]map[string]string{
			"TestA": {
				"extra-annotation-l1": "test",
			},
		}
		node.ExtraAnnotations = map[string]map[string]string{
			"TestD": {
				"extra-node-annotation-l1": "test",
			},
		}

		// Define extra labels for both cluster and node levels
		TestClusterInstance.Spec.ExtraLabels = map[string]map[string]string{
			"TestA": {
				"extra-label-l1": "test",
			},
		}
		node.ExtraLabels = map[string]map[string]string{
			"TestD": {
				"extra-node-label-l1": "test",
			},
		}

		got, err := tmplEngine.ProcessTemplates(ctx, c, testLogger, TestClusterInstance)
		Expect(err).ToNot(HaveOccurred())

		// Verify manifest suppression
		expectedSlice := []RenderedObject{
			{
				// Verify rendering and extra annotations & labels are successfully executed for cluster-level templates
				action: actionRender,
				object: unstructured.Unstructured{Object: map[string]interface{}{
					"apiVersion": "test.io/v1",
					"kind":       "TestA",
					"metadata": map[string]interface{}{
						"name":      TestClusterInstance.Spec.ClusterName,
						"namespace": TestClusterInstance.Namespace,
						"annotations": map[string]interface{}{
							"extra-annotation-l1": "test",
						},
						"labels": map[string]interface{}{
							OwnedByLabel: GenerateOwnedByLabelValue(TestClusterInstance.Namespace,
								TestClusterInstance.Name),
							"extra-label-l1": "test",
						},
					},
					"spec": map[string]interface{}{
						"name": TestClusterInstance.Spec.ClusterName,
					},
				}},
			},
			{
				action: actionSuppress,
				object: unstructured.Unstructured{Object: map[string]interface{}{
					"apiVersion": "test.io/v1",
					"kind":       "TestB",
					"metadata": map[string]interface{}{
						"name":      TestClusterInstance.Spec.ClusterName,
						"namespace": TestClusterInstance.Namespace,
					},
					"spec": map[string]interface{}{
						"name": TestClusterInstance.Spec.ClusterName,
					},
				}},
			},
			{
				action: actionSuppress,
				object: unstructured.Unstructured{Object: map[string]interface{}{
					"apiVersion": "test.io/v1",
					"kind":       "TestC",
					"metadata": map[string]interface{}{
						"name":      TestClusterInstance.Spec.Nodes[0].HostName,
						"namespace": TestClusterInstance.Namespace,
					},
					"spec": map[string]interface{}{
						"name": TestClusterInstance.Spec.Nodes[0].HostName,
					},
				}},
			},
			{
				// Verify rendering and extra annotations & labels are successfully executed for node-level templates
				action: actionRender,
				object: unstructured.Unstructured{Object: map[string]interface{}{
					"apiVersion": "test.io/v1",
					"kind":       "TestD",
					"metadata": map[string]interface{}{
						"name":      TestClusterInstance.Spec.Nodes[0].HostName,
						"namespace": TestClusterInstance.Namespace,
						"annotations": map[string]interface{}{
							"extra-node-annotation-l1": "test",
						},
						"labels": map[string]interface{}{
							OwnedByLabel: GenerateOwnedByLabelValue(TestClusterInstance.Namespace,
								TestClusterInstance.Name),
							"extra-node-label-l1": "test",
						},
					},
					"spec": map[string]interface{}{
						"name": TestClusterInstance.Spec.Nodes[0].HostName,
					},
				}},
			},
		}
		expected := RenderedObjectCollection{}
		err = expected.AddObjects(expectedSlice)
		Expect(err).ToNot(HaveOccurred())
		Expect(reflect.DeepEqual(got, expected)).To(BeTrue())
	})
})
