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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	. "github.com/onsi/gomega"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	"github.com/stolostron/siteconfig/api/v1alpha1"
	ai_templates "github.com/stolostron/siteconfig/internal/templates/assisted-installer"
	hcp_templates "github.com/stolostron/siteconfig/internal/templates/hosted-cluster"
	ibi_templates "github.com/stolostron/siteconfig/internal/templates/image-based-installer"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/yaml.v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// TestTemplateEngineStreamBoundaries verifies that YAML document boundaries and
// empty documents are handled without changing resource content or leaking fields
// between documents. An invalid document must stop decoding the stream.
func TestTemplateEngineStreamBoundaries(t *testing.T) {
	const resource = "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: example}\n"
	for _, output := range []string{"# comment\n", "null\n", "~\n", "---\n", "---\n---\n", "--- # comment\nnull\n---\n"} {
		t.Run(fmt.Sprintf("empty %q", output), func(t *testing.T) {
			g := NewWithT(t)
			objects, err := NewTemplateEngine().render("empty", output, nil)
			g.Expect(err).NotTo(HaveOccurred(), "empty or null YAML documents should be accepted")
			g.Expect(objects).To(BeEmpty(), "empty or null YAML documents must not produce resources")
		})
	}
	for _, output := range []string{
		resource,
		"--- {apiVersion: v1, kind: ConfigMap, metadata: {name: example}}\n",
		"--- # leading comment\n---\nnull\n---\n" + resource + "---\n~\n---\n",
		`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"example"}}`,
	} {
		t.Run(fmt.Sprintf("resource %q", output), func(t *testing.T) {
			g := NewWithT(t)
			objects, err := NewTemplateEngine().render("resource", output, nil)
			g.Expect(err).NotTo(HaveOccurred(), "document separators and skipped documents should not invalidate a resource")
			g.Expect(objects).To(Equal([]map[string]interface{}{{
				"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]interface{}{"name": "example"},
			}}), "decoding should preserve the complete resource and omit skipped documents")
		})
	}
	t.Run("preserves scalar content and isolates document maps", func(t *testing.T) {
		g := NewWithT(t)
		objects, err := NewTemplateEngine().render("content", resource+`data:
  quoted: "before---after"
  multiline: |
    ---
    next line
  nested: {entries: [one, two]}
--- # second document
`+resource, nil)
		g.Expect(err).NotTo(HaveOccurred(), "separator text inside scalar values should be preserved")
		g.Expect(objects).To(HaveLen(2), "only document boundaries should split the stream")
		g.Expect(objects[0]["data"]).To(Equal(map[string]interface{}{
			"quoted": "before---after", "multiline": "---\nnext line\n",
			"nested": map[string]interface{}{"entries": []interface{}{"one", "two"}},
		}), "quoted, multiline, and nested values should survive decoding")
		g.Expect(objects[1]).NotTo(HaveKey("data"), "fields from the first document must not leak into the second")
	})
	t.Run("does not flatten List resources", func(t *testing.T) {
		g := NewWithT(t)
		objects, err := NewTemplateEngine().render("list", "apiVersion: v1\nkind: List\nmetadata: {name: example}\nitems: []\n", nil)
		g.Expect(err).NotTo(HaveOccurred(), "a Kubernetes List should decode as a resource")
		g.Expect(objects).To(HaveLen(1), "List items must not be flattened into separate stream resources")
		g.Expect(objects[0]["kind"]).To(Equal("List"), "the List resource kind should be preserved")
	})
	t.Run("rejects scalar content on a document marker line", func(t *testing.T) {
		g := NewWithT(t)
		_, err := NewTemplateEngine().render("scalar", "--- |\n  scalar\n", nil)
		g.Expect(err).To(MatchError("failed to unmarshal YAML document 1 in template scalar"), "a scalar on a marker line must be rejected as a resource")
	})
	for _, invalid := range []string{"{}\n", "scalar\n", "apiVersion: v1\nkind: ConfigMap\n"} {
		t.Run(fmt.Sprintf("stops at invalid middle document %q", invalid), func(t *testing.T) {
			g := NewWithT(t)
			objects, err := NewTemplateEngine().render("middle", resource+"---\n"+invalid+"---\n"+resource, nil)
			g.Expect(err).To(HaveOccurred(), "an invalid middle document must fail decoding")
			g.Expect(err.Error()).To(ContainSubstring("YAML document 2"), "the error should identify the invalid middle document")
			g.Expect(objects).To(HaveLen(1), "decoding must stop before the third document")
		})
	}
}

