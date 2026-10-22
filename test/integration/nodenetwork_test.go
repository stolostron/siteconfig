//go:build integration

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

package integration

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"
	aiv1beta1 "github.com/openshift/assisted-service/api/v1beta1"
	"github.com/stolostron/siteconfig/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// This suite tests generated schema behavior; lifecycle admission has separate webhook tests.
func TestNodeTemplateDataSchema(t *testing.T) {
	g := NewWithT(t)
	env := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}
	cfg, err := env.Start()
	g.Expect(err).To(Succeed())
	t.Cleanup(func() { g.Expect(env.Stop()).To(Succeed()) })
	scheme := runtime.NewScheme()
	g.Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
	g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	g.Expect(err).To(Succeed())
	ctx := context.Background()
	g.Expect(c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "network-test"}})).To(Succeed())

	for _, withNetwork := range []bool{false, true} {
		name := "without-network"
		if withNetwork {
			name = "with-network"
		}
		t.Run("typed-and-unstructured-roundtrip-"+name, func(t *testing.T) {
			g := NewWithT(t)
			ci := schemaClusterInstance(name)
			const params = `{"text":"on","zero":0,"large":9007199254740993,"fraction":1.25,"false":false,"null":null,"empty":"","object":{},"array":[null,{"x.y":[true,2]}],"":[],"a/b~c":{"nested":null}}`
			g.Expect(json.Unmarshal([]byte(params), &ci.Spec.Nodes[0].TemplateData)).To(Succeed())
			if withNetwork {
				ci.Spec.Nodes[0].NodeNetwork = &aiv1beta1.NMStateConfigSpec{}
				g.Expect(json.Unmarshal([]byte(`{"interfaces":[{"name":"eno1","macAddress":"02:00:00:00:00:01"}],"config":{"interfaces":[{"name":"eno1","state":"up"}]}}`), ci.Spec.Nodes[0].NodeNetwork)).To(Succeed())
			}
			networkJSON, err := json.Marshal(ci.Spec.Nodes[0].NodeNetwork)
			g.Expect(err).To(Succeed())
			g.Expect(c.Create(ctx, ci)).To(Succeed())
			check := func(want string) {
				t.Helper()
				got := &v1alpha1.ClusterInstance{}
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(ci), got)).To(Succeed())
				data := got.Spec.Nodes[0].TemplateData
				encoded, err := json.Marshal(data)
				g.Expect(err).To(Succeed())
				g.Expect(string(encoded)).To(MatchJSON(want))
				g.Expect(data).To(HaveKey("null"))
				g.Expect(data["null"].Raw).To(BeEmpty())
				g.Expect(string(data["large"].Raw)).To(Equal("9007199254740993"))
				encoded, err = json.Marshal(got.Spec.Nodes[0].NodeNetwork)
				g.Expect(err).To(Succeed())
				g.Expect(string(encoded)).To(MatchJSON(string(networkJSON)))
				u := &unstructured.Unstructured{}
				u.SetGroupVersionKind(v1alpha1.GroupVersion.WithKind("ClusterInstance"))
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(ci), u)).To(Succeed())
				nodes, found, err := unstructured.NestedSlice(u.Object, "spec", "nodes")
				g.Expect(err).To(Succeed())
				g.Expect(found).To(BeTrue())
				node := nodes[0].(map[string]interface{})
				raw := node["templateData"].(map[string]interface{})
				g.Expect(raw).To(HaveKey("null"))
				g.Expect(raw["null"]).To(BeNil())
				g.Expect(raw["large"]).To(Equal(int64(9007199254740993)))
				if !withNetwork {
					g.Expect(node).NotTo(HaveKey("nodeNetwork"))
				}
				encoded, err = json.Marshal(raw)
				g.Expect(err).To(Succeed())
				g.Expect(string(encoded)).To(MatchJSON(want))
			}
			check(params)
			ci.Spec.Nodes[0].TemplateData["added"] = apiextensionsv1.JSON{Raw: []byte(`{"nested":[null,false,0]}`)}
			g.Expect(c.Update(ctx, ci)).To(Succeed())
			check(params[:len(params)-1] + `,"added":{"nested":[null,false,0]}}`)
			delete(ci.Spec.Nodes[0].TemplateData, "added")
			ci.Spec.Nodes[0].TemplateData["text"] = apiextensionsv1.JSON{Raw: []byte(`"changed"`)}
			g.Expect(c.Update(ctx, ci)).To(Succeed())
			var changed map[string]json.RawMessage
			g.Expect(json.Unmarshal([]byte(params), &changed)).To(Succeed())
			changed["text"] = json.RawMessage(`"changed"`)
			encoded, err := json.Marshal(changed)
			g.Expect(err).To(Succeed())
			check(string(encoded))
		})
	}

	cases := []struct {
		name, patch string
		valid       bool
	}{
		{"omitted", `{}`, true},
		{"empty-map", `{"templateData":{}}`, true},
		{"parameters-only", `{"templateData":{"value":[null,false,{},[]]}}`, true},
		{"null-network", `{"nodeNetwork":null,"templateData":{"value":null}}`, true},
		{"empty-network", `{"nodeNetwork":{},"templateData":{"value":null}}`, true},
		{"inline", `{"nodeNetwork":{"interfaces":[{"name":"eno1","macAddress":"02:00:00:00:00:01"}],"config":{"nested":{"custom":true}}}}`, true},
		{"string-map", `{"templateData":"invalid"}`, false},
		{"number-map", `{"templateData":42}`, false},
		{"boolean-map", `{"templateData":true}`, false},
		{"array-map", `{"templateData":[]}`, false},
		{"empty-interfaces", `{"nodeNetwork":{"interfaces":[]}}`, false},
		{"invalid-mac", `{"nodeNetwork":{"interfaces":[{"name":"eno1","macAddress":"invalid"}]}}`, false},
		{"missing-mac", `{"nodeNetwork":{"interfaces":[{"name":"eno1"}]}}`, false},
		{"missing-name", `{"nodeNetwork":{"interfaces":[{"macAddress":"02:00:00:00:00:01"}]}}`, false},
		{"scalar-config", `{"nodeNetwork":{"config":"invalid"}}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			u := schemaUnstructured(t, tc.name, tc.patch)
			err := c.Create(ctx, u)
			if !tc.valid {
				g.Expect(apierrors.IsInvalid(err)).To(BeTrue(), "expected rejection, got %v", err)
				return
			}
			g.Expect(err).To(Succeed())
			got := &v1alpha1.ClusterInstance{}
			g.Expect(c.Get(ctx, client.ObjectKeyFromObject(u), got)).To(Succeed())
		})
	}
	t.Run("unknown-node-and-network-siblings-pruned", func(t *testing.T) {
		g := NewWithT(t)
		u := schemaUnstructured(t, "pruning", `{"unknownNode":1,"templateData":{"null":null,"object":{"unknown":1}},"nodeNetwork":{"unknownNetwork":2,"templateData":{"wrongLocation":true}}}`)
		g.Expect(c.Create(ctx, u, client.FieldValidation("Ignore"))).To(Succeed())
		g.Expect(c.Get(ctx, client.ObjectKeyFromObject(u), u)).To(Succeed())
		nodes, _, err := unstructured.NestedSlice(u.Object, "spec", "nodes")
		g.Expect(err).To(Succeed())
		node := nodes[0].(map[string]interface{})
		g.Expect(node).NotTo(HaveKey("unknownNode"))
		data := node["templateData"].(map[string]interface{})
		g.Expect(data).To(HaveKey("null"))
		g.Expect(data["null"]).To(BeNil())
		g.Expect(data["object"].(map[string]interface{})["unknown"]).To(Equal(int64(1)))
		network := node["nodeNetwork"].(map[string]interface{})
		g.Expect(network).NotTo(HaveKey("unknownNetwork"))
		g.Expect(network).NotTo(HaveKey("templateData"))
	})
	for _, tc := range []struct{ name, patch, field string }{
		{"strict-node", `{"unknownNode":1}`, "unknownNode"},
		{"strict-network", `{"nodeNetwork":{"unknownNetwork":1}}`, "unknownNetwork"},
		{"strict-old-location", `{"nodeNetwork":{"templateData":{"wrongLocation":true}}}`, "templateData"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			u := schemaUnstructured(t, tc.name, tc.patch)
			g.Expect(c.Create(ctx, u, client.FieldValidation("Strict"))).To(MatchError(ContainSubstring(tc.field)))
		})
	}
}

func schemaClusterInstance(name string) *v1alpha1.ClusterInstance {
	return &v1alpha1.ClusterInstance{
		TypeMeta:   metav1.TypeMeta{APIVersion: v1alpha1.GroupVersion.String(), Kind: "ClusterInstance"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "network-test"},
		Spec: v1alpha1.ClusterInstanceSpec{
			ClusterName: "network-test", BaseDomain: "example.com", ClusterImageSetNameRef: "test-image",
			PullSecretRef: corev1.LocalObjectReference{Name: "pull-secret"}, HoldInstallation: true,
			TemplateRefs: []v1alpha1.TemplateRef{{Name: "cluster", Namespace: "network-test"}},
			Nodes: []v1alpha1.NodeSpec{{HostName: "node1", Role: "master", BmcAddress: "redfish://example.com",
				BmcCredentialsName: v1alpha1.BmcCredentialsName{Name: "bmc-secret"},
				BootMACAddress:     "02:00:00:00:00:01",
				TemplateRefs:       []v1alpha1.TemplateRef{{Name: "node", Namespace: "network-test"}},
			}},
		},
	}
}

func schemaUnstructured(t *testing.T, name, patch string) *unstructured.Unstructured {
	g := NewWithT(t)
	t.Helper()
	encoded, err := json.Marshal(schemaClusterInstance(name))
	g.Expect(err).To(Succeed())
	u := &unstructured.Unstructured{}
	g.Expect(u.UnmarshalJSON(encoded)).To(Succeed())
	extra := &unstructured.Unstructured{}
	g.Expect(extra.UnmarshalJSON([]byte(`{"apiVersion":"v1","kind":"Test","patch":` + patch + `}`))).To(Succeed())
	nodes, _, err := unstructured.NestedSlice(u.Object, "spec", "nodes")
	g.Expect(err).To(Succeed())
	for key, value := range extra.Object["patch"].(map[string]interface{}) {
		nodes[0].(map[string]interface{})[key] = value
	}
	g.Expect(unstructured.SetNestedSlice(u.Object, nodes, "spec", "nodes")).To(Succeed())
	return u
}
