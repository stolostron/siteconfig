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
limitations under the License.
*/

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	aiv1beta1 "github.com/openshift/assisted-service/api/v1beta1"
)

var _ = Describe("ValidateClusterInstance", func() {
	var clusterInstance *ClusterInstance

	BeforeEach(func() {
		clusterInstance = &ClusterInstance{
			Spec: ClusterInstanceSpec{
				TemplateRefs: []TemplateRef{
					{
						Name:      "cluster-template",
						Namespace: "default",
					},
				},
				Nodes: []NodeSpec{
					{
						HostName: "node1",
						TemplateRefs: []TemplateRef{
							{
								Name:      "node-template",
								Namespace: "default",
							},
						},
						Role: "master"},
				},
				ClusterType: ClusterTypeSNO,
			},
		}
	})

	It("should return an error if cluster-level template reference is missing", func() {
		clusterInstance.Spec.TemplateRefs = nil
		err := ValidateClusterInstance(clusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("missing cluster-level template reference"))
	})

	It("should return an error if any node is missing a template reference", func() {
		clusterInstance.Spec.Nodes[0].TemplateRefs = nil
		err := ValidateClusterInstance(clusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("missing node-level template reference"))
	})

	It("should return an error if control-plane agent validation fails", func() {
		clusterInstance.Spec.Nodes[0].Role = "worker" // No control-plane nodes
		err := ValidateClusterInstance(clusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("at least 1 control-plane agent is required"))
	})

	It("should return an error if a HCP cluster has any control-plane nodes", func() {
		clusterInstance.Spec.Nodes[0].Role = "master" // A control-plane node
		clusterInstance.Spec.ClusterType = ClusterTypeHostedControlPlane
		err := ValidateClusterInstance(clusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("hosted control plane clusters must not have control-plane agents"))
	})

	It("should return an error if SNO cluster has more than one control-plane node", func() {
		clusterInstance.Spec.Nodes = append(clusterInstance.Spec.Nodes, NodeSpec{
			HostName: "node2",
			Role:     "master",
			TemplateRefs: []TemplateRef{
				{
					Name:      "node-template",
					Namespace: "default",
				},
			}})
		err := ValidateClusterInstance(clusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("single node OpenShift cluster-type must have exactly 1 control-plane agent"))
	})
})

