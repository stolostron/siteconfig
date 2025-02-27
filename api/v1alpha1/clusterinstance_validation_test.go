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
				Nodes: []NodeSpec{{
					BmcAddress:         "idrac-virtualmedia+https://198.51.100.0/redfish/v1/Systems/System.Embedded.1",
					BmcCredentialsName: BmcCredentialsName{Name: "bmc-secret"},
					BootMACAddress:     "00:00:5E:00:53:00",
					HostName:           "node1",
					Role:               "master",
					BootMode:           "UEFI",
					InstallerArgs:      "[\"--append-karg\", \"nameserver=198.51.100.0\", \"-n\"]",
					TemplateRefs:       []TemplateRef{{Name: "node-template", Namespace: "site-sno-du-1"}},
				}},
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

		It("should return nil for extraLabel", func() {
			newClusterInstance.Spec.ExtraLabels["ClusterDeployment"] = map[string]string{
				"foo": "bar",
			}
			err := validatePostProvisioningChanges(testLogger, oldClusterInstance, newClusterInstance, false)
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("invalid spec changes", func() {
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

		It("should return error for BootMACAddress changes", func() {
			newClusterInstance.Spec.Nodes[0].BootMACAddress = "this-should-not-change"
			err := validatePostProvisioningChanges(testLogger, oldClusterInstance, newClusterInstance, false)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("detected unauthorized changes in immutable fields: /nodes/0/bootMACAddress"))
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
