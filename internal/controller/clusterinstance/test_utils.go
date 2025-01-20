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
*/

package clusterinstance

import (
	"context"
	"fmt"

	aiv1beta1 "github.com/openshift/assisted-service/api/v1beta1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/stolostron/siteconfig/api/v1alpha1"

	"github.com/onsi/gomega"
)

const (
	secretResource    = "Secret"
	configMapResource = "ConfigMap"

	// nolint:gosec
	testPullSecretVal = `{"auths":{"cloud.openshift.com":{"auth":"dXNlcjpwYXNzd29yZAo=","email":"r@r.com"}}}`
)

type TestParams struct {
	ClusterName         string
	ClusterNamespace    string
	PullSecret          string
	BmcCredentialsName  string
	ClusterImageSetName string
	ExtraManifestName   string
	ClusterTemplateRef  string
	NodeTemplateRef     string
}

func (tp *TestParams) GeneratePullSecret() *corev1.Secret {
	return GetMockPullSecret(tp.PullSecret, tp.ClusterNamespace)
}

func (tp *TestParams) GenerateBMCSecret() *corev1.Secret {
	return GetMockBmcSecret(tp.BmcCredentialsName, tp.ClusterNamespace)
}

func (tp *TestParams) GenerateClusterImageSet() *hivev1.ClusterImageSet {
	return GetMockClusterImageSet(tp.ClusterImageSetName)
}

func (tp *TestParams) GenerateExtraManifest() *corev1.ConfigMap {
	return GetMockExtraManifest(tp.ExtraManifestName, tp.ClusterNamespace)
}

func (tp *TestParams) GenerateClusterTemplate() *corev1.ConfigMap {
	return GetMockClusterTemplate(tp.ClusterTemplateRef, tp.ClusterNamespace)
}

func (tp *TestParams) GenerateNodeTemplate() *corev1.ConfigMap {
	return GetMockNodeTemplate(tp.NodeTemplateRef, tp.ClusterNamespace)
}

func (tp *TestParams) GenerateSNOClusterInstance() *v1alpha1.ClusterInstance {
	return GetMockSNOClusterInstance(tp)
}

func (tp *TestParams) GetResources() map[string]string {
	resources := map[string]string{}

	if tp.PullSecret != "" {
		resources[tp.PullSecret] = secretResource
	}

	if tp.BmcCredentialsName != "" {
		resources[tp.BmcCredentialsName] = secretResource
	}

	if tp.ClusterImageSetName != "" {
		resources[tp.ClusterImageSetName] = "ClusterImageSet"
	}

	if tp.ClusterTemplateRef != "" {
		resources[tp.ClusterTemplateRef] = configMapResource
	}

	if tp.NodeTemplateRef != "" {
		resources[tp.NodeTemplateRef] = configMapResource
	}

	if tp.ExtraManifestName != "" {
		resources[tp.ExtraManifestName] = configMapResource
	}

	return resources
}

func GetMockBmcSecret(name, namespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string][]byte{"username": []byte("admin"), "password": []byte("password")}}
}

func GetMockClusterImageSet(name string) *hivev1.ClusterImageSet {
	return &hivev1.ClusterImageSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "",
		}}
}

func GetMockPullSecret(name, namespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte(testPullSecretVal)}}
}

func GetMockClusterTemplate(name, namespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string]string{"ManagedCluster": "foobar"}}
}

func GetMockNodeTemplate(name, namespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string]string{"BareMetalhost": "foobar"}}
}

func GetMockExtraManifest(name, namespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string]string{"foo": "bar"}}
}

func GetMockSNOClusterInstance(testParams *TestParams) *v1alpha1.ClusterInstance {
	installConfigOverrides := `{"networking":{"networkType":"OVNKubernetes"}, "cpuPartitioningMode":"AllNodes"}`
	installerArgs := `["--append-karg", "nameserver=203.0.113.0", "-n"]`
	nodeIgnitionConfigOverride := `{"ignition": {"version": "3.1.0"}, "storage": {"files": [` +
		`{"path": "/etc/containers/registries.conf", "overwrite": true, ` +
		`"contents": {"source": "data:text/plain;base64,foobar=="}}]}}`

	return &v1alpha1.ClusterInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testParams.ClusterName,
			Namespace: testParams.ClusterNamespace,
		},
		Spec: v1alpha1.ClusterInstanceSpec{
			ClusterName:            testParams.ClusterName,
			PullSecretRef:          corev1.LocalObjectReference{Name: testParams.PullSecret},
			ClusterImageSetNameRef: testParams.ClusterImageSetName,
			SSHPublicKey:           "test-ssh",
			BaseDomain:             "abcd",
			ClusterType:            v1alpha1.ClusterTypeSNO,
			ExtraManifestsRefs:     []corev1.LocalObjectReference{{Name: testParams.ExtraManifestName}},
			TemplateRefs: []v1alpha1.TemplateRef{
				{Name: testParams.ClusterTemplateRef, Namespace: testParams.ClusterNamespace}},
			InstallConfigOverrides: installConfigOverrides,
			Nodes: []v1alpha1.NodeSpec{{
				Role:                   "master",
				BmcAddress:             "192.0.2.1",
				BmcCredentialsName:     v1alpha1.BmcCredentialsName{Name: testParams.BmcCredentialsName},
				InstallerArgs:          installerArgs,
				IgnitionConfigOverride: nodeIgnitionConfigOverride,
				TemplateRefs: []v1alpha1.TemplateRef{
					{Name: testParams.NodeTemplateRef, Namespace: testParams.ClusterNamespace}}}}},
	}
}