var _ = Describe("validatePostProvisioningChanges", func() {

	var (
		oldClusterInstance, newClusterInstance *ClusterInstance
		testLogger                             logr.Logger
	)

	BeforeEach(func() {
		testLogger = logf.Log.WithName("test")

		oldClusterInstance = &ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:        "site-sno-du-1",
				Namespace:   "site-sno-du-1",
				Annotations: make(map[string]string),
			},
			Spec: ClusterInstanceSpec{
				ClusterName:            "site-sno-du-1",
				PullSecretRef:          corev1.LocalObjectReference{Name: "pullSecretName"},
				ClusterImageSetNameRef: "openshift-test",
				SSHPublicKey:           "ssh-rsa",
				BaseDomain:             "example.com",
				ApiVIPs:                []string{"192.0.2.1", "192.0.2.2"},
				HoldInstallation:       false,
				AdditionalNTPSources:   []string{"NTP.server1", "192.0.2.3"},
				MachineNetwork:         []MachineNetworkEntry{{CIDR: "203.0.113.0/24"}},
				ClusterNetwork:         []ClusterNetworkEntry{{CIDR: "203.0.113.0/24", HostPrefix: 23}},
				ServiceNetwork:         []ServiceNetworkEntry{{CIDR: "203.0.113.0/24"}},
				NetworkType:            "OVNKubernetes",
				ExtraLabels:            map[string]map[string]string{"ManagedCluster": {"group-du-sno": "test", "common": "true", "sites": "site-sno-du-1"}},
				InstallConfigOverrides: "{\"capabilities\":{\"baselineCapabilitySet\": \"None\", \"additionalEnabledCapabilities\": [ \"marketplace\", \"NodeTuning\" ] }}",
				ExtraManifestsRefs:     []corev1.LocalObjectReference{{Name: "foobar1"}, {Name: "foobar2"}},
				TemplateRefs:           []TemplateRef{{Name: "cluster-v1", Namespace: "site-sno-du-1"}},
				Nodes: []NodeSpec{
					{
						BmcAddress:         "idrac-virtualmedia+https://198.51.100.0/redfish/v1/Systems/System.Embedded.1",
						BmcCredentialsName: BmcCredentialsName{Name: "bmc-secret"},
						BootMACAddress:     "00:00:5E:00:53:00",
						HostName:           "master-node1",
						Role:               "master",
						BootMode:           "UEFI",
						InstallerArgs:      "[\"--append-karg\", \"nameserver=198.51.100.0\", \"-n\"]",
						TemplateRefs:       []TemplateRef{{Name: "node-template", Namespace: "site-sno-du-1"}},
						NodeNetwork: &aiv1beta1.NMStateConfigSpec{
							Interfaces: []*aiv1beta1.Interface{
								{Name: "eno1", MacAddress: "00:00:5E:00:53:00"},
								{Name: "bond99", MacAddress: "00:00:5E:00:53:01"},
							},
						},
					},
					{
						BmcAddress:         "idrac-virtualmedia+https://198.51.100.1/redfish/v1/Systems/System.Embedded.1",
						BmcCredentialsName: BmcCredentialsName{Name: "bmc-secret"},
						BootMACAddress:     "00:00:5E:00:53:01",
						HostName:           "worker-node1",
						Role:               "worker",
						BootMode:           "UEFI",
						InstallerArgs:      "[\"--append-karg\", \"nameserver=198.51.100.0\", \"-n\"]",
						TemplateRefs:       []TemplateRef{{Name: "node-template", Namespace: "site-sno-du-1"}},
						NodeNetwork: &aiv1beta1.NMStateConfigSpec{
							Interfaces: []*aiv1beta1.Interface{
								{Name: "eno1", MacAddress: "00:00:5E:00:53:00"},
								{Name: "bond99", MacAddress: "00:00:5E:00:53:01"},
							},
						},
					},
					{
						BmcAddress:         "idrac-virtualmedia+https://198.51.100.2/redfish/v1/Systems/System.Embedded.1",
						BmcCredentialsName: BmcCredentialsName{Name: "bmc-secret"},
						BootMACAddress:     "00:00:5E:00:53:02",
						HostName:           "worker-node2",
						Role:               "worker",
						BootMode:           "UEFI",
						InstallerArgs:      "[\"--append-karg\", \"nameserver=198.51.100.0\", \"-n\"]",
						TemplateRefs:       []TemplateRef{{Name: "node-template", Namespace: "site-sno-du-1"}},
						NodeNetwork: &aiv1beta1.NMStateConfigSpec{
							Interfaces: []*aiv1beta1.Interface{
								{Name: "eno1", MacAddress: "00:00:5E:00:53:00"},
								{Name: "bond99", MacAddress: "00:00:5E:00:53:01"},
							},
						},
					},
				},
			},
		}

		newClusterInstance = oldClusterInstance.DeepCopy()

	})

	It("should return nil for no spec changes", func() {
		err := validatePostProvisioningChanges(testLogger, oldClusterInstance, newClusterInstance, false)
		Expect(err).ToNot(HaveOccurred())
	})

	Context("valid spec changes", func() {
		It("should return nil for extraAnnotation", func() {
			newClusterInstance.Spec.ExtraAnnotations = map[string]map[string]string{
				"BareMetalHost": {
					"foo": "bar",
				},
			}
			err := validatePostProvisioningChanges(testLogger, oldClusterInstance, newClusterInstance, false)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return nil for node deletion", func() {
			nodes := newClusterInstance.Spec.Nodes
			index := 1
			nodes = append(nodes[0:index], nodes[index+1:]...)
			newClusterInstance.Spec.Nodes = nodes
			err := validatePostProvisioningChanges(testLogger, oldClusterInstance, newClusterInstance, false)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return nil for node deletion from beginning", func() {
			// Remove the first node (index 0)
			newClusterInstance.Spec.Nodes = newClusterInstance.Spec.Nodes[1:]
			err := validatePostProvisioningChanges(testLogger, oldClusterInstance, newClusterInstance, false)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return nil for node deletion from end", func() {
			// Remove the last node
			newClusterInstance.Spec.Nodes = newClusterInstance.Spec.Nodes[:len(newClusterInstance.Spec.Nodes)-1]
			err := validatePostProvisioningChanges(testLogger, oldClusterInstance, newClusterInstance, false)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return nil for node addition at beginning", func() {
			// Create a new node to add at the beginning
			newNode := NodeSpec{
				BmcAddress:         "idrac-virtualmedia+https://198.51.100.99/redfish/v1/Systems/System.Embedded.1",
				BmcCredentialsName: BmcCredentialsName{Name: "bmc-secret"},
				BootMACAddress:     "00:00:5E:00:53:99",
				HostName:           "new-worker-node",
				Role:               "worker",
				BootMode:           "UEFI",
				InstallerArgs:      "[\"--append-karg\", \"nameserver=198.51.100.0\", \"-n\"]",
				TemplateRefs:       []TemplateRef{{Name: "node-template", Namespace: "site-sno-du-1"}},
				NodeNetwork: &aiv1beta1.NMStateConfigSpec{
					Interfaces: []*aiv1beta1.Interface{
						{Name: "eno1", MacAddress: "00:00:5E:00:53:99"},
					},
				},
			}
			// Insert at the beginning
			newClusterInstance.Spec.Nodes = append([]NodeSpec{newNode}, newClusterInstance.Spec.Nodes...)
			err := validatePostProvisioningChanges(testLogger, oldClusterInstance, newClusterInstance, false)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return nil for node addition in middle", func() {
			// Create a new node to add in the middle
			newNode := NodeSpec{
				BmcAddress:         "idrac-virtualmedia+https://198.51.100.98/redfish/v1/Systems/System.Embedded.1",
				BmcCredentialsName: BmcCredentialsName{Name: "bmc-secret"},
				BootMACAddress:     "00:00:5E:00:53:98",
				HostName:           "middle-worker-node",
				Role:               "worker",
				BootMode:           "UEFI",
				InstallerArgs:      "[\"--append-karg\", \"nameserver=198.51.100.0\", \"-n\"]",
				TemplateRefs:       []TemplateRef{{Name: "node-template", Namespace: "site-sno-du-1"}},
				NodeNetwork: &aiv1beta1.NMStateConfigSpec{
					Interfaces: []*aiv1beta1.Interface{
						{Name: "eno1", MacAddress: "00:00:5E:00:53:98"},
					},
				},
			}
			// Insert in the middle (after first node)
			nodes := newClusterInstance.Spec.Nodes
			insertIndex := 1
			newNodes := make([]NodeSpec, 0, len(nodes)+1)
			newNodes = append(newNodes, nodes[:insertIndex]...)
			newNodes = append(newNodes, newNode)
			newNodes = append(newNodes, nodes[insertIndex:]...)
			newClusterInstance.Spec.Nodes = newNodes
			err := validatePostProvisioningChanges(testLogger, oldClusterInstance, newClusterInstance, false)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return nil for node reordering (same nodes, different order)", func() {
			// Reverse the order of nodes - this should be considered "no change"
			// since it's the same set of nodes just reordered
			nodes := make([]NodeSpec, len(newClusterInstance.Spec.Nodes))
			copy(nodes, newClusterInstance.Spec.Nodes)

			// Reverse the order
			for i, j := 0, len(nodes)-1; i < j; i, j = i+1, j-1 {
				nodes[i], nodes[j] = nodes[j], nodes[i]
			}

			newClusterInstance.Spec.Nodes = nodes
			err := validatePostProvisioningChanges(testLogger, oldClusterInstance, newClusterInstance, false)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return nil for node replacement (same count)", func() {
			// Replace all nodes with completely new ones (same count)
			newClusterInstance.Spec.Nodes = []NodeSpec{
				{
					BmcAddress:         "idrac-virtualmedia+https://198.51.100.99/redfish/v1/Systems/System.Embedded.1",
					BmcCredentialsName: BmcCredentialsName{Name: "bmc-secret"},
					BootMACAddress:     "00:00:5E:00:53:99",
					HostName:           "replacement-worker-1",
					Role:               "worker",
					BootMode:           "UEFI",
					InstallerArgs:      "[\"--append-karg\", \"nameserver=198.51.100.0\", \"-n\"]",
					TemplateRefs:       []TemplateRef{{Name: "node-template", Namespace: "site-sno-du-1"}},
					NodeNetwork: &aiv1beta1.NMStateConfigSpec{
						Interfaces: []*aiv1beta1.Interface{
							{Name: "eno1", MacAddress: "00:00:5E:00:53:99"},
						},
					},
				},
				{
					BmcAddress:         "idrac-virtualmedia+https://198.51.100.98/redfish/v1/Systems/System.Embedded.1",
					BmcCredentialsName: BmcCredentialsName{Name: "bmc-secret"},
					BootMACAddress:     "00:00:5E:00:53:98",
					HostName:           "replacement-worker-2",
					Role:               "worker",
					BootMode:           "UEFI",
					InstallerArgs:      "[\"--append-karg\", \"nameserver=198.51.100.0\", \"-n\"]",
					TemplateRefs:       []TemplateRef{{Name: "node-template", Namespace: "site-sno-du-1"}},
					NodeNetwork: &aiv1beta1.NMStateConfigSpec{
						Interfaces: []*aiv1beta1.Interface{
							{Name: "eno1", MacAddress: "00:00:5E:00:53:98"},
						},
					},
				},
				{
					BmcAddress:         "idrac-virtualmedia+https://198.51.100.97/redfish/v1/Systems/System.Embedded.1",
					BmcCredentialsName: BmcCredentialsName{Name: "bmc-secret"},
					BootMACAddress:     "00:00:5E:00:53:97",
					HostName:           "replacement-worker-3",
					Role:               "worker",
					BootMode:           "UEFI",
					InstallerArgs:      "[\"--append-karg\", \"nameserver=198.51.100.0\", \"-n\"]",
					TemplateRefs:       []TemplateRef{{Name: "node-template", Namespace: "site-sno-du-1"}},
					NodeNetwork: &aiv1beta1.NMStateConfigSpec{
						Interfaces: []*aiv1beta1.Interface{
							{Name: "eno1", MacAddress: "00:00:5E:00:53:97"},
						},
					},
				},
			}
			err := validatePostProvisioningChanges(testLogger, oldClusterInstance, newClusterInstance, false)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return nil for mixed operation: remove 1, add 2 (net +1)", func() {
			// Remove the first node and add two new nodes (net scale-out)
			originalNodes := oldClusterInstance.Spec.Nodes
			newClusterInstance.Spec.Nodes = []NodeSpec{
				// Keep the second and third nodes (remove first)
				originalNodes[1],
				originalNodes[2],
				// Add two new nodes
				{
					BmcAddress:         "idrac-virtualmedia+https://198.51.100.96/redfish/v1/Systems/System.Embedded.1",
					BmcCredentialsName: BmcCredentialsName{Name: "bmc-secret"},
					BootMACAddress:     "00:00:5E:00:53:96",
					HostName:           "new-worker-1",
					Role:               "worker",
					BootMode:           "UEFI",
					InstallerArgs:      "[\"--append-karg\", \"nameserver=198.51.100.0\", \"-n\"]",
					TemplateRefs:       []TemplateRef{{Name: "node-template", Namespace: "site-sno-du-1"}},
					NodeNetwork: &aiv1beta1.NMStateConfigSpec{
						Interfaces: []*aiv1beta1.Interface{
							{Name: "eno1", MacAddress: "00:00:5E:00:53:96"},
						},
					},
				},
				{
					BmcAddress:         "idrac-virtualmedia+https://198.51.100.95/redfish/v1/Systems/System.Embedded.1",
					BmcCredentialsName: BmcCredentialsName{Name: "bmc-secret"},
					BootMACAddress:     "00:00:5E:00:53:95",
					HostName:           "new-worker-2",
					Role:               "worker",
					BootMode:           "UEFI",
					InstallerArgs:      "[\"--append-karg\", \"nameserver=198.51.100.0\", \"-n\"]",
					TemplateRefs:       []TemplateRef{{Name: "node-template", Namespace: "site-sno-du-1"}},
					NodeNetwork: &aiv1beta1.NMStateConfigSpec{
						Interfaces: []*aiv1beta1.Interface{
							{Name: "eno1", MacAddress: "00:00:5E:00:53:95"},
						},
					},
				},
			}
			err := validatePostProvisioningChanges(testLogger, oldClusterInstance, newClusterInstance, false)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return nil for mixed operation: remove 2, add 1 (net -1)", func() {
			// Remove the first two nodes and add one new node (net scale-in)
			originalNodes := oldClusterInstance.Spec.Nodes
			newClusterInstance.Spec.Nodes = []NodeSpec{
				// Keep only the third node (remove first two)
				originalNodes[2],
				// Add one new node
				{
					BmcAddress:         "idrac-virtualmedia+https://198.51.100.94/redfish/v1/Systems/System.Embedded.1",
					BmcCredentialsName: BmcCredentialsName{Name: "bmc-secret"},
					BootMACAddress:     "00:00:5E:00:53:94",
					HostName:           "replacement-worker",
					Role:               "worker",
					BootMode:           "UEFI",
					InstallerArgs:      "[\"--append-karg\", \"nameserver=198.51.100.0\", \"-n\"]",
					TemplateRefs:       []TemplateRef{{Name: "node-template", Namespace: "site-sno-du-1"}},
					NodeNetwork: &aiv1beta1.NMStateConfigSpec{
						Interfaces: []*aiv1beta1.Interface{
							{Name: "eno1", MacAddress: "00:00:5E:00:53:94"},
						},
					},
				},
			}
			err := validatePostProvisioningChanges(testLogger, oldClusterInstance, newClusterInstance, false)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return nil for clusterImageSetNameRef", func() {
			newClusterInstance.Spec.ClusterImageSetNameRef = "openshift-test-updated"
			err := validatePostProvisioningChanges(testLogger, oldClusterInstance, newClusterInstance, false)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return nil for extraLabel", func() {
			newClusterInstance.Spec.ExtraLabels["ClusterDeployment"] = map[string]string{
				"foo": "bar",
			}
			err := validatePostProvisioningChanges(testLogger, oldClusterInstance, newClusterInstance, false)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return nil when cluster-level cpuArchitecture is set from unset (CRD upgrade)", func() {
			// Simulate CRD upgrade scenario: old spec has no cpuArchitecture,
			// new spec has it set after the field was added to the CRD.
			oldClusterInstance.Spec.CPUArchitecture = ""
			newClusterInstance.Spec.CPUArchitecture = CPUArchitectureX86_64
			err := validatePostProvisioningChanges(testLogger, oldClusterInstance, newClusterInstance, false)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return nil when node cpuArchitecture is set from unset (CRD upgrade)", func() {
			// Simulate CRD upgrade scenario: old nodes have no cpuArchitecture,
			// new nodes have it set after the field was added to the CRD.
			for i := range newClusterInstance.Spec.Nodes {
				newClusterInstance.Spec.Nodes[i].CPUArchitecture = CPUArchitectureX86_64
			}
			err := validatePostProvisioningChanges(testLogger, oldClusterInstance, newClusterInstance, false)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return nil for changes to nodes.nodeNetwork.macAddress when reinstall is triggered", func() {
			newClusterInstance.Spec.Reinstall = &ReinstallSpec{
				Generation: "reinstall-1",
			}
			newClusterInstance.Spec.Nodes[0].NodeNetwork.Interfaces[0].MacAddress = "this-is-allowed"
			reinstallRequested := isReinstallRequested(newClusterInstance)
			Expect(reinstallRequested).To(BeTrue())
			err := validatePostProvisioningChanges(testLogger, oldClusterInstance, newClusterInstance, reinstallRequested)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return nil for changes to nodes.bmcAddress when reinstall is triggered", func() {
			newClusterInstance.Spec.Reinstall = &ReinstallSpec{
				Generation: "reinstall-1",
			}
			newClusterInstance.Spec.Nodes[0].BmcAddress = "this-is-allowed"
			reinstallRequested := isReinstallRequested(newClusterInstance)
			Expect(reinstallRequested).To(BeTrue())
			err := validatePostProvisioningChanges(testLogger, oldClusterInstance, newClusterInstance, reinstallRequested)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("invalid spec changes", func() {
		It("should return error for cluster-level cpuArchitecture change when already set", func() {
			oldClusterInstance.Spec.CPUArchitecture = CPUArchitectureX86_64
			newClusterInstance = oldClusterInstance.DeepCopy()
			newClusterInstance.Spec.CPUArchitecture = CPUArchitectureAarch64
			err := validatePostProvisioningChanges(testLogger, oldClusterInstance, newClusterInstance, false)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("detected unauthorized changes in immutable fields"))
			Expect(err.Error()).To(ContainSubstring("/cpuArchitecture"))
		})

		It("should return error for ClusterNetwork changes", func() {
			newClusterInstance.Spec.ClusterNetwork = []ClusterNetworkEntry{
				{
					CIDR:       "test",
					HostPrefix: 123,
				},
			}
			err := validatePostProvisioningChanges(testLogger, oldClusterInstance, newClusterInstance, false)
			Expect(err).To(HaveOccurred())
		})

		It("should return error for BmcAddress changes", func() {
			newClusterInstance.Spec.Nodes[0].BmcAddress = "this-should-not-change"
			err := validatePostProvisioningChanges(testLogger, oldClusterInstance, newClusterInstance, false)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("detected unauthorized node modifications"))
			Expect(err.Error()).To(ContainSubstring("unauthorized change to bmcAddress"))
		})

		It("should return error for BootMACAddress changes", func() {
			newClusterInstance.Spec.Nodes[0].BootMACAddress = "this-should-not-change"
			err := validatePostProvisioningChanges(testLogger, oldClusterInstance, newClusterInstance, false)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("detected unauthorized node modifications"))
			Expect(err.Error()).To(ContainSubstring("unauthorized change to bootMACAddress"))
		})

		It("should return error for cpuArchitecture change when already set", func() {
			// When cpuArchitecture is already set, changing it should be rejected
			for i := range oldClusterInstance.Spec.Nodes {
				oldClusterInstance.Spec.Nodes[i].CPUArchitecture = CPUArchitectureX86_64
			}
			newClusterInstance = oldClusterInstance.DeepCopy()
			for i := range newClusterInstance.Spec.Nodes {
				newClusterInstance.Spec.Nodes[i].CPUArchitecture = CPUArchitectureAarch64
			}
			err := validatePostProvisioningChanges(testLogger, oldClusterInstance, newClusterInstance, false)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("detected unauthorized node modifications"))
			Expect(err.Error()).To(ContainSubstring("unauthorized change to /cpuArchitecture"))
		})
	})

	Context("scaling operations", func() {
		It("should allow adding a new node", func() {
			newClusterInstance.Spec.Nodes = append(newClusterInstance.Spec.Nodes, NodeSpec{HostName: "node2"})
			err := validatePostProvisioningChanges(testLogger, oldClusterInstance, newClusterInstance, false)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should allow removing a node", func() {
			oldClusterInstance.Spec.Nodes = []NodeSpec{
				{
					HostName: "node1",
				},
				{
					HostName: "node2",
				},
			}
			newClusterInstance.Spec.Nodes = newClusterInstance.Spec.Nodes[:0]
			err := validatePostProvisioningChanges(testLogger, oldClusterInstance, newClusterInstance, false)
			Expect(err).ToNot(HaveOccurred())
		})
	})

})

