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

package controller

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/go-logr/logr"
	bmh_v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	aiv1beta1 "github.com/openshift/assisted-service/api/v1beta1"
	"github.com/sakhoury/siteconfig/api/v1alpha1"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func getMockAgentClusterInstallTemplate() string {
	return `apiVersion: extensions.hive.openshift.io/v1beta1
kind: AgentClusterInstall
metadata:
  name: "{{ .Site.ClusterName }}"
  namespace: "{{ .Site.ClusterName }}"
  annotations:
{{ if .SpecialVars.InstallConfigOverrides }}
    agent-install.openshift.io/install-config-overrides: '{{ .SpecialVars.InstallConfigOverrides }}'
{{ end }}
    metaclusterinstall.openshift.io/sync-wave: "1"
spec:
  clusterDeploymentRef:
    name: "{{ .Site.ClusterName }}"
  holdInstallation: {{ .Site.HoldInstallation }}
  imageSetRef:
    name: "{{ .Site.ClusterImageSetNameRef }}"
{{ if .Site.ApiVIPs }}
  apiVIPs:
{{ .Site.ApiVIPs | toYaml | indent 4 }}
{{ end }}
{{ if .Site.IngressVIPs }}
  ingressVIPs:
{{ .Site.IngressVIPs | toYaml | indent 4 }}
{{ end }}
  networking:
{{ if .Site.ClusterNetwork }}
    clusterNetwork:
{{ .Site.ClusterNetwork | toYaml | indent 6 }}
{{ end }}
{{ if .Site.MachineNetwork }}
    machineNetwork:
{{ .Site.MachineNetwork | toYaml | indent 6 }}
{{ end }}
{{ if .Site.ServiceNetwork }}
    serviceNetwork:
{{ .Site.ServiceNetwork | toYaml | indent 6 }}
{{ end }}
  provisionRequirements:
    controlPlaneAgents: {{ .SpecialVars.ControlPlaneAgents }}
    workerAgents: {{ .SpecialVars.WorkerAgents }}
{{ if (anyFieldDefined .Site.ProxySettings) }}
  proxy:
{{ .Site.ProxySettings | toYaml | indent 4 }}
{{ end }}
  sshPublicKey: "{{ .Site.SSHPublicKey }}"
  manifestsConfigMapRef:
    name: "{{ .Site.ClusterName }}"`
}

func getMockNMStateConfigTemplate() string {
	return `apiVersion: agent-install.openshift.io/v1beta1
kind: NMStateConfig
metadata:
  annotations:
    metaclusterinstall.openshift.io/sync-wave: "1"
  name: "{{ .SpecialVars.CurrentNode.HostName }}"
  namespace: "{{ .Site.ClusterName }}"
  labels:
    nmstate-label: "{{ .Site.ClusterName }}"
spec:
  config:
{{ .SpecialVars.CurrentNode.NodeNetwork.NetConfig  | toYaml | indent 4}}
  interfaces:
{{ .SpecialVars.CurrentNode.NodeNetwork.Interfaces | toYaml | indent 4 }}`
}

type NetConfigData struct {
	Interfaces []*aiv1beta1.Interface
	Config     map[string]interface{}
}

func (nc *NetConfigData) RawNetConfig() string {
	b, err := yaml.Marshal(nc.Config)
	if err != nil {
		return ""
	}
	return string(b)
}

func (nc *NetConfigData) GetInterfaces() []interface{} {
	interfaces := make([]interface{}, len(nc.Interfaces))
	for i, intf := range nc.Interfaces {
		interfaces[i] = map[string]interface{}{"name": intf.Name, "macAddress": intf.MacAddress}
	}
	return interfaces
}

