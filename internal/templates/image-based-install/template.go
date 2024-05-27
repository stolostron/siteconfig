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

package imagebasedinstall

const ImageClusterInstall = `apiVersion: extensions.hive.openshift.io/v1alpha1
kind: ImageClusterInstall
metadata:
  name: "{{ .Site.ClusterName }}"
  namespace: "{{ .Site.ClusterName }}"
  annotations:
    metaclusterinstall.openshift.io/sync-wave: "1"
spec:
  clusterDeploymentRef:
    name: "{{ .Site.ClusterName }}"
  imageSetRef:
    name: "{{ .Site.ClusterImageSetNameRef }}"
  hostname: "{{ .SpecialVars.CurrentNode.HostName }}"
  sshKey: "{{ .Site.SSHPublicKey }}"
{{ if .Site.CaBundleRef.Name }}
  caBundleRef:
{{ .Site.CaBundleRef | toYaml | indent 4 }}
{{ end }}
  networkConfigRef:
    name: "{{ .SpecialVars.CurrentNode.HostName }}"
{{ if gt (len .Site.ExtraManifestsRefs) 0 }}
  extraManifestsRef:
{{ .Site.ExtraManifestsRefs | toYaml | indent 4 }}
{{ end }}
  bareMetalHostRef:
    name: "{{ .SpecialVars.CurrentNode.HostName }}"
    namespace: "{{ .Site.ClusterName }}"
{{ if .Site.MachineNetwork }}
machineNetwork:
{{ .Site.MachineNetwork | toYaml | indent 4 }}
{{ end }}
{{ if (anyFieldDefined .Site.ProxySettings) }}
  proxy:
{{ .Site.ProxySettings | toYaml | indent 4 }}
{{ end }}
`

const ClusterDeployment = `apiVersion: hive.openshift.io/v1
kind: ClusterDeployment
metadata:
  name: "{{ .Site.ClusterName }}"
  namespace: "{{ .Site.ClusterName }}"
  annotations:
    metaclusterinstall.openshift.io/sync-wave: "1"
spec:
  baseDomain: "{{ .Site.BaseDomain }}"
  clusterInstallRef:
    group: extensions.hive.openshift.io
    kind: ImageClusterInstall
    name: "{{ .Site.ClusterName }}"
    version: v1alpha1
  clusterName: "{{ .Site.ClusterName }}"
  platform:
    agentBareMetal:
      agentSelector:
        matchLabels:
          cluster-name: "{{ .Site.ClusterName }}"
  pullSecretRef:
    name: "{{ .Site.PullSecretRef.Name }}"`

const NetworkConfigMap = `apiVersion: v1
kind: ConfigMap
metadata:
  annotations:
    metaclusterinstall.openshift.io/sync-wave: "1"
  name: "{{ .SpecialVars.CurrentNode.HostName }}"
  namespace: "{{ .Site.ClusterName }}"
data:
  network-config: |
{{ .SpecialVars.CurrentNode.NodeNetwork.Config | toYaml | indent 4}}
`

const KlusterletAddonConfig = `apiVersion: agent.open-cluster-management.io/v1
kind: KlusterletAddonConfig
metadata:
  annotations:
    metaclusterinstall.openshift.io/sync-wave: "2"
  labels:
    installer.name: multiclusterhub
    installer.namespace: open-cluster-management
  name: "{{ .Site.ClusterName }}"
  namespace: "{{ .Site.ClusterName }}"
spec:
  clusterName: "{{ .Site.ClusterName }}"
  clusterNamespace: "{{ .Site.ClusterName }}"
  clusterLabels:
    cloud: auto-detect
    vendor: auto-detect
  applicationManager:
    enabled: true
  certPolicyController:
    enabled: true
  iamPolicyController:
    enabled: true
  policyController:
    enabled: true
  searchCollector:
    enabled: false`

const ManagedCluster = `apiVersion: cluster.open-cluster-management.io/v1
kind: ManagedCluster
metadata:
  name: "{{ .Site.ClusterName }}"
  labels:
{{ .Site.ClusterLabels | toYaml | indent 4 }}
  annotations:
    metaclusterinstall.openshift.io/sync-wave: "2"
spec:
  hubAcceptsClient: true`

const BareMetalHost = `apiVersion: metal3.io/v1alpha1
kind: BareMetalHost
metadata:
  name: "{{ .SpecialVars.CurrentNode.HostName }}"
  namespace: "{{ .Site.ClusterName }}"
  annotations:
    metaclusterinstall.openshift.io/sync-wave: "1"
    inspect.metal3.io: "{{ .SpecialVars.CurrentNode.IronicInspect }}"
{{ if .SpecialVars.CurrentNode.NodeLabels }}
    bmac.agent-install.openshift.io.node-label:
{{ .SpecialVars.CurrentNode.NodeLabels | toYaml | indent 6 }}
{{ end }}
    bmac.agent-install.openshift.io/hostname: "{{ .SpecialVars.CurrentNode.HostName }}"
{{ if .SpecialVars.CurrentNode.InstallerArgs  }}
    bmac.agent-install.openshift.io/installer-args: {{ .SpecialVars.CurrentNode.InstallerArgs  }}
{{ end }}
{{ if .SpecialVars.CurrentNode.IgnitionConfigOverride }}
    bmac.agent-install.openshift.io/ignition-config-overrides: {{ .SpecialVars.CurrentNode.IgnitionConfigOverride }}
{{ end }}
    bmac.agent-install.openshift.io/role: "{{ .SpecialVars.CurrentNode.Role }}"
  labels:
    infraenvs.agent-install.openshift.io: "{{ .Site.ClusterName }}"
spec:
  bootMode: "{{ .SpecialVars.CurrentNode.BootMode }}"
  bmc:
    address: "{{ .SpecialVars.CurrentNode.BmcAddress }}"
    disableCertificateVerification: true
    credentialsName: "{{ .SpecialVars.CurrentNode.BmcCredentialsName.Name }}"
  bootMACAddress: "{{ .SpecialVars.CurrentNode.BootMACAddress }}"
  automatedCleaningMode: "{{ .SpecialVars.CurrentNode.AutomatedCleaningMode }}"
  online: true
{{ if (anyFieldDefined .SpecialVars.CurrentNode.RootDeviceHints) }}
  rootDeviceHints:
{{ .SpecialVars.CurrentNode.RootDeviceHints | toYaml | indent 4 }}
{{ end }}`

func GetClusterTemplates() map[string]string {
	data := make(map[string]string)
	data["ClusterDeployment"] = ClusterDeployment
	data["ManagedCluster"] = ManagedCluster
	data["KlusterletAddonConfig"] = KlusterletAddonConfig
	return data
}

func GetNodeTemplates() map[string]string {
	data := make(map[string]string)
	data["ImageClusterInstall"] = ImageClusterInstall
	data["BareMetalHost"] = BareMetalHost
	data["NetworkConfigMap"] = NetworkConfigMap
	return data
}