var _ = Describe("Path Matching Functions", func() {

	Context("pathMatchesPattern", func() {
		It("should return true for an exact match", func() {
			Expect(pathMatchesPattern("foo/bar", "foo/bar")).To(BeTrue())
		})

		It("should return true when wildcard matches a single segment", func() {
			Expect(pathMatchesPattern("foo/bar", "foo/*")).To(BeTrue())
		})

		It("should return false when paths are different", func() {
			Expect(pathMatchesPattern("foo/bar", "foo/test")).To(BeFalse())
		})

		It("should return true when path has extra segments", func() {
			Expect(pathMatchesPattern("foo/bar/test", "foo/bar")).To(BeTrue())
		})

		It("should return true when wildcard matches multiple positions", func() {
			Expect(pathMatchesPattern("foo/bar/test", "foo/*/test")).To(BeTrue())
		})
	})

	Context("pathMatchesAnyPattern", func() {
		It("should return true when at least one pattern matches", func() {
			patterns := []string{"foo/bar", "foo/*"}
			Expect(pathMatchesAnyPattern("foo/bar", patterns)).To(BeTrue())
		})

		It("should return false when no patterns match", func() {
			patterns := []string{"bar/foo", "foo/bar/baz"}
			Expect(pathMatchesAnyPattern("foo/bar", patterns)).To(BeFalse())
		})

		It("should return true when wildcard pattern matches", func() {
			patterns := []string{"foo/*"}
			Expect(pathMatchesAnyPattern("foo/bar", patterns)).To(BeTrue())
		})
	})
})