func getMockNetConfig() *NetConfigData {
	netConf := &NetConfigData{}

	netConf.Config = map[string]interface{}{
		"interfaces": []interface{}{
			map[string]interface{}{
				"name": "eno1",
				"type": "ethernet",
				"ipv4": map[string]interface{}{"dhcp": false, "enabled": true, "address": []interface{}{
					map[string]interface{}{"ip": "10.16.231.3", "prefix-length": 24},
					map[string]interface{}{"ip": "10.16.231.28", "prefix-length": 24},
					map[string]interface{}{"ip": "10.16.231.31", "prefix-length": 24},
				}},
				"ipv6": map[string]interface{}{"dhcp": false, "enabled": true, "address": []interface{}{
					map[string]interface{}{"ip": "2620:52:0:10e7:e42:a1ff:fe8a:601", "prefix-length": 64},
					map[string]interface{}{"ip": "2620:52:0:10e7:e42:a1ff:fe8a:602", "prefix-length": 64},
					map[string]interface{}{"ip": "2620:52:0:10e7:e42:a1ff:fe8a:603", "prefix-length": 64}},
				}},
			map[string]interface{}{
				"name":  "bond99",
				"type":  "bond",
				"state": "up",
				"ipv6": map[string]interface{}{
					"enabled":          true,
					"link-aggregation": map[string]interface{}{"mode": "balance-rr", "options": map[string]interface{}{"miimon": "140"}, "slaves": []interface{}{"eth0", "eth1"}},
					"address":          []interface{}{map[string]interface{}{"ip": "2620:52:0:1302::100", "prefix-length": 64}}}},
		},
		"dns-resolver": map[string]interface{}{"config": map[string]interface{}{"server": []interface{}{"10.19.42.41"}}},
		"routes": map[string]interface{}{
			"config": []interface{}{map[string]interface{}{"destination": "0.0.0.0/0", "next-hop-address": "10.16.231.254", "next-hop-interface": "eno1", "table-id": 254}},
		},
	}

	netConf.Interfaces = []*aiv1beta1.Interface{
		{Name: "eno1", MacAddress: "02:00:00:80:12:13"},
		{Name: "bond99", MacAddress: "02:00:00:80:12:14"},
	}

	return netConf
}