// TestTemplateEngineStreamErrorRedaction ensures that invalid rendered content in
// a later document cannot leak through either the returned error or renderer logs.
func TestTemplateEngineStreamErrorRedaction(t *testing.T) {
	NewWithT(t).Expect(hivev1.AddToScheme(scheme.Scheme)).To(Succeed(), "register ClusterImageSet for the redaction test fixture")
	for _, tt := range []struct {
		name     string
		document string
	}{
		{name: "short scalar", document: "secret42\n"},
		{name: "long scalar", document: "customer-password-for-test\n"},
		{name: "invalid mapping key", document: "data:\n  ? [customer-password-for-test]\n  : value\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			var logs bytes.Buffer
			core := zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.AddSync(&logs), zap.ErrorLevel)
			cluster := &v1alpha1.ClusterInstance{Spec: v1alpha1.ClusterInstanceSpec{ClusterImageSetNameRef: "release"}}
			c := fakeclient.NewClientBuilder().WithScheme(scheme.Scheme).
				WithObjects(GetMockClusterImageSet("release", "image")).Build()
			objects, err := NewTemplateEngine().renderManifestFromTemplate(context.Background(), c, zap.New(core),
				cluster, nil, "templates", "resources.yaml",
				"apiVersion: v1\nkind: ConfigMap\nmetadata: {name: valid}\n---\n"+tt.document)
			const safeError = "failed to unmarshal YAML document 2 in template resources.yaml"
			g.Expect(err).To(MatchError(safeError), "the error should identify the document without including rendered content")
			g.Expect(objects).To(BeEmpty(), "an invalid later document should reject the entire template")
			var entry map[string]interface{}
			g.Expect(json.Unmarshal(logs.Bytes(), &entry)).To(Succeed(), "rendering should log one JSON error entry")
			g.Expect(entry["error"]).To(Equal(safeError), "the logged error should contain only the sanitized document location")
			g.Expect(logs.String()).NotTo(ContainSubstring("secret42"), "logs must omit short scalar values")
			g.Expect(logs.String()).NotTo(ContainSubstring("custome"), "logs must omit even the decoder's truncated scalar values")
		})
	}
}

// TestTemplateEngineSetObjectErrorRedaction guards against object decoder errors
// embedding the manifest in logs or returned errors, while retaining prior objects.
func TestTemplateEngineSetObjectErrorRedaction(t *testing.T) {
	NewWithT(t).Expect(hivev1.AddToScheme(scheme.Scheme)).To(Succeed(), "register ClusterImageSet for parsing error fixtures")
	const validDocument = "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: valid}\n---\n"
	// An empty kind passes template validation but the object decoder rejects it
	// with an error containing the complete manifest, including these dummy secrets.
	const invalidDocument = "apiVersion: v1\nkind: \"\"\nmetadata: {name: invalid}\ndata: {password: secret42}\n"
	for _, tt := range []struct {
		name   string
		prefix string
		count  int
	}{
		{name: "first document"},
		{name: "later document", prefix: validDocument, count: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			var logs bytes.Buffer
			core := zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.AddSync(&logs), zap.ErrorLevel)
			cluster := &v1alpha1.ClusterInstance{Spec: v1alpha1.ClusterInstanceSpec{ClusterImageSetNameRef: "release"}}
			c := fakeclient.NewClientBuilder().WithScheme(scheme.Scheme).
				WithObjects(GetMockClusterImageSet("release", "image")).Build()
			objects, err := NewTemplateEngine().renderManifestFromTemplate(context.Background(), c, zap.New(core),
				cluster, nil, "templates", "resources.yaml", tt.prefix+invalidDocument)
			g.Expect(err).To(MatchError("failed to parse rendered template resources.yaml"), "parsing errors should identify only the template")
			g.Expect(errors.Unwrap(err)).To(BeNil(), "the original decoder error must not be available through unwrapping")
			g.Expect(objects).To(HaveLen(tt.count), "objects processed before the parsing failure should still be returned")
			if tt.count > 0 {
				g.Expect(objects[0].GetName()).To(Equal("valid"), "the previously processed resource should be preserved")
			}
			var entry map[string]interface{}
			g.Expect(json.Unmarshal(logs.Bytes(), &entry)).To(Succeed(), "parsing should log one JSON error entry")
			g.Expect(entry["msg"]).To(Equal("Failed to parse rendered template"), "the log message should omit decoder diagnostics")
			g.Expect(entry["templateRef"]).To(Equal("templates"), "the log should identify the template reference")
			g.Expect(entry["templateKey"]).To(Equal("resources.yaml"), "the log should identify the failing template key")
			g.Expect(entry).NotTo(HaveKey("error"), "the log must not include the raw object decoder error")
			g.Expect(logs.String()).NotTo(ContainSubstring("secret42"), "logs must omit manifest contents")
		})
	}
}