var _ = Describe("hasSpecChanged", func() {

	var (
		oldCluster, newCluster *ClusterInstance
	)

	BeforeEach(func() {
		oldCluster = &ClusterInstance{
			Spec: ClusterInstanceSpec{
				ClusterName: "old-cluster",
			},
		}
		newCluster = &ClusterInstance{
			Spec: ClusterInstanceSpec{
				ClusterName: "new-cluster",
			},
		}
	})

	It("should return true when specs are different", func() {
		Expect(hasSpecChanged(oldCluster, newCluster)).To(BeTrue())
	})

	It("should return false when specs are identical", func() {
		Expect(hasSpecChanged(oldCluster, oldCluster)).To(BeFalse())
	})

})

var _ = Describe("ClusterInstance Status Checks", func() {
	var clusterInstance *ClusterInstance

	BeforeEach(func() {
		clusterInstance = &ClusterInstance{}
	})

	Context("isProvisioningInProgress", func() {
		It("should return true when provisioning is in progress", func() {
			clusterInstance.Status.Conditions = []metav1.Condition{
				{
					Type:   string(ClusterProvisioned),
					Reason: string(InProgress),
				},
			}
			Expect(isProvisioningInProgress(clusterInstance)).To(BeTrue())
		})

		It("should return false when provisioning is not in progress", func() {
			Expect(isProvisioningInProgress(clusterInstance)).To(BeFalse())
		})
	})

	Context("isProvisioningCompleted", func() {
		It("should return true when provisioning is completed", func() {
			clusterInstance.Status.Conditions = []metav1.Condition{
				{
					Type:   string(ClusterProvisioned),
					Reason: string(Completed),
				},
			}
			Expect(isProvisioningCompleted(clusterInstance)).To(BeTrue())
		})

		It("should return false when provisioning is not completed", func() {
			Expect(isProvisioningCompleted(clusterInstance)).To(BeFalse())
		})
	})

	Context("isReinstallRequested", func() {

		BeforeEach(func() {
			clusterInstance = &ClusterInstance{
				Spec: ClusterInstanceSpec{
					Reinstall: &ReinstallSpec{
						Generation: "test-2",
					},
				},
				Status: ClusterInstanceStatus{
					Reinstall: &ReinstallStatus{
						ObservedGeneration: "test-1",
					},
				},
			}
		})

		It("should return true when reinstall is requested (generation mismatch)", func() {
			Expect(isReinstallRequested(clusterInstance)).To(BeTrue())
		})

		It("should return false when reinstall spec is nil", func() {
			clusterInstance.Spec.Reinstall = nil
			Expect(isReinstallRequested(clusterInstance)).To(BeFalse())
		})

		It("should return false when reinstall status is nil", func() {
			clusterInstance.Status.Reinstall = nil
			Expect(isReinstallRequested(clusterInstance)).To(BeTrue())
		})

		It("should return false when reinstall is not requested (generation matches)", func() {
			clusterInstance.Status.Reinstall.ObservedGeneration = "test-2"
			Expect(isReinstallRequested(clusterInstance)).To(BeFalse())
		})
	})

	Context("isReinstallInProgress", func() {
		It("should return true when reinstall is in progress", func() {
			clusterInstance.Spec.Reinstall = &ReinstallSpec{Generation: "test-2"}
			clusterInstance.Status.Reinstall = &ReinstallStatus{
				InProgressGeneration: "test-2",
				ObservedGeneration:   "test-1",
				RequestEndTime:       metav1.Time{},
			}
			Expect(isReinstallInProgress(clusterInstance)).To(BeTrue())
		})

		It("should return false when reinstall has completed", func() {
			clusterInstance.Spec.Reinstall = &ReinstallSpec{Generation: "test-2"}
			clusterInstance.Status.Reinstall = &ReinstallStatus{
				InProgressGeneration: "",
				ObservedGeneration:   "test-2", // Completed reinstall
				RequestEndTime:       metav1.Now(),
			}
			Expect(isReinstallInProgress(clusterInstance)).To(BeFalse())
		})

		It("should return false when reinstall is not requested", func() {
			Expect(isReinstallInProgress(clusterInstance)).To(BeFalse())
		})
	})

})

