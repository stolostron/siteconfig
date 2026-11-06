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
	"encoding/json"
	"testing"

	. "github.com/onsi/gomega"
	aiv1beta1 "github.com/openshift/assisted-service/api/v1beta1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

func TestNodeNetworkJSONCompatibility(t *testing.T) {
	for _, raw := range []string{
		`{}`, `null`,
		`{"interfaces":[{"name":"eno1","macAddress":"00:11:22:33:44:55"}]}`,
		`{"config":{"interfaces":[{"name":"eno1","ipv4":{"enabled":true}}]}}`,
		`{"interfaces":[{"name":"eno1","macAddress":"00:11:22:33:44:55"}],"config":{"routes":{"config":[]}}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			g := NewWithT(t)
			var upstream *aiv1beta1.NMStateConfigSpec
			var node NodeSpec
			g.Expect(json.Unmarshal([]byte(raw), &upstream)).To(Succeed())
			g.Expect(json.Unmarshal([]byte(`{"nodeNetwork":`+raw+`,"templateData":{"unused":null}}`), &node)).To(Succeed())
			g.Expect(node.NodeNetwork).To(Equal(upstream))
			want, err := json.Marshal(upstream)
			g.Expect(err).To(Succeed())
			got, err := json.Marshal(node.NodeNetwork)
			g.Expect(err).To(Succeed())
			g.Expect(string(got)).To(MatchJSON(string(want)))
		})
	}
}

func TestNodeParametersRoundTripAndDeepCopy(t *testing.T) {
	g := NewWithT(t)
	const parameters = `{"text":"on","number":9007199254740993,"fraction":1.25,"bool":false,"null":null,` +
		`"empty":"","array":[null,0,{"nested.key":[true,"x"]}],"object":{},"":[]}`
	for _, network := range []string{"", `,"nodeNetwork":{"interfaces":[` +
		`{"name":"eno1","macAddress":"00:11:22:33:44:55"}],"config":{"foo":"bar"},"unknownSibling":42}`} {
		t.Run(network, func(t *testing.T) {
			g := NewWithT(t)
			var node NodeSpec
			g.Expect(json.Unmarshal([]byte(`{"templateData":`+parameters+network+`}`), &node)).To(Succeed())
			got, err := json.Marshal(node.TemplateData)
			g.Expect(err).To(Succeed())
			g.Expect(string(got)).To(MatchJSON(parameters))
			g.Expect(node.TemplateData).To(HaveKey("null"))
			g.Expect(node.TemplateData["null"].Raw).To(BeEmpty())
			g.Expect(string(node.TemplateData["number"].Raw)).To(Equal("9007199254740993"))
			encoded, err := json.Marshal(node)
			g.Expect(err).To(Succeed())
			g.Expect(string(encoded)).NotTo(ContainSubstring("unknownSibling"))
			var roundTrip NodeSpec
			g.Expect(json.Unmarshal(encoded, &roundTrip)).To(Succeed())
			g.Expect(roundTrip).To(Equal(node))

			copy := node.DeepCopy()
			copy.TemplateData["text"].Raw[1] = 'X'
			copy.TemplateData["array"].Raw[1] = '0'
			copy.TemplateData["added"] = apiextensionsv1.JSON{Raw: []byte(`true`)}
			delete(copy.TemplateData, "null")
			if network != "" {
				copy.NodeNetwork.Interfaces[0].Name = "eno2"
				copy.NodeNetwork.NetConfig.Raw[0] = 'X'
			} else {
				g.Expect(copy.NodeNetwork).To(BeNil())
			}
			unchanged, err := json.Marshal(node)
			g.Expect(err).To(Succeed())
			g.Expect(string(unchanged)).To(Equal(string(encoded)))
		})
	}
	for _, parameters := range []map[string]apiextensionsv1.JSON{nil, {}} {
		node := &NodeSpec{TemplateData: parameters}
		g.Expect(node.DeepCopy()).To(Equal(node))
		encoded, err := json.Marshal(node)
		g.Expect(err).To(Succeed())
		g.Expect(string(encoded)).NotTo(ContainSubstring("templateData"))
	}
	var nilNode *NodeSpec
	g.Expect(nilNode.DeepCopy()).To(BeNil())
}
