/*
Copyright 2024.

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
	"reflect"
	"testing"

	"github.com/go-logr/logr"
	bmh_v1alpha1 "github.com/metal3-io/baremetal-operator/apis/metal3.io/v1alpha1"
	aiv1beta1 "github.com/openshift/assisted-service/api/v1beta1"
	"github.com/stolostron/siteconfig/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTemplateEngine_render(t *testing.T) {

	NetConfig := GetMockNetConfig()
	TestClusterInstance := &v1alpha1.ClusterInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "site-sno-du-1",
			Namespace: "site-sno-du-1",
		},
		Spec: v1alpha1.ClusterInstanceSpec{
			ClusterName:            "site-sno-du-1",
			PullSecretRef:          corev1.LocalObjectReference{Name: "pullSecretName"},
			ClusterImageSetNameRef: "openshift-test",
			SSHPublicKey:           "ssh-rsa",
			BaseDomain:             "example.com",
			ApiVIPs:                []string{"192.0.2.1", "192.0.2.2"},
			HoldInstallation:       false,
			AdditionalNTPSources:   []string{"NTP.server1", "192.0.2.3"},
			MachineNetwork:         []v1alpha1.MachineNetworkEntry{{CIDR: "203.0.113.0/24"}},
			ClusterNetwork:         []v1alpha1.ClusterNetworkEntry{{CIDR: "203.0.113.0/24", HostPrefix: 23}},
			ServiceNetwork:         []v1alpha1.ServiceNetworkEntry{{CIDR: "203.0.113.0/24"}},
			NetworkType:            "OVNKubernetes",
			ExtraLabels:            map[string]map[string]string{"ManagedCluster": {"group-du-sno": "test", "common": "true", "sites": "site-sno-du-1"}},
			InstallConfigOverrides: "{\"capabilities\":{\"baselineCapabilitySet\": \"None\", \"additionalEnabledCapabilities\": [ \"marketplace\", \"NodeTuning\" ] }}",
			IgnitionConfigOverride: "igen",
			DiskEncryption: &v1alpha1.DiskEncryption{
				Type: "nbde",
				Tang: []v1alpha1.TangConfig{{URL: "http://203.0.113.1:7500", Thumbprint: "1234567890"}}},
			Proxy:              &aiv1beta1.Proxy{NoProxy: "foobar"},
			ExtraManifestsRefs: []corev1.LocalObjectReference{{Name: "foobar1"}, {Name: "foobar2"}},
			TemplateRefs:       []v1alpha1.TemplateRef{{Name: "cluster-v1", Namespace: "site-sno-du-1"}},
			Nodes: []v1alpha1.NodeSpec{{
				BmcAddress:             "idrac-virtualmedia+https://198.51.100.0/redfish/v1/Systems/System.Embedded.1",
				BmcCredentialsName:     v1alpha1.BmcCredentialsName{Name: "bmc-secret"},
				BootMACAddress:         "00:00:5E:00:53:00",
				AutomatedCleaningMode:  "disabled",
				RootDeviceHints:        &bmh_v1alpha1.RootDeviceHints{HCTL: "1:2:0:0"},
				HostName:               "node1",
				Role:                   "master",
				IronicInspect:          "enabled",
				BootMode:               "UEFI",
				InstallerArgs:          "[\"--append-karg\", \"nameserver=198.51.100.0\", \"-n\"]",
				IgnitionConfigOverride: "{\"ignition\": {\"version\": \"3.1.0\"}, \"storage\": {\"files\": [{\"path\": \"/etc/containers/registries.conf\", \"overwrite\": true, \"contents\": {\"source\": \"data:text/plain;base64,foobar==\"}}]}}",
				TemplateRefs:           []v1alpha1.TemplateRef{{Name: "node-template", Namespace: "site-sno-du-1"}},
				NodeNetwork: &aiv1beta1.NMStateConfigSpec{
					NetConfig:  aiv1beta1.NetConfig{Raw: []byte(NetConfig.RawNetConfig())},
					Interfaces: NetConfig.Interfaces,
				},
			}},
		},
	}

	TestData, _ := buildClusterData(TestClusterInstance, &TestClusterInstance.Spec.Nodes[0])

	type fields struct {
		Log logr.Logger
	}
	type args struct {
		templateType string
		templateStr  string
		data         *ClusterData
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    map[string]interface{}
		wantErr bool
	}{
		{
			name: "Test with invalid template",
			args: args{
				templateType: "AgentClusterInstall",
				templateStr:  "foobar {{if}}",
				data:         TestData,
			},
			want:    nil,
			wantErr: true,
		},

		{
			name: "Test with a valid AgentClusterInstall-like template",
			args: args{
				templateType: "AgentClusterInstall",
				templateStr:  GetMockAgentClusterInstallTemplate(),
				data:         TestData,
			},
			want: map[string]interface{}{
				"apiVersion": "extensions.hive.openshift.io/v1beta1",
				"kind":       "AgentClusterInstall",
				"metadata": map[string]interface{}{
					"annotations": map[string]interface{}{
						"agent-install.openshift.io/install-config-overrides": "{\"networking\":{\"networkType\":\"OVNKubernetes\"},\"capabilities\":{\"baselineCapabilitySet\": \"None\", \"additionalEnabledCapabilities\": [ \"marketplace\", \"NodeTuning\" ] }}",
						"siteconfig.open-cluster-management.io/sync-wave":     "1"},
					"name":      "site-sno-du-1",
					"namespace": "site-sno-du-1",
				},
				"spec": map[string]interface{}{
					"apiVIPs":               []interface{}{"192.0.2.1", "192.0.2.2"},
					"clusterDeploymentRef":  map[string]interface{}{"name": "site-sno-du-1"},
					"holdInstallation":      false,
					"imageSetRef":           map[string]interface{}{"name": "openshift-test"},
					"proxy":                 map[string]interface{}{"noProxy": "foobar"},
					"manifestsConfigMapRef": map[string]interface{}{"name": "site-sno-du-1"},
					"networking": map[string]interface{}{
						"clusterNetwork": []interface{}{map[string]interface{}{"cidr": "203.0.113.0/24", "hostPrefix": 23}},
						"machineNetwork": []interface{}{map[string]interface{}{"cidr": "203.0.113.0/24"}},
						"serviceNetwork": []interface{}{"203.0.113.0/24"}},
					"provisionRequirements": map[string]interface{}{"controlPlaneAgents": 1, "workerAgents": 0},
					"sshPublicKey":          "ssh-rsa"}},
			wantErr: false,
		},

		{
			name: "Test with valid NMState-like template",
			args: args{
				templateType: "NMStateConfig",
				templateStr:  GetMockNMStateConfigTemplate(),
				data:         TestData,
			},
			want: map[string]interface{}{
				"apiVersion": "agent-install.openshift.io/v1beta1",
				"kind":       "NMStateConfig",
				"metadata": map[string]interface{}{
					"annotations": map[string]interface{}{"siteconfig.open-cluster-management.io/sync-wave": "1"},
					"labels":      map[string]interface{}{"nmstate-label": "site-sno-du-1"},
					"name":        "node1",
					"namespace":   "site-sno-du-1",
				},
				"spec": map[string]interface{}{
					"config":     NetConfig.Config,
					"interfaces": NetConfig.GetInterfaces(),
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmplEngine := &TemplateEngine{
				Log: tt.fields.Log,
			}
			got, err := tmplEngine.render(tt.args.templateType, tt.args.templateStr, tt.args.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("TemplateEngine.render() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("TemplateEngine.render() = %v, want %v", got, tt.want)
			}
		})
	}
}

var _ = Describe("renderTemplates", func() {
	var (
		c                   client.Client
		ctx                 = context.Background()
		tmplEngine          *TemplateEngine
		TestClusterInstance *v1alpha1.ClusterInstance
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			Build()

		testLogger := ctrl.Log.WithName("TemplateEngine")
		tmplEngine = NewTemplateEngine(testLogger)

		TestClusterInstance = &v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "site-sno-du-1",
				Namespace: "site-sno-du-1",
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				ClusterName: "site-sno-du-1",
				Nodes: []v1alpha1.NodeSpec{{
					HostName: "node1",
				}},
			},
		}
	})

	It("fails when the template reference cannot be retrieved", func() {
		TestClusterInstance.Spec.TemplateRefs = []v1alpha1.TemplateRef{{Name: "does-not-exist", Namespace: "test"}}

		_, err := tmplEngine.renderTemplates(ctx, c, TestClusterInstance, nil)
		Expect(err).To(HaveOccurred())
	})

	It("fails to render template because it cannot build the site data", func() {
		TestClusterInstance.Spec.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "cluster-level", Namespace: "test"},
		}

		clusterTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-level", Namespace: "test"},
			Data: map[string]string{
				"TestA": GetMockBasicClusterTemplate("TestA"),
			},
		}
		Expect(c.Create(ctx, clusterTemplates)).To(Succeed())

		TestClusterInstance.Spec.InstallConfigOverrides = "{foobar}"
		_, err := tmplEngine.renderTemplates(ctx, c, TestClusterInstance, nil)
		Expect(err).To(HaveOccurred())
		Expect(err).To(MatchError(ContainSubstring("invalid json parameter set at installConfigOverride")))
	})

	It("fails to render template due to invalid template", func() {
		TestClusterInstance.Spec.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "cluster-level", Namespace: "test"},
		}

		clusterTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-level", Namespace: "test"},
			Data: map[string]string{
				"TestA": "{{.Spec.doesNotExist}}",
			},
		}
		Expect(c.Create(ctx, clusterTemplates)).To(Succeed())

		_, err := tmplEngine.renderTemplates(ctx, c, TestClusterInstance, nil)
		Expect(err).To(HaveOccurred())
		Expect(err).To(MatchError(ContainSubstring("field doesNotExist")))
	})

	It("suppresses rendering manifests at cluster-level", func() {

		TestClusterInstance.Spec.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "cluster-level", Namespace: "test"},
		}

		clusterTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-level", Namespace: "test"},
			Data: map[string]string{
				"TestA_Foobar": GetMockBasicClusterTemplate("TestA"),
				"TestB":        GetMockBasicClusterTemplate("TestB"),
			},
		}
		Expect(c.Create(ctx, clusterTemplates)).To(Succeed())

		TestClusterInstance.Spec.SuppressedManifests = []string{"TestA", "TestC"}

		got, err := tmplEngine.renderTemplates(ctx, c, TestClusterInstance, nil)
		Expect(err).ToNot(HaveOccurred())

		Expect(len(got)).To(Equal(1))
		Expect(got[0]).To(Equal(map[string]interface{}{
			"apiVersion": "test.io/v1",
			"kind":       "TestB",
			"metadata": map[string]interface{}{
				"labels": map[string]interface{}{
					OwnedByLabel: GenerateOwnedByLabelValue(TestClusterInstance.Namespace, TestClusterInstance.Name),
				},
			},
			"spec": map[string]interface{}{
				"name": "site-sno-du-1",
			},
		}))
	})

	It("suppresses rendering manifests at node-level", func() {

		node := &TestClusterInstance.Spec.Nodes[0]
		node.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "node-level", Namespace: "test"},
		}

		nodeTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "node-level", Namespace: "test"},
			Data: map[string]string{
				"TestC": GetMockBasicNodeTemplate("TestC"),
				"TestD": GetMockBasicNodeTemplate("TestD"),
			},
		}
		Expect(c.Create(ctx, nodeTemplates)).To(Succeed())

		node.SuppressedManifests = []string{"TestA", "TestC"}

		got, err := tmplEngine.renderTemplates(ctx, c, TestClusterInstance, node)
		Expect(err).ToNot(HaveOccurred())

		Expect(len(got)).To(Equal(1))
		Expect(got[0]).To(Equal(map[string]interface{}{
			"apiVersion": "test.io/v1",
			"kind":       "TestD",
			"metadata": map[string]interface{}{
				"labels": map[string]interface{}{
					OwnedByLabel: GenerateOwnedByLabelValue(TestClusterInstance.Namespace, TestClusterInstance.Name),
				},
			},
			"spec": map[string]interface{}{
				"name": "node1",
			},
		}))
	})

	It("renders a cluster-level template with extra annotations and labels", func() {

		TestClusterInstance.Spec.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "cluster-level", Namespace: "test"},
		}

		clusterTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-level", Namespace: "test"},
			Data: map[string]string{
				"Cluster": GetMockBasicClusterTemplate("Cluster"),
			},
		}
		Expect(c.Create(ctx, clusterTemplates)).To(Succeed())

		TestClusterInstance.Spec.ExtraAnnotations = map[string]map[string]string{
			"Cluster": {
				"extra-annotation-l1": "test",
				"extra-annotation-l2": "test",
			},
		}
		TestClusterInstance.Spec.ExtraLabels = map[string]map[string]string{
			"Cluster": {
				"extra-labels-l1": "test",
				"extra-labels-l2": "test",
			},
		}
		got, err := tmplEngine.renderTemplates(ctx, c, TestClusterInstance, nil)
		Expect(err).ToNot(HaveOccurred())

		Expect(len(got)).To(Equal(1))
		Expect(got[0]).To(Equal(map[string]interface{}{
			"apiVersion": "test.io/v1",
			"kind":       "Cluster",
			"metadata": map[string]interface{}{
				"annotations": map[string]interface{}{
					"extra-annotation-l1": "test",
					"extra-annotation-l2": "test",
				},
				"labels": map[string]interface{}{
					OwnedByLabel:      GenerateOwnedByLabelValue(TestClusterInstance.Namespace, TestClusterInstance.Name),
					"extra-labels-l1": "test",
					"extra-labels-l2": "test",
				},
			},
			"spec": map[string]interface{}{
				"name": "site-sno-du-1",
			},
		}))
	})

	It("renders a node-level template with extra annotations and labels defined at cluster-level", func() {
		node := &TestClusterInstance.Spec.Nodes[0]
		node.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "node-level", Namespace: "test"},
		}

		clusterTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "node-level", Namespace: "test"},
			Data: map[string]string{
				"Node": GetMockBasicNodeTemplate("Node"),
			},
		}
		Expect(c.Create(ctx, clusterTemplates)).To(Succeed())

		// Extra annotations defined at cluster-level
		TestClusterInstance.Spec.ExtraAnnotations = map[string]map[string]string{
			"Node": {
				"extra-node-annotation-l1": "test",
				"extra-node-annotation-l2": "test",
			},
		}
		TestClusterInstance.Spec.ExtraLabels = map[string]map[string]string{
			"Node": {
				"extra-labels-l1": "test",
				"extra-labels-l2": "test",
			},
		}
		got, err := tmplEngine.renderTemplates(ctx, c, TestClusterInstance, node)
		Expect(err).ToNot(HaveOccurred())

		Expect(len(got)).To(Equal(1))
		Expect(got[0]).To(Equal(map[string]interface{}{
			"apiVersion": "test.io/v1",
			"kind":       "Node",
			"metadata": map[string]interface{}{
				"annotations": map[string]interface{}{
					"extra-node-annotation-l1": "test",
					"extra-node-annotation-l2": "test",
				},
				"labels": map[string]interface{}{
					OwnedByLabel:      GenerateOwnedByLabelValue(TestClusterInstance.Namespace, TestClusterInstance.Name),
					"extra-labels-l1": "test",
					"extra-labels-l2": "test",
				},
			},
			"spec": map[string]interface{}{
				"name": "node1",
			},
		}))
	})

	It("renders a node-level template with extra annotations and labels defined at node-level", func() {
		node := &TestClusterInstance.Spec.Nodes[0]
		node.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "node-level", Namespace: "test"},
		}

		clusterTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "node-level", Namespace: "test"},
			Data: map[string]string{
				"Node": GetMockBasicNodeTemplate("Node"),
			},
		}
		Expect(c.Create(ctx, clusterTemplates)).To(Succeed())

		// Extra annotations defined at node-level
		node.ExtraAnnotations = map[string]map[string]string{
			"Node": {
				"extra-node-annotation-l1": "test",
				"extra-node-annotation-l2": "test",
			},
		}
		node.ExtraLabels = map[string]map[string]string{
			"Node": {
				"extra-node-labels-l1": "test",
				"extra-node-labels-l2": "test",
			},
		}
		got, err := tmplEngine.renderTemplates(ctx, c, TestClusterInstance, node)
		Expect(err).ToNot(HaveOccurred())

		Expect(len(got)).To(Equal(1))
		Expect(got[0]).To(Equal(map[string]interface{}{
			"apiVersion": "test.io/v1",
			"kind":       "Node",
			"metadata": map[string]interface{}{
				"annotations": map[string]interface{}{
					"extra-node-annotation-l1": "test",
					"extra-node-annotation-l2": "test",
				},
				"labels": map[string]interface{}{
					OwnedByLabel:           GenerateOwnedByLabelValue(TestClusterInstance.Namespace, TestClusterInstance.Name),
					"extra-node-labels-l1": "test",
					"extra-node-labels-l2": "test",
				},
			},
			"spec": map[string]interface{}{
				"name": "node1",
			},
		}))
	})

})

var _ = Describe("ProcessTemplates", func() {
	var (
		c                   client.Client
		ctx                 = context.Background()
		tmplEngine          *TemplateEngine
		TestClusterInstance v1alpha1.ClusterInstance
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			Build()

		testLogger := ctrl.Log.WithName("TemplateEngine")
		tmplEngine = NewTemplateEngine(testLogger)

		TestClusterInstance = v1alpha1.ClusterInstance{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "site-sno-du-1",
				Namespace: "site-sno-du-1",
			},
			Spec: v1alpha1.ClusterInstanceSpec{
				ClusterName: "site-sno-du-1",
				Nodes: []v1alpha1.NodeSpec{{
					HostName: "node1",
				}},
			},
		}
	})

	It("fails to process cluster-level templates due to an erroneous cluster template", func() {
		// Define and create cluster-level template refs
		clusterTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-level", Namespace: "test"},
			Data: map[string]string{
				"TestA": GetMockBasicClusterTemplate("TestA"),
				"TestB": `{{.foobar}}`,
			},
		}
		Expect(c.Create(ctx, clusterTemplates)).To(Succeed())

		TestClusterInstance.Spec.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "cluster-level", Namespace: "test"},
		}

		_, err := tmplEngine.ProcessTemplates(ctx, c, TestClusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(err).To(MatchError(ContainSubstring("can't evaluate field")))
	})

	It("fails to process node-level templates due to an erroneous node template", func() {
		// Define and create cluster-level template refs
		clusterTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-level", Namespace: "test"},
			Data: map[string]string{
				"TestA": GetMockBasicClusterTemplate("TestA"),
			},
		}
		Expect(c.Create(ctx, clusterTemplates)).To(Succeed())

		TestClusterInstance.Spec.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "cluster-level", Namespace: "test"},
		}

		// Define and create node-level template refs
		node := &TestClusterInstance.Spec.Nodes[0]
		nodeTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "node-level", Namespace: "test"},
			Data: map[string]string{
				"TestC": GetMockBasicNodeTemplate("TestC"),
				"TestD": `{{.foobar}}`,
			},
		}
		Expect(c.Create(ctx, nodeTemplates)).To(Succeed())
		node.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "node-level", Namespace: "test"},
		}

		_, err := tmplEngine.ProcessTemplates(ctx, c, TestClusterInstance)
		Expect(err).To(HaveOccurred())
		Expect(err).To(MatchError(ContainSubstring("can't evaluate field")))
	})

	It("successfully processes cluster and node level templates with manifest suppression", func() {

		// Define and create cluster-level template refs
		clusterTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "cluster-level", Namespace: "test"},
			Data: map[string]string{
				"TestA": GetMockBasicClusterTemplate("TestA"),
				"TestB": GetMockBasicClusterTemplate("TestB"),
			},
		}
		Expect(c.Create(ctx, clusterTemplates)).To(Succeed())

		TestClusterInstance.Spec.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "cluster-level", Namespace: "test"},
		}

		// Define and create node-level template refs
		node := &TestClusterInstance.Spec.Nodes[0]
		node.TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "node-level", Namespace: "test"},
		}
		nodeTemplates := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "node-level", Namespace: "test"},
			Data: map[string]string{
				"TestC": GetMockBasicNodeTemplate("TestC"),
				"TestD": GetMockBasicNodeTemplate("TestD"),
			},
		}
		Expect(c.Create(ctx, nodeTemplates)).To(Succeed())

		// Define suppressed manifest lists for both cluster and node levels
		TestClusterInstance.Spec.SuppressedManifests = []string{"TestB"}
		node.SuppressedManifests = []string{"TestC"}

		// Define extra annotations for both cluster and node levels
		TestClusterInstance.Spec.ExtraAnnotations = map[string]map[string]string{
			"TestA": {
				"extra-annotation-l1": "test",
			},
		}
		node.ExtraAnnotations = map[string]map[string]string{
			"TestD": {
				"extra-node-annotation-l1": "test",
			},
		}

		// Define extra labels for both cluster and node levels
		TestClusterInstance.Spec.ExtraLabels = map[string]map[string]string{
			"TestA": {
				"extra-label-l1": "test",
			},
		}
		node.ExtraLabels = map[string]map[string]string{
			"TestD": {
				"extra-node-label-l1": "test",
			},
		}

		got, err := tmplEngine.ProcessTemplates(ctx, c, TestClusterInstance)
		Expect(err).ToNot(HaveOccurred())

		// Verify manifest suppression
		Expect(len(got)).To(Equal(2))

		// Verify rendering and extra annotations & labels are successfully executed for cluster-level templates
		Expect(got[0]).To(Equal(map[string]interface{}{
			"apiVersion": "test.io/v1",
			"kind":       "TestA",
			"metadata": map[string]interface{}{
				"annotations": map[string]interface{}{
					"extra-annotation-l1": "test",
				},
				"labels": map[string]interface{}{
					OwnedByLabel:     GenerateOwnedByLabelValue(TestClusterInstance.Namespace, TestClusterInstance.Name),
					"extra-label-l1": "test",
				},
			},
			"spec": map[string]interface{}{
				"name": "site-sno-du-1",
			},
		}))

		// Verify rendering and extra annotations & labels are successfully executed for node-level templates
		Expect(got[1]).To(Equal(map[string]interface{}{
			"apiVersion": "test.io/v1",
			"kind":       "TestD",
			"metadata": map[string]interface{}{
				"annotations": map[string]interface{}{
					"extra-node-annotation-l1": "test",
				},
				"labels": map[string]interface{}{
					OwnedByLabel:          GenerateOwnedByLabelValue(TestClusterInstance.Namespace, TestClusterInstance.Name),
					"extra-node-label-l1": "test",
				},
			},
			"spec": map[string]interface{}{
				"name": "node1",
			},
		}))
	})
})