var _ = Describe("isValidJSON", func() {
	It("should return true for an empty string", func() {
		Expect(isValidJSON("")).To(BeTrue())
	})

	It("should return true for valid JSON objects", func() {
		Expect(isValidJSON(`{"key": "value"}`)).To(BeTrue())
	})

	It("should return true for valid JSON arrays", func() {
		Expect(isValidJSON(`[1, 2, 3]`)).To(BeTrue())
	})

	It("should return false for invalid JSON", func() {
		Expect(isValidJSON(`{"key": "value"`)).To(BeFalse()) // Missing closing brace
	})

	It("should return false for plain text", func() {
		Expect(isValidJSON("This is not JSON")).To(BeFalse())
	})

	It("should return false for a malformed JSON array", func() {
		Expect(isValidJSON(`[1, 2,]`)).To(BeFalse()) // Trailing comma is invalid
	})

	It("should return false for non-JSON characters", func() {
		Expect(isValidJSON(`{key: value}`)).To(BeFalse()) // Keys must be in double quotes
	})
})

var _ = Describe("validateClusterInstanceJSONFields", func() {
	var clusterInstance *ClusterInstance

	BeforeEach(func() {
		clusterInstance = &ClusterInstance{
			Spec: ClusterInstanceSpec{
				InstallConfigOverrides: "{}",
				IgnitionConfigOverride: "{}",
				ClusterType:            ClusterTypeSNO,
				Nodes: []NodeSpec{
					{
						HostName:               "master-0",
						Role:                   "master",
						InstallerArgs:          "{}",
						IgnitionConfigOverride: "{}",
					},
				},
			},
		}
	})

	It("should pass for valid JSON strings", func() {
		expectErr := validateClusterInstanceJSONFields(clusterInstance)
		Expect(expectErr).To(BeNil())
	})

	It("should fail for invalid InstallConfigOverrides JSON", func() {
		clusterInstance.Spec.InstallConfigOverrides = "{invalid_json}"
		expectErr := validateClusterInstanceJSONFields(clusterInstance)
		Expect(expectErr).To(HaveOccurred())
		Expect(expectErr.Error()).To(ContainSubstring("installConfigOverrides is not a valid JSON"))
	})

	It("should fail for invalid IgnitionConfigOverride JSON", func() {
		clusterInstance.Spec.IgnitionConfigOverride = "{invalid_json}"
		expectErr := validateClusterInstanceJSONFields(clusterInstance)
		Expect(expectErr).To(HaveOccurred())
		Expect(expectErr.Error()).To(ContainSubstring("cluster-level ignitionConfigOverride is not a valid JSON"))
	})

	It("should fail for invalid node-level InstallerArgs JSON", func() {
		clusterInstance.Spec.Nodes[0].InstallerArgs = "{invalid_json}"
		expectErr := validateClusterInstanceJSONFields(clusterInstance)
		Expect(expectErr).To(HaveOccurred())
		Expect(expectErr.Error()).To(ContainSubstring("installerArgs is not a valid JSON"))
	})
})

