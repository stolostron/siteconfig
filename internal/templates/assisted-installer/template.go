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

// Default assisted installer install template names
const (
	ClusterLevelInstallTemplates = "ai-cluster-templates-v1"
	NodeLevelInstallTemplates    = "ai-node-templates-v1"
)

const AgentClusterInstall = `apiVersion: extensions.hive.openshift.io/v1beta1
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
{{ if .Spec.PlatformType }}
  platformType: "{{ .Spec.PlatformType }}"
{{ end }}
  sshPublicKey: "{{ .Spec.SSHPublicKey }}"
{{ if gt (len .Spec.ExtraManifestsRefs) 0 }}
  manifestsConfigMapRefs:
{{ .Spec.ExtraManifestsRefs | toYaml | indent 4 }}
{{ end }}`

const ClusterDeployment = `apiVersion: hive.openshift.io/v1
kind: ClusterDeployment
metadata:
  name: "{{ .Spec.ClusterName }}"
  namespace: "{{ .Spec.ClusterName }}"
  annotations:
    siteconfig.open-cluster-management.io/sync-wave: "1"
spec:
  baseDomain: "{{ .Spec.BaseDomain }}"
  clusterInstallRef:
    group: extensions.hive.openshift.io
    kind: AgentClusterInstall
    name: "{{ .Spec.ClusterName }}"
    version: v1beta1
  clusterName: "{{ .Spec.ClusterName }}"
  platform:
    agentBareMetal:
      agentSelector:
        matchLabels:
          cluster-name: "{{ .Spec.ClusterName }}"
  pullSecretRef:
    name: "{{ .Spec.PullSecretRef.Name }}"`

const InfraEnv = `apiVersion: agent-install.openshift.io/v1beta1
kind: InfraEnv
metadata:
  annotations:
    siteconfig.open-cluster-management.io/sync-wave: "1"
  name: "{{ .Spec.ClusterName }}"
  namespace: "{{ .Spec.ClusterName }}"
spec:
  clusterRef:
    name: "{{ .Spec.ClusterName }}"
    namespace: "{{ .Spec.ClusterName }}"
  sshAuthorizedKey: "{{ .Spec.SSHPublicKey }}"
{{ if .Spec.Proxy }}
  proxy:
{{ .Spec.Proxy | toYaml | indent 4 }}
{{ end }}
  pullSecretRef:
    name: "{{ .Spec.PullSecretRef.Name }}"
  ignitionConfigOverride: '{{ .Spec.IgnitionConfigOverride }}'
  nmStateConfigLabelSelector:
    matchLabels:
      nmstate-label: "{{ .Spec.ClusterName }}"
  additionalNTPSources:
{{ .Spec.AdditionalNTPSources | toYaml | indent 4 }}`

const KlusterletAddonConfig = `apiVersion: agent.open-cluster-management.io/v1
kind: KlusterletAddonConfig
metadata:
  annotations:
    siteconfig.open-cluster-management.io/sync-wave: "2"
  name: "{{ .Spec.ClusterName }}"
  namespace: "{{ .Spec.ClusterName }}"
spec:
  clusterName: "{{ .Spec.ClusterName }}"
  clusterNamespace: "{{ .Spec.ClusterName }}"
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

const NMStateConfig = `{{ if .SpecialVars.CurrentNode.NodeNetwork }}
apiVersion: agent-install.openshift.io/v1beta1
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
{{ .SpecialVars.CurrentNode.NodeNetwork.NetConfig | toYaml | indent 4}}
  interfaces:
{{ .SpecialVars.CurrentNode.NodeNetwork.Interfaces | toYaml | indent 4 }}
{{ end }}`

const ManagedCluster = `apiVersion: cluster.open-cluster-management.io/v1
kind: ManagedCluster
metadata:
  name: "{{ .Spec.ClusterName }}"
  annotations:
    siteconfig.open-cluster-management.io/sync-wave: "2"
  labels:
    cloud: auto-detect
    vendor: auto-detect
spec:
  hubAcceptsClient: true`

const BareMetalHost = `apiVersion: metal3.io/v1alpha1
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
    bmac.agent-install.openshift.io/hostname: "{{ .SpecialVars.CurrentNode.HostName }}"
{{ if .SpecialVars.CurrentNode.InstallerArgs  }}
    bmac.agent-install.openshift.io/installer-args: '{{ .SpecialVars.CurrentNode.InstallerArgs  }}'
{{ end }}
{{ if .SpecialVars.CurrentNode.IgnitionConfigOverride }}
    bmac.agent-install.openshift.io/ignition-config-overrides: '{{ .SpecialVars.CurrentNode.IgnitionConfigOverride }}'
{{ end }}
    bmac.agent-install.openshift.io/role: "{{ .SpecialVars.CurrentNode.Role }}"
  labels:
    infraenvs.agent-install.openshift.io: "{{ .Spec.ClusterName }}"
spec:
  bootMode: "{{ .SpecialVars.CurrentNode.BootMode }}"
  bmc:
    address: "{{ .SpecialVars.CurrentNode.BmcAddress }}"
    disableCertificateVerification: true
    credentialsName: "{{ .SpecialVars.CurrentNode.BmcCredentialsName.Name }}"
  bootMACAddress: "{{ .SpecialVars.CurrentNode.BootMACAddress }}"
  automatedCleaningMode: "{{ .SpecialVars.CurrentNode.AutomatedCleaningMode }}"
  online: true
{{ if .SpecialVars.CurrentNode.RootDeviceHints }}
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
