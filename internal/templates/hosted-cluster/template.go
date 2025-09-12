/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
*/

package hostedcluster

// Default hosted cluster install template names
const (
	ClusterLevelInstallTemplates = "hcp-cluster-templates-v1"
	NodeLevelInstallTemplates    = "hcp-node-templates-v1"
)

/*
Cluster-level installation templates
*/
const HostedCluster = `apiVersion: hypershift.openshift.io/v1beta1
kind: HostedCluster
metadata:
  name: "{{ .Spec.ClusterName }}"
  namespace: "{{ .Spec.ClusterName }}"
  annotations:
    siteconfig.open-cluster-management.io/sync-wave: "1"
spec:
  {{ if .Spec.CaBundleRef }}
  additionalTrustBundle:
  {{ .Spec.CaBundleRef | toYaml | indent 4 }}
  {{ end }}
  olmCatalogPlacement: management
  configuration:
    operatorhub:
      disableAllDefaultSources: true
  controllerAvailabilityPolicy: HighlyAvailable
  infrastructureAvailabilityPolicy: HighlyAvailable
  dns:
    baseDomain: "{{ .Spec.BaseDomain }}"
  etcd:
    managed:
      storage:
        type: PersistentVolume
    managementType: Managed
  fips: false
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
{{ .Spec.ServiceNetwork | toYaml | indent 6 }}
{{ end }}
    networkType: "{{ .Spec.NetworkType }}"
  platform:
    type: Agent
    agent:
      agentNamespace: "{{ .Spec.ClusterName }}"
  pullSecret:
{{ .Spec.PullSecretRef | toYaml | indent 4 }}
  release:
    image: "{{ .SpecialVars.ReleaseImage }}"
  services:
  - service: APIServer
    servicePublishingStrategy:
      type: LoadBalancer
  - service: OAuthServer
    servicePublishingStrategy:
      type: Route
  - service: OIDC
    servicePublishingStrategy:
      type: Route
  - service: Konnectivity
    servicePublishingStrategy:
      type: Route
  - service: Ignition
    servicePublishingStrategy:
      type: Route
  sshKey:
    name: sshkey-{{ .Spec.ClusterName }}
  infraID: "{{ .Spec.ClusterName }}"`

const KlusterletAddonConfig = `apiVersion: agent.open-cluster-management.io/v1
kind: KlusterletAddonConfig
metadata:
  annotations:
    siteconfig.open-cluster-management.io/sync-wave: "2"
  name: "{{ .Spec.ClusterName }}"
  namespace: "{{ .Spec.ClusterName }}"
spec:
  applicationManager:
    enabled: false
  certPolicyController:
    enabled: false
  policyController:
    enabled: true
  searchCollector:
    enabled: false`

const ManagedCluster = `apiVersion: cluster.open-cluster-management.io/v1
kind: ManagedCluster
metadata:
  name: "{{ .Spec.ClusterName }}"
  annotations:
    siteconfig.open-cluster-management.io/sync-wave: "2"
    import.open-cluster-management.io/hosting-cluster-name: local-cluster
    import.open-cluster-management.io/klusterlet-deploy-mode: Hosted
    open-cluster-management/created-via: hypershift
  labels:
    cloud: BareMetal
    vendor: OpenShift
    ishosted: "true"
spec:
  hubAcceptsClient: true`

// nolint:gosec
const SshPubKeySecret = `apiVersion: v1
kind: Secret
metadata:
  name: sshkey-{{ .Spec.ClusterName }}
  namespace: "{{ .Spec.ClusterName }}"
  labels:
    hypershift.openshift.io/safe-to-delete-with-cluster: "true"
  annotations:
    siteconfig.open-cluster-management.io/sync-wave: "1"
stringData:
  id_rsa.pub: "{{ .Spec.SSHPublicKey }}"
`

/*
  Node-level installation templates
*/

