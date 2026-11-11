/*
Copyright 2026.

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
	"encoding/json"
	"testing"

	. "github.com/onsi/gomega"
	aiv1beta1 "github.com/openshift/assisted-service/api/v1beta1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func networkValidationFixture() *ClusterInstance {
	return &ClusterInstance{Spec: ClusterInstanceSpec{
		ClusterType:  ClusterTypeSNO,
		TemplateRefs: []TemplateRef{{Name: "cluster", Namespace: "templates"}},
		Nodes: []NodeSpec{{HostName: "node1", Role: "master",
			TemplateRefs: []TemplateRef{{Name: "node", Namespace: "templates"}},
			NodeNetwork: &aiv1beta1.NMStateConfigSpec{
				Interfaces: []*aiv1beta1.Interface{
					{Name: "eno1", MacAddress: "00:11:22:33:44:55"},
					{Name: "eno2", MacAddress: "00:11:22:33:44:77"},
				},
			},
			TemplateData: map[string]apiextensionsv1.JSON{"ip": {Raw: []byte(`"192.0.2.10"`)},
				"nested":      {Raw: []byte(`{"addresses":["192.0.2.10"],"optional":null}`)},
				"extraLabels": {Raw: []byte(`{"foo":"old"}`)},
				"large":       {Raw: []byte(`9007199254740992`)},
			},
		}},
	}}
}

func TestNetworkParameterAdmission(t *testing.T) {
	g := NewWithT(t)
	v := &clusterInstanceValidator{}
	_, err := v.ValidateCreate(context.Background(), networkValidationFixture())
	g.Expect(err).To(Succeed())
	withoutNetwork := networkValidationFixture()
	withoutNetwork.Spec.Nodes[0].NodeNetwork = nil
	_, err = v.ValidateCreate(context.Background(), withoutNetwork)
	g.Expect(err).To(Succeed())
	for _, state := range []struct {
		name                                                                                  string
		hold, release, provisioning, completed, reinstall, active, allowData, allowInterfaces bool
	}{
		{name: "held", hold: true, allowData: true, allowInterfaces: true},
		{name: "release hold with final edits", hold: true, release: true, allowData: true, allowInterfaces: true},
		{name: "unknown status"},
		{name: "active provisioning", provisioning: true},
		{name: "completed", completed: true},
		{name: "completed even if hold remains set", hold: true, completed: true},
		{name: "new reinstall", completed: true, reinstall: true, allowInterfaces: true},
		{name: "active reinstall", completed: true, reinstall: true, active: true},
	} {
		t.Run(state.name, func(t *testing.T) {
			old := networkValidationFixture()
			old.Spec.HoldInstallation = state.hold
			if state.provisioning {
				old.Status.Conditions = []metav1.Condition{{Type: string(ClusterProvisioned),
					Status: metav1.ConditionFalse, Reason: string(InProgress)}}
			}
			if state.completed {
				old.Status.Conditions = []metav1.Condition{{Type: string(ClusterProvisioned),
					Status: metav1.ConditionTrue, Reason: string(Completed)}}
			}
			if state.active {
				old.Spec.Reinstall = &ReinstallSpec{Generation: "reinstall-1"}
				old.Status.Reinstall = &ReinstallStatus{InProgressGeneration: "reinstall-1"}
			}
			for _, change := range []struct {
				name    string
				mutate  func(*NodeSpec)
				allowed bool
			}{
				{"edit", func(nn *NodeSpec) { nn.TemplateData["ip"] = apiextensionsv1.JSON{Raw: []byte(`"192.0.2.20"`)} }, state.allowData},
				{"add", func(nn *NodeSpec) { nn.TemplateData["new"] = apiextensionsv1.JSON{Raw: []byte(`false`)} }, state.allowData},
				{"delete", func(nn *NodeSpec) { delete(nn.TemplateData, "ip") }, state.allowData},
				{"nested", func(nn *NodeSpec) {
					nn.TemplateData["nested"] = apiextensionsv1.JSON{Raw: []byte(`{"addresses":["192.0.2.20"],"optional":null}`)}
				}, state.allowData},
				{"remove map", func(nn *NodeSpec) { nn.TemplateData = nil }, state.allowData},
				{"large integer", func(nn *NodeSpec) {
					nn.TemplateData["large"] = apiextensionsv1.JSON{Raw: []byte(`9007199254740993`)}
				}, state.allowData},
				{"permission-name key", func(nn *NodeSpec) {
					nn.TemplateData["extraLabels"] = apiextensionsv1.JSON{Raw: []byte(`{"foo":"new"}`)}
				}, state.allowData},
				{"permission-name removal", func(nn *NodeSpec) { delete(nn.TemplateData, "extraLabels") }, state.allowData},
				{"nested permission name", func(nn *NodeSpec) {
					nn.TemplateData["nested"] = apiextensionsv1.JSON{Raw: []byte(`{"extraAnnotations":"new"}`)}
				}, state.allowData},
				{"escaped permission key", func(nn *NodeSpec) {
					nn.TemplateData["extraLabels/a~b"] = apiextensionsv1.JSON{Raw: []byte(`true`)}
				}, state.allowData},
				{"interface name", func(nn *NodeSpec) { nn.NodeNetwork.Interfaces[0].Name = "eno3" }, state.allowInterfaces},
				{"interface addition", func(nn *NodeSpec) {
					nn.NodeNetwork.Interfaces = append(nn.NodeNetwork.Interfaces,
						&aiv1beta1.Interface{Name: "eno3", MacAddress: "00:11:22:33:44:88"})
				}, state.allowInterfaces},
				{"interface removal", func(nn *NodeSpec) { nn.NodeNetwork.Interfaces = nn.NodeNetwork.Interfaces[:1] }, state.allowInterfaces},
				{"MAC", func(nn *NodeSpec) { nn.NodeNetwork.Interfaces[0].MacAddress = "00:11:22:33:44:66" }, state.allowInterfaces},
			} {
				t.Run(change.name, func(t *testing.T) {
					g := NewWithT(t)
					updated := old.DeepCopy()
					if state.release {
						updated.Spec.HoldInstallation = false
					}
					if state.reinstall {
						updated.Spec.Reinstall = &ReinstallSpec{Generation: "reinstall-1"}
					}
					change.mutate(&updated.Spec.Nodes[0])
					_, err := v.ValidateUpdate(context.Background(), old, updated)
					if change.allowed {
						g.Expect(err).To(Succeed())
					} else {
						g.Expect(err).To(HaveOccurred())
					}
				})
			}
		})
	}
}

func TestReinstallInterfaceCompatibility(t *testing.T) {
	v := &clusterInstanceValidator{}
	for _, parameters := range []struct {
		name    string
		present bool
	}{{"parameters absent", false}, {"parameters present", true}} {
		for _, change := range []struct {
			name   string
			mutate func(*NodeSpec)
		}{
			{"rename", func(node *NodeSpec) { node.NodeNetwork.Interfaces[0].Name = "eno3" }},
			{"add", func(node *NodeSpec) {
				node.NodeNetwork.Interfaces = append(node.NodeNetwork.Interfaces,
					&aiv1beta1.Interface{Name: "eno3", MacAddress: "00:11:22:33:44:88"})
			}},
			{"remove", func(node *NodeSpec) { node.NodeNetwork.Interfaces = node.NodeNetwork.Interfaces[:1] }},
			{"MAC", func(node *NodeSpec) { node.NodeNetwork.Interfaces[0].MacAddress = "00:11:22:33:44:66" }},
		} {
			t.Run(parameters.name+"/"+change.name, func(t *testing.T) {
				g := NewWithT(t)
				old := networkValidationFixture()
				old.Status.Conditions = []metav1.Condition{{Type: string(ClusterProvisioned),
					Status: metav1.ConditionTrue, Reason: string(Completed)}}
				if !parameters.present {
					old.Spec.Nodes[0].TemplateData = nil
				}
				updated := old.DeepCopy()
				updated.Spec.Reinstall = &ReinstallSpec{Generation: "reinstall-1"}
				change.mutate(&updated.Spec.Nodes[0])
				_, err := v.ValidateUpdate(context.Background(), old, updated)
				g.Expect(err).To(Succeed(), "existing interface update permissions must be preserved")

				if updated.Spec.Nodes[0].TemplateData == nil {
					updated.Spec.Nodes[0].TemplateData = map[string]apiextensionsv1.JSON{}
				}
				updated.Spec.Nodes[0].TemplateData["extraLabels"] = apiextensionsv1.JSON{Raw: []byte(`{"foo":"new"}`)}
				_, err = v.ValidateUpdate(context.Background(), old, updated)
				g.Expect(err).To(MatchError(ContainSubstring("templateData")), "a permitted interface edit must not allow parameter changes")
			})
		}
	}
}

func TestParameterAdmissionWithoutNetwork(t *testing.T) {
	v := &clusterInstanceValidator{}
	for _, state := range []string{"held", "release hold", "completed", "new reinstall"} {
		for _, change := range []struct {
			name, before, after string
			unchanged           bool
		}{
			{"add map", `null`, `{"extraLabels":null}`, false},
			{"remove map", `{"extraLabels":null}`, `null`, false},
			{"delete null key", `{"extraLabels":null}`, `{}`, false},
			{"edit boolean", `{"extraLabels":false}`, `{"extraLabels":true}`, false},
			{"nested large integer", `{"extraLabels":{"ids":[9007199254740992]}}`,
				`{"extraLabels":{"ids":[9007199254740993]}}`, false},
			{"array order", `{"extraLabels":[1,2]}`, `{"extraLabels":[2,1]}`, false},
			{"object order and whitespace", `{"extraLabels":{"a":9007199254740993,"b":null}}`,
				`{"extraLabels": {"b": null, "a": 9007199254740993}}`, true},
			{"omitted empty map", `null`, `{}`, true},
		} {
			t.Run(state+"/"+change.name, func(t *testing.T) {
				g := NewWithT(t)
				old := networkValidationFixture()
				old.Spec.Nodes[0].NodeNetwork = nil
				old.Spec.Nodes[0].TemplateData = nil
				g.Expect(json.Unmarshal([]byte(change.before), &old.Spec.Nodes[0].TemplateData)).To(Succeed())
				old.Spec.HoldInstallation = state == "held" || state == "release hold"
				if !old.Spec.HoldInstallation {
					old.Status.Conditions = []metav1.Condition{{Type: string(ClusterProvisioned),
						Status: metav1.ConditionTrue, Reason: string(Completed)}}
				}
				updated := old.DeepCopy()
				updated.Spec.Nodes[0].TemplateData = nil
				g.Expect(json.Unmarshal([]byte(change.after), &updated.Spec.Nodes[0].TemplateData)).To(Succeed())
				if state == "release hold" {
					updated.Spec.HoldInstallation = false
				}
				if state == "new reinstall" {
					updated.Spec.Reinstall = &ReinstallSpec{Generation: "reinstall-1"}
				}
				// Legitimate label changes must still be accepted with equivalent parameters.
				updated.Spec.Nodes[0].ExtraLabels = map[string]map[string]string{"ConfigMap": {"example": "value"}}
				_, err := v.ValidateUpdate(context.Background(), old, updated)
				if old.Spec.HoldInstallation || change.unchanged {
					g.Expect(err).To(Succeed())
				} else {
					g.Expect(err).To(MatchError(ContainSubstring("templateData")))
				}
			})
		}
	}
}

func TestInvalidParameterComparisonFailsClosed(t *testing.T) {
	g := NewWithT(t)
	for _, invalidOld := range []bool{false, true} {
		old := networkValidationFixture().Spec.Nodes[0]
		updated := *old.DeepCopy()
		invalid := &updated
		if invalidOld {
			invalid = &old
		}
		// The API server normally excludes invalid JSON; typed callers must not
		// bypass immutability when comparison cannot marshal their parameters.
		invalid.TemplateData["extraLabels"] = apiextensionsv1.JSON{Raw: []byte(`invalid-json`)}
		g.Expect(getUnauthorizedChanges(old, updated, true)).To(ContainElement("templateData"))
	}
}

func TestNetworkParametersDuringScaleOut(t *testing.T) {
	g := NewWithT(t)
	old := networkValidationFixture()
	old.Status.Conditions = []metav1.Condition{{Type: string(ClusterProvisioned),
		Status: metav1.ConditionTrue, Reason: string(Completed)}}
	updated := old.DeepCopy()
	worker := updated.Spec.Nodes[0].DeepCopy()
	worker.HostName, worker.Role = "worker1", "worker"
	worker.TemplateData["ip"] = apiextensionsv1.JSON{Raw: []byte(`"192.0.2.20"`)}
	updated.Spec.Nodes = append([]NodeSpec{*worker}, updated.Spec.Nodes...)
	v := &clusterInstanceValidator{}
	_, err := v.ValidateUpdate(context.Background(), old, updated)
	g.Expect(err).To(Succeed())
	for _, key := range []string{"ip", "extraLabels", "bmcAddress", "rootDeviceHints", "nodeNetwork/interfaces"} {
		changed := updated.DeepCopy()
		changed.Spec.Nodes[1].TemplateData[key] = apiextensionsv1.JSON{Raw: []byte(`"192.0.2.30"`)}
		_, err = v.ValidateUpdate(context.Background(), old, changed)
		g.Expect(err).To(HaveOccurred(), "scale-out must not bypass immutability for key %q", key)
	}
}
