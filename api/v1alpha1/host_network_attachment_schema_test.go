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
	"os"
	"strings"

	"github.com/go-openapi/spec"
	"github.com/go-openapi/strfmt"
	"github.com/go-openapi/validate"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/yaml"
)

var _ = Describe("Generated host network attachment schemas", func() {
	checkSchema := func(path string) func() {
		return func() {
			var templateSchema, bindingSchema spec.Schema
			BeforeEach(func() {
				data, err := os.ReadFile(path)
				Expect(err).NotTo(HaveOccurred(), "read the shipped CRD from %s", path)
				var crd struct {
					Spec struct {
						Versions []struct {
							Schema struct{ OpenAPIV3Schema spec.Schema }
						}
					}
				}
				Expect(yaml.Unmarshal(data, &crd)).To(Succeed(), "decode the shipped CRD from %s", path)
				Expect(crd.Spec.Versions).To(HaveLen(1), "the fixture expects a single ClusterInstance API version")
				ciSpec := crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"]
				templates := ciSpec.Properties["hostNetworkAttachmentTemplates"]
				nodeSchema := ciSpec.Properties["nodes"].Items.Schema
				interfaces := nodeSchema.Properties["networkInterfaces"]
				Expect(ciSpec.Required).NotTo(ContainElement("hostNetworkAttachmentTemplates"), "clusters can omit managed attachments")
				Expect(nodeSchema.Required).NotTo(ContainElement("networkInterfaces"), "nodes can omit interface bindings")
				Expect(templates.Default).To(BeNil(), "upgrades must not introduce managed attachments")
				Expect(interfaces.Default).To(BeNil(), "upgrades must not introduce interface bindings")
				Expect(templates.Items).NotTo(BeNil(), "the CRD must validate each attachment template")
				Expect(interfaces.Items).NotTo(BeNil(), "the CRD must validate each interface binding")
				templateSchema = *templates.Items.Schema
				bindingSchema = *interfaces.Items.Schema
			})
			It("preserves the upstream access-mode default", func() {
				Expect(templateSchema.Properties["spec"].Properties["mode"].Default).To(Equal("access"),
					"omitting the attachment mode must use BMO's access-mode default")
			})
			DescribeTable("validates HNA templates using the shipped schema", func(edit func(map[string]interface{}), valid bool) {
				object := map[string]interface{}{"name": "storage-net", "spec": map[string]interface{}{"mode": "trunk", "nativeVLAN": 100, "allowedVLANs": []interface{}{"1", "4094", "100-200"}, "mtu": 9000}}
				edit(object)
				err := validate.AgainstSchema(&templateSchema, object, strfmt.Default)
				if valid {
					Expect(err).NotTo(HaveOccurred(), "the CRD must accept valid attachment template fields: %#v", object)
				} else {
					Expect(err).To(HaveOccurred(), "the CRD must reject invalid attachment template fields: %#v", object)
				}
			},
				Entry("accepts a complete template", func(map[string]interface{}) {}, true),
				Entry("accepts access mode without optional fields", func(obj map[string]interface{}) {
					s := obj["spec"].(map[string]interface{})
					s["mode"] = "access"
					delete(s, "allowedVLANs")
					delete(s, "mtu")
				}, true),
				Entry("accepts hybrid mode", func(obj map[string]interface{}) { obj["spec"].(map[string]interface{})["mode"] = "hybrid" }, true),
				Entry("accepts boundary values", func(obj map[string]interface{}) {
					s := obj["spec"].(map[string]interface{})
					s["nativeVLAN"] = 4094
					s["mtu"] = 9216
				}, true),
				Entry("accepts minimum MTU and native VLAN", func(obj map[string]interface{}) {
					s := obj["spec"].(map[string]interface{})
					s["nativeVLAN"] = 1
					s["mtu"] = 68
				}, true),
				Entry("rejects a missing name", func(obj map[string]interface{}) { delete(obj, "name") }, false),
				Entry("rejects an invalid name", func(obj map[string]interface{}) { obj["name"] = "Invalid_Name" }, false),
				Entry("rejects an overlong name", func(obj map[string]interface{}) { obj["name"] = strings.Repeat("a", 254) }, false),
				Entry("rejects a missing spec", func(obj map[string]interface{}) { delete(obj, "spec") }, false),
				Entry("rejects an unsupported mode", func(obj map[string]interface{}) { obj["spec"].(map[string]interface{})["mode"] = "invalid" }, false),
				Entry("rejects a missing native VLAN", func(obj map[string]interface{}) { delete(obj["spec"].(map[string]interface{}), "nativeVLAN") }, false),
				Entry("rejects native VLAN zero", func(obj map[string]interface{}) { obj["spec"].(map[string]interface{})["nativeVLAN"] = 0 }, false),
				Entry("rejects native VLAN above the maximum", func(obj map[string]interface{}) { obj["spec"].(map[string]interface{})["nativeVLAN"] = 4095 }, false),
				Entry("rejects MTU below the minimum", func(obj map[string]interface{}) { obj["spec"].(map[string]interface{})["mtu"] = 67 }, false),
				Entry("rejects MTU above the maximum", func(obj map[string]interface{}) { obj["spec"].(map[string]interface{})["mtu"] = 9217 }, false),
				Entry("rejects numeric allowed VLANs", func(obj map[string]interface{}) {
					obj["spec"].(map[string]interface{})["allowedVLANs"] = []interface{}{100}
				}, false),
				Entry("rejects malformed VLAN ranges", func(obj map[string]interface{}) {
					obj["spec"].(map[string]interface{})["allowedVLANs"] = []interface{}{"100-200-300"}
				}, false),
			)
			DescribeTable("validates network interface bindings using the shipped schema", func(edit func(map[string]interface{}), valid bool) {
				object := map[string]interface{}{"name": "eno1", "hostNetworkAttachment": map[string]interface{}{"name": "storage-net", "namespace": "cluster"},
					"switchPort": map[string]interface{}{"switchID": "AA:BB:CC:DD:EE:FF", "portID": "Ethernet1/10", "switchSystemName": "tor-1"}}
				edit(object)
				err := validate.AgainstSchema(&bindingSchema, object, strfmt.Default)
				if valid {
					Expect(err).NotTo(HaveOccurred(), "the CRD must accept valid interface binding fields: %#v", object)
				} else {
					Expect(err).To(HaveOccurred(), "the CRD must reject invalid interface binding fields: %#v", object)
				}
			},
				Entry("accepts a named interface and switch port", func(map[string]interface{}) {}, true),
				Entry("accepts a MAC selector", func(obj map[string]interface{}) { delete(obj, "name"); obj["macAddress"] = "00:11:22:33:44:55" }, true),
				Entry("accepts omitted optional fields", func(obj map[string]interface{}) {
					delete(obj, "switchPort")
					delete(obj["hostNetworkAttachment"].(map[string]interface{}), "namespace")
				}, true),
				Entry("rejects an invalid interface name", func(obj map[string]interface{}) { obj["name"] = "eno 1" }, false),
				Entry("rejects an invalid MAC", func(obj map[string]interface{}) { delete(obj, "name"); obj["macAddress"] = "not-a-mac" }, false),
				Entry("rejects a missing attachment", func(obj map[string]interface{}) { delete(obj, "hostNetworkAttachment") }, false),
				Entry("rejects a missing attachment name", func(obj map[string]interface{}) {
					delete(obj["hostNetworkAttachment"].(map[string]interface{}), "name")
				}, false),
				Entry("rejects an empty attachment name", func(obj map[string]interface{}) { obj["hostNetworkAttachment"].(map[string]interface{})["name"] = "" }, false),
				Entry("rejects an overlong attachment name", func(obj map[string]interface{}) {
					obj["hostNetworkAttachment"].(map[string]interface{})["name"] = strings.Repeat("a", 254)
				}, false),
				Entry("rejects an overlong namespace", func(obj map[string]interface{}) {
					obj["hostNetworkAttachment"].(map[string]interface{})["namespace"] = strings.Repeat("a", 64)
				}, false),
				Entry("rejects an invalid switch MAC", func(obj map[string]interface{}) { obj["switchPort"].(map[string]interface{})["switchID"] = "not-a-mac" }, false),
				Entry("rejects a missing switch ID", func(obj map[string]interface{}) { delete(obj["switchPort"].(map[string]interface{}), "switchID") }, false),
				Entry("rejects a missing port ID", func(obj map[string]interface{}) { delete(obj["switchPort"].(map[string]interface{}), "portID") }, false),
			)
		}
	}

	Context("base CRD", checkSchema("../../config/crd/bases/siteconfig.open-cluster-management.io_clusterinstances.yaml"))
	Context("bundle CRD", checkSchema("../../bundle/manifests/siteconfig.open-cluster-management.io_clusterinstances.yaml"))
})
