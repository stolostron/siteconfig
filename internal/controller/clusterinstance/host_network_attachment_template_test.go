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

package clusterinstance

import (
	"reflect"
	"testing"

	bmhv1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	"github.com/stolostron/siteconfig/api/v1alpha1"
	"github.com/stolostron/siteconfig/internal/templates"
	aiTemplates "github.com/stolostron/siteconfig/internal/templates/assisted-installer"
	hcpTemplates "github.com/stolostron/siteconfig/internal/templates/hosted-cluster"
	ibiTemplates "github.com/stolostron/siteconfig/internal/templates/image-based-installer"
	"k8s.io/utils/ptr"
)

func TestBareMetalHostTemplatesOmitUnconfiguredNetworkInterfaces(t *testing.T) {
	templates := map[string]string{
		"assisted-installer": aiTemplates.BareMetalHost,
		"hosted-cluster":     hcpTemplates.BareMetalHost,
	}
	inputs := map[string][]bmhv1alpha1.NetworkInterface{"nil": nil, "empty": {}}

	for name, template := range templates {
		for input, interfaces := range inputs {
			t.Run(name+"/"+input, func(t *testing.T) {
				data := &ClusterData{
					Spec: v1alpha1.ClusterInstanceSpec{ClusterName: "test-cluster"},
					SpecialVars: SpecialVars{CurrentNode: v1alpha1.NodeSpec{
						HostName: "test-node", NetworkInterfaces: interfaces,
					}},
				}
				objects, err := NewTemplateEngine().render("BareMetalHost", template, data)
				if err != nil {
					t.Fatalf("render() returned error: %v", err)
				}
				if len(objects) != 1 {
					t.Fatalf("render() returned %d objects, want 1", len(objects))
				}
				spec, ok := objects[0].manifest["spec"].(map[string]interface{})
				if !ok {
					t.Fatalf("rendered object has no spec map: %#v", objects[0])
				}
				if _, ok := spec["networkInterfaces"]; ok {
					t.Errorf("networkInterfaces should be omitted for %s bindings: %#v", input, spec)
				}
			})
		}
	}
}

func TestHostNetworkAttachmentTemplatesRenderMultipleDocuments(t *testing.T) {
	data := &ClusterData{Spec: v1alpha1.ClusterInstanceSpec{
		ClusterName: "test-cluster",
		HostNetworkAttachmentTemplates: []v1alpha1.HostNetworkAttachmentTemplate{
			{
				Name: "storage-trunk",
				Spec: bmhv1alpha1.HostNetworkAttachmentSpec{
					Mode:       bmhv1alpha1.SwitchportModeTrunk,
					NativeVLAN: 100,
					AllowedVLANs: []string{
						"101",
						"102-103",
					},
				},
			},
			{
				Name: "management-access",
				Spec: bmhv1alpha1.HostNetworkAttachmentSpec{
					Mode:       bmhv1alpha1.SwitchportModeAccess,
					NativeVLAN: 200,
					MTU:        ptr.To(9000),
				},
			},
		},
	}}

	templates := map[string]string{
		"assisted-installer": aiTemplates.HostNetworkAttachment,
		"hosted-cluster":     hcpTemplates.HostNetworkAttachment,
	}

	for name, template := range templates {
		t.Run(name, func(t *testing.T) {
			objects, err := NewTemplateEngine().render("HostNetworkAttachment", template, data)
			if err != nil {
				t.Fatalf("render() returned error: %v", err)
			}
			if len(objects) != 2 {
				t.Fatalf("render() returned %d objects, want 2", len(objects))
			}
			for i, wantName := range []string{"storage-trunk", "management-access"} {
				if got := objects[i].manifest["kind"]; got != "HostNetworkAttachment" {
					t.Errorf("object %d kind = %v, want HostNetworkAttachment", i, got)
				}
				metadata := objects[i].manifest["metadata"].(map[string]interface{})
				if got := metadata["name"]; got != wantName {
					t.Errorf("object %d name = %v, want %s", i, got, wantName)
				}
				if got := metadata["namespace"]; got != "test-cluster" {
					t.Errorf("object %d namespace = %v, want test-cluster", i, got)
				}
				labels, ok := metadata["labels"].(map[string]interface{})
				if !ok {
					t.Fatalf("object %d labels = %#v, want labels map", i, metadata["labels"])
				}
				if got := labels["cluster.open-cluster-management.io/backup"]; got != "cluster-activation" {
					t.Errorf("object %d backup label = %v, want cluster-activation", i, got)
				}
			}

			firstSpec := objects[0].manifest["spec"].(map[string]interface{})
			if got := firstSpec["allowedVLANs"]; !reflect.DeepEqual(got, []interface{}{"101", "102-103"}) {
				t.Errorf("first allowedVLANs = %#v, want [101 102-103]", got)
			}
			secondSpec := objects[1].manifest["spec"].(map[string]interface{})
			if got := secondSpec["mtu"]; got != 9000 {
				t.Errorf("second mtu = %v, want 9000", got)
			}
		})
	}
}

