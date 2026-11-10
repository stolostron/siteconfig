/*
Copyright 2026 Red Hat, Inc.

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
	"context"

	bmo "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func provisionedHNAClusterInstance() *ClusterInstance {
	return &ClusterInstance{
		Spec: ClusterInstanceSpec{
			ClusterName: "test-cluster", ClusterType: ClusterTypeSNO,
			TemplateRefs: []TemplateRef{{Name: "cluster-template", Namespace: "default"}},
			HostNetworkAttachmentTemplates: []HostNetworkAttachmentTemplate{{Name: "storage-net",
				Spec: bmo.HostNetworkAttachmentSpec{Mode: bmo.SwitchportModeAccess, NativeVLAN: 100}}},
			Nodes: []NodeSpec{{HostName: "master", Role: "master",
				TemplateRefs: []TemplateRef{{Name: "node-template", Namespace: "default"}},
				NetworkInterfaces: []bmo.NetworkInterface{{Name: "eno1",
					HostNetworkAttachment: bmo.HostNetworkAttachmentRef{Name: "external-net", Namespace: "shared"}}}}},
		},
		Status: ClusterInstanceStatus{Conditions: []metav1.Condition{{Type: string(ClusterProvisioned),
			Status: metav1.ConditionTrue, Reason: string(Completed)}}},
	}
}

var _ = Describe("Host network attachment update permissions", func() {
	DescribeTableSubtree("network attachment changes", func(edit func(*ClusterInstance)) {
		DescribeTable("checks attachment edits through update admission", func(requestReinstall bool) {
			oldCI := provisionedHNAClusterInstance()
			if requestReinstall {
				oldCI.Spec.Reinstall = &ReinstallSpec{Generation: "generation-1"}
				oldCI.Status.Reinstall = &ReinstallStatus{ObservedGeneration: "generation-1", RequestEndTime: metav1.Now()}
			}
			newCI := oldCI.DeepCopy()
			edit(newCI)
			if requestReinstall {
				newCI.Spec.Reinstall.Generation = "generation-2"
			}
			Expect(ValidateClusterInstance(newCI)).To(Succeed(),
				"the fixture must pass general validation before checking attachment update permissions")
			_, err := (&clusterInstanceValidator{}).ValidateUpdate(context.Background(), oldCI, newCI)
			if requestReinstall {
				Expect(err).NotTo(HaveOccurred(), "an eligible reinstall generation must permit attachment changes")
			} else {
				Expect(err).To(MatchError(ContainSubstring("invalid spec changes detected")),
					"ordinary updates must preserve existing attachment configuration")
			}
		},
			Entry("rejects ordinary updates", false),
			Entry("accepts a new eligible reinstall generation", true),
		)
	},
		Entry("adding a template", func(ci *ClusterInstance) {
			ci.Spec.HostNetworkAttachmentTemplates = append(ci.Spec.HostNetworkAttachmentTemplates,
				HostNetworkAttachmentTemplate{Name: "management-net",
					Spec: bmo.HostNetworkAttachmentSpec{Mode: bmo.SwitchportModeAccess, NativeVLAN: 200}})
		}),
		Entry("removing templates", func(ci *ClusterInstance) { ci.Spec.HostNetworkAttachmentTemplates = nil }),
		Entry("changing a template", func(ci *ClusterInstance) { ci.Spec.HostNetworkAttachmentTemplates[0].Spec.NativeVLAN = 200 }),
		Entry("adding a binding", func(ci *ClusterInstance) {
			ci.Spec.Nodes[0].NetworkInterfaces = append(ci.Spec.Nodes[0].NetworkInterfaces,
				bmo.NetworkInterface{Name: "eno2", HostNetworkAttachment: bmo.HostNetworkAttachmentRef{Name: "storage-net"}})
		}),
		Entry("removing bindings", func(ci *ClusterInstance) { ci.Spec.Nodes[0].NetworkInterfaces = nil }),
		Entry("changing a binding", func(ci *ClusterInstance) { ci.Spec.Nodes[0].NetworkInterfaces[0].Name = "eno2" }),
	)
})

var _ = Describe("Host network attachment scale-out permissions", func() {
	DescribeTable("allows new-worker bindings without authorizing changes to retained configuration",
		func(scenario, expectedError string) {
			oldCI := provisionedHNAClusterInstance()
			newCI := oldCI.DeepCopy()
			worker := *newCI.Spec.Nodes[0].DeepCopy()
			worker.HostName = "worker"
			worker.Role = "worker"
			switch scenario {
			case "managed":
				worker.NetworkInterfaces[0].HostNetworkAttachment = bmo.HostNetworkAttachmentRef{Name: "storage-net"}
			case "managed-explicit":
				worker.NetworkInterfaces[0].HostNetworkAttachment = bmo.HostNetworkAttachmentRef{Name: "storage-net", Namespace: newCI.Spec.ClusterName}
			case "new-template":
				newCI.Spec.HostNetworkAttachmentTemplates = append(newCI.Spec.HostNetworkAttachmentTemplates,
					HostNetworkAttachmentTemplate{Name: "new-net", Spec: bmo.HostNetworkAttachmentSpec{Mode: bmo.SwitchportModeAccess, NativeVLAN: 200}})
			case "changed-template":
				newCI.Spec.HostNetworkAttachmentTemplates[0].Spec.NativeVLAN = 200
			case "retained-binding":
				newCI.Spec.Nodes[0].NetworkInterfaces[0].Name = "eno2"
			}
			newCI.Spec.Nodes = append(newCI.Spec.Nodes, worker)
			Expect(ValidateClusterInstance(newCI)).To(Succeed(),
				"the fixture must pass general validation before checking scale-out permissions for %q", scenario)
			_, err := (&clusterInstanceValidator{}).ValidateUpdate(context.Background(), oldCI, newCI)
			if expectedError == "" {
				Expect(err).NotTo(HaveOccurred(), "a new worker may use the %q reference without changing existing configuration", scenario)
			} else {
				Expect(err).To(MatchError(ContainSubstring(expectedError)),
					"scale-out scenario %q must preserve the existing attachment configuration", scenario)
			}
		},
		Entry("accepts a managed reference", "managed", ""),
		Entry("accepts a managed reference with an explicit namespace", "managed-explicit", ""),
		Entry("accepts an external namespaced reference", "external", ""),
		Entry("rejects a new cluster-level template", "new-template", "hostNetworkAttachmentTemplates"),
		Entry("rejects a changed cluster-level template", "changed-template", "hostNetworkAttachmentTemplates"),
		Entry("rejects a changed retained-node binding", "retained-binding", "networkInterfaces"),
	)
})