const rangeConfigMapTemplate = `{{ range .Spec.AdditionalNTPSources }}
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ . }}
{{ end }}`

// TestTemplateEngineRange checks that template loops can emit zero, one, or many
// resources while preserving iteration order.
func TestTemplateEngineRange(t *testing.T) {
	for _, names := range [][]string{nil, {}, {"one"}, {"one", "two", "three"}} {
		t.Run(fmt.Sprintf("names=%v", names), func(t *testing.T) {
			g := NewWithT(t)
			data := &ClusterData{Spec: v1alpha1.ClusterInstanceSpec{AdditionalNTPSources: names}}
			objects, err := NewTemplateEngine().render("range", rangeConfigMapTemplate, data)
			g.Expect(err).NotTo(HaveOccurred(), "range expansion should render for nil, empty, and populated inputs")
			g.Expect(objects).To(HaveLen(len(names)), "each range iteration should produce one resource")
			for i, name := range names {
				g.Expect(objects[i]["metadata"]).To(HaveKeyWithValue("name", name), "resource %d should follow range iteration order", i)
			}
		})
	}
}

// TestTemplateEngineConditional checks that a false condition emits no resources,
// and that a true condition preserves every resource produced by its body.
func TestTemplateEngineConditional(t *testing.T) {
	for _, names := range [][]string{nil, {}, {"one"}, {"one", "two", "three"}} {
		for _, enabled := range []bool{false, true} {
			t.Run(fmt.Sprintf("names=%v enabled=%t", names, enabled), func(t *testing.T) {
				g := NewWithT(t)
				data := &ClusterData{Spec: v1alpha1.ClusterInstanceSpec{AdditionalNTPSources: names, HoldInstallation: enabled}}
				objects, err := NewTemplateEngine().render("conditional", " \n{{ if .Spec.HoldInstallation }}"+rangeConfigMapTemplate+"{{ end }}\n", data)
				g.Expect(err).NotTo(HaveOccurred(), "conditional expansion should render with enabled=%t", enabled)
				if enabled {
					g.Expect(objects).To(HaveLen(len(names)), "a true condition should preserve every resource emitted by its body")
				} else {
					g.Expect(objects).To(BeEmpty(), "a false condition must suppress the entire template body")
				}
			})
		}
	}
}

