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

package imagebasedinstaller

// Default image-based installer install template names
const (
	ClusterLevelInstallTemplates = "ibi-cluster-templates-v1"
	NodeLevelInstallTemplates    = "ibi-node-templates-v1"
)

const ImageClusterInstall = `apiVersion: extensions.hive.openshift.io/v1alpha1
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
    kind: ImageClusterInstall
    name: "{{ .Spec.ClusterName }}"
    version: v1alpha1
  clusterName: "{{ .Spec.ClusterName }}"
  platform:
    none: {}
  pullSecretRef:
    name: "{{ .Spec.PullSecretRef.Name }}"`

// nolint:gosec
// This is for dynamic templating purposes, not hardcoded credentials (gosec G101).
const NetworkSecret = `{{ if .SpecialVars.CurrentNode.NodeNetwork }}
apiVersion: v1
kind: Secret
metadata:
  annotations:
    siteconfig.open-cluster-management.io/sync-wave: "1"
  name: "{{ .SpecialVars.CurrentNode.HostName }}"
  namespace: "{{ .Spec.ClusterName }}"
type: Opaque
data:
  nmstate: |
{{ .SpecialVars.CurrentNode.NodeNetwork.NetConfig | toYaml | b64enc | indent 4}}
{{ end }}`

const KlusterletAddonConfig = `apiVersion: agent.open-cluster-management.io/v1
kind: KlusterletAddonConfig
metadata:
  annotations:
    siteconfig.open-cluster-management.io/sync-wave: "2"
  labels:
    installer.name: multiclusterhub
    installer.namespace: open-cluster-management
  name: "{{ .Spec.ClusterName }}"
  namespace: "{{ .Spec.ClusterName }}"
spec:
  clusterName: "{{ .Spec.ClusterName }}"
  clusterNamespace: "{{ .Spec.ClusterName }}"
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
{{ if .SpecialVars.CurrentNode.NodeRef }}
  name: "{{ .SpecialVars.CurrentNode.NodeRef.Name }}"
  namespace: "{{ .SpecialVars.CurrentNode.NodeRef.Namespace }}"
{{ else }}
  name: "{{ .SpecialVars.CurrentNode.HostName }}"
  namespace: "{{ .Spec.ClusterName }}" 
{{ end }}      
  annotations:
    siteconfig.open-cluster-management.io/sync-wave: "1"
    inspect.metal3.io: "{{ .SpecialVars.CurrentNode.IronicInspect }}"
spec:
  bootMode: "{{ .SpecialVars.CurrentNode.BootMode }}"
  bmc:
    address: "{{ .SpecialVars.CurrentNode.BmcAddress }}"
    disableCertificateVerification: true
    credentialsName: "{{ .SpecialVars.CurrentNode.BmcCredentialsName.Name }}"
  bootMACAddress: "{{ .SpecialVars.CurrentNode.BootMACAddress }}"
  automatedCleaningMode: "{{ .SpecialVars.CurrentNode.AutomatedCleaningMode }}"
  online: false
  externallyProvisioned: true
{{ if .SpecialVars.CurrentNode.RootDeviceHints }}
  rootDeviceHints:
{{ .SpecialVars.CurrentNode.RootDeviceHints | toYaml | indent 4 }}
{{ end }}
{{ if .SpecialVars.CurrentNode.NodeNetwork }}
  preprovisioningNetworkDataName: {{ .SpecialVars.CurrentNode.HostName }}
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
	data["NetworkSecret"] = NetworkSecret
	return data
}