const InfraEnv = `apiVersion: agent-install.openshift.io/v1beta1
kind: InfraEnv
metadata:
  annotations:
    siteconfig.open-cluster-management.io/sync-wave: "1"
{{ if .SpecialVars.CurrentNode.HostRef }}
  name: "{{ .SpecialVars.CurrentNode.HostRef.Name }}"
  namespace: "{{ .SpecialVars.CurrentNode.HostRef.Namespace }}"
{{ else }}
  name: "{{ .SpecialVars.CurrentNode.HostName }}"
  namespace: "{{ .Spec.ClusterName }}"
{{ end }}
spec:
  sshAuthorizedKey: "{{ .Spec.SSHPublicKey }}"
{{ if .Spec.Proxy }}
  proxy:
{{ .Spec.Proxy | toYaml | indent 4 }}
{{ end }}
{{ if .SpecialVars.CurrentNode.CPUArchitecture }}
  cpuArchitecture: "{{ .SpecialVars.CurrentNode.CPUArchitecture }}"
{{ else if and
    (.Spec.CPUArchitecture)
    (ne .Spec.CPUArchitecture "multi")
}}
  cpuArchitecture: "{{ .Spec.CPUArchitecture }}"
{{ end }}
  pullSecretRef:
    name: "{{ .Spec.PullSecretRef.Name }}"
  ignitionConfigOverride: '{{ .Spec.IgnitionConfigOverride }}'
  nmStateConfigLabelSelector:
    matchLabels:
{{ if .SpecialVars.CurrentNode.HostRef }}
      nmstate-label: "{{ .SpecialVars.CurrentNode.HostRef.Name }}"
{{ else }}
      nmstate-label: "{{ .SpecialVars.CurrentNode.HostName }}"
{{ end }}
  additionalNTPSources:
{{ .Spec.AdditionalNTPSources | toYaml | indent 4 }}`

const NodePool = `apiVersion: hypershift.openshift.io/v1beta1
kind: NodePool
metadata:
  annotations:
    siteconfig.open-cluster-management.io/sync-wave: "2"
{{ if .SpecialVars.CurrentNode.HostRef }}
  name: "{{ .SpecialVars.CurrentNode.HostRef.Name }}"
  namespace: "{{ .SpecialVars.CurrentNode.HostRef.Namespace }}"
{{ else }}
  name: "{{ .SpecialVars.CurrentNode.HostName }}"
  namespace: "{{ .Spec.ClusterName }}"
{{ end }}
{{ if .Spec.ExtraLabels.NodePool }}
  labels:
{{ .Spec.ExtraLabels.NodePool | toYaml | indent 4 }}
{{ end }}
spec:
  clusterName: "{{ .Spec.ClusterName }}"
  replicas: 1
  management:
    autoRepair: true
    upgradeType: InPlace
    inPlace:
      maxUnavailable: 100%
  platform:
    type: Agent
    agent:
      agentLabelSelector:
        matchLabels:
{{ if .SpecialVars.CurrentNode.HostRef }}
          agent-install.openshift.io/bmh: "{{ .SpecialVars.CurrentNode.HostRef.Name }}"
{{ else }}
          agent-install.openshift.io/bmh: "{{ .SpecialVars.CurrentNode.HostName }}"
{{ end }}
  release:
    image: "{{ .SpecialVars.ReleaseImage }}"
{{ if .SpecialVars.CurrentNode.HostRef }}
  tuningConfig:
    - name: "performanceprofile-{{ .SpecialVars.CurrentNode.HostRef.Name }}"
  config:
    - name: "machineconfigs-{{ .SpecialVars.CurrentNode.HostRef.Name }}"
{{ else }}
  tuningConfig:
    - name: "performanceprofile-{{ .SpecialVars.CurrentNode.HostName }}"
  config:
    - name: "machineconfigs-{{ .SpecialVars.CurrentNode.HostName }}"
{{ end }}
{{ if .SpecialVars.CurrentNode.NodeLabels }}
  nodeLabels:
{{ .SpecialVars.CurrentNode.NodeLabels | toYaml | indent 4 }}
{{ end }}`