// TestTemplateEngineStreamActionsAndMetadata verifies that every document receives
// the appropriate render, prune, or suppress action at both cluster and node scope.
// It also checks metadata precedence, ownership labels, and sync-wave preservation.
func TestTemplateEngineStreamActionsAndMetadata(t *testing.T) {
	NewWithT(t).Expect(hivev1.AddToScheme(scheme.Scheme)).To(Succeed(), "register ClusterImageSet for action and metadata test fixtures")
	for _, nodeLevel := range []bool{false, true} {
		for _, action := range []action{actionRender, actionPrune, actionSuppress} {
			t.Run(fmt.Sprintf("node=%t action=%s", nodeLevel, action), func(t *testing.T) {
				g := NewWithT(t)
				ctx := context.Background()
				cluster := &v1alpha1.ClusterInstance{
					ObjectMeta: metav1.ObjectMeta{Name: "cluster", Namespace: "cluster"},
					Spec: v1alpha1.ClusterInstanceSpec{
						ClusterName: "cluster", ClusterImageSetNameRef: "release",
						AdditionalNTPSources: []string{"one", "two"},
						ExtraLabels:          map[string]map[string]string{"ConfigMap": {"source": "cluster"}},
						ExtraAnnotations:     map[string]map[string]string{"ConfigMap": {"source": "cluster"}},
					},
				}
				var node *v1alpha1.NodeSpec
				expectedSource := "cluster"
				if nodeLevel {
					node = &v1alpha1.NodeSpec{HostName: "node",
						ExtraLabels:      map[string]map[string]string{"ConfigMap": {"source": "node"}},
						ExtraAnnotations: map[string]map[string]string{"ConfigMap": {"source": "node"}},
					}
					expectedSource = "node"
				}
				switch action {
				case actionPrune:
					// Node selectors supplement cluster selectors; pruning takes precedence over suppression.
					cluster.Spec.SuppressedManifests = []string{"ConfigMap"}
					selector := []v1alpha1.ResourceRef{{APIVersion: "v1", Kind: "ConfigMap"}}
					if node != nil {
						node.PruneManifests = selector
					} else {
						cluster.Spec.PruneManifests = selector
					}
				case actionSuppress:
					if node != nil {
						node.SuppressedManifests = []string{"ConfigMap"}
					} else {
						cluster.Spec.SuppressedManifests = []string{"ConfigMap"}
					}
				}
				c := fakeclient.NewClientBuilder().WithScheme(scheme.Scheme).
					WithObjects(GetMockClusterImageSet("release", "image")).Build()
				objects, err := NewTemplateEngine().renderManifestFromTemplate(ctx, c, zap.NewNop(), cluster, node, "templates", "resources", `{{ range .Spec.AdditionalNTPSources }}
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ if $.SpecialVars.CurrentNode.HostName }}{{ $.SpecialVars.CurrentNode.HostName }}{{ else }}{{ $.Spec.ClusterName }}{{ end }}-{{ . }}
  annotations:
    siteconfig.open-cluster-management.io/sync-wave: "2"
{{ end }}
---
null
`)
				g.Expect(err).NotTo(HaveOccurred(), "rendering should succeed for node=%t action=%s", nodeLevel, action)
				g.Expect(objects).To(HaveLen(2), "both range resources should be processed and the trailing null skipped")
				for i, object := range objects {
					g.Expect(object.action).To(Equal(action), "resource %d should receive the selected action", i)
					g.Expect(object.GetName()).To(Equal(expectedSource+"-"+cluster.Spec.AdditionalNTPSources[i]), "resource %d should use the selected cluster or node context", i)
					wave, err := object.GetSyncWave()
					g.Expect(err).NotTo(HaveOccurred(), "resource %s should retain a valid sync wave", object.GetName())
					g.Expect(wave).To(Equal(2), "resource %s should retain its template sync wave", object.GetName())
					if action == actionRender {
						g.Expect(object.GetLabels()).To(HaveKeyWithValue(OwnedByLabel, "cluster_cluster"), "rendered resource %s should carry ClusterInstance ownership", object.GetName())
						g.Expect(object.GetLabels()).To(HaveKeyWithValue("source", expectedSource), "resource %s should use labels from the most specific scope", object.GetName())
						g.Expect(object.GetAnnotations()).To(HaveKeyWithValue("source", expectedSource), "resource %s should use annotations from the most specific scope", object.GetName())
					} else {
						g.Expect(object.GetLabels()).To(BeEmpty(), "pruned or suppressed resource %s must not receive rendering labels", object.GetName())
						g.Expect(object.GetAnnotations()).NotTo(HaveKey("source"), "pruned or suppressed resource %s must not receive extra annotations", object.GetName())
					}
				}
			})
		}
	}
}