func TestHostNetworkAttachmentTemplateRendersNoDocumentsWithoutTemplates(t *testing.T) {
	data := &ClusterData{Spec: v1alpha1.ClusterInstanceSpec{ClusterName: "test-cluster"}}
	objects, err := NewTemplateEngine().render("HostNetworkAttachment", templates.HostNetworkAttachment, data)
	if err != nil {
		t.Fatalf("render() returned error: %v", err)
	}
	if len(objects) != 0 {
		t.Errorf("render() returned %d objects, want 0 when no attachments are configured", len(objects))
	}
}

func TestBareMetalHostTemplatesRenderNetworkInterfaces(t *testing.T) {
	attachments := []bmhv1alpha1.NetworkInterface{
		{
			Name: "eno1",
			HostNetworkAttachment: bmhv1alpha1.HostNetworkAttachmentRef{
				Name: "storage-trunk",
			},
			SwitchPort: &bmhv1alpha1.SwitchPortIdentifier{
				SwitchID:         "AA:BB:CC:DD:EE:FF",
				PortID:           "Ethernet1/10",
				SwitchSystemName: "tor-1",
			},
		},
		{
			MACAddress: "AA:BB:CC:DD:EE:FF",
			HostNetworkAttachment: bmhv1alpha1.HostNetworkAttachmentRef{
				Name:      "management-access",
				Namespace: "shared-networking",
			},
		},
	}
	data := &ClusterData{
		Spec: v1alpha1.ClusterInstanceSpec{ClusterName: "test-cluster"},
		SpecialVars: SpecialVars{CurrentNode: v1alpha1.NodeSpec{
			HostName:          "test-node",
			NetworkInterfaces: attachments,
		}},
	}

	templates := map[string]string{
		"assisted-installer": aiTemplates.BareMetalHost,
		"hosted-cluster":     hcpTemplates.BareMetalHost,
	}
	for name, template := range templates {
		t.Run(name, func(t *testing.T) {
			objects, err := NewTemplateEngine().render("BareMetalHost", template, data)
			if err != nil {
				t.Fatalf("render() returned error: %v", err)
			}
			spec := objects[0].manifest["spec"].(map[string]interface{})
			interfaces := spec["networkInterfaces"].([]interface{})
			if len(interfaces) != 2 {
				t.Fatalf("networkInterfaces has %d entries, want 2", len(interfaces))
			}
			first := interfaces[0].(map[string]interface{})
			firstHNA := first["hostNetworkAttachment"].(map[string]interface{})
			if first["name"] != "eno1" || firstHNA["name"] != "storage-trunk" || firstHNA["namespace"] != "test-cluster" {
				t.Errorf("first network interface = %#v, want name eno1 referencing test-cluster/storage-trunk", first)
			}
			if namespace := data.SpecialVars.CurrentNode.NetworkInterfaces[0].HostNetworkAttachment.Namespace; namespace != "" {
				t.Errorf("input attachment namespace = %q after rendering, want the original empty namespace", namespace)
			}
			firstSwitchPort, ok := first["switchPort"].(map[string]interface{})
			if !ok {
				t.Fatalf("first network interface switchPort = %#v, want switch ID AA:BB:CC:DD:EE:FF, port Ethernet1/10, and system tor-1",
					first["switchPort"])
			}
			if firstSwitchPort["switchID"] != "AA:BB:CC:DD:EE:FF" ||
				firstSwitchPort["portID"] != "Ethernet1/10" ||
				firstSwitchPort["switchSystemName"] != "tor-1" {
				t.Errorf("first switch port = %#v, want switch ID AA:BB:CC:DD:EE:FF, port Ethernet1/10, and system tor-1",
					firstSwitchPort)
			}
			second := interfaces[1].(map[string]interface{})
			secondHNA := second["hostNetworkAttachment"].(map[string]interface{})
			if second["macAddress"] != "AA:BB:CC:DD:EE:FF" || secondHNA["name"] != "management-access" || secondHNA["namespace"] != "shared-networking" {
				t.Errorf("second network interface = %#v, want MAC AA:BB:CC:DD:EE:FF referencing shared-networking/management-access", second)
			}
			if _, ok := second["switchPort"]; ok {
				t.Errorf("second network interface switchPort = %#v, want omitted", second["switchPort"])
			}
		})
	}

	t.Run("image-based-installer", func(t *testing.T) {
		objects, err := NewTemplateEngine().render("BareMetalHost", ibiTemplates.BareMetalHost, data)
		if err != nil {
			t.Fatalf("render() returned error: %v", err)
		}
		if len(objects) != 1 {
			t.Fatalf("render() returned %d objects, want 1", len(objects))
		}
		spec := objects[0].manifest["spec"].(map[string]interface{})
		if interfaces, ok := spec["networkInterfaces"]; ok {
			t.Errorf("networkInterfaces = %#v, want omitted", interfaces)
		}
	})
}