const NMStateConfig = `{{ if .SpecialVars.CurrentNode.NodeNetwork }}
apiVersion: agent-install.openshift.io/v1beta1
kind: NMStateConfig
metadata:
  annotations:
    siteconfig.open-cluster-management.io/sync-wave: "1"
{{ if .SpecialVars.CurrentNode.HostRef }}
  name: "{{ .SpecialVars.CurrentNode.HostRef.Name }}"
  namespace: "{{ .SpecialVars.CurrentNode.HostRef.Namespace }}"
{{ else }}
  name: "{{ .SpecialVars.CurrentNode.HostName }}"
  namespace: "{{ .Spec.ClusterName }}"
{{ end }}
  labels:
{{ if .SpecialVars.CurrentNode.HostRef }}
    nmstate-label: "{{ .SpecialVars.CurrentNode.HostRef.Name }}"
{{ else }}
    nmstate-label: "{{ .SpecialVars.CurrentNode.HostName }}"
{{ end }}
spec:
  config:
{{ .SpecialVars.CurrentNode.NodeNetwork.NetConfig | toYaml | indent 4}}
  interfaces:
{{ .SpecialVars.CurrentNode.NodeNetwork.Interfaces | toYaml | indent 4 }}
{{ end }}`

const BareMetalHost = `apiVersion: metal3.io/v1alpha1
kind: BareMetalHost
metadata:
{{ if .SpecialVars.CurrentNode.HostRef }}
  name: "{{ .SpecialVars.CurrentNode.HostRef.Name }}"
  namespace: "{{ .SpecialVars.CurrentNode.HostRef.Namespace }}"
{{ else }}
  name: "{{ .SpecialVars.CurrentNode.HostName }}"
  namespace: "{{ .Spec.ClusterName }}" 
{{ end }}
  annotations:
    siteconfig.open-cluster-management.io/sync-wave: "3"
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
{{ if .SpecialVars.CurrentNode.HostRef }}
    infraenvs.agent-install.openshift.io: "{{ .SpecialVars.CurrentNode.HostRef.Name }}"
{{ else }}
    infraenvs.agent-install.openshift.io: "{{ .SpecialVars.CurrentNode.HostName }}"
{{ end }}
spec:
  bootMode: "{{ .SpecialVars.CurrentNode.BootMode }}"
  bmc:
    address: "{{ .SpecialVars.CurrentNode.BmcAddress }}"
    disableCertificateVerification: true
    credentialsName: "{{ .SpecialVars.CurrentNode.BmcCredentialsName.Name }}"
  bootMACAddress: "{{ .SpecialVars.CurrentNode.BootMACAddress }}"
  automatedCleaningMode: "{{ .SpecialVars.CurrentNode.AutomatedCleaningMode }}"
  online: true
{{ if .SpecialVars.CurrentNode.CPUArchitecture }}
  architecture: "{{ .SpecialVars.CurrentNode.CPUArchitecture }}"
{{ else if and
    (.Spec.CPUArchitecture)
    (ne .Spec.CPUArchitecture "multi")
}}
  architecture: "{{ .Spec.CPUArchitecture }}"
{{ end }}
{{ if .SpecialVars.CurrentNode.RootDeviceHints }}
  rootDeviceHints:
{{ .SpecialVars.CurrentNode.RootDeviceHints | toYaml | indent 4 }}
{{ end }}`

func GetClusterTemplates() map[string]string {
	data := make(map[string]string)
	data["HostedCluster"] = HostedCluster
	data["SshPubKeySecret"] = SshPubKeySecret
	data["ManagedCluster"] = ManagedCluster
	data["KlusterletAddonConfig"] = KlusterletAddonConfig
	return data
}

func GetNodeTemplates() map[string]string {
	data := make(map[string]string)
	data["InfraEnv"] = InfraEnv
	data["NodePool"] = NodePool
	data["BareMetalHost"] = BareMetalHost
	data["NMStateConfig"] = NMStateConfig
	return data
}
