package controller

import (
	"context"

	hivev1 "github.com/openshift/hive/apis/hive/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fakeclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/stolostron/siteconfig/api/v1alpha1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func getMockBmcSecret(name, namespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string][]byte{"username": []byte("admin"), "password": []byte("password")}}
}

func getMockClusterImageSet(name string) *hivev1.ClusterImageSet {
	return &hivev1.ClusterImageSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "",
		}}
}

func getMockPullSecret(name, namespace string) *corev1.Secret {
	testPullSecretVal := `{"auths":{"cloud.openshift.com":{"auth":"dXNlcjpwYXNzd29yZAo=","email":"r@r.com"}}}`
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte(testPullSecretVal)}}
}

func getMockClusterTemplate(name, namespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string]string{"ManagedCluster": "foobar"}}
}

func getMockNodeTemplate(name, namespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string]string{"BareMetalhost": "foobar"}}
}

func getMockExtraManifest(name, namespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string]string{"foo": "bar"}}
}

func getMockSNOClusterInstance(clusterName, clusterNamespace, pullSecret, bmcCredentialsName, clusterImageSetName, extraManifest, clusterTemplateRef, nodeTemplateRef string) *v1alpha1.ClusterInstance {
	installConfigOverrides := "{\"networking\":{\"networkType\":\"OVNKubernetes\"},\"cpuPartitioningMode\":\"AllNodes\"}"
	installerArgs := "[\"--append-karg\", \"nameserver=8.8.8.8\", \"-n\"]"
	nodeIgnitionConfigOverride := "{\"ignition\": {\"version\": \"3.1.0\"}, \"storage\": {\"files\": [{\"path\": \"/etc/containers/registries.conf\", \"overwrite\": true, \"contents\": {\"source\": \"data:text/plain;base64,aGVsbG8gZnJvbSB6dHAgcG9saWN5IGdlbmVyYXRvcg==\"}}]}}"

	return &v1alpha1.ClusterInstance{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: clusterNamespace,
		},
		Spec: v1alpha1.ClusterInstanceSpec{
			ClusterName:            clusterName,
			PullSecretRef:          &corev1.LocalObjectReference{Name: pullSecret},
			ClusterImageSetNameRef: clusterImageSetName,
			SSHPublicKey:           "test-ssh",
			BaseDomain:             "abcd",
			ClusterType:            v1alpha1.ClusterTypeSNO,
			ExtraManifestsRefs:     []corev1.LocalObjectReference{{Name: extraManifest}},
			TemplateRefs: []v1alpha1.TemplateRef{
				{Name: clusterTemplateRef, Namespace: clusterNamespace}},
			InstallConfigOverrides: installConfigOverrides,
			Nodes: []v1alpha1.NodeSpec{{
				Role:                   "master",
				BmcAddress:             "1:2:3:4",
				BmcCredentialsName:     v1alpha1.BmcCredentialsName{Name: bmcCredentialsName},
				InstallerArgs:          installerArgs,
				IgnitionConfigOverride: nodeIgnitionConfigOverride,
				TemplateRefs: []v1alpha1.TemplateRef{
					{Name: nodeTemplateRef, Namespace: clusterNamespace}}}}},
	}
}

func SetupTestPrereqs(ctx context.Context, c client.Client, bmc, pullSecret *corev1.Secret,
	clusterImageSet *hivev1.ClusterImageSet, clusterTemplate, nodeTemplate, extraManifest *corev1.ConfigMap) {
	Expect(c.Create(ctx, bmc)).To(Succeed())
	Expect(c.Create(ctx, clusterImageSet)).To(Succeed())
	Expect(c.Create(ctx, pullSecret)).To(Succeed())
	Expect(c.Create(ctx, clusterTemplate)).To(Succeed())
	Expect(c.Create(ctx, nodeTemplate)).To(Succeed())
	Expect(c.Create(ctx, extraManifest)).To(Succeed())
}

