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
	"reflect"
	"sort"
	"testing"

	bmh_v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	aiv1beta1 "github.com/openshift/assisted-service/api/v1beta1"
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

func TestTemplateEngineRender(t *testing.T) {

	NetConfig := GetMockNetConfig()
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

	TestData, _ := buildClusterData(TestClusterInstance, &TestClusterInstance.Spec.Nodes[0])

	type args struct {
		templateType string
		templateStr  string
		data         *ClusterData
	}
	tests := []struct {
		name    string
		args    args
		want    map[string]interface{}
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
			name: "Test with a valid AgentClusterInstall-like template",
			args: args{
				templateType: "AgentClusterInstall",
				templateStr:  GetMockAgentClusterInstallTemplate(),
				data:         TestData,
			},
			want: map[string]interface{}{
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
					"provisionRequirements": map[string]interface{}{"controlPlaneAgents": 1, "workerAgents": 0},
					"sshPublicKey":          "ssh-rsa"}},
			wantErr: false,
		},

		{
			name: "Test with valid NMState-like template",
			args: args{
				templateType: "NMStateConfig",
				templateStr:  GetMockNMStateConfigTemplate(),
				data:         TestData,
			},
			want: map[string]interface{}{
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
			},
			wantErr: false,
		},

		{
			name: "Test with a valid ImageClusterInstall-like template",
			args: args{
				templateType: "ImageClusterInstall",
				templateStr:  GetMockImageClusterInstallTemplate(),
				data:         TestData,
			},
			want: map[string]interface{}{
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
				}},
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
			want: map[string]interface{}{
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

var _ = Describe("renderTemplates", func() {
	var (
		c                   client.Client
		ctx                 = context.Background()
		testLogger          = zap.NewNop().Named("Test")
		tmplEngine          = NewTemplateEngine()
		TestClusterInstance *v1alpha1.ClusterInstance
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
			},
		}
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
			},
		}
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