var _ = Describe("validateControlPlaneAgentCount", func() {
	var clusterInstance *ClusterInstance

	BeforeEach(func() {
		clusterInstance = &ClusterInstance{
			Spec: ClusterInstanceSpec{
				ClusterType: ClusterTypeSNO,
				Nodes: []NodeSpec{
					{
						HostName: "master-0",
						Role:     "master",
					},
				},
			},
		}
	})

	It("should pass for a valid single control-plane agent in SNO", func() {
		expectErr := validateControlPlaneAgentCount(clusterInstance)
		Expect(expectErr).To(BeNil())
	})

	It("should fail for zero control-plane agents", func() {
		clusterInstance.Spec.Nodes = []NodeSpec{}
		expectErr := validateControlPlaneAgentCount(clusterInstance)
		Expect(expectErr).To(HaveOccurred())
		Expect(expectErr.Error()).To(ContainSubstring("at least 1 control-plane agent is required"))
	})

	It("should fail for more than one control-plane agent in SNO", func() {
		clusterInstance.Spec.Nodes = append(clusterInstance.Spec.Nodes, NodeSpec{HostName: "master-1", Role: "master"})
		expectErr := validateControlPlaneAgentCount(clusterInstance)
		Expect(expectErr).To(HaveOccurred())
		Expect(expectErr.Error()).To(ContainSubstring("single node OpenShift cluster-type must have exactly 1 control-plane agent"))
	})

	It("should pass for multiple control-plane agents in Highly Available (MNO) clusters", func() {
		clusterInstance.Spec.ClusterType = ClusterTypeHighlyAvailable
		clusterInstance.Spec.Nodes = append(clusterInstance.Spec.Nodes, NodeSpec{HostName: "master-1", Role: "master"})
		expectErr := validateControlPlaneAgentCount(clusterInstance)
		Expect(expectErr).To(BeNil())
	})

	It("should pass for HighlyAvailableArbiter cluster with arbiter nodes and 2 master nodes", func() {
		clusterInstance.Spec.ClusterType = ClusterTypeHighlyAvailableArbiter
		clusterInstance.Spec.Nodes = append(clusterInstance.Spec.Nodes, NodeSpec{HostName: "master-0", Role: "master"})
		clusterInstance.Spec.Nodes = append(clusterInstance.Spec.Nodes, NodeSpec{HostName: "master-1", Role: "master"})
		clusterInstance.Spec.Nodes = append(clusterInstance.Spec.Nodes, NodeSpec{HostName: "arbiter-1", Role: "arbiter"})
		expectErr := validateControlPlaneAgentCount(clusterInstance)
		Expect(expectErr).To(BeNil())
	})

	It("should fail for HighlyAvailableArbiter cluster with arbiter nodes and less than 2 master nodes", func() {
		clusterInstance.Spec.ClusterType = ClusterTypeHighlyAvailableArbiter
		clusterInstance.Spec.Nodes = append(clusterInstance.Spec.Nodes, NodeSpec{HostName: "arbiter-1", Role: "arbiter"})
		expectErr := validateControlPlaneAgentCount(clusterInstance)
		Expect(expectErr).To(HaveOccurred())
		Expect(expectErr.Error()).To(ContainSubstring("highly available arbiter cluster-type must have at least 1 arbiter agent and 2 control-plane agents"))
	})

	It("should fail for HighlyAvailableArbiter cluster without arbiter nodes", func() {
		clusterInstance.Spec.ClusterType = ClusterTypeHighlyAvailableArbiter
		clusterInstance.Spec.Nodes = append(clusterInstance.Spec.Nodes, NodeSpec{HostName: "master-1", Role: "master"})
		expectErr := validateControlPlaneAgentCount(clusterInstance)
		Expect(expectErr).To(HaveOccurred())
		Expect(expectErr.Error()).To(ContainSubstring("highly available arbiter cluster-type must have at least 1 arbiter agent"))
	})

	It("should fail for SNO cluster with arbiter nodes", func() {
		clusterInstance.Spec.ClusterType = ClusterTypeSNO
		clusterInstance.Spec.Nodes = append(clusterInstance.Spec.Nodes, NodeSpec{HostName: "arbiter-1", Role: "arbiter"})
		expectErr := validateControlPlaneAgentCount(clusterInstance)
		Expect(expectErr).To(HaveOccurred())
		Expect(expectErr.Error()).To(ContainSubstring("arbiter agents can only be used with HighlyAvailableArbiter cluster-type"))
	})

	It("should fail for HighlyAvailable cluster with arbiter nodes", func() {
		clusterInstance.Spec.ClusterType = ClusterTypeHighlyAvailable
		clusterInstance.Spec.Nodes = append(clusterInstance.Spec.Nodes, NodeSpec{HostName: "arbiter-1", Role: "arbiter"})
		expectErr := validateControlPlaneAgentCount(clusterInstance)
		Expect(expectErr).To(HaveOccurred())
		Expect(expectErr.Error()).To(ContainSubstring("arbiter agents can only be used with HighlyAvailableArbiter cluster-type"))
	})
})

var _ = Describe("validateReinstallRequest", func() {
	var clusterInstance *ClusterInstance

	BeforeEach(func() {
		clusterInstance = &ClusterInstance{
			Spec: ClusterInstanceSpec{
				Reinstall: &ReinstallSpec{Generation: "test-1"},
			},
			Status: ClusterInstanceStatus{
				Conditions: []metav1.Condition{
					{
						Type:   string(ClusterProvisioned),
						Reason: string(Completed),
					},
				},
				Reinstall: &ReinstallStatus{},
			},
		}
	})

	It("should return an error if provisioning is not completed", func() {
		clusterInstance.Status.Conditions = nil // No completed provisioning condition
		err := validateReinstallRequest(clusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("reinstall can only be requested after successful provisioning completion"))
	})

	It("should return an error for invalid generation names", func() {
		clusterInstance.Spec.Reinstall.Generation = "INVALID#GEN"
		err := validateReinstallRequest(clusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("invalid reinstall generation"))
	})

	It("should return an error if reinstall is still in progress", func() {
		clusterInstance.Status.Reinstall.InProgressGeneration = "test-0"
		err := validateReinstallRequest(clusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("reinstall generation update is not allowed while a request is still active"))
	})

	It("should return an error if a Generation value is recycled", func() {
		clusterInstance.Status.Reinstall = &ReinstallStatus{
			ObservedGeneration:   "test",
			InProgressGeneration: "",
			RequestEndTime:       metav1.Now(),
			History: []ReinstallHistory{
				{
					Generation: "test-1",
				},
			},
		}
		err := validateReinstallRequest(clusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("cannot reuse a previously used reinstall generation"))
	})

	It("should return an error if the generation is in history", func() {
		clusterInstance.Status.Reinstall = &ReinstallStatus{
			InProgressGeneration: "",
			ObservedGeneration:   "test-0",
			RequestEndTime:       metav1.Now(),
		}
		clusterInstance.Status.Reinstall.History = []ReinstallHistory{
			{
				Generation: "test-1",
			},
		}
		err := validateReinstallRequest(clusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("cannot reuse a previously used reinstall generation"))
	})

	It("should return nil for a valid new reinstall request", func() {
		clusterInstance.Spec.Reinstall.Generation = "test-2"
		clusterInstance.Status.Reinstall.ObservedGeneration = "test-1"
		clusterInstance.Status.Reinstall.InProgressGeneration = ""
		clusterInstance.Status.Reinstall.RequestEndTime = metav1.Now()
		err := validateReinstallRequest(clusterInstance)
		Expect(err).ToNot(HaveOccurred())
	})
})

var _ = Describe("validateReinstallGeneration", func() {
	It("should return an error if generation name is empty", func() {
		err := validateReinstallGeneration("")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("generation name cannot be empty"))
	})

	It("should return an error if generation name is too long", func() {
		longName := "a" + string(make([]byte, maxGenerationLength))
		err := validateReinstallGeneration(longName)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("is too long"))
	})

	It("should return an error if generation name contains invalid characters", func() {
		err := validateReinstallGeneration("invalid#gen")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("must match regex"))
	})

	It("should return nil for a valid generation name", func() {
		err := validateReinstallGeneration("valid-gen1")
		Expect(err).ToNot(HaveOccurred())
	})
})