func GetMockAgentClusterInstallTemplate() string {
	return `apiVersion: extensions.hive.openshift.io/v1beta1
kind: AgentClusterInstall
metadata:
  name: "{{ .Spec.ClusterName }}"
  namespace: "{{ .Spec.ClusterName }}"
  annotations:
{{ if .SpecialVars.InstallConfigOverrides }}
    agent-install.openshift.io/install-config-overrides: '{{ .SpecialVars.InstallConfigOverrides }}'
{{ end }}
    siteconfig.open-cluster-management.io/sync-wave: "1"
spec:
  clusterDeploymentRef:
    name: "{{ .Spec.ClusterName }}"
  holdInstallation: {{ .Spec.HoldInstallation }}
  imageSetRef:
    name: "{{ .Spec.ClusterImageSetNameRef }}"
{{ if .Spec.ApiVIPs }}
  apiVIPs:
{{ .Spec.ApiVIPs | toYaml | indent 4 }}
{{ end }}
{{ if .Spec.IngressVIPs }}
  ingressVIPs:
{{ .Spec.IngressVIPs | toYaml | indent 4 }}
{{ end }}
  networking:
{{ if .Spec.ClusterNetwork }}
    clusterNetwork:
{{ .Spec.ClusterNetwork | toYaml | indent 6 }}
{{ end }}
{{ if .Spec.MachineNetwork }}
    machineNetwork:
{{ .Spec.MachineNetwork | toYaml | indent 6 }}
{{ end }}
{{ if .Spec.ServiceNetwork }}
    serviceNetwork:
{{ $serviceNetworks := list }}
{{ range .Spec.ServiceNetwork }}
{{ $serviceNetworks = append $serviceNetworks .CIDR }}
{{ end }}
{{ $serviceNetworks | toYaml | indent 6 }}
{{ end }}
  provisionRequirements:
    controlPlaneAgents: {{ .SpecialVars.ControlPlaneAgents }}
    workerAgents: {{ .SpecialVars.WorkerAgents }}
{{ if .Spec.Proxy }}
  proxy:
{{ .Spec.Proxy | toYaml | indent 4 }}
{{ end }}
  sshPublicKey: "{{ .Spec.SSHPublicKey }}"
  manifestsConfigMapRef:
    name: "{{ .Spec.ClusterName }}"`
}

func GetMockImageClusterInstallTemplate() string {
	return `apiVersion: extensions.hive.openshift.io/v1alpha1
kind: ImageClusterInstall
metadata:
  name: "{{ .Spec.ClusterName }}"
  namespace: "{{ .Spec.ClusterName }}"
  annotations:
    siteconfig.open-cluster-management.io/sync-wave: "1"
spec:
  clusterDeploymentRef:
    name: "{{ .Spec.ClusterName }}"
  imageSetRef:
    name: "{{ .Spec.ClusterImageSetNameRef }}"
  hostname: "{{ .SpecialVars.CurrentNode.HostName }}"
  sshKey: "{{ .Spec.SSHPublicKey }}"
{{ if .Spec.CaBundleRef }}
  caBundleRef:
{{ .Spec.CaBundleRef | toYaml | indent 4 }}
{{ end }}
{{ if .Spec.ExtraManifestsRefs }}
  extraManifestsRefs:
{{ .Spec.ExtraManifestsRefs | toYaml | indent 4 }}
{{ end }}
  bareMetalHostRef:
    name: "{{ .SpecialVars.CurrentNode.HostName }}"
    namespace: "{{ .Spec.ClusterName }}"
{{ if .Spec.MachineNetwork }}
  machineNetwork: "{{ (index .Spec.MachineNetwork 0).CIDR }}"
{{ end }}
{{ if .Spec.Proxy }}
  proxy:
{{ .Spec.Proxy | toYaml | indent 4 }}
{{ end }}
`
}

