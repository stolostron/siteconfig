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
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/gomega"
	aiv1beta1 "github.com/openshift/assisted-service/api/v1beta1"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	"github.com/stolostron/siteconfig/api/v1alpha1"
	ai "github.com/stolostron/siteconfig/internal/templates/assisted-installer"
	hcp "github.com/stolostron/siteconfig/internal/templates/hosted-cluster"
	ibi "github.com/stolostron/siteconfig/internal/templates/image-based-installer"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/yaml"
)

func TestDefaultNetworkTemplates(t *testing.T) {
	const config = `{"interfaces":[{"name":"eno1","type":"ethernet","ipv4":{"enabled":true}}]}`
	for _, tc := range []struct {
		name, source string
		secret       bool
	}{
		{"AI", ai.NMStateConfig, false}, {"HCP", hcp.NMStateConfig, false}, {"IBI", ibi.NetworkSecret, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			for _, hostRef := range []*v1alpha1.HostRef{nil, {Name: "existing-host", Namespace: "hosts"}} {
				data := &ClusterData{Spec: v1alpha1.ClusterInstanceSpec{ClusterName: "cluster"},
					SpecialVars: SpecialVars{CurrentNode: v1alpha1.NodeSpec{HostName: "node1", HostRef: hostRef}}}
				engine := NewTemplateEngine()
				got, err := engine.render(tc.name, tc.source, data)
				g.Expect(err).To(Succeed())
				g.Expect(got).To(BeNil())
				data.SpecialVars.CurrentNode.TemplateData = map[string]apiextensionsv1.JSON{"unused": {Raw: []byte(`null`)}}
				got, err = engine.render(tc.name, tc.source, data)
				g.Expect(err).To(Succeed())
				g.Expect(got).To(BeNil(), "parameters alone must not make default templates emit a network resource")
				data.SpecialVars.CurrentNode.TemplateData = nil
				nn := &aiv1beta1.NMStateConfigSpec{}
				g.Expect(json.Unmarshal([]byte(`{"config":`+config+`,"interfaces":[`+
					`{"name":"eno1","macAddress":"00:11:22:33:44:55"}]}`), nn)).To(Succeed())
				data.SpecialVars.CurrentNode.NodeNetwork = nn
				baseline, err := engine.render(tc.name, tc.source, data)
				g.Expect(err).To(Succeed())
				data.SpecialVars.CurrentNode.TemplateData = map[string]apiextensionsv1.JSON{"unused": {Raw: []byte(`{"nested":null}`)}}
				got, err = engine.render(tc.name, tc.source, data)
				g.Expect(err).To(Succeed())
				g.Expect(got).To(Equal(baseline))
				object, err := NewRenderedObject(got)
				g.Expect(err).To(Succeed())
				name, namespace := "node1", "cluster"
				if hostRef != nil {
					name, namespace = hostRef.Name, hostRef.Namespace
				}
				g.Expect(object.GetName()).To(Equal(name))
				g.Expect(object.GetNamespace()).To(Equal(namespace))
				g.Expect(object.object.GetAnnotations()[WaveAnnotation]).To(Equal("1"))
				if tc.secret {
					encoded := got["data"].(map[string]interface{})["nmstate"].(string)
					decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
					g.Expect(err).To(Succeed())
					content, err := yaml.YAMLToJSON(decoded)
					g.Expect(err).To(Succeed())
					g.Expect(string(content)).To(MatchJSON(config))
				} else {
					encoded, err := json.Marshal(got["spec"])
					g.Expect(err).To(Succeed())
					var spec aiv1beta1.NMStateConfigSpec
					g.Expect(json.Unmarshal(encoded, &spec)).To(Succeed())
					g.Expect(spec.Interfaces).To(Equal(nn.Interfaces))
					g.Expect(string(spec.NetConfig.Raw)).To(MatchYAML(string(nn.NetConfig.Raw)))
					g.Expect(object.object.GetLabels()["nmstate-label"]).To(Equal(name))
				}
			}
		})
	}
}

