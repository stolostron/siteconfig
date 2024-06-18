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

package assistedinstaller

const AgentClusterInstall = `apiVersion: extensions.hive.openshift.io/v1beta1
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
{{ $serviceNetworks := list }}
{{ range .Site.ServiceNetwork }}
{{ $serviceNetworks = append $serviceNetworks .CIDR }}
{{ end }}
{{ $serviceNetworks | toYaml | indent 6 }}
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
    kind: AgentClusterInstall
    name: "{{ .Site.ClusterName }}"
    version: v1beta1
  clusterName: "{{ .Site.ClusterName }}"
  platform:
    agentBareMetal:
      agentSelector:
        matchLabels:
          cluster-name: "{{ .Site.ClusterName }}"
  pullSecretRef:
    name: "{{ .Site.PullSecretRef.Name }}"`

const InfraEnv = `apiVersion: agent-install.openshift.io/v1beta1
kind: InfraEnv
metadata:
  annotations:
    metaclusterinstall.openshift.io/sync-wave: "1"
  name: "{{ .Site.ClusterName }}"
  namespace: "{{ .Site.ClusterName }}"
spec:
  clusterRef:
    name: "{{ .Site.ClusterName }}"
    namespace: "{{ .Site.ClusterName }}"
  sshAuthorizedKey: "{{ .Site.SSHPublicKey }}"
{{ if (anyFieldDefined .Site.ProxySettings) }}
  proxy:
{{ .Site.ProxySettings | toYaml | indent 4 }}
{{ end }}
  pullSecretRef:
    name: "{{ .Site.PullSecretRef.Name }}"
  ignitionConfigOverride: {{ .Site.IgnitionConfigOverride }}
  nmStateConfigLabelSelector:
    matchLabels:
      nmstate-label: "{{ .Site.ClusterName }}"
  additionalNTPSources:
{{ .Site.AdditionalNTPSources | toYaml | indent 4 }}`

const KlusterletAddonConfig = `apiVersion: agent.open-cluster-management.io/v1
kind: KlusterletAddonConfig
metadata:
  annotations:
    metaclusterinstall.openshift.io/sync-wave: "2"
  name: "{{ .Site.ClusterName }}"
  namespace: "{{ .Site.ClusterName }}"
spec:
  clusterName: "{{ .Site.ClusterName }}"
  clusterNamespace: "{{ .Site.ClusterName }}"
  clusterLabels:
    cloud: auto-detect
    vendor: auto-detect
  applicationManager:
    enabled: false
  certPolicyController:
    enabled: false
  iamPolicyController:
    enabled: false
  policyController:
    enabled: true
  searchCollector:
    enabled: false`

const NMStateConfig = `apiVersion: agent-install.openshift.io/v1beta1
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
{{ .SpecialVars.CurrentNode.NodeNetwork.NetConfig | toYaml | indent 4}}
  interfaces:
{{ .SpecialVars.CurrentNode.NodeNetwork.Interfaces | toYaml | indent 4 }}`

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
	data["AgentClusterInstall"] = AgentClusterInstall
	data["ClusterDeployment"] = ClusterDeployment
	data["InfraEnv"] = InfraEnv
	data["ManagedCluster"] = ManagedCluster
	data["KlusterletAddonConfig"] = KlusterletAddonConfig
	return data
}

func GetNodeTemplates() map[string]string {
	data := make(map[string]string)
	data["BareMetalHost"] = BareMetalHost
	data["NMStateConfig"] = NMStateConfig
	return data
}