func TestSiteConfigBuilder_render(t *testing.T) {

	NetConfig := getMockNetConfig()
	TestSiteConfig := &v1alpha1.SiteConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "site-sno-du-1",
			Namespace: "site-sno-du-1",
		},
		Spec: v1alpha1.SiteConfigSpec{
			ClusterName:            "site-sno-du-1",
			PullSecretRef:          &corev1.LocalObjectReference{Name: "pullSecretName"},
			ClusterImageSetNameRef: "openshift-test",
			SSHPublicKey:           "ssh-rsa",
			BaseDomain:             "example.com",
			ApiVIPs:                []string{"1.2.3.4", "1.2.3.5"},
			HoldInstallation:       false,
			AdditionalNTPSources:   []string{"NTP.server1", "10.16.231.22"},
			MachineNetwork:         []v1alpha1.MachineNetworkEntry{{CIDR: "10.16.231.0/24"}},
			ClusterNetwork:         []v1alpha1.ClusterNetworkEntry{{CIDR: "10.128.0.0/14", HostPrefix: 23}},
			ServiceNetwork:         []string{"172.30.0.0/16"},
			NetworkType:            "OVNKubernetes",
			ClusterLabels:          map[string]string{"group-du-sno": "test", "common": "true", "sites": "site-sno-du-1"},
			InstallConfigOverrides: "{\"capabilities\":{\"baselineCapabilitySet\": \"None\", \"additionalEnabledCapabilities\": [ \"marketplace\", \"NodeTuning\" ] }}",
			IgnitionConfigOverride: "igen",
			DiskEncryption: v1alpha1.DiskEncryption{
				Type: "nbde",
				Tang: []v1alpha1.TangConfig{{URL: "http://10.0.0.1:7500", Thumbprint: "1234567890"}}},
			ProxySettings:      aiv1beta1.Proxy{NoProxy: "foobar"},
			ExtraManifestsRefs: []corev1.LocalObjectReference{{Name: "foobar1"}, {Name: "foobar2"}},
			TemplateRefs:       []v1alpha1.TemplateRef{{Name: "cluster-v1", Namespace: "site-sno-du-1"}},
			Nodes: []v1alpha1.NodeSpec{{
				BmcAddress:             "idrac-virtualmedia+https://10.16.231.87/redfish/v1/Systems/System.Embedded.1",
				BmcCredentialsName:     v1alpha1.BmcCredentialsName{Name: "bmc-secret"},
				BootMACAddress:         "00:00:00:01:20:30",
				AutomatedCleaningMode:  "disabled",
				RootDeviceHints:        bmh_v1alpha1.RootDeviceHints{HCTL: "1:2:0:0"},
				HostName:               "node1",
				Role:                   "master",
				IronicInspect:          "enabled",
				BootMode:               "UEFI",
				InstallerArgs:          "[\"--append-karg\", \"nameserver=8.8.8.8\", \"-n\"]",
				IgnitionConfigOverride: "{\"ignition\": {\"version\": \"3.1.0\"}, \"storage\": {\"files\": [{\"path\": \"/etc/containers/registries.conf\", \"overwrite\": true, \"contents\": {\"source\": \"data:text/plain;base64,aGVsbG8gZnJvbSB6dHAgcG9saWN5IGdlbmVyYXRvcg==\"}}]}}",
				TemplateRefs:           []v1alpha1.TemplateRef{{Name: "node-template", Namespace: "site-sno-du-1"}},
				NodeNetwork: aiv1beta1.NMStateConfigSpec{
					NetConfig:  aiv1beta1.NetConfig{Raw: []byte(NetConfig.RawNetConfig())},
					Interfaces: NetConfig.Interfaces,
				},
			}},
		},
	}

	TestData, _ := buildSiteData(TestSiteConfig, &TestSiteConfig.Spec.Nodes[0])

	type fields struct {
		Log logr.Logger
	}
	type args struct {
		templateType string
		templateStr  string
		data         *SiteData
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    map[string]interface{}
		wantErr bool
	}{
		{
			name: "Ttest with invalid template",
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
				templateStr:  getMockAgentClusterInstallTemplate(),
				data:         TestData,
			},
			want: map[string]interface{}{
				"apiVersion": "extensions.hive.openshift.io/v1beta1",
				"kind":       "AgentClusterInstall",
				"metadata": map[string]interface{}{
					"annotations": map[string]interface{}{
						"agent-install.openshift.io/install-config-overrides": "{\"networking\":{\"networkType\":\"OVNKubernetes\"},\"capabilities\":{\"baselineCapabilitySet\": \"None\", \"additionalEnabledCapabilities\": [ \"marketplace\", \"NodeTuning\" ] }}",
						"metaclusterinstall.openshift.io/sync-wave":           "1"},
					"name":      "site-sno-du-1",
					"namespace": "site-sno-du-1",
				},
				"spec": map[string]interface{}{
					"apiVIPs":               []interface{}{"1.2.3.4", "1.2.3.5"},
					"clusterDeploymentRef":  map[string]interface{}{"name": "site-sno-du-1"},
					"holdInstallation":      false,
					"imageSetRef":           map[string]interface{}{"name": "openshift-test"},
					"proxy":                 map[string]interface{}{"noProxy": "foobar"},
					"manifestsConfigMapRef": map[string]interface{}{"name": "site-sno-du-1"},
					"networking": map[string]interface{}{
						"clusterNetwork": []interface{}{map[string]interface{}{"cidr": "10.128.0.0/14", "hostPrefix": 23}},
						"machineNetwork": []interface{}{map[string]interface{}{"cidr": "10.16.231.0/24"}},
						"serviceNetwork": []interface{}{"172.30.0.0/16"}},
					"provisionRequirements": map[string]interface{}{"controlPlaneAgents": 1, "workerAgents": 0},
					"sshPublicKey":          "ssh-rsa"}},
			wantErr: false,
		},

		{
			name: "Test with valid NMState-like template",
			args: args{
				templateType: "NMStateConfig",
				templateStr:  getMockNMStateConfigTemplate(),
				data:         TestData,
			},
			want: map[string]interface{}{
				"apiVersion": "agent-install.openshift.io/v1beta1",
				"kind":       "NMStateConfig",
				"metadata": map[string]interface{}{
					"annotations": map[string]interface{}{"metaclusterinstall.openshift.io/sync-wave": "1"},
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scbuilder := &SiteConfigBuilder{
				Log: tt.fields.Log,
			}
			got, err := scbuilder.render(tt.args.templateType, tt.args.templateStr, tt.args.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("SiteConfigBuilder.render() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SiteConfigBuilder.render() = %v, want %v", got, tt.want)
			}
		})
	}
}

func getMockBasicClusterTemplate(kind string) string {
	return fmt.Sprintf(`apiVersion: test.io/v1
kind: %s
spec:
  name: "{{ .Site.ClusterName }}"`, kind)
}