func TestNetworkAttachmentDependencySyncWaves(t *testing.T) {
	data := &ClusterData{
		Spec: v1alpha1.ClusterInstanceSpec{
			ClusterName: "test-cluster",
			HostNetworkAttachmentTemplates: []v1alpha1.HostNetworkAttachmentTemplate{
				{Name: "storage-trunk"},
			},
		},
		SpecialVars: SpecialVars{
			CurrentNode: v1alpha1.NodeSpec{
				HostName: "test-node",
				NetworkInterfaces: []bmhv1alpha1.NetworkInterface{
					{
						Name: "eno1",
						HostNetworkAttachment: bmhv1alpha1.HostNetworkAttachmentRef{
							Name: "storage-trunk",
						},
					},
				},
			},
		},
	}

	templates := map[string]struct {
		bmhTemplate string
		hnaTemplate string
		bmhSyncWave string
		hnaSyncWave string
	}{
		"assisted-installer": {
			bmhTemplate: aiTemplates.BareMetalHost,
			hnaTemplate: aiTemplates.HostNetworkAttachment,
			bmhSyncWave: "3",
			hnaSyncWave: "0",
		},
		"hosted-cluster": {
			bmhTemplate: hcpTemplates.BareMetalHost,
			hnaTemplate: hcpTemplates.HostNetworkAttachment,
			bmhSyncWave: "3",
			hnaSyncWave: "0",
		},
	}

	for name, templates := range templates {
		t.Run(name, func(t *testing.T) {
			bmhObjects, err := NewTemplateEngine().render("BareMetalHost", templates.bmhTemplate, data)
			if err != nil {
				t.Fatalf("BareMetalHost render() returned error: %v", err)
			}
			if got := bmhObjects[0].manifest["metadata"].(map[string]interface{})["annotations"].(map[string]interface{})[WaveAnnotation]; got != templates.bmhSyncWave {
				t.Errorf("BareMetalHost sync wave = %v, want %s", got, templates.bmhSyncWave)
			}

			hnaObjects, err := NewTemplateEngine().render("HostNetworkAttachment", templates.hnaTemplate, data)
			if err != nil {
				t.Fatalf("HostNetworkAttachment render() returned error: %v", err)
			}
			if got := hnaObjects[0].manifest["metadata"].(map[string]interface{})["annotations"].(map[string]interface{})[WaveAnnotation]; got != templates.hnaSyncWave {
				t.Errorf("HostNetworkAttachment sync wave = %v, want %s", got, templates.hnaSyncWave)
			}
		})
	}
}

func TestBareMetalHostTemplatesResolveManagedReferenceNamespaces(t *testing.T) {
	templates := map[string]string{
		"assisted-installer": aiTemplates.BareMetalHost,
		"hosted-cluster":     hcpTemplates.BareMetalHost,
	}
	for name, template := range templates {
		t.Run(name, func(t *testing.T) {
			var references []interface{}
			for _, namespace := range []string{"", "test-cluster"} {
				data := &ClusterData{
					Spec: v1alpha1.ClusterInstanceSpec{ClusterName: "test-cluster",
						HostNetworkAttachmentTemplates: []v1alpha1.HostNetworkAttachmentTemplate{{Name: "storage-net"}}},
					SpecialVars: SpecialVars{CurrentNode: v1alpha1.NodeSpec{HostName: "host",
						NetworkInterfaces: []bmhv1alpha1.NetworkInterface{{Name: "eno1",
							HostNetworkAttachment: bmhv1alpha1.HostNetworkAttachmentRef{Name: "storage-net", Namespace: namespace}}}}},
				}
				objects, err := NewTemplateEngine().render("BareMetalHost", template, data)
				if err != nil {
					t.Fatalf("render(namespace=%q): %v", namespace, err)
				}
				if len(objects) != 1 {
					t.Fatalf("render(namespace=%q) produced %d objects, want 1", namespace, len(objects))
				}
				spec := objects[0].manifest["spec"].(map[string]interface{})
				interfaces := spec["networkInterfaces"].([]interface{})
				if len(interfaces) != 1 {
					t.Fatalf("render(namespace=%q) produced %d interfaces, want 1", namespace, len(interfaces))
				}
				reference := interfaces[0].(map[string]interface{})["hostNetworkAttachment"]
				want := map[string]interface{}{"name": "storage-net", "namespace": "test-cluster"}
				if !reflect.DeepEqual(reference, want) {
					t.Errorf("namespace=%q reference=%#v, want %#v", namespace, reference, want)
				}
				references = append(references, reference)
			}
			if !reflect.DeepEqual(references[0], references[1]) {
				t.Errorf("defaulted and explicit managed references differ: %#v", references)
			}
		})
	}
}