func GetMockNMStateConfigTemplate() string {
	return `apiVersion: agent-install.openshift.io/v1beta1
kind: NMStateConfig
metadata:
  annotations:
    siteconfig.open-cluster-management.io/sync-wave: "1"
  name: "{{ .SpecialVars.CurrentNode.HostName }}"
  namespace: "{{ .Spec.ClusterName }}"
  labels:
    nmstate-label: "{{ .Spec.ClusterName }}"
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

func GetMockNetConfig() *NetConfigData {
	prefixLengthKey := "prefix-length"
	netConf := &NetConfigData{}
	netConf.Config = map[string]interface{}{
		"interfaces": []interface{}{
			map[string]interface{}{
				"name": "eno1",
				"type": "ethernet",
				"ipv4": map[string]interface{}{"dhcp": false, "enabled": true, "address": []interface{}{
					map[string]interface{}{"ip": "203.0.113.1", prefixLengthKey: 24},
					map[string]interface{}{"ip": "203.0.113.2", prefixLengthKey: 24},
					map[string]interface{}{"ip": "203.0.113.3", prefixLengthKey: 24},
				}},
				"ipv6": map[string]interface{}{"dhcp": false, "enabled": true, "address": []interface{}{
					map[string]interface{}{"ip": "	2001:0DB8:0:0:0:0:0:1", prefixLengthKey: 32},
					map[string]interface{}{"ip": "	2001:0DB8:0:0:0:0:0:2", prefixLengthKey: 32},
					map[string]interface{}{"ip": "	2001:0DB8:0:0:0:0:0:3", prefixLengthKey: 32}},
				}},
			map[string]interface{}{
				"name":  "bond99",
				"type":  "bond",
				"state": "up",
				"ipv6": map[string]interface{}{
					"enabled": true,
					"link-aggregation": map[string]interface{}{
						"mode":    "balance-rr",
						"options": map[string]interface{}{"miimon": "140"}, "slaves": []interface{}{"eth0", "eth1"}},
					"address": []interface{}{map[string]interface{}{
						"ip":            "2001:0DB8:0:0:0:0:0:4",
						"prefix-length": 64}}}},
		},
		"dns-resolver": map[string]interface{}{"config": map[string]interface{}{
			"server": []interface{}{"203.0.113.4"}}},
		"routes": map[string]interface{}{
			"config": []interface{}{map[string]interface{}{
				"destination":        "0.0.0.0/0",
				"next-hop-address":   "203.0.113.255",
				"next-hop-interface": "eno1",
				"table-id":           254}},
		},
	}

	netConf.Interfaces = []*aiv1beta1.Interface{
		{Name: "eno1", MacAddress: "00:00:5E:00:53:00"},
		{Name: "bond99", MacAddress: "00:00:5E:00:53:01"},
	}

	return netConf
}

func GetMockBasicClusterTemplate(kind string) string {
	return fmt.Sprintf(`apiVersion: test.io/v1
kind: %s
metadata:
  name: "{{ .Spec.ClusterName }}"
  namespace: "{{ .Spec.ClusterName }}"
spec:
  name: "{{ .Spec.ClusterName }}"`, kind)
}

func GetMockBasicNodeTemplate(kind string) string {
	return fmt.Sprintf(`apiVersion: test.io/v1
kind: %s
metadata:
  name: "{{ .SpecialVars.CurrentNode.HostName }}"
  namespace: "{{ .Spec.ClusterName }}"
spec:
  name: "{{ .SpecialVars.CurrentNode.HostName }}"`, kind)
}

func SetupTestResources(ctx context.Context, c client.Client, testParams *TestParams) {
	gomega.Expect(c.Create(ctx, testParams.GeneratePullSecret())).To(gomega.Succeed())
	gomega.Expect(c.Create(ctx, testParams.GenerateBMCSecret())).To(gomega.Succeed())
	gomega.Expect(c.Create(ctx, testParams.GenerateClusterImageSet())).To(gomega.Succeed())
	gomega.Expect(c.Create(ctx, testParams.GenerateClusterTemplate())).To(gomega.Succeed())
	gomega.Expect(c.Create(ctx, testParams.GenerateNodeTemplate())).To(gomega.Succeed())
	gomega.Expect(c.Create(ctx, testParams.GenerateExtraManifest())).To(gomega.Succeed())
}

func TeardownTestResources(ctx context.Context, c client.Client, testParams *TestParams) {

	for k, v := range testParams.GetResources() {
		if k != "" {
			key := types.NamespacedName{
				Name:      k,
				Namespace: testParams.ClusterNamespace,
			}

			var obj client.Object
			switch v {
			case "ConfigMap":
				obj = &corev1.ConfigMap{}
			case secretResource:
				obj = &corev1.Secret{}
			case "ClusterImageSet":
				obj = &hivev1.ClusterImageSet{}
			}

			if err := c.Get(ctx, key, obj); err == nil {
				gomega.Expect(c.Delete(ctx, obj)).To(gomega.Succeed())
			}
		}
	}
}