func getMockBasicNodeTemplate(kind string) string {
	return fmt.Sprintf(`apiVersion: test.io/v1
kind: %s
spec:
  name: "{{ .SpecialVars.CurrentNode.HostName }}"`, kind)
}

var _ = Describe("renderTemplates", func() {
	var (
		c              client.Client
		ctx            = context.Background()
		scBuilder      *SiteConfigBuilder
		TestSiteConfig *v1alpha1.SiteConfig
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.SiteConfig{}).
			Build()

		testLogger := ctrl.Log.WithName("SiteConfigBuilder")
		scBuilder = NewSiteConfigBuilder(testLogger)

		TestSiteConfig = &v1alpha1.SiteConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "site-sno-du-1",
				Namespace: "site-sno-du-1",
			},
			Spec: v1alpha1.SiteConfigSpec{
				ClusterName: "site-sno-du-1",
				Nodes: []v1alpha1.NodeSpec{{
					HostName: "node1",
				}},
			},
		}
	})

	It("fails when the template reference cannot be retrieved", func() {
		TestSiteConfig.Spec.TemplateRefs = []v1alpha1.TemplateRef{{Name: "does-not-exist", Namespace: "test"}}

		_, err := scBuilder.renderTemplates(ctx, c, TestSiteConfig, nil)
		Expect(err).To(HaveOccurred())
	})

	It("fails to render template because it cannot build the site data", func() {
		TestSiteConfig.Spec.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "cluster-level", Namespace: "test"},
		}

		clusterTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-level", Namespace: "test"},
			Data: map[string]string{
				"TestA": getMockBasicClusterTemplate("TestA"),
			},
		}
		Expect(c.Create(ctx, clusterTemplates)).To(Succeed())

		TestSiteConfig.Spec.InstallConfigOverrides = "{foobar}"
		_, err := scBuilder.renderTemplates(ctx, c, TestSiteConfig, nil)
		Expect(err).To(HaveOccurred())
		Expect(err).To(MatchError(ContainSubstring("invalid json parameter set at installConfigOverride")))
	})

	It("fails to render template due to invalid template", func() {
		TestSiteConfig.Spec.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "cluster-level", Namespace: "test"},
		}

		clusterTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-level", Namespace: "test"},
			Data: map[string]string{
				"TestA": "{{.Site.doesNotExist}}",
			},
		}
		Expect(c.Create(ctx, clusterTemplates)).To(Succeed())

		_, err := scBuilder.renderTemplates(ctx, c, TestSiteConfig, nil)
		Expect(err).To(HaveOccurred())
		Expect(err).To(MatchError(ContainSubstring("field doesNotExist")))
	})

	It("suppresses rendering manifests at cluster-level", func() {

		TestSiteConfig.Spec.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "cluster-level", Namespace: "test"},
		}

		clusterTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-level", Namespace: "test"},
			Data: map[string]string{
				"TestA": getMockBasicClusterTemplate("TestA"),
				"TestB": getMockBasicClusterTemplate("TestB"),
			},
		}
		Expect(c.Create(ctx, clusterTemplates)).To(Succeed())

		TestSiteConfig.Spec.SuppressedManifests = []string{"TestA", "TestC"}

		got, err := scBuilder.renderTemplates(ctx, c, TestSiteConfig, nil)
		Expect(err).ToNot(HaveOccurred())

		Expect(len(got)).To(Equal(1))
		Expect(got[0]).To(Equal(map[string]interface{}{
			"apiVersion": "test.io/v1",
			"kind":       "TestB",
			"spec": map[string]interface{}{
				"name": "site-sno-du-1",
			},
		}))
	})

	It("suppresses rendering manifests at node-level", func() {

		node := &TestSiteConfig.Spec.Nodes[0]
		node.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "node-level", Namespace: "test"},
		}

		nodeTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "node-level", Namespace: "test"},
			Data: map[string]string{
				"TestC": getMockBasicNodeTemplate("TestC"),
				"TestD": getMockBasicNodeTemplate("TestD"),
			},
		}
		Expect(c.Create(ctx, nodeTemplates)).To(Succeed())

		node.SuppressedManifests = []string{"TestA", "TestC"}

		got, err := scBuilder.renderTemplates(ctx, c, TestSiteConfig, node)
		Expect(err).ToNot(HaveOccurred())

		Expect(len(got)).To(Equal(1))
		Expect(got[0]).To(Equal(map[string]interface{}{
			"apiVersion": "test.io/v1",
			"kind":       "TestD",
			"spec": map[string]interface{}{
				"name": "node1",
			},
		}))
	})

	It("renders a cluster-level template with extra annotations", func() {

		TestSiteConfig.Spec.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "cluster-level", Namespace: "test"},
		}

		clusterTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-level", Namespace: "test"},
			Data: map[string]string{
				"Cluster": getMockBasicClusterTemplate("Cluster"),
			},
		}
		Expect(c.Create(ctx, clusterTemplates)).To(Succeed())

		TestSiteConfig.Spec.ExtraAnnotations = map[string]map[string]string{
			"Cluster": {
				"extra-annotation-l1": "test",
				"extra-annotation-l2": "test",
			},
		}
		got, err := scBuilder.renderTemplates(ctx, c, TestSiteConfig, nil)
		Expect(err).ToNot(HaveOccurred())

		Expect(len(got)).To(Equal(1))
		Expect(got[0]).To(Equal(map[string]interface{}{
			"apiVersion": "test.io/v1",
			"kind":       "Cluster",
			"metadata": map[string]interface{}{
				"annotations": map[string]interface{}{
					"extra-annotation-l1": "test",
					"extra-annotation-l2": "test",
				},
			},
			"spec": map[string]interface{}{
				"name": "site-sno-du-1",
			},
		}))
	})

	It("renders a node-level template with extra annotations defined at cluster-level", func() {
		node := &TestSiteConfig.Spec.Nodes[0]
		node.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "node-level", Namespace: "test"},
		}

		clusterTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "node-level", Namespace: "test"},
			Data: map[string]string{
				"Node": getMockBasicNodeTemplate("Node"),
			},
		}
		Expect(c.Create(ctx, clusterTemplates)).To(Succeed())

		// Extra annotations defined at cluster-level
		TestSiteConfig.Spec.ExtraAnnotations = map[string]map[string]string{
			"Node": {
				"extra-node-annotation-l1": "test",
				"extra-node-annotation-l2": "test",
			},
		}
		got, err := scBuilder.renderTemplates(ctx, c, TestSiteConfig, node)
		Expect(err).ToNot(HaveOccurred())

		Expect(len(got)).To(Equal(1))
		Expect(got[0]).To(Equal(map[string]interface{}{
			"apiVersion": "test.io/v1",
			"kind":       "Node",
			"metadata": map[string]interface{}{
				"annotations": map[string]interface{}{
					"extra-node-annotation-l1": "test",
					"extra-node-annotation-l2": "test",
				},
			},
			"spec": map[string]interface{}{
				"name": "node1",
			},
		}))
	})

	It("renders a node-level template with extra annotations defined at node-level", func() {
		node := &TestSiteConfig.Spec.Nodes[0]
		node.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "node-level", Namespace: "test"},
		}

		clusterTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "node-level", Namespace: "test"},
			Data: map[string]string{
				"Node": getMockBasicNodeTemplate("Node"),
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
		got, err := scBuilder.renderTemplates(ctx, c, TestSiteConfig, node)
		Expect(err).ToNot(HaveOccurred())

		Expect(len(got)).To(Equal(1))
		Expect(got[0]).To(Equal(map[string]interface{}{
			"apiVersion": "test.io/v1",
			"kind":       "Node",
			"metadata": map[string]interface{}{
				"annotations": map[string]interface{}{
					"extra-node-annotation-l1": "test",
					"extra-node-annotation-l2": "test",
				},
			},
			"spec": map[string]interface{}{
				"name": "node1",
			},
		}))
	})

})

