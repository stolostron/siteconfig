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
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/stolostron/siteconfig/api/v1alpha1"

	. "github.com/onsi/gomega"
)

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
	testPullSecretVal := `{"auths":{"cloud.openshift.com":{"auth":"dXNlcjpwYXNzd29yZAo=","email":"r@r.com"}}}`
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

func GetMockSNOClusterInstance(clusterName, clusterNamespace, pullSecret, bmcCredentialsName, clusterImageSetName, extraManifest, clusterTemplateRef, nodeTemplateRef string) *v1alpha1.ClusterInstance {
	installConfigOverrides := "{\"networking\":{\"networkType\":\"OVNKubernetes\"},\"cpuPartitioningMode\":\"AllNodes\"}"
	installerArgs := "[\"--append-karg\", \"nameserver=8.8.8.8\", \"-n\"]"
	nodeIgnitionConfigOverride := "{\"ignition\": {\"version\": \"3.1.0\"}, \"storage\": {\"files\": [{\"path\": \"/etc/containers/registries.conf\", \"overwrite\": true, \"contents\": {\"source\": \"data:text/plain;base64,aGVsbG8gZnJvbSB6dHAgcG9saWN5IGdlbmVyYXRvcg==\"}}]}}"

	return &v1alpha1.ClusterInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: clusterNamespace,
		},
		Spec: v1alpha1.ClusterInstanceSpec{
			ClusterName:            clusterName,
			PullSecretRef:          corev1.LocalObjectReference{Name: pullSecret},
			ClusterImageSetNameRef: clusterImageSetName,
			SSHPublicKey:           "test-ssh",
			BaseDomain:             "abcd",
			ClusterType:            v1alpha1.ClusterTypeSNO,
			ExtraManifestsRefs:     []corev1.LocalObjectReference{{Name: extraManifest}},
			TemplateRefs: []v1alpha1.TemplateRef{
				{Name: clusterTemplateRef, Namespace: clusterNamespace}},
			InstallConfigOverrides: installConfigOverrides,
			Nodes: []v1alpha1.NodeSpec{{
				Role:                   "master",
				BmcAddress:             "1:2:3:4",
				BmcCredentialsName:     v1alpha1.BmcCredentialsName{Name: bmcCredentialsName},
				InstallerArgs:          installerArgs,
				IgnitionConfigOverride: nodeIgnitionConfigOverride,
				TemplateRefs: []v1alpha1.TemplateRef{
					{Name: nodeTemplateRef, Namespace: clusterNamespace}}}}},
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

func GetMockBasicClusterTemplate(kind string) string {
	return fmt.Sprintf(`apiVersion: test.io/v1
kind: %s
spec:
  name: "{{ .Spec.ClusterName }}"`, kind)
}

func GetMockBasicNodeTemplate(kind string) string {
	return fmt.Sprintf(`apiVersion: test.io/v1
kind: %s
spec:
  name: "{{ .SpecialVars.CurrentNode.HostName }}"`, kind)
}

func SetupTestPrereqs(ctx context.Context, c client.Client, bmc, pullSecret *corev1.Secret,
	clusterImageSet *hivev1.ClusterImageSet, clusterTemplate, nodeTemplate, extraManifest *corev1.ConfigMap) {
	Expect(c.Create(ctx, bmc)).To(Succeed())
	Expect(c.Create(ctx, clusterImageSet)).To(Succeed())
	Expect(c.Create(ctx, pullSecret)).To(Succeed())
	Expect(c.Create(ctx, clusterTemplate)).To(Succeed())
	Expect(c.Create(ctx, nodeTemplate)).To(Succeed())
	Expect(c.Create(ctx, extraManifest)).To(Succeed())
}