func TestNodeParametersWithoutNetwork(t *testing.T) {
	g := NewWithT(t)
	const source = `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .SpecialVars.CurrentNode.HostName }}
  namespace: {{ .Spec.ClusterName }}
data:
  value: {{ templateVar .SpecialVars.CurrentNode "setting" | quote }}`
	ci := v1alpha1.ClusterInstance{Spec: v1alpha1.ClusterInstanceSpec{
		ClusterName: "cluster", ClusterImageSetNameRef: "image",
		Nodes: []v1alpha1.NodeSpec{
			{HostName: "node1", TemplateData: map[string]apiextensionsv1.JSON{"setting": {Raw: []byte(`"first"`)}}},
			{HostName: "node2", TemplateData: map[string]apiextensionsv1.JSON{"setting": {Raw: []byte(`"second"`)}}},
		},
	}}
	for i := range ci.Spec.Nodes {
		ci.Spec.Nodes[i].TemplateRefs = []v1alpha1.TemplateRef{{Name: "shared", Namespace: "templates"}}
	}
	scheme := runtime.NewScheme()
	g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
	g.Expect(hivev1.AddToScheme(scheme)).To(Succeed())
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(
		GetMockClusterImageSet("image", "release"),
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "shared", Namespace: "templates"},
			Data: map[string]string{"Settings": source}},
	).Build()
	objects, err := NewTemplateEngine().ProcessTemplates(context.Background(), c, zap.NewNop(), ci)
	g.Expect(err).To(Succeed())
	values := map[string]interface{}{}
	for _, object := range objects.GetRenderObjects() {
		values[object.GetName()] = object.object.Object["data"].(map[string]interface{})["value"]
	}
	g.Expect(values).To(Equal(map[string]interface{}{"node1": "first", "node2": "second"}))
}

func TestTemplateErrorIdentifiesConfigMapWithDuplicateKey(t *testing.T) {
	g := NewWithT(t)
	scheme := runtime.NewScheme()
	g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
	g.Expect(hivev1.AddToScheme(scheme)).To(Succeed())
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(
		GetMockClusterImageSet("image", "release"),
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "shared", Namespace: "first"},
			Data: map[string]string{"Settings": " "}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "shared", Namespace: "second"},
			Data: map[string]string{"Settings": `{{ templateVar .SpecialVars.CurrentNode "required" }}`}},
	).Build()
	refs := []v1alpha1.TemplateRef{{Name: "shared", Namespace: "first"}, {Name: "shared", Namespace: "second"}}
	for _, nodeLevel := range []bool{true, false} {
		ci := v1alpha1.ClusterInstance{Spec: v1alpha1.ClusterInstanceSpec{ClusterImageSetNameRef: "image"}}
		if nodeLevel {
			ci.Spec.Nodes = []v1alpha1.NodeSpec{{HostName: "node1", TemplateRefs: refs}}
		} else {
			ci.Spec.TemplateRefs = refs
		}
		_, err := NewTemplateEngine().ProcessTemplates(context.Background(), c, zap.NewNop(), ci)
		g.Expect(err).To(MatchError(ContainSubstring(`template ConfigMap "second/shared", key "Settings"`)))
		g.Expect(err).To(MatchError(ContainSubstring("templateData is not defined")))
		g.Expect(err.Error()).NotTo(ContainSubstring("first/shared"))
		if nodeLevel {
			g.Expect(err).To(MatchError(ContainSubstring(`node "node1"`)))
		} else {
			g.Expect(err.Error()).NotTo(ContainSubstring(`node ""`))
		}
	}
}