var _ = Describe("ProcessTemplates", func() {
	var (
		c              client.Client
		ctx            = context.Background()
		scBuilder      *SiteConfigBuilder
		TestSiteConfig v1alpha1.SiteConfig
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.SiteConfig{}).
			Build()

		testLogger := ctrl.Log.WithName("SiteConfigBuilder")
		scBuilder = NewSiteConfigBuilder(testLogger)

		TestSiteConfig = v1alpha1.SiteConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "site-sno-du-1",
				Namespace: "site-sno-du-1",
			},
			Spec: v1alpha1.SiteConfigSpec{
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
				"TestA": getMockBasicClusterTemplate("TestA"),
				"TestB": `{{.foobar}}`,
			},
		}
		Expect(c.Create(ctx, clusterTemplates)).To(Succeed())

		TestSiteConfig.Spec.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "cluster-level", Namespace: "test"},
		}

		_, err := scBuilder.ProcessTemplates(ctx, c, TestSiteConfig)
		Expect(err).To(HaveOccurred())
		Expect(err).To(MatchError(ContainSubstring("can't evaluate field")))
	})

	It("fails to process node-level templates due to an erroneous node template", func() {
		// Define and create cluster-level template refs
		clusterTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-level", Namespace: "test"},
			Data: map[string]string{
				"TestA": getMockBasicClusterTemplate("TestA"),
			},
		}
		Expect(c.Create(ctx, clusterTemplates)).To(Succeed())

		TestSiteConfig.Spec.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "cluster-level", Namespace: "test"},
		}

		// Define and create node-level template refs
		node := &TestSiteConfig.Spec.Nodes[0]
		nodeTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "node-level", Namespace: "test"},
			Data: map[string]string{
				"TestC": getMockBasicNodeTemplate("TestC"),
				"TestD": `{{.foobar}}`,
			},
		}
		Expect(c.Create(ctx, nodeTemplates)).To(Succeed())
		node.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "node-level", Namespace: "test"},
		}

		_, err := scBuilder.ProcessTemplates(ctx, c, TestSiteConfig)
		Expect(err).To(HaveOccurred())
		Expect(err).To(MatchError(ContainSubstring("can't evaluate field")))
	})

	It("successfully processes cluster and node level templates with manifest suppression", func() {

		// Define and create cluster-level template refs
		clusterTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-level", Namespace: "test"},
			Data: map[string]string{
				"TestA": getMockBasicClusterTemplate("TestA"),
				"TestB": getMockBasicClusterTemplate("TestB"),
			},
		}
		Expect(c.Create(ctx, clusterTemplates)).To(Succeed())

		TestSiteConfig.Spec.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "cluster-level", Namespace: "test"},
		}

		// Define and create node-level template refs
		node := &TestSiteConfig.Spec.Nodes[0]
		node.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "node-level", Namespace: "test"},
		}
		nodeTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "node-level", Namespace: "test"},
			Data: map[string]string{
				"TestC": getMockBasicNodeTemplate("TestC"),
				"TestD": getMockBasicNodeTemplate("TestD"),
			},
		}
		Expect(c.Create(ctx, nodeTemplates)).To(Succeed())

		// Define suppressed manifest lists for both cluster and node levels
		TestSiteConfig.Spec.SuppressedManifests = []string{"TestB"}
		node.SuppressedManifests = []string{"TestC"}

		// Define extra annotations for both cluster and node levels
		TestSiteConfig.Spec.ExtraAnnotations = map[string]map[string]string{
			"TestA": {
				"extra-annotation-l1": "test",
			},
		}
		node.ExtraAnnotations = map[string]map[string]string{
			"TestD": {
				"extra-node-annotation-l1": "test",
			},
		}

		got, err := scBuilder.ProcessTemplates(ctx, c, TestSiteConfig)
		Expect(err).ToNot(HaveOccurred())

		// Verify manifest suppression
		Expect(len(got)).To(Equal(2))

		// Verify rendering and extra annotations are successfully executed for cluster-level templates
		Expect(got[0]).To(Equal(map[string]interface{}{
			"apiVersion": "test.io/v1",
			"kind":       "TestA",
			"metadata": map[string]interface{}{
				"annotations": map[string]interface{}{
					"extra-annotation-l1": "test",
				},
			},
			"spec": map[string]interface{}{
				"name": "site-sno-du-1",
			},
		}))

		// Verify rendering and extra annotations are successfully executed for node-level templates
		Expect(got[1]).To(Equal(map[string]interface{}{
			"apiVersion": "test.io/v1",
			"kind":       "TestD",
			"metadata": map[string]interface{}{
				"annotations": map[string]interface{}{
					"extra-node-annotation-l1": "test",
				},
			},
			"spec": map[string]interface{}{
				"name": "node1",
			},
		}))
	})
})