var _ = Describe("GetAllowedPaths", func() {
	Context("SpecChangeBlocked", func() {
		It("should return empty slice", func() {
			paths := SpecChangeBlocked.GetAllowedPaths()
			Expect(paths).To(Equal([]string{}))
			Expect(paths).To(HaveLen(0))
		})
	})

	Context("SpecChangeUnrestricted", func() {
		It("should return nil to indicate all paths allowed", func() {
			paths := SpecChangeUnrestricted.GetAllowedPaths()
			Expect(paths).To(BeNil())
		})
	})

	Context("SpecChangeBaseOnly", func() {
		It("should return cluster-level and node-level allowed paths", func() {
			paths := SpecChangeBaseOnly.GetAllowedPaths()

			Expect(paths).NotTo(BeNil())
			Expect(paths).NotTo(BeEmpty())

			// Should contain all cluster-level paths
			Expect(paths).To(ContainElement("/extraAnnotations"))
			Expect(paths).To(ContainElement("/extraLabels"))
			Expect(paths).To(ContainElement("/suppressedManifests"))
			Expect(paths).To(ContainElement("/pruneManifests"))
			Expect(paths).To(ContainElement("/clusterImageSetNameRef"))
			Expect(paths).To(ContainElement("/holdInstallation"))

			// Should contain all node-level paths
			Expect(paths).To(ContainElement("/nodes/*/extraAnnotations"))
			Expect(paths).To(ContainElement("/nodes/*/extraLabels"))
			Expect(paths).To(ContainElement("/nodes/*/suppressedManifests"))
			Expect(paths).To(ContainElement("/nodes/*/pruneManifests"))

			// Should NOT contain reinstall-specific paths
			Expect(paths).NotTo(ContainElement("/reinstall"))
			Expect(paths).NotTo(ContainElement("/nodes/*/bmcAddress"))
			Expect(paths).NotTo(ContainElement("/nodes/*/bootMACAddress"))
			Expect(paths).NotTo(ContainElement("/nodes/*/rootDeviceHints"))
		})
	})

	Context("SpecChangeReinstall", func() {
		It("should return base paths plus reinstall-specific paths", func() {
			paths := SpecChangeReinstall.GetAllowedPaths()

			Expect(paths).NotTo(BeNil())
			Expect(paths).NotTo(BeEmpty())

			// Should contain all cluster-level paths
			Expect(paths).To(ContainElement("/extraAnnotations"))
			Expect(paths).To(ContainElement("/extraLabels"))
			Expect(paths).To(ContainElement("/suppressedManifests"))
			Expect(paths).To(ContainElement("/pruneManifests"))
			Expect(paths).To(ContainElement("/clusterImageSetNameRef"))
			Expect(paths).To(ContainElement("/holdInstallation"))

			// Should contain all node-level paths
			Expect(paths).To(ContainElement("/nodes/*/extraAnnotations"))
			Expect(paths).To(ContainElement("/nodes/*/extraLabels"))
			Expect(paths).To(ContainElement("/nodes/*/suppressedManifests"))
			Expect(paths).To(ContainElement("/nodes/*/pruneManifests"))

			// Should contain reinstall cluster path
			Expect(paths).To(ContainElement("/reinstall"))

			// Should contain reinstall node paths
			Expect(paths).To(ContainElement("/nodes/*/bmcAddress"))
			Expect(paths).To(ContainElement("/nodes/*/bootMACAddress"))
			Expect(paths).To(ContainElement("/nodes/*/nodeNetwork/interfaces/*/macAddress"))
			Expect(paths).To(ContainElement("/nodes/*/rootDeviceHints"))
		})

		It("should be a superset of SpecChangeBaseOnly paths", func() {
			basePaths := SpecChangeBaseOnly.GetAllowedPaths()
			reinstallPaths := SpecChangeReinstall.GetAllowedPaths()

			// Every base path should be in reinstall paths
			for _, basePath := range basePaths {
				Expect(reinstallPaths).To(ContainElement(basePath))
			}
		})
	})

	Context("Invalid/Unknown Permission Level", func() {
		It("should return empty slice for invalid permission value", func() {
			invalidPermission := SpecChangePermission(999)
			paths := invalidPermission.GetAllowedPaths()

			Expect(paths).To(Equal([]string{}))
			Expect(paths).To(HaveLen(0))
		})
	})

	Context("Path Content Verification", func() {
		It("should not contain duplicate paths in SpecChangeBaseOnly", func() {
			paths := SpecChangeBaseOnly.GetAllowedPaths()

			seen := make(map[string]bool)
			for _, path := range paths {
				Expect(seen[path]).To(BeFalse(), "Path %s appears multiple times", path)
				seen[path] = true
			}
		})

		It("should not contain duplicate paths in SpecChangeReinstall", func() {
			paths := SpecChangeReinstall.GetAllowedPaths()

			seen := make(map[string]bool)
			for _, path := range paths {
				Expect(seen[path]).To(BeFalse(), "Path %s appears multiple times", path)
				seen[path] = true
			}
		})

		It("should have all paths start with forward slash", func() {
			permissions := []SpecChangePermission{
				SpecChangeBaseOnly,
				SpecChangeReinstall,
			}

			for _, permission := range permissions {
				paths := permission.GetAllowedPaths()
				for _, path := range paths {
					Expect(path).To(HavePrefix("/"), "Path %s should start with /", path)
				}
			}
		})
	})
})

