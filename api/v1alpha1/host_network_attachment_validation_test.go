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
	"strings"

	bmh_v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Host network attachment admission", func() {
	var clusterInstance *ClusterInstance

	BeforeEach(func() {
		clusterInstance = &ClusterInstance{Spec: ClusterInstanceSpec{
			ClusterName:  "test-cluster",
			ClusterType:  ClusterTypeSNO,
			TemplateRefs: []TemplateRef{{Name: "cluster-template", Namespace: "default"}},
			HostNetworkAttachmentTemplates: []HostNetworkAttachmentTemplate{{
				Name: "storage-net",
				Spec: bmh_v1alpha1.HostNetworkAttachmentSpec{
					Mode:       bmh_v1alpha1.SwitchportModeTrunk,
					NativeVLAN: 100,
				},
			}},
			Nodes: []NodeSpec{{
				HostName:     "node1",
				Role:         "master",
				TemplateRefs: []TemplateRef{{Name: "node-template", Namespace: "default"}},
			}},
		}}
	})

	// Exercise both entry points so invalid specs are rejected before rendering,
	// including updates made while installation is held.
	checkAdmission := func(ci *ClusterInstance, expectedError string) {
		validator := &clusterInstanceValidator{}
		_, err := validator.ValidateCreate(context.Background(), ci)
		if expectedError == "" {
			Expect(err).NotTo(HaveOccurred(), "create admission must accept valid attachment configuration")
		} else {
			Expect(err).To(MatchError(ContainSubstring(expectedError)),
				"create admission must reject invalid attachment configuration before rendering")
		}

		oldCI := ci.DeepCopy()
		oldCI.Spec.HoldInstallation = true
		oldCI.Spec.HostNetworkAttachmentTemplates = nil
		for i := range oldCI.Spec.Nodes {
			oldCI.Spec.Nodes[i].NetworkInterfaces = nil
		}
		newCI := ci.DeepCopy()
		newCI.Spec.HoldInstallation = true
		_, err = validator.ValidateUpdate(context.Background(), oldCI, newCI)
		if expectedError == "" {
			Expect(err).NotTo(HaveOccurred(), "update admission must allow valid attachment changes while installation is held")
		} else {
			Expect(err).To(MatchError(ContainSubstring(expectedError)),
				"update admission must reject invalid attachment changes even while installation is held")
		}
	}

	DescribeTable("validates attachment template names", func(names []string, expectedError string) {
		spec := clusterInstance.Spec.HostNetworkAttachmentTemplates[0].Spec
		clusterInstance.Spec.HostNetworkAttachmentTemplates = nil
		for _, name := range names {
			clusterInstance.Spec.HostNetworkAttachmentTemplates = append(
				clusterInstance.Spec.HostNetworkAttachmentTemplates, HostNetworkAttachmentTemplate{Name: name, Spec: spec})
		}
		checkAdmission(clusterInstance, expectedError)
	},
		Entry("accepts omitted templates", []string(nil), ""),
		Entry("accepts distinct DNS subdomains", []string{"storage.net-1", strings.Repeat("a", 253)}, ""),
		Entry("rejects an empty name", []string{""}, `hostNetworkAttachmentTemplates[0]: invalid name ""`),
		Entry("rejects duplicate names", []string{"storage-net", "storage-net"}, `duplicate name "storage-net"`),
		Entry("rejects uppercase letters", []string{"Storage"}, "invalid name"),
		Entry("rejects a leading hyphen", []string{"-storage"}, "invalid name"),
		Entry("rejects a trailing hyphen", []string{"storage-"}, "invalid name"),
		Entry("rejects an empty DNS label", []string{"storage..net"}, "invalid name"),
		Entry("rejects an overlong name", []string{strings.Repeat("a", 254)}, "invalid name"),
	)

	DescribeTable("validates HNA mode and allowed VLANs",
		func(mode bmh_v1alpha1.SwitchPortMode, vlans []string, expectedError string) {
			clusterInstance.Spec.HostNetworkAttachmentTemplates[0].Spec.Mode = mode
			clusterInstance.Spec.HostNetworkAttachmentTemplates[0].Spec.AllowedVLANs = vlans
			checkAdmission(clusterInstance, expectedError)
		},
		Entry("accepts access mode without tagged VLANs", bmh_v1alpha1.SwitchportModeAccess, nil, ""),
		Entry("accepts omitted mode without tagged VLANs", bmh_v1alpha1.SwitchPortMode(""), []string{}, ""),
		Entry("rejects tagged VLANs in access mode", bmh_v1alpha1.SwitchportModeAccess, []string{"100"},
			"hostNetworkAttachmentTemplates[0].spec.allowedVLANs: cannot be specified for access mode"),
		Entry("treats omitted mode as access", bmh_v1alpha1.SwitchPortMode(""), []string{"100"},
			"hostNetworkAttachmentTemplates[0].spec.allowedVLANs: cannot be specified for access mode"),
		Entry("accepts trunk VLAN boundaries and ranges", bmh_v1alpha1.SwitchportModeTrunk,
			[]string{"1", "4094", "1-4094", "100-101"}, ""),
		Entry("accepts hybrid VLAN boundaries and ranges", bmh_v1alpha1.SwitchportModeHybrid,
			[]string{"1", "4094", "1-4094"}, ""),
		Entry("accepts trunk mode without tagged VLANs", bmh_v1alpha1.SwitchportModeTrunk, nil, ""),
		Entry("rejects VLAN zero", bmh_v1alpha1.SwitchportModeTrunk, []string{"0"},
			"hostNetworkAttachmentTemplates[0].spec.allowedVLANs[0]"),
		Entry("rejects VLAN above the maximum", bmh_v1alpha1.SwitchportModeTrunk, []string{"4095"},
			"VLAN ID 4095 out of range 1-4094"),
		Entry("rejects range start below the minimum", bmh_v1alpha1.SwitchportModeTrunk, []string{"0-100"},
			"VLAN ID 0 out of range 1-4094"),
		Entry("rejects range end above the maximum", bmh_v1alpha1.SwitchportModeTrunk, []string{"1-4095"},
			"VLAN ID 4095 out of range 1-4094"),
		Entry("rejects range start above the maximum", bmh_v1alpha1.SwitchportModeTrunk, []string{"4095-4096"},
			"VLAN ID 4095 out of range 1-4094"),
		Entry("rejects range end below the minimum", bmh_v1alpha1.SwitchportModeTrunk, []string{"1-0"},
			"VLAN ID 0 out of range 1-4094"),
		Entry("rejects equal range bounds", bmh_v1alpha1.SwitchportModeTrunk, []string{"100-100"},
			"range start (100) must be less than end (100)"),
		Entry("rejects descending ranges", bmh_v1alpha1.SwitchportModeTrunk, []string{"200-100"},
			"range start (200) must be less than end (100)"),
		Entry("rejects empty entries", bmh_v1alpha1.SwitchportModeTrunk, []string{""}, "invalid VLAN ID"),
		Entry("rejects nonnumeric entries", bmh_v1alpha1.SwitchportModeTrunk, []string{"abc"}, "invalid VLAN ID"),
		Entry("rejects missing range ends", bmh_v1alpha1.SwitchportModeTrunk, []string{"1-"}, "invalid range end"),
		Entry("rejects nonnumeric range ends", bmh_v1alpha1.SwitchportModeTrunk, []string{"1-abc"}, "invalid range end"),
		Entry("rejects extra range separators", bmh_v1alpha1.SwitchportModeTrunk, []string{"1-2-3"}, "invalid range end"),
		Entry("rejects overflowing VLAN IDs", bmh_v1alpha1.SwitchportModeTrunk,
			[]string{"999999999999999999999999999999"}, "invalid VLAN ID"),
		Entry("rejects overflowing range ends", bmh_v1alpha1.SwitchportModeTrunk,
			[]string{"1-999999999999999999999999999999"}, "invalid range end"),
		Entry("checks every VLAN entry", bmh_v1alpha1.SwitchportModeHybrid, []string{"100", "4095"},
			"hostNetworkAttachmentTemplates[0].spec.allowedVLANs[1]"),
	)

	It("reports the invalid HNA template index", func() {
		clusterInstance.Spec.HostNetworkAttachmentTemplates = append(
			clusterInstance.Spec.HostNetworkAttachmentTemplates, HostNetworkAttachmentTemplate{
				Name: "invalid-net",
				Spec: bmh_v1alpha1.HostNetworkAttachmentSpec{
					Mode:         bmh_v1alpha1.SwitchportModeTrunk,
					NativeVLAN:   100,
					AllowedVLANs: []string{"4095"},
				},
			})
		checkAdmission(clusterInstance, "hostNetworkAttachmentTemplates[1].spec.allowedVLANs[0]")
	})

	DescribeTable("validates selectors per node",
		func(selectors []bmh_v1alpha1.NetworkInterface, expectedError string) {
			for i := range selectors {
				selectors[i].HostNetworkAttachment = bmh_v1alpha1.HostNetworkAttachmentRef{Name: "storage-net"}
			}
			clusterInstance.Spec.Nodes[0].NetworkInterfaces = selectors
			checkAdmission(clusterInstance, expectedError)
		},
		Entry("rejects a missing selector", []bmh_v1alpha1.NetworkInterface{{}},
			"exactly one of name or macAddress must be set"),
		Entry("rejects simultaneous name and MAC selectors", []bmh_v1alpha1.NetworkInterface{
			{Name: "eno1", MACAddress: "00:11:22:33:44:55"}}, "exactly one of name or macAddress must be set"),
		Entry("rejects duplicate names", []bmh_v1alpha1.NetworkInterface{{Name: "eno1"}, {Name: "eno1"}},
			"nodes[0].networkInterfaces[1]: duplicate interface selector"),
		Entry("matches BMO name comparison", []bmh_v1alpha1.NetworkInterface{{Name: "eno1"}, {Name: "ENO1"}},
			"duplicate interface selector"),
		Entry("rejects duplicate MACs", []bmh_v1alpha1.NetworkInterface{
			{MACAddress: "aa:bb:cc:dd:ee:ff"}, {MACAddress: "aa:bb:cc:dd:ee:ff"}}, "duplicate interface selector"),
		Entry("rejects MACs differing only in case", []bmh_v1alpha1.NetworkInterface{
			{MACAddress: "AA:BB:CC:DD:EE:FF"}, {MACAddress: "aa:bb:cc:dd:ee:ff"}}, "duplicate interface selector"),
		Entry("accepts distinct selectors", []bmh_v1alpha1.NetworkInterface{
			{Name: "eno1"}, {Name: "eno2"}, {MACAddress: "aa:bb:cc:dd:ee:ff"}}, ""),
	)

	It("rejects duplicate selectors even when attachments differ", func() {
		clusterInstance.Spec.Nodes[0].NetworkInterfaces = []bmh_v1alpha1.NetworkInterface{
			{Name: "eno1", HostNetworkAttachment: bmh_v1alpha1.HostNetworkAttachmentRef{Name: "storage-net"}},
			{Name: "eno1", HostNetworkAttachment: bmh_v1alpha1.HostNetworkAttachmentRef{
				Name: "shared-net", Namespace: "inventory"}},
		}
		checkAdmission(clusterInstance, "nodes[0].networkInterfaces[1]: duplicate interface selector")
	})

	DescribeTable("validates attachment references",
		func(reference bmh_v1alpha1.HostNetworkAttachmentRef, expectedError string) {
			clusterInstance.Spec.Nodes[0].NetworkInterfaces = []bmh_v1alpha1.NetworkInterface{{
				Name: "eno1", HostNetworkAttachment: reference,
			}}
			checkAdmission(clusterInstance, expectedError)
		},
		Entry("rejects an empty attachment name", bmh_v1alpha1.HostNetworkAttachmentRef{},
			`hostNetworkAttachment.name "" is invalid`),
		Entry("requires a namespace for an external attachment", bmh_v1alpha1.HostNetworkAttachmentRef{Name: "external-net"},
			`external hostNetworkAttachment "external-net" must specify a namespace`),
		Entry("rejects an invalid namespace", bmh_v1alpha1.HostNetworkAttachmentRef{Name: "external-net", Namespace: "INVALID_NAMESPACE"},
			"hostNetworkAttachment.namespace"),
		Entry("accepts a namespaced external attachment", bmh_v1alpha1.HostNetworkAttachmentRef{Name: "external-net", Namespace: "inventory"}, ""),
		Entry("accepts an external attachment with the same name as a managed one",
			bmh_v1alpha1.HostNetworkAttachmentRef{Name: "storage-net", Namespace: "inventory"}, ""),
	)

	It("allows the same selector on different nodes", func() {
		clusterInstance.Spec.Nodes[0].NetworkInterfaces = []bmh_v1alpha1.NetworkInterface{{
			Name: "eno1", HostNetworkAttachment: bmh_v1alpha1.HostNetworkAttachmentRef{Name: "storage-net"},
		}}
		worker := *clusterInstance.Spec.Nodes[0].DeepCopy()
		worker.HostName = "worker1"
		worker.Role = "worker"
		clusterInstance.Spec.Nodes = append(clusterInstance.Spec.Nodes, worker)
		checkAdmission(clusterInstance, "")
	})

	It("reports the node index for an invalid reference on a later node", func() {
		clusterInstance.Spec.Nodes[0].NetworkInterfaces = []bmh_v1alpha1.NetworkInterface{{
			Name: "eno1", HostNetworkAttachment: bmh_v1alpha1.HostNetworkAttachmentRef{Name: "storage-net"},
		}}
		worker := *clusterInstance.Spec.Nodes[0].DeepCopy()
		worker.HostName = "worker1"
		worker.Role = "worker"
		worker.NetworkInterfaces[0].HostNetworkAttachment = bmh_v1alpha1.HostNetworkAttachmentRef{
			Name: "invalid_net", Namespace: "inventory",
		}
		clusterInstance.Spec.Nodes = append(clusterInstance.Spec.Nodes, worker)
		checkAdmission(clusterInstance, `nodes[1].networkInterfaces[0]: hostNetworkAttachment.name "invalid_net" is invalid`)
	})
})