// TestTemplateEngineDefaultTemplateCompatibility guards against regressions in the
// built-in AI, IBI, and HCP templates by comparing their decoded content with the
// former single-document decoder at both cluster and node scope.
func TestTemplateEngineDefaultTemplateCompatibility(t *testing.T) {
	g := NewWithT(t)
	g.Expect(hivev1.AddToScheme(scheme.Scheme)).To(Succeed(), "register ClusterImageSet for built-in template fixtures")
	params := &TestParams{ClusterName: "cluster", ClusterNamespace: "cluster", ClusterImageSetName: "release"}
	cluster := GetMockSNOClusterInstance(params)
	cluster.Spec.Nodes[0].HostName = "node"
	c := fakeclient.NewClientBuilder().WithScheme(scheme.Scheme).
		WithObjects(GetMockClusterImageSet("release", "image")).Build()
	for _, flow := range []struct {
		name    string
		cluster map[string]string
		node    map[string]string
	}{
		{name: "AI", cluster: ai_templates.GetClusterTemplates(), node: ai_templates.GetNodeTemplates()},
		{name: "IBI", cluster: ibi_templates.GetClusterTemplates(), node: ibi_templates.GetNodeTemplates()},
		{name: "HCP", cluster: hcp_templates.GetClusterTemplates(), node: hcp_templates.GetNodeTemplates()},
	} {
		for _, scope := range []struct {
			name      string
			node      *v1alpha1.NodeSpec
			templates map[string]string
		}{
			{name: "cluster", templates: flow.cluster},
			{name: "node", node: &cluster.Spec.Nodes[0], templates: flow.node},
		} {
			for key, source := range scope.templates {
				t.Run(flow.name+"/"+scope.name+"/"+key, func(t *testing.T) {
					g := NewWithT(t)
					data, err := buildClusterData(context.Background(), c, cluster, scope.node)
					g.Expect(err).NotTo(HaveOccurred(), "build cluster data for %s/%s/%s", flow.name, scope.name, key)
					parsed, err := ParseTemplate(key, source)
					g.Expect(err).NotTo(HaveOccurred(), "parse built-in template %s/%s/%s", flow.name, scope.name, key)
					var buffer bytes.Buffer
					g.Expect(parsed.Execute(&buffer, data)).To(Succeed(), "expand built-in template %s/%s/%s", flow.name, scope.name, key)
					// Compare the whole decoded map with the former single-document decoder.
					var legacy map[string]interface{}
					g.Expect(yaml.Unmarshal(buffer.Bytes(), &legacy)).To(Succeed(), "decode %s/%s/%s with the former decoder", flow.name, scope.name, key)
					objects, err := NewTemplateEngine().render(key, source, data)
					g.Expect(err).NotTo(HaveOccurred(), "decode %s/%s/%s with the stream decoder", flow.name, scope.name, key)
					if legacy == nil {
						g.Expect(objects).To(BeEmpty(), "built-in templates with empty output must still emit no resources")
					} else {
						g.Expect(objects).To(Equal([]map[string]interface{}{legacy}), "stream decoding must preserve the complete built-in resource")
					}
				})
			}
		}
	}
}

func TestTemplateEngineRenderStreamErrorReadAhead(t *testing.T) {
	const streamConfigMap = "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: example}\n"
	for _, position := range []int{2, 3} {
		t.Run(fmt.Sprintf("syntax error at document %d", position), func(t *testing.T) {
			g := NewWithT(t)
			prefix := strings.Repeat(streamConfigMap+"---\n", position-1)
			_, err := NewTemplateEngine().render("read-ahead", prefix+"@sensitive-rendered-value\n", &ClusterData{})

			// yaml.v3 reads ahead into the next document, so the index identifies
			// the failing Decode call rather than the document containing the error.
			g.Expect(err).To(MatchError(fmt.Sprintf("failed to unmarshal YAML document %d in template read-ahead", position-1)),
				"read-ahead errors should retain the decode location without exposing rendered content or decoder diagnostics")
		})
	}
}
