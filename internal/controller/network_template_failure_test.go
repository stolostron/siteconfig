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

package controller

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	hivev1 "github.com/openshift/hive/apis/hive/v1"
	"github.com/stolostron/siteconfig/api/v1alpha1"
	ci "github.com/stolostron/siteconfig/internal/controller/clusterinstance"
	"github.com/stolostron/siteconfig/internal/controller/configuration"
	"github.com/stolostron/siteconfig/internal/controller/deletion"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestNodeTemplateFailurePreventsApplyAndRetries(t *testing.T) {
	for _, tc := range []struct {
		name, source, message string
		data                  map[string]apiextensionsv1.JSON
	}{
		{"missing key", "", `templateData key "nodeIp" is missing`, map[string]apiextensionsv1.JSON{"other": {Raw: []byte(`true`)}}},
		{"absent map", "", "templateData is not defined", nil},
		{"parse error", `{{ undefinedFunction }}`, "failed to parse template", nil},
		{"execution error", `{{ fail "execution failed" }}`, "execution failed", nil},
		{"invalid YAML", "kind: [", "failed to unmarshal YAML", nil},
		{"invalid manifest", "kind: ConfigMap\nmetadata:\n  name: node-output", "missing apiVersion", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testNodeTemplateFailure(t, tc.source, tc.message, tc.data)
		})
	}
}

func testNodeTemplateFailure(t *testing.T, source, message string, parameters map[string]apiextensionsv1.JSON) {
	g := NewWithT(t)
	t.Helper()
	ctx := context.Background()
	g.Expect(v1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())
	g.Expect(hivev1.AddToScheme(scheme.Scheme)).To(Succeed())
	cluster := &v1alpha1.ClusterInstance{
		ObjectMeta: metav1.ObjectMeta{Name: "network-test", Namespace: "network-test", Generation: 1,
			Finalizers:  []string{clusterInstanceFinalizer},
			Annotations: map[string]string{veleroRestoreStatusAnnotation: "true"}},
		Spec: v1alpha1.ClusterInstanceSpec{
			ClusterName: "network-test", ClusterType: v1alpha1.ClusterTypeSNO,
			ClusterImageSetNameRef: "test-image", PullSecretRef: corev1.LocalObjectReference{Name: "pull-secret"},
			HoldInstallation: true,
			TemplateRefs:     []v1alpha1.TemplateRef{{Name: "cluster-template", Namespace: "network-test"}},
			Nodes: []v1alpha1.NodeSpec{{HostName: "node1", Role: "master",
				BmcCredentialsName: v1alpha1.BmcCredentialsName{Name: "bmc-secret"},
				TemplateRefs:       []v1alpha1.TemplateRef{{Name: "node-template", Namespace: "network-test"}},
				TemplateData:       parameters,
			}},
		},
	}
	const clusterTemplate = `apiVersion: v1
kind: ConfigMap
metadata:
  name: cluster-output
  namespace: network-test
data:
  stage: rendered-before-node`
	const nodeTemplate = `apiVersion: v1
kind: ConfigMap
metadata:
  name: node-output
  namespace: network-test
data:
  ip: {{ templateVar .SpecialVars.CurrentNode "nodeIp" | quote }}`
	if source == "" {
		source = nodeTemplate
	}
	c := createSSAMockClient(cluster, ci.GetMockClusterImageSet("test-image", "release-image"),
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "pull-secret", Namespace: cluster.Namespace}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "bmc-secret", Namespace: cluster.Namespace}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cluster-template", Namespace: cluster.Namespace},
			Data: map[string]string{"ClusterOutput": clusterTemplate}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "node-template", Namespace: cluster.Namespace},
			Data: map[string]string{"NetworkOutput": source}},
	)
	store, err := configuration.NewConfigurationStore(configuration.NewDefaultConfiguration())
	g.Expect(err).To(Succeed())
	log := zap.NewNop()
	r := &ClusterInstanceReconciler{Client: c, Scheme: scheme.Scheme, Log: log,
		TmplEngine: ci.NewTemplateEngine(), ConfigStore: store,
		DeletionHandler: &deletion.DeletionHandler{Client: c, Logger: log}}
	key := client.ObjectKeyFromObject(cluster)
	result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
	g.Expect(err).To(MatchError(ContainSubstring(message)))
	g.Expect(result.RequeueAfter).To(Equal(DefaultTemplateRenderingErrorDelay))
	g.Expect(c.Get(ctx, key, cluster)).To(Succeed())
	condition := meta.FindStatusCondition(cluster.Status.Conditions, string(v1alpha1.RenderedTemplates))
	g.Expect(condition).NotTo(BeNil())
	g.Expect(condition.Status).To(Equal(metav1.ConditionFalse))
	g.Expect(condition.Reason).To(Equal(string(v1alpha1.Failed)))
	g.Expect(condition.Message).To(ContainSubstring(message))
	g.Expect(condition.Message).To(ContainSubstring(`node "node1"`))
	g.Expect(condition.Message).To(ContainSubstring(`template ConfigMap "network-test/node-template"`))
	g.Expect(condition.Message).To(ContainSubstring(`key "NetworkOutput"`))
	g.Expect(condition.Message).To(ContainSubstring("NetworkOutput"))
	g.Expect(cluster.Status.ObservedGeneration).To(BeZero())
	g.Expect(cluster.Status.ManifestsRendered).To(BeEmpty())
	for _, name := range []string{"cluster-output", "node-output"} {
		err := c.Get(ctx, client.ObjectKey{Name: name, Namespace: cluster.Namespace}, &corev1.ConfigMap{})
		g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "no partially rendered resource should be applied")
	}
	// Correct the template and held spec, then exercise the existing retry path.
	templateConfigMap := &corev1.ConfigMap{}
	g.Expect(c.Get(ctx, client.ObjectKey{Name: "node-template", Namespace: cluster.Namespace}, templateConfigMap)).To(Succeed())
	templateConfigMap.Data["NetworkOutput"] = nodeTemplate
	g.Expect(c.Update(ctx, templateConfigMap)).To(Succeed())
	cluster.Spec.Nodes[0].TemplateData = map[string]apiextensionsv1.JSON{
		"nodeIp": {Raw: []byte(`"192.0.2.10"`)},
	}
	g.Expect(c.Update(ctx, cluster)).To(Succeed())
	result, err = r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
	g.Expect(err).To(Succeed())
	g.Expect(result.IsZero()).To(BeTrue())
	g.Expect(c.Get(ctx, key, cluster)).To(Succeed())
	for _, conditionType := range []v1alpha1.ClusterInstanceConditionType{
		v1alpha1.RenderedTemplates, v1alpha1.RenderedTemplatesValidated, v1alpha1.RenderedTemplatesApplied,
	} {
		g.Expect(meta.IsStatusConditionTrue(cluster.Status.Conditions, string(conditionType))).To(BeTrue())
	}
	g.Expect(cluster.Status.ObservedGeneration).To(Equal(cluster.Generation))
	output := &corev1.ConfigMap{}
	g.Expect(c.Get(ctx, client.ObjectKey{Name: "node-output", Namespace: cluster.Namespace}, output)).To(Succeed())
	g.Expect(output.Data["ip"]).To(Equal("192.0.2.10"))
}