func TestTemplateErrorOrder(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	scheme := runtime.NewScheme()
	g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
	g.Expect(hivev1.AddToScheme(scheme)).To(Succeed())
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "shared", Namespace: "templates"},
		Data: map[string]string{"B": `{{fail "failure-B"}}`, "A": `{{fail "failure-A"}}`}}
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(
		GetMockClusterImageSet("image", "release"), cm).Build()
	ci := v1alpha1.ClusterInstance{Spec: v1alpha1.ClusterInstanceSpec{ClusterImageSetNameRef: "image",
		Nodes: []v1alpha1.NodeSpec{{HostName: "node1", TemplateRefs: []v1alpha1.TemplateRef{{Name: cm.Name, Namespace: cm.Namespace}}}}}}
	for _, key := range []string{"A", "B"} {
		for retry := 0; retry < 100; retry++ {
			_, err := NewTemplateEngine().ProcessTemplates(ctx, c, zap.NewNop(), ci)
			g.Expect(err).To(MatchError(ContainSubstring(`key "` + key + `"`)))
			if key == "A" {
				g.Expect(err.Error()).NotTo(ContainSubstring("failure-B"))
			}
		}
		g.Expect(c.Get(ctx, client.ObjectKeyFromObject(cm), cm)).To(Succeed())
		cm.Data[key] = " "
		g.Expect(c.Update(ctx, cm)).To(Succeed())
	}
	_, err := NewTemplateEngine().ProcessTemplates(ctx, c, zap.NewNop(), ci)
	g.Expect(err).To(Succeed())
}

func TestStructuredNetworkParameters(t *testing.T) {
	g := NewWithT(t)
	// Match the structured example in the user guide.
	const input = `templateData:
  dnsServers: ["192.0.2.53", "192.0.2.54"]
  addresses:
  - ip: "192.0.2.10"
    prefix-length: 24
  - ip: "192.0.2.11"
    prefix-length: 24
  ipv4Enabled: true
  dhcp: false
`
	const source = `{{- $node := .SpecialVars.CurrentNode }}
apiVersion: agent-install.openshift.io/v1beta1
kind: NMStateConfig
metadata:
  name: structured
spec:
  config:
    dns-resolver:
      config:
        server: {{ templateVar $node "dnsServers" | toJson }}
    interfaces:
    - name: eno1
      type: ethernet
      state: up
      ipv4:
        enabled: {{ templateVar $node "ipv4Enabled" | toJson }}
        dhcp: {{ templateVar $node "dhcp" | toJson }}
        address: {{ templateVar $node "addresses" | toJson }}
`
	var node v1alpha1.NodeSpec
	g.Expect(yaml.Unmarshal([]byte(input), &node)).To(Succeed())
	manifest, err := NewTemplateEngine().render("structured", source, &ClusterData{SpecialVars: SpecialVars{CurrentNode: node}})
	g.Expect(err).To(Succeed())
	encoded, err := json.Marshal(manifest["spec"])
	g.Expect(err).To(Succeed())
	g.Expect(string(encoded)).To(MatchJSON(`{"config":{
		"dns-resolver":{"config":{"server":["192.0.2.53","192.0.2.54"]}},
		"interfaces":[{"name":"eno1","type":"ethernet","state":"up","ipv4":{
			"enabled":true,"dhcp":false,"address":[
				{"ip":"192.0.2.10","prefix-length":24},{"ip":"192.0.2.11","prefix-length":24}
			]}}]}}`))
}