var _ = Describe("validateSiteConfig", func() {
	var (
		bmcCredentialsName                           = "bmh-secret"
		bmc, pullSecret                              *corev1.Secret
		c                                            client.Client
		ctx                                          = context.Background()
		clusterName                                  = "test-cluster"
		clusterNamespace                             = "test-cluster"
		clusterImageSetName                          = "testimage:foobar"
		clusterImageSet                              *hivev1.ClusterImageSet
		clusterTemplateRef                           = "cluster-template-ref"
		clusterTemplate, nodeTemplate, extraManifest *corev1.ConfigMap
		extraManifestName                            = "extra-manifest"
		nodeTemplateRef                              = "node-template-ref"
		clusterInstance                              *v1alpha1.ClusterInstance
	)

	BeforeEach(func() {
		c = fakeclient.NewClientBuilder().
			WithScheme(scheme.Scheme).
			WithStatusSubresource(&v1alpha1.ClusterInstance{}).
			Build()

		bmc = getMockBmcSecret(bmcCredentialsName, clusterNamespace)
		clusterImageSet = getMockClusterImageSet(clusterImageSetName)
		pullSecret = getMockPullSecret("pull-secret", clusterNamespace)
		clusterTemplate = getMockClusterTemplate(clusterTemplateRef, clusterNamespace)
		nodeTemplate = getMockNodeTemplate(nodeTemplateRef, clusterNamespace)
		extraManifest = getMockExtraManifest(extraManifestName, clusterNamespace)

		SetupTestPrereqs(ctx, c, bmc, pullSecret, clusterImageSet, clusterTemplate, nodeTemplate, extraManifest)

		clusterInstance = getMockSNOClusterInstance(clusterName, clusterNamespace, pullSecret.Name, bmcCredentialsName, clusterImageSetName, extraManifestName, clusterTemplateRef, nodeTemplateRef)

	})

	It("successfully validates a well-defined SiteConfig", func() {
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := validateClusterInstance(ctx, c, clusterInstance)
		Expect(err).ToNot(HaveOccurred())
	})

	It("fails validation when cluster name is not defined", func() {
		clusterInstance.Spec.ClusterName = ""
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := validateClusterInstance(ctx, c, clusterInstance)
		Expect(err).To(MatchError(ContainSubstring("missing cluster name")))
	})

	It("fails validation when clusterImageSetName reference is not defined", func() {
		clusterInstance.Spec.ClusterImageSetNameRef = ""
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := validateClusterInstance(ctx, c, clusterInstance)
		Expect(err).To(MatchError(ContainSubstring("clusterImageSetNameRef cannot be empty")))
	})

	It("fails validation when clusterImageSet resource does not exist", func() {
		clusterInstance.Spec.ClusterImageSetNameRef = "does-not-exist"
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := validateClusterInstance(ctx, c, clusterInstance)
		Expect(err).To(MatchError(ContainSubstring("encountered error validating ClusterImageSetNameRef")))
	})

	It("fails validation when cluster-level template refs are not defined", func() {
		clusterInstance.Spec.TemplateRefs = []v1alpha1.TemplateRef{}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := validateClusterInstance(ctx, c, clusterInstance)
		Expect(err).To(MatchError("missing cluster-level TemplateRefs"))
	})

	It("fails validation due to missing pull secret", func() {
		clusterInstance.Spec.PullSecretRef = &corev1.LocalObjectReference{Name: "does-not-exist"}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := validateClusterInstance(ctx, c, clusterInstance)
		Expect(err).To(MatchError(ContainSubstring("failed to validate Pull Secret")))
	})

	It("fails validation due to invalid cluster-level installConfigOverrides JSON-formatted strings", func() {
		clusterInstance.Spec.InstallConfigOverrides = "foobar"
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())
		err := validateClusterInstance(ctx, c, clusterInstance)
		Expect(err).To(MatchError(ContainSubstring("installConfigOverrides is not a valid JSON-formatted string")))
	})

	It("fails validation due to invalid cluster-level ignitionConfigOverride JSON-formatted strings", func() {
		clusterInstance.Spec.IgnitionConfigOverride = "{foo:bar}"
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())
		err := validateClusterInstance(ctx, c, clusterInstance)
		Expect(err).To(MatchError(ContainSubstring("cluster-level ignitionConfigOverride is not a valid JSON-formatted string")))
	})

	It("fails validation when an ExtraManifest reference does not exist", func() {
		clusterInstance.Spec.ExtraManifestsRefs = []corev1.LocalObjectReference{{Name: "does-not-exist"}}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())
		err := validateClusterInstance(ctx, c, clusterInstance)
		Expect(err).To(MatchError(ContainSubstring("failed to retrieve ExtraManifest")))
	})

	It("fails validation when node-level template refs are not defined", func() {
		clusterInstance.Spec.Nodes[0].TemplateRefs = []v1alpha1.TemplateRef{}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := validateClusterInstance(ctx, c, clusterInstance)
		Expect(err).To(MatchError(ContainSubstring("missing node-level template refs")))
	})

	It("fails validation when a node-level template ref does not exist", func() {
		clusterInstance.Spec.Nodes[0].TemplateRefs = []v1alpha1.TemplateRef{
			{Name: "does-not-exist", Namespace: clusterName}}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := validateClusterInstance(ctx, c, clusterInstance)
		Expect(err).To(MatchError(ContainSubstring("failed to validate node-level TemplateRef")))
	})

	It("fails validation due to missing BMC credential secret", func() {
		clusterInstance.Spec.Nodes[0].BmcCredentialsName = v1alpha1.BmcCredentialsName{Name: "does-not-exist"}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := validateClusterInstance(ctx, c, clusterInstance)
		Expect(err).To(MatchError(ContainSubstring("failed to validate BMC credentials")))
	})

	It("fails validation due to invalid node-level installerArgs JSON-formatted strings", func() {
		clusterInstance.Spec.Nodes[0].InstallerArgs = "{foo:bar}"
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := validateClusterInstance(ctx, c, clusterInstance)
		Expect(err).To(MatchError(ContainSubstring("installerArgs is not a valid JSON-formatted string")))
	})

	It("fails validation due to invalid node-level ignitionConfigOverride JSON-formatted strings", func() {
		clusterInstance.Spec.Nodes[0].IgnitionConfigOverride = "{foo:bar}"
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())
		err := validateClusterInstance(ctx, c, clusterInstance)

		Expect(err).To(MatchError(ContainSubstring("ignitionConfigOverride is not a valid JSON-formatted string")))
	})

	It("fails validation when no control-plane agents are configured", func() {
		clusterInstance.Spec.Nodes[0].Role = "worker"
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := validateClusterInstance(ctx, c, clusterInstance)
		Expect(err).To(MatchError(ContainSubstring("at least 1 ControlPlane agent is required")))
	})

	It("fails validation when worker agents are defined for SNO-based cluster-type", func() {
		clusterInstance.Spec.Nodes = []v1alpha1.NodeSpec{
			{
				Role:               "master",
				BmcAddress:         "1:2:3:4",
				BmcCredentialsName: v1alpha1.BmcCredentialsName{Name: bmcCredentialsName},
				TemplateRefs: []v1alpha1.TemplateRef{
					{Name: nodeTemplateRef, Namespace: clusterName}}},
			{
				Role:               "worker",
				BmcAddress:         "4:5:6:7",
				BmcCredentialsName: v1alpha1.BmcCredentialsName{Name: bmcCredentialsName},
				TemplateRefs: []v1alpha1.TemplateRef{
					{Name: nodeTemplateRef, Namespace: clusterName}}}}
		Expect(c.Create(ctx, clusterInstance)).To(Succeed())

		err := validateClusterInstance(ctx, c, clusterInstance)
		Expect(err).To(MatchError(ContainSubstring("sno cluster-type requires 1 control-plane agent and no worker agents")))
	})
})
