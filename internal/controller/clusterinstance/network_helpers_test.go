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

package clusterinstance

import (
	"bytes"
	"encoding/json"
	"testing"

	. "github.com/onsi/gomega"
	"github.com/stolostron/siteconfig/api/v1alpha1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

func TestTemplateVar(t *testing.T) {
	g := NewWithT(t)
	for _, tc := range []struct {
		name string
		raw  string
		want interface{}
	}{
		{"string", `"on"`, "on"}, {"boolean", `false`, false},
		{"zero", `0`, int64(0)}, {"large integer", `9007199254740993`, int64(9007199254740993)},
		{"int64 minimum", `-9223372036854775808`, int64(-9223372036854775808)},
		{"int64 maximum", `9223372036854775807`, int64(9223372036854775807)},
		{"beyond int64", `9223372036854775809`, float64(9223372036854775809)},
		{"fraction", `1.25`, float64(1.25)}, {"exponent", `1e3`, float64(1000)},
		{"null", `null`, nil}, {"empty string", `""`, ""},
		{"empty object", `{}`, map[string]interface{}{}}, {"empty array", `[]`, []interface{}{}},
		{"nested", `{"a":[9007199254740993,null,false,{"b":1.25}]}`,
			map[string]interface{}{"a": []interface{}{int64(9007199254740993), nil, false, map[string]interface{}{"b": float64(1.25)}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			var nn v1alpha1.NodeSpec
			// Exercise the same JSON null representation as a typed API read.
			g.Expect(json.Unmarshal([]byte(`{"templateData":{"key":`+tc.raw+`}}`), &nn)).To(Succeed())
			before := nn.DeepCopy()
			got, err := templateVar(nn, "key")
			g.Expect(err).To(Succeed())
			if tc.want == nil {
				g.Expect(got).To(BeNil())
			} else {
				g.Expect(got).To(Equal(tc.want))
			}
			g.Expect(hasTemplateVar(nn, "key")).To(BeTrue())
			g.Expect(&nn).To(Equal(before))
			// Exercise inline JSON through the actual renderer and its YAML parser.
			const source = `apiVersion: v1
kind: Example
metadata:
  name: parameter
spec:
  value: {{ templateVar .SpecialVars.CurrentNode "key" | toJson }}`
			docs, err := NewTemplateEngine().render("parameter", source,
				&ClusterData{SpecialVars: SpecialVars{CurrentNode: nn}})
			g.Expect(err).To(Succeed())
			g.Expect(docs).To(HaveLen(1))
			value := docs[0].manifest["spec"].(map[string]interface{})["value"]
			encoded, err := json.Marshal(value)
			g.Expect(err).To(Succeed())
			// Out-of-range integers render as the JSON encoding of their float64 value.
			expected, err := json.Marshal(tc.want)
			g.Expect(err).To(Succeed())
			g.Expect(encoded).To(Equal(expected))
		})
	}
	for _, tc := range []struct {
		node    v1alpha1.NodeSpec
		message string
	}{
		{v1alpha1.NodeSpec{}, "templateData is not defined"},
		{v1alpha1.NodeSpec{TemplateData: map[string]apiextensionsv1.JSON{}}, `templateData key "missing" is missing`},
	} {
		_, err := templateVar(tc.node, "missing")
		g.Expect(err).To(MatchError(tc.message))
		g.Expect(hasTemplateVar(tc.node, "missing")).To(BeFalse())
	}
	for _, raw := range []string{`{`, `true false`, `42 trailing`, ` `} {
		nn := v1alpha1.NodeSpec{TemplateData: map[string]apiextensionsv1.JSON{"bad": {Raw: []byte(raw)}}}
		_, err := templateVar(nn, "bad")
		g.Expect(err).To(MatchError(ContainSubstring(`key "bad" contains invalid JSON`)))
		g.Expect(hasTemplateVar(nn, "bad")).To(BeTrue())
	}
	for _, raw := range [][]byte{nil, {}, []byte("null")} {
		nn := v1alpha1.NodeSpec{TemplateData: map[string]apiextensionsv1.JSON{"": {Raw: raw}}}
		value, err := templateVar(nn, "")
		g.Expect(err).To(Succeed())
		g.Expect(value).To(BeNil())
		g.Expect(hasTemplateVar(nn, "")).To(BeTrue())
	}
}

func TestNetworkHelpersInTemplates(t *testing.T) {
	g := NewWithT(t)
	var nn v1alpha1.NodeSpec
	g.Expect(json.Unmarshal([]byte(`{"templateData":{"number":9007199254740993,`+
		`"prefix":24,"text":"on","null":null,"false":false,"nested":{"a":[1,null]}}}`), &nn)).To(Succeed())
	before := nn.DeepCopy()
	tmpl, err := ParseTemplate("parameters", `{{templateVar . "number" | toJson}}|`+
		`{{templateVar . "prefix" | int}}|{{templateVar . "text" | quote}}|`+
		`{{if hasTemplateVar . "null"}}{{templateVar . "null" | toJson}}{{end}}|`+
		`{{templateVar . "null" | toYaml}}|`+
		`{{if hasTemplateVar . "false"}}{{templateVar . "false"}}{{end}}|`+
		`{{templateVar . "nested" | toYaml | nindent 2}}|`+
		`{{if hasTemplateVar . "optional"}}{{templateVar . "optional"}}{{else}}fallback{{end}}`)
	g.Expect(err).To(Succeed())
	var output bytes.Buffer
	g.Expect(tmpl.Execute(&output, nn)).To(Succeed())
	g.Expect(output.String()).To(Equal("9007199254740993|24|\"on\"|null|null|false|\n  a:\n  - 1\n  - null|fallback"))
	g.Expect(&nn).To(Equal(before))
	tmpl, err = ParseTemplate("NMStateConfig", `{{templateVar . "requiredIP"}}`)
	g.Expect(err).To(Succeed())
	err = tmpl.Execute(&output, nn)
	g.Expect(err).To(MatchError(ContainSubstring("NMStateConfig")))
	g.Expect(err).To(MatchError(ContainSubstring("requiredIP")))
}

func TestExistingTemplateFunctionsRemainAvailable(t *testing.T) {
	g := NewWithT(t)
	// These functions exist in slim-sprig v2 and must remain available.
	for _, name := range []string{"merge", "mergeOverwrite", "semverCompare", "toJson", "toYaml"} {
		g.Expect(funcMap()).To(HaveKey(name))
	}
}

func TestTemplateNumberConversions(t *testing.T) {
	for _, tc := range []struct {
		name, raw, source, want, errorText string
	}{
		{"direct add", `24`, `{{add (templateVar . "n") 1}}`, "25", ""},
		{"direct comparison", `24`, `{{eq (templateVar . "n") 24}}`, "true", ""},
		{"direct greater than", `24`, `{{gt (templateVar . "n") 1}}`, "true", ""},
		{"large integer add", `9007199254740993`, `{{add (templateVar . "n") 1}}`, "9007199254740994", ""},
		{"int64 maximum", `9223372036854775807`, `{{templateVar . "n" | toJson}}`, "9223372036854775807", ""},
		{"beyond int64", `9223372036854775809`, `{{templateVar . "n" | toJson}}`, "9223372036854776000", ""},
		{"decimal form", `24.0`, `{{templateVar . "n" | int}}`, "24", ""},
		{"exponent form", `1e3`, `{{templateVar . "n" | int64}}`, "1000", ""},
		{"fraction comparison", `1.25`, `{{eq (templateVar . "n") 1.25}}`, "true", ""},
		{"mixed comparison", `1.25`, `{{gt (templateVar . "n") 1}}`, "", "incompatible types for comparison"},
		{"zero truthiness", `0`, `{{if templateVar . "n"}}true{{else}}false{{end}}`, "false", ""},
		{"zero default", `0`, `{{templateVar . "n" | default 99}}`, "99", ""},
		{"zero presence", `0`, `{{if hasTemplateVar . "n"}}{{templateVar . "n"}}{{else}}99{{end}}`, "0", ""},
		{"false default", `false`, `{{templateVar . "n" | default true}}`, "true", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			var node v1alpha1.NodeSpec
			g.Expect(json.Unmarshal([]byte(`{"templateData":{"n":`+tc.raw+`}}`), &node)).To(Succeed())
			tmpl, err := ParseTemplate(tc.name, tc.source)
			g.Expect(err).To(Succeed())
			var output bytes.Buffer
			err = tmpl.Execute(&output, node)
			if tc.errorText != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tc.errorText)))
			} else {
				g.Expect(err).To(Succeed())
				g.Expect(output.String()).To(Equal(tc.want))
			}
		})
	}
}

func TestTemplateRouteSwitch(t *testing.T) {
	const source = `{{ $emit := true }}` +
		`{{ if hasTemplateVar . "emitDefaultRoute" }}{{ $emit = templateVar . "emitDefaultRoute" }}{{ end }}` +
		`{{ if $emit }}next-hop-address: {{ templateVar . "gateway" | quote }}{{ end }}`
	for _, tc := range []struct{ name, parameters, want string }{
		{"default", `{"gateway":"192.0.2.1"}`, `next-hop-address: "192.0.2.1"`},
		{"omit route", `{"emitDefaultRoute":false}`, ""},
		{"explicit gateway", `{"emitDefaultRoute":true,"gateway":"192.0.2.254"}`, `next-hop-address: "192.0.2.254"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			var node v1alpha1.NodeSpec
			g.Expect(json.Unmarshal([]byte(`{"templateData":`+tc.parameters+`}`), &node)).To(Succeed())
			tmpl, err := ParseTemplate("gateway", source)
			g.Expect(err).To(Succeed())
			var output bytes.Buffer
			g.Expect(tmpl.Execute(&output, node)).To(Succeed())
			g.Expect(output.String()).To(Equal(tc.want))
		})
	}
}