func TestCustomNetworkSample(t *testing.T) {
	g := NewWithT(t)
	sampleDir := filepath.Join("..", "..", "..", "config", "samples", "assisted-installer", "custom-node-network")
	read := func(name string) []byte {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(sampleDir, name))
		g.Expect(err).To(Succeed())
		return b
	}
	var ci v1alpha1.ClusterInstance
	g.Expect(yaml.Unmarshal(read("clusterinstance.yaml"), &ci)).To(Succeed())
	source := string(read("NMStateConfig.yaml.tmpl"))
	engine := NewTemplateEngine()
	data := &ClusterData{Spec: ci.Spec, SpecialVars: SpecialVars{CurrentNode: ci.Spec.Nodes[0]}}
	rendered, err := engine.render("NMStateConfig", source, data)
	g.Expect(err).To(Succeed())
	encoded, err := yaml.Marshal(rendered)
	g.Expect(err).To(Succeed())
	g.Expect(string(encoded)).To(MatchYAML(string(read("expected-nmstateconfig.yaml"))))

	// Process a complete custom bundle for two nodes, with different IP/MAC/HostRef values.
	worker := ci.Spec.Nodes[0].DeepCopy()
	worker.HostName, worker.Role = "node2.example.com", "worker"
	worker.HostRef = &v1alpha1.HostRef{Name: "existing-worker", Namespace: "hosts"}
	worker.TemplateData["nodeIp"] = apiextensionsv1.JSON{Raw: []byte(`"192.0.2.20"`)}
	worker.NodeNetwork.Interfaces[0].MacAddress = "00:11:22:33:44:66"
	ci.Spec.Nodes = append(ci.Spec.Nodes, *worker)
	// Only node resources are needed to verify the sample bundle.
	ci.Spec.TemplateRefs = nil
	before := ci.DeepCopy()
	bundle := ai.GetNodeTemplates()
	bundle["NMStateConfig"] = source
	ref := ci.Spec.Nodes[0].TemplateRefs[0]
	scheme := runtime.NewScheme()
	g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
	g.Expect(hivev1.AddToScheme(scheme)).To(Succeed())
	c := fakeclient.NewClientBuilder().WithScheme(scheme).WithObjects(
		GetMockClusterImageSet(ci.Spec.ClusterImageSetNameRef, "test-release"),
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: ref.Name, Namespace: ref.Namespace}, Data: bundle},
	).Build()
	objects, err := engine.ProcessTemplates(context.Background(), c, zap.NewNop(), ci)
	g.Expect(err).To(Succeed())
	g.Expect(&ci).To(Equal(before))
	counts := map[string]int{}
	networks := map[string]RenderedObject{}
	infraEnvs := map[string]RenderedObject{}
	for _, object := range objects.GetRenderObjects() {
		counts[object.GetKind()]++
		switch object.GetKind() {
		case "NMStateConfig":
			networks[object.GetName()] = object
		case "InfraEnv":
			infraEnvs[object.GetName()] = object
		}
	}
	g.Expect(counts).To(Equal(map[string]int{"NMStateConfig": 2, "InfraEnv": 2, "BareMetalHost": 2}))
	for i, name := range []string{"node1.example.com", "existing-worker"} {
		network := networks[name]
		infra := infraEnvs[name]
		g.Expect(infra.GetNamespace()).To(Equal(network.GetNamespace()))
		var spec aiv1beta1.NMStateConfigSpec
		encoded, err := json.Marshal(network.object.Object["spec"])
		g.Expect(err).To(Succeed())
		g.Expect(json.Unmarshal(encoded, &spec)).To(Succeed())
		g.Expect(spec.Interfaces).To(Equal(ci.Spec.Nodes[i].NodeNetwork.Interfaces))
		ip, err := templateVar(ci.Spec.Nodes[i], "nodeIp")
		g.Expect(err).To(Succeed())
		g.Expect(ip).To(BeAssignableToTypeOf(""))
		g.Expect(string(spec.NetConfig.Raw)).To(ContainSubstring(ip.(string)))
		infraSpec := infra.object.Object["spec"].(map[string]interface{})
		selector := infraSpec["nmStateConfigLabelSelector"].(map[string]interface{})["matchLabels"]
		g.Expect(selector).To(Equal(map[string]interface{}{"nmstate-label": name}))
		g.Expect(network.object.GetLabels()["nmstate-label"]).To(Equal(name))
		g.Expect(network.object.GetAnnotations()[WaveAnnotation]).To(Equal("1"))
	}
}