var _ = Describe("determineSpecChangePermission", func() {
	var (
		oldClusterInstance *ClusterInstance
		newClusterInstance *ClusterInstance
	)

	BeforeEach(func() {
		oldClusterInstance = &ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "test-namespace",
			},
			Spec: ClusterInstanceSpec{
				ClusterName:      "test-cluster",
				HoldInstallation: false,
			},
		}
		newClusterInstance = oldClusterInstance.DeepCopy()
	})

	Context("When HoldInstallation is enabled (true)", func() {

		BeforeEach(func() {
			oldClusterInstance.Spec.HoldInstallation = true
		})

		Context("and provisioning has NOT completed", func() {

			It("should return SpecChangeUnrestricted when no provisioning status exists", func() {
				oldClusterInstance.Status.Conditions = nil

				newClusterInstance = oldClusterInstance.DeepCopy()

				result := determineSpecChangePermission(oldClusterInstance, newClusterInstance)
				Expect(result).To(Equal(SpecChangeUnrestricted), "Should return SpecChangeUnrestricted when OLD HoldInstallation=true and no provisioning status")
			})

			It("should return SpecChangeUnrestricted when provisioning status is Unknown", func() {
				oldClusterInstance.Status.Conditions = []metav1.Condition{
					{
						Type:   string(ClusterProvisioned),
						Status: metav1.ConditionUnknown,
						Reason: string(Unknown),
					},
				}

				newClusterInstance = oldClusterInstance.DeepCopy()

				result := determineSpecChangePermission(oldClusterInstance, newClusterInstance)
				Expect(result).To(Equal(SpecChangeUnrestricted), "Should return SpecChangeUnrestricted when OLD HoldInstallation=true and status is Unknown")
			})

			It("should return SpecChangeUnrestricted when provisioning is InProgress", func() {
				oldClusterInstance.Status.Conditions = []metav1.Condition{
					{
						Type:   string(ClusterProvisioned),
						Status: metav1.ConditionFalse,
						Reason: string(InProgress),
					},
				}

				newClusterInstance = oldClusterInstance.DeepCopy()

				result := determineSpecChangePermission(oldClusterInstance, newClusterInstance)
				Expect(result).To(Equal(SpecChangeUnrestricted), "Should return SpecChangeUnrestricted when OLD HoldInstallation=true and provisioning InProgress")
			})

			It("should return SpecChangeUnrestricted when provisioning has Failed", func() {
				oldClusterInstance.Status.Conditions = []metav1.Condition{
					{
						Type:   string(ClusterProvisioned),
						Status: metav1.ConditionFalse,
						Reason: string(Failed),
					},
				}

				newClusterInstance = oldClusterInstance.DeepCopy()

				result := determineSpecChangePermission(oldClusterInstance, newClusterInstance)
				Expect(result).To(Equal(SpecChangeUnrestricted), "Should return SpecChangeUnrestricted when OLD HoldInstallation=true and provisioning Failed")
			})
		})

		Context("and provisioning HAS completed", func() {

			It("should return SpecChangeBaseOnly when provisioning is Completed", func() {
				newClusterInstance.Spec.HoldInstallation = true
				newClusterInstance.Status.Conditions = []metav1.Condition{
					{
						Type:   string(ClusterProvisioned),
						Status: metav1.ConditionTrue,
						Reason: string(Completed),
					},
				}

				result := determineSpecChangePermission(oldClusterInstance, newClusterInstance)
				Expect(result).To(Equal(SpecChangeBaseOnly), "Should return SpecChangeBaseOnly when provisioning completed (HoldInstallation has no effect)")
			})
		})
	})

	Context("When HoldInstallation is disabled (false)", func() {

		BeforeEach(func() {
			oldClusterInstance.Spec.HoldInstallation = false
		})

		Context("and provisioning is in progress", func() {

			It("should follow standard logic and NOT allow changes during InProgress", func() {
				oldClusterInstance.Status.Conditions = []metav1.Condition{
					{
						Type:   string(ClusterProvisioned),
						Status: metav1.ConditionFalse,
						Reason: string(InProgress),
					},
				}

				newClusterInstance = oldClusterInstance.DeepCopy()
				newClusterInstance.Spec.HoldInstallation = false

				result := determineSpecChangePermission(oldClusterInstance, newClusterInstance)
				Expect(result).To(Equal(SpecChangeBlocked), "Should return SpecChangeBlocked when provisioning is in progress")
			})

			It("should return SpecChangeBaseOnly when provisioning is not in progress", func() {
				oldClusterInstance.Status.Conditions = []metav1.Condition{
					{
						Type:   string(ClusterProvisioned),
						Status: metav1.ConditionTrue,
						Reason: string(Completed),
					},
				}

				newClusterInstance = oldClusterInstance.DeepCopy()
				newClusterInstance.Spec.HoldInstallation = false

				result := determineSpecChangePermission(oldClusterInstance, newClusterInstance)
				Expect(result).To(Equal(SpecChangeBaseOnly), "Should return SpecChangeBaseOnly when provisioning is completed and no special operations")
			})
		})

		Context("and reinstall is in progress", func() {

			It("should follow standard logic and NOT allow changes during reinstall", func() {
				oldClusterInstance.Spec.Reinstall = &ReinstallSpec{
					Generation: "test-1",
				}
				oldClusterInstance.Status.Reinstall = &ReinstallStatus{
					InProgressGeneration: "test-1",
					ObservedGeneration:   "test-0",
					RequestEndTime:       metav1.Time{},
				}

				newClusterInstance = oldClusterInstance.DeepCopy()

				result := determineSpecChangePermission(oldClusterInstance, newClusterInstance)
				Expect(result).To(Equal(SpecChangeBlocked), "Should return SpecChangeBlocked when reinstall is in progress")
			})
		})
	})

	Context("HoldInstallation toggle validation", func() {

		// HoldInstallation can ONLY be set to true during CREATE
		// During ValidateUpdate, it can ONLY change from true -> false, NEVER false -> true
		// Even if provisioning hasn't started, false → true is not allowed in updates

		It("should NOT allow toggling HoldInstallation from false to true in ANY update", func() {
			oldClusterInstance.Spec.HoldInstallation = false
			oldClusterInstance.Status.Conditions = []metav1.Condition{
				{
					Type:   string(ClusterProvisioned),
					Status: metav1.ConditionFalse,
					Reason: string(InProgress),
				},
			}

			newClusterInstance = oldClusterInstance.DeepCopy()
			newClusterInstance.Spec.HoldInstallation = true

			result := determineSpecChangePermission(oldClusterInstance, newClusterInstance)
			Expect(result).To(Equal(SpecChangeBlocked))
		})

		It("should NOT allow toggling HoldInstallation from false to true even with no provisioning status", func() {
			oldClusterInstance.Spec.HoldInstallation = false
			oldClusterInstance.Status.Conditions = nil

			newClusterInstance = oldClusterInstance.DeepCopy()
			newClusterInstance.Spec.HoldInstallation = true

			result := determineSpecChangePermission(oldClusterInstance, newClusterInstance)
			Expect(result).To(Equal(SpecChangeBlocked))
		})

		It("should NOT allow toggling HoldInstallation from false to true when provisioning completed", func() {
			oldClusterInstance.Spec.HoldInstallation = false
			oldClusterInstance.Status.Conditions = []metav1.Condition{
				{
					Type:   string(ClusterProvisioned),
					Status: metav1.ConditionTrue,
					Reason: string(Completed),
				},
			}

			newClusterInstance = oldClusterInstance.DeepCopy()
			newClusterInstance.Spec.HoldInstallation = true

			result := determineSpecChangePermission(oldClusterInstance, newClusterInstance)
			Expect(result).To(Equal(SpecChangeBaseOnly))
		})

		It("should allow disabling HoldInstallation from true to false", func() {
			oldClusterInstance.Spec.HoldInstallation = true
			oldClusterInstance.Status.Conditions = []metav1.Condition{
				{
					Type:   string(ClusterProvisioned),
					Status: metav1.ConditionFalse,
					Reason: string(InProgress),
				},
			}

			newClusterInstance = oldClusterInstance.DeepCopy()
			newClusterInstance.Spec.HoldInstallation = false

			result := determineSpecChangePermission(oldClusterInstance, newClusterInstance)
			Expect(result).To(Equal(SpecChangeUnrestricted))
		})

		It("should handle the case where HoldInstallation remains true throughout", func() {
			oldClusterInstance.Spec.HoldInstallation = true
			oldClusterInstance.Status.Conditions = []metav1.Condition{
				{
					Type:   string(ClusterProvisioned),
					Status: metav1.ConditionFalse,
					Reason: string(InProgress),
				},
			}

			newClusterInstance = oldClusterInstance.DeepCopy()
			newClusterInstance.Spec.HoldInstallation = true

			result := determineSpecChangePermission(oldClusterInstance, newClusterInstance)
			Expect(result).To(Equal(SpecChangeUnrestricted))
		})
	})

	Context("Edge cases", func() {

		It("should handle nil status conditions gracefully when HoldInstallation is false", func() {
			oldClusterInstance.Spec.HoldInstallation = false
			oldClusterInstance.Status.Conditions = nil

			newClusterInstance = oldClusterInstance.DeepCopy()

			result := determineSpecChangePermission(oldClusterInstance, newClusterInstance)
			Expect(result).To(Equal(SpecChangeBlocked))
		})

		It("should handle nil status conditions when attempting to enable HoldInstallation", func() {
			oldClusterInstance.Spec.HoldInstallation = false
			oldClusterInstance.Status.Conditions = nil

			newClusterInstance = oldClusterInstance.DeepCopy()
			newClusterInstance.Spec.HoldInstallation = true

			result := determineSpecChangePermission(oldClusterInstance, newClusterInstance)
			Expect(result).To(Equal(SpecChangeBlocked))
		})
	})
})
